package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type codexAsyncHook struct {
	SessionID      string          `json:"session_id"`
	AgentID        string          `json:"agent_id"`
	TurnID         string          `json:"turn_id"`
	ToolUseID      string          `json:"tool_use_id"`
	TranscriptPath string          `json:"transcript_path"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Cwd            string          `json:"cwd"`
}
type codexFollowupStart struct {
	Hook      json.RawMessage `json:"hook"`
	Locator   Locator         `json:"locator"`
	StartedAt int64           `json:"started_at"`
}
type codexThread struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Ephemeral bool   `json:"ephemeral"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
	CanAcceptDirectInput *bool `json:"canAcceptDirectInput"`
}

func codexFollowupSpec(raw []byte) (codexAsyncHook, InteractionSpec, error) {
	var p codexAsyncHook
	var object map[string]json.RawMessage
	spec := InteractionSpec{Kind: "followupMessage", AgentVersion: "0.166.0", AdapterVersion: 1, Title: "Codex follow-up message", Scope: "Send a new user message to the original running Codex thread. This does not answer a blocking request or win a race with messages sent from another terminal.", SupportedActions: []string{"send_message", "release"}, TTL: 300 * time.Second}
	if len(raw) > interactionRequestLimit || interactionJSON(raw, &object) != nil || json.Unmarshal(raw, &p) != nil {
		return p, spec, errors.New("codex_async_contract_unverified")
	}
	spec.ToolName = p.ToolName
	spec.ToolInput = p.ToolInput
	spec.NativeInput = p.ToolInput
	spec.NativeID = p.ToolUseID
	spec.TurnID = p.TurnID
	spec.Cwd = p.Cwd
	if p.HookEventName != "PostToolUse" || p.ToolName != "request_user_input_async" || !nativeInteractionID(p.SessionID) || !nativeInteractionID(p.TurnID) || !nativeInteractionID(p.ToolUseID) || p.AgentID != "" && !nativeInteractionID(p.AgentID) || !filepath.IsAbs(p.TranscriptPath) || strings.ContainsAny(p.TranscriptPath, "\x00\r\n") {
		return p, spec, errors.New("codex_async_contract_unverified")
	}
	// FunctionCallOutputBody serializes text as a string in 0.166.0. Also accept
	// the equivalent object, but never infer success from the mere PostToolUse name.
	response := p.ToolResponse
	var encoded string
	if json.Unmarshal(response, &encoded) == nil {
		response = []byte(encoded)
	}
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	if interactionJSON(response, &accepted) != nil || !accepted.Accepted {
		return p, spec, errors.New("codex_async_result_unverified")
	}
	var input struct {
		Questions []struct {
			Title   string   `json:"title"`
			Options []string `json:"options,omitempty"`
		} `json:"questions"`
	}
	if interactionJSON(p.ToolInput, &input) != nil || len(input.Questions) < 1 || len(input.Questions) > 64 {
		return p, spec, errors.New("codex_async_schema_unverified")
	}
	var text []string
	total := 0
	for _, q := range input.Questions {
		if strings.TrimSpace(q.Title) == "" {
			return p, spec, errors.New("codex_async_schema_unverified")
		}
		text = append(text, q.Title)
		for _, option := range q.Options {
			total++
			if total > 256 || strings.TrimSpace(option) == "" {
				return p, spec, errors.New("codex_async_schema_unverified")
			}
			text = append(text, "- "+option)
		}
		text = append(text, "")
	}
	spec.Message = strings.Join(text, "\n")
	minimum := 1
	maximum := interactionAnswerLimit
	spec.Fields = []InteractionField{{ID: "text", Type: "string", Label: "New message", Required: true, MinLength: &minimum, MaxLength: &maximum}}
	return p, spec, nil
}

// Existing default endpoints only. This mirrors Codex 0.166's guarded Unix
// rendezvous algorithm without creating directories, repairing aliases or scans.
func codexVerifiedSocket(home string) (string, error) {
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return "", err
	}
	return codexVerifiedSocketAt(home, root)
}
func codexVerifiedSocketAt(home, root string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errors.New("codex_trusted_daemon_endpoint_missing")
	}
	parent, err := filepath.EvalSymlinks(filepath.Join(home, "app-server-control"))
	if err != nil {
		return "", errors.New("codex_trusted_daemon_endpoint_missing")
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || (stat.Uid != 0 && int(stat.Uid) != os.Geteuid()) || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
		return "", errors.New("codex_daemon_socket_untrusted")
	}
	advertised := filepath.Join(parent, "app-server-control.sock")
	directory := filepath.Join(root, fmt.Sprintf("codex-daemon-%d", os.Geteuid()))
	info, err = os.Lstat(directory)
	if err != nil {
		return "", errors.New("codex_trusted_daemon_endpoint_missing")
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0700 {
		return "", errors.New("codex_daemon_socket_untrusted")
	}
	physical := filepath.Join(directory, digest(advertised))
	target, err := os.Readlink(advertised)
	if err != nil || target != physical {
		return "", errors.New("codex_daemon_socket_untrusted")
	}
	info, err = os.Lstat(physical)
	if err != nil {
		return "", errors.New("codex_trusted_daemon_endpoint_missing")
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0600 {
		return "", errors.New("codex_daemon_socket_untrusted")
	}
	return physical, nil
}
func codexIsAncestor(pid int) bool {
	current := os.Getpid()
	for depth := 0; depth < 32 && current > 1; depth++ {
		if current == pid {
			return true
		}
		parent, err := codexProcessParent(current)
		if err != nil || parent == current {
			return false
		}
		current = parent
	}
	return false
}
func codexConnect(ctx context.Context) (*codexRPC, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(home, ".codex")
	}
	physical, err := codexVerifiedSocket(home)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "unix", physical)
	if err != nil {
		return nil, errors.New("codex_daemon_unreachable")
	}
	conn := connection.(*net.UnixConn)
	fail := func(e error) (*codexRPC, error) { _ = conn.Close(); return nil, e }
	pid, uid, err := codexSocketPeer(conn)
	if err != nil || uid != os.Geteuid() || !codexIsAncestor(pid) {
		return fail(errors.New("codex_daemon_owner_mismatch"))
	}
	birth, err := processIdentity(pid)
	if err != nil {
		return fail(err)
	}
	// Recheck alias and private socket permissions after connect; peer credentials bind the actual stream.
	again, err := codexVerifiedSocket(home)
	if err != nil || again != physical {
		return fail(errors.New("codex_daemon_socket_untrusted"))
	}
	client, err := codexHandshake(ctx, conn)
	if err != nil {
		return fail(err)
	}
	client.peerPID = pid
	client.peerBirth = birth
	result, err := client.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "mobterm-interaction", "title": "MobTerm interaction", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true, "mcpServerOpenaiFormElicitation": false}})
	if err != nil {
		return fail(err)
	}
	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if json.Unmarshal(result, &initialized) != nil {
		return fail(errors.New("codex_agent_version_unverified"))
	}
	_, version, _ := strings.Cut(initialized.UserAgent, "/")
	parts := strings.Fields(version)
	if len(parts) == 0 || parts[0] != "0.166.0" {
		return fail(errors.New("codex_agent_version_unverified"))
	}
	if err = client.write(1, []byte(`{"method":"initialized"}`)); err != nil {
		return fail(err)
	}
	return client, nil
}
func (c *codexRPC) thread(ctx context.Context, p codexAsyncHook, expected string) (codexThread, error) {
	var selected codexThread
	birth, err := processIdentity(c.peerPID)
	if err != nil || birth != c.peerBirth {
		return selected, errors.New("codex_daemon_owner_changed")
	}
	result, err := c.request(ctx, "thread/loaded/list", map[string]any{"limit": 64})
	if err != nil {
		return selected, err
	}
	var loaded struct {
		Data       []string `json:"data"`
		NextCursor *string  `json:"nextCursor"`
	}
	if json.Unmarshal(result, &loaded) != nil || loaded.NextCursor != nil || len(loaded.Data) > 64 {
		return selected, errors.New("codex_thread_identity_unverified")
	}
	matches := 0
	seen := map[string]bool{}
	for _, id := range loaded.Data {
		if !nativeInteractionID(id) || seen[id] {
			return selected, errors.New("codex_thread_identity_unverified")
		}
		seen[id] = true
		if p.AgentID != "" && id != p.AgentID {
			continue
		}
		if expected != "" && id != expected {
			continue
		}
		result, err = c.request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false})
		if err != nil {
			return selected, err
		}
		var response struct {
			Thread codexThread `json:"thread"`
		}
		if json.Unmarshal(result, &response) != nil {
			return selected, errors.New("codex_thread_identity_unverified")
		}
		candidate := response.Thread
		if candidate.ID == id && candidate.SessionID == p.SessionID && candidate.Path == p.TranscriptPath && !candidate.Ephemeral && (candidate.Status.Type == "active" || candidate.Status.Type == "idle") && candidate.CanAcceptDirectInput != nil && *candidate.CanAcceptDirectInput {
			matches++
			selected = candidate
		}
	}
	if matches != 1 {
		return selected, errors.New("codex_thread_identity_unverified")
	}
	return selected, nil
}

// PostToolUse waits only for bounded identity discovery, never the phone answer.
// The child owns one 300-second request, no listener or future wake-up schedule.
func startCodexFollowup(ctx context.Context, s Store, l Locator, raw []byte) error {
	config, err := localInteractions(s)
	if err != nil {
		return err
	}
	enabled := false
	for _, agents := range config {
		enabled = enabled || agents["codex"]["followupMessage"] > 0
	}
	if !enabled {
		return errors.New("interaction_disabled")
	}
	snapshot, err := json.Marshal(codexFollowupStart{Hook: raw, Locator: l, StartedAt: time.Now().Unix()})
	if err != nil || len(snapshot) > interactionFrameLimit {
		return errors.New("interaction_request_too_large")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "interaction", "codex-followup")
	cmd.Env = append(os.Environ(), "MOBTERM_CONFIG_DIR="+s.Dir)
	cmd.Stdin = bytes.NewReader(snapshot)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(stdout, 32)).ReadString('\n')
		ready <- err == nil && line == "ready\n"
	}()
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	success := false
	select {
	case success = <-ready:
	case <-timer.C:
	case <-ctx.Done():
	}
	_ = stdout.Close()
	if !success {
		_ = cmd.Process.Kill()
	}
	go func() { _ = cmd.Wait() }()
	if !success {
		return errors.New("codex_followup_discovery_unavailable")
	}
	return nil
}
func interactionCodexFollowup(ctx context.Context, s Store, api API, raw []byte) (any, error) {
	var start codexFollowupStart
	if len(raw) > interactionFrameLimit || interactionJSON(raw, &start) != nil || start.StartedAt > time.Now().Unix()+1 || start.StartedAt < time.Now().Unix()-5 {
		return nil, errors.New("invalid_codex_followup")
	}
	p, spec, err := codexFollowupSpec(start.Hook)
	if err != nil {
		return nil, err
	}
	id, err := s.installation()
	if err != nil || start.Locator.InstallationID != id {
		return nil, errors.New("codex_followup_target_mismatch")
	}
	discover, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	client, err := codexConnect(discover)
	if err != nil {
		cancel()
		return nil, err
	}
	defer client.conn.Close()
	thread, err := client.thread(discover, p, "")
	cancel()
	if err != nil {
		return nil, err
	}
	// ACK before the original Hook exits; ancestor/peer binding was checked while
	// the original Hook process was still alive. Later checks use the same socket.
	if _, err = io.WriteString(os.Stdout, "ready\n"); err != nil {
		return nil, err
	}
	bounded, stop := context.WithDeadline(ctx, time.Unix(start.StartedAt+300, 0))
	defer stop()
	spec.OwnerPID = client.peerPID
	spec.OwnerBirth = client.peerBirth
	meta, _ := json.Marshal(map[string]any{"thread_id": thread.ID, "session_id": thread.SessionID, "native_call_id": p.ToolUseID, "turn_id": p.TurnID, "daemon_pid": client.peerPID, "daemon_birth": client.peerBirth, "transcript_path": p.TranscriptPath})
	spec.NativeMetadata = meta
	a := Activity{Agent: "codex", Session: p.SessionID, Run: p.TurnID, Instance: digest(fmt.Sprintf("codex-daemon\x00%d\x00%s\x00%s", client.peerPID, client.peerBirth, thread.ID)), RequestID: p.ToolUseID, State: "waiting_input", Reminder: "needs_input"}
	decision, err := interactionNativeWait(bounded, s, api, start.Locator, a, spec)
	if err != nil || decision == nil {
		return nil, err
	}
	if decision.Action == "release" {
		return nil, nil
	}
	failed := true
	defer func() {
		if failed {
			_ = interactionComplete(s, decision.RequestID, "delivery_unknown")
		}
	}()
	if decision.Action != "send_message" {
		return nil, errors.New("invalid_codex_followup_action")
	}
	// The core has durably claimed dispatching. Any later uncertainty is terminal;
	// do not reconnect, rerun queue/add or treat clientUserMessageId as a dedupe key.
	if _, err = client.thread(bounded, p, thread.ID); err != nil {
		return nil, err
	}
	record, err := interactionRead(s, decision.RequestID)
	if err != nil || record.Frame == nil {
		return nil, errors.New("codex_followup_receipt_missing")
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	pair, ok := pairs[record.Frame.PairID]
	if !ok {
		return nil, errors.New("pair_not_active")
	}
	if err = interactionEligible(bounded, s, api, pair, record, record.Pairs[pair.PairID]); err != nil {
		return nil, err
	}
	if err = approvalTarget(bounded, s, start.Locator); err != nil {
		return nil, err
	}
	// Check the same live owner and local policy after awaited network/native reads.
	record, err = interactionRead(s, decision.RequestID)
	policy, pe := localInteractions(s)
	if err != nil || pe != nil || record.State != "dispatching" || record.Frame == nil || !interactionAlive(record) || time.Now().Unix() >= decision.ExpiresAt || policy[pair.PairID]["codex"]["followupMessage"] <= 0 {
		return nil, errors.New("codex_followup_authorization_changed")
	}
	var response struct {
		Answers map[string]string `json:"answers"`
	}
	if interactionJSON([]byte(decision.ResponseJSON), &response) != nil || strings.TrimSpace(response.Answers["text"]) == "" {
		return nil, errors.New("invalid_codex_followup_answer")
	}
	if err = client.queueFollowup(bounded, thread.ID, record.Frame.DecisionID, response.Answers["text"]); err != nil {
		return nil, err
	}
	if err = interactionComplete(s, decision.RequestID, "response_written"); err != nil {
		return nil, err
	}
	failed = false
	return nil, nil
}

// Exactly one mutation. An absent/malformed ACK is unknown, never retried.
func (c *codexRPC) queueFollowup(ctx context.Context, threadID, decisionID, message string) error {
	result, err := c.request(ctx, "thread/queue/add", map[string]any{"threadId": threadID, "clientUserMessageId": decisionID, "input": []any{map[string]any{"type": "text", "text": message, "text_elements": []any{}}}})
	if err != nil {
		return err
	}
	var queued struct {
		QueuedSubmission struct {
			ID       string `json:"id"`
			ClientID string `json:"clientUserMessageId"`
		} `json:"queuedSubmission"`
	}
	if json.Unmarshal(result, &queued) != nil || !nativeInteractionID(queued.QueuedSubmission.ID) || queued.QueuedSubmission.ClientID != decisionID {
		return errors.New("codex_followup_receipt_unverified")
	}
	return nil
}
