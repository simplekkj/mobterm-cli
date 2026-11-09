package main

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type InteractionHint struct {
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	V                 int    `json:"v"`
	RequestID         string `json:"request_id"`
	Digest            string `json:"digest"`
	Kind              string `json:"kind"`
	ExpiresAt         int64  `json:"expires_at"`
}
type InteractionFrame struct {
	V                int    `json:"v"`
	PairID           string `json:"pair_id"`
	RequestID        string `json:"request_id"`
	Digest           string `json:"digest,omitempty"`
	Kind             string `json:"kind,omitempty"`
	Action           string `json:"action,omitempty"`
	ResponseJSON     string `json:"response_json,omitempty"`
	ResponseDigest   string `json:"response_digest,omitempty"`
	DecisionID       string `json:"decision_id,omitempty"`
	PolicyGeneration int64  `json:"policy_generation,omitempty"`
	RequestRevision  int64  `json:"request_revision,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	Proof            string `json:"proof,omitempty"`
}
type InteractionDecision struct {
	RequestID    string `json:"request_id"`
	Digest       string `json:"digest"`
	Action       string `json:"action"`
	ResponseJSON string `json:"response_json"`
	ExpiresAt    int64  `json:"expires_at"`
	OwnerToken   string `json:"owner_token"`
}
type InteractionOutput struct {
	Value     any
	RequestID string
}
type InteractionRecord struct {
	ExpectedNativeID     string            `json:"expected_native_id,omitempty"`
	ExpectedSession      string            `json:"expected_session,omitempty"`
	ExpectedAnswerDigest string            `json:"expected_answer_digest,omitempty"`
	UnavailableReason    string            `json:"unavailable_reason,omitempty"`
	V                    int               `json:"v"`
	RequestID            string            `json:"request_id"`
	Digest               string            `json:"digest"`
	Kind                 string            `json:"kind"`
	Agent                string            `json:"agent"`
	RequestJSON          string            `json:"request_json,omitempty"`
	Locator              Locator           `json:"locator"`
	NativeIdentity       string            `json:"native_identity"`
	PID                  int               `json:"pid"`
	Birth                string            `json:"birth"`
	ParentPID            int               `json:"parent_pid"`
	ParentBirth          string            `json:"parent_birth"`
	ExpiresAt            int64             `json:"expires_at"`
	CreatedAt            int64             `json:"created_at"`
	Revision             int64             `json:"request_revision"`
	State                string            `json:"state"`
	Pairs                map[string]int64  `json:"pairs"`
	Frame                *InteractionFrame `json:"frame,omitempty"`
	OwnerToken           string            `json:"owner_token"`
	Evidence             string            `json:"evidence,omitempty"`
}
type InteractionCapabilities struct {
	V                    int                        `json:"v"`
	PairID               string                     `json:"pair_id"`
	InstallationID       string                     `json:"installation_id"`
	Enabled              bool                       `json:"enabled"`
	Protocols            []int                      `json:"interaction_protocols"`
	PolicyGeneration     int64                      `json:"policy_generation"`
	CapabilityGeneration int64                      `json:"capability_generation"`
	ExpiresAt            int64                      `json:"expires_at"`
	Interactions         map[string]map[string]bool `json:"interactions"`
}
type InteractionPolicy map[string]map[string]map[string]int64

func interactionMAC(key []byte, f InteractionFrame) (string, error) {
	derived, e := hkdf.Key(sha256.New, key, []byte(f.PairID), "mobterm/interaction/1", 32)
	if e != nil {
		return "", e
	}
	m := hmac.New(sha256.New, derived)
	for _, v := range []string{"mobterm/interaction/1", f.PairID, f.RequestID, f.Digest, f.Kind, f.Action, f.ResponseDigest, f.DecisionID, strconv.FormatInt(f.PolicyGeneration, 10), strconv.FormatInt(f.RequestRevision, 10), strconv.FormatInt(f.ExpiresAt, 10)} {
		_ = binary.Write(m, binary.BigEndian, uint32(len(v)))
		_, _ = io.WriteString(m, v)
	}
	return base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}
func readInteractionFrame(ctx context.Context, in io.Reader, v any) error {
	result := make(chan error, 1)
	go func() {
		var header [4]byte
		if _, e := io.ReadFull(in, header[:]); e != nil {
			result <- errors.New("invalid_interaction_frame")
			return
		}
		n := binary.BigEndian.Uint32(header[:])
		if n == 0 || n > interactionFrameLimit {
			result <- errors.New("invalid_interaction_frame")
			return
		}
		raw := make([]byte, n)
		if _, e := io.ReadFull(in, raw); e != nil {
			result <- errors.New("truncated_interaction_frame")
			return
		}
		var tail [1]byte
		if n, e := in.Read(tail[:]); n != 0 || e != io.EOF {
			result <- errors.New("trailing_interaction_frame")
			return
		}
		result <- interactionJSON(raw, v)
	}()
	select {
	case e := <-result:
		return e
	case <-ctx.Done():
		return errors.New("interaction_frame_cancelled")
	}
}
func interactionCapabilities(ctx context.Context, api API, p Pair) (InteractionCapabilities, error) {
	var c InteractionCapabilities
	m, e := api.request(ctx, p, "GET", "/v2/pairs/"+p.PairID+"/interaction-capabilities", nil)
	if e != nil {
		return c, e
	}
	raw, e := json.Marshal(m)
	if e != nil {
		return c, e
	}
	if interactionJSON(raw, &c) != nil || c.V != 1 || c.PairID != p.PairID || c.InstallationID != p.InstallationID || len(c.Protocols) != 1 || c.Protocols[0] != 1 || c.PolicyGeneration < 1 || c.CapabilityGeneration < 1 || c.ExpiresAt <= time.Now().Unix() || c.ExpiresAt > time.Now().Unix()+61 || len(c.Interactions) != 4 {
		return c, errors.New("invalid_interaction_grant")
	}
	for _, agent := range []string{"codex", "claude", "opencode", "pi"} {
		kinds := c.Interactions[agent]
		if len(kinds) != 6 {
			return c, errors.New("invalid_interaction_grant")
		}
		for _, kind := range []string{"approval", "question", "form", "elicitation", "planApproval", "followupMessage"} {
			if _, ok := kinds[kind]; !ok {
				return c, errors.New("invalid_interaction_grant")
			}
		}
	}
	return c, nil
}
func localInteractions(s Store) (InteractionPolicy, error) {
	m := InteractionPolicy{}
	e := s.readJSON("interaction-policy-v1.json", &m)
	if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if m == nil {
		return nil, errors.New("invalid_interaction_policy")
	}
	return m, e
}
func interactionRead(s Store, id string) (InteractionRecord, error) {
	var r InteractionRecord
	if !opaqueID.MatchString(id) {
		return r, errors.New("invalid_interaction_request")
	}
	e := s.readJSON("interaction-"+id+".json", &r)
	if e == nil && (r.V != 1 || r.RequestID != id || r.RequestJSON != "" && digest(r.RequestJSON) != r.Digest || len(r.RequestJSON) > interactionRequestLimit) {
		e = errors.New("invalid_interaction_request")
	}
	return r, e
}
func interactionAlive(r InteractionRecord) bool {
	a, e := processIdentity(r.PID)
	if e != nil || a != r.Birth {
		return false
	}
	a, e = processIdentity(r.ParentPID)
	return e == nil && a == r.ParentBirth
}
func interactionEligible(ctx context.Context, s Store, api API, p Pair, r InteractionRecord, g int64) error {
	pairs, e := s.pairs()
	if e != nil {
		return e
	}
	current, ok := pairs[p.PairID]
	if !ok || current != p || p.Status != "active" || p.InstallationID != r.Locator.InstallationID {
		return errors.New("pair_not_active")
	}
	m, e := localInteractions(s)
	if e != nil || m[p.PairID][r.Agent][r.Kind] <= 0 {
		return errors.New("interaction_disabled")
	}
	c, e := interactionCapabilities(ctx, api, p)
	if e != nil {
		return e
	}
	if !c.Enabled || c.PolicyGeneration != g || !c.Interactions[r.Agent][r.Kind] {
		return errors.New("interaction_disabled")
	}
	return nil
}
func interactionResult(r InteractionRecord, pair string, inspect bool) map[string]any {
	state := r.State
	if (state == "pending" || state == "decision_recorded") && (time.Now().Unix() >= r.ExpiresAt || !interactionAlive(r)) || state == "unavailable" && time.Now().Unix() >= r.ExpiresAt {
		state = "expired"
	}
	if state == "dispatching" && !interactionAlive(r) {
		state = "delivery_unknown"
	}
	v := map[string]any{"v": 1, "pair_id": pair, "request_id": r.RequestID, "digest": r.Digest, "kind": r.Kind, "agent": r.Agent, "expires_at": r.ExpiresAt, "policy_generation": r.Pairs[pair], "request_revision": r.Revision, "state": state}
	if r.UnavailableReason != "" {
		v["unavailable_reason"] = r.UnavailableReason
	}
	if inspect && (state == "pending" || state == "unavailable") && r.RequestJSON != "" {
		v["request_json"] = r.RequestJSON
		var spec InteractionSpec
		if interactionJSON([]byte(r.RequestJSON), &spec) == nil {
			v["supported_actions"] = spec.SupportedActions
		}
	}
	if r.Frame != nil && r.Frame.PairID == pair {
		v["decision_id"] = r.Frame.DecisionID
		v["action"] = r.Frame.Action
		v["response_digest"] = r.Frame.ResponseDigest
		if r.Evidence != "" {
			v["evidence"] = r.Evidence
		}
	}
	return v
}
func interactionConfigure(ctx context.Context, s Store, api API, in io.Reader) (any, error) {
	var f struct {
		V                int    `json:"v"`
		PairID           string `json:"pair_id"`
		Agent            string `json:"agent"`
		Kind             string `json:"kind"`
		Enabled          bool   `json:"enabled"`
		PolicyGeneration int64  `json:"policy_generation"`
	}
	if e := readInteractionFrame(ctx, in, &f); e != nil {
		return nil, e
	}
	if f.V != 1 || !opaqueID.MatchString(f.PairID) || !interactionAgent(f.Agent) || !interactionKind(f.Kind) {
		return nil, errors.New("invalid_interaction_config")
	}
	pairs, e := s.pairs()
	if e != nil {
		return nil, e
	}
	p, ok := pairs[f.PairID]
	if !ok || p.Status != "active" {
		return nil, errors.New("pair_not_active")
	}
	if f.Enabled {
		c, e := interactionCapabilities(ctx, api, p)
		if e != nil {
			return nil, e
		}
		if !c.Enabled || c.PolicyGeneration != f.PolicyGeneration || !c.Interactions[f.Agent][f.Kind] {
			return nil, errors.New("interaction_policy_unconfirmed")
		}
	}
	unlock, e := s.lock(ctx)
	if e != nil {
		return nil, e
	}
	defer unlock()
	m, e := localInteractions(s)
	if e != nil {
		return nil, e
	}
	if m[f.PairID] == nil {
		m[f.PairID] = map[string]map[string]int64{}
	}
	if m[f.PairID][f.Agent] == nil {
		m[f.PairID][f.Agent] = map[string]int64{}
	}
	delete(m[f.PairID][f.Agent], f.Kind)
	if f.Enabled {
		m[f.PairID][f.Agent][f.Kind] = f.PolicyGeneration
	}
	if e = s.writeJSON("interaction-policy-v1.json", m); e != nil {
		return nil, e
	}
	if !f.Enabled {
		entries, e := os.ReadDir(s.Dir)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if id, ok := interactionFileID(entry.Name()); ok {
				r, e := interactionRead(s, id)
				if e != nil {
					return nil, e
				}
				if r.Agent != f.Agent || r.Kind != f.Kind || r.State != "pending" && r.State != "decision_recorded" {
					continue
				}
				anyEnabled := false
				for pairID := range r.Pairs {
					anyEnabled = anyEnabled || m[pairID][r.Agent][r.Kind] > 0
				}
				if !anyEnabled || r.Frame != nil && r.Frame.PairID == f.PairID {
					r.State = "native_cancelled"
					r.RequestJSON = ""
					if r.Frame != nil {
						r.Frame.ResponseJSON = ""
					}
				}
				if e = s.writeJSON(entry.Name(), r); e != nil {
					return nil, e
				}
			}
		}
	}
	return map[string]any{"v": 1, "enabled": f.Enabled, "configuration_saved": true, "loaded": "unknown"}, nil
}
func interactionCommand(ctx context.Context, s Store, api API, command string, in io.Reader) (any, error) {
	if err := interactionCleanup(ctx, s); err != nil {
		return nil, err
	}
	if command == "configure" {
		return interactionConfigure(ctx, s, api, in)
	}
	if command == "receipt" {
		return interactionReceipt(ctx, s, in)
	}
	if command == "authorize-approval" {
		return interactionAuthorizeApproval(ctx, s, api, in)
	}
	if command == "authorize" {
		return interactionAuthorize(ctx, s, api, in)
	}
	if command != "inspect" && command != "respond" && command != "status" && command != "release" {
		return nil, errors.New("invalid_interaction_command")
	}
	var f InteractionFrame
	if e := readInteractionFrame(ctx, in, &f); e != nil {
		return nil, e
	}
	if f.V != 1 || !opaqueID.MatchString(f.PairID) || !opaqueID.MatchString(f.RequestID) {
		return nil, errors.New("invalid_interaction_frame")
	}
	r, e := interactionRead(s, f.RequestID)
	if e != nil {
		return nil, e
	}
	g, ok := r.Pairs[f.PairID]
	if !ok {
		return nil, errors.New("interaction_pair_mismatch")
	}
	pairs, e := s.pairs()
	if e != nil {
		return nil, e
	}
	p, ok := pairs[f.PairID]
	if !ok || p.Status != "active" || p.InstallationID != r.Locator.InstallationID {
		return nil, errors.New("pair_not_active")
	}
	// Status remains readable after the target exits or a policy is switched off,
	// but only by an original participant with a currently active remote pairing.
	if command == "status" || command == "inspect" && r.State != "pending" {
		if _, e = interactionCapabilities(ctx, api, p); e != nil {
			return nil, e
		}
		return interactionResult(r, f.PairID, command == "inspect"), nil
	}
	mutation := command == "respond" || command == "release"
	if mutation {
		if !opaqueID.MatchString(f.DecisionID) || f.Digest != r.Digest || f.Kind != r.Kind || f.ExpiresAt != r.ExpiresAt || f.RequestRevision != r.Revision || f.PolicyGeneration != g || f.ResponseDigest != digest(f.ResponseJSON) || len(f.ResponseJSON) > interactionAnswerLimit || (command == "release") != (f.Action == "release") {
			return nil, errors.New("interaction_proof_mismatch")
		}
		key, e := decode64(p.EncryptionKey, 32)
		if e != nil {
			return nil, e
		}
		expected, e := interactionMAC(key, f)
		if e != nil || !hmac.Equal([]byte(expected), []byte(f.Proof)) {
			return nil, errors.New("interaction_proof_mismatch")
		}
		if r.Frame != nil {
			if r.Frame.DecisionID == f.DecisionID && r.Frame.PairID == f.PairID && r.Frame.Action == f.Action && r.Frame.ResponseDigest == f.ResponseDigest {
				if _, e = interactionCapabilities(ctx, api, p); e != nil {
					return nil, e
				}
				return interactionResult(r, f.PairID, false), nil
			}
			if r.Frame.DecisionID == f.DecisionID || command != "release" || r.State != "decision_recorded" {
				return nil, errors.New("interaction_conflict")
			}
		}
	}
	if e = interactionEligible(ctx, s, api, p, r, g); e != nil {
		return nil, e
	}
	if e = approvalTarget(ctx, s, r.Locator); e != nil {
		return nil, e
	}
	unlock, e := s.lock(ctx)
	if e != nil {
		return nil, e
	}
	defer unlock()
	r, e = interactionRead(s, f.RequestID)
	if e != nil {
		return nil, e
	}
	if mutation && r.Frame != nil && r.Frame.DecisionID == f.DecisionID && r.Frame.PairID == f.PairID && r.Frame.Action == f.Action && r.Frame.ResponseDigest == f.ResponseDigest {
		return interactionResult(r, f.PairID, false), nil
	}
	if mutation && r.Frame != nil && r.Frame.DecisionID == f.DecisionID {
		return nil, errors.New("interaction_conflict")
	}
	if r.State != "pending" && !(command == "release" && r.State == "decision_recorded") {
		return nil, errors.New("interaction_conflict")
	}
	if time.Now().Unix() >= r.ExpiresAt || !interactionAlive(r) {
		r.State = "expired"
		r.RequestJSON = ""
		_ = s.writeJSON("interaction-"+r.RequestID+".json", r)
		return nil, errors.New("interaction_expired")
	}
	// Recheck local cancellation after network checks and before durable CAS.
	m, e := localInteractions(s)
	if e != nil || m[f.PairID][r.Agent][r.Kind] <= 0 || !interactionCurrentPair(s, p) {
		return nil, errors.New("interaction_disabled")
	}
	if !mutation {
		return interactionResult(r, f.PairID, true), nil
	}
	var spec InteractionSpec
	if interactionJSON([]byte(r.RequestJSON), &spec) != nil {
		return nil, errors.New("invalid_interaction_request")
	}
	if e = interactionAnswers(spec, f.Action, f.ResponseJSON); e != nil {
		return nil, e
	}
	r.Frame = &f
	r.State = "decision_recorded"
	if command == "release" {
		r.State = "released"
		r.RequestJSON = ""
		r.Frame.ResponseJSON = ""
	}
	e = s.writeJSON("interaction-"+r.RequestID+".json", r)
	return interactionResult(r, f.PairID, false), e
}
func interactionFileID(name string) (string, bool) {
	if !strings.HasPrefix(name, "interaction-") || !strings.HasSuffix(name, ".json") || name == "interaction-policy-v1.json" {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, "interaction-"), ".json")
	return id, opaqueID.MatchString(id)
}
func interactionComplete(s Store, id, state string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, e := s.lock(ctx)
	if e != nil {
		return e
	}
	defer unlock()
	r, e := interactionRead(s, id)
	if e != nil {
		return e
	}
	if state == "response_written" || state == "delivery_unknown" {
		if r.State == "native_confirmed" || r.State == "superseded" {
			return nil
		}
		if r.State != "dispatching" {
			return errors.New("interaction_completion_conflict")
		}
	} else if r.State != "pending" && r.State != "decision_recorded" {
		return nil
	}
	r.State = state
	r.RequestJSON = ""
	if r.Frame != nil {
		r.Frame.ResponseJSON = ""
	}
	return s.writeJSON("interaction-"+id+".json", r)
}
func interactionReceipt(ctx context.Context, s Store, in io.Reader) (any, error) {
	var f struct {
		V          int    `json:"v"`
		RequestID  string `json:"request_id"`
		OwnerToken string `json:"owner_token"`
		State      string `json:"state"`
		Evidence   string `json:"evidence,omitempty"`
	}
	if e := readInteractionFrame(ctx, in, &f); e != nil {
		return nil, e
	}
	unlock, e := s.lock(ctx)
	if e != nil {
		return nil, e
	}
	defer unlock()
	r, e := interactionRead(s, f.RequestID)
	if e != nil {
		return nil, e
	}
	birth, e := processIdentity(os.Getppid())
	if e != nil || f.V != 1 || !hmac.Equal([]byte(f.OwnerToken), []byte(r.OwnerToken)) || os.Getppid() != r.ParentPID || birth != r.ParentBirth {
		return nil, errors.New("interaction_owner_mismatch")
	}
	if f.State != "response_written" && f.State != "delivery_unknown" && f.State != "native_cancelled" && f.State != "native_confirmed" && f.State != "superseded" {
		return nil, errors.New("invalid_interaction_receipt")
	}
	if r.State != "dispatching" && !(r.State == "response_written" && (f.State == "native_confirmed" || f.State == "superseded")) {
		return nil, errors.New("interaction_receipt_conflict")
	}
	if (f.State == "native_confirmed" || f.State == "superseded") && (r.Agent != "opencode" && r.Agent != "claude" || f.State == "native_confirmed" && f.Evidence != "question_answer_match" || f.State == "superseded" && f.Evidence != "question_answer_differs") {
		return nil, errors.New("invalid_interaction_evidence")
	}
	r.State = f.State
	r.Evidence = f.Evidence
	r.RequestJSON = ""
	if r.Frame != nil {
		r.Frame.ResponseJSON = ""
	}
	e = s.writeJSON("interaction-"+r.RequestID+".json", r)
	return map[string]any{"v": 1, "state": r.State}, e
}

type interactionSendersKey struct{}

// In-process owners can join notification senders before destroying their store
// or transport. Stop/join all native waiters before calling wait, so no sender
// can be added after the join. Native response delivery itself never waits here.
func withInteractionSenders(ctx context.Context) (context.Context, func()) {
	var senders sync.WaitGroup
	return context.WithValue(ctx, interactionSendersKey{}, &senders), senders.Wait
}

func interactionNativeWait(ctx context.Context, s Store, api API, l Locator, a Activity, spec InteractionSpec) (*InteractionDecision, error) {
	if !interactionKind(spec.Kind) || !interactionAgent(a.Agent) || interactionSchema(spec.Fields) != nil {
		return nil, errors.New("unsupported_interaction_schema")
	}
	configured, e := localInteractions(s)
	if e != nil {
		return nil, e
	}
	enabled := false
	for _, agents := range configured {
		enabled = enabled || agents[a.Agent][spec.Kind] > 0
	}
	if !enabled {
		_, _, _, err := applyActivity(ctx, s, l, a)
		if err == nil {
			startActivityFlush(s)
		}
		return nil, err
	}
	now := time.Now().Unix()
	ttl := spec.TTL
	if ttl <= 0 || ttl > 300*time.Second {
		ttl = 120 * time.Second
	}
	if spec.Kind == "approval" && ttl > 120*time.Second {
		ttl = 120 * time.Second
	}
	expires := now + int64(ttl/time.Second)
	if end, ok := ctx.Deadline(); ok && end.Unix()-3 < expires {
		expires = end.Unix() - 3
	}
	if expires <= now {
		return nil, nil
	}
	birth, e := processIdentity(os.Getpid())
	if e != nil {
		return nil, e
	}
	parentPID := os.Getppid()
	if spec.OwnerPID > 0 {
		parentPID = spec.OwnerPID
	}
	parent, e := processIdentity(parentPID)
	if e != nil {
		return nil, e
	}
	if spec.OwnerPID > 0 && (spec.OwnerBirth == "" || parent != spec.OwnerBirth) {
		return nil, errors.New("interaction_owner_mismatch")
	}
	if a.Run == "" {
		binding := a
		binding.Reminder = ""
		binding.RequestID = ""
		binding.Approval = nil
		binding.Interaction = nil
		_, run, _, e := applyActivity(ctx, s, l, binding)
		if e != nil {
			return nil, e
		}
		a.Run = run.Run
	}
	spec.V = 1
	spec.RequestID = randomID()
	spec.Agent = a.Agent
	spec.SessionID = a.Session
	spec.RunID = a.Run
	spec.Locator = l
	spec.CreatedAt = now
	spec.ExpiresAt = expires
	spec.PID = os.Getpid()
	spec.Birth = birth
	spec.ParentPID = parentPID
	spec.ParentBirth = parent
	spec.RuntimeIdentity = a.Instance
	if spec.Fields == nil {
		spec.Fields = []InteractionField{}
	}
	raw, e := json.Marshal(spec)
	if e != nil {
		return nil, e
	}
	if len(raw) > interactionRequestLimit {
		// No record or dispatch exists yet. This helper owns the sole reminder;
		// the adapter must not retry arbitrary child failures after native I/O.
		return nil, interactionUnavailable(ctx, s, api, l, a, spec, "interaction_request_too_large")
	}
	r := InteractionRecord{V: 1, RequestID: spec.RequestID, Digest: digest(string(raw)), Kind: spec.Kind, Agent: a.Agent, RequestJSON: string(raw), Locator: l, PID: os.Getpid(), Birth: birth, ParentPID: parentPID, ParentBirth: parent, CreatedAt: now, ExpiresAt: expires, Revision: 1, State: "pending", Pairs: map[string]int64{}, OwnerToken: randomID()}
	// Native IDs belong to the live runtime, not to a phone. An absent native ID
	// represents a distinct held Hook invocation and must never merge by content.
	if spec.NativeID != "" {
		identity, _ := json.Marshal([]string{a.Agent, a.Session, a.Run, a.Instance, spec.NativeID, l.InstallationID, l.OccupantID, parent})
		r.NativeIdentity = digest(string(identity))
	}
	pairs, e := s.pairs()
	if e != nil {
		return nil, e
	}
	for id, p := range pairs {
		if configured[id][a.Agent][spec.Kind] <= 0 || p.Status != "active" || p.InstallationID != l.InstallationID {
			continue
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		c, err := interactionCapabilities(check, api, p)
		cancel()
		if err == nil && c.Enabled && c.Interactions[a.Agent][spec.Kind] {
			r.Pairs[id] = c.PolicyGeneration
		}
	}
	if len(r.Pairs) == 0 {
		_, _, _, err := applyActivity(ctx, s, l, a)
		if err == nil {
			startActivityFlush(s)
		}
		return nil, err
	}
	for id := range r.Pairs {
		wire, _ := json.Marshal(interactionResult(r, id, true))
		if len(wire)+1 > interactionFrameLimit {
			return nil, interactionUnavailable(ctx, s, api, l, a, spec, "interaction_request_too_large")
		}
	}
	unlock, e := s.lock(ctx)
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(s.Dir)
	active := 0
	records := 0
	if e == nil {
		for _, entry := range entries {
			if id, ok := interactionFileID(entry.Name()); ok {
				old, err := interactionRead(s, id)
				if err != nil {
					e = err
					break
				}
				records++
				// Never evict a live runtime's replay tombstone to admit another action.
				parentBirth, parentErr := processIdentity(old.ParentPID)
				if old.ExpiresAt+86400 < now && (parentErr != nil || parentBirth != old.ParentBirth) {
					if err = os.Remove(filepath.Join(s.Dir, entry.Name())); err != nil {
						e = err
						break
					}
					records--
					continue
				}
				if r.NativeIdentity != "" && r.NativeIdentity == old.NativeIdentity {
					unlock()
					return nil, errors.New("interaction_already_registered")
				}
				if old.State == "pending" || old.State == "decision_recorded" {
					if old.ExpiresAt > now && interactionAlive(old) {
						active++
					} else {
						old.State = "expired"
						old.RequestJSON = ""
						if old.Frame != nil {
							old.Frame.ResponseJSON = ""
						}
						if err = s.writeJSON(entry.Name(), old); err != nil {
							e = err
							break
						}
					}
				}
			}
		}
	}
	if e == nil && (active >= 8 || records >= 1024) {
		e = errors.New("interaction_capacity")
	}
	if e == nil {
		e = s.writeJSON("interaction-"+r.RequestID+".json", r)
	}
	unlock()
	if e != nil {
		return nil, e
	}
	defer func() { _ = interactionComplete(s, r.RequestID, "native_cancelled") }()
	a.Interaction = &InteractionHint{V: 1, RequestID: r.RequestID, Digest: r.Digest, Kind: r.Kind, ExpiresAt: r.ExpiresAt}
	a.Approval = nil
	a.Occurrence = digest("interaction\x00" + r.RequestID)
	if a.RequestID == "" {
		a.RequestID = r.RequestID
	}
	a.Reminder = "needs_input"
	a.State = "waiting_input"
	if spec.Kind == "approval" {
		a.State = "waiting_approval"
	}
	senders, _ := ctx.Value(interactionSendersKey{}).(*sync.WaitGroup)
	if senders != nil {
		senders.Add(1)
	}
	go func() {
		if senders != nil {
			defer senders.Done()
		}
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = publishActivity(sendCtx, s, api, l, a)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = interactionComplete(s, r.RequestID, "native_cancelled")
			return nil, ctx.Err()
		case <-ticker.C:
		}
		if time.Now().Unix() >= expires || !interactionAlive(r) {
			_ = interactionComplete(s, r.RequestID, "expired")
			return nil, errors.New("interaction_expired")
		}
		current, e := interactionRead(s, r.RequestID)
		if e != nil {
			return nil, e
		}
		if current.State == "released" {
			return &InteractionDecision{RequestID: r.RequestID, Digest: r.Digest, Action: "release", ResponseJSON: "{}", ExpiresAt: expires, OwnerToken: r.OwnerToken}, nil
		}
		if current.State != "pending" && current.State != "decision_recorded" {
			return nil, errors.New("interaction_cancelled")
		}
		policy, e := localInteractions(s)
		if e != nil {
			return nil, e
		}
		anyEnabled := false
		for id := range current.Pairs {
			anyEnabled = anyEnabled || policy[id][a.Agent][spec.Kind] > 0
		}
		if !anyEnabled {
			_ = interactionComplete(s, r.RequestID, "native_cancelled")
			return nil, errors.New("interaction_disabled")
		}
		if current.State != "decision_recorded" {
			continue
		}
		f := current.Frame
		if f == nil {
			return nil, errors.New("invalid_interaction_decision")
		}
		p, ok := pairs[f.PairID]
		if !ok {
			return nil, errors.New("pair_not_active")
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		e = interactionEligible(check, s, api, p, current, current.Pairs[f.PairID])
		if e == nil {
			e = approvalTarget(check, s, l)
		}
		cancel()
		if e != nil {
			_ = interactionComplete(s, r.RequestID, "native_cancelled")
			return nil, e
		}
		unlock, e = s.lock(ctx)
		if e != nil {
			return nil, e
		}
		again, e := interactionRead(s, r.RequestID)
		policy, policyErr := localInteractions(s)
		if e == nil && policyErr == nil && policy[f.PairID][a.Agent][spec.Kind] > 0 && interactionCurrentPair(s, p) && again.State == "decision_recorded" && again.Frame != nil && again.Frame.DecisionID == f.DecisionID && time.Now().Unix() < expires && interactionAlive(again) {
			again.State = "dispatching"
			e = s.writeJSON("interaction-"+r.RequestID+".json", again)
		} else {
			e = errors.New("interaction_dispatch_conflict")
		}
		unlock()
		if e != nil {
			return nil, e
		}
		return &InteractionDecision{RequestID: r.RequestID, Digest: r.Digest, Action: f.Action, ResponseJSON: f.ResponseJSON, ExpiresAt: expires, OwnerToken: r.OwnerToken}, nil
	}
}

func writeInteractionOutput(out io.Writer, s Store, value any) error {
	wrapped, ok := value.(InteractionOutput)
	if !ok {
		if p, yes := value.(*InteractionOutput); yes && p != nil {
			wrapped = *p
			ok = true
		}
	}
	if ok {
		value = wrapped.Value
	}
	raw, e := json.Marshal(value)
	if e == nil && len(raw)+1 > interactionFrameLimit {
		e = errors.New("interaction_output_too_large")
	}
	if e == nil {
		var written int
		written, e = out.Write(append(raw, '\n'))
		if e == nil && written != len(raw)+1 {
			e = io.ErrShortWrite
		}
	}
	if ok && wrapped.RequestID != "" {
		state := "response_written"
		if e != nil {
			state = "delivery_unknown"
		}
		finish := interactionComplete(s, wrapped.RequestID, state)
		if e == nil {
			e = finish
		}
	}
	return e
}

// Kept separate from old approval v3 framing and HMAC domains.
func interactionFrameBytes(v any) []byte {
	raw, _ := json.Marshal(v)
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.BigEndian, uint32(len(raw)))
	b.Write(raw)
	return b.Bytes()
}

// Non-actionable native observations can still expose their complete context
// over authenticated SSH. They never acquire dispatch permission or a waiter.
func interactionUnavailable(ctx context.Context, s Store, api API, l Locator, a Activity, spec InteractionSpec, reason string) error {
	if !interactionKind(spec.Kind) || reason == "" || len(reason) > 256 {
		return errors.New("invalid_interaction_unavailable")
	}
	if a.Run == "" {
		binding := a
		binding.Reminder = ""
		binding.RequestID = ""
		binding.Approval = nil
		binding.Interaction = nil
		_, run, _, err := applyActivity(ctx, s, l, binding)
		if err != nil {
			return err
		}
		a.Run = run.Run
	}
	now := time.Now().Unix()
	spec.V = 1
	spec.RequestID = randomID()
	spec.Agent = a.Agent
	spec.SessionID = a.Session
	spec.RunID = a.Run
	spec.Locator = l
	spec.CreatedAt = now
	spec.ExpiresAt = now + 300
	spec.SupportedActions = []string{}
	if spec.Fields == nil {
		spec.Fields = []InteractionField{}
	}
	raw, e := json.Marshal(spec)
	if e != nil {
		return e
	}
	if len(raw) > interactionRequestLimit {
		// The full native context is not transportable; never display a truncated
		// plan or command as though it were complete. The reason remains visible.
		spec.ToolInput = nil
		spec.NativeInput = nil
		spec.NativeMetadata = nil
		spec.Message = ""
		spec.Fields = []InteractionField{}
		spec.Title = ""
		spec.Scope = ""
		reason = "complete_context_exceeds_limit"
		raw, e = json.Marshal(spec)
		if e != nil {
			return e
		}
	}
	r := InteractionRecord{V: 1, RequestID: spec.RequestID, Digest: digest(string(raw)), Kind: spec.Kind, Agent: a.Agent, RequestJSON: string(raw), Locator: l, CreatedAt: now, ExpiresAt: spec.ExpiresAt, Revision: 1, State: "unavailable", Pairs: map[string]int64{}, UnavailableReason: reason}
	pairs, e := s.pairs()
	if e != nil {
		return e
	}
	for id, p := range pairs {
		if p.Status != "active" || p.InstallationID != l.InstallationID {
			continue
		}
		r.Pairs[id] = 0 // Read-only observation, never dispatch authority.
		if reason == "interaction_request_too_large" || reason == "complete_context_exceeds_limit" {
			// Preserve the enabled interaction's reminder even when the separate
			// general needs_input policy is off. The unavailable state and empty
			// actions still prohibit responding; generation only gates notification.
			configured, err := localInteractions(s)
			if err != nil {
				return err
			}
			if configured[id][a.Agent][spec.Kind] > 0 {
				check, cancel := context.WithTimeout(ctx, 2*time.Second)
				grant, err := interactionCapabilities(check, api, p)
				cancel()
				if err == nil && grant.Enabled && grant.Interactions[a.Agent][spec.Kind] {
					r.Pairs[id] = grant.PolicyGeneration
				}
			}
		}
	}
	if len(r.Pairs) == 0 {
		return publishActivity(ctx, s, api, l, a)
	}
	unlock, e := s.lock(ctx)
	if e != nil {
		return e
	}
	entries, e := os.ReadDir(s.Dir)
	if e == nil {
		count := 0
		for _, entry := range entries {
			if id, ok := interactionFileID(entry.Name()); ok {
				old, err := interactionRead(s, id)
				if err != nil {
					e = err
					break
				}
				if old.State == "unavailable" && old.ExpiresAt < now {
					e = os.Remove(filepath.Join(s.Dir, entry.Name()))
					if e != nil {
						break
					}
				} else {
					count++
				}
			}
		}
		if count >= 1024 {
			e = errors.New("interaction_capacity")
		}
	}
	if e == nil {
		e = s.writeJSON("interaction-"+r.RequestID+".json", r)
	}
	unlock()
	if e != nil {
		return e
	}
	a.Interaction = &InteractionHint{V: 1, RequestID: r.RequestID, Digest: r.Digest, Kind: r.Kind, ExpiresAt: r.ExpiresAt, UnavailableReason: reason}
	a.Approval = nil
	a.Occurrence = digest("unavailable\x00" + r.RequestID)
	if a.State == "" {
		a.State = "waiting_input"
	}
	a.Reminder = "needs_input"
	return publishActivity(ctx, s, api, l, a)
}

func interactionSetExpected(s Store, id, session, nativeID, answerDigest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, e := s.lock(ctx)
	if e != nil {
		return e
	}
	defer unlock()
	r, e := interactionRead(s, id)
	if e != nil {
		return e
	}
	if r.State != "dispatching" || r.Agent != "claude" || r.Kind != "question" || nativeID == "" || len(answerDigest) != 64 || r.PID != os.Getpid() {
		return errors.New("invalid_interaction_evidence")
	}
	r.ExpectedNativeID = nativeID
	r.ExpectedSession = session
	r.ExpectedAnswerDigest = answerDigest
	return s.writeJSON("interaction-"+id+".json", r)
}
func interactionConfirmClaude(s Store, session, toolID string, answersJSON json.RawMessage) error {
	var answers map[string]any
	if interactionJSON(answersJSON, &answers) != nil {
		return errors.New("invalid_interaction_evidence")
	}
	canonical, e := json.Marshal(answers)
	if e != nil {
		return e
	}
	answerDigest := digest(string(canonical))
	birth, e := processIdentity(os.Getppid())
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, e := s.lock(ctx)
	if e != nil {
		return e
	}
	defer unlock()
	entries, e := os.ReadDir(s.Dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if id, ok := interactionFileID(entry.Name()); ok {
			r, e := interactionRead(s, id)
			if e != nil {
				return e
			}
			if r.Agent != "claude" || r.Kind != "question" || r.ExpectedSession != session || r.ExpectedNativeID != toolID || r.ParentPID != os.Getppid() || r.ParentBirth != birth || r.ExpectedAnswerDigest == "" || r.State != "dispatching" && r.State != "response_written" {
				continue
			}
			r.State = "superseded"
			r.Evidence = "question_answer_differs"
			if r.ExpectedAnswerDigest == answerDigest {
				r.State = "native_confirmed"
				r.Evidence = "question_answer_match"
			}
			r.RequestJSON = ""
			if r.Frame != nil {
				r.Frame.ResponseJSON = ""
			}
			if e = s.writeJSON(entry.Name(), r); e != nil {
				return e
			}
		}
	}
	return nil
}

// Regular command entry is the cleanup opportunity; no background daemon is
// needed and expiry never reopens a continuation. Keep the replay identities.
func interactionCleanup(ctx context.Context, s Store) error {
	unlock, e := s.lock(ctx)
	if e != nil {
		return e
	}
	defer unlock()
	entries, e := os.ReadDir(s.Dir)
	if e != nil {
		return e
	}
	now := time.Now().Unix()
	for _, entry := range entries {
		if id, ok := interactionFileID(entry.Name()); ok {
			r, e := interactionRead(s, id)
			if e != nil {
				return e
			}
			if r.ExpiresAt > now {
				continue
			}
			switch r.State {
			case "pending", "decision_recorded", "unavailable":
				r.State = "expired"
			case "dispatching":
				r.State = "delivery_unknown"
			default:
				continue
			}
			r.RequestJSON = ""
			if r.Frame != nil {
				r.Frame.ResponseJSON = ""
			}
			if e = s.writeJSON(entry.Name(), r); e != nil {
				return e
			}
		}
	}
	return nil
}

// The native plugin performs this bounded recheck after its last awaited native
// GET and immediately before the only POST/return. It cannot allocate a new send.
func interactionAuthorize(ctx context.Context, s Store, api API, in io.Reader) (any, error) {
	var f struct {
		V          int    `json:"v"`
		RequestID  string `json:"request_id"`
		OwnerToken string `json:"owner_token"`
	}
	if e := readInteractionFrame(ctx, in, &f); e != nil {
		return nil, e
	}
	r, e := interactionRead(s, f.RequestID)
	if e != nil {
		return nil, e
	}
	birth, e := processIdentity(os.Getppid())
	if e != nil || f.V != 1 || os.Getppid() != r.ParentPID || birth != r.ParentBirth || !hmac.Equal([]byte(f.OwnerToken), []byte(r.OwnerToken)) {
		return nil, errors.New("interaction_owner_mismatch")
	}
	allowed := r.State == "dispatching" && r.Frame != nil && time.Now().Unix() < r.ExpiresAt
	if allowed {
		pairs, err := s.pairs()
		if err != nil {
			return nil, err
		}
		p, ok := pairs[r.Frame.PairID]
		if !ok {
			allowed = false
		} else {
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			e = interactionEligible(check, s, api, p, r, r.Pairs[p.PairID])
			if e == nil {
				e = approvalTarget(check, s, r.Locator)
			}
			cancel()
			allowed = e == nil
		}
	}
	if allowed {
		current, err := interactionRead(s, r.RequestID)
		policy, pe := localInteractions(s)
		allowed = err == nil && pe == nil && current.State == "dispatching" && time.Now().Unix() < r.ExpiresAt && policy[r.Frame.PairID][r.Agent][r.Kind] > 0
	}
	return map[string]any{"v": 1, "allowed": allowed}, nil
}

// Legacy OpenCode allow-once also performs the final native-side policy check.
// Its original held parent is the authority; this never reclaims consumed state.
func interactionAuthorizeApproval(ctx context.Context, s Store, api API, in io.Reader) (any, error) {
	var f struct {
		V         int    `json:"v"`
		RequestID string `json:"request_id"`
		Digest    string `json:"digest"`
	}
	if e := readInteractionFrame(ctx, in, &f); e != nil {
		return nil, e
	}
	r, e := approvalRead(s, f.RequestID)
	if e != nil {
		return nil, e
	}
	birth, e := processIdentity(os.Getppid())
	if e != nil || f.V != 1 || r.Agent != "opencode" || r.ParentPID != os.Getppid() || r.ParentBirth != birth || r.Digest != f.Digest {
		return nil, errors.New("interaction_owner_mismatch")
	}
	allowed := r.State == "consumed" && r.Action == "allow_once" && time.Now().Unix() < r.ExpiresAt
	if allowed {
		pairs, err := s.pairs()
		if err != nil {
			return nil, err
		}
		p, ok := pairs[r.DecidingPair]
		if !ok {
			allowed = false
		} else {
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			e = approvalEligible(check, s, api, p, r, r.Pairs[r.DecidingPair])
			if e == nil {
				e = approvalTarget(check, s, r.Locator)
			}
			cancel()
			allowed = e == nil
		}
	}
	if allowed {
		policy, e := localApprovals(s)
		allowed = e == nil && policy[r.DecidingPair][r.Agent] > 0 && time.Now().Unix() < r.ExpiresAt
	}
	return map[string]any{"v": 1, "allowed": allowed}, nil
}

func interactionCurrentPair(s Store, expected Pair) bool {
	pairs, e := s.pairs()
	if e != nil {
		return false
	}
	current, ok := pairs[expected.PairID]
	return ok && current == expected && current.Status == "active"
}
