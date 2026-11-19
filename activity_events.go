package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type activityInput struct {
	CompletionBody        string          `json:"completion_body_base64,omitempty"`
	CompletionTooLarge    bool            `json:"completion_body_too_large,omitempty"`
	AgentID               string          `json:"agent_id,omitempty"`
	AgentType             string          `json:"agent_type,omitempty"`
	TranscriptPath        string          `json:"transcript_path,omitempty"`
	PromptID              string          `json:"prompt_id,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	LastAssistantMessage  string          `json:"last_assistant_message,omitempty"`
	CodexAssistantMessage string          `json:"last-assistant-message,omitempty"`
	UnavailableReason     string          `json:"unavailable_reason,omitempty"`
	Cwd                   string          `json:"cwd,omitempty"`
	RequestID             string          `json:"request_id"`
	AdapterRevision       int             `json:"adapter_revision"`
	RuntimeVersion        string          `json:"runtime_version"`
	Event                 string          `json:"event"`
	SessionID             string          `json:"session_id"`
	EventID               string          `json:"event_id"`
	InstanceID            string          `json:"instance_id"`
	RunID                 string          `json:"run_id"`
	ToolID                string          `json:"tool_id"`
	ToolName              string          `json:"tool_name"`
	ToolStatus            string          `json:"tool_status"`
	Aborted               *bool           `json:"aborted"`
	HookEventName         string          `json:"hook_event_name"`
	TurnID                string          `json:"turn_id"`
	ToolUseID             string          `json:"tool_use_id"`
	NotificationType      string          `json:"notification_type"`
	Type                  string          `json:"type"`
	Thread                string          `json:"thread-id"`
	Turn                  string          `json:"turn-id"`
	ToolInput             json.RawMessage `json:"tool_input"`
	NativeForm            json.RawMessage `json:"native_form,omitempty"`
	PendingPermissions    json.RawMessage `json:"pending_permissions,omitempty"`
}

func parseActivity(ctx context.Context, s Store, source string, raw []byte) (Activity, activityInput, error) {
	var p activityInput
	a := Activity{Agent: source}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, &p) != nil {
		return a, p, errors.New("invalid_activity")
	}
	valid := func(v string) bool { return v != "" && len(v) <= 256 && strings.IndexFunc(v, unicode.IsControl) < 0 }
	if source == "codex" && p.Type == "agent-turn-complete" {
		if p.SessionID != "" && p.SessionID != p.Thread || p.TurnID != "" && p.TurnID != p.Turn {
			return a, p, errors.New("conflicting_codex_identity")
		}
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
	if (source == "codex" || source == "claude") && p.AgentID != "" {
		if !valid(p.AgentID) {
			return a, p, errors.New("invalid_agent_identity")
		}
		// Child user interactions remain available, but cannot mutate the root run.
		childInteraction := p.HookEventName == "PermissionRequest" || p.HookEventName == "Elicitation" || (p.HookEventName == "PreToolUse" || p.HookEventName == "PostToolUse") && (p.ToolName == "request_user_input" || p.ToolName == "request_user_input_async" || p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode") || p.HookEventName == "Notification" && (p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog")
		if source == "claude" && p.HookEventName == "PostToolUseFailure" && (p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode") {
			childInteraction = true
		}
		a.Session = p.AgentID
		a.Instance = digest(p.SessionID + "\x00child\x00" + p.AgentID)
		if !childInteraction {
			return a, p, nil
		}
	}
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
		a.Summary = cleanMessageSummary(p.Summary)
		a.Run = p.RunID
		a.Instance = p.InstanceID
		a.Occurrence = newReplyOccurrence(source, p.Event, p.SessionID, p.EventID, p.InstanceID)
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
		case "permission.asked", "tool_gate":
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
		if p.CompletionBody != "" || p.CompletionTooLarge {
			if a.Reminder != "turn_completed" || source == "opencode" && p.Event != "session.execution.succeeded" || source == "pi" && p.Event != "agent_settled" {
				return a, p, errors.New("invalid_native_completion")
			}
			if p.CompletionTooLarge {
				a.Body.TooLarge = true
			} else if len(p.CompletionBody) <= base64.StdEncoding.EncodedLen(bodyLimit) {
				data, decodeErr := base64.StdEncoding.Strict().DecodeString(p.CompletionBody)
				if decodeErr == nil && base64.StdEncoding.EncodeToString(data) == p.CompletionBody {
					a.Body = newAssistantBody(string(data))
				}
			} else {
				a.Body.TooLarge = true
			}
		}
		if p.RequestID != "" && !valid(p.RequestID) {
			return a, p, errors.New("invalid_request_identity")
		}
		if strings.HasPrefix(p.Event, "tool_") && p.Event != "tool_gate" {
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
		if source == "codex" && (p.ToolName == "request_user_input" || p.ToolName == "request_user_input_async") || source == "claude" && (p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode") {
			a.State = "waiting_input"
			a.Reminder = "needs_input"
			a.RequestID = p.ToolUseID
		}
	case "Elicitation":
		a.State = "waiting_input"
		a.Reminder = "needs_input"
	case "PostToolUse":
		if (source == "codex" && (p.ToolName == "request_user_input" || p.ToolName == "request_user_input_async")) || source == "claude" && p.ToolName == "AskUserQuestion" {
			a.RequestID = p.ToolUseID
			a.ResolveRequest = true
		}
		a.State = "running"
		a.ToolStatus = "returned"
	case "PostToolUseFailure":
		a.State = "running"
		a.ToolStatus = "failed"
	case "PermissionRequest":
		a.State = "waiting_approval"
		a.Reminder = "needs_input"
		if source == "codex" {
			// This hook precedes native approval aggregation; it proves no wait.
			a.State = "tool_pending"
			a.Reminder = ""
		}
	case "Interrupt":
		if source == "codex" {
			a.State = "interrupted"
			a.Reminder = "interrupted"
		}
	case "NotifyComplete":
		a.Summary = cleanMessageSummary(p.CodexAssistantMessage)
		if source == "codex" && p.Type == "agent-turn-complete" && p.AgentID == "" && utf8.Valid(raw) {
			a.Body = newAssistantBody(p.CodexAssistantMessage)
		}
		a.State = "turn_completed"
		a.Reminder = "turn_completed"
		a.Occurrence = digest("codex\x00" + p.SessionID + "\x00" + p.TurnID)
	case "Notification":
		if source == "claude" && p.NotificationType == "idle_prompt" {
			var stop claudeBodyCapture
			cycle, stopped, summary, e := claudeCycleEvent(ctx, s, p.SessionID, "read", "", p.PromptID, &stop)
			a.ClaudeStop = stop.id
			if e != nil {
				return a, p, e
			}
			if !stopped {
				return a, p, nil
			}
			a.Run = cycle
			a.Summary = summary
			a.State = "turn_completed"
			a.Reminder = "turn_completed"
			a.Occurrence = claudeCompletionOccurrence(p.SessionID, cycle)
			break
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
		if source != "claude" {
			return a, p, nil
		}
		var capture claudeBodyCapture
		if p.LastAssistantMessage != "" && utf8.Valid(raw) {
			if id, e := s.existingInstallation(); e == nil {
				// A slow optional probe must leave time to record the base Stop.
				probe, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
				l, probeErr := captureTarget(probe, id)
				cancel()
				if probeErr == nil && l.OccupantID != "" && l.validate() == nil {
					capture.locator = l
					capture.body = newAssistantBody(p.LastAssistantMessage)
				}
			}
		}
		_, _, _, err := claudeCycleEvent(ctx, s, p.SessionID, "stop", cleanMessageSummary(p.LastAssistantMessage), p.PromptID, &capture)
		return a, p, err // Only idle after this cycle's Stop can confirm completion.
	default:
		return a, p, nil
	}
	if source == "claude" && p.HookEventName == "UserPromptSubmit" {
		if _, _, err := parseEvent(ctx, s, source, raw); err != nil {
			return a, p, err
		}
	}
	if source == "claude" && p.AgentID == "" && a.Reminder != "turn_completed" {
		if p.HookEventName == "PreToolUse" || p.HookEventName == "PostToolUse" || p.HookEventName == "PostToolUseFailure" {
			if _, _, _, err := claudeCycleEvent(ctx, s, p.SessionID, "resume", "", p.PromptID); err != nil {
				return a, p, err
			}
		}
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
	action := "read"
	if stop {
		action = "stop"
	}
	cycle, stopped, _, err := claudeCycleEvent(ctx, s, session, action, "", "")
	return cycle, stopped, err
}
func claudeCycleEvent(ctx context.Context, s Store, session, action, summary, promptID string, capture ...*claudeBodyCapture) (string, bool, string, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", false, "", err
	}
	defer unlock()
	cycles := map[string]claudeCycle{}
	if err = s.readJSON("claude-cycles.json", &cycles); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, "", err
	}
	key := digest(session)
	cycle := cycles[key]
	if cycle.Cycle == "" || cycle.Updated < time.Now().Unix()-86400 {
		return "", false, "", nil
	}
	stops := map[string]string{}
	if err = s.readJSON("claude-stops-v2.json", &stops); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, "", err
	}
	if cycle.PromptID != "" && promptID != "" && cycle.PromptID != promptID {
		return cycle.Cycle, false, "", nil
	}
	switch action {
	case "stop":
		// A duplicate Stop in the same still-stopped cycle has no new
		// ownership. Only an intervening resume starts a new candidate.
		if cycle.StopID != "" && stops[key] == cycle.Cycle {
			return cycle.Cycle, true, cycle.Summary, nil
		}
		if len(stops) > 256 {
			stops = map[string]string{}
		}
		stops[key] = cycle.Cycle
		cycle.StopID = randomID()
		cycle.BodyTarget = ""
		cycle.Bodies = nil
		if len(capture) > 0 && capture[0].locator.OccupantID != "" && cycle.PromptID == promptID {
			c := capture[0]
			cycle.BodyTarget = nativeTargetIdentity("", c.locator)
			cycle.Bodies = map[string]*bodyRef{}
			pairs, readErr := s.pairs()
			if readErr == nil {
				for id, pair := range pairs {
					if pair.Status == "active" && pair.InstallationID == c.locator.InstallationID {
						cycle.Bodies[id] = s.freezeBody(pair, c.body, time.Now().Unix())
					}
				}
			}
		}
		cycle.Summary = cleanMessageSummary(summary)
		cycle.SummaryCycle = cycle.Cycle
	case "resume":
		delete(stops, key)
		cycle.StopID = ""
		cycle.BodyTarget = ""
		cycle.Bodies = nil
		cycle.Summary = ""
		cycle.SummaryCycle = ""
	}
	if action != "read" {
		cycles[key] = cycle
		if err = saveClaudeCycles(s, cycles); err != nil {
			return "", false, "", err
		}
		if err = s.writeJSON("claude-stops-v2.json", stops); err != nil {
			return "", false, "", err
		}
	}
	stopped := stops[key] == cycle.Cycle
	if !stopped || cycle.SummaryCycle != cycle.Cycle {
		return cycle.Cycle, stopped, "", nil
	}
	if len(capture) > 0 && cycle.PromptID == promptID {
		capture[0].id = cycle.StopID
	}
	return cycle.Cycle, stopped, cycle.Summary, nil
}
func observe(ctx context.Context, s Store, api API, source string, raw []byte) (any, error) {
	var dispatch activityInput
	if source == "claude" && json.Unmarshal(raw, &dispatch) == nil && (dispatch.HookEventName == "Elicitation" || dispatch.HookEventName == "PreToolUse" && (dispatch.ToolName == "AskUserQuestion" || dispatch.ToolName == "ExitPlanMode")) {
		return interactionObserve(ctx, s, api, source, raw)
	}
	a, input, err := parseActivity(ctx, s, source, raw)
	childEnd := source == "claude" && input.AgentID != "" && (input.HookEventName == "Stop" || input.HookEventName == "SubagentStop")
	if err != nil || a.State == "" && !childEnd {
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
	if source == "claude" && input.AgentID != "" {
		if err := bindClaudeChildWait(&a, input, l); err != nil {
			return nil, err
		}
		if childEnd {
			return nil, finishClaudeChildWaits(ctx, s, &a, l)
		}
		if input.HookEventName == "PostToolUse" || input.HookEventName == "PostToolUseFailure" {
			if err := finishClaudeChildTool(ctx, s, l, a, input); err != nil {
				return nil, err
			}
			return nil, flushRunSnapshots(ctx, s, api)
		}
	} else if source == "claude" && input.HookEventName == "PostToolUse" && input.ToolName == "AskUserQuestion" {
		var tool struct {
			Answers json.RawMessage `json:"answers"`
		}
		if json.Unmarshal(input.ToolInput, &tool) == nil && len(tool.Answers) > 0 {
			if err := interactionConfirmClaude(s, a.Session, input.ToolUseID, tool.Answers, l); err != nil {
				return nil, err
			}
		}
	}
	if source == "opencode" || source == "pi" {
		a.Occurrence = nativeTargetIdentity(a.Occurrence, l)
	}
	if source == "codex" {
		allowed, e := admitCodexActivity(ctx, s, l, &a, input)
		if e != nil || !allowed {
			return nil, e
		}
	}

	if source == "pi" && input.Event == "ui_prompt_start" {
		return nil, interactionUnavailable(ctx, s, api, l, a, InteractionSpec{Kind: "question", Title: "Pi extension input", AgentVersion: input.RuntimeVersion, Scope: "This third-party UI event exposes no native request ID, complete fields or response resolver. Answer in the original Pi terminal."}, "pi_third_party_ui_has_no_resolver")
	}
	if input.UnavailableReason != "" {
		switch input.UnavailableReason {
		case "interaction_request_too_large", "agent_version_unverified":
		default:
			return nil, errors.New("invalid_unavailable_reason")
		}
		kind := "form"
		if input.Event == "permission.asked" {
			kind = "approval"
		}
		return nil, interactionUnavailable(ctx, s, api, l, a, InteractionSpec{Kind: kind, Title: "Agent interaction", NativeID: input.RequestID, AgentVersion: input.RuntimeVersion, Scope: "The complete native request cannot be safely handled remotely. Use the original terminal."}, input.UnavailableReason)
	}
	if source == "codex" && input.ToolName == "request_user_input_async" {
		if input.HookEventName == "PreToolUse" {
			// PreToolUse is only an observation: another Hook can rewrite/block
			// this call. The successful PostToolUse owns the sole reminder.
			a.Reminder = ""
			a.RequestID = ""
			a.State = "tool_pending"
			return nil, publishActivity(ctx, s, api, l, a)
		}
		if input.HookEventName == "PostToolUse" {
			_, spec, probeErr := codexFollowupSpec(raw)
			if probeErr == nil {
				probeErr = startCodexFollowup(ctx, s, l, raw)
			}
			if probeErr == nil {
				return nil, nil
			}
			a.ResolveRequest = false
			a.Reminder = "needs_input"
			a.State = "waiting_input"
			return nil, interactionUnavailable(ctx, s, api, l, a, spec, safeCode(probeErr.Error()))
		}
	}
	if source == "codex" && input.HookEventName == "PreToolUse" && input.ToolName == "request_user_input" {
		return nil, interactionUnavailable(ctx, s, api, l, a, InteractionSpec{Kind: "question", Title: "Codex question", ToolName: input.ToolName, ToolInput: input.ToolInput, NativeInput: input.ToolInput, NativeID: input.ToolUseID, TurnID: input.TurnID, Cwd: input.Cwd, Scope: "This is an observed native question, not proof that the question is still pending. Use the original terminal.", AgentVersion: "0.166.0", AdapterVersion: 1}, "codex_tui_attach_only_unavailable")
	}
	if input.HookEventName == "PermissionRequest" {
		return approvalWait(ctx, s, api, l, a, input)
	}
	if source == "codex" && (input.HookEventName == "PreToolUse" || input.HookEventName == "PostToolUse") && input.ToolName != "request_user_input" && input.ToolName != "request_user_input_async" {
		return nil, receiveActivity(ctx, s, l, a)
	}
	return nil, publishActivity(ctx, s, api, l, a)
}

type claudeBodyCapture struct {
	id      string
	locator Locator
	body    assistantBody
}

// Called while accepting the idle occurrence under the Store lock. A stop
// replaced after parsing idle cannot supply text to that earlier observation.
func (s Store) claudeBodies(a Activity, l Locator) map[string]*bodyRef {
	var cycles map[string]claudeCycle
	var stops map[string]string
	key := digest(a.Session)
	if s.readJSON("claude-cycles.json", &cycles) != nil || s.readJSON("claude-stops-v2.json", &stops) != nil {
		return nil
	}
	c := cycles[key]
	if a.ClaudeStop == "" || c.StopID != a.ClaudeStop || c.Cycle != a.Run || stops[key] != a.Run || c.BodyTarget != nativeTargetIdentity("", l) || c.Updated+86400 <= time.Now().Unix() {
		return nil
	}
	return c.Bodies
}

// Optional archive references cannot consume the configuration budget reserved
// for the native stop/idle state. Omitted references have no read authority.
func saveClaudeCycles(s Store, cycles map[string]claudeCycle) error {
	err := s.writeJSON("claude-cycles.json", cycles)
	if err == nil || err.Error() != "configuration exceeds size limit" {
		return err
	}
	for key, c := range cycles {
		c.Bodies = nil
		c.BodyTarget = ""
		c.StopID = ""
		cycles[key] = c
	}
	return s.writeJSON("claude-cycles.json", cycles)
}
