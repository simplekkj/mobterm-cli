package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Real normalization, store, encrypted notification and Hook dispatch paths;
// only the local host probe and HTTP service are controlled fixtures.
func reviewInteractionSetup(t *testing.T) (Store, API, Pair, Locator, chan RunPayload) {
	t.Helper()
	claudeNotifyEnvironment(t)
	t.Setenv("FIXTURE_CLAUDE_VERSION", "2.1.317 (Claude Code)")
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	l, err := captureTarget(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	notices := make(chan RunPayload, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "interaction-capabilities") {
			printJSON(w, interactionTestGrant(p))
			return
		}
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			printJSON(w, activityCaps(p))
			return
		}
		var envelope Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if envelope.Kind == "needs_input" {
			key, _ := decode64(p.EncryptionKey, 32)
			block, _ := aes.NewCipher(key)
			g, _ := cipher.NewGCM(block)
			nonce, _ := decode64(envelope.Nonce, 12)
			ciphertext, _ := base64.StdEncoding.DecodeString(envelope.Ciphertext)
			raw, err := g.Open(nil, nonce, ciphertext, envelope.aad())
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var payload RunPayload
			if err = json.Unmarshal(raw, &payload); err != nil {
				t.Error(err)
			}
			notices <- payload
		}
		printJSON(w, map[string]any{"v": 2, "event_id": envelope.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	t.Cleanup(server.Close)
	p.APIURL = server.URL
	if err = s.savePair(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err = s.writeJSON("interaction-policy-v1.json", InteractionPolicy{p.PairID: {"opencode": {"form": 3}, "claude": {"elicitation": 3}}}); err != nil {
		t.Fatal(err)
	}
	return s, API{server.Client()}, p, l, notices
}
func reviewNotice(t *testing.T, notices chan RunPayload, results ...chan error) RunPayload {
	var done chan error
	if len(results) > 0 {
		done = results[0]
	}
	t.Helper()
	select {
	case p := <-notices:
		return p
	case err := <-done:
		t.Fatalf("native waiter ended before notification: %v", err)
		return RunPayload{}
	case <-time.After(5 * time.Second):
		t.Fatal("missing needs_input")
		return RunPayload{}
	}
}
func reviewDecision(t *testing.T, s Store, api API, pair Pair, notice RunPayload, action, response string) {
	t.Helper()
	if notice.Interaction == nil {
		t.Fatal("missing interaction hint")
	}
	r, err := interactionRead(s, notice.Interaction.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	f := interactionTestFrame(pair, r)
	f.Action = action
	f.ResponseJSON = response
	f.ResponseDigest = digest(response)
	key, _ := decode64(pair.EncryptionKey, 32)
	f.Proof, _ = interactionMAC(key, f)
	if _, err = interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); err != nil {
		t.Fatal(err)
	}
}
func TestInteractionOpenCodeNormalizedOverflowPublishesOnlyReadonlyReminder(t *testing.T) {
	s, api, pair, _, notices := reviewInteractionSetup(t)
	form := map[string]any{"id": "frm_large", "sessionID": "session", "state": map[string]any{"status": "pending"}, "fields": []any{map[string]any{"key": "q", "type": "string", "title": strings.Repeat("界", 4500)}}}
	native, _ := json.Marshal(form)
	spec, err := openCodeFormSpec(native, "session")
	if err != nil {
		t.Fatal(err)
	}
	normalized, _ := json.Marshal(spec)
	raw, _ := json.Marshal(map[string]any{"adapter_revision": 2, "runtime_version": "2.2.5", "event": "form.created", "session_id": "session", "run_id": "run", "event_id": "event-large", "instance_id": "runtime", "request_id": "frm_large", "native_form": form})
	if len(raw) >= interactionRequestLimit || len(normalized) <= interactionRequestLimit {
		t.Fatalf("bad budget fixture: raw=%d spec=%d", len(raw), len(normalized))
	}
	result, err := interactionObserve(context.Background(), s, api, "opencode", raw)
	if err != nil || result != nil {
		t.Fatalf("overflow must return no native decision: %v %v", result, err)
	}
	notice := reviewNotice(t, notices)
	if notice.Interaction == nil || notice.Interaction.UnavailableReason != "complete_context_exceeds_limit" {
		t.Fatalf("missing reason: %+v", notice.Interaction)
	}
	r, err := interactionRead(s, notice.Interaction.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "unavailable" || r.Frame != nil || r.PID != 0 || r.OwnerToken != "" || len(r.Pairs) != 1 {
		t.Fatal("overflow gained native authority")
	}
	var shown InteractionSpec
	if err = json.Unmarshal([]byte(r.RequestJSON), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.SupportedActions) != 0 || len(shown.Fields) != 0 || len(r.RequestJSON) > interactionRequestLimit {
		t.Fatal("unsafe partial actionable form")
	}
	if len(notices) != 0 {
		t.Fatal("duplicate overflow reminder")
	}
	f := interactionTestFrame(pair, r)
	if _, err = interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); err == nil {
		t.Fatal("read-only overflow authorized a response")
	}
	saved, _ := interactionRead(s, r.RequestID)
	if saved.State != "unavailable" || saved.Frame != nil {
		t.Fatal("overflow acquired a decision")
	}

	if form["state"].(map[string]any)["status"] != "pending" {
		t.Fatal("original form changed")
	}
}
func TestInteractionClaudeElicitationPreservesPromptCyclesAndErrors(t *testing.T) {
	s, api, pair, l, notices := reviewInteractionSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, waitSenders := withInteractionSenders(ctx)
	t.Cleanup(waitSenders)
	var previous string
	for cycle := 0; cycle < 2; cycle++ {
		prompt := []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"session"}`)
		a, _, err := parseActivity(ctx, s, "claude", prompt)
		if err != nil {
			t.Fatal(err)
		}
		if err = publishActivity(ctx, s, api, l, a); err != nil {
			t.Fatal(err)
		}
		if a.Run == "" || a.Run == previous {
			t.Fatal("new prompt cycle not allocated")
		}
		for request := 0; request < 2; request++ {
			raw := []byte(fmt.Sprintf(`{"hook_event_name":"Elicitation","session_id":"session","elicitation_id":"native-%d-%d","requested_schema":{"type":"object","properties":{"flag":{"type":"boolean"}}}}`, cycle, request))
			done := make(chan error, 1)
			t.Cleanup(func() {
				cancel()
				for range done {
				}
			})
			go func() {
				defer close(done)
				value, e := observe(ctx, s, api, "claude", raw)
				if e == nil {
					e = writeInteractionOutput(io.Discard, s, value)
				}
				done <- e
			}()
			notice := reviewNotice(t, notices, done)
			if notice.RunID != a.Run {
				t.Fatalf("wrong cycle: %s != %s", notice.RunID, a.Run)
			}
			r, err := interactionRead(s, notice.Interaction.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var spec InteractionSpec
			if err = json.Unmarshal([]byte(r.RequestJSON), &spec); err != nil {
				t.Fatal(err)
			}
			if spec.RunID != a.Run || spec.NativeID != fmt.Sprintf("native-%d-%d", cycle, request) {
				t.Fatal("request identity lost")
			}
			reviewDecision(t, s, api, pair, notice, "accept", `{"answers":{"flag":false}}`)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			r, _ = interactionRead(s, r.RequestID)
			if r.State != "response_written" {
				t.Fatal("native response not written")
			}
		}
		terminal := Activity{Agent: "claude", Session: "session", Instance: "session", Run: a.Run, Occurrence: "done-" + a.Run, State: "turn_completed"}
		if _, _, _, err = applyActivity(ctx, s, l, terminal); err != nil {
			t.Fatal(err)
		}
		previous = a.Run
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range db.Runs {
		if r.State != "turn_completed" {
			t.Fatal("old run reopened")
		}
	}
	if len(notices) != 0 {
		t.Fatal("duplicate elicitation")
	}
	// Without a prompt cycle, the parser still supplies a nonempty occurrence.
	raw := []byte(`{"hook_event_name":"Elicitation","session_id":"no-cycle","elicitation_id":"native","requested_schema":{"type":"object","properties":{"flag":{"type":"boolean"}}}}`)
	a, _, err := parseActivity(ctx, s, "claude", raw)
	if err != nil || a.Occurrence == "" {
		t.Fatal("missing occurrence", err)
	}
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		for range done {
		}
	})
	go func() { defer close(done); _, e := observe(ctx, s, api, "claude", raw); done <- e }()
	notice := reviewNotice(t, notices, done)
	reviewDecision(t, s, api, pair, notice, "accept", `{"answers":{"flag":false}}`)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	db, _ = loadRuns(s)
	found := false
	for _, r := range db.Runs {
		if r.Run == notice.RunID {
			found = r.FirstOccurrence != ""
		}
	}
	if !found {
		t.Fatal("production binding dropped occurrence")
	}
	if err = os.WriteFile(s.Dir+"/claude-cycles.json", []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = observe(ctx, s, api, "claude", raw); err == nil {
		t.Fatal("parser error discarded")
	}
	if len(notices) != 0 {
		t.Fatal("invalid parse emitted reminder")
	}
}

func TestInteractionOpenCodeAdjacentNormalizedBudgetAndSmallForm(t *testing.T) {
	s, api, pair, _, notices := reviewInteractionSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, waitSenders := withInteractionSenders(ctx)
	t.Cleanup(waitSenders)
	run := func(index, count int, overflow bool) int {
		form := map[string]any{"id": fmt.Sprintf("frm_%d", index), "sessionID": "session", "state": map[string]any{"status": "pending"}, "fields": []any{map[string]any{"key": "q", "type": "string", "title": strings.Repeat("x", count)}}}
		raw, _ := json.Marshal(map[string]any{"adapter_revision": 2, "runtime_version": "2.2.5", "event": "form.created", "session_id": "session", "run_id": "run", "event_id": fmt.Sprintf("event-%d", index), "instance_id": "runtime", "request_id": fmt.Sprintf("frm_%d", index), "native_form": form})
		if len(raw) >= interactionRequestLimit {
			t.Fatal("fixture exceeds raw budget")
		}
		done := make(chan error, 1)
		t.Cleanup(func() {
			cancel()
			for range done {
			}
		})
		go func() {
			defer close(done)
			v, e := interactionObserve(ctx, s, api, "opencode", raw)
			if e == nil && overflow && v != nil {
				e = fmt.Errorf("overflow returned native decision")
			}
			done <- e
		}()
		notice := reviewNotice(t, notices, done)
		r, e := interactionRead(s, notice.Interaction.RequestID)
		if e != nil {
			t.Fatal(e)
		}
		if overflow {
			if r.State != "unavailable" {
				t.Fatal("overflow was actionable")
			}
		} else {
			if r.State != "pending" || r.UnavailableReason != "" {
				t.Fatal("in-budget request rejected")
			}
			reviewDecision(t, s, api, pair, notice, "answer", `{"answers":{"q":"value"}}`)
		}
		if e = <-done; e != nil {
			t.Fatal(e)
		}
		if len(notices) != 0 {
			t.Fatal("duplicate reminder")
		}
		return len(r.RequestJSON)
	}
	small := run(0, 100, false)
	boundaryCount := 100 + (interactionRequestLimit-small)/2
	boundary := run(1, boundaryCount, false)
	if boundary < interactionRequestLimit-1 || boundary > interactionRequestLimit {
		t.Fatalf("not adjacent: %d", boundary)
	}
	run(2, boundaryCount+1, true)
}

func TestInteractionConfirmedReceiptRedactsOtherPair(t *testing.T) {
	s, api, pairs, r := interactionTestSetup(t)
	f := interactionTestFrame(pairs[0], r)
	r.Frame = &f
	r.State = "native_confirmed"
	r.Evidence = "question_answer_match"
	r.RequestJSON = ""
	r.Frame.ResponseJSON = ""
	if err := s.writeJSON("interaction-"+r.RequestID+".json", r); err != nil {
		t.Fatal(err)
	}
	for i, p := range pairs {
		result, err := interactionCommand(context.Background(), s, api, "status", bytes.NewReader(interactionFrameBytes(InteractionFrame{V: 1, PairID: p.PairID, RequestID: r.RequestID})))
		if err != nil {
			t.Fatal(err)
		}
		receipt := result.(map[string]any)
		if receipt["state"] != "native_confirmed" || receipt["response_json"] != nil || receipt["request_json"] != nil {
			t.Fatal("incorrect global status/privacy")
		}
		for _, key := range []string{"decision_id", "action", "response_digest", "evidence"} {
			if (receipt[key] != nil) != (i == 0) {
				t.Fatal("receipt attribution/privacy mismatch", key)
			}
		}
	}
}
