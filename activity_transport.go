package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type capabilityCacheEntry struct {
	Status int             `json:"status,omitempty"`
	Code   string          `json:"code,omitempty"`
	Value  RunCapabilities `json:"value"`
	Until  int64           `json:"until"`
}

// Observation/encoding only. Approval inspect, decision and native consumption
// deliberately continue to call runCapabilities for a fresh authorization.
func cachedRunCapabilities(ctx context.Context, s Store, api API, p Pair) (RunCapabilities, error) {
	unlock, err := lockFile(ctx, filepath.Join(s.Dir, ".capabilities-lock"))
	if err != nil {
		return RunCapabilities{}, err
	}
	defer unlock()
	cache := map[string]capabilityCacheEntry{}
	if err = s.readJSON("capabilities-cache-v2.json", &cache); err != nil && !errors.Is(err, os.ErrNotExist) {
		return RunCapabilities{}, err
	}
	now := time.Now().Unix()
	key := digest(p.APIURL + "\x00" + p.PairID + "\x00" + p.InstallationID + "\x00" + p.SendToken)
	for id, v := range cache {
		if v.Until <= now {
			delete(cache, id)
		}
	}
	if entry, ok := cache[key]; ok {
		if entry.Status != 0 {
			return RunCapabilities{}, &APIError{Status: entry.Status, Code: entry.Code, RetryAfter: time.Duration(entry.Until-now) * time.Second}
		}
		return entry.Value, nil
	}
	c, err := runCapabilities(ctx, api, p)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 429 && len(cache) < 128 {
			delay := ae.RetryAfter
			if delay < time.Second {
				delay = time.Second
			}
			cache[key] = capabilityCacheEntry{Until: time.Now().Add(delay).Unix(), Status: 429, Code: safeCode(ae.Code)}
			if saveErr := s.writeJSON("capabilities-cache-v2.json", cache); saveErr != nil {
				return c, saveErr
			}
		}
		return c, err
	}
	until := now + 60
	if c.V == 2 && c.ExpiresAt < until {
		until = c.ExpiresAt
	}
	if len(cache) >= 128 {
		return c, nil
	} // No unbounded cache growth across retired pairs.
	cache[key] = capabilityCacheEntry{Value: c, Until: until}
	return c, s.writeJSON("capabilities-cache-v2.json", cache)
}

// Claim before network I/O while holding the process-shared store lock. A
// crashed observer releases automatically after this short lease. Retry-After
// and completion are applied under the same lock; stale snapshots cannot send.
func claimStateSend(ctx context.Context, s Store, pair string) (func(time.Duration), error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	db, err := loadRuns(s)
	if err != nil {
		unlock()
		return nil, err
	}
	now := time.Now().Unix()
	if db.NextState[pair] > now {
		unlock()
		return nil, nil
	}
	lease := now + int64((apiRequestTimeout+5*time.Second)/time.Second)
	db.NextState[pair] = lease
	err = saveRuns(s, db)
	unlock()
	if err != nil {
		return nil, err
	}
	return func(delay time.Duration) {
		settle, cancel := settlementContext(ctx)
		defer cancel()
		unlock, err := s.lock(settle)
		if err != nil {
			return
		}
		defer unlock()
		db, err := loadRuns(s)
		if err != nil || db.NextState[pair] != lease {
			return
		}
		db.NextState[pair] = time.Now().Add(delay).Unix()
		_ = saveRuns(s, db)
	}, nil
}

// Advance a durable per-phase Pair cursor before I/O. Failed capability requests
// cannot monopolize every subsequent short flush, and one busy Pair cannot take
// all four positions while another Pair has a critical reminder waiting.
func scheduleRunIntents(db *RunStore, states bool) []string {
	phase := 0
	if states {
		phase = 1
	}
	groups := map[string][]string{}
	now := time.Now().Unix()
	for key, intent := range db.Intents {
		if (intent.Kind == "state") != states || states && db.NextState[intent.PairID] > now {
			continue
		}
		groups[intent.PairID] = append(groups[intent.PairID], key)
	}
	ids := make([]string, 0, len(groups))
	for id, keys := range groups {
		ids = append(ids, id)
		sort.Slice(keys, func(i, j int) bool {
			a, b := db.Intents[keys[i]], db.Intents[keys[j]]
			if a.Payload.OccurredAt == b.Payload.OccurredAt {
				return keys[i] < keys[j]
			}
			return a.Payload.OccurredAt < b.Payload.OccurredAt
		})
	}
	sort.Strings(ids)
	start := sort.SearchStrings(ids, db.LastPair[phase])
	for start < len(ids) && ids[start] <= db.LastPair[phase] {
		start++
	}
	ids = append(ids[start:], ids[:start]...)
	result := []string{}
	for len(result) < 4 {
		added := false
		for _, id := range ids {
			if len(groups[id]) == 0 {
				continue
			}
			result = append(result, groups[id][0])
			groups[id] = groups[id][1:]
			db.LastPair[phase] = id
			added = true
			if len(result) == 4 {
				break
			}
		}
		if !added {
			break
		}
	}
	return result
}
