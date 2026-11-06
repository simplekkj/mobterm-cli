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
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type ApprovalRequest struct {
	Session        string           `json:"session,omitempty"`
	Run            string           `json:"run,omitempty"`
	Instance       string           `json:"instance,omitempty"`
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
	if err == nil && ((r.V != 2 && r.V != 3) || r.RequestID != id || r.OperationJSON != "" && digest(r.OperationJSON) != r.Digest || (r.State == "pending" || r.State == "decision_recorded") && r.OperationJSON == "" || len(r.OperationJSON) > 8192) {
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
		defer func() {
			unlock()
			if err == nil && !f.Enabled {
				startActivityFlush(s)
			}
		}()
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
	if f.V != 3 || !opaqueID.MatchString(f.PairID) || !opaqueID.MatchString(f.RequestID) {
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
	if r.V != 3 {
		return nil, errors.New("approval_context_upgrade_required")
	}
	generation, ok := r.Pairs[f.PairID]
	if !ok {
		return nil, errors.New("request_pair_mismatch")
	}
	if command != "inspect" {
		if r.Agent == "opencode" && f.Action == "deny" {
			return nil, errors.New("approval_batch_scope_requires_interaction")
		}
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
		if err.Error() == "approval_disabled" {
			if settleErr := revokeApprovalPair(s, r.RequestID, p.PairID); settleErr != nil {
				return nil, settleErr
			}
			_ = startActivityFlush(s)
		}
		return nil, err
	}
	if err = approvalTarget(ctx, s, r.Locator); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		unlock()
		if err == nil && command == "release" {
			startActivityFlush(s)
		}
	}()
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
	if time.Now().Unix() >= r.ExpiresAt || !approvalAlive(r) || !approvalRunWaiting(s, r) {
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
	if err == nil && command == "release" {
		err = resolveApprovalActivityLocked(s, r)
	}
	return approvalResult(r, f.PairID, generation, false), err
}
func approvalDecisions(agent string) []string {
	if agent == "opencode" {
		return []string{"allow_once"}
	}
	return []string{"allow_once", "deny"}
}
func approvalResult(r ApprovalRequest, pair string, generation int64, inspect bool) map[string]any {
	v := map[string]any{"v": 3, "pair_id": pair, "request_id": r.RequestID, "digest": r.Digest, "expires_at": r.ExpiresAt, "policy_generation": generation, "state": r.State, "agent": r.Agent}
	if inspect {
		v["operation_json"] = r.OperationJSON
		v["allowed_decisions"] = approvalDecisions(r.Agent)
	}
	return v
}

// Use only the native request's absolute execution directory, never process cwd
// or a cached project/terminal title. Preserve its exact bytes in the digest.
func approvalCwdValid(cwd string) bool {
	return strings.HasPrefix(cwd, "/") && len(cwd) <= 4096 && strings.IndexFunc(cwd, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf) }) < 0
}
func approvalInputSupported(agent string, p activityInput) bool {
	if (agent == "codex" || agent == "claude") && !approvalCwdValid(p.Cwd) {
		return false
	}
	if len(p.ToolInput) == 0 || len(p.ToolInput) > 6144 {
		return false
	}
	var input map[string]any
	if interactionJSON(p.ToolInput, &input) != nil {
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
		if agent == "codex" && strings.HasPrefix(p.ToolName, "mcp__") {
			parts := strings.SplitN(strings.TrimPrefix(p.ToolName, "mcp__"), "__", 2)
			return len(parts) == 2 && nativeInteractionID(parts[0]) && nativeInteractionID(parts[1])
		}
		return false
	}
	for _, key := range required {
		if _, ok := input[key].(string); !ok {
			return false
		}
	}
	return true
}
func approvalOperation(l Locator, a Activity, input activityInput, id string, created, expires int64) ([]byte, error) {
	if !approvalInputSupported(a.Agent, input) {
		return nil, errors.New("invalid_approval_context")
	}
	var semanticInput any
	decoder := json.NewDecoder(bytes.NewReader(input.ToolInput))
	decoder.UseNumber()
	if decoder.Decode(&semanticInput) != nil {
		return nil, errors.New("invalid_approval_context")
	}
	operation := map[string]any{"context_version": 3, "agent": a.Agent, "session_id": a.Session, "run_id": a.Run, "request_id": id, "native_request_id": input.ToolUseID, "locator": l, "tool_name": input.ToolName, "tool_input": semanticInput, "allowed_decisions": approvalDecisions(a.Agent), "created_at": created, "expires_at": expires}
	if a.Agent == "codex" || a.Agent == "claude" {
		operation["cwd"] = input.Cwd
	}
	raw, err := json.Marshal(operation)
	if err != nil || len(raw) > 8192 {
		return nil, errors.New("invalid_approval_context")
	}
	return raw, nil
}
func approvalWait(ctx context.Context, s Store, api API, l Locator, a Activity, input activityInput) (result any, resultErr error) {
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
	if a.Agent == "codex" {
		a.State, a.Reminder = "tool_pending", ""
		a.Approval, a.RequestID = nil, ""
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
		binding := a
		binding.Reminder = ""
		binding.RequestID = ""
		_, r, _, err := applyActivity(ctx, s, l, binding)
		if err != nil {
			return nil, err
		}
		a.Run = r.Run
	}
	raw, err := approvalOperation(l, a, input, id, created, expires)
	if err != nil {
		return nil, nil
	}
	r := ApprovalRequest{Session: a.Session, Run: a.Run, Instance: a.Instance, V: 3, RequestID: id, Digest: digest(string(raw)), Agent: a.Agent, OperationJSON: string(raw), Locator: l, ExpiresAt: expires, CreatedAt: created, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parentBirth, State: "pending", Pairs: map[string]int64{}}
	if input.ToolUseID != "" {
		r.NativeIdentity = digest(a.Agent + "\x00" + a.Session + "\x00" + a.Run + "\x00" + input.ToolUseID + "\x00" + l.InstallationID + "\x00" + l.OccupantID)
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	for pid, p := range pairs {
		g := configured[pid][a.Agent]
		if g > 0 && p.Status == "active" && p.InstallationID == l.InstallationID {
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
	if err = approvalTarget(ctx, s, l); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	// Recheck local authority under the same lock as request registration.
	configured, err = localApprovals(s)
	if err != nil {
		unlock()
		return nil, err
	}
	for pid := range r.Pairs {
		if configured[pid][a.Agent] <= 0 {
			delete(r.Pairs, pid)
		}
	}
	if len(r.Pairs) == 0 {
		unlock()
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	count := 0
	if err == nil {
		for _, entry := range entries {
			if stringsHasApproval(entry.Name()) {
				old, e := approvalRead(s, entry.Name()[9:len(entry.Name())-5])
				if e == nil && old.ExpiresAt+3600 < created {
					if e = resolveApprovalActivityLocked(s, old); e != nil {
						err = e
						break
					}
					_ = os.Remove(filepath.Join(s.Dir, entry.Name()))
					continue
				}
				if e == nil && r.NativeIdentity != "" && old.NativeIdentity == r.NativeIdentity {
					unlock()
					return nil, nil
				}
				if e == nil && old.ExpiresAt <= created {
					if old.OperationJSON != "" {
						old.State = "expired"
						old.OperationJSON = ""
						if e = s.writeJSON(entry.Name(), old); e != nil {
							err = e
							break
						}
					}
					if e = resolveApprovalActivityLocked(s, old); e != nil {
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
	if err == nil {
		a.State, a.Reminder = "waiting_approval", "needs_input"
		a.RequestID = id
		a.Approval = &ApprovalHint{id, r.Digest, expires}
		a.Occurrence = digest("approval\x00" + id)
		var changed bool
		_, _, changed, err = applyActivityLocked(s, l, a)
		if err == nil && !changed {
			r.State, r.OperationJSON = "cancelled", ""
			err = s.writeJSON("approval-"+id+".json", r)
			unlock()
			return nil, err
		}
	}
	unlock()
	if err != nil {
		_ = finishApproval(s, id, "cancelled")
		return nil, err
	}
	// Persist both the waiter and its waiting state before any sender can run.
	// Every return path settles this request, including already-consumed/released.
	defer func() {
		if err := finishApproval(s, id, "cancelled"); err != nil && resultErr == nil {
			result, resultErr = nil, err
		}
		startActivityFlush(s)
	}()
	startActivityFlush(s)
	lastValidation := time.Now()
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
		if current.State != "pending" && current.State != "decision_recorded" {
			return nil, finishApproval(s, id, current.State)
		}
		if !approvalAlive(current) || !approvalRunWaiting(s, current) {
			return nil, finishApproval(s, id, "cancelled")
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
			if time.Since(lastValidation) >= time.Second {
				lastValidation = time.Now()
				check, cancel := context.WithTimeout(ctx, 2*time.Second)
				e = approvalTarget(check, s, l)
				cancel()
				if e != nil {
					return nil, finishApproval(s, id, "cancelled")
				}
			}
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
		if e == nil && again.State == "decision_recorded" && again.DecisionID == current.DecisionID && time.Now().Unix() < expires && approvalAlive(again) && approvalRunWaiting(s, again) {
			again.State = "consumed"
			again.OperationJSON = ""
			e = s.writeJSON("approval-"+id+".json", again)
			if e == nil {
				e = resolveApprovalActivityLocked(s, again)
			}
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
		if err = s.writeJSON("approval-"+id+".json", r); err != nil {
			return err
		}
	}
	return resolveApprovalActivityLocked(s, r)
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
			if err = resolveApprovalActivityLocked(s, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// New requests carry exact run attribution. Legacy records without it cannot
// safely identify a pending request and are deliberately left untouched.
func resolveApprovalActivityLocked(s Store, r ApprovalRequest) error {
	if r.Session == "" || r.Run == "" || r.Instance == "" {
		return nil
	}
	_, _, _, err := applyActivityLocked(s, r.Locator, Activity{Agent: r.Agent, Session: r.Session, Run: r.Run, Instance: r.Instance, RequestID: r.RequestID, ResolveRequest: true, State: "running", Occurrence: digest("approval-resolved\x00" + r.RequestID)})
	return err
}

// Notification suppression never rewrites an existing envelope. A consumed,
// expired or otherwise invalid waiter cannot produce a fresh needs_input alert.
func approvalNotificationValid(s Store, hint *ApprovalHint, pair string) bool {
	r, err := approvalRead(s, hint.RequestID)
	if err != nil || r.State != "pending" || r.Digest != hint.Digest || r.ExpiresAt != hint.ExpiresAt || r.ExpiresAt <= time.Now().Unix() || !approvalAlive(r) || r.Pairs[pair] <= 0 || !approvalRunWaiting(s, r) {
		return false
	}
	configured, err := localApprovals(s)
	return err == nil && configured[pair][r.Agent] > 0
}

func reconcileApprovalHint(s Store, hint *ApprovalHint) error {
	r, err := approvalRead(s, hint.RequestID)
	if err != nil {
		return nil // Unknown legacy provenance is not permission to rewrite runs.
	}
	if r.State != "pending" && r.State != "decision_recorded" {
		return finishApproval(s, r.RequestID, r.State)
	}
	if r.ExpiresAt <= time.Now().Unix() {
		return finishApproval(s, r.RequestID, "expired")
	}
	if !approvalAlive(r) || !approvalRunWaiting(s, r) {
		return finishApproval(s, r.RequestID, "cancelled")
	}
	configured, err := localApprovals(s)
	if err != nil {
		return err
	}
	for pid := range r.Pairs {
		if configured[pid][r.Agent] > 0 {
			return nil
		}
	}
	return finishApproval(s, r.RequestID, "released")
}

// A verified policy failure belongs to one pair. Preserve other phones' still
// valid authority and settle the run only once no pair can answer this request.
func revokeApprovalPair(s Store, id, pair string) error {
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
	if r.State != "pending" && r.State != "decision_recorded" {
		return resolveApprovalActivityLocked(s, r)
	}
	delete(r.Pairs, pair)
	if len(r.Pairs) == 0 {
		r.State, r.OperationJSON = "released", ""
	}
	if err = s.writeJSON("approval-"+id+".json", r); err != nil {
		return err
	}
	if r.State == "released" {
		return resolveApprovalActivityLocked(s, r)
	}
	return nil
}

func approvalRunWaiting(s Store, r ApprovalRequest) bool {
	if r.Session == "" || r.Run == "" || r.Instance == "" {
		return true // Historical records lack sufficient attribution to guess.
	}
	db, err := loadRuns(s)
	if err != nil {
		return false
	}
	target, _ := json.Marshal(r.Locator)
	instance := digest(r.Agent + "\x00" + r.Session + "\x00" + r.Instance + "\x00" + string(target))
	run, ok := db.Runs[digest(instance+"\x00"+r.Run)]
	request := run.Requests[r.RequestID]
	return ok && !terminalRun(run.State) && request.Approval != nil && request.Approval.Digest == r.Digest
}
