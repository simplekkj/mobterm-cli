package main

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"
)

type deliveryRetriesKey struct{}
type deliveryRetries struct {
	mu       sync.Mutex
	attempts map[string]int
}

// Only completion/test/explicit flush commands opt in. Share the attempt limit
// across fresh delivery and later outbox recovery in the same invocation.
func withDeliveryRetries(ctx context.Context) context.Context {
	return context.WithValue(ctx, deliveryRetriesKey{}, &deliveryRetries{attempts: map[string]int{}})
}
func deliveryAttemptAllowed(ctx context.Context, key string, reserve bool) bool {
	r, ok := ctx.Value(deliveryRetriesKey{}).(*deliveryRetries)
	if !ok {
		return reserve
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attempts[key] >= 2 {
		return false
	}
	if reserve {
		r.attempts[key]++
	}
	return true
}

func deliveryRetryDelay(posts int, retryAfter time.Duration) time.Duration {
	base := 15 * time.Second * time.Duration(1<<min(max(posts-1, 0), 5))
	if base > 4*time.Minute {
		base = 4 * time.Minute
	}
	// Add up to 25% jitter, including at the cap; never shorten Retry-After.
	if n, err := rand.Int(rand.Reader, big.NewInt(int64(base/4)+1)); err == nil {
		base += time.Duration(n.Int64())
	}
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	if retryAfter > base {
		base = retryAfter
	}
	return base
}

func deliverOne(ctx context.Context, s Store, a API, d delivery) (map[string]any, error) {
	for {
		result, err := deliverAttempt(ctx, s, a, d)
		if err == nil || result["state"] != "pending" || !deliveryAttemptAllowed(ctx, deliveryKey(d.PairID, d.EventID), false) {
			return result, err
		}
		next, ok := result["next_attempt_at"].(int64)
		end, bounded := ctx.Deadline()
		// Leave a useful request window and persistence time; never wait unboundedly.
		if !ok || !bounded || next == 0 || time.Unix(next, 0).Add(5*time.Second).After(end) {
			return result, err
		}
		delay := time.Until(time.Unix(next, 0))
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}

// Capability negotiation is part of completion delivery too. Wait outside the
// capability-cache lock, and leave explicit approval authorization unchanged.
func completionCapabilities(ctx context.Context, s Store, a API, p Pair) (RunCapabilities, error) {
	for attempt := 0; ; attempt++ {
		value, err := cachedRunCapabilities(ctx, s, a, p)
		if err == nil || attempt == 1 || ctx.Value(deliveryRetriesKey{}) == nil {
			return value, err
		}
		var apiErr *APIError
		var connection *apiConnectionError
		var response *apiResponseError
		if errors.As(err, &apiErr) {
			if apiErr.Status != 429 && apiErr.Status < 500 {
				return value, err
			}
		} else if !errors.As(err, &connection) && !errors.As(err, &response) && err.Error() != "invalid API response" {
			return value, err
		}
		var delay time.Duration
		if apiErr != nil {
			delay = apiErr.RetryAfter
		}
		waited, waitErr := waitDeliveryRetry(ctx, deliveryRetryDelay(1, delay))
		if waitErr != nil {
			return value, waitErr
		}
		if !waited {
			return value, err
		}
	}
}
func waitDeliveryRetry(ctx context.Context, delay time.Duration) (bool, error) {
	end, ok := ctx.Deadline()
	if !ok || time.Now().Add(delay+5*time.Second).After(end) {
		return false, nil
	}
	timer := time.NewTimer(max(delay, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return true, nil
	}
}
