package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The ordinary CLI entry point, with only the test relay certificate trusted
// in this subprocess. No Keychain or global TLS configuration is modified.
func runReboundFixtureCLI(certPath string) int {
	raw, err := os.ReadFile(certPath)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(raw) {
		return 1
	}
	x509.SetFallbackRoots(roots)
	return run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func serveClaudeFixtureCompletion(w http.ResponseWriter, model string, stream bool) {
	message := map[string]any{"type": "message", "role": "assistant", "id": "msg_fixture_" + randomID(), "model": model, "content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 1}, "stop_reason": nil}
	if !stream {
		message["content"] = []map[string]string{{"type": "text", "text": "completed"}}
		message["stop_reason"] = "end_turn"
		printJSON(w, message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(name string, fields map[string]any) {
		fields["type"] = name
		data, _ := json.Marshal(fields)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	}
	send("message_start", map[string]any{"message": message})
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "text_delta", "text": "completed"}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 3}})
	send("message_stop", map[string]any{})
}

func TestLivePiReboundReply(t *testing.T) {
	testLiveReboundReply(t, "pi", os.Getenv("PI_BINARY"))
}

func TestLiveClaudeReboundReply(t *testing.T) {
	testLiveReboundReply(t, "claude", os.Getenv("CLAUDE_BINARY"))
}

func TestLiveOpenCodeReboundReply(t *testing.T) {
	testLiveReboundReply(t, "opencode", os.Getenv("OPENCODE_BINARY"))
}

func testLiveReboundReply(t *testing.T, agent, binary string) {
	t.Helper()
	if binary == "" {
		t.Skipf("set %s_BINARY for the interactive reply test", strings.ToUpper(agent))
	}
	root, err := os.MkdirTemp("/tmp", "mt-"+agent+"-reply-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// A real Claude idle_prompt follows Stop after the native idle timer.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	s := Store{filepath.Join(root, "store")}
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	events := make(chan Envelope, 8)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/pairs/"+p.PairID:
			printJSON(w, map[string]string{"status": "active", "pair_id": p.PairID, "installation_id": id})
		case strings.HasSuffix(r.URL.Path, "/interaction-capabilities"):
			printJSON(w, interactionTestGrant(p))
		case strings.HasSuffix(r.URL.Path, "/capabilities"):
			printJSON(w, activityCaps(p))
		case strings.HasSuffix(r.URL.Path, "/events"), strings.Contains(r.URL.Path, "/snapshots/"):
			var envelope Envelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Errorf("invalid envelope: %v", err)
				return
			}
			if envelope.Kind == "turn_completed" && strings.HasSuffix(r.URL.Path, "/events") {
				events <- envelope
			}
			printJSON(w, map[string]any{"v": 2, "event_id": envelope.EventID, "state": "accepted", "outbox_handoff": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()
	p.APIURL = relay.URL
	if err = s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	api := API{relay.Client()}
	cfg := replyTestConfig(p, 3, 0, true)
	cfg.Agent = agent
	key, _ := decode64(p.EncryptionKey, 32)
	cfg.Proof, _ = cfg.mac(key)
	if _, err := replyCommand(ctx, s, api, "configure", bytes.NewReader(interactionFrameBytes(cfg))); err != nil {
		t.Fatal(err)
	}

	const seed = "first fixture input"
	const text = "继续 🧪 second input"
	histories := make(chan []string, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			printJSON(w, map[string]int{"input_tokens": 10})
			return
		}
		var body struct {
			Stream   bool
			Model    string
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		var users []string
		for _, m := range body.Messages {
			if m.Role != "user" {
				continue
			}
			keep := func(value string) {
				if agent == "pi" || value == seed || value == text {
					users = append(users, value)
				}
			}
			var content string
			if json.Unmarshal(m.Content, &content) == nil {
				keep(content)
			} else {
				var parts []struct{ Type, Text string }
				if err := json.Unmarshal(m.Content, &parts); err != nil {
					t.Error(err)
					return
				}
				for _, part := range parts {
					if part.Type == "text" {
						keep(part.Text)
					}
				}
			}
		}
		if len(users) > 0 {
			histories <- users
		}
		if agent == "claude" {
			serveClaudeFixtureCompletion(w, body.Model, body.Stream)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"fixture","model":"local","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"completed"},"finish_reason":null}]}

data: {"id":"fixture","model":"local","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`)
	}))
	defer model.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, agent)
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	for _, dir := range []string{config, bin, home} {
		check(os.Mkdir(dir, 0700))
	}
	check(os.Symlink(binary, filepath.Join(bin, agent)))
	if agent == "pi" {
		check(os.WriteFile(filepath.Join(config, "models.json"), []byte(fmt.Sprintf(`{"providers":{"fixture":{"baseUrl":%q,"api":"openai-completions","apiKey":"synthetic-local","models":[{"id":"local"}]}}}`, model.URL+"/v1")), 0600))
	} else if agent == "opencode" {
		configJSON := fmt.Sprintf(`{"model":"fixture/local","update":"disable","providers":{"fixture":{"package":"@opencode/ai/providers/openai-compatible","env":["MOBTERM_FIXTURE_KEY"],"settings":{"baseURL":%q},"models":{"local":{"name":"Fixture","capabilities":{"tools":true},"limit":{"context":32000,"output":1024}}}}}}`, model.URL+"/v1")
		check(os.WriteFile(filepath.Join(config, "opencode.json"), []byte(configJSON), 0600))
	} else {
		configJSON := fmt.Sprintf(`{"hasCompletedOnboarding":true,"theme":"dark","projects":{%q:{"hasTrustDialogAccepted":true}},"customApiKeyResponses":{"approved":["offline-fixture"]}}`, root)
		check(os.WriteFile(filepath.Join(config, ".claude.json"), []byte(configJSON), 0600))
	}
	cert := filepath.Join(root, "relay.pem")
	check(os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: relay.Certificate().Raw}), 0600))
	env := []string{
		"HOME=" + home, "PATH=" + bin + ":" + os.Getenv("PATH"), "TERM=xterm-256color",
		"PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_CODING_AGENT_DIR=" + config,
		"MOBTERM_CONFIG_DIR=" + s.Dir, "MOBTERM_TEST_RELAY_CERT=" + cert,
		"GODEBUG=x509usefallbackroots=1", "NO_PROXY=127.0.0.1,localhost",
		// Race-instrumented helpers must still exit within the native protocol deadlines.
		"GORACE=atexit_sleep_ms=0",
	}
	if agent == "claude" {
		env = append(env, "CLAUDE_CONFIG_DIR="+config, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=offline-fixture", "ANTHROPIC_BASE_URL="+model.URL)
	}
	if agent == "opencode" {
		env = append(env, "OPENCODE_CONFIG_DIR="+config, "OPENCODE_DISABLE_AUTOUPDATE=1", "MOBTERM_FIXTURE_KEY=synthetic", "XDG_CONFIG_HOME="+filepath.Join(root, "xdg-config"), "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_STATE_HOME="+filepath.Join(root, "state"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"))
	}
	command := func(cmd string, args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, cmd, args...)
		c.Dir, c.Env = root, env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", filepath.Base(cmd), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	cli, err := os.Executable()
	check(err)
	if agent == "pi" && !ordinaryReplyVersion("pi", command(binary, "--version")) {
		t.Skip("native Pi version is below the ordinary reply minimum")
	}
	command(cli, "hooks", "install", "--agent", agent, "--json")
	socket := filepath.Join(root, "tmux.sock")
	tmux := func(args ...string) string {
		t.Helper()
		return command("tmux", append([]string{"-u", "-f", "/dev/null", "-S", socket}, args...)...)
	}
	file := filepath.Join(root, "session.jsonl")
	launch := "exec " + shellQuote(binary) + " --provider fixture --model local --session " + shellQuote(file)
	if agent == "claude" {
		launch = "exec " + shellQuote(binary) + " --plugin-dir " + shellQuote(filepath.Join(s.Dir, "native", "claude-reply")) + " --model sonnet"
	}
	originalLaunch := launch + " " + shellQuote(seed)
	if agent == "opencode" {
		launch = "exec " + shellQuote(binary) + " --standalone"
		originalLaunch = launch + " --prompt " + shellQuote(seed)
	}
	pane := tmux("new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "120", "-y", "35", "-c", root, originalLaunch)
	defer exec.Command("tmux", "-S", socket, "kill-server").Run()
	var envelope Envelope
	recoverDelivery := time.NewTicker(5 * time.Second)
	defer recoverDelivery.Stop()
	for envelope.EventID == "" {
		select {
		case envelope = <-events:
		case <-recoverDelivery.C:
			// The short native hook may leave its durable event pending (especially
			// under the race detector). Exercise the normal recovery entry point.
			records, e := outboxRecords(ctx, s, p.PairID, false)
			if e == nil {
				for _, d := range records {
					now := time.Now().Unix()
					if d.Kind == "turn_completed" && d.State == "pending" && d.Lease <= now && d.Next <= now {
						t.Log("recovering the pending source notification with outbox flush")
						command(cli, "outbox", "flush", "--json")
						break
					}
				}
			}
		case <-ctx.Done():
			t.Fatal("original completion did not reach relay")
		}
	}
	f := replyFrame{bodyIdentity: bodyIdentity{1, p.APIURL, id, p.PairID, envelope.EventID, envelopeBodyDigest(envelope), randomID()}}
	var original replyRecord
	for ctx.Err() == nil {
		records, e := outboxRecords(ctx, s, p.PairID, false)
		check(e)
		for _, d := range records {
			if d.EventID != envelope.EventID || d.Reply == nil {
				continue
			}
			original, e = readReply(s, d.Reply.SourceID)
			if e == nil && original.NativeTurn != "" && original.NativeStorage != "" {
				break
			}
		}
		if original.NativeTurn != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if original.NativeTurn == "" || original.NativeStorage == "" {
		t.Fatal("real source did not register a stable storage and assistant entry")
	}
	tmux("respawn-pane", "-k", "-t", pane, "exec /bin/sleep 120")
	if agent == "claude" {
		launch += " --resume " + shellQuote(original.Session)
		// Keep a real interactive PTY for Claude, while this new host has no
		// multiplexer namespace through which the adapter can prove a locator.
		launch = "exec /usr/bin/env -u TMUX -u TMUX_PANE " + strings.TrimPrefix(launch, "exec ")
	}
	if agent == "opencode" {
		launch += " --session " + shellQuote(original.Session)
	}
	newPane := tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-c", root, launch)
	if newPane == pane {
		t.Fatal("expected a different pane")
	}
	for ctx.Err() == nil {
		host, e := currentNativeHost(s, original.NativeStorage, func(h nativeHost) bool { return h.Logical == original.LogicalSession })
		if e == nil && host.PID != original.ParentPID {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.Expires = time.Now().Unix() + 30
	latest, e := replyRequest(ctx, s, api, p, "latest", f)
	check(e)
	if agent == "claude" {
		if latest.State != "unavailable" || latest.Reason != "navigation_binding_unconfirmed" || latest.Locator != nil {
			t.Fatalf("unmapped PTY produced a navigation target: %s %s", latest.State, latest.Reason)
		}
	} else if latest.State != "current" || latest.Locator == nil || latest.Locator.PaneID != newPane || latest.Locator.OccupantID == "" || latest.Locator.OccupantID == original.Locator.OccupantID {
		t.Fatalf("restored host did not prove its own strong pane: %s %s", latest.State, latest.Reason)
	}
	eligible, err := replyRequest(ctx, s, api, p, "inspect", f)
	check(err)
	if eligible.State != "eligible" {
		t.Fatalf("restored inspect: %s %s", eligible.State, eligible.Reason)
	}
	current, err := readReply(s, original.SourceID)
	check(err)
	if current.ParentPID == original.ParentPID || current.ParentBirth == original.ParentBirth || current.OwnerToken == original.OwnerToken || current.NativeTurn != original.NativeTurn || current.Locator != original.Locator {
		t.Fatal("rebind did not keep the exact source and transfer the live waiter")
	}
	decision := replySendFrame(f, current, text)
	decision.Expires = min(decision.Expires, eligible.Expires)
	sent, err := replyRequest(ctx, s, api, p, "send", decision)
	check(err)
	if sent.State != "decision_recorded" {
		t.Fatalf("send: %s %s", sent.State, sent.Reason)
	}
	for i, want := range [][]string{{seed}, {seed, text}} {
		select {
		case got := <-histories:
			if !slices.Equal(got, want) {
				t.Fatalf("model call %d received wrong user history %q", i, got)
			}
		case <-ctx.Done():
			t.Fatalf("missing model call %d", i)
		}
	}
	wantReceipt := "delivery_unknown"
	if agent == "opencode" {
		wantReceipt = "native_consumed"
	}
	if agent == "claude" {
		wantReceipt = "native_accepted"
	}
	decision.Text = ""
	for ctx.Err() == nil {
		decision.Expires = time.Now().Unix() + 30
		status, err := replyRequest(ctx, s, api, p, "status", decision)
		check(err)
		if status.State == wantReceipt {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	check(ctx.Err())
	tmux("kill-pane", "-t", newPane)
	decision.Expires = time.Now().Unix() + 30
	status, err := replyRequest(ctx, s, api, p, "status", decision)
	check(err)
	durable, err := readReply(s, original.SourceID)
	check(err)
	if status.State != wantReceipt || status.Decision != decision.DecisionID || durable.Frame == nil || durable.Frame.Text != "" || durable.Frame.DecisionID != decision.DecisionID {
		t.Fatal("original decision did not remain queryable without its body after native exit")
	}
}
