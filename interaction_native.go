package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

func nativeInteractionID(id string) bool {
	return id != "" && len(id) <= 1024 && strings.IndexFunc(id, unicode.IsControl) < 0
}
func nativeInteractionVersion(agent, actual string) bool {
	switch agent {
	case "claude":
		return actual == "2.1.316" || actual == "2.1.317"
	case "codex":
		return actual == "0.166.0"
	case "opencode":
		return actual == "2.2.5"
	case "pi":
		return actual == "1.4.0" || actual == "1.5.0"
	}
	return false
}
func nativeInteractionActual(ctx context.Context, agent string) string {
	p := probeAgent(ctx, agent)
	c := agentCompatibilities[agent]
	if p.status != probeOK {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(p.actual, c.prefix), c.suffix)
}
func nativeKeys(value map[string]json.RawMessage, allowed ...string) bool {
	for k := range value {
		found := false
		for _, a := range allowed {
			found = found || a == k
		}
		if !found {
			return false
		}
	}
	return true
}

// A Question is mapped by its original text, never by sequence at dispatch time.
// The documented classic Hook contract uses comma-separated multi-select labels.
// Ambiguous labels are rejected until an official-host array contract is proven.
func claudeQuestionSpec(input json.RawMessage) (InteractionSpec, error) {
	spec := InteractionSpec{Kind: "question", Title: "Claude Code question", ToolName: "AskUserQuestion", ToolInput: input, NativeInput: input, SupportedActions: []string{"answer", "cancel", "release"}, TTL: 300 * time.Second}
	var object map[string]json.RawMessage
	if interactionJSON(input, &object) != nil {
		return spec, errors.New("invalid_question")
	}
	if _, ok := object["response"]; ok {
		return spec, errors.New("claude_whole_response_unverified")
	}
	var questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		MultiSelect bool `json:"multiSelect"`
	}
	if interactionJSON(object["questions"], &questions) != nil || len(questions) < 1 || len(questions) > 4 {
		return spec, errors.New("unsupported_question")
	}
	seen := map[string]bool{}
	for index, q := range questions {
		if q.Question == "" || seen[q.Question] || len(q.Options) < 2 || len(q.Options) > 4 {
			return spec, errors.New("ambiguous_question")
		}
		seen[q.Question] = true
		f := InteractionField{ID: fmt.Sprintf("q%d", index), Type: "string", Label: q.Question, Description: q.Header, Required: true, Custom: true}
		if q.MultiSelect {
			f.Type = "multiselect"
			f.Custom = false
			n := 1
			f.MinItems = &n
		}
		labels := map[string]bool{}
		for _, o := range q.Options {
			if o.Label == "" || labels[o.Label] || q.MultiSelect && strings.Contains(o.Label, ",") {
				return spec, errors.New("ambiguous_question_options")
			}
			labels[o.Label] = true
			f.Options = append(f.Options, InteractionOption{ID: o.Label, Label: o.Label})
			if o.Description != "" {
				f.Description += "\n" + o.Label + ": " + o.Description
			}
		}
		spec.Fields = append(spec.Fields, f)
	}
	return spec, interactionSchema(spec.Fields)
}
func claudeQuestionOutput(spec InteractionSpec, decision InteractionDecision) (any, error) {
	if decision.Action == "release" {
		return nil, nil
	}
	output := map[string]any{"hookEventName": "PreToolUse"}
	if decision.Action == "cancel" {
		output["permissionDecision"] = "deny"
		output["permissionDecisionReason"] = "User cancelled this question"
		return map[string]any{"hookSpecificOutput": output}, nil
	}
	if interactionAnswers(spec, decision.Action, decision.ResponseJSON) != nil {
		return nil, errors.New("invalid_question_answer")
	}
	var response struct {
		Answers map[string]any `json:"answers"`
	}
	_ = interactionJSON([]byte(decision.ResponseJSON), &response)
	var original map[string]json.RawMessage
	_ = interactionJSON(spec.NativeInput, &original)
	answers := map[string]any{}
	for _, field := range spec.Fields {
		value := response.Answers[field.ID]
		if field.Type == "multiselect" {
			labels := []string{}
			for _, v := range value.([]any) {
				label := v.(string)
				if strings.Contains(label, ",") {
					return nil, errors.New("ambiguous_question_answer")
				}
				labels = append(labels, label)
			}
			value = strings.Join(labels, ", ")
		}
		answers[field.Label] = value
	}
	original["answers"], _ = json.Marshal(answers)
	output["permissionDecision"] = "allow"
	output["updatedInput"] = original
	return map[string]any{"hookSpecificOutput": output}, nil
}

// Only a closed, bounded JSON Schema subset is actionable. Unknown constraints
// are not erased: the caller returns to the native UI with no decision.
func elicitationFields(raw json.RawMessage) ([]InteractionField, error) {
	var schema map[string]json.RawMessage
	bad := errors.New("unsupported_elicitation_schema")
	if interactionJSON(raw, &schema) != nil || !nativeKeys(schema, "type", "properties", "required", "additionalProperties", "title", "description", "$schema") {
		return nil, bad
	}
	var typ string
	_ = json.Unmarshal(schema["type"], &typ)
	if typ != "object" {
		return nil, bad
	}
	if v, ok := schema["additionalProperties"]; ok && string(v) != "false" {
		return nil, bad
	}
	var props map[string]json.RawMessage
	if interactionJSON(schema["properties"], &props) != nil || len(props) == 0 || len(props) > 64 {
		return nil, bad
	}
	var required []string
	if v, ok := schema["required"]; ok && interactionJSON(v, &required) != nil {
		return nil, bad
	}
	req := map[string]bool{}
	for _, k := range required {
		if _, ok := props[k]; !ok || req[k] {
			return nil, bad
		}
		req[k] = true
	}
	keys := []string{}
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := []InteractionField{}
	for _, key := range keys {
		var p map[string]json.RawMessage
		if interactionJSON(props[key], &p) != nil || !nativeKeys(p, "type", "title", "description", "enum", "enumNames", "minLength", "maxLength", "minimum", "maximum", "format", "default", "items", "minItems", "maxItems", "uniqueItems") {
			return nil, bad
		}
		f := InteractionField{ID: key, Label: key, Required: req[key]}
		if json.Unmarshal(p["type"], &f.Type) != nil {
			return nil, bad
		}
		_ = json.Unmarshal(p["title"], &f.Label)
		_ = json.Unmarshal(p["description"], &f.Description)
		for _, key := range []string{"minLength", "maxLength", "format"} {
			if _, ok := p[key]; ok && f.Type != "string" {
				return nil, bad
			}
		}
		for _, key := range []string{"minimum", "maximum"} {
			if _, ok := p[key]; ok && f.Type != "number" && f.Type != "integer" {
				return nil, bad
			}
		}
		for _, key := range []string{"items", "minItems", "maxItems", "uniqueItems"} {
			if _, ok := p[key]; ok && f.Type != "array" {
				return nil, bad
			}
		}
		if v, ok := p["default"]; ok {
			f.Description += "\nDefault (not automatically submitted): " + string(v)
		}
		for _, k := range []string{"minimum", "maximum"} {
			if v, ok := p[k]; ok {
				if _, e := interactionNumber(json.Number(string(v))); e != nil {
					return nil, bad
				}
			}
		}
		for _, k := range []string{"minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems", "format", "enum", "enumNames", "items", "uniqueItems"} {
			if v, ok := p[k]; ok && string(v) == "null" {
				return nil, bad
			}
		}
		for k, target := range map[string]any{"minLength": &f.MinLength, "maxLength": &f.MaxLength, "minimum": &f.Minimum, "maximum": &f.Maximum, "minItems": &f.MinItems, "maxItems": &f.MaxItems, "format": &f.Format} {
			if v, ok := p[k]; ok && json.Unmarshal(v, target) != nil {
				return nil, bad
			}
		}
		enum := p["enum"]
		enumNames := p["enumNames"]
		if f.Type == "array" {
			f.Type = "multiselect"
			var item map[string]json.RawMessage
			if interactionJSON(p["items"], &item) != nil || !nativeKeys(item, "type", "enum", "enumNames") {
				return nil, bad
			}
			var t string
			_ = json.Unmarshal(item["type"], &t)
			if t != "string" {
				return nil, bad
			}
			enum = item["enum"]
			enumNames = item["enumNames"]
			f.Custom = len(enum) == 0
			if v, ok := p["uniqueItems"]; ok && string(v) != "true" && string(v) != "false" {
				return nil, bad
			}
		}
		if len(enum) > 0 {
			if f.Type != "string" && f.Type != "multiselect" {
				return nil, bad
			}
			var values, names []string
			if interactionJSON(enum, &values) != nil {
				return nil, bad
			}
			if len(enumNames) > 0 && (interactionJSON(enumNames, &names) != nil || len(names) != len(values)) {
				return nil, bad
			}
			for i, v := range values {
				label := v
				if len(names) > 0 {
					label = names[i]
				}
				f.Options = append(f.Options, InteractionOption{ID: v, Label: label})
			}
		}
		fields = append(fields, f)
	}
	if interactionSchema(fields) != nil {
		return nil, bad
	}
	return fields, nil
}

type nativeForm struct {
	ID        string            `json:"id"`
	SessionID string            `json:"sessionID"`
	Title     string            `json:"title"`
	Fields    []json.RawMessage `json:"fields"`
	Metadata  json.RawMessage   `json:"metadata,omitempty"`
	State     json.RawMessage   `json:"state,omitempty"`
	Location  json.RawMessage   `json:"location,omitempty"`
}

func openCodeFormSpec(raw json.RawMessage, session string) (InteractionSpec, error) {
	spec := InteractionSpec{Kind: "form", NativeInput: raw, SupportedActions: []string{"answer", "cancel", "release"}, TTL: 300 * time.Second}
	var form nativeForm
	bad := errors.New("unsupported_native_form")
	if interactionJSON(raw, &form) != nil || form.SessionID != session || session == "global" || !nativeInteractionID(form.ID) || len(form.Fields) == 0 {
		return spec, bad
	}
	spec.NativeID = form.ID
	spec.Title = form.Title
	spec.NativeMetadata = form.Metadata
	for _, rawField := range form.Fields {
		var p map[string]json.RawMessage
		if interactionJSON(rawField, &p) != nil || !nativeKeys(p, "key", "type", "title", "description", "required", "hidden", "when", "format", "minLength", "maxLength", "placeholder", "default", "options", "custom", "minimum", "maximum", "minItems", "maxItems", "url") {
			return spec, bad
		}
		f := InteractionField{}
		_ = json.Unmarshal(p["key"], &f.ID)
		_ = json.Unmarshal(p["type"], &f.Type)
		if f.Type == "string" {
			f.LengthUnit = "utf16"
		}
		f.Label = f.ID
		for _, k := range []string{"minimum", "maximum"} {
			if v, ok := p[k]; ok {
				if _, e := interactionNumber(json.Number(string(v))); e != nil {
					return spec, bad
				}
			}
		}
		for k, target := range map[string]any{"title": &f.Label, "description": &f.Description, "required": &f.Required, "custom": &f.Custom, "minLength": &f.MinLength, "maxLength": &f.MaxLength, "minimum": &f.Minimum, "maximum": &f.Maximum, "minItems": &f.MinItems, "maxItems": &f.MaxItems, "format": &f.Format} {
			if v, ok := p[k]; ok && json.Unmarshal(v, target) != nil {
				return spec, bad
			}
		}
		if v, ok := p["default"]; ok {
			f.Description += "\nDefault (not automatically submitted): " + string(v)
		}
		if v, ok := p["hidden"]; ok && string(v) != "false" {
			return spec, bad
		}
		if f.Type == "external" {
			var url string
			if json.Unmarshal(p["url"], &url) != nil || url == "" {
				return spec, bad
			}
			f.Required = true
			f.Description += "\n" + url + "\nConfirm only after completing this external step. This does not verify authentication."
		}
		var options []struct {
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description,omitempty"`
		}
		if v, ok := p["options"]; ok {
			if interactionJSON(v, &options) != nil || len(options) == 0 {
				return spec, bad
			}
			for _, o := range options {
				f.Options = append(f.Options, InteractionOption{ID: o.Value, Label: o.Label})
				if o.Description != "" {
					f.Description += "\n" + o.Label + ": " + o.Description
				}
			}
		}
		var conditions []struct {
			Key   string          `json:"key"`
			Op    string          `json:"op"`
			Value json.RawMessage `json:"value"`
		}
		if v, ok := p["when"]; ok {
			if interactionJSON(v, &conditions) != nil {
				return spec, bad
			}
			for _, c := range conditions {
				f.When = append(f.When, InteractionCondition{FieldID: c.Key, Op: c.Op, Value: c.Value})
			}
		}
		if f.Required && f.Type == "string" && (f.MinLength == nil || *f.MinLength < 1) {
			n := 1
			f.MinLength = &n
		}
		if f.Required && f.Type == "multiselect" && (f.MinItems == nil || *f.MinItems < 1) {
			n := 1
			f.MinItems = &n
		}
		spec.Fields = append(spec.Fields, f)
	}
	return spec, interactionSchema(spec.Fields)
}

func interactionObserve(ctx context.Context, s Store, api API, source string, raw []byte) (any, error) {
	// A disabled Pi gate leaves the existing tool chain untouched. Once locally
	// opted in, every target/schema/network error must block this invocation.
	if source == "pi" {
		configured, e := localInteractions(s)
		enabled := false
		for _, agents := range configured {
			enabled = enabled || agents["pi"]["approval"] > 0
		}
		if e == nil && !enabled {
			return map[string]any{"gate_enabled": false}, nil
		}
	}
	if len(raw) > interactionRequestLimit {
		return nil, errors.New("interaction_request_too_large")
	}
	var object map[string]json.RawMessage
	if interactionJSON(raw, &object) != nil {
		return nil, errors.New("invalid_native_interaction")
	}
	var p activityInput
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("invalid_native_interaction")
	}
	if source == "claude" && !(p.HookEventName == "Elicitation" || p.HookEventName == "PreToolUse" && (p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode")) {
		return observe(ctx, s, api, source, raw)
	}
	a, _, err := parseActivity(ctx, s, source, raw)
	if source == "claude" && p.HookEventName == "Elicitation" {
		a = Activity{Agent: source, Session: p.SessionID, Instance: p.SessionID, State: "waiting_input", Reminder: "needs_input"}
		err = nil
	}
	if err != nil || !nativeInteractionID(p.SessionID) {
		return nil, errors.New("invalid_native_interaction")
	}
	id, err := s.installation()
	if err != nil {
		return nil, err
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	spec := InteractionSpec{NativeID: p.ToolUseID, Cwd: p.Cwd, TurnID: p.TurnID, AdapterVersion: 1, ToolName: p.ToolName, ToolInput: p.ToolInput, TTL: 120 * time.Second}
	if source == "claude" {
		switch {
		case p.HookEventName == "Elicitation":
			var mode, message, nativeID string
			_ = json.Unmarshal(object["mode"], &mode)
			_ = json.Unmarshal(object["message"], &message)
			_ = json.Unmarshal(object["elicitation_id"], &nativeID)
			if mode != "" && mode != "form" {
				spec.Kind = "elicitation"
				spec.NativeInput = raw
				spec.Message = message
				return nil, interactionUnavailable(ctx, s, api, l, a, spec, "native_authentication_required")
			}
			fields, e := elicitationFields(object["requested_schema"])
			if e != nil {
				spec.Kind = "elicitation"
				spec.NativeInput = raw
				spec.Message = message
				return nil, interactionUnavailable(ctx, s, api, l, a, spec, "unsupported_elicitation_schema")
			}
			spec.Kind = "elicitation"
			spec.Title = "Claude Code MCP form"
			spec.Message = message
			spec.NativeID = nativeID
			spec.NativeInput = raw
			spec.Fields = fields
			spec.SupportedActions = []string{"accept", "decline", "cancel", "release"}
			spec.TTL = 300 * time.Second
		case p.ToolName == "AskUserQuestion":
			spec, err = claudeQuestionSpec(p.ToolInput)
		case p.ToolName == "ExitPlanMode":
			spec.Kind = "planApproval"
			spec.Title = "Claude Code plan"
			spec.NativeInput = p.ToolInput
			var plan struct {
				Plan string `json:"plan"`
			}
			_ = json.Unmarshal(p.ToolInput, &plan)
			spec.Message = plan.Plan
			spec.Scope = "Approval changes the native permission mode. The exact classic Hook response and resulting mode are not verified; review the complete plan in the original terminal."
			reason := "plan_approval_mode_unverified"
			if plan.Plan == "" {
				reason = "plan_content_missing"
			}
			return nil, interactionUnavailable(ctx, s, api, l, a, spec, reason)
		}
		spec.AgentVersion = nativeInteractionActual(ctx, "claude")
	} else if source == "opencode" {
		if p.Event == "form.created" {
			spec, err = openCodeFormSpec(object["native_form"], p.SessionID)
		} else if p.Event == "permission.asked" {
			configured, e := localInteractions(s)
			if e != nil {
				return nil, e
			}
			enabled := false
			for _, agents := range configured {
				enabled = enabled || agents["opencode"]["approval"] > 0
			}
			if !enabled {
				legacy, e := approvalWait(ctx, s, api, l, a, p)
				if e != nil || legacy == nil {
					return nil, e
				}
				return map[string]any{"legacy_approval": legacy}, nil
			}
			if !approvalInputSupported("opencode", p) {
				return nil, nil
			}
			spec.Kind = "approval"
			spec.Title = "OpenCode permission"
			spec.NativeID = p.RequestID
			spec.NativeInput = p.ToolInput
			spec.SupportedActions = []string{"allow_once", "deny", "release"}
			spec.Scope = "Deny rejects all permission requests pending in this session at execution time, including requests arriving concurrently."
			spec.NativeMetadata = object["pending_permissions"]
		} else {
			return nil, errors.New("unsupported_native_interaction")
		}
		spec.AgentVersion = p.RuntimeVersion
	} else if source == "pi" {
		if p.Event != "tool_gate" || !nativeInteractionID(p.ToolUseID) || !approvalCwdValid(p.Cwd) || len(p.ToolInput) == 0 {
			return nil, errors.New("unsupported_native_interaction")
		}
		switch p.ToolName {
		case "bash", "read", "write", "edit", "grep", "find", "ls":
		default:
			return nil, errors.New("unsupported_pi_tool")
		}
		var input map[string]json.RawMessage
		if interactionJSON(p.ToolInput, &input) != nil {
			return nil, errors.New("invalid_pi_input")
		}
		required := map[string][]string{"bash": {"command"}, "read": {"path"}, "write": {"path", "content"}, "edit": {"path", "oldText", "newText"}, "grep": {"pattern"}, "find": {"pattern"}, "ls": {}}
		for _, field := range required[p.ToolName] {
			var value string
			if json.Unmarshal(input[field], &value) != nil {
				return nil, errors.New("invalid_pi_tool_input")
			}
		}

		spec.Kind = "approval"
		spec.Title = "MobTerm tool approval"
		spec.NativeInput = p.ToolInput
		spec.SupportedActions = []string{"allow_once", "deny", "release"}
		spec.AgentVersion = p.RuntimeVersion
		spec.Scope = "This gate authorizes only the displayed input at this extension stage. Other trusted extensions still run and may block or modify the tool input."
	} else {
		return nil, errors.New("unsupported_native_interaction")
	}
	if err != nil || !nativeInteractionVersion(source, spec.AgentVersion) {
		reason := "agent_version_unverified"
		if err != nil {
			reason = err.Error()
		}
		if spec.Kind == "" {
			spec.Kind = "form"
		}
		spec.NativeInput = raw
		spec.Cwd = p.Cwd
		return nil, interactionUnavailable(ctx, s, api, l, a, spec, reason)
	}
	spec.NativeID = func() string {
		if spec.NativeID != "" {
			return spec.NativeID
		}
		return p.ToolUseID
	}()
	spec.Cwd = p.Cwd
	spec.TurnID = p.TurnID
	spec.AdapterVersion = 1
	a.State = "waiting_input"
	if spec.Kind == "approval" {
		a.State = "waiting_approval"
	}
	a.Reminder = "needs_input"
	a.RequestID = spec.NativeID
	decision, err := interactionNativeWait(ctx, s, api, l, a, spec)
	if err != nil || decision == nil {
		if source == "pi" {
			return map[string]any{"gate_enabled": true, "action": "deny"}, nil
		}
		return nil, err
	}
	if source != "claude" {
		return decision, nil
	}
	if decision.Action == "release" {
		return nil, nil
	}
	var output any
	if spec.Kind == "question" {
		output, err = claudeQuestionOutput(spec, *decision)
	} else if decision.Action != "release" {
		native := map[string]any{"hookEventName": "Elicitation", "action": decision.Action}
		if decision.Action == "accept" {
			var response struct {
				Answers map[string]any `json:"answers"`
			}
			err = interactionJSON([]byte(decision.ResponseJSON), &response)
			native["content"] = response.Answers
		}
		output = map[string]any{"hookSpecificOutput": native}
	}
	if err == nil && spec.Kind == "question" && decision.Action == "answer" {
		var result struct {
			Hook struct {
				Input struct {
					Answers map[string]any `json:"answers"`
				} `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		encoded, _ := json.Marshal(output)
		if json.Unmarshal(encoded, &result) != nil {
			err = errors.New("invalid_native_answer_evidence")
		} else {
			answers, _ := json.Marshal(result.Hook.Input.Answers)
			err = interactionSetExpected(s, decision.RequestID, p.SessionID, p.ToolUseID, digest(string(answers)))
		}
	}
	if err != nil {
		_ = interactionComplete(s, decision.RequestID, "delivery_unknown")
		return nil, err
	}
	return InteractionOutput{Value: output, RequestID: decision.RequestID}, nil
}
