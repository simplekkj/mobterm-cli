package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestV2Golden(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	nonce := make([]byte, 12)
	for i := range nonce {
		nonce[i] = byte(i + 32)
	}
	l := fixtureLocator()
	l.OccupantID = "tmux-proc-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p := RunPayload{ActiveToolCount: 1, V: 2, Locator: l, Agent: "codex", SessionID: "session_fixture", RunID: "run_fixture", StreamID: "stream_fixture_000000000000001", Revision: 7, State: "waiting_approval", OccurredAt: 1999999900, Tools: []RunTool{{"invocation_fixture", "shell", "pending"}}, Approval: &ApprovalHint{"request_fixture_00000000000001", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 2000000000}}
	e, err := sealRun(key, Envelope{V: 2, PairID: "pair_fixture_000000000000000001", EventID: "event_fixture_00000000000000001", Kind: "needs_input", StreamID: p.StreamID, Revision: p.Revision, CapabilityGeneration: 1, ExpiresAt: 2000000000}, p, nonce)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(p)
	f := golden{2, base64.StdEncoding.EncodeToString(key), string(plain), string(e.aad()), e}
	b, _ := json.MarshalIndent(f, "", "  ")
	if os.Getenv("UPDATE_V2_GOLDEN") == "1" {
		if err = os.WriteFile("testdata/protocol-v2.json", append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/protocol-v2.json")
	if err != nil || !bytes.Equal(bytes.TrimSpace(want), b) {
		t.Fatal("v2 golden mismatch", err)
	}
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	encrypted, _ := base64.StdEncoding.DecodeString(e.Ciphertext)
	for _, mutate := range []func(*Envelope){func(e *Envelope) { e.Kind = "state" }, func(e *Envelope) { e.Revision++ }, func(e *Envelope) { e.CapabilityGeneration++ }, func(e *Envelope) { e.StreamID = "other_stream_0000000000000001" }} {
		copy := e
		mutate(&copy)
		if _, err := g.Open(nil, nonce, encrypted, copy.aad()); err == nil {
			t.Fatal("metadata authenticated")
		}
	}
}
func TestApprovalGolden(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	f := ApprovalFrame{V: 3, PairID: "pair_fixture_000000000000000001", RequestID: "request_fixture_00000000000001", Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", DecisionID: "decision_fixture_0000000000001", Action: "allow_once", PolicyGeneration: 3, ExpiresAt: 2000000000}
	operation, err := approvalOperation(fixtureLocator(), Activity{Agent: "codex", Session: "session_fixture", Run: "run_fixture"}, activityInput{Cwd: "/work/示例 project", ToolUseID: "tool_fixture", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"printf 'hello <world>'","description":"Fixture operation"}`)}, f.RequestID, 1999999880, f.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	f.Digest = digest(string(operation))
	proof, err := approvalMAC(key, f)
	if err != nil {
		t.Fatal(err)
	}
	f.Proof = proof
	b, _ := json.MarshalIndent(map[string]any{"key": base64.StdEncoding.EncodeToString(key), "frame": f, "operation_json": string(operation), "operation_sha256": f.Digest}, "", "  ")
	if os.Getenv("UPDATE_V2_GOLDEN") == "1" {
		if err = os.WriteFile("testdata/approval-v2.json", append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/approval-v2.json")
	if err != nil || !bytes.Equal(bytes.TrimSpace(want), b) {
		t.Fatal("approval golden mismatch", err)
	}
	f.Action = "deny"
	other, _ := approvalMAC(key, f)
	if other == proof {
		t.Fatal("action not bound")
	}
}
func TestRunParallelAndTerminalOrdering(t *testing.T) {
	s := Store{t.TempDir()}
	if err := os.Chmod(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l := fixtureLocator()
	a := Activity{Agent: "pi", Session: "session", Instance: "instance", Run: "run1", Occurrence: "start", State: "running"}
	_, _, _, err := applyActivity(ctx, s, l, a)
	if err != nil {
		t.Fatal(err)
	}
	a.State = "tool_running"
	a.ToolStatus = "running"
	a.ToolID = "a"
	a.Occurrence = "tool-a"
	applyActivity(ctx, s, l, a)
	a.ToolID = "b"
	a.Occurrence = "tool-b"
	applyActivity(ctx, s, l, a)
	a.State = "running"
	a.ToolStatus = "failed"
	a.ToolID = "a"
	a.Occurrence = "end-a"
	_, r, _, err := applyActivity(ctx, s, l, a)
	if err != nil || r.State != "tool_running" || len(r.Tools) != 2 {
		t.Fatal(r, err)
	}
	a.State = "turn_completed"
	a.ToolID = ""
	a.Occurrence = "done"
	_, r, _, _ = applyActivity(ctx, s, l, a)
	revision := r.Revision
	a.State = "running"
	a.Occurrence = "late"
	_, r, changed, err := applyActivity(ctx, s, l, a)
	if err != nil || changed || r.Revision != revision || r.State != "turn_completed" {
		t.Fatal(r, err)
	}
}
