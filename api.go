package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct{ Client *http.Client }

// Preserve cancellation/timeout evidence without exposing network error text.
// The displayed legacy error remains unchanged for other API callers.
type apiConnectionError struct{ cause error }

func (e *apiConnectionError) Error() string { return "API unavailable" }
func (e *apiConnectionError) Unwrap() error { return e.cause }

type APIError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("API HTTP %d (%s)", e.Status, e.Code) }
func newAPI() API {
	return API{&http.Client{Timeout: 4 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect refused") }}}
}
func (a API) request(ctx context.Context, p Pair, method, path string, body any) (map[string]any, error) {
	// Keep the last second of a finite command budget for durable result commits.
	deadline := time.Now().Add(4 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Add(-time.Second).Before(deadline) {
		deadline = end.Add(-time.Second)
	}
	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ctx = requestCtx

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(b) > 8192 {
			return nil, errors.New("request exceeds limit")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.APIURL, "/")+path, reader)
	if err != nil {
		return nil, errors.New("invalid API request")
	}
	req.Header.Set("Authorization", "Bearer "+p.SendToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, &apiConnectionError{cause: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(b) > 16384 {
		return nil, errors.New("invalid API response")
	}
	v := map[string]any{}
	if json.Unmarshal(b, &v) != nil {
		return nil, errors.New("invalid API response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, _ := v["error"].(string)
		if len(code) > 80 || strings.ContainsAny(code, "\r\n") {
			code = "request_failed"
		}
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return v, &APIError{Status: resp.StatusCode, Code: code, RetryAfter: retryAfter}
	}
	return v, nil
}
func (a API) send(ctx context.Context, p Pair, e Envelope) (map[string]any, error) {
	var v map[string]any
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		v, err = a.request(ctx, p, "POST", "/v1/pairs/"+p.PairID+"/events", e)
		state, _ := v["state"].(string)
		if v["event_id"] == e.EventID && (terminalDelivery(state) || v["outbox_handoff"] == true && (state == "accepted" || state == "sending" || state == "retryable_failed")) {
			return v, nil
		}
		if err == nil {
			return v, nil
		}
		var ae *APIError
		if errors.As(err, &ae) && ae.Status != 429 && ae.Status < 500 {
			return v, err
		}
		if ctx.Err() != nil {
			return v, err
		}
		if attempt == 0 {
			delay := 100 * time.Millisecond
			if ae != nil && ae.RetryAfter > delay {
				delay = ae.RetryAfter
			}
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < delay+time.Second {
				return v, err
			}
			select {
			case <-ctx.Done():
				return v, err
			case <-time.After(delay):
			}
		}
	}
	return v, err
}
func setup(ctx context.Context, s Store, a API, in io.Reader) (map[string]any, error) {
	var p SetupPacket
	if err := readFrameContext(ctx, in, &p); err != nil {
		return nil, pairingFailure("setup_frame", "invalid_frame", err)
	}
	if err := p.validate(); err != nil {
		return nil, pairingFailure("setup_frame", "invalid_frame", err)
	}
	id, err := s.installation()
	if err != nil {
		return nil, pairingFailure("installation", "local_config_failed", err)
	}
	if p.InstallationID != id {
		return nil, pairingFailure("installation", "installation_changed", errors.New("installation identity changed"))
	}
	pair := Pair{p, "pending"}
	existing, e := s.pairs()
	if e != nil {
		return nil, pairingFailure("local_save", "local_config_failed", e)
	}
	if old, ok := existing[p.PairID]; ok {
		if old.SetupPacket != p {
			return nil, pairingFailure("local_save", "installation_changed", errors.New("pair identity conflict"))
		}
		pair = old
	}

	if err = s.savePair(ctx, pair); err != nil {
		return nil, pairingFailure("local_save", "local_save_failed", err)
	}
	v, err := a.request(ctx, pair, "POST", "/v1/pairs/"+p.PairID+"/claim", map[string]string{"installation_id": id, "claim_id": p.PairID})
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 410 && (ae.Code == "pair_expired" || ae.Code == "pair_revoked") {
			if retireErr := s.retirePairIfCurrent(ctx, p.PairID, strings.TrimPrefix(ae.Code, "pair_"), false, &pair); retireErr != nil {
				return nil, pairingFailure("local_save", "local_save_failed", retireErr)
			}
			return nil, pairingFailure("claim", "unknown", err)
		}
		// A lost claim response can leave a remotely active, locally pending pair.
		remote, queryErr := a.request(ctx, pair, "GET", "/v1/pairs/"+p.PairID, nil)
		if errors.As(queryErr, &ae) && ae.Status == 410 && (ae.Code == "pair_expired" || ae.Code == "pair_revoked") {
			if retireErr := s.retirePairIfCurrent(ctx, p.PairID, strings.TrimPrefix(ae.Code, "pair_"), false, &pair); retireErr != nil {
				return nil, pairingFailure("local_save", "local_save_failed", retireErr)
			}
		}
		if queryErr != nil || remote["status"] != "active" || remote["installation_id"] != id {
			return nil, pairingFailure("claim", "unknown", err)
		}
		v = remote
	}
	if v["status"] != "active" {
		return nil, pairingFailure("claim", "pair_status_failed", errors.New("API has not activated pair"))
	}
	if err = s.transitionPair(ctx, pair, "active"); err != nil {
		return nil, pairingFailure("local_save", "local_save_failed", err)
	}
	pair.Status = "active"
	// Read the durable local value before acknowledging the SSH machine call.
	saved, err := s.pairs()
	if err != nil || saved[p.PairID] != pair {
		return nil, pairingFailure("local_readback", "local_save_failed", errors.New("local pair readback failed"))
	}
	return map[string]any{"v": 1, "pair_id": p.PairID, "installation_id": id, "status": "active", "local_saved": true}, nil
}

// Valid excessively long numeric values saturate at a day: all CLI events expire
// in an hour, so this terminates safely instead of overflowing into an early retry.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if strings.Trim(value, "0123456789") == "" {
		n, e := strconv.ParseUint(value, 10, 64)
		if e != nil || n > 86400 {
			return 24 * time.Hour
		}
		return time.Duration(n) * time.Second
	}
	if date, e := http.ParseTime(value); e == nil && date.After(now) {
		d := date.Sub(now)
		if d > 24*time.Hour {
			return 24 * time.Hour
		}
		return d
	}
	return 0
}
