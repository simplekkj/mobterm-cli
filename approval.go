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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type ApprovalRequest struct {
	NativeIdentity string           `json:"native_identity,omitempty"`
	V              int              `json:"v"`
	RequestID      string           `json:"request_id"`
	Digest         string           `json:"digest"`
	Agent          string           `json:"agent"`
	OperationJSON  string           `json:"operation_json"`
	Locator        Locator          `json:"locator"`
	ExpiresAt      int64            `json:"expires_at"`
	CreatedAt      int64            `json:"created_at"`
	PID            int              `json:"pid"`
	Birth          string           `json:"birth"`
	ParentPID      int              `json:"parent_pid"`
	ParentBirth    string           `json:"parent_birth"`
	State          string           `json:"state"`
	Pairs          map[string]int64 `json:"pairs"`
	DecisionID     string           `json:"decision_id,omitempty"`
	Action         string           `json:"action,omitempty"`
	DecidingPair   string           `json:"deciding_pair,omitempty"`
}
type ApprovalFrame struct {
	V                int    `json:"v"`
	PairID           string `json:"pair_id"`
	RequestID        string `json:"request_id"`
	Digest           string `json:"digest,omitempty"`
	DecisionID       string `json:"decision_id,omitempty"`
	Action           string `json:"action,omitempty"`
	PolicyGeneration int64  `json:"policy_generation,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	Proof            string `json:"proof,omitempty"`
}
type ApprovalConfig struct {
	V                int    `json:"v"`
	PairID           string `json:"pair_id"`
	Agent            string `json:"agent"`
	Enabled          bool   `json:"enabled"`
	PolicyGeneration int64  `json:"policy_generation"`
}

func approvalMAC(key []byte, f ApprovalFrame) (string, error) {
	derived, err := hkdf.Key(sha256.New, key, []byte(f.PairID), "mobterm/approval/2", 32)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, derived)
	fmt.Fprintf(m, "mobterm/approval/2\n%s\n%s\n%s\n%s\n%s\n%d\n%d", f.PairID, f.RequestID, f.Digest, f.DecisionID, f.Action, f.PolicyGeneration, f.ExpiresAt)
	return base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}
func approvalRead(s Store, id string) (ApprovalRequest, error) {
	var r ApprovalRequest
	if !opaqueID.MatchString(id) {
		return r, errors.New("invalid_request")
	}
	err := s.readJSON("approval-"+id+".json", &r)
	if err == nil && (r.V != 2 || r.RequestID != id || r.OperationJSON != "" && digest(r.OperationJSON) != r.Digest || (r.State == "pending" || r.State == "decision_recorded") && r.OperationJSON == "" || len(r.OperationJSON) > 8192) {
		err = errors.New("invalid_request")
	}
	return r, err
}
func approvalAlive(r ApprovalRequest) bool {
	a, e := processIdentity(r.PID)
	if e != nil || a != r.Birth {
		return false
	}
	a, e = processIdentity(r.ParentPID)
	return e == nil && a == r.ParentBirth
}
func approvalTarget(ctx context.Context, s Store, l Locator) error {
	raw, _ := json.Marshal(l)
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.BigEndian, uint32(len(raw)))
	buf.Write(raw)
	_, err := resolveTarget(ctx, s, buf)
	return err
}
func localApprovals(s Store) (map[string]map[string]int64, error) {
	m := map[string]map[string]int64{}
	err := s.readJSON("approval-policy-v2.json", &m)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if m == nil {
		return nil, errors.New("invalid_approval_policy")
	}
	return m, err
}
func approvalEligible(ctx context.Context, s Store, api API, p Pair, r ApprovalRequest, generation int64) error {
	if p.Status != "active" || p.InstallationID != r.Locator.InstallationID {
		return errors.New("pair_not_active")
	}
	m, err := localApprovals(s)
	if err != nil || m[p.PairID][r.Agent] <= 0 {
		return errors.New("approval_disabled")
	}
	c, err := runCapabilities(ctx, api, p)
	if err != nil {
		return err
	}
	if !c.Enabled || !c.Inbox || !c.States || c.PolicyGeneration != generation || !c.Approvals[r.Agent] {
		return errors.New("approval_disabled")
	}
	return nil
}
func approvalCommand(ctx context.Context, s Store, api API, command string, in io.Reader) (any, error) {
	if command == "configure" {
		var f ApprovalConfig
		if err := readFrameContext(ctx, in, &f); err != nil {
			return nil, err
		}
		if f.V != 2 || !opaqueID.MatchString(f.PairID) || (f.Agent != "codex" && f.Agent != "claude" && f.Agent != "opencode") {
			return nil, errors.New("invalid_approval_config")
		}
		pairs, err := s.pairs()
		if err != nil {
			return nil, err
		}
		p, ok := pairs[f.PairID]
		if !ok || p.Status != "active" {
			return nil, errors.New("pair_not_active")
		}
		if f.Enabled {
			c, err := runCapabilities(ctx, api, p)
			if err != nil {
				return nil, err
			}
			if !c.Enabled || !c.Approvals[f.Agent] || c.PolicyGeneration != f.PolicyGeneration {
				return nil, errors.New("approval_policy_unconfirmed")
			}
		}
		unlock, err := s.lock(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		m, err := localApprovals(s)
		if err != nil {
			return nil, err
		}
		if m[f.PairID] == nil {
			m[f.PairID] = map[string]int64{}
		}
		delete(m[f.PairID], f.Agent)
		if f.Enabled {
			m[f.PairID][f.Agent] = f.PolicyGeneration
		}
		err = s.writeJSON("approval-policy-v2.json", m)
		if err == nil && !f.Enabled {
			err = releaseDisabledApprovalsLocked(s, m)
		}
		return map[string]any{"v": 2, "enabled": f.Enabled, "configuration_saved": err == nil, "loaded": "unknown", "trust": "native_review_required"}, err
	}
	if command != "inspect" && command != "decide" && command != "release" {
		return nil, errors.New("invalid_approval_command")
	}
	var f ApprovalFrame
	if err := readFrameContext(ctx, in, &f); err != nil {
		return nil, err
	}
	if f.V != 2 || !opaqueID.MatchString(f.PairID) || !opaqueID.MatchString(f.RequestID) {
		return nil, errors.New("invalid_approval_frame")
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	p, ok := pairs[f.PairID]
	if !ok {
		return nil, errors.New("pair_not_active")
	}
	r, err := approvalRead(s, f.RequestID)
	if err != nil {
		return nil, err
	}
	generation, ok := r.Pairs[f.PairID]
	if !ok {
		return nil, errors.New("request_pair_mismatch")
	}
	if command != "inspect" {
		if !opaqueID.MatchString(f.DecisionID) || f.Digest != r.Digest || f.ExpiresAt != r.ExpiresAt || f.PolicyGeneration != generation || ((command == "release") != (f.Action == "release")) || (f.Action != "release" && f.Action != "allow_once" && f.Action != "deny") {
			return nil, errors.New("approval_proof_mismatch")
		}
		key, err := decode64(p.EncryptionKey, 32)
		if err != nil {
			return nil, err
		}
		expected, err := approvalMAC(key, f)
		if err != nil || !hmac.Equal([]byte(expected), []byte(f.Proof)) {
			return nil, errors.New("approval_proof_mismatch")
		}
	}
	if err = approvalEligible(ctx, s, api, p, r, generation); err != nil {
		return nil, err
	}
	if err = approvalTarget(ctx, s, r.Locator); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err = approvalRead(s, f.RequestID)
	if err != nil {
		return nil, err
	}
	if command != "inspect" && r.DecisionID == f.DecisionID && r.Action == f.Action && r.DecidingPair == f.PairID {
		return approvalResult(r, f.PairID, generation, false), nil
	}
	if r.State != "pending" {
		return nil, errors.New("approval_conflict")
	}
	if time.Now().Unix() >= r.ExpiresAt || !approvalAlive(r) {
		return nil, errors.New("approval_expired")
	}
	if command == "inspect" {
		return approvalResult(r, f.PairID, generation, true), nil
	}
	r.DecisionID = f.DecisionID
	r.Action = f.Action
	r.DecidingPair = f.PairID
	r.State = "decision_recorded"
	if command == "release" {
		r.State = "released"
		r.OperationJSON = ""
	}
	err = s.writeJSON("approval-"+r.RequestID+".json", r)
	return approvalResult(r, f.PairID, generation, false), err
}
func approvalResult(r ApprovalRequest, pair string, generation int64, inspect bool) map[string]any {
	v := map[string]any{"v": 2, "pair_id": pair, "request_id": r.RequestID, "digest": r.Digest, "expires_at": r.ExpiresAt, "policy_generation": generation, "state": r.State, "agent": r.Agent}
	if inspect {
		v["operation_json"] = r.OperationJSON
		v["allowed_decisions"] = []string{"allow_once", "deny"}
	}
	return v
}
func approvalInputSupported(agent string, p activityInput) bool {
	if len(p.ToolInput) == 0 || len(p.ToolInput) > 6144 {
		return false
	}
	var input map[string]any
	if json.Unmarshal(p.ToolInput, &input) != nil {
		return false
	}
	required := []string{}
	switch p.ToolName {
	case "permission":
		if agent != "opencode" {
			return false
		}
		if _, ok := input["action"].(string); !ok {
			return false
		}
		resources, ok := input["resources"].([]any)
		if !ok {
			return false
		}
		for _, resource := range resources {
			if _, ok := resource.(string); !ok {
				return false
			}
		}
		return true
	case "Bash":
		required = []string{"command"}
	case "apply_patch":
		if agent != "codex" {
			return false
		}
		required = []string{"command"}
	case "Write":
		required = []string{"file_path", "content"}
	case "Edit":
		required = []string{"file_path", "old_string", "new_string"}
	default:
		return false
	}
	for _, key := range required {
		if _, ok := input[key].(string); !ok {
			return false
		}
	}
	return true
}
func approvalWait(ctx context.Context, s Store, api API, l Locator, a Activity, input activityInput) (any, error) {
	// Opt-in is local as well as remote: disabled hooks never wait on an API lookup.
	configured, err := localApprovals(s)
	if err != nil {
		return nil, err
	}
	enabled := false
	for _, agents := range configured {
		if agents[a.Agent] > 0 {
			enabled = true
		}
	}
	if !enabled || !approvalInputSupported(a.Agent, input) {
		// Disabled approval observes locally and returns immediately; it never holds
		// the native prompt waiting for network delivery.
		_, _, _, err := applyActivity(ctx, s, l, a)
		if err == nil {
			startActivityFlush(s)
		}
		return nil, err
	}
	created := time.Now().Unix()
	expires := created + 120
	if end, ok := ctx.Deadline(); ok && end.Unix()-3 < expires {
		expires = end.Unix() - 3
	}
	if expires <= created {
		return nil, nil
	}
	id := randomID()
	birth, err := processIdentity(os.Getpid())
	if err != nil {
		return nil, err
	}
	parentBirth, err := processIdentity(os.Getppid())
	if err != nil {
		return nil, err
	}
	if a.Run == "" { // Bind to the persisted current run before creating the request.
		_, r, _, err := applyActivity(ctx, s, l, a)
		if err != nil {
			return nil, err
		}
		a.Run = r.Run
	}
	var semanticInput any
	decoder := json.NewDecoder(bytes.NewReader(input.ToolInput))
	decoder.UseNumber()
	if decoder.Decode(&semanticInput) != nil {
		return nil, nil
	}
	operation := map[string]any{"agent": a.Agent, "session_id": a.Session, "run_id": a.Run, "request_id": id, "native_request_id": input.ToolUseID, "locator": l, "tool_name": input.ToolName, "tool_input": semanticInput, "allowed_decisions": []string{"allow_once", "deny"}, "created_at": created, "expires_at": expires}
	raw, err := json.Marshal(operation)
	if err != nil || len(raw) > 8192 {
		return nil, nil
	}
	r := ApprovalRequest{V: 2, RequestID: id, Digest: digest(string(raw)), Agent: a.Agent, OperationJSON: string(raw), Locator: l, ExpiresAt: expires, CreatedAt: created, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parentBirth, State: "pending", Pairs: map[string]int64{}}
	if input.ToolUseID != "" {
		r.NativeIdentity = digest(a.Agent + "\x00" + a.Session + "\x00" + a.Run + "\x00" + input.ToolUseID + "\x00" + l.InstallationID + "\x00" + l.OccupantID)
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	for pid, p := range pairs {
		g := configured[pid][a.Agent]
		if g > 0 {
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			c, e := runCapabilities(check, api, p)
			cancel()
			if e == nil && p.Status == "active" && p.InstallationID == l.InstallationID && c.Enabled && c.States && c.Inbox && c.Approvals[a.Agent] {
				r.Pairs[pid] = c.PolicyGeneration
			}
		}
	}
	if len(r.Pairs) == 0 {
		return nil, nil
	}
	for pid, generation := range r.Pairs {
		wire, _ := json.Marshal(approvalResult(r, pid, generation, true))
		if len(wire) > maxFrame {
			return nil, nil
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Dir)
	count := 0
	if err == nil {
		for _, entry := range entries {
			if stringsHasApproval(entry.Name()) {
				old, e := approvalRead(s, entry.Name()[9:len(entry.Name())-5])
				if e == nil && old.ExpiresAt+3600 < created {
					_ = os.Remove(filepath.Join(s.Dir, entry.Name()))
					continue
				}
				if e == nil && r.NativeIdentity != "" && old.NativeIdentity == r.NativeIdentity {
					unlock()
					return nil, nil
				}
				if e == nil && old.ExpiresAt <= created && old.OperationJSON != "" {
					old.State = "expired"
					old.OperationJSON = ""
					if e = s.writeJSON(entry.Name(), old); e != nil {
						err = e
						break
					}
				}
				if e == nil && old.ExpiresAt > created && (old.State == "pending" || old.State == "decision_recorded") {
					count++
				}
			}
		}
	}
	if err == nil && (count >= 8 || len(entries) > 1100) {
		err = errors.New("approval_capacity")
	}
	if err == nil {
		err = s.writeJSON("approval-"+id+".json", r)
	}
	unlock()
	if err != nil {
		return nil, err
	}
	a.Approval = &ApprovalHint{id, r.Digest, expires}
	a.Occurrence = digest("approval\x00" + id)
	// Notification IO has a separate short budget and cannot block the consumer.
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = publishActivity(sendCtx, s, api, l, a)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, finishApproval(s, id, "cancelled")
		case <-ticker.C:
		}
		if time.Now().Unix() >= expires {
			return nil, finishApproval(s, id, "expired")
		}
		current, e := approvalRead(s, id)
		if e != nil {
			return nil, e
		}
		if current.State == "released" {
			return nil, nil
		}
		configured, e := localApprovals(s)
		if e != nil {
			return nil, finishApproval(s, id, "cancelled")
		}
		anyEnabled := false
		for pid := range current.Pairs {
			if configured[pid][current.Agent] > 0 {
				anyEnabled = true
			}
		}
		if !anyEnabled {
			return nil, finishApproval(s, id, "released")
		}
		if current.State != "decision_recorded" {
			continue
		}
		p, ok := pairs[current.DecidingPair]
		if !ok {
			return nil, finishApproval(s, id, "cancelled")
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		e = approvalEligible(check, s, api, p, current, current.Pairs[current.DecidingPair])
		if e == nil {
			e = approvalTarget(check, s, l)
		}
		cancel()
		if e != nil {
			return nil, finishApproval(s, id, "cancelled")
		}
		unlock, e = s.lock(ctx)
		if e != nil {
			return nil, e
		}
		again, e := approvalRead(s, id)
		if e == nil && again.State == "decision_recorded" && again.DecisionID == current.DecisionID && time.Now().Unix() < expires && approvalAlive(again) {
			again.State = "consumed"
			again.OperationJSON = ""
			e = s.writeJSON("approval-"+id+".json", again)
		} else {
			e = errors.New("approval_consume_conflict")
		}
		unlock()
		if e != nil {
			return nil, e
		}
		behavior := "deny"
		if current.Action == "allow_once" {
			behavior = "allow"
		}
		result := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]any{"behavior": behavior}}}
		if a.Agent == "opencode" {
			result["expires_at"] = expires
			result["request_id"] = id
			result["digest"] = r.Digest
		}
		return result, nil
	}
}

// A bounded child drains this invocation's durable intent without holding the
// synchronous native permission prompt. It exits after five seconds, opens no
// listener and schedules no future wakeups.
func startActivityFlush(s Store) {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(executable, "activity", "flush")
	cmd.Env = append(os.Environ(), "MOBTERM_CONFIG_DIR="+s.Dir)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
func stringsHasApproval(name string) bool {
	return len(name) > 14 && name[:9] == "approval-" && name[len(name)-5:] == ".json" && name != "approval-policy-v2.json"
}
func finishApproval(s Store, id, state string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := approvalRead(s, id)
	if err != nil {
		return err
	}
	if r.State == "pending" || r.State == "decision_recorded" {
		r.State = state
		r.OperationJSON = ""
		return s.writeJSON("approval-"+id+".json", r)
	}
	return nil
}

// Caller owns Store.lock. The configuration write precedes this sweep; waiters
// also recheck that durable policy, so a crash cannot preserve old authority.
func releaseDisabledApprovalsLocked(s Store, configured map[string]map[string]int64) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !stringsHasApproval(entry.Name()) {
			continue
		}
		r, err := approvalRead(s, entry.Name()[9:len(entry.Name())-5])
		if err != nil {
			return err
		}
		if r.State != "pending" && r.State != "decision_recorded" {
			continue
		}
		enabled := false
		for pid := range r.Pairs {
			if configured[pid][r.Agent] > 0 {
				enabled = true
			}
		}
		if !enabled {
			r.State = "released"
			r.OperationJSON = ""
			if err = s.writeJSON(entry.Name(), r); err != nil {
				return err
			}
		}
	}
	return nil
}
