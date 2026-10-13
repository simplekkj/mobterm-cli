package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) Store { t.Helper(); return Store{filepath.Join(t.TempDir(), "state")} }
func testPair(id, url string) Pair {
	return Pair{SetupPacket{V: 1, APIURL: url, PairID: "pair_fixture_000000000000000001", InstallationID: id, SendToken: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)), EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))}, "active"}
}
func TestSetupReadbackAndRepeat(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/claim") {
			t.Error("wrong route")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["claim_id"] != "pair_fixture_000000000000000001" || body["installation_id"] != id {
			t.Error("bad claim")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing auth")
		}
		printJSON(w, map[string]string{"status": "active"})
	}))
	defer server.Close()
	p := testPair(id, server.URL)
	a := API{server.Client()}
	for i := 0; i < 2; i++ {
		v, err := setup(context.Background(), s, a, bytes.NewReader(framed(p.SetupPacket)))
		if err != nil || v["local_saved"] != true {
			t.Fatalf("setup %v %v", v, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("repeat did not reach idempotent claim")
	}
	info, _ := os.Stat(filepath.Join(s.Dir, "pairs.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	p.InstallationID = "different_install_000000000000"
	if _, err = setup(context.Background(), s, a, bytes.NewReader(framed(p.SetupPacket))); err == nil {
		t.Fatal("wrong installation accepted")
	}
}
func TestStoreConcurrentIdentityAndSymlink(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.installation()
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("installation race")
		}
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Dir, "evil.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if s.writeJSON("evil.json", map[string]string{"secret": "never"}) == nil {
		t.Fatal("symlink overwritten")
	}
	if s.readJSON("evil.json", new(any)) == nil {
		t.Fatal("symlink followed")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "preserve" {
		t.Fatal("outside modified")
	}
}
func TestRetryReusesExactEnvelopeAcrossInvocations(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	l := fixtureLocator()
	l.InstallationID = id
	ctx := context.Background()
	e, err := eventEnvelope(ctx, s, p, "turn_completed", "source-turn-a", l)
	if err != nil {
		t.Fatal(err)
	}
	same, err := eventEnvelope(ctx, s, p, "turn_completed", "source-turn-a", l)
	if err != nil || same != e {
		t.Fatal("retry envelope changed")
	}
	next, err := eventEnvelope(ctx, s, p, "turn_completed", "source-turn-b", l)
	if err != nil || next.EventID == e.EventID {
		t.Fatal("distinct turns merged")
	}
	l.PaneID = "%4"
	if _, err = eventEnvelope(ctx, s, p, "turn_completed", "source-turn-a", l); err == nil {
		t.Fatal("reused ID target changed")
	}
}
func TestSendBoundedRetriesAndStage(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e Envelope
		_ = json.NewDecoder(r.Body).Decode(&e)
		b, _ := json.Marshal(e)
		bodies = append(bodies, string(b))
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			printJSON(w, map[string]string{"state": "retryable_failed", "error": "apns_unavailable"})
			return
		}
		printJSON(w, map[string]string{"state": "apns_accepted"})
	}))
	defer server.Close()
	a := API{server.Client()}
	p := testPair("install_fixture000000000000", server.URL)
	e := Envelope{V: 1, EventID: "event_fixture00000000000"}
	v, err := a.send(context.Background(), p, e)
	if err != nil || v["state"] != "apns_accepted" || calls.Load() != 2 || bodies[0] != bodies[1] {
		t.Fatal("retry or stage failed", err)
	}
}
func TestClaudeSemantics(t *testing.T) {
	s := testStore(t)
	_, _ = s.installation()
	ctx := context.Background()
	invoke := func(event, notification, tool string) (string, string, error) {
		p := map[string]any{"session_id": "session-a", "hook_event_name": event, "notification_type": notification, "tool_use_id": tool, "transcript_path": "/must/not/read", "message": "SECRET", "tool_input": map[string]string{"content": "SECRET"}}
		b, _ := json.Marshal(p)
		return parseEvent(ctx, s, "claude", b)
	}
	for _, ev := range []string{"Stop", "SubagentStop", "PermissionRequest"} {
		kind, _, err := invoke(ev, "", "")
		if err != nil || kind != "" {
			t.Fatal("unsafe event mapped")
		}
	}
	_, _, _ = invoke("UserPromptSubmit", "", "")
	_, _, _ = invoke("PreToolUse", "", "tool-a")
	kind, id, err := invoke("Notification", "permission_prompt", "")
	if err != nil || kind != "needs_input" {
		t.Fatal("human wait missing", err)
	}
	_, retry, err := invoke("Notification", "permission_prompt", "")
	if err != nil || retry == id {
		t.Fatal("distinct native input occurrences merged")
	}
	kind, done, err := invoke("Notification", "idle_prompt", "")
	if err != nil || kind != "turn_completed" {
		t.Fatal("native completion missing")
	}
	kind, _, _ = invoke("Notification", "auth_success", "")
	if kind != "" {
		t.Fatal("auth mapped")
	}
	_, _, _ = invoke("UserPromptSubmit", "", "")
	_, next, _ := invoke("Notification", "idle_prompt", "")
	if next == done {
		t.Fatal("turns merged")
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, "claude-cycles.json"))
	if bytes.Contains(b, []byte("SECRET")) || bytes.Contains(b, []byte("transcript")) {
		t.Fatal("payload persisted")
	}
}
func TestCodexStableIdentityAndIgnoreApproval(t *testing.T) {
	s := testStore(t)
	for _, typ := range []string{"PermissionRequest", "Stop", "approval-requested"} {
		b, _ := json.Marshal(map[string]string{"type": typ})
		kind, _, err := parseEvent(context.Background(), s, "codex", b)
		if err != nil || kind != "" {
			t.Fatal("unsafe event mapped")
		}
	}
	kind, id, err := parseEvent(context.Background(), s, "codex", []byte(`{"type":"agent-turn-complete","thread-id":"thread-a","turn-id":"turn-a","last-assistant-message":"never upload"}`))
	if err != nil || kind != "turn_completed" || len(id) != 64 {
		t.Fatal("complete failed")
	}
	if _, _, err = parseEvent(context.Background(), s, "codex", []byte(`{"type":"agent-turn-complete","thread-id":"thread-a"}`)); err == nil {
		t.Fatal("missing turn identity accepted")
	}
}
func TestFrameCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	err = readFrameContext(ctx, reader, new(SetupPacket))
	if err == nil {
		t.Fatal("blocked frame did not cancel")
	}
}
func TestHookFailureIsNonblockingAndSilent(t *testing.T) {
	t.Setenv("MOBTERM_CONFIG_DIR", filepath.Join(t.TempDir(), "state"))
	var out, diag bytes.Buffer
	code := run([]string{"notify", "--source", "codex", `{"type":"agent-turn-complete","thread-id":"t","turn-id":"u","last-assistant-message":"SECRET"}`}, strings.NewReader(""), &out, &diag)
	if code != 0 || out.Len() != 0 || strings.Contains(diag.String(), "SECRET") {
		t.Fatal("hook influenced agent or leaked input")
	}
}
func TestExpiredCachedEventIsNotRenewed(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	l := fixtureLocator()
	l.InstallationID = id
	e, err := eventEnvelope(context.Background(), s, p, "turn_completed", "old", l)
	if err != nil {
		t.Fatal(err)
	}
	var cache map[string]cachedEnvelope
	_ = s.readJSON("events.json", &cache)
	for k, v := range cache {
		v.Envelope.ExpiresAt = time.Now().Unix() - 1
		cache[k] = v
	}
	_ = s.writeJSON("events.json", cache)
	_, err = eventEnvelope(context.Background(), s, p, e.Kind, "old", l)
	if err == nil {
		t.Fatal("expired event renewed")
	}
}
