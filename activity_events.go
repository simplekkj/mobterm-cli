package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

type activityInput struct {
	Cwd              string          `json:"cwd,omitempty"`
	RequestID        string          `json:"request_id"`
	AdapterRevision  int             `json:"adapter_revision"`
	RuntimeVersion   string          `json:"runtime_version"`
	Event            string          `json:"event"`
	SessionID        string          `json:"session_id"`
	EventID          string          `json:"event_id"`
	InstanceID       string          `json:"instance_id"`
	RunID            string          `json:"run_id"`
	ToolID           string          `json:"tool_id"`
	ToolName         string          `json:"tool_name"`
	ToolStatus       string          `json:"tool_status"`
	Aborted          *bool           `json:"aborted"`
	HookEventName    string          `json:"hook_event_name"`
	TurnID           string          `json:"turn_id"`
	ToolUseID        string          `json:"tool_use_id"`
	NotificationType string          `json:"notification_type"`
	Type             string          `json:"type"`
	Thread           string          `json:"thread-id"`
	Turn             string          `json:"turn-id"`
	ToolInput        json.RawMessage `json:"tool_input"`
}

func parseActivity(ctx context.Context, s Store, source string, raw []byte) (Activity, activityInput, error) {
	var p activityInput
	a := Activity{Agent: source}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, &p) != nil {
		return a, p, errors.New("invalid_activity")
	}
	valid := func(v string) bool { return v != "" && len(v) <= 256 && strings.IndexFunc(v, unicode.IsControl) < 0 }
	if source == "codex" && p.Type == "agent-turn-complete" {
		p.SessionID = p.Thread
		p.TurnID = p.Turn
		p.HookEventName = "NotifyComplete"
	}
	if !valid(p.SessionID) {
		return a, p, errors.New("invalid_session")
	}
	a.Session = p.SessionID
	a.Run = p.TurnID
	a.Instance = p.SessionID
	if source == "opencode" || source == "pi" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&p) != nil {
			return a, p, errors.New("invalid_native_activity")
		}
		policy, ok := agentCompatibilities[source]
		if !ok || p.AdapterRevision != 2 || policy.evaluate(policy.prefix+p.RuntimeVersion+policy.suffix) != "" || !valid(p.EventID) || !valid(p.InstanceID) || !valid(p.RunID) {
			return a, p, errors.New("invalid_native_activity")
		}
		a.Run = p.RunID
		a.Instance = p.InstanceID
		a.Occurrence = digest(source + "\x00" + p.Event + "\x00" + p.SessionID + "\x00" + p.EventID + "\x00" + p.InstanceID)
		switch p.Event {
		case "session.execution.started", "agent_start":
			a.State = "running"
		case "session.execution.succeeded", "agent_settled":
			a.State = "turn_completed"
			a.Reminder = "turn_completed"
			if source == "pi" {
				if p.Aborted == nil {
					return a, p, errors.New("settled_requires_aborted")
				}
				if *p.Aborted {
					a.State = "interrupted"
					a.Reminder = "interrupted"
				}
			}
		case "session.execution.failed":
			a.State = "failed"
			a.Reminder = "failed"
		case "session.execution.interrupted":
			a.State = "interrupted"
			a.Reminder = "interrupted"
		case "permission.asked":
			a.RequestID = p.RequestID
			a.State = "waiting_approval"
			a.Reminder = "needs_input"
		case "form.created", "ui_prompt_start":
			a.RequestID = p.RequestID
			a.State = "waiting_input"
			a.Reminder = "needs_input"
		case "ui_prompt_end", "permission.replied", "form.replied", "form.cancelled":
			a.RequestID = p.RequestID
			a.ResolveRequest = true
			a.State = "running"
		case "tool_execution_start":
			a.State = "tool_running"
			a.ToolStatus = "running"
		case "tool_pending":
			a.State = "tool_pending"
			a.ToolStatus = "pending"
		case "tool_execution_end":
			a.State = "running"
			a.ToolStatus = p.ToolStatus
			if a.ToolStatus != "succeeded" && a.ToolStatus != "failed" && a.ToolStatus != "interrupted" && a.ToolStatus != "returned" {
				return a, p, errors.New("invalid_tool_result")
			}
		default:
			return a, p, nil
		}
		if p.RequestID != "" && !valid(p.RequestID) {
			return a, p, errors.New("invalid_request_identity")
		}
		if strings.HasPrefix(p.Event, "tool_") {
			if !valid(p.ToolID) {
				return a, p, errors.New("missing_tool_identity")
			}
			a.ToolID = p.ToolID
			a.ToolCategory = toolCategory(p.ToolName)
		}
		return a, p, nil
	}
	if source != "codex" && source != "claude" {
		return a, p, errors.New("unknown_agent")
	}
	if source == "codex" && p.HookEventName != "SessionStart" && p.HookEventName != "SessionEnd" && !valid(p.TurnID) {
		return a, p, errors.New("missing_turn_identity")
	}
	switch p.HookEventName {
	case "UserPromptSubmit":
		a.State = "starting"
	case "PreToolUse":
		a.State = "tool_pending"
		a.ToolStatus = "pending"
	case "PostToolUse":
		a.State = "running"
		a.ToolStatus = "returned"
	case "PostToolUseFailure":
		a.State = "running"
		a.ToolStatus = "failed"
	case "PermissionRequest":
		a.State = "waiting_approval"
		a.Reminder = "needs_input"
	case "Interrupt":
		if source == "codex" {
			a.State = "interrupted"
			a.Reminder = "interrupted"
		}
	case "NotifyComplete":
		a.State = "turn_completed"
		a.Reminder = "turn_completed"
		a.Occurrence = digest("codex\x00" + p.SessionID + "\x00" + p.TurnID)
	case "Notification":
		if source == "claude" && p.NotificationType == "idle_prompt" {
			cycle, stopped, e := claudeActivityCycle(ctx, s, p.SessionID, false)
			if e != nil {
				return a, p, e
			}
			if !stopped {
				return a, p, nil
			}
			a.Run = cycle
		}
		kind, id, err := parseEvent(ctx, s, source, raw)
		if err != nil {
			return a, p, err
		}
		a.Reminder = kind
		a.Occurrence = id
		if kind == "turn_completed" {
			a.State = "turn_completed"
		} else if kind == "needs_input" {
			a.State = "waiting_input"
		}
	case "Stop":
		_, _, err := claudeActivityCycle(ctx, s, p.SessionID, true)
		return a, p, err // Only idle after this cycle's Stop can confirm completion.
	default:
		return a, p, nil
	}
	if source == "claude" && p.HookEventName == "UserPromptSubmit" {
		if _, _, err := parseEvent(ctx, s, source, raw); err != nil {
			return a, p, err
		}
	}
	if source == "claude" {
		cycle, _, err := claudeActivityCycle(ctx, s, p.SessionID, false)
		if err != nil {
			return a, p, err
		}
		if cycle != "" {
			a.Run = cycle
		}
	}
	if a.ToolStatus != "" {
		if !valid(p.ToolUseID) {
			if source == "codex" { // Codex's hook contract has no universal invocation ID; preserve a bounded observation, do not claim parallel identity.
				a.ToolStatus = ""
				if p.HookEventName == "PostToolUse" {
					a.State = "running"
				} else {
					a.State = "tool_pending"
				}
			} else {
				return a, p, errors.New("missing_tool_identity")
			}
		} else {
			a.ToolID = p.ToolUseID
			a.ToolCategory = toolCategory(p.ToolName)
		}
	}
	if a.Occurrence == "" {
		if p.ToolUseID != "" {
			a.Occurrence = digest(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", source, p.SessionID, p.TurnID, p.HookEventName, p.ToolUseID))
		} else {
			a.Occurrence = digest(source + "\x00" + p.SessionID + "\x00" + p.TurnID + "\x00" + p.HookEventName + "\x00" + randomID())
		}
	}
	return a, p, nil
}

func claudeActivityCycle(ctx context.Context, s Store, session string, stop bool) (string, bool, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	cycles := map[string]claudeCycle{}
	err = s.readJSON("claude-cycles.json", &cycles)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return "", false, err
	}
	cycle := cycles[digest(session)]
	stops := map[string]string{}
	err = s.readJSON("claude-stops-v2.json", &stops)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return "", false, err
	}
	if stop && cycle.Cycle != "" {
		if len(stops) > 256 {
			stops = map[string]string{}
		}
		stops[digest(session)] = cycle.Cycle
		err = s.writeJSON("claude-stops-v2.json", stops)
	}
	return cycle.Cycle, cycle.Cycle != "" && stops[digest(session)] == cycle.Cycle, err
}
func observe(ctx context.Context, s Store, api API, source string, raw []byte) (any, error) {
	a, input, err := parseActivity(ctx, s, source, raw)
	if err != nil || a.State == "" {
		return nil, err
	}
	id, err := s.installation()
	if err != nil {
		return nil, err
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	if source == "opencode" || source == "pi" {
		a.Occurrence = nativeTargetIdentity(a.Occurrence, l)
	}
	if input.HookEventName == "PermissionRequest" {
		return approvalWait(ctx, s, api, l, a, input)
	}
	return nil, publishActivity(ctx, s, api, l, a)
}
