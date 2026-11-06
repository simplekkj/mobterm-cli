package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// These tests inspect local admission and explicitly invoke sending where needed.
// Keep the asynchronous dispatcher separate from the synchronous HTTP count.
func holdApprovalDispatch(t *testing.T, s Store) {
	t.Helper()
	if err := ensureDir(s.Dir, true); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockFile(context.Background(), filepath.Join(s.Dir, ".activity-pending-lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
}

func fixtureApproval(t *testing.T, s Store, l Locator, p Pair, a Activity) (ApprovalRequest, Activity) {
	t.Helper()
	birth, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := processIdentity(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	r := ApprovalRequest{V: 3, Session: a.Session, Run: a.Run, Instance: a.Instance, Agent: a.Agent, RequestID: randomID(), OperationJSON: "{}", Digest: digest("{}"), Locator: l, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 120, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parent, State: "pending", Pairs: map[string]int64{p.PairID: 3}}
	if err := s.writeJSON("approval-"+r.RequestID+".json", r); err != nil {
		t.Fatal(err)
	}
	a.State, a.Reminder = "waiting_approval", "needs_input"
	a.RequestID = r.RequestID
	a.Approval = &ApprovalHint{r.RequestID, r.Digest, r.ExpiresAt}
	a.Occurrence = digest("approval\x00" + r.RequestID)
	if _, _, _, err := applyActivity(context.Background(), s, l, a); err != nil {
		t.Fatal(err)
	}
	return r, a
}

func TestCodexPassiveApprovalDoesNotNotify(t *testing.T) {
	for _, mode := range []string{"disabled", "unsupported", "no_pair"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			holdApprovalDispatch(t, s)
			id, _ := s.installation()
			l := fixtureLocator()
			l.InstallationID = id
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			if mode != "no_pair" {
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "disabled" {
				if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
					t.Fatal(err)
				}
			}
			a, input, err := parseActivity(context.Background(), s, "codex", []byte(`{"hook_event_name":"PermissionRequest","session_id":"session","turn_id":"turn","cwd":"/work/native","tool_name":"Bash","tool_input":{"command":"pwd"}}`))
			if err != nil || a.State != "tool_pending" || a.Reminder != "" {
				t.Fatal("pre-approval observation claims a confirmed wait", err)
			}
			if mode == "unsupported" {
				input.ToolName = "unknown"
			}
			result, err := approvalWait(context.Background(), s, API{server.Client()}, l, a, input)
			if err != nil || result != nil || calls.Load() != 0 {
				t.Fatal("passive approval waited for API or produced a decision", err, calls.Load())
			}
			db, err := loadRuns(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, intent := range db.Intents {
				if intent.Kind == "needs_input" || intent.Payload.Approval != nil || intent.Payload.State == "waiting_approval" {
					t.Fatal("passive observation created a high-priority or actionable intent")
				}
			}
			files, _ := filepath.Glob(filepath.Join(s.Dir, "approval-*.json"))
			for _, file := range files {
				if stringsHasApproval(filepath.Base(file)) {
					t.Fatal("passive observation created a waiter")
				}
			}
		})
	}
}

func TestApprovalResolutionPreservesOtherRequestsAndRejectsLateWaiting(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	l := fixtureLocator()
	l.InstallationID = id
	p := testPair(id, "https://fixture.invalid")
	ctx := context.Background()
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	a := Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"}
	first, late := fixtureApproval(t, s, l, p, a)
	second, _ := fixtureApproval(t, s, l, p, a)
	question := a
	question.State, question.RequestID, question.Occurrence = "waiting_input", "question", "question"
	if _, _, _, err := applyActivity(ctx, s, l, question); err != nil {
		t.Fatal(err)
	}
	if err := finishApproval(s, first.RequestID, "consumed"); err != nil {
		t.Fatal(err)
	}
	late.Occurrence = "late-observer"
	if _, _, changed, err := applyActivity(ctx, s, l, late); err != nil || changed {
		t.Fatal("late waiting event revived consumed approval", err)
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range db.Runs {
		if len(run.Requests) != 2 || run.Requests[first.RequestID].State != "" || run.Requests[second.RequestID].Approval == nil || run.Requests["question"].State != "waiting_input" || run.State != "waiting_approval" {
			t.Fatal("settling one approval removed or displaced another request")
		}
	}
	if err := finishApproval(s, second.RequestID, "released"); err != nil {
		t.Fatal(err)
	}
	db, _ = loadRuns(s)
	for _, run := range db.Runs {
		if run.State != "waiting_input" || run.Approval != nil || len(run.Requests) != 1 {
			t.Fatal("remaining question state not restored")
		}
	}
}

func TestResolvedApprovalDropsIntentAndEncodedOutbox(t *testing.T) {
	for _, encoded := range []bool{false, true} {
		t.Run(map[bool]string{false: "intent", true: "outbox"}[encoded], func(t *testing.T) {
			s := testStore(t)
			holdApprovalDispatch(t, s)
			id, _ := s.installation()
			l := fixtureLocator()
			l.InstallationID = id
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			ctx := context.Background()
			if err := s.savePair(ctx, p); err != nil {
				t.Fatal(err)
			}
			if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
				t.Fatal(err)
			}
			r, _ := fixtureApproval(t, s, l, p, Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"})
			var d delivery
			if encoded {
				db, _ := loadRuns(s)
				for key, intent := range db.Intents {
					var caps RunCapabilities
					raw, _ := json.Marshal(activityCaps(p))
					_ = json.Unmarshal(raw, &caps)
					intent, err := encodeRunIntent(ctx, s, key, intent, caps, p)
					if err != nil {
						t.Fatal(err)
					}
					d, err = enqueueRunReminder(ctx, s, intent)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := finishApproval(s, r.RequestID, "released"); err != nil {
				t.Fatal(err)
			}
			if encoded {
				result, err := deliverAttempt(ctx, s, API{server.Client()}, d)
				if err != nil || result["state"] != "disabled" {
					t.Fatal("resolved outbox reminder still pending", err)
				}
			} else if err := flushRunIntents(ctx, s, API{server.Client()}, false); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 0 {
				t.Fatal("resolved approval sent HTTP", calls.Load())
			}
		})
	}
}

func TestApprovalConcurrentMissingNativeIDRemainIndependent(t *testing.T) {
	claudeNotifyEnvironment(t)
	s := testStore(t)
	holdApprovalDispatch(t, s)
	id, _ := s.installation()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		printJSON(w, activityCaps(p))
	}))
	defer server.Close()
	p.APIURL = server.URL
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
		t.Fatal(err)
	}
	input := activityInput{Cwd: "/work", HookEventName: "PermissionRequest", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"pwd"}`)}
	a := Activity{Agent: "codex", Session: "session", Instance: "session", Run: "run", State: "tool_pending"}
	// Simulate waiters that died before settling their run. Registration's
	// expiry/retention sweep must settle both before admitting the new requests.
	for _, age := range []int64{1, 3601} {
		old, _ := fixtureApproval(t, s, l, p, a)
		old.ExpiresAt = time.Now().Unix() - age
		if err := s.writeJSON("approval-"+old.RequestID+".json", old); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := approvalWait(ctx, s, API{server.Client()}, l, a, input); done <- err }()
	}
	deadline := time.Now().Add(5 * time.Second)
	var requests map[string]PendingRequest
	registered := false
	for time.Now().Before(deadline) {
		db, err := loadRuns(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range db.Runs {
			requests = run.Requests
		}
		registered = len(requests) == 2
		for id := range requests {
			r, err := approvalRead(s, id)
			registered = registered && err == nil && r.ExpiresAt > time.Now().Unix()
		}
		if registered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !registered {
		t.Fatal("identical commands without native IDs were merged or expired waits survived registration")
	}
	for requestID, pending := range requests {
		r, err := approvalRead(s, requestID)
		if err != nil || r.NativeIdentity != "" || pending.Approval == nil || pending.Approval.RequestID != requestID || r.Session != a.Session || r.Run != a.Run || r.Locator != l {
			t.Fatal("concurrent approval identity or attribution lost", err)
		}
	}
	// Cancelling this invocation ends both independent waiters, each by its ID.
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled waiter failed to settle")
		}
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range db.Runs {
		if len(run.Requests) != 0 || run.Approval != nil || run.State != "running" {
			t.Fatal("cancelled requests retained waiting state")
		}
	}
}

func TestApprovalRecoverySettlesOnlyProvenRequest(t *testing.T) {
	for _, reason := range []string{"expired", "parent_exit", "disabled", "released"} {
		t.Run(reason, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			l := fixtureLocator()
			l.InstallationID = id
			p := testPair(id, "https://fixture.invalid")
			if err := s.savePair(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
				t.Fatal(err)
			}
			r, a := fixtureApproval(t, s, l, p, Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"})
			switch reason {
			case "expired":
				r.ExpiresAt = time.Now().Unix() - 1
			case "parent_exit":
				r.ParentBirth = "reused-parent-pid"
			case "disabled":
				if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{}); err != nil {
					t.Fatal(err)
				}
			case "released":
				r.State, r.OperationJSON = "released", ""
			}
			if err := s.writeJSON("approval-"+r.RequestID+".json", r); err != nil {
				t.Fatal(err)
			}
			if err := reconcileApprovalHint(s, a.Approval); err != nil {
				t.Fatal(err)
			}
			db, _ := loadRuns(s)
			for _, run := range db.Runs {
				if run.Approval != nil || len(run.Requests) != 0 || run.State != "running" {
					t.Fatal("recovered resolved request still waiting")
				}
			}
		})
	}
}

func TestApprovalResolvedDuringCapabilityLookupCannotSend(t *testing.T) {
	s := testStore(t)
	holdApprovalDispatch(t, s)
	id, _ := s.installation()
	l := fixtureLocator()
	l.InstallationID = id
	p := testPair(id, "")
	entered, release := make(chan struct{}), make(chan struct{})
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			close(entered)
			<-release
			printJSON(w, activityCaps(p))
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	p.APIURL = server.URL
	ctx := context.Background()
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
		t.Fatal(err)
	}
	r, _ := fixtureApproval(t, s, l, p, Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"})
	done := make(chan error, 1)
	go func() { done <- flushRunIntents(ctx, s, API{server.Client()}, false) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("sender did not reach capability check")
	}
	if err := finishApproval(s, r.RequestID, "released"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil || posts.Load() != 0 {
		t.Fatal("resolved lookup race produced a reminder", err, posts.Load())
	}
}

func TestApprovalRevocationPreservesOtherPairAuthority(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	l := fixtureLocator()
	l.InstallationID = id
	p := testPair(id, "https://fixture.invalid")
	ctx := context.Background()
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, _ := fixtureApproval(t, s, l, p, Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"})
	second := randomID()
	r.Pairs[second] = 3
	if err := s.writeJSON("approval-"+r.RequestID+".json", r); err != nil {
		t.Fatal(err)
	}
	if err := revokeApprovalPair(s, r.RequestID, p.PairID); err != nil {
		t.Fatal(err)
	}
	r, err := approvalRead(s, r.RequestID)
	if err != nil || r.State != "pending" || len(r.Pairs) != 1 || r.Pairs[second] != 3 {
		t.Fatal("one revoked pair removed another phone's authority", err)
	}
	db, _ := loadRuns(s)
	for _, run := range db.Runs {
		if run.State != "waiting_approval" || len(run.Requests) != 1 {
			t.Fatal("request resolved while another phone can answer")
		}
	}
	if err := revokeApprovalPair(s, r.RequestID, second); err != nil {
		t.Fatal(err)
	}
	db, _ = loadRuns(s)
	for _, run := range db.Runs {
		if run.State != "running" || len(run.Requests) != 0 {
			t.Fatal("last revoked pair did not resolve the request")
		}
	}
}

func TestTerminalRunCannotKeepActionableApproval(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	l := fixtureLocator()
	l.InstallationID = id
	p := testPair(id, "https://fixture.invalid")
	ctx := context.Background()
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 3}}); err != nil {
		t.Fatal(err)
	}
	a := Activity{Agent: "codex", Session: "session", Run: "turn", Instance: "session"}
	r, waiting := fixtureApproval(t, s, l, p, a)
	a.State, a.Reminder, a.Occurrence = "interrupted", "interrupted", "interrupt"
	if _, _, _, err := applyActivity(ctx, s, l, a); err != nil {
		t.Fatal(err)
	}
	if approvalNotificationValid(s, waiting.Approval, p.PairID) {
		t.Fatal("interrupted run kept an actionable approval")
	}
	if err := reconcileApprovalHint(s, waiting.Approval); err != nil {
		t.Fatal(err)
	}
	r, err := approvalRead(s, r.RequestID)
	if err != nil || r.State != "cancelled" {
		t.Fatal("terminal approval was not cancelled", err)
	}
	db, _ := loadRuns(s)
	for _, run := range db.Runs {
		if run.State != "interrupted" || len(run.Requests) != 0 || run.Revision != 2 {
			t.Fatal("approval cleanup revived or changed terminal run")
		}
	}
}
