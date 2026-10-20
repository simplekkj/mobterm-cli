package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func nativePayload(source string) nativeEvent {
	p := nativeEvent{AdapterRevision: 1, RuntimeVersion: agentCompatibilities[source].reference, Event: "session.execution.succeeded", SessionID: "private-session", EventID: "private-event", InstanceID: "private-instance"}
	if source == "pi" {
		no := false
		p.Event = "agent_settled"
		p.Aborted = &no
	}
	return p
}

func TestNativeEventContract(t *testing.T) {
	for _, source := range []string{"opencode", "pi"} {
		for _, sample := range []string{"minimum", "current", "future", "below", "prerelease", "missing_version", "wrong_revision", "missing_session", "missing_event", "missing_instance", "unknown_event", "aborted", "no_aborted", "oversize", "extra"} {
			t.Run(source+"/"+sample, func(t *testing.T) {
				p := nativePayload(source)
				want := "turn_completed"
				invalid := false
				switch sample {
				case "minimum":
					p.RuntimeVersion = agentCompatibilities[source].minimum.String()
				case "future":
					p.RuntimeVersion = "99.0.0"
				case "below":
					p.RuntimeVersion = "0.1.0"
					invalid = true
				case "prerelease":
					p.RuntimeVersion = "99.0.0-rc.1"
					invalid = true
				case "missing_version":
					p.RuntimeVersion = ""
					invalid = true
				case "wrong_revision":
					p.AdapterRevision = 2
					invalid = true
				case "missing_session":
					p.SessionID = ""
					invalid = true
				case "missing_event":
					p.EventID = ""
					invalid = true
				case "missing_instance":
					p.InstanceID = ""
					invalid = true
				case "unknown_event":
					p.Event = "unknown"
					want = ""
				case "aborted":
					yes := true
					p.Aborted = &yes
					want = ""
					invalid = source == "opencode"
				case "no_aborted":
					p.Aborted = nil
					invalid = source == "pi"
				case "oversize":
					p.EventID = strings.Repeat("x", 4096)
					invalid = true
				case "extra":
					invalid = true
				}
				data, _ := json.Marshal(p)
				if sample == "extra" {
					data = append(data[:len(data)-1], []byte(`,"prompt":"PRIVATE"}`)...)
				}
				kind, id, err := parseNativeEvent(source, data)
				if invalid {
					if err == nil {
						t.Fatal("invalid metadata accepted")
					}
					return
				}
				if err != nil || kind != want || (kind != "" && len(id) != 64) {
					t.Fatalf("kind=%q err=%v", kind, err)
				}
			})
		}
	}
	for _, event := range []string{"permission.asked", "form.created"} {
		p := nativePayload("opencode")
		p.Event = event
		b, _ := json.Marshal(p)
		if kind, _, err := parseNativeEvent("opencode", b); kind != "needs_input" || err != nil {
			t.Fatal("native wait mapping", err)
		}
	}
}

func TestNativeNotifyRetryAndTerminalScope(t *testing.T) {
	claudeNotifyEnvironment(t)
	for _, source := range []string{"opencode", "pi"} {
		t.Run(source, func(t *testing.T) {
			s := testStore(t)
			id, err := s.installation()
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, b)
				n := len(bodies)
				mu.Unlock()
				if n%2 == 1 {
					w.WriteHeader(503)
					return
				}
				printJSON(w, map[string]string{"state": "apns_accepted"})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			if err := s.savePair(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			event := nativePayload(source)
			for i := 0; i < 3; i++ {
				if i == 2 {
					event.InstanceID = "another-tui"
				}
				b, _ := json.Marshal(event)
				if _, err := notify(context.Background(), s, newAPI(), source, b, "", false); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 6 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[0], bodies[2]) || !bytes.Equal(bodies[2], bodies[3]) || !bytes.Equal(bodies[4], bodies[5]) || bytes.Equal(bodies[0], bodies[4]) {
				t.Fatal("reentry/retry/instance scope mismatch")
			}
			for _, body := range bodies {
				var fields map[string]any
				_ = json.Unmarshal(body, &fields)
				if len(fields) != 7 {
					t.Fatal("public envelope changed")
				}
				var e Envelope
				_ = json.Unmarshal(body, &e)
				key, _ := decode64(p.EncryptionKey, 32)
				l, err := openEnvelope(key, e, time.Now())
				if err != nil || l.PaneID != "%3" {
					t.Fatal("wrong target", err)
				}
				if bytes.Contains(body, []byte("private-")) {
					t.Fatal("source metadata exposed")
				}
			}
			cache, _ := os.ReadFile(filepath.Join(s.Dir, "events.json"))
			if bytes.Contains(cache, []byte("private-")) {
				t.Fatal("raw metadata persisted")
			}
			l := fixtureLocator()
			first := nativeTargetIdentity("same-source", l)
			l.PaneID = "%4"
			if first == nativeTargetIdentity("same-source", l) {
				t.Fatal("terminal dimension omitted")
			}
			t.Setenv("HERDR_PANE_ID", "w:p")
			data, _ := json.Marshal(event)
			if _, err := notify(context.Background(), s, newAPI(), source, data, "", false); err == nil {
				t.Fatal("ambiguous multiplexer accepted")
			}
		})
	}
}
