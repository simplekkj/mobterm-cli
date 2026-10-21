package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const waitSession = "session-with-private-content"

func claudeWaitPayload(notification string) []byte {
	b, _ := json.Marshal(map[string]any{
		"session_id": waitSession, "hook_event_name": "Notification", "notification_type": notification,
		"message": "PRIVATE_MESSAGE", "transcript_path": "/PRIVATE_TRANSCRIPT/not-readable",
		"tool_input": map[string]string{"content": "PRIVATE_TOOL_INPUT"},
	})
	return b
}

func seedClaudeCycle(t *testing.T, s Store, state string) {
	t.Helper()
	if state == "missing" {
		return
	}
	updated := time.Now().Unix()
	if state == "expired" {
		updated -= 86401
	}
	if err := s.writeJSON("claude-cycles.json", map[string]claudeCycle{
		digest(waitSession): {Cycle: "existing-cycle", Updated: updated},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeWaitWithoutCompletionAssociation(t *testing.T) {
	for _, notification := range []string{"permission_prompt", "elicitation_dialog"} {
		for _, state := range []string{"missing", "expired", "valid"} {
			t.Run(notification+"/"+state, func(t *testing.T) {
				s := testStore(t)
				if _, err := s.installation(); err != nil {
					t.Fatal(err)
				}
				seedClaudeCycle(t, s, state)
				before, _ := os.ReadFile(filepath.Join(s.Dir, "claude-cycles.json"))
				seen := map[string]bool{}
				for i := 0; i < 2; i++ {
					kind, identity, err := parseEvent(context.Background(), s, "claude", claudeWaitPayload(notification))
					if err != nil || kind != "needs_input" || len(identity) != 64 || seen[identity] {
						t.Fatalf("wait %d: kind=%q identity length=%d unique=%v err=%v", i, kind, len(identity), !seen[identity], err)
					}
					seen[identity] = true
				}
				after, _ := os.ReadFile(filepath.Join(s.Dir, "claude-cycles.json"))
				if !bytes.Equal(before, after) {
					t.Fatal("human wait changed or renewed completion association")
				}
				kind, identity, err := parseEvent(context.Background(), s, "claude", claudeWaitPayload("idle_prompt"))
				if err != nil {
					t.Fatal(err)
				}
				if state == "valid" {
					if kind != "turn_completed" || identity == "" {
						t.Fatal("valid completion association lost")
					}
				} else if kind != "" || identity != "" {
					t.Fatal("human wait fabricated a completion association")
				}
			})
		}
	}
}

// These command fixtures exercise notify's actual version and target checks;
// the separate live-tmux test verifies the real terminal command contract.
func claudeNotifyEnvironment(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for name, script := range map[string]string{
		"claude": "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf '%s\\n' \"$FIXTURE_CLAUDE_VERSION\"\n",
		"tmux":   "#!/bin/sh\n[ \"$1\" = -S ] && [ \"$2\" = /fixture/tmux.sock ] && [ \"$3\" = list-panes ] || exit 1\nprintf '%s\\t%s\\t%s\\t%s\\t%s\\t%s\\n' /fixture/tmux.sock 123:456:789 '$1' '@2' '%3' \"$FIXTURE_PANE_PID\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FIXTURE_PANE_PID", fmt.Sprint(os.Getpid()))
	t.Setenv("FIXTURE_CLAUDE_VERSION", "2.1.282 (Claude Code)")
	t.Setenv("TMUX", "/fixture/tmux.sock,123,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("HERDR_PANE_ID", "")
}

func TestClaudeWaitNotifyEncryptedRetries(t *testing.T) {
	claudeNotifyEnvironment(t)
	for _, notification := range []string{"permission_prompt", "elicitation_dialog"} {
		for _, state := range []string{"missing", "expired", "valid"} {
			t.Run(notification+"/"+state, func(t *testing.T) {
				s := testStore(t)
				t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
				id, err := s.installation()
				if err != nil {
					t.Fatal(err)
				}
				seedClaudeCycle(t, s, state)
				var mu sync.Mutex
				var bodies [][]byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || r.URL.Path != "/v1/pairs/pair_fixture_000000000000000001/events" {
						t.Error("wrong notify request")
					}
					mu.Lock()
					bodies = append(bodies, b)
					attempt := len(bodies)
					mu.Unlock()
					if attempt%2 == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						printJSON(w, map[string]string{"state": "retryable_failed", "error": "apns_unavailable"})
						return
					}
					var e Envelope
					_ = json.Unmarshal(b, &e)
					printJSON(w, map[string]string{"event_id": e.EventID, "state": "apns_accepted"})
				}))
				defer server.Close()
				p := testPair(id, server.URL)
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					var out, diag bytes.Buffer
					if code := run([]string{"notify", "--source", "claude"}, bytes.NewReader(claudeWaitPayload(notification)), &out, &diag); code != 0 || out.Len() != 0 || !strings.Contains(diag.String(), "agent continues") {
						t.Fatalf("notification failed or produced output: exit=%d stdout=%d stderr=%d", code, out.Len(), diag.Len())
					}
					records, e := outboxRecords(context.Background(), s, "", false)
					if e != nil {
						t.Fatal(e)
					}
					for _, d := range records {
						if d.State == "pending" {
							_, e = updateDelivery(context.Background(), s, d.PairID, d.EventID, func(d *delivery) error { d.Next = 0; return nil })
							if e != nil {
								t.Fatal(e)
							}
						}
					}
					if _, e = flushOutbox(context.Background(), s, newAPI(), "", false); e != nil {
						t.Fatal(e)
					}
				}
				mu.Lock()
				defer mu.Unlock()
				if len(bodies) != 4 {
					t.Fatalf("two independent waits with retries sent %d requests, want 4", len(bodies))
				}
				if !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[2], bodies[3]) {
					t.Fatal("HTTP retry changed exact event ID/nonce/ciphertext body")
				}
				key, err := decode64(p.EncryptionKey, 32)
				if err != nil {
					t.Fatal(err)
				}
				var first Envelope
				for _, index := range []int{0, 2} {
					var envelope Envelope
					if err := json.Unmarshal(bodies[index], &envelope); err != nil {
						t.Fatal(err)
					}
					locator, err := openEnvelope(key, envelope, time.Now())
					want := Locator{V: 1, InstallationID: id, Backend: "tmux", Namespace: "/fixture/tmux.sock", ServerIdentity: "123:456:789", WorkspaceID: "$1", ViewID: "@2", PaneID: "%3"}
					want.OccupantID, _ = processIdentity(os.Getpid())
					if err != nil || locator != want || envelope.Kind != "needs_input" {
						t.Fatalf("encrypted wait target mismatch: %v", err)
					}
					if index == 0 {
						first = envelope
					} else if envelope.EventID == first.EventID || envelope.Nonce == first.Nonce || envelope.Ciphertext == first.Ciphertext {
						t.Fatal("separate native waits reused encrypted event")
					}
					for _, secret := range []string{waitSession, "PRIVATE_", "/fixture/tmux.sock", p.EncryptionKey, p.SendToken} {
						if bytes.Contains(bodies[index], []byte(secret)) {
							t.Fatal("plaintext secret in event body")
						}
					}
				}
				for _, name := range []string{"events.json", "claude-cycles.json"} {
					data, err := os.ReadFile(filepath.Join(s.Dir, name))
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if bytes.Contains(data, []byte("PRIVATE_")) || bytes.Contains(data, []byte(waitSession)) {
						t.Fatal("raw hook payload persisted")
					}
				}
			})
		}
	}
}

func TestClaudeWaitRetainsVersionSessionAndTargetGuards(t *testing.T) {
	claudeNotifyEnvironment(t)
	for _, guard := range []string{"version", "session", "target", "ambiguous_target"} {
		t.Run(guard, func(t *testing.T) {
			s := testStore(t)
			t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
			id, err := s.installation()
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("unsafe human wait reached API")
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			if err := s.savePair(context.Background(), testPair(id, server.URL)); err != nil {
				t.Fatal(err)
			}
			payload := claudeWaitPayload("permission_prompt")
			switch guard {
			case "version":
				t.Setenv("FIXTURE_CLAUDE_VERSION", "unsupported")
			case "session":
				payload = bytes.Replace(payload, []byte(waitSession), nil, 1)
			case "target":
				t.Setenv("TMUX_PANE", "%99")
			case "ambiguous_target":
				t.Setenv("HERDR_PANE_ID", "w1:p1")
			}
			var out, diag bytes.Buffer
			code := run([]string{"notify", "--source", "claude"}, bytes.NewReader(payload), &out, &diag)
			if code != 0 || out.Len() != 0 || diag.String() != "mobterm-cli: notification unavailable (agent continues): request_failed\n" {
				t.Fatal("guard failure changed silent, nonblocking hook contract")
			}
			if strings.Contains(diag.String(), "PRIVATE_") {
				t.Fatal("hook payload leaked to diagnostics")
			}
		})
	}
}

func TestClaudeWaitEnvelopeCacheBounded(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	l := fixtureLocator()
	l.InstallationID = id
	ctx := context.Background()
	seedOutbox(t, s, p, 511, "pending", time.Now().Unix())
	kind, identity, err := parseEvent(ctx, s, "claude", claudeWaitPayload("permission_prompt"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eventEnvelope(ctx, s, p, kind, identity, l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventEnvelope(ctx, s, p, kind, "next-wait", l); err == nil {
		t.Fatal("cache grew beyond 512 pending events")
	}
	if retry, err := eventEnvelope(ctx, s, p, kind, identity, l); err != nil || retry != e {
		t.Fatal("full cache stopped stable retry")
	}
	records, err := outboxRecords(ctx, s, "", false)
	if err != nil || len(records) != 512 {
		t.Fatal("pending capacity", len(records), err)
	}
	for _, d := range records {
		if d.EventID != e.EventID {
			_, err = updateDelivery(ctx, s, d.PairID, d.EventID, func(d *delivery) error { d.Expires = time.Now().Unix() - 1; return nil })
			break
		}
	}
	if _, err = outboxRecords(ctx, s, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err = eventEnvelope(ctx, s, p, kind, "next-wait", l); err != nil {
		t.Fatal("expired pending capacity not released", err)
	}
	summary, err := outboxSummary(ctx, s, "")
	if err != nil || summary["pending"] != 512 || summary["terminated"] != 1 {
		t.Fatal("lost tombstone or pending count", summary, err)
	}
}
