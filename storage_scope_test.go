package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPhysicalSessionHeadsPreserveCopiesAndFrozenHistory(t *testing.T) {
	ctx := context.Background()
	fx := newReplyFixture(t)
	p := fx.pairs[0]
	legacy := replySendFrame(fx.frames[0], fx.record, "original decision")
	status, err := replyRequest(ctx, fx.s, fx.api, p, "send", legacy)
	if err != nil || status.State != "decision_recorded" {
		t.Fatalf("freeze legacy decision: %s %v", status.State, err)
	}

	original := filepath.Join(t.TempDir(), "session.jsonl")
	copied := filepath.Join(t.TempDir(), "session.jsonl")
	alias := filepath.Join(t.TempDir(), "restored.jsonl")
	for _, path := range []string{original, copied} {
		if err := os.WriteFile(path, []byte("identical native data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(original, alias); err != nil {
		t.Fatal(err)
	}
	type result struct {
		record replyRecord
		frame  bodyIdentity
		scope  string
	}
	notice := func(storage, run, event string, locator Locator) result {
		t.Helper()
		input := replyWaitInput{"1.5.2", fx.record.Session, run, event, "same_activation", time.Now().Unix() + 120, storage, "assistant-entry"}
		r, err := replyRegistration(fx.s, "pi", input, locator, true)
		if err != nil {
			t.Fatal(err)
		}
		payload := activityInput{AdapterRevision: 2, RuntimeVersion: input.Version, Event: "agent_settled", SessionID: input.Session,
			RunID: input.Run, EventID: input.Event, InstanceID: input.Instance, NativeStorage: storage}
		finished := false
		payload.Aborted = &finished
		for _, kind := range []string{"agent_start", "agent_settled"} {
			payload.Event = kind
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			a, _, err := parseActivity(ctx, fx.s, "pi", raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, changed, e := applyActivity(ctx, fx.s, locator, a); e != nil || !changed {
				t.Fatal("capture", kind, changed, e)
			}
		}
		ik := digest(p.PairID + "\x00" + r.Cursor)
		db, err := loadRuns(fx.s)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := encodeRunIntent(ctx, fx.s, ik, db.Intents[ik], RunCapabilities{CapabilityGeneration: 1}, p)
		if err != nil {
			t.Fatal(err)
		}
		wire := *intent.Envelope
		source, ok := summaryPlaintext(t, p, wire)["source"].(map[string]any)
		if !ok || source["retires"] != audienceSession(p, fx.record.LogicalSession) || source["session"] != audienceSession(p, r.LogicalSession) {
			t.Fatal("authenticated retirement must name the original config-only digest of this Pair")
		}
		encrypted, err := base64.StdEncoding.DecodeString(wire.Ciphertext)
		if err != nil || len(encrypted) > maxV2Ciphertext || notificationPayloadSize(wire) > 4096 {
			t.Fatal("source migration exceeds transport bounds", err)
		}
		t.Logf("business reminder ciphertext %d bytes, conservative serialized APNs %d bytes", len(encrypted), notificationPayloadSize(wire))
		other := fx.pairs[1]
		otherKey := digest(other.PairID + "\x00" + r.Cursor)
		suppressed := db.Intents[otherKey]
		suppressed.Kind = "state"
		snapshot, err := encodeRunIntent(ctx, fx.s, otherKey, suppressed, RunCapabilities{CapabilityGeneration: 1}, other)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := summaryPlaintext(t, other, *snapshot.Envelope)["source"].(map[string]any)["retires"]; present {
			t.Fatal("suppressed reminder retired the other Pair's visible session")
		}
		d, err := enqueueRunReminder(ctx, fx.s, intent)
		if err != nil || d.Reply == nil || d.Reply.SourceID != r.SourceID {
			t.Fatalf("delivery does not match storage-scoped waiter: %v", err)
		}
		return result{r, bodyIdentity{1, p.APIURL, p.InstallationID, p.PairID, d.EventID, d.Reply.Envelope, randomID()}, d.VisibleSession}
	}
	current := func(r result) bool {
		return visibleSourceCurrent(fx.s, r.frame, r.scope) && replyCursorReason(fx.s, r.record) == ""
	}
	// Hold an unscoped source until its actual CLI outbox encode. This models
	// a pre-upgrade intake that had no per-Pair visible head yet.
	delayed := Activity{Agent: "pi", Session: fx.record.Session, Run: "legacy-delayed-run", Instance: "legacy-delayed-activation",
		State: "waiting_input", Reminder: "needs_input", Occurrence: digest("legacy-delayed")}
	if _, _, _, err := applyActivity(ctx, fx.s, fx.l, delayed); err != nil {
		t.Fatal(err)
	}
	delayedKey := digest(p.PairID + "\x00" + delayed.Occurrence)
	db, err := loadRuns(fx.s)
	if err != nil {
		t.Fatal(err)
	}
	held := db.Intents[delayedKey]
	a := notice(original, "same_run", "same_event", fx.l)
	b := notice(copied, "same_run", "same_event", fx.l)
	if a.scope == b.scope || a.record.SourceID == b.record.SourceID || !current(a) || !current(b) {
		t.Fatal("independent native files merged event IDs, reply cursors, or visible heads")
	}
	moved := fx.l
	moved.PaneID = "%19"
	newA := notice(alias, "later_run", "later_event", moved)
	if newA.scope != a.scope || visibleSourceCurrent(fx.s, a.frame, a.scope) || replyCursorReason(fx.s, a.record) == "" || !current(newA) || !current(b) {
		t.Fatal("moving the same storage must supersede only its own older visible and reply heads")
	}
	if visibleSourceCurrent(fx.s, fx.frames[0].bodyIdentity, audienceSession(p, fx.record.LogicalSession)) ||
		!visibleSourceCurrent(fx.s, fx.frames[1].bodyIdentity, audienceSession(fx.pairs[1], fx.record.LogicalSession)) {
		t.Fatal("legacy navigation was not retired at the actual Pair delivery boundary")
	}
	late, err := encodeRunIntent(ctx, fx.s, delayedKey, held, RunCapabilities{CapabilityGeneration: 1}, p)
	if err != nil {
		t.Fatal(err)
	}
	if visibleSourceCurrent(fx.s, bodyIdentity{Event: late.Envelope.EventID, Envelope: envelopeBodyDigest(*late.Envelope)}, held.Payload.Source.Session) || !current(b) {
		t.Fatal("late unscoped encode regained a visible head after storage was established")
	}
	// A late callback from an older adapter can still name its own cursor
	// and request. The copied sessions make that configuration-only claim ambiguous.
	delayed.Run, delayed.Occurrence = "late-unscoped-run", digest("late-unscoped-request")
	delayed.Interaction = &InteractionHint{RequestID: randomID()}
	if _, _, _, err := applyActivity(ctx, fx.s, fx.l, delayed); err != nil {
		t.Fatal(err)
	}
	unscoped := fx.record
	unscoped.Cursor = delayed.Occurrence
	if replyCursorReason(fx.s, unscoped) == "" || nativeRequestCurrent(fx.s, unscoped.Agent, unscoped.Session, delayed.Interaction.RequestID, fx.l, unscoped.LogicalSession) {
		t.Fatal("a late unscoped input reacquired authority after a physical cursor advanced")
	}
	legacy.Text = ""
	got, err := replyRequest(ctx, fx.s, fx.api, p, "status", legacy)
	if err != nil || got.State != "decision_recorded" || got.Decision != legacy.DecisionID {
		t.Fatal("frozen status lost", got.State, err)
	}
	repeated := replyWaitInput{"1.5.2", fx.record.Session, fx.record.Run, "original_event", fx.record.Instance, time.Now().Unix() + 120, original, "entry"}
	if _, err = replyRegistration(fx.s, "pi", repeated, moved, true); err == nil || err.Error() != "reply_already_registered" {
		t.Fatal("an old frozen native occurrence acquired another source", err)
	}
}

func TestClaudePromptCycleSeparatesCopiedTranscriptsBeforeFirstWrite(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first := filepath.Join(t.TempDir(), "session.jsonl")
	second := filepath.Join(t.TempDir(), "session.jsonl")
	event := func(path, kind string) Activity {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"hook_event_name": kind, "session_id": "same-session", "prompt_id": "same-prompt", "transcript_path": path, "notification_type": "idle_prompt"})
		a, _, err := parseActivity(ctx, s, "claude", raw)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	for _, path := range []string{first, second} {
		event(path, "UserPromptSubmit")
		if err := os.WriteFile(path, []byte("same native transcript"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	event(first, "Stop")
	a := event(first, "Notification")
	if a.Reminder != "turn_completed" || event(second, "Notification").Reminder != "" {
		t.Fatal("Stop crossed transcript boundaries")
	}
	event(second, "Stop")
	b := event(second, "Notification")
	if b.Reminder != "turn_completed" || a.Run == b.Run || a.LogicalSession == b.LogicalSession {
		t.Fatal("copied transcripts share an input cycle or a logical head")
	}
}
