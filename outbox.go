package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const outboxRetention = int64(7 * 86400)
const outboxByteLimit = 80 * 1024 * 1024
const outboxShardLimit = 2 * 1024 * 1024

type delivery struct {
	PairID      string    `json:"pair_id"`
	EventID     string    `json:"event_id"`
	Kind        string    `json:"kind"`
	LocatorHash string    `json:"locator_hash"`
	Created     int64     `json:"created"`
	Expires     int64     `json:"expires_at"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	Next        int64     `json:"next_attempt_at,omitempty"`
	Posts       int       `json:"posts"`
	Owner       string    `json:"owner,omitempty"`
	Lease       int64     `json:"lease_until,omitempty"`
	Envelope    *Envelope `json:"envelope,omitempty"`
}
type outboxCount struct {
	Records    int `json:"records"`
	Pending    int `json:"pending"`
	Bytes      int `json:"bytes"`
	HandedOff  int `json:"handed_off"`
	Terminated int `json:"terminated"`
}
type outboxIndex struct {
	V        int                    `json:"v"`
	Migrated bool                   `json:"migrated"`
	Total    outboxCount            `json:"total"`
	Pairs    map[string]outboxCount `json:"pairs"`
	Shards   map[string]int         `json:"shards"`
	Legacy   map[string]string      `json:"legacy"`
}
type outboxShard struct {
	V       int                 `json:"v"`
	Records map[string]delivery `json:"records"`
}
type outboxTransaction struct {
	V     int         `json:"v"`
	Shard string      `json:"shard"`
	Data  outboxShard `json:"data"`
	Index outboxIndex `json:"index"`
}

func deliveryKey(pair, event string) string { return digest(pair + ":" + event) }
func countDelivery(d delivery) outboxCount {
	c := outboxCount{Records: 1}
	if d.Envelope != nil {
		b, _ := json.Marshal(d.Envelope)
		c.Bytes = len(b)
	}
	if d.State == "pending" {
		c.Pending = 1
	} else if d.State == "handed_off" {
		c.HandedOff = 1
	} else {
		c.Terminated = 1
	}
	return c
}
func addCounts(a, b outboxCount, sign int) outboxCount {
	return outboxCount{a.Records + sign*b.Records, a.Pending + sign*b.Pending, a.Bytes + sign*b.Bytes, a.HandedOff + sign*b.HandedOff, a.Terminated + sign*b.Terminated}
}
func (s Store) outboxRead(name string, limit int64, v any) error {
	b, e := secureRead(filepath.Join(s.Dir, "outbox-v1", name), limit)
	if e != nil {
		return e
	}
	if json.Unmarshal(b, v) != nil {
		return errors.New("outbox_corrupt")
	}
	return nil
}
func (s Store) outboxWrite(name string, v any, limit int) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > limit {
		return errors.New("outbox_shard_capacity")
	}
	return atomicWrite(filepath.Join(s.Dir, "outbox-v1", name), b, 0600)
}
func (s Store) outboxLoad(recover bool) (outboxIndex, error) {
	var tx outboxTransaction
	e := s.outboxRead("transaction.json", 36*1024*1024, &tx)
	if e == nil {
		if !recover {
			return outboxIndex{}, errors.New("outbox_recovery_required")
		}
		if tx.V != 1 || len(tx.Shard) != 2 || strings.Trim(tx.Shard, "0123456789abcdef") != "" || tx.Data.V != 1 || tx.Index.V != 1 {
			return outboxIndex{}, errors.New("outbox_corrupt")
		}
		if e = s.outboxWrite(tx.Shard+".json", tx.Data, outboxShardLimit); e != nil {
			return outboxIndex{}, e
		}
		if e = s.outboxWrite("index.json", tx.Index, 32*1024*1024); e != nil {
			return outboxIndex{}, e
		}
		if e = os.Remove(filepath.Join(s.Dir, "outbox-v1", "transaction.json")); e != nil {
			return outboxIndex{}, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return outboxIndex{}, e
	}
	idx := outboxIndex{V: 1, Pairs: map[string]outboxCount{}, Shards: map[string]int{}, Legacy: map[string]string{}}
	e = s.outboxRead("index.json", 32*1024*1024, &idx)
	if errors.Is(e, os.ErrNotExist) {
		entries, readErr := os.ReadDir(filepath.Join(s.Dir, "outbox-v1"))
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return idx, readErr
		}
		for _, entry := range entries {
			name := entry.Name()
			if len(name) == 7 && strings.HasSuffix(name, ".json") {
				return idx, errors.New("outbox_index_missing")
			}
		}
		e = nil
	} else if e == nil && (idx.V != 1 || idx.Pairs == nil || idx.Shards == nil || idx.Legacy == nil || len(idx.Legacy) > 512) {
		e = errors.New("outbox_corrupt")
	}
	return idx, e
}
func (s Store) loadShard(key string) (outboxShard, error) {
	sh := outboxShard{V: 1, Records: map[string]delivery{}}
	err := s.outboxRead(key[:2]+".json", outboxShardLimit, &sh)
	if errors.Is(err, os.ErrNotExist) {
		idx, e := s.outboxLoad(false)
		if e != nil {
			return sh, e
		}
		if idx.Shards[key[:2]] > 0 {
			return sh, errors.New("outbox_shard_missing")
		}
		return sh, nil
	}
	if err == nil && (sh.V != 1 || sh.Records == nil || len(sh.Records) > 4096) {
		err = errors.New("outbox_corrupt")
	}
	if err == nil {
		for k, d := range sh.Records {
			if !opaqueID.MatchString(d.PairID) || !opaqueID.MatchString(d.EventID) || k != deliveryKey(d.PairID, d.EventID) || k[:2] != key[:2] || d.Created <= 0 || d.Expires <= 0 || d.Posts < 0 || d.Posts > 12 {
				return sh, errors.New("outbox_corrupt")
			}
			if d.State != "pending" && d.State != "handed_off" && d.State != "legacy_unknown" && !terminalDelivery(d.State) {
				return sh, errors.New("outbox_corrupt")
			}
			if d.State == "pending" && d.Envelope == nil {
				return sh, errors.New("outbox_corrupt")
			}
			if d.Envelope != nil && (d.Envelope.PairID != d.PairID || d.Envelope.EventID != d.EventID || d.Envelope.Kind != d.Kind) {
				return sh, errors.New("outbox_corrupt")
			}
		}
	}
	return sh, err
}
func (s Store) saveShard(idx outboxIndex, key string, old, sh outboxShard) error {
	for key, d := range old.Records {
		delete(idx.Legacy, key)
		c := countDelivery(d)
		idx.Total = addCounts(idx.Total, c, -1)
		idx.Pairs[d.PairID] = addCounts(idx.Pairs[d.PairID], c, -1)
	}
	for key, d := range sh.Records {
		if d.State == "legacy_unknown" {
			idx.Legacy[key] = d.PairID
		}
		c := countDelivery(d)
		idx.Total = addCounts(idx.Total, c, 1)
		idx.Pairs[d.PairID] = addCounts(idx.Pairs[d.PairID], c, 1)
	}
	for p, c := range idx.Pairs {
		if c.Records == 0 {
			delete(idx.Pairs, p)
		} else if c.Pending > 512 || c.Records > 4096 {
			return errors.New("outbox_pair_capacity")
		}
	}
	if idx.Total.Pending > 16384 || idx.Total.Records > 131072 || idx.Total.Bytes > outboxByteLimit {
		return errors.New("outbox_installation_capacity")
	}
	idx.Shards[key[:2]] = len(sh.Records)
	b, _ := json.Marshal(sh)
	if len(b) > outboxShardLimit {
		return errors.New("outbox_shard_capacity")
	}
	if err := s.outboxWrite("transaction.json", outboxTransaction{1, key[:2], sh, idx}, 36*1024*1024); err != nil {
		return err
	}
	_, err := s.outboxLoad(true)
	return err
}
func cloneShard(sh outboxShard) outboxShard {
	out := outboxShard{V: 1, Records: map[string]delivery{}}
	for k, d := range sh.Records {
		out.Records[k] = d
	}
	return out
}
func pruneShard(sh *outboxShard, now int64) {
	for k, d := range sh.Records {
		if d.Created+outboxRetention <= now {
			delete(sh.Records, k)
			continue
		}
		if d.Expires <= now && d.State == "pending" {
			if d.State == "pending" {
				d.State = "permanent_failed"
				d.Error = "expired"
			}
			d.Envelope = nil
			d.Owner = ""
			d.Lease = 0
			d.Next = 0
			sh.Records[k] = d
		}
	}
}
func (s Store) prepareOutbox() error { return ensureDir(filepath.Join(s.Dir, "outbox-v1"), true) }

// Called under the cross-process state lock. A durable per-shard transaction makes
// interrupted migration/rewrite restartable without a second envelope or new TTL.
func (s Store) migrateOutbox() error {
	if err := s.prepareOutbox(); err != nil {
		return err
	}
	idx, err := s.outboxLoad(true)
	if err != nil {
		return err
	}
	if idx.Migrated {
		err := os.Remove(filepath.Join(s.Dir, "events.json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cache := map[string]cachedEnvelope{}
	err = s.readJSON("events.json", &cache)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("outbox_migration_failed")
	}
	if cache == nil || len(cache) > 512 {
		return errors.New("outbox_migration_failed")
	}
	for _, c := range cache {
		e := c.Envelope
		if !opaqueID.MatchString(e.PairID) || !opaqueID.MatchString(e.EventID) || e.ExpiresAt <= 0 {
			return errors.New("outbox_migration_failed")
		}
		key := deliveryKey(e.PairID, e.EventID)
		sh, err := s.loadShard(key)
		if err != nil {
			return err
		}
		if _, ok := sh.Records[key]; ok {
			continue
		}
		old := cloneShard(sh)
		d := delivery{PairID: e.PairID, EventID: e.EventID, Kind: e.Kind, LocatorHash: c.LocatorHash, Created: e.ExpiresAt - 3600, Expires: e.ExpiresAt, State: "legacy_unknown"}
		d.Envelope = &e
		if d.Created+outboxRetention <= time.Now().Unix() {
			continue
		}
		sh.Records[key] = d
		idx, err = s.outboxLoad(true)
		if err != nil {
			return err
		}
		if err = s.saveShard(idx, key, old, sh); err != nil {
			return err
		}
	}
	idx, err = s.outboxLoad(true)
	if err != nil {
		return err
	}
	idx.Migrated = true
	if err = s.outboxWrite("index.json", idx, 32*1024*1024); err != nil {
		return err
	}
	err = os.Remove(filepath.Join(s.Dir, "events.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func enqueueDelivery(ctx context.Context, s Store, p Pair, kind, identity string, l Locator, agent string, nativeSource ...string) (delivery, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return delivery{}, err
	}
	defer unlock()
	if err = s.migrateOutbox(); err != nil {
		return delivery{}, err
	}
	if len(nativeSource) > 0 && nativeSource[0] != "" {
		idx, e := s.outboxLoad(true)
		if e != nil {
			return delivery{}, e
		}
		keyBytes, e := decode64(p.EncryptionKey, 32)
		if e != nil {
			return delivery{}, e
		}
		for key, pair := range idx.Legacy {
			if pair != p.PairID {
				continue
			}
			sh, e := s.loadShard(key)
			if e != nil {
				return delivery{}, e
			}
			old, ok := sh.Records[key]
			if !ok || old.State != "legacy_unknown" || old.Envelope == nil {
				return delivery{}, errors.New("outbox_corrupt")
			}
			if old.Created+outboxRetention <= time.Now().Unix() {
				continue
			}
			locator, e := authenticateEnvelope(keyBytes, *old.Envelope)
			if e != nil {
				return delivery{}, errors.New("outbox_legacy_authentication_failed")
			}
			plain, _ := json.Marshal(locator)
			legacyIdentity := digest(nativeSource[0] + "\x00" + string(plain))
			if old.EventID == digest(legacyIdentity+"\x00"+p.PairID) {
				return old, nil
			}
		}
	}
	eventID := digest(identity + "\x00" + p.PairID)
	key := deliveryKey(p.PairID, eventID)
	sh, err := s.loadShard(key)
	if err != nil {
		return delivery{}, err
	}
	old := cloneShard(sh)
	now := time.Now().Unix()
	pruneShard(&sh, now)
	plain, _ := json.Marshal(l)
	hash := digest(string(plain))
	if d, ok := sh.Records[key]; ok {
		if d.LocatorHash != hash || d.Kind != kind {
			return d, errors.New("event_identity_conflict")
		}
		if d.Expires <= now {
			return d, errors.New("source_event_expired")
		}
		return d, nil
	}
	keyBytes, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return delivery{}, err
	}
	e, err := sealAgent(keyBytes, Envelope{V: 1, PairID: p.PairID, EventID: eventID, Kind: kind, ExpiresAt: now + 3600}, l, nil, agent)
	if err != nil {
		return delivery{}, err
	}
	d := delivery{PairID: p.PairID, EventID: eventID, Kind: kind, LocatorHash: hash, Created: now, Expires: e.ExpiresAt, State: "pending", Envelope: &e}
	sh.Records[key] = d
	idx, err := s.outboxLoad(true)
	if err != nil {
		return delivery{}, err
	}
	if err = s.saveShard(idx, key, old, sh); err != nil {
		if err.Error() != "outbox_pair_capacity" && err.Error() != "outbox_installation_capacity" && err.Error() != "outbox_shard_capacity" {
			return delivery{}, err
		}
		// Reclaim only expired payloads / completed retention windows before refusing
		// capacity. Never evict a valid pending delivery or protected tombstone.
		if gcErr := s.gcOutboxLocked(ctx); gcErr != nil {
			return delivery{}, gcErr
		}
		sh, err = s.loadShard(key)
		if err != nil {
			return delivery{}, err
		}
		old = cloneShard(sh)
		sh.Records[key] = d
		idx, err = s.outboxLoad(true)
		if err != nil {
			return delivery{}, err
		}
		if err = s.saveShard(idx, key, old, sh); err != nil {
			return delivery{}, err
		}
	}
	return d, nil
}
func updateDelivery(ctx context.Context, s Store, pair, event string, change func(*delivery) error) (delivery, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return delivery{}, err
	}
	defer unlock()
	if err = s.migrateOutbox(); err != nil {
		return delivery{}, err
	}
	key := deliveryKey(pair, event)
	sh, err := s.loadShard(key)
	if err != nil {
		return delivery{}, err
	}
	old := cloneShard(sh)
	d, ok := sh.Records[key]
	if !ok {
		return d, errors.New("outbox_record_missing")
	}
	if err = change(&d); err != nil {
		return d, err
	}
	sh.Records[key] = d
	idx, err := s.outboxLoad(true)
	if err != nil {
		return d, err
	}
	err = s.saveShard(idx, key, old, sh)
	return d, err
}
func terminalDelivery(state string) bool {
	return state == "apns_accepted" || state == "permanent_failed" || state == "revoked" || state == "disabled"
}
func deliveryResult(d delivery) map[string]any {
	v := map[string]any{"v": 1, "pair_id": d.PairID, "event_id": d.EventID, "state": d.State, "posts": d.Posts, "expires_at": d.Expires}
	if d.State == "handed_off" {
		v["outbox_handoff"] = true
		v["state"] = "accepted"
	}
	if d.Error != "" {
		v["error"] = d.Error
	}
	if d.Next > 0 {
		v["next_attempt_at"] = d.Next
	}
	return v
}
func deliverOne(ctx context.Context, s Store, a API, d delivery) (map[string]any, error) {
	owner := randomID()
	now := time.Now().Unix()
	claimed, err := updateDelivery(ctx, s, d.PairID, d.EventID, func(current *delivery) error {
		if current.State != "pending" || current.Next > now || current.Lease > now {
			return errors.New("outbox_not_due")
		}
		if current.Expires <= now || current.Posts >= 12 {
			current.State = "permanent_failed"
			current.Error = "expired"
			if current.Posts >= 12 {
				current.Error = "attempts_exhausted"
			}
			current.Envelope = nil
			return nil
		}
		current.Owner = owner
		current.Lease = now + 15
		current.Posts++
		return nil
	})
	if err != nil {
		if err.Error() == "outbox_not_due" {
			return deliveryResult(claimed), nil
		}
		return deliveryResult(claimed), err
	}
	if claimed.State != "pending" {
		return deliveryResult(claimed), nil
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	p, ok := pairs[d.PairID]
	var response map[string]any
	if !ok || p.Status != "active" {
		err = errors.New("pair_not_active")
	} else {
		response, err = a.request(ctx, p, "POST", "/v1/pairs/"+p.PairID+"/events", claimed.Envelope)
	}
	// Authentication failures suspend only this pair; network errors never retire it.
	var authError *APIError
	if errors.As(err, &authError) {
		if authError.Status == 401 && authError.Code == "unauthorized" {
			if e := s.transitionPair(ctx, p, "review_required"); e != nil {
				return nil, e
			}
		}
		if authError.Status == 410 && (authError.Code == "pair_revoked" || authError.Code == "pair_expired") {
			if e := s.retirePairIfCurrent(ctx, p.PairID, strings.TrimPrefix(authError.Code, "pair_"), false, &p); e != nil {
				return nil, e
			}
		}
	}
	requestErr := err
	// Persist the result even if the request used the command's remaining budget.
	saveCtx, cancel := settlementContext(ctx)
	defer cancel()
	updated, saveErr := updateDelivery(saveCtx, s, d.PairID, d.EventID, func(current *delivery) error {
		if current.Owner != owner {
			return errors.New("outbox_owner_changed")
		}
		current.Owner = ""
		current.Lease = 0
		current.Error = ""
		current.Next = 0
		state, _ := response["state"].(string)
		valid := response["event_id"] == d.EventID
		if valid && terminalDelivery(state) {
			current.State = state
			current.Envelope = nil
			if code, ok := response["error"].(string); ok {
				current.Error = safeCode(code)
			}
			return nil
		}
		if valid && response["outbox_handoff"] == true && (state == "accepted" || state == "sending" || state == "retryable_failed") {
			current.State = "handed_off"
			current.Envelope = nil
			return nil
		}
		if !ok || p.Status != "active" {
			current.State = "permanent_failed"
			current.Error = "pair_not_active"
			current.Envelope = nil
			return nil
		}
		current.Error = "api_unavailable"
		delay := 15 * time.Second * time.Duration(1<<min(current.Posts-1, 5))
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
		var ae *APIError
		if errors.As(requestErr, &ae) {
			current.Error = safeCode(ae.Code)
			if ae.RetryAfter > delay {
				delay = ae.RetryAfter
			}
			if ae.Status >= 400 && ae.Status < 500 && ae.Status != 429 {
				current.State = "permanent_failed"
				current.Envelope = nil
				return nil
			}
		}
		if requestErr == nil {
			current.Error = "service_handoff_unconfirmed"
		}
		current.Next = time.Now().Add(delay).Unix()
		if current.Next >= current.Expires || current.Posts >= 12 {
			current.State = "permanent_failed"
			current.Envelope = nil
			current.Error = "retry_after_expiry"
			if current.Posts >= 12 {
				current.Error = "attempts_exhausted"
			}
			current.Next = 0
		}
		return nil
	})
	if saveErr != nil {
		return deliveryResult(claimed), saveErr
	}
	if updated.State == "handed_off" || terminalDelivery(updated.State) {
		requestErr = nil
	}
	return deliveryResult(updated), requestErr
}
func safeCode(s string) string {
	if len(s) > 80 || strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return "request_failed"
	}
	return s
}
func outboxRecords(ctx context.Context, s Store, pair string, mutate bool) ([]delivery, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if mutate {
		if err = s.migrateOutbox(); err != nil {
			return nil, err
		}
	} else {
		if _, err = s.outboxLoad(false); err != nil {
			return nil, err
		}
	}
	var result []delivery
	for i := 0; i < 256; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		key := fmt.Sprintf("%02x", i)
		sh, err := s.loadShard(key)
		if err != nil {
			return nil, err
		}
		if mutate {
			old := cloneShard(sh)
			pruneShard(&sh, time.Now().Unix())
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(sh)
			if string(a) != string(b) {
				idx, e := s.outboxLoad(true)
				if e != nil {
					return nil, e
				}
				if e = s.saveShard(idx, key, old, sh); e != nil {
					return nil, e
				}
			}
		}
		for _, d := range sh.Records {
			if pair == "" || d.PairID == pair {
				result = append(result, d)
			}
		}
	}
	return result, nil
}
func outboxSummary(ctx context.Context, s Store, pair string) (map[string]any, error) {
	records, err := outboxRecords(ctx, s, pair, false)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"pending": 0, "handed_off": 0, "terminated": 0, "waiting_for": "next_agent_event_or_explicit_flush"}
	pending, handed, terminated := 0, 0, 0
	next := int64(0)
	recent := []string{}
	sort.Slice(records, func(i, j int) bool { return records[i].Created > records[j].Created })
	for _, d := range records {
		if d.State == "pending" && d.Expires > time.Now().Unix() {
			pending++
			if next == 0 || d.Next < next {
				next = d.Next
			}
		} else if d.State == "handed_off" {
			handed++
		} else {
			terminated++
		}
		if d.Error != "" && len(recent) < 8 {
			recent = append(recent, d.Error)
		}
	}
	result["pending"] = pending
	result["handed_off"] = handed
	result["terminated"] = terminated
	result["next_attempt_at"] = next
	result["recent_errors"] = recent
	return result, nil
}
func flushOutbox(ctx context.Context, s Store, a API, pair string, refresh bool) (map[string]any, error) {
	records, err := outboxRecords(ctx, s, pair, true)
	if err != nil {
		return nil, err
	}
	queues := map[string][]delivery{}
	now := time.Now().Unix()
	sort.Slice(records, func(i, j int) bool { return records[i].Created < records[j].Created })
	for _, d := range records {
		if refresh && d.State == "handed_off" || !refresh && d.State == "pending" && d.Next <= now && d.Lease <= now {
			queues[d.PairID] = append(queues[d.PairID], d)
		}
	}
	ids := []string{}
	for p := range queues {
		ids = append(ids, p)
	}
	sort.Strings(ids)
	var selected []delivery
	for len(selected) < 32 {
		added := false
		for _, p := range ids {
			if len(queues[p]) > 0 {
				selected = append(selected, queues[p][0])
				queues[p] = queues[p][1:]
				added = true
				if len(selected) == 32 {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	results := make([]map[string]any, len(selected))
	var wg sync.WaitGroup
	failures := make(chan error, len(selected))
	slots := make(chan struct{}, 4)
	for i, d := range selected {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(i int, d delivery) {
			defer wg.Done()
			defer func() { <-slots }()
			if refresh {
				var e error
				results[i], e = refreshDelivery(ctx, s, a, d)
				if e != nil {
					failures <- e
					if results[i] == nil {
						results[i] = map[string]any{"pair_id": d.PairID, "event_id": d.EventID, "error": "outbox_operation_failed"}
					}
				}
			} else {
				var e error
				results[i], e = deliverOne(ctx, s, a, d)
				if e != nil {
					failures <- e
					if results[i] == nil {
						results[i] = map[string]any{"pair_id": d.PairID, "event_id": d.EventID, "error": "outbox_operation_failed"}
					}
				}
			}
		}(i, d)
	}
	wg.Wait()
	summaryCtx, cancel := settlementContext(ctx)
	defer cancel()
	summary, err := outboxSummary(summaryCtx, s, pair)
	if err != nil {
		return nil, err
	}
	close(failures)
	var failure error
	for e := range failures {
		if failure == nil {
			failure = e
		}
	}
	return map[string]any{"v": 1, "results": results, "outbox": summary}, failure
}
func refreshDelivery(ctx context.Context, s Store, a API, d delivery) (map[string]any, error) {
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	p, ok := pairs[d.PairID]
	if !ok || p.Status != "active" {
		return deliveryResult(d), errors.New("pair_not_active")
	}
	response, requestErr := a.request(ctx, p, "GET", "/v1/pairs/"+p.PairID+"/events/"+d.EventID, nil)
	state, _ := response["state"].(string)
	updated, err := updateDelivery(ctx, s, d.PairID, d.EventID, func(current *delivery) error {
		if current.State != "handed_off" {
			return nil
		}
		if response["event_id"] == d.EventID && terminalDelivery(state) {
			current.State = state
			current.Error = ""
			if code, ok := response["error"].(string); ok {
				current.Error = safeCode(code)
			}
		} else if response["event_id"] == d.EventID && requestErr == nil {
			current.Error = ""
		} else if requestErr != nil {
			current.Error = "service_record_unavailable"
			if time.Now().Unix() > current.Expires+86400 {
				current.Error = "service_retention_may_have_elapsed"
			}
		}
		return nil
	})
	if err != nil {
		return deliveryResult(d), err
	}
	return deliveryResult(updated), requestErr
}

func (s Store) gcOutboxLocked(ctx context.Context) error {
	for i := 0; i < 256; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := fmt.Sprintf("%02x", i)
		sh, err := s.loadShard(key)
		if err != nil {
			return err
		}
		old := cloneShard(sh)
		pruneShard(&sh, time.Now().Unix())
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(sh)
		if string(a) == string(b) {
			continue
		}
		idx, err := s.outboxLoad(true)
		if err != nil {
			return err
		}
		if err = s.saveShard(idx, key, old, sh); err != nil {
			return err
		}
	}
	return nil
}

func settlementContext(ctx context.Context) (context.Context, context.CancelFunc) {
	end := time.Now().Add(time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	return context.WithDeadline(context.WithoutCancel(ctx), end)
}
