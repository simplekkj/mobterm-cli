package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApprovalContextBindsNativeDirectory(t *testing.T) {
	for _, tool := range []string{"Bash", "apply_patch"} {
		input := activityInput{Cwd: "/work/one", ToolName: tool, ToolInput: json.RawMessage(`{"command":"update ./relative/file"}`)}
		activity := Activity{Agent: "codex", Session: "session", Run: "run"}
		first, err := approvalOperation(fixtureLocator(), activity, input, "request", 1, 121)
		if err != nil {
			t.Fatal(err)
		}
		input.Cwd = "/work/two"
		second, err := approvalOperation(fixtureLocator(), activity, input, "request", 1, 121)
		if err != nil || digest(string(first)) == digest(string(second)) {
			t.Fatal("execution directory not bound", err)
		}
		for _, cwd := range []string{"", "relative", "/bad\x00dir", "/bad\ndir", "/bad\u202edir", "/" + strings.Repeat("x", 4096)} {
			input.Cwd = cwd
			if _, err := approvalOperation(fixtureLocator(), activity, input, "request", 1, 121); err == nil {
				t.Fatal("invalid native directory accepted")
			}
		}
	}
	s := testStore(t)
	a, p, err := parseActivity(context.Background(), s, "codex", []byte(`{"hook_event_name":"PermissionRequest","session_id":"session","turn_id":"turn","cwd":"/work/native","tool_name":"Bash","tool_input":{"command":"pwd"}}`))
	if err != nil || a.State != "waiting_approval" || p.Cwd != "/work/native" || !approvalInputSupported("codex", p) {
		t.Fatal("native context lost", err)
	}
	if !approvalInputSupported("claude", p) {
		t.Fatal("valid Claude request refused")
	}
	if !approvalInputSupported("opencode", activityInput{ToolName: "permission", ToolInput: json.RawMessage(`{"action":"read","resources":["/work/file"]}`)}) {
		t.Fatal("complete OpenCode request refused")
	}
	input := activityInput{Cwd: "/" + strings.Repeat("d", 4095), ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"` + strings.Repeat("x", 5000) + `"}`)}
	if _, err := approvalOperation(fixtureLocator(), a, input, "request", 1, 121); err == nil {
		t.Fatal("oversize complete context accepted")
	}
}

func TestApprovalOldInspectionFrameCannotApprove(t *testing.T) {
	s := testStore(t)
	for _, command := range []string{"inspect", "decide", "release"} {
		_, err := approvalCommand(context.Background(), s, API{}, command, bytes.NewReader(framed(ApprovalFrame{V: 2, PairID: randomID(), RequestID: randomID()})))
		if err == nil || err.Error() != "invalid_approval_frame" {
			t.Fatal("old client must fall back to terminal", command, err)
		}
	}
}

func TestApprovalOldRequestAndTamperedContextCannotApprove(t *testing.T) {
	s := testStore(t)
	pair := testPair("installation_fixture_0000001", "https://fixture.invalid")
	if err := s.savePair(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	request := ApprovalRequest{V: 2, RequestID: randomID(), Digest: digest("{}"), OperationJSON: "{}", Agent: "codex", State: "pending"}
	if err := s.writeJSON("approval-"+request.RequestID+".json", request); err != nil {
		t.Fatal(err)
	}
	_, err := approvalCommand(context.Background(), s, API{}, "inspect", bytes.NewReader(framed(ApprovalFrame{V: 3, PairID: pair.PairID, RequestID: request.RequestID})))
	if err == nil || err.Error() != "approval_context_upgrade_required" {
		t.Fatal("old incomplete operation accepted", err)
	}
	raw, err := approvalOperation(fixtureLocator(), Activity{Agent: "codex", Session: "session", Run: "run"}, activityInput{Cwd: "/work/one", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"pwd"}`)}, request.RequestID, time.Now().Unix(), time.Now().Unix()+120)
	if err != nil {
		t.Fatal(err)
	}
	request.V = 3
	request.Digest = digest(string(raw))
	request.OperationJSON = strings.Replace(string(raw), "/work/one", "/work/two", 1)
	if err := s.writeJSON("approval-"+request.RequestID+".json", request); err != nil {
		t.Fatal(err)
	}
	if _, err := approvalRead(s, request.RequestID); err == nil {
		t.Fatal("tampered directory retained stale digest")
	}
}
