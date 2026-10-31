package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RunCapabilities struct {
	V                    int             `json:"v"`
	Enabled              bool            `json:"enabled"`
	States               bool            `json:"states"`
	Inbox                bool            `json:"inbox"`
	CapabilityGeneration int64           `json:"capability_generation"`
	PolicyGeneration     int64           `json:"policy_generation"`
	ExpiresAt            int64           `json:"expires_at"`
	Approvals            map[string]bool `json:"approvals"`
	Policy               struct {
		TurnCompleted bool `json:"turn_completed"`
		NeedsInput    bool `json:"needs_input"`
		AbnormalEnd   bool `json:"abnormal_end"`
	} `json:"policy"`
}

func runCapabilities(ctx context.Context, a API, p Pair) (RunCapabilities, error) {
	var c RunCapabilities
	m, err := a.request(ctx, p, "GET", "/v2/pairs/"+p.PairID+"/capabilities", nil)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			return c, nil
		}
		return c, err
	}
	b, _ := json.Marshal(m)
	if m["pair_id"] != p.PairID || m["installation_id"] != p.InstallationID {
		return c, errors.New("invalid_capability_receipt")
	}
	for _, name := range []string{"enabled", "states", "inbox"} {
		if _, ok := m[name].(bool); !ok {
			return c, errors.New("invalid_capability_receipt")
		}
	}
	for _, name := range []string{"capability_generation", "policy_generation", "expires_at"} {
		n, ok := m[name].(float64)
		if !ok || n < 0 || n >= 9007199254740991 || n != float64(int64(n)) {
			return c, errors.New("invalid_capability_receipt")
		}
	}
	policy, ok := m["policy"].(map[string]any)
	if !ok {
		return c, errors.New("invalid_capability_receipt")
	}
	for _, name := range []string{"turn_completed", "needs_input", "abnormal_end"} {
		if _, ok := policy[name].(bool); !ok {
			return c, errors.New("invalid_capability_receipt")
		}
	}
	approvals, ok := m["approvals"].(map[string]any)
	if !ok {
		return c, errors.New("invalid_capability_receipt")
	}
	for _, name := range []string{"codex", "claude", "opencode", "pi"} {
		if _, ok := approvals[name].(bool); !ok {
			return c, errors.New("invalid_capability_receipt")
		}
	}
	if json.Unmarshal(b, &c) != nil || c.V != 2 || c.ExpiresAt <= time.Now().Unix() || c.ExpiresAt > time.Now().Add(61*time.Second).Unix() || (c.Enabled && c.CapabilityGeneration <= 0) {
		return c, errors.New("invalid_capability_receipt")
	}
	return c, nil
}

type PendingRequest struct {
	State    string        `json:"state"`
	Approval *ApprovalHint `json:"approval,omitempty"`
}

type Activity struct {
	RequestID      string
	ResolveRequest bool
	Agent          string
	Session        string
	Run            string
	Instance       string
	Occurrence     string
	State          string
	ToolID         string
	ToolCategory   string
	ToolStatus     string
	Reminder       string
	Approval       *ApprovalHint
}
type StoredRun struct {
	FirstOccurrence string                    `json:"first_occurrence,omitempty"`
	Requests        map[string]PendingRequest `json:"requests,omitempty"`
	Agent           string                    `json:"agent"`
	Session         string                    `json:"session"`
	Run             string                    `json:"run"`
	Instance        string                    `json:"instance"`
	Locator         Locator                   `json:"locator"`
	Revision        int64                     `json:"revision"`
	State           string                    `json:"state"`
	Updated         int64                     `json:"updated"`
	Seen            []string                  `json:"seen"`
	Tools           map[string]RunTool        `json:"tools"`
	Streams         map[string]string         `json:"streams"`
	Pending         map[string]Envelope       `json:"pending"`
	Approval        *ApprovalHint             `json:"approval,omitempty"`
}
type RunIntent struct {
	PairID     string     `json:"pair_id"`
	Key        string     `json:"key"`
	Occurrence string     `json:"occurrence"`
	Kind       string     `json:"kind"`
	Payload    RunPayload `json:"payload"`
	ExpiresAt  int64      `json:"expires_at"`
	Envelope   *Envelope  `json:"envelope,omitempty"`
}
type RunStore struct {
	LastPair  [2]string            `json:"last_pair,omitempty"`
	Retired   map[string]int64     `json:"retired,omitempty"`
	NextState map[string]int64     `json:"next_state"`
	Intents   map[string]RunIntent `json:"intents"`
	Runs      map[string]StoredRun `json:"runs"`
	Current   map[string]string    `json:"current"`
}

func loadRuns(s Store) (RunStore, error) {
	r := RunStore{Runs: map[string]StoredRun{}, Current: map[string]string{}, Intents: map[string]RunIntent{}}
	raw, err := secureRead(filepath.Join(s.Dir, "runs-v2.json"), runStoreLimit)
	// One bounded legacy recovery allowance; never applied to keys/configuration.
	// A compact rewrite still must fit the normal write budget, otherwise retain
	// the original file and report capacity rather than destroying responsibility.
	if err != nil && err.Error() == "configuration exceeds size limit" {
		raw, err = secureRead(filepath.Join(s.Dir, "runs-v2.json"), 2*runStoreLimit)
	}
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if r.Retired == nil {
		r.Retired = map[string]int64{}
	}
	if r.Intents == nil {
		r.Intents = map[string]RunIntent{}
	}
	if r.NextState == nil {
		r.NextState = map[string]int64{}
	}
	if r.Runs == nil || r.Current == nil {
		return r, errors.New("run_store_corrupt")
	}
	return r, err
}

// Runs have their own bounded compact format. This accommodates the old valid
// 128 x 256 occurrence ledger without increasing limits for keys/configuration.
const runStoreLimit = 16 * 1024 * 1024

func saveRuns(s Store, db RunStore) error {
	raw, err := json.Marshal(db)
	if err != nil {
		return err
	}
	if len(raw) > runStoreLimit {
		return errors.New("run_store_capacity")
	}
	return atomicWrite(filepath.Join(s.Dir, "runs-v2.json"), raw, 0600)
}
func reclaimRuns(db *RunStore, now int64, needSlot bool) {
	for pair, next := range db.NextState {
		if next <= now {
			delete(db.NextState, pair)
		}
	}
	for key, until := range db.Retired {
		if until <= now {
			delete(db.Retired, key)
		}
	}
	busy := map[string]bool{}
	for _, intent := range db.Intents {
		if intent.ExpiresAt > now {
			busy[intent.Key] = true
		}
	}
	keys := []string{}
	for key, run := range db.Runs {
		if !busy[key] && terminalRun(run.State) && (run.Approval == nil || run.Approval.ExpiresAt <= now) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return db.Runs[keys[i]].Updated < db.Runs[keys[j]].Updated })
	for _, key := range keys {
		run := db.Runs[key]
		if run.Updated >= now-86400 && (!needSlot || len(db.Runs) < 128) {
			continue
		}
		db.Retired[key] = now + 86400
		if run.FirstOccurrence != "" {
			target, _ := json.Marshal(run.Locator)
			instance := digest(run.Agent + "\x00" + run.Session + "\x00" + run.Instance + "\x00" + string(target))
			db.Retired[digest(instance+"\x00occurrence\x00"+run.FirstOccurrence)] = now + 86400
		}
		delete(db.Runs, key)
	}
}
func terminalRun(state string) bool {
	return state == "turn_completed" || state == "failed" || state == "interrupted"
}
func toolCategory(name string) string {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec_command":
		return "shell"
	case "read", "write", "edit", "apply_patch":
		return "file"
	case "grep", "glob", "search":
		return "search"
	case "webfetch", "websearch":
		return "web"
	}
	if strings.HasPrefix(name, "mcp") {
		return "mcp"
	}
	return "other"
}
func applyActivity(ctx context.Context, s Store, l Locator, a Activity) (string, StoredRun, bool, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", StoredRun{}, false, err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return "", StoredRun{}, false, err
	}
	target, _ := json.Marshal(l)
	instance := digest(a.Agent + "\x00" + a.Session + "\x00" + a.Instance + "\x00" + string(target))
	if a.Run == "" {
		if db.Retired[digest(instance+"\x00occurrence\x00"+a.Occurrence)] > time.Now().Unix() {
			return "", StoredRun{}, false, nil
		}
		for key, r := range db.Runs {
			if r.Agent == a.Agent && r.Session == a.Session && r.Instance == a.Instance && r.Locator == l {
				for _, occurrence := range r.Seen {
					if occurrence == a.Occurrence {
						return key, r, false, nil
					}
				}
			}
		}
		if a.State == "starting" || a.State == "running" {
			a.Run = randomID()
		} else {
			a.Run = db.Current[instance]
		}
		if a.Run == "" {
			a.Run = randomID()
		}
	}
	key := digest(instance + "\x00" + a.Run)
	r, exists := db.Runs[key]
	now := time.Now().Unix()
	if !exists {
		reclaimRuns(&db, now, true)
		if db.Retired[key] > now {
			return key, r, false, nil
		}
		// 128 is the retained snapshot target, not a daily business limit.
		// Reserve bounded overflow for critical obligations while active runs
		// occupy all ordinary slots. Never evict a run with an unsent intent.
		if len(db.Runs) >= 128 && a.Reminder == "" || len(db.Runs) >= 512 {
			return "", r, false, errors.New("run_capacity_retry_after_flush")
		}
		r = StoredRun{FirstOccurrence: a.Occurrence, Agent: a.Agent, Session: a.Session, Run: a.Run, Instance: a.Instance, Locator: l, Tools: map[string]RunTool{}, Streams: map[string]string{}, Pending: map[string]Envelope{}}
	}
	for _, id := range r.Seen {
		if id == a.Occurrence {
			return key, r, false, nil
		}
	}
	if terminalRun(r.State) {
		return key, r, false, nil
	}
	r.Revision++
	r.Updated = now
	r.State = a.State
	if r.Requests == nil {
		r.Requests = map[string]PendingRequest{}
	}
	if a.RequestID != "" {
		if a.ResolveRequest {
			if _, known := r.Requests[a.RequestID]; !known {
				return key, db.Runs[key], false, nil
			}
			delete(r.Requests, a.RequestID)
		} else {
			if _, exists := r.Requests[a.RequestID]; !exists && len(r.Requests) >= 64 {
				return key, r, false, errors.New("request_capacity")
			}
			r.Requests[a.RequestID] = PendingRequest{State: a.State, Approval: a.Approval}
		}
	}
	r.Approval = a.Approval
	r.Seen = append(r.Seen, a.Occurrence)
	if len(r.Seen) > 256 {
		r.Seen = r.Seen[len(r.Seen)-256:]
	}
	if a.ToolID != "" {
		if previous, ok := r.Tools[a.ToolID]; ok && (previous.Status == "returned" || previous.Status == "succeeded" || previous.Status == "failed" || previous.Status == "interrupted") && (a.ToolStatus == "pending" || a.ToolStatus == "running") {
			return key, db.Runs[key], false, nil
		}
		if len(r.Tools) >= 32 {
			if _, ok := r.Tools[a.ToolID]; !ok {
				for id, tool := range r.Tools {
					if tool.Status != "pending" && tool.Status != "running" {
						delete(r.Tools, id)
						break
					}
				}
				if len(r.Tools) >= 32 {
					return key, r, false, errors.New("tool_capacity")
				}
			}
		}
		r.Tools[a.ToolID] = RunTool{a.ToolID, a.ToolCategory, a.ToolStatus}
	}
	if a.State == "running" || a.State == "tool_running" || a.State == "tool_pending" {
		for _, t := range r.Tools {
			if t.Status == "running" {
				r.State = "tool_running"
				break
			}
			if t.Status == "pending" {
				r.State = "tool_pending"
			}
		}
	}
	if !terminalRun(r.State) {
		requestIDs := make([]string, 0, len(r.Requests))
		for id := range r.Requests {
			requestIDs = append(requestIDs, id)
		}
		sort.Strings(requestIDs)
		for _, id := range requestIDs {
			request := r.Requests[id]
			if r.State != "waiting_approval" || request.State == "waiting_approval" {
				r.State = request.State
				r.Approval = request.Approval
			}
		}
	} else {
		r.Requests = nil
		r.Approval = nil
	}
	pairs, err := s.pairs()
	if err != nil {
		return key, r, false, err
	}
	for ik, intent := range db.Intents {
		if intent.ExpiresAt <= now {
			delete(db.Intents, ik)
		}
	}
	for pid, pair := range pairs {
		if pair.Status != "active" || pair.InstallationID != l.InstallationID {
			continue
		}
		stream := r.Streams[pid]
		if stream == "" {
			stream = randomID()
			r.Streams[pid] = stream
		}
		kind := a.Reminder
		if kind == "" {
			kind = "state"
		}
		ik := digest(pid + "\x00" + a.Occurrence)
		if kind == "state" {
			ik = digest(pid + "\x00state\x00" + key)
		}
		if kind != "state" {
			delete(db.Intents, digest(pid+"\x00state\x00"+key))
		}
		if _, ok := db.Intents[ik]; !ok {
			stateCount := 0
			for _, intent := range db.Intents {
				if intent.Kind == "state" {
					stateCount++
				}
			}
			if len(db.Intents) >= 512 || kind == "state" && stateCount >= 256 {
				return key, r, false, errors.New("activity_pending_capacity")
			}
		}
		db.Intents[ik] = RunIntent{PairID: pid, Key: key, Occurrence: a.Occurrence, Kind: kind, Payload: payloadForRun(r, stream), ExpiresAt: now + 3600}
		if kind != "state" {
			delete(db.Intents, digest(pid+"\x00state\x00"+key))
		}
	}
	db.Runs[key] = r
	if !exists && (a.State == "starting" || a.State == "running") {
		db.Current[instance] = a.Run
	}
	if len(db.Current) > 256 {
		db.Current = map[string]string{instance: a.Run}
	}
	err = saveRuns(s, db)
	return key, r, true, err
}
func payloadForRun(r StoredRun, stream string) RunPayload {
	tools := []RunTool{}
	for _, t := range r.Tools {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].InvocationID < tools[j].InvocationID })
	active := 0
	for _, tool := range tools {
		if tool.Status == "pending" || tool.Status == "running" {
			active++
		}
	}
	p := RunPayload{V: 2, Locator: r.Locator, Agent: r.Agent, SessionID: r.Session, RunID: r.Run, StreamID: stream, Revision: r.Revision, State: r.State, OccurredAt: r.Updated, Tools: tools, Approval: r.Approval, ActiveToolCount: active}
	// Safe summaries may omit rows; the active invocation count never becomes the
	// subset size. Keep room for AEAD and every possible APNs slash escape.
	for {
		raw, _ := json.Marshal(p)
		if len(raw) <= 1500 || len(p.Tools) == 0 {
			break
		}
		p.Tools = p.Tools[:len(p.Tools)-1]
		p.ToolsTruncated = true
	}
	return p
}
func publishActivity(ctx context.Context, s Store, api API, l Locator, a Activity) error {
	if _, _, _, err := applyActivity(ctx, s, l, a); err != nil {
		if err.Error() != "run_capacity_retry_after_flush" && err.Error() != "activity_pending_capacity" && err.Error() != "run_store_capacity" {
			return err
		}
		// Capacity is recoverable: hand off pending responsibility before one retry.
		// Failure leaves the prior readable store and outbox untouched.
		_ = flushRunSnapshots(ctx, s, api)
		if _, _, _, retryErr := applyActivity(ctx, s, l, a); retryErr != nil {
			return retryErr
		}
	}
	return flushRunSnapshots(ctx, s, api)
}
func removeRunIntent(ctx context.Context, s Store, key, occurrence string) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return err
	}
	if db.Intents[key].Occurrence == occurrence {
		delete(db.Intents, key)
		return saveRuns(s, db)
	}
	return nil
}
func encodeRunIntent(ctx context.Context, s Store, key string, intent RunIntent, c RunCapabilities, p Pair) (RunIntent, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return intent, err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return intent, err
	}
	current, ok := db.Intents[key]
	if !ok || current.Occurrence != intent.Occurrence {
		return intent, errors.New("activity_superseded")
	}
	if current.Envelope != nil {
		return current, nil
	}
	raw, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return intent, err
	}
	e, err := sealRun(raw, Envelope{V: 2, PairID: p.PairID, EventID: digest(intent.Occurrence + "\x00" + p.PairID), Kind: intent.Kind, StreamID: intent.Payload.StreamID, Revision: intent.Payload.Revision, CapabilityGeneration: c.CapabilityGeneration, ExpiresAt: intent.ExpiresAt}, intent.Payload, nil)
	if err != nil {
		return intent, err
	}
	current.Kind = intent.Kind
	current.Payload = intent.Payload
	current.Envelope = &e
	db.Intents[key] = current
	return current, saveRuns(s, db)
}
func enqueueRunReminder(ctx context.Context, s Store, intent RunIntent) (delivery, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return delivery{}, err
	}
	defer unlock()
	if err = s.migrateOutbox(); err != nil {
		return delivery{}, err
	}
	e := *intent.Envelope
	key := deliveryKey(e.PairID, e.EventID)
	sh, err := s.loadShard(key)
	if err != nil {
		return delivery{}, err
	}
	if old, ok := sh.Records[key]; ok {
		return old, nil
	}
	old := cloneShard(sh)
	raw, _ := json.Marshal(intent.Payload.Locator)
	d := delivery{PairID: e.PairID, EventID: e.EventID, Kind: e.Kind, LocatorHash: digest(string(raw)), Created: intent.Payload.OccurredAt, Expires: e.ExpiresAt, State: "pending", Envelope: &e}
	sh.Records[key] = d
	idx, err := s.outboxLoad(true)
	if err != nil {
		return d, err
	}
	return d, s.saveShard(idx, key, old, sh)
}
func flushRunSnapshots(ctx context.Context, s Store, api API) error {
	// Three short phases: fresh business first, due durable reminders next,
	// coalesced observations last. All inherit the native caller's deadline.
	budget := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 5*time.Second {
		budget = 600 * time.Millisecond
	}
	fresh, cancel := context.WithTimeout(ctx, budget)
	first := flushRunIntents(fresh, s, api, false)
	cancel()
	recovery, cancel := context.WithTimeout(ctx, budget)
	_, retryErr := flushOutbox(recovery, s, api, "", false)
	cancel()
	stateErr := flushRunIntents(ctx, s, api, true)
	if first != nil {
		return first
	}
	if retryErr != nil {
		return retryErr
	}
	return stateErr
}
func flushRunIntents(ctx context.Context, s Store, api API, states bool) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	db, err := loadRuns(s)
	var keys []string
	if err == nil {
		previous := db.LastPair
		keys = scheduleRunIntents(&db, states)
		if db.LastPair != previous {
			err = saveRuns(s, db)
		}
	}
	unlock()
	if err != nil {
		return err
	}
	pairs, err := s.pairs()
	if err != nil {
		return err
	}

	var last error
	count := 0
	for _, key := range keys {
		if count >= 4 || ctx.Err() != nil {
			break
		}
		intent := db.Intents[key]
		if (intent.Kind == "state") != states {
			continue
		}
		if intent.Kind == "state" && db.NextState[intent.PairID] > time.Now().Unix() {
			continue
		}
		p, ok := pairs[intent.PairID]
		if !ok || p.Status != "active" || intent.ExpiresAt <= time.Now().Unix() {
			_ = removeRunIntent(ctx, s, key, intent.Occurrence)
			continue
		}
		count++
		if intent.Envelope == nil {
			c, e := cachedRunCapabilities(ctx, s, api, p)
			if e != nil {
				last = e
				continue
			}
			if !c.Enabled || !c.States || !c.Inbox {
				if intent.Kind == "turn_completed" || intent.Kind == "needs_input" {
					d, e := enqueueDelivery(ctx, s, p, intent.Kind, intent.Occurrence, intent.Payload.Locator, intent.Payload.Agent)
					if e != nil {
						last = e
						continue
					}
					if e = removeRunIntent(ctx, s, key, intent.Occurrence); e != nil {
						last = e
						continue
					}
					if d.State == "pending" {
						_, last = deliverOne(ctx, s, api, d)
					}
				} else {
					last = removeRunIntent(ctx, s, key, intent.Occurrence)
				}
				continue
			}
			if hint := intent.Payload.Approval; hint != nil {
				request, readErr := approvalRead(s, hint.RequestID)
				if readErr != nil || !c.Approvals[intent.Payload.Agent] || request.Pairs[p.PairID] != c.PolicyGeneration {
					intent.Payload.Approval = nil
				}
			}
			allowed := intent.Kind == "state" || intent.Kind == "test" || intent.Kind == "turn_completed" && c.Policy.TurnCompleted || intent.Kind == "needs_input" && (c.Policy.NeedsInput || intent.Payload.Approval != nil && c.Approvals[intent.Payload.Agent]) || (intent.Kind == "failed" || intent.Kind == "interrupted") && c.Policy.AbnormalEnd
			if !allowed {
				intent.Kind = "state"
			}
			intent, e = encodeRunIntent(ctx, s, key, intent, c, p)
			if e != nil {
				last = e
				continue
			}
		}
		if intent.Kind == "state" && !states {
			continue
		}
		if intent.Kind != "state" {
			d, e := enqueueRunReminder(ctx, s, intent)
			if e != nil {
				last = e
				continue
			}
			if e = removeRunIntent(ctx, s, key, intent.Occurrence); e != nil {
				last = e
				continue
			}
			if d.State == "pending" {
				_, last = deliverOne(ctx, s, api, d)
			}
			continue
		}
		release, claimErr := claimStateSend(ctx, s, p.PairID)
		if claimErr != nil {
			last = claimErr
			continue
		}
		if release == nil {
			continue
		}
		response, e := api.request(ctx, p, "PUT", "/v2/pairs/"+p.PairID+"/snapshots/"+intent.Envelope.StreamID, intent.Envelope)
		delay := 2 * time.Second
		if e != nil {
			delay = 5 * time.Second
			var ae *APIError
			if errors.As(e, &ae) && ae.RetryAfter > delay {
				delay = ae.RetryAfter
			}
		}
		release(delay)
		if e != nil {
			last = e
			var ae *APIError
			if errors.As(e, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != 429 {
				_ = removeRunIntent(ctx, s, key, intent.Occurrence)
			}
			continue
		}
		if response["v"] != float64(2) {
			last = errors.New("invalid_snapshot_receipt")
			continue
		}
		last = removeRunIntent(ctx, s, key, intent.Occurrence)
	}
	return last
}
