package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func summaryPlaintext(t *testing.T, p Pair, e Envelope) map[string]any {
	t.Helper()
	key, _ := decode64(p.EncryptionKey, 32)
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	nonce, _ := base64.StdEncoding.DecodeString(e.Nonce)
	encrypted, _ := base64.StdEncoding.DecodeString(e.Ciphertext)
	raw, err := g.Open(nil, nonce, encrypted, e.aad())
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMessageSummaryNativeNegotiationAndRetry(t *testing.T) {
	for _, v := range []int{1, 2} {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			claudeNotifyEnvironment(t)
			ctx := context.Background()
			s := testStore(t)
			id, _ := s.installation()
			pair := testPair(id, "")
			var mu sync.Mutex
			var posted []Envelope
			fail := true
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "capabilities") {
					if v == 1 {
						w.WriteHeader(404)
						printJSON(w, map[string]any{"error": "not_found"})
					} else {
						printJSON(w, activityCaps(pair))
					}
					return
				}
				if r.Method == "GET" {
					w.WriteHeader(404)
					printJSON(w, map[string]any{"error": "not_found"})
					return
				}
				var e Envelope
				if json.NewDecoder(r.Body).Decode(&e) != nil {
					t.Error("bad envelope")
				}
				mu.Lock()
				posted = append(posted, e)
				failed := fail
				mu.Unlock()
				if failed {
					w.WriteHeader(503)
					printJSON(w, map[string]any{"error": "try_later"})
					return
				}
				printJSON(w, map[string]any{"event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
			}))
			defer server.Close()
			pair.APIURL = server.URL
			if err := s.savePair(ctx, pair); err != nil {
				t.Fatal(err)
			}
			input := func(run, summary string) []byte {
				raw, _ := json.Marshal(map[string]any{"adapter_revision": 2, "runtime_version": "1.3.0", "event": "agent_settled", "session_id": "session", "run_id": run, "event_id": run, "instance_id": "instance", "aborted": false, "summary": summary, "completion_body_base64": base64.StdEncoding.EncodeToString([]byte(summary + "\n\n  🐱"))})
				return raw
			}
			api := API{server.Client()}
			// Exercise the native observer and explicit dispatch with its network
			// command budget. The notify wrapper has a separate short Hook deadline;
			// it promises durable reception, not HTTP completion in that interval.
			if _, err := observe(ctx, s, api, "pi", input("first", "本次回复摘要")); err == nil {
				t.Fatal("network failure hidden")
			} else {
				var failure *APIError
				if !errors.As(err, &failure) || failure.Status != 503 {
					t.Fatalf("expected simulated HTTP failure, got %v", err)
				}
			}
			records, err := outboxRecords(ctx, s, "", false)
			if err != nil || len(records) != 1 {
				t.Fatal("missing durable delivery", err)
			}
			first := *records[0].Envelope
			if first.V != v || summaryPlaintext(t, pair, first)["summary"] != "本次回复摘要" {
				t.Fatal("negotiation lost message")
			}
			mu.Lock()
			fail = false
			mu.Unlock()
			if _, err = updateDelivery(ctx, s, pair.PairID, first.EventID, func(d *delivery) error { d.Next = time.Now().Unix() - 1; return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err = observe(ctx, Store{s.Dir}, api, "pi", input("first", "重复回调不能替换")); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			copies := append([]Envelope(nil), posted...)
			mu.Unlock()
			if len(copies) != 2 || copies[0] != copies[1] {
				t.Fatal("recovery changed ciphertext or double-sent", len(copies))
			}
			checkTestBody(t, Store{s.Dir}, pair, first, "本次回复摘要\n\n  🐱")
			if _, err = observe(ctx, s, api, "pi", input("second", "")); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			last := posted[len(posted)-1]
			mu.Unlock()
			if last.EventID == first.EventID || summaryPlaintext(t, pair, last)["summary"] != nil {
				t.Fatal("new round reused old summary or identity")
			}
		})
	}
}

func TestMessageSummaryTextBoundaries(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\x1b[31m完成\x1b[0m\n第一段。\n\n第二段", "完成 第一段。"},
		{"\x1b]8;;https://example.com\x07已完成\x1b]8;;\x07\u202e\x00", "已完成"},
		{"```go\nsecret()\n```\n\n已更新配置。", "已更新配置。"},
		{"```go\nsecret()\n```", ""},
		{"{\"token\":\"value\"}", ""},
		{"![image](https://example.com/a.png)", ""},
		{`{
 "token":"private"
}`, ""},
		{"<think>\nprivate reasoning\n</think>\n\nPublic result.", "Public result."},
		{"  \n\t", ""},
	} {
		if got := cleanMessageSummary(tc.in); got != tc.want {
			t.Errorf("summary = %q; want %q", got, tc.want)
		}
	}
	for _, text := range []string{strings.Repeat("中", 300), strings.Repeat("👨‍👩‍👧‍👦", 50), strings.Repeat("e\u0301", 100), strings.Repeat("<&\"\\", 100)} {
		got := cleanMessageSummary(text)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 120 || len(got) > 512 || got == "" {
			t.Fatal("summary limit")
		}
		if cleanMessageSummary(got) != got {
			t.Fatal("summary not idempotent")
		}
	}
	if got := cleanMessageSummary(strings.Repeat("a", 119) + "e\u0301"); got != strings.Repeat("a", 119) {
		t.Fatal("split combining cluster", got)
	}
}

func TestMessageSummaryFrozenAcrossDurableRecovery(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	id, _ := s.installation()
	pair := testPair(id, "https://example.com")
	_ = s.savePair(ctx, pair)
	l := fixtureLocator()
	l.InstallationID = id
	first, err := enqueueDeliverySummary(ctx, s, pair, "turn_completed", "same", l, "codex", "第一条摘要")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := enqueueDeliverySummary(ctx, Store{s.Dir}, pair, "turn_completed", "same", l, "codex", "替换内容")
	if err != nil || *duplicate.Envelope != *first.Envelope {
		t.Fatal("legacy envelope changed", err)
	}
	if summaryPlaintext(t, pair, *first.Envelope)["summary"] != "第一条摘要" {
		t.Fatal("missing legacy summary")
	}
	a := Activity{Agent: "pi", Session: "s", Instance: "i", Run: "r", Occurrence: "done", State: "turn_completed", Reminder: "turn_completed", Summary: "本轮摘要"}
	if _, _, _, err = applyActivity(ctx, s, l, a); err != nil {
		t.Fatal(err)
	}
	a.Summary = "重复回调的新文字"
	if _, _, changed, e := applyActivity(ctx, s, l, a); e != nil || changed {
		t.Fatal("duplicate changed", e)
	}
	db, err := loadRuns(Store{s.Dir})
	if err != nil {
		t.Fatal(err)
	}
	for k, intent := range db.Intents {
		if intent.Payload.Summary != "本轮摘要" {
			t.Fatal("intent changed")
		}
		encoded, e := encodeRunIntent(ctx, s, k, intent, RunCapabilities{CapabilityGeneration: 1}, pair)
		if e != nil {
			t.Fatal(e)
		}
		intent.Payload.Summary = "late change"
		replay, e := encodeRunIntent(ctx, s, k, intent, RunCapabilities{CapabilityGeneration: 1}, pair)
		if e != nil || *replay.Envelope != *encoded.Envelope {
			t.Fatal("v2 envelope changed", e)
		}
		if summaryPlaintext(t, pair, *encoded.Envelope)["summary"] != "本轮摘要" {
			t.Fatal("missing v2 summary")
		}
	}
}

func TestMessageSummaryBudgetRetainsCoreIdentity(t *testing.T) {
	key := make([]byte, 32)
	l := fixtureLocator()
	l.Namespace = strings.Repeat("/", 512)
	l.ServerIdentity = strings.Repeat("s", 200)
	e := Envelope{V: 1, PairID: strings.Repeat("p", 128), EventID: strings.Repeat("e", 128), Kind: "turn_completed", ExpiresAt: 2000000000}
	sealed, err := sealAgentSummary(key, e, l, nil, "codex", strings.Repeat("<&", 60))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sealed.Ciphertext)
	if len(raw) > maxCiphertext || notificationPayloadSize(sealed) > 4096 {
		t.Fatal("v1 budget exceeded")
	}
	opened, err := authenticateEnvelope(key, sealed)
	if err != nil || opened != l {
		t.Fatal("identity damaged", err)
	}
	p := RunPayload{V: 2, Locator: fixtureLocator(), Agent: "codex", SessionID: strings.Repeat("s", 256), RunID: strings.Repeat("r", 256), StreamID: strings.Repeat("z", 128), Revision: 1, State: "turn_completed", Summary: strings.Repeat("<&", 60), Tools: []RunTool{}}
	for i := 0; i < 32; i++ {
		p.Tools = append(p.Tools, RunTool{strings.Repeat("t", 128), "shell", "returned"})
	}
	e.V = 2
	e.StreamID = p.StreamID
	e.Revision = 1
	e.CapabilityGeneration = 1
	sealed, err = sealRun(key, e, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = base64.StdEncoding.DecodeString(sealed.Ciphertext)
	if len(raw) > maxV2Ciphertext || notificationPayloadSize(sealed) > 4096 {
		t.Fatal("v2 budget exceeded")
	}
	// Required high-escaping identity alone may exceed the wire budget; it must
	// remain a real capacity failure after every optional display field is gone.
	p.Locator.Namespace = strings.Repeat("<&", 256)
	p.Tools = nil
	if _, err = sealRun(key, e, p, nil); err == nil {
		t.Fatal("oversize core silently accepted")
	}
	stress := e
	stress.Ciphertext = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{255}, 2400))
	stress.Nonce = base64.StdEncoding.EncodeToString(make([]byte, 12))
	if notificationPayloadSize(stress) <= 4096 {
		t.Fatal("slash reserialization was not budgeted")
	}

}
