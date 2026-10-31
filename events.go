package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type hookInput struct {
	Type             string `json:"type"`
	ThreadID         string `json:"thread-id"`
	TurnID           string `json:"turn-id"`
	SessionID        string `json:"session_id"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
}
type claudeCycle struct {
	Cycle   string `json:"cycle"`
	Updated int64  `json:"updated"`
}

func parseEvent(ctx context.Context, s Store, source string, payload []byte) (kind, identity string, err error) {
	if len(payload) > 1024*1024 {
		return "", "", errors.New("hook input limit exceeded")
	}
	if source == "opencode" || source == "pi" {
		return parseNativeEvent(source, payload)
	}
	var p hookInput
	if json.Unmarshal(payload, &p) != nil {
		return "", "", errors.New("invalid hook JSON")
	}
	switch source {
	case "codex":
		if p.Type != "agent-turn-complete" {
			return "", "", nil
		}
		if len(p.ThreadID) == 0 || len(p.ThreadID) > 256 || len(p.TurnID) == 0 || len(p.TurnID) > 256 {
			return "", "", errors.New("Codex event lacks stable thread/turn identity")
		}
		return "turn_completed", digest("codex\x00" + p.ThreadID + "\x00" + p.TurnID), nil
	case "claude":
		if p.SessionID == "" || len(p.SessionID) > 256 {
			return "", "", errors.New("Claude hook lacks session identity")
		}
		if p.HookEventName != "UserPromptSubmit" && p.HookEventName != "Notification" {
			return "", "", nil
		}
		key := digest(p.SessionID)
		if p.HookEventName == "Notification" {
			switch p.NotificationType {
			case "permission_prompt", "elicitation_dialog":
				// A native human wait does not depend on a recorded prompt cycle.
				// Upstream has no occurrence ID: each invocation gets a fresh identity,
				// held in the bounded envelope cache before any HTTP retries.
				return "needs_input", digest("claude\x00" + key + "\x00input\x00" + randomID()), nil
			case "idle_prompt":
				// Completion still requires a live prompt association below.
			default:
				return "", "", nil
			}
		}
		unlock, err := s.lock(ctx)
		if err != nil {
			return "", "", err
		}
		defer unlock()
		sessions := map[string]claudeCycle{}
		err = s.readJSON("claude-cycles.json", &sessions)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		now := time.Now().Unix()
		for k, v := range sessions {
			if v.Updated < now-86400 {
				delete(sessions, k)
			}
		}
		cycle := sessions[key]
		switch p.HookEventName {
		case "UserPromptSubmit":
			if len(sessions) >= 256 && cycle.Cycle == "" {
				return "", "", errors.New("Claude session association limit reached")
			}
			cycle = claudeCycle{Cycle: randomID(), Updated: now}
			sessions[key] = cycle
			return "", "", s.writeJSON("claude-cycles.json", sessions)
		case "Notification":
			if cycle.Cycle == "" {
				return "", "", nil
			}
			return "turn_completed", digest("claude\x00" + key + "\x00" + cycle.Cycle + "\x00completed"), nil
		}
	}
	return "", "", errors.New("unsupported event source")
}

type cachedEnvelope struct {
	Envelope    Envelope `json:"envelope"`
	LocatorHash string   `json:"locator_hash"`
}

func eventEnvelope(ctx context.Context, s Store, p Pair, kind, identity string, l Locator) (Envelope, error) {
	d, err := enqueueDelivery(ctx, s, p, kind, identity, l, "")
	if err != nil {
		return Envelope{}, err
	}
	if d.Envelope == nil {
		return Envelope{}, errors.New("source_event_already_processed")
	}
	return *d.Envelope, nil
}
func notify(ctx context.Context, s Store, a API, source string, payload []byte, pairID string, test bool) ([]map[string]any, error) {
	if !test {
		var metadata struct {
			AdapterRevision int    `json:"adapter_revision"`
			Type            string `json:"type"`
			HookEventName   string `json:"hook_event_name"`
		}
		_ = json.Unmarshal(payload, &metadata)
		var managed HookRecord
		_ = s.readJSON("hooks-"+source+".json", &managed)
		rich := source == "codex" && (managed.CodexNative != "" || managed.CodexJSON != "") || source == "claude" && managed.ClaudeOwned["PermissionRequest"] != ""
		if metadata.AdapterRevision == 2 || rich && (source == "codex" && metadata.Type == "agent-turn-complete" || source == "claude" && (metadata.HookEventName == "UserPromptSubmit" || metadata.HookEventName == "Notification")) {
			if metadata.AdapterRevision == 2 || source == "claude" && metadata.HookEventName == "UserPromptSubmit" {
				short, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				ctx = short
			}
			_, e := observe(ctx, s, a, source, payload)
			return nil, e
		}
	}
	id, err := s.installation()
	if err != nil {
		return nil, err
	}
	kind, identity := "test", randomID()
	if !test {
		if source != "opencode" && source != "pi" {
			if err = hookVersions(ctx, source); err != nil {
				return nil, err
			}
		}
		kind, identity, err = parseEvent(ctx, s, source, payload)
		if err != nil {
			return nil, err
		}
		if kind == "" {
			return nil, nil
		}
	}
	l := Locator{V: 1, InstallationID: id, Backend: "test"}
	if !test {
		l, err = captureTarget(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	nativeSource := ""
	if !test && (source == "opencode" || source == "pi") {
		nativeSource = identity
		identity = nativeTargetIdentity(identity, l)
	}
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	var results []map[string]any
	var pending []delivery
	var lastErr error
	// Finish durable fan-out to every eligible pair before performing any network IO.
	for pid, p := range pairs {
		if pairID != "" && pid != pairID {
			continue
		}
		if p.InstallationID != id || p.Status != "active" {
			continue
		}
		d, e := enqueueDelivery(ctx, s, p, kind, identity, l, source, nativeSource)
		if e != nil {
			lastErr = e
			results = append(results, map[string]any{"v": 1, "pair_id": pid, "state": "not_saved", "error": safeCode(e.Error())})
			continue
		}
		if d.State == "pending" {
			pending = append(pending, d)
		} else {
			results = append(results, deliveryResult(d))
		}
	}
	for _, d := range pending {
		if ctx.Err() != nil {
			results = append(results, deliveryResult(d))
			lastErr = ctx.Err()
			continue
		}
		v, e := deliverOne(ctx, s, a, d)
		if e != nil {
			lastErr = e
		}
		results = append(results, v)
	}
	if len(results) == 0 && lastErr == nil {
		lastErr = errors.New("no matching active local pair")
	}
	// New deliveries have priority. Old due work only consumes a remaining budget.
	if deadline, ok := ctx.Deadline(); !test && ok && time.Until(deadline) > time.Second {
		_, _ = flushOutbox(ctx, s, a, "", false)
	}

	return results, lastErr
}

// Preserve the original notify argv, its appended input and its exit/output contract. Never eval it.
func runNotifyChain(s Store, payload string) int {
	var r HookRecord
	if err := s.readJSON("hooks-codex.json", &r); err != nil {
		return 0
	}
	if len(r.Chain) == 0 {
		return 0
	}
	for _, arg := range r.Chain {
		if arg == "--mobterm-chain" || filepath.Base(arg) == "mobterm-cli" {
			return 1
		}
	}
	cmd := exec.Command(r.Chain[0], append(r.Chain[1:], payload)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}
func readHook(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, 1024*1024+1))
	if err != nil || len(b) > 1024*1024 {
		return nil, errors.New("hook input limit exceeded")
	}
	return b, nil
}
