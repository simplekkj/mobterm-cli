package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func signedBodyFrame(t *testing.T, p Pair, e Envelope) bodyRequest {
	t.Helper()
	f := bodyRequest{bodyIdentity{1, strings.TrimSuffix(p.APIURL, "/"), p.InstallationID, p.PairID, e.EventID, envelopeBodyDigest(e), randomID()}, time.Now().Unix() + 30, ""}
	k, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		t.Fatal(err)
	}
	f.Proof, err = bodyMAC(k, f)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func readTestBody(t *testing.T, s Store, f bodyRequest) bodyResponse {
	t.Helper()
	r, e := readMessageBody(context.Background(), s, bytes.NewReader(framed(f)))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func checkTestBody(t *testing.T, s Store, p Pair, e Envelope, want string) {
	t.Helper()
	f := signedBodyFrame(t, p, e)
	r := readTestBody(t, s, f)
	raw, err := base64.StdEncoding.DecodeString(r.Base64)
	if err != nil || r.Status != "available" || r.bodyIdentity != f.bodyIdentity || r.Bytes != len(want) || r.Digest != digest(want) || string(raw) != want || r.Expires <= time.Now().Unix() {
		t.Fatalf("body read %s length %d", r.Status, r.Bytes)
	}
}
func TestMessageBodySharedVector(t *testing.T) {
	var v struct {
		Key      string `json:"key_base64"`
		Envelope Envelope
		Request  bodyRequest
	}
	b, err := os.ReadFile(testdataDir + "/message-body-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	key, err := decode64(v.Key, 32)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bodyMAC(key, v.Request)
	if err != nil || got != v.Request.Proof || envelopeBodyDigest(v.Envelope) != v.Request.Envelope {
		t.Fatal("cross-language vector mismatch")
	}
}
func TestMessageBodyAccessAndLimits(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "https://example.test")
	if err = s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	l := fixtureLocator()
	l.InstallationID = id
	if got := newAssistantBody("\x1b[32m前言\x1b[0m\n\n  \t🐱\n```go\nx()\n```").Text; got != "前言\n\n  \t🐱\n```go\nx()\n```" {
		t.Fatal("body formatting changed")
	}
	if got := newAssistantBody("<think>internal</think>\nfinal").Text; got != "final" {
		t.Fatal("tagged reasoning leaked")
	}
	text := strings.Repeat("\\\"", bodyLimit/2)
	b := newAssistantBody(text)
	d, err := enqueueBodyDelivery(ctx, s, p, "turn_completed", "occurrence", l, "codex", "excerpt", nil, &b)
	if err != nil {
		t.Fatal(err)
	}
	f := signedBodyFrame(t, p, *d.Envelope)
	checkTestBody(t, s, p, *d.Envelope, text)
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"message", "read", "--stdin-frame"}, bytes.NewReader(framed(f)), &stdout, &stderr); code != 0 {
		t.Fatal("normal entry failed", code)
	}
	var wireResponse bodyResponse
	if err = json.Unmarshal(stdout.Bytes(), &wireResponse); err != nil || wireResponse.Status != "available" || wireResponse.bodyIdentity != f.bodyIdentity || wireResponse.Bytes != bodyLimit {
		t.Fatal("read entry response", err)
	}
	reply := readTestBody(t, s, f)
	wire, _ := json.Marshal(reply)
	if len(wire) > 768<<10 {
		t.Fatal("response budget")
	}
	bad := f
	bad.Nonce = randomID()
	if r := readTestBody(t, s, bad); r.Status != "denied" {
		t.Fatal("nonce tamper accepted")
	}
	changed := *d.Envelope
	changed.Ciphertext += "A"
	if r := readTestBody(t, s, signedBodyFrame(t, p, changed)); r.Status != "denied" {
		t.Fatal("another envelope matched")
	}
	bad = f
	bad.Event = "../../pairs.json"
	if _, err = readMessageBody(ctx, s, bytes.NewReader(framed(bad))); err == nil {
		t.Fatal("path accepted")
	}
	empty := testStore(t)
	if r := readTestBody(t, empty, f); r.Status != "unavailable" {
		t.Fatal(r.Status)
	}
	if _, err = os.Stat(empty.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created store")
	}
	oversized := newAssistantBody(text + "x")
	d2, err := enqueueBodyDelivery(ctx, s, p, "turn_completed", "oversize", l, "codex", "excerpt", nil, &oversized)
	if err != nil {
		t.Fatal(err)
	}
	if r := readTestBody(t, s, signedBodyFrame(t, p, *d2.Envelope)); r.Status != "too_large" || r.Base64 != "" {
		t.Fatal(r.Status)
	}
	before := *d.Envelope
	bodyPath := filepath.Join(s.bodyDir(), bodyName(d.Body.Ref, p))
	if err := os.WriteFile(bodyPath, []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if r := readTestBody(t, s, f); r.Status != "unavailable" || r.Base64 != "" {
		t.Fatal("invalid UTF8/body mismatch accepted")
	}
	if err := os.WriteFile(bodyPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := readMessageBody(cancelled, s, bytes.NewReader(nil)); err == nil {
		t.Fatal("cancelled request accepted")
	}
	_, err = updateDelivery(ctx, s, p.PairID, d.EventID, func(current *delivery) error {
		current.Envelope = nil
		current.State = "handed_off"
		current.Expires = time.Now().Unix() - 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkTestBody(t, s, p, before, text)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); checkTestBody(t, s, p, before, text) }()
	}
	wg.Wait()
	if err = s.retirePair(ctx, p.PairID, "revoked", false); err != nil {
		t.Fatal(err)
	}
	if r := readTestBody(t, s, f); r.Status != "denied" {
		t.Fatal("retired pair could read")
	}
	files, err := os.ReadDir(s.bodyDir())
	if err != nil || len(files) != 0 {
		t.Fatal("retirement did not clear archive")
	}
}
func TestMessageBodyRetentionAndWriteFailures(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://example.test")
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	l := fixtureLocator()
	l.InstallationID = id
	b := newAssistantBody("x")
	now := time.Now().Unix()
	old := s.freezeBody(p, b, now-bodyTTL)
	d, err := enqueueBodyDelivery(ctx, s, p, "turn_completed", "old", l, "codex", "excerpt", old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := readTestBody(t, s, signedBodyFrame(t, p, *d.Envelope)); r.Status != "missing" {
		t.Fatal("expired body read")
	}
	if _, err = os.Stat(filepath.Join(s.bodyDir(), bodyName(*old, p))); err != nil {
		t.Fatal("read performed physical cleanup")
	}
	if r := s.freezeBody(p, b, now); r.Status != "available" {
		t.Fatal(r.Status)
	}
	if _, err = os.Stat(filepath.Join(s.bodyDir(), bodyName(*old, p))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired not reclaimed")
	}
	// Populate private raw files cheaply, then exercise the actual writer under both limits.
	for i := 0; i < bodyFileLimit; i++ {
		r := bodyRef{ID: randomID(), Created: now - 1000 + int64(i)}
		path := filepath.Join(s.bodyDir(), bodyName(r, p))
		if err = os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.freezeBody(p, b, now); r.Status != "available" {
		t.Fatal(r.Status)
	}
	files, _ := os.ReadDir(s.bodyDir())
	if len(files) != bodyFileLimit {
		t.Fatal(len(files))
	}
	for i := 0; i < bodyTotalLimit/bodyLimit; i++ {
		if err = os.Truncate(filepath.Join(s.bodyDir(), files[i].Name()), bodyLimit); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.freezeBody(p, newAssistantBody(strings.Repeat("x", bodyLimit)), now); r.Status != "available" {
		t.Fatal(r.Status)
	}
	files, _ = os.ReadDir(s.bodyDir())
	total := int64(0)
	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	if total > bodyTotalLimit {
		t.Fatal(total)
	}
	// A wrong-type archive root between observations cannot consume the base event.
	if err = os.Rename(s.bodyDir(), s.bodyDir()+"-fixture"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(s.bodyDir()+"-fixture", s.bodyDir()); err != nil {
		t.Fatal(err)
	}
	d, err = enqueueBodyDelivery(ctx, s, p, "turn_completed", "failed-save", l, "codex", "base", nil, &b)
	if err != nil || d.Envelope == nil || d.Body.Ref.Status != "save_failed" {
		t.Fatal("optional storage consumed delivery", err)
	}
	// Retirement revokes first even if physical deletion fails.
	if err = s.retirePair(ctx, p.PairID, "revoked", false); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	if r := readTestBody(t, s, signedBodyFrame(t, p, *d.Envelope)); r.Status != "denied" {
		t.Fatal(r.Status)
	}
}
func TestMessageBodyClaudeTargetAndStopOwnership(t *testing.T) {
	claudeNotifyEnvironment(t)
	s := testStore(t)
	ctx := context.Background()
	id, _ := s.installation()
	p := testPair(id, "https://example.test")
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	event := func(name, prompt, agent, text string) Activity {
		input, _ := json.Marshal(map[string]string{"session_id": "s", "prompt_id": prompt, "agent_id": agent, "hook_event_name": name, "last_assistant_message": text, "notification_type": "idle_prompt", "tool_use_id": "t", "tool_name": "Bash"})
		a, _, err := parseActivity(ctx, s, "claude", input)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	event("UserPromptSubmit", "p", "", "")
	event("Stop", "p", "", "正文\n\n  🐱")
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	idle := event("Notification", "p", "", "")
	ref := s.claudeBodies(idle, l)[p.PairID]
	if ref == nil || ref.Digest != digest("正文\n\n  🐱") {
		t.Fatal("lost exact Claude candidate")
	}
	moved := l
	moved.OccupantID = "tmux-proc-v1:" + strings.Repeat("1", 64)
	if len(s.claudeBodies(idle, moved)) != 0 {
		t.Fatal("rebound a candidate to another target")
	}
	event("Stop", "p", "", "duplicate cannot replace")
	if r := s.claudeBodies(idle, l)[p.PairID]; r == nil || r.ID != ref.ID {
		t.Fatal("duplicate stop replaced captured body")
	}
	if idleMissingPrompt := event("Notification", "", "", ""); len(s.claudeBodies(idleMissingPrompt, l)) > 0 {
		t.Fatal("absent prompt adopted capture")
	}
	event("Stop", "p", "child", "private")
	if r := s.claudeBodies(idle, l)[p.PairID]; r == nil || r.ID != ref.ID {
		t.Fatal("child changed root")
	}
	event("PreToolUse", "p", "", "")
	if len(s.claudeBodies(idle, l)) != 0 {
		t.Fatal("resumed candidate read")
	}
	event("UserPromptSubmit", "q", "", "")
	event("Stop", "p", "", "old")
	if a := event("Notification", "q", "", ""); a.State != "" {
		t.Fatal("stale prompt completed")
	}
}

func TestNativeCompletionBodyInputBudget(t *testing.T) {
	s := testStore(t)
	text := strings.Repeat("\\\"", bodyLimit/2)
	frame := map[string]any{"adapter_revision": 2, "runtime_version": "2.1.0", "event": "session.execution.succeeded", "session_id": "root", "event_id": "occur", "instance_id": "instance", "run_id": "run", "completion_body_base64": base64.StdEncoding.EncodeToString([]byte(text))}
	parse := func() (Activity, error) {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		a, _, err := parseActivity(context.Background(), s, "opencode", raw)
		return a, err
	}
	a, err := parse()
	if err != nil || a.Body.Text != text || a.Reminder != "turn_completed" {
		t.Fatal("bounded escaping/base64 not fully decoded", err)
	}
	frame["completion_body_base64"] = "***"
	a, err = parse()
	if err != nil || a.Body.Text != "" || a.Reminder != "turn_completed" {
		t.Fatal("bad extension consumed base event", err)
	}
	frame["event"] = "session.execution.started"
	if _, err = parse(); err == nil {
		t.Fatal("non-completion accepted extension")
	}
	frame["completion_body_base64"] = strings.Repeat("x", 1<<20)
	if _, err = parse(); err == nil {
		t.Fatal("hook budget bypass")
	}
}
