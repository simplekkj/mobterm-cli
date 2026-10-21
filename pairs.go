package main

import (
	"context"
	"errors"
	"os"
	"time"
)

type retiredPair struct {
	PairRef   string `json:"pair_ref"`
	Status    string `json:"status"`
	RetiredAt int64  `json:"retired_at"`
}

func (s Store) retirePair(ctx context.Context, id, reason string, pendingOnly bool) error {
	return s.retirePairIfCurrent(ctx, id, reason, pendingOnly, nil)
}
func (s Store) retirePairIfCurrent(ctx context.Context, id, reason string, pendingOnly bool, expected *Pair) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	p, ok := pairs[id]
	if !ok {
		return errors.New("pair_not_found")
	}
	if expected != nil && p != *expected {
		return errors.New("pair_operation_superseded")
	}
	if pendingOnly && p.Status != "pending" && p.Status != "pending_review" {
		return errors.New("only_pending_attempt_can_be_abandoned")
	}
	var history []retiredPair
	err = s.readJSON("pair-history.json", &history)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ref := digest(id)
	found := false
	for _, h := range history {
		if h.PairRef == ref {
			found = true
		}
	}
	if !found {
		history = append(history, retiredPair{ref, reason, time.Now().Unix()})
	}
	if len(history) > 128 {
		history = history[len(history)-128:]
	}
	// History contains no credentials; interruption may leave a harmless summary,
	// while active credentials are removed only by the atomic pairs commit below.
	if err = s.writeJSON("pair-history.json", history); err != nil {
		return err
	}
	delete(pairs, id)
	return s.writeJSON("pairs.json", pairs)
}
func reconcilePairs(ctx context.Context, s Store, a API, id string) (map[string]any, error) {
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	results := []map[string]any{}
	var last error
	for pid, p := range pairs {
		if id != "" && id != pid {
			continue
		}
		remote, e := a.request(ctx, p, "GET", "/v1/pairs/"+pid, nil)
		state, _ := remote["status"].(string)
		var ae *APIError
		confirmedInactive := e == nil && (state == "revoked" || state == "expired")
		if errors.As(e, &ae) && ae.Status == 410 && (ae.Code == "pair_revoked" || ae.Code == "pair_expired") {
			confirmedInactive = true
			state = "revoked"
			if ae.Code == "pair_expired" {
				state = "expired"
			}
		}
		if confirmedInactive {
			e = s.retirePairIfCurrent(ctx, pid, state, false, &p)
		} else if e == nil && state == "active" && remote["installation_id"] == p.InstallationID {
			e = s.transitionPair(ctx, p, "active")
		} else if e == nil && state == "pending" {
			state = p.Status
		} else {
			state = "credentials_review_required"
			original := p
			if p.Status == "pending" || p.Status == "pending_review" {
				p.Status = "pending_review"
			} else if errors.As(e, &ae) && (ae.Status == 401 || ae.Status == 403 || ae.Status == 404) || e == nil {
				p.Status = "review_required"
			}
			if saveErr := s.transitionPair(ctx, original, p.Status); saveErr != nil {
				e = saveErr
			}
		}
		v := map[string]any{"pair_id": pid, "status": state}
		if e != nil {
			last = e
			v["error"] = "pair_status_unconfirmed"
		}
		results = append(results, v)
	}
	return map[string]any{"v": 1, "pairs": results}, last
}
