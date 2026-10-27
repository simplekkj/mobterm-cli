package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type retiredPair struct {
	PairRef         string `json:"pair_ref"`
	Status          string `json:"status"`
	RetiredAt       int64  `json:"retired_at"`
	OriginRef       string `json:"origin_ref,omitempty"`
	InstallationRef string `json:"installation_ref,omitempty"`
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
	// Persist the marker before the credentials commit; a retry resumes deletion.
	if err = s.recordRetirementLocked(id, reason, p.APIURL, p.InstallationID); err != nil {
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

// Retirement markers are never evicted: a late setup can outlive any normal
// command timeout. Bound new identities explicitly instead of reopening old IDs.
const maxRetiredPairs = 4096

type RetirePacket struct {
	V              int    `json:"v"`
	APIURL         string `json:"api_url"`
	PairID         string `json:"pair_id"`
	InstallationID string `json:"installation_id"`
}

func (p RetirePacket) validate() error {
	u, err := url.Parse(p.APIURL)
	if p.V != 1 || !opaqueID.MatchString(p.PairID) || !opaqueID.MatchString(p.InstallationID) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid retirement identity or HTTPS origin")
	}
	return nil
}
func retireLocalPair(ctx context.Context, s Store, in io.Reader) (map[string]any, error) {
	var p RetirePacket
	if err := readFrameContext(ctx, in, &p); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	// Freeze the expected record before acquiring the write lock; a changed
	// credential/status cannot be silently adopted by this management operation.
	expected, err := s.pairs()
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var identity struct {
		V  int    `json:"v"`
		ID string `json:"installation_id"`
	}
	if err = s.readJSON("installation.json", &identity); err != nil {
		return nil, err
	}
	if identity.V != 1 || identity.ID != p.InstallationID {
		return nil, errors.New("installation_changed")
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	old, exists := pairs[p.PairID]
	before, expectedExists := expected[p.PairID]
	if exists != expectedExists || exists && old != before {
		return nil, errors.New("pair_operation_superseded")
	}
	if exists && (old.PairID != p.PairID || old.InstallationID != p.InstallationID || strings.TrimSuffix(old.APIURL, "/") != strings.TrimSuffix(p.APIURL, "/")) {
		return nil, errors.New("pair_identity_changed")
	}
	if err = s.recordRetirementLocked(p.PairID, "local_retired", p.APIURL, p.InstallationID); err != nil {
		return nil, err
	}
	if exists {
		delete(pairs, p.PairID)
		if err = s.writeJSON("pairs.json", pairs); err != nil {
			return nil, err
		}
	}
	persisted, err := s.pairs()
	if err != nil {
		return nil, err
	}
	if _, remains := persisted[p.PairID]; remains {
		return nil, errors.New("pair_retirement_unconfirmed")
	}
	status := "local_absent"
	if exists {
		status = "local_retired"
	}
	return map[string]any{"v": 1, "ok": true, "api_url": p.APIURL, "pair_id": p.PairID, "installation_id": p.InstallationID, "status": status}, nil
}

// Caller holds the Store lock. Markers contain no credentials and are never
// evicted: an interrupted setup may resume after arbitrarily many later pairs.
func (s Store) recordRetirementLocked(id, reason, apiURL, installationID string) error {
	var history []retiredPair
	if err := s.readJSON("pair-history.json", &history); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ref, origin, installation := digest(id), digest(strings.TrimSuffix(apiURL, "/")), digest(installationID)
	for _, h := range history {
		if h.PairRef == ref {
			if h.OriginRef != "" && (h.OriginRef != origin || h.InstallationRef != installation) {
				return errors.New("retirement_identity_changed")
			}
			return nil
		}
	}
	if len(history) >= maxRetiredPairs {
		return errors.New("retired_pair_capacity_reached")
	}
	history = append(history, retiredPair{PairRef: ref, Status: reason, RetiredAt: time.Now().Unix(), OriginRef: origin, InstallationRef: installation})
	return s.writeJSON("pair-history.json", history)
}
