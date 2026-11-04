package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeQuestionNativeMapping(t *testing.T) {
	raw := json.RawMessage(`{"questions":[{"question":"框架?","header":"选择","options":[{"label":"A","description":"one"},{"label":"B","description":"two"}],"multiSelect":false},{"question":"功能?","header":"多选","options":[{"label":"x","description":""},{"label":"y","description":""}],"multiSelect":true}],"metadata":{"kept":true}}`)
	spec, e := claudeQuestionSpec(raw)
	if e != nil {
		t.Fatal(e)
	}
	result, e := claudeQuestionOutput(spec, InteractionDecision{Action: "answer", ResponseJSON: `{"answers":{"q0":"自己的答案","q1":["x","y"]}}`})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(result)
	var output struct {
		Hook struct {
			Input map[string]json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	_ = json.Unmarshal(b, &output)
	if string(output.Hook.Input["metadata"]) != `{"kept":true}` || len(output.Hook.Input["questions"]) == 0 {
		t.Fatalf("original input lost: %s", b)
	}
	var answers map[string]string
	_ = json.Unmarshal(output.Hook.Input["answers"], &answers)
	if answers["框架?"] != "自己的答案" || answers["功能?"] != "x, y" {
		t.Fatal(answers)
	}
	for _, bad := range []string{strings.Replace(string(raw), `功能?`, `框架?`, 1), strings.Replace(string(raw), `"label":"x"`, `"label":"x,z"`, 1)} {
		if _, e = claudeQuestionSpec(json.RawMessage(bad)); e == nil {
			t.Fatal("ambiguous native mapping accepted")
		}
	}
	if _, e = claudeQuestionOutput(spec, InteractionDecision{Action: "answer", ResponseJSON: `{"answers":{"q0":"A","q1":["x,z"]}}`}); e == nil {
		t.Fatal("ambiguous answer accepted")
	}
}
func TestElicitationNativeSchema(t *testing.T) {
	fields, e := elicitationFields(json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"},"count":{"type":"integer","minimum":0},"name":{"type":"string","enum":["a","b"]}},"required":["enabled","count"]}`))
	if e != nil {
		t.Fatal(e)
	}
	spec := InteractionSpec{Kind: "elicitation", Fields: fields, SupportedActions: []string{"accept", "decline", "cancel"}}
	if e = interactionAnswers(spec, "accept", `{"answers":{"enabled":false,"count":0,"name":"a"}}`); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"type":"object","properties":{"x":{"type":"string","pattern":".*"}}}`, `{"type":"object","properties":{"x":{"$ref":"https://remote"}}}`, `{"type":"object","properties":{"x":{"type":"string"}},"required":["missing"]}`} {
		if _, e = elicitationFields(json.RawMessage(raw)); e == nil {
			t.Fatal("unknown constraint accepted")
		}
	}
}
func TestOpenCodeNativeFormConditionsAndExternal(t *testing.T) {
	raw := json.RawMessage(`{"id":"frm_one","sessionID":"session","title":"Form","state":{"status":"pending"},"fields":[{"key":"choices","type":"multiselect","options":[{"value":"a","label":"A"},{"value":"b","label":"B"}],"required":true},{"key":"detail","type":"string","when":[{"key":"choices","op":"eq","value":"a"}],"required":true},{"key":"external","type":"external","url":"https://example.test/verify"}]}`)
	spec, e := openCodeFormSpec(raw, "session")
	if e != nil {
		t.Fatal(e)
	}
	if e = interactionAnswers(spec, "answer", `{"answers":{"choices":["a"],"detail":"说明","external":true}}`); e != nil {
		t.Fatal(e)
	}
	for _, response := range []string{`{"answers":{"choices":["b"],"detail":"hidden","external":true}}`, `{"answers":{"choices":["a"],"detail":"","external":true}}`, `{"answers":{"choices":["b"],"external":false}}`} {
		if interactionAnswers(spec, "answer", response) == nil {
			t.Fatal("invalid native answer accepted")
		}
	}
	if _, e = openCodeFormSpec(raw, "global"); e == nil {
		t.Fatal("global rebound")
	}
	if _, e = openCodeFormSpec(json.RawMessage(strings.Replace(string(raw), `"type":"string"`, `"type":"string","pattern":".+"`, 1)), "session"); e == nil {
		t.Fatal("unknown pattern dropped")
	}
}
func TestOpenCodeNativePrimitiveFields(t *testing.T) {
	raw := json.RawMessage(`{"id":"frm_types","sessionID":"session","fields":[{"key":"text","type":"string","minLength":2,"maxLength":2},{"key":"flag","type":"boolean","required":true},{"key":"count","type":"integer","minimum":0},{"key":"ratio","type":"number","maximum":1.5}]}`)
	spec, err := openCodeFormSpec(raw, "session")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Fields[0].LengthUnit != "utf16" {
		t.Fatal("native UTF-16 length lost")
	}
	for _, f := range spec.Fields[1:] {
		if f.LengthUnit != "" {
			t.Fatal("string constraints attached to non-string")
		}
	}
	if err = interactionAnswers(spec, "answer", `{"answers":{"text":"😀","flag":false,"count":0,"ratio":1.25}}`); err != nil {
		t.Fatal(err)
	}
	if interactionAnswers(spec, "answer", `{"answers":{"text":"a","flag":false,"count":0,"ratio":1.25}}`) == nil {
		t.Fatal("native string minimum lost")
	}
}
func TestNativeInteractionVersionAndIDs(t *testing.T) {
	for agent, version := range map[string]string{"codex": "0.166.0", "claude": "2.1.317", "opencode": "2.2.5", "pi": "1.4.0"} {
		if !nativeInteractionVersion(agent, version) || nativeInteractionVersion(agent, version+"-beta") {
			t.Fatal(agent)
		}
	}
	if nativeInteractionVersion("claude", "2.1.282") || !nativeInteractionID("parent/child/call") || nativeInteractionID("call\nnext") {
		t.Fatal("invalid native capability boundary")
	}
}

func TestClaudeInteractionHookMigration(t *testing.T) {
	data, record, err := editClaude([]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"user-audit"}]}]},"theme":"dark"}`), "install", "p", "/opt/mobterm-cli", HookRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(data, &root)
	var hooks map[string][]map[string]json.RawMessage
	_ = json.Unmarshal(root["hooks"], &hooks)
	oldCommand := map[string]any{"type": "command", "command": "'/opt/mobterm-cli' observe --source claude", "timeout": 10}
	oldEntry := map[string]any{"hooks": []any{oldCommand}}
	oldRaw, _ := json.Marshal(oldEntry)
	for i, entry := range hooks["PreToolUse"] {
		if strings.Contains(string(entry["hooks"]), "interaction wait") {
			_ = json.Unmarshal(oldRaw, &hooks["PreToolUse"][i])
		}
	}
	record.ClaudeOwned["PreToolUse"] = string(oldRaw)
	delete(hooks, "Elicitation")
	delete(record.ClaudeOwned, "Elicitation")
	root["hooks"], _ = json.Marshal(hooks)
	data, _ = json.Marshal(root)
	migrated, next, err := editClaude(data, "install", "p", "/opt/mobterm-cli", record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(migrated), "interaction wait --source claude") != 2 || !strings.Contains(string(migrated), "user-audit") {
		t.Fatal("migration duplicated or deleted hooks", string(migrated))
	}
	again, _, err := editClaude(migrated, "install", "p", "/opt/mobterm-cli", next)
	if err != nil || string(again) != string(migrated) {
		t.Fatal("migration is not idempotent", err)
	}
	removed, _, err := editClaude(migrated, "uninstall", "p", "/opt/mobterm-cli", next)
	if err != nil || strings.Contains(string(removed), "mobterm-cli") || !strings.Contains(string(removed), "user-audit") {
		t.Fatal("uninstall ownership", err)
	}
}
func TestCodexMCPApprovalAndOpenCodeLegacyScope(t *testing.T) {
	p := activityInput{Cwd: "/fixture", ToolName: "mcp__filesystem__read_file", ToolInput: json.RawMessage(`{"path":"/fixture/完整","nested":{"all":true}}`)}
	if !approvalInputSupported("codex", p) {
		t.Fatal("complete MCP rejected")
	}
	p.ToolName = "request_permissions"
	if approvalInputSupported("codex", p) {
		t.Fatal("permission set flattened")
	}
	p.ToolName = "mcp__server__tool"
	p.ToolInput = json.RawMessage(`{"path":"a","path":"b"}`)
	if approvalInputSupported("codex", p) {
		t.Fatal("ambiguous MCP input accepted")
	}
	raw, e := approvalOperation(fixtureLocator(), Activity{Agent: "opencode"}, activityInput{ToolName: "permission", ToolInput: json.RawMessage(`{"action":"bash","resources":["echo fixture"]}`)}, "id", 1, 121)
	if e != nil {
		t.Fatal(e)
	}
	var operation struct {
		Allowed []string `json:"allowed_decisions"`
	}
	_ = json.Unmarshal(raw, &operation)
	if len(operation.Allowed) != 1 || operation.Allowed[0] != "allow_once" {
		t.Fatal("legacy batch deny lacks scope")
	}
}
