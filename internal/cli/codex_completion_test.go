package cli

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexCompletionMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "child", "background", "not-loaded", "different-thread", "later-turn", "in-progress"} {
		t.Run(mode, func(t *testing.T) {
			client := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				var method string
				var params map[string]any
				if json.Unmarshal(req["method"], &method) != nil || json.Unmarshal(req["params"], &params) != nil || params["threadId"] != "thread" {
					t.Error("missing exact thread identity")
				}
				switch method {
				case "thread/read":
					if params["includeTurns"] != false {
						t.Error("unexpected transcript request")
					}
					thread := map[string]any{"id": "thread", "threadSource": "user", "parentThreadId": nil, "originator": "codex-tui", "ephemeral": false, "status": map[string]any{"type": "idle"}}
					switch mode {
					case "child":
						thread["parentThreadId"] = "parent"
					case "background":
						thread["originator"] = "codex-exec"
					case "not-loaded":
						thread["status"] = map[string]string{"type": "notLoaded"}
					case "different-thread":
						thread["id"] = "sibling"
					}
					return map[string]any{"thread": thread}, true
				case "thread/turns/list":
					if params["itemsView"] != "notLoaded" || params["sortDirection"] != "desc" || params["limit"] != float64(1) {
						t.Error("requested unnecessary history")
					}
					turn := map[string]string{"id": "turn", "status": "completed"}
					if mode == "later-turn" {
						turn["id"] = "other"
					}
					if mode == "in-progress" {
						turn["status"] = "inProgress"
					}
					return map[string]any{"data": []any{turn}}, true
				default:
					t.Error("unexpected native method", method)
					return nil, false
				}
			})
			err := client.confirmCompletion(context.Background(), "thread", "turn")
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mode %s: %v", mode, err)
			}
		})
	}
}

func TestUnboundCodexPayload(t *testing.T) {
	l := fixtureLocator()
	p := payloadForRun(StoredRun{Agent: "codex", Session: "thread", Run: "turn", State: "turn_completed", Revision: 1, CodexRuntime: &codexRuntimeSource{Installation: l.InstallationID, Home: "/private/native-home", PeerPID: 123}}, randomID())
	p.Source = &noticeSource{Session: randomID(), RunOrder: 1}
	wire, _ := json.Marshal(p)
	var fields map[string]any
	if json.Unmarshal(wire, &fields) != nil || fields["installation_id"] != l.InstallationID || fields["locator"] != nil || fields["codex_runtime"] != nil {
		t.Fatal("unbound identity leaked or omitted", string(wire))
	}
	e := Envelope{V: 2, PairID: randomID(), EventID: randomID(), Kind: "turn_completed", StreamID: p.StreamID, Revision: 1, CapabilityGeneration: 1}
	key := make([]byte, 32)
	if _, err := sealRun(key, e, p, nil); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RunPayload){func(v *RunPayload) { v.Locator = l }, func(v *RunPayload) { v.Source = nil }, func(v *RunPayload) { v.InstallationID = "" }, func(v *RunPayload) { v.Agent = "pi" }, func(v *RunPayload) { v.Approval = &ApprovalHint{} }} {
		bad := p
		change(&bad)
		if _, err := sealRun(key, e, bad, nil); err == nil {
			t.Fatal("invalid unbound completion accepted")
		}
	}
}

// The barrier is after a real confirmCompletion exchange with the fixture
// runtime and before the first run-store write. A second command has a finite
// lock budget, just like two independently spawned notify handlers.
func TestCodexCompletionFirstRegistrationOrder(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	installation, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	pair := testPair(installation, "https://example.invalid")
	if err := s.savePair(ctx, pair); err != nil {
		t.Fatal(err)
	}
	thread := randomID()
	a := Activity{Agent: "codex", Session: thread, LogicalSession: digest(thread), Instance: digest(thread),
		Run: randomID(), State: "turn_completed", Reminder: "turn_completed", Occurrence: randomID(),
		CodexRoot: true, CodexRuntime: &codexRuntimeSource{Installation: installation}}
	b := a
	b.Run, b.Occurrence = randomID(), randomID()
	var latest atomic.Value
	latest.Store(a.Run)
	serve := func(req map[string]json.RawMessage) (any, bool) {
		switch string(req["method"]) {
		case `"thread/read"`:
			return map[string]any{"thread": map[string]any{"id": thread, "originator": "codex-tui", "threadSource": "user", "ephemeral": false, "status": map[string]string{"type": "idle"}}}, true
		case `"thread/turns/list"`:
			return map[string]any{"data": []any{map[string]any{"id": latest.Load(), "status": "completed"}}}, true
		default:
			return nil, true
		}
	}
	first, second := codexTestRPC(t, serve), codexTestRPC(t, serve)
	confirmed, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- registerCodexCompletion(ctx, s, a, func() error {
			err := first.confirmCompletion(ctx, thread, a.Run)
			close(confirmed)
			<-resume
			return err
		})
	}()
	<-confirmed
	latest.Store(b.Run)
	register := func(ctx context.Context, s Store, client *codexRPC, event Activity) error {
		return registerCodexCompletion(ctx, s, event, func() error {
			return client.confirmCompletion(ctx, thread, event.Run)
		})
	}
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	bErr := register(short, s, second, b)
	expired := short.Err() == context.DeadlineExceeded
	cancel()
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if bErr != nil {
		if !expired {
			t.Fatal(bErr)
		}
		if err := register(ctx, s, second, b); err != nil {
			t.Fatal(err)
		}
	}

	// Deliver B first; the older intent is still eligible for historical
	// display but must never regain the action head on delayed delivery.
	for _, event := range []Activity{b, a} {
		db, err := loadRuns(s)
		if err != nil {
			t.Fatal(err)
		}
		key := digest(pair.PairID + "\x00" + event.Occurrence)
		if _, err := encodeRunIntent(ctx, s, key, db.Intents[key], RunCapabilities{CapabilityGeneration: 1}, pair); err != nil {
			t.Fatal(err)
		}
	}
	reopened := Store{Dir: s.Dir}
	if err := register(ctx, reopened, second, b); err != nil {
		t.Fatal(err)
	}
	if err := register(ctx, reopened, first, a); err == nil || err.Error() != "codex_completion_turn_unconfirmed" {
		t.Fatalf("stale callback after B: %v", err)
	}
	db, err := loadRuns(reopened)
	if err != nil {
		t.Fatal(err)
	}
	head := db.Visible[audienceSession(pair, a.LogicalSession)]
	want := digest(b.Occurrence + "\x00" + pair.PairID)
	if head.Event != want || head.RunOrder != 2 || db.LastSourceOrder != 2 {
		t.Fatalf("latest is B=%t, run order=%d, allocated=%d", head.Event == want, head.RunOrder, db.LastSourceOrder)
	}

	// If B was the very first callback ever stored, a late first observation
	// of A must leave no run, intent or source-order allocation after restart.
	bFirst := testStore(t)
	if err := bFirst.savePair(ctx, pair); err != nil {
		t.Fatal(err)
	}
	if err := register(ctx, bFirst, second, b); err != nil {
		t.Fatal(err)
	}
	if err := register(ctx, Store{Dir: bFirst.Dir}, first, a); err == nil || err.Error() != "codex_completion_turn_unconfirmed" {
		t.Fatalf("first A after B: %v", err)
	}
	db, err = loadRuns(bFirst)
	if err != nil || len(db.Runs) != 1 || len(db.Intents) != 1 || db.LastSourceOrder != 1 {
		t.Fatalf("late first observation changed store: %v", err)
	}
}
