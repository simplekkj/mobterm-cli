package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRichNotifyRecoversDurableOutbox(t *testing.T) {
	for _, failure := range []string{"503", "429", "lost_receipt"} {
		t.Run(failure, func(t *testing.T) {
			claudeNotifyEnvironment(t)
			s := testStore(t)
			id, _ := s.installation()
			p := testPair(id, "")
			var posts []Envelope
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "capabilities") {
					printJSON(w, activityCaps(p))
					return
				}
				if r.Method == "PUT" {
					printJSON(w, map[string]any{"v": 2})
					return
				}
				var e Envelope
				json.NewDecoder(r.Body).Decode(&e)
				mu.Lock()
				posts = append(posts, e)
				count := len(posts)
				mu.Unlock()
				if count == 1 {
					if failure == "lost_receipt" {
						w.Write([]byte(`{"lost":true}`))
						return
					}
					status := 503
					if failure == "429" {
						status = 429
						w.Header().Set("Retry-After", "90")
					}
					w.WriteHeader(status)
					printJSON(w, map[string]any{"error": "try_later"})
					return
				}
				printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
			}))
			defer server.Close()
			p.APIURL = server.URL
			s.savePair(context.Background(), p)
			api := API{server.Client()}
			s.writeJSON("hooks-codex.json", HookRecord{CodexNative: "fixture"})
			input := func(event, run, id string) []byte {
				b, _ := json.Marshal(map[string]any{"type": "agent-turn-complete", "thread-id": "s", "turn-id": run})
				return b
			}
			_, err := notify(context.Background(), s, api, "codex", input("agent_settled", "r1", "done"), "", false)
			if err == nil && failure != "lost_receipt" {
				t.Fatal("failure hidden")
			}
			records, err := outboxRecords(context.Background(), s, "", false)
			if err != nil || len(records) != 1 || records[0].State != "pending" {
				t.Fatal("outbox missing", records, err)
			}
			next := records[0].Next
			if failure == "429" && next < time.Now().Unix()+85 {
				t.Fatal("Retry-After ignored")
			}
			notify(context.Background(), s, api, "codex", input("agent_settled", "r1", "same"), "", false)
			if len(posts) != 1 {
				t.Fatal("retry before due")
			}
			_, err = updateDelivery(context.Background(), s, p.PairID, records[0].EventID, func(d *delivery) error { d.Next = time.Now().Unix() - 1; return nil })
			if err != nil {
				t.Fatal(err)
			}
			_, err = notify(context.Background(), s, api, "codex", input("agent_settled", "r1", "same"), "", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(posts) != 2 || posts[0] != posts[1] || posts[1].V != 2 {
				t.Fatal("identity mutated, downgrade, or recovery missing", len(posts))
			}
			records, _ = outboxRecords(context.Background(), s, "", false)
			if records[0].State != "handed_off" {
				t.Fatal(records)
			}
		})
	}
}

func TestRunStorageOversizeLegacyRecoveryAndAtomicBudget(t *testing.T) {
	s := testStore(t)
	s.installation()
	db := RunStore{Runs: map[string]StoredRun{}, Current: map[string]string{}, Intents: map[string]RunIntent{}}
	for i := 0; i < 128; i++ {
		seen := []string{}
		for j := 0; j < 256; j++ {
			seen = append(seen, digest(fmt.Sprint(i, "/", j)))
		}
		db.Runs[fmt.Sprint(i)] = StoredRun{Seen: seen, State: "running", Updated: time.Now().Unix()}
	}
	raw, _ := json.MarshalIndent(db, "", "  ")
	if len(raw) <= 2*1024*1024 {
		t.Fatal("fixture does not reproduce former read limit")
	}
	if err := atomicWrite(filepath.Join(s.Dir, "runs-v2.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRuns(s)
	if err != nil || len(loaded.Runs) != 128 {
		t.Fatal("legacy responsibilities lost", err)
	}
	if err = saveRuns(s, loaded); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "runs-v2.json"))
	loaded.Runs["oversize"] = StoredRun{Session: strings.Repeat("x", runStoreLimit)}
	if saveRuns(s, loaded) == nil {
		t.Fatal("oversize write accepted")
	}
	after, _ := os.ReadFile(filepath.Join(s.Dir, "runs-v2.json"))
	if string(before) != string(after) {
		t.Fatal("last readable state overwritten")
	}
	if _, err = loadRuns(s); err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(strings.Repeat(" ", runStoreLimit)), []byte(`{"runs":{},"current":{},"intents":{}}`)...)
	if err := atomicWrite(filepath.Join(s.Dir, "runs-v2.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := loadRuns(s)
	if err != nil {
		t.Fatal("bounded legacy recovery", err)
	}
	if err := saveRuns(s, recovered); err != nil {
		t.Fatal("legacy compaction", err)
	}
	if s.writeJSON("bounded.json", strings.Repeat("x", 2*1024*1024)) == nil {
		t.Fatal("generic writer exceeded generic reader")
	}
}

func TestRunRetentionDoesNotLimitDailyBusiness(t *testing.T) {
	s := testStore(t)
	s.installation()
	ctx := context.Background()
	l := fixtureLocator()
	for i := 0; i < 160; i++ {
		a := Activity{Agent: "codex", Session: "s", Instance: "i", Run: fmt.Sprint(i), Occurrence: fmt.Sprint("done", i), State: "turn_completed", Reminder: "turn_completed"}
		if _, _, changed, err := applyActivity(ctx, s, l, a); err != nil || !changed {
			t.Fatal(i, changed, err)
		}
		if _, err := loadRuns(s); err != nil {
			t.Fatal(i, err)
		}
	}
	db, _ := loadRuns(s)
	if len(db.Runs) > 128 {
		t.Fatal("completed retention exceeded", len(db.Runs))
	}
	a := Activity{Agent: "codex", Session: "s", Instance: "i", Run: "0", Occurrence: "late", State: "running"}
	if _, _, changed, err := applyActivity(ctx, s, l, a); err != nil || changed {
		t.Fatal("evicted terminal resurrected", changed, err)
	}
}

func TestCriticalRunOverflowProtectsUnfinishedRuns(t *testing.T) {
	s := testStore(t)
	s.installation()
	ctx := context.Background()
	l := fixtureLocator()
	for i := 0; i < 128; i++ {
		_, _, _, err := applyActivity(ctx, s, l, Activity{Agent: "pi", Session: "s", Instance: "i", Run: fmt.Sprint(i), Occurrence: fmt.Sprint(i), State: "running"})
		if err != nil {
			t.Fatal(err)
		}
	}
	prior, _ := loadRuns(s)
	for key, run := range prior.Runs {
		run.Updated = time.Now().Unix() - 90000
		prior.Runs[key] = run
	}
	if err := saveRuns(s, prior); err != nil {
		t.Fatal(err)
	}
	_, _, changed, err := applyActivity(ctx, s, l, Activity{Agent: "pi", Session: "s", Instance: "i", Run: "critical", Occurrence: "done", State: "turn_completed", Reminder: "turn_completed"})
	if err != nil || !changed {
		t.Fatal("critical observation blocked by state slots", err)
	}
	db, _ := loadRuns(s)
	if len(db.Runs) != 129 {
		t.Fatal("unfinished run evicted")
	}
}

func TestCapabilityCacheAndStateClaimAcrossObservers(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); printJSON(w, activityCaps(p)) }))
	defer server.Close()
	p.APIURL = server.URL
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cachedRunCapabilities(context.Background(), s, API{server.Client()}, p); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatal("capability stampede", requests.Load())
	}
	if _, err := runCapabilities(context.Background(), API{server.Client()}, p); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("fresh authorization used observation cache")
	}
	var claims atomic.Int32
	var release func(time.Duration)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn, err := claimStateSend(context.Background(), s, p.PairID)
			if err != nil {
				t.Error(err)
			}
			if fn != nil {
				claims.Add(1)
				release = fn
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("concurrent state sends", claims.Load())
	}
	release(90 * time.Second)
	fn, err := claimStateSend(context.Background(), s, p.PairID)
	if err != nil || fn != nil {
		t.Fatal("Retry-After bypass", err)
	}
}

func TestResolvedRequestPreservesOtherWaitsAndTools(t *testing.T) {
	s := testStore(t)
	s.installation()
	ctx := context.Background()
	l := fixtureLocator()
	a := Activity{Agent: "opencode", Session: "s", Instance: "i", Run: "r"}
	apply := func(occ, state, request string, resolved bool) StoredRun {
		a.Occurrence = occ
		a.State = state
		a.RequestID = request
		a.ResolveRequest = resolved
		_, r, _, err := applyActivity(ctx, s, l, a)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	apply("p1", "waiting_approval", "p1", false)
	apply("f1", "waiting_input", "f1", false)
	if r := apply("p1done", "running", "p1", true); r.State != "waiting_input" || r.Approval != nil {
		t.Fatal(r)
	}
	a.ToolID = "t1"
	a.ToolStatus = "running"
	a.ToolCategory = "shell"
	apply("tool", "tool_running", "", false)
	a.ToolID = ""
	if r := apply("f1done", "running", "f1", true); r.State != "tool_running" || len(r.Requests) != 0 {
		t.Fatal(r)
	}
	apply("done", "turn_completed", "", false)
	if r := apply("late", "running", "f1", true); r.State != "turn_completed" {
		t.Fatal("late resolution revived terminal")
	}
}

func TestCapabilitiesRetryAfterPersistsAcrossCalls(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		printJSON(w, map[string]any{"error": "rate_limited"})
	}))
	defer server.Close()
	p.APIURL = server.URL
	for i := 0; i < 3; i++ {
		if _, err := cachedRunCapabilities(context.Background(), s, API{server.Client()}, p); err == nil {
			t.Fatal("429 hidden")
		}
	}
	if requests != 1 {
		t.Fatal("Retry-After not persisted", requests)
	}
}

func TestRetainedTerminalCapacityWithPairsKeepsCriticalDelivery(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	ctx := context.Background()
	l := fixtureLocator()
	l.InstallationID = id
	db := RunStore{Runs: map[string]StoredRun{}, Current: map[string]string{}, Intents: map[string]RunIntent{}}
	for i := 0; i < 128; i++ {
		db.Runs[fmt.Sprint(i)] = StoredRun{State: "turn_completed", Updated: time.Now().Unix(), Seen: []string{digest(fmt.Sprint(i))}}
	}
	// More than the former arbitrary retired-count threshold must not gate admission.
	db.Retired = map[string]int64{}
	for i := 0; i < 4200; i++ {
		db.Retired[digest(fmt.Sprint("retired", i))] = time.Now().Unix() + 86400
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	q := p
	q.PairID = "pair_fixture_000000000000000002"
	posts := map[string]Envelope{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pair := p
		if strings.Contains(r.URL.Path, q.PairID) {
			pair = q
		}
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			printJSON(w, activityCaps(pair))
			return
		}
		var e Envelope
		json.NewDecoder(r.Body).Decode(&e)
		posts[e.PairID] = e
		printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p.APIURL = server.URL
	q.APIURL = server.URL
	s.savePair(ctx, p)
	s.savePair(ctx, q)
	a := Activity{Agent: "codex", Session: "s", Instance: "i", Run: "129", Occurrence: "done129", State: "turn_completed", Reminder: "turn_completed"}
	if err := publishActivity(ctx, s, API{server.Client()}, l, a); err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Fatal("critical multi-Pair delivery lost", len(posts))
	}
	if err := publishActivity(ctx, s, API{server.Client()}, l, a); err != nil {
		t.Fatal(err)
	}
	records, err := outboxRecords(ctx, s, "", false)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	for _, d := range records {
		if d.State != "handed_off" {
			t.Fatal(d.State)
		}
	}
	db, err = loadRuns(s)
	if err != nil || len(db.Runs) != 128 {
		t.Fatal(len(db.Runs), err)
	}
}

func TestRunConcurrentPersistenceReadable(t *testing.T) {
	s := testStore(t)
	s.installation()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _, err := applyActivity(context.Background(), s, fixtureLocator(), Activity{Agent: "pi", Session: "s", Instance: "i", Run: fmt.Sprint(i), Occurrence: fmt.Sprint(i), State: "running"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	db, err := loadRuns(s)
	if err != nil || len(db.Runs) != 16 {
		t.Fatal(len(db.Runs), err)
	}
}

func TestRunIntentPairRotationSurvivesRestart(t *testing.T) {
	s := testStore(t)
	s.installation()
	db := RunStore{Runs: map[string]StoredRun{}, Current: map[string]string{}, Intents: map[string]RunIntent{}}
	for pair := 0; pair < 6; pair++ {
		for n := 0; n < 8; n++ {
			key := fmt.Sprint(pair, "/", n)
			db.Intents[key] = RunIntent{PairID: fmt.Sprint(pair), Kind: "turn_completed", Payload: RunPayload{OccurredAt: int64(n)}}
		}
	}
	first := scheduleRunIntents(&db, false)
	if len(first) != 4 {
		t.Fatal(first)
	}
	owners := map[string]bool{}
	for _, key := range first {
		owners[db.Intents[key].PairID] = true
	}
	if len(owners) != 4 {
		t.Fatal("one Pair occupied all slots")
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	second := scheduleRunIntents(&db, false)
	if db.Intents[second[0]].PairID != "4" || db.Intents[second[1]].PairID != "5" {
		t.Fatal("failed earlier requests starved later Pair", second)
	}
}

// Invoked only by TestActivityStorageAndTransportAcrossProcesses using this
// test binary and an isolated store. Ordinary suite execution is a no-op.
func TestActivityProcessWorker(t *testing.T) {
	worker := os.Getenv("MOBTERM_TEST_ACTIVITY_WORKER")
	if worker == "" {
		return
	}
	s := Store{Dir: os.Getenv("MOBTERM_TEST_ACTIVITY_STORE")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	l := fixtureLocator()
	l.InstallationID = id
	for i := 0; i < 4; i++ {
		_, _, _, err := applyActivity(ctx, s, l, Activity{Agent: "pi", Session: "s", Instance: "i", Run: "shared", Occurrence: worker + fmt.Sprint(i), State: "running"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "ready-"+worker), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(s.Dir, "start-flush")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := flushRunSnapshots(ctx, s, newAPI()); err != nil && err.Error() != "activity_superseded" {
		t.Fatal(err)
	}
}

func TestActivityStorageAndTransportAcrossProcesses(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "")
	var caps, states atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			caps.Add(1)
			printJSON(w, activityCaps(p))
			return
		}
		if r.Method != "PUT" {
			t.Error("unexpected reminder from state")
			w.WriteHeader(500)
			return
		}
		states.Add(1)
		time.Sleep(150 * time.Millisecond)
		printJSON(w, map[string]any{"v": 2})
	}))
	defer server.Close()
	p.APIURL = server.URL
	if err := s.savePair(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	commands := []*exec.Cmd{}
	outputs := []*bytes.Buffer{}
	for _, worker := range []string{"one", "two"} {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestActivityProcessWorker$")
		cmd.Env = append(os.Environ(), "MOBTERM_TEST_ACTIVITY_WORKER="+worker, "MOBTERM_TEST_ACTIVITY_STORE="+s.Dir)
		output := &bytes.Buffer{}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		commands = append(commands, cmd)
		outputs = append(outputs, output)
	}
	for {
		_, one := os.Stat(filepath.Join(s.Dir, "ready-one"))
		_, two := os.Stat(filepath.Join(s.Dir, "ready-two"))
		if one == nil && two == nil {
			break
		}
		select {
		case <-ctx.Done():
			for _, cmd := range commands {
				_ = cmd.Wait()
			}
			t.Fatal("workers did not reach flush barrier")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "start-flush"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("worker %d: %v %s", i, err, outputs[i].String())
		}
	}
	db, err := loadRuns(s)
	if err != nil || len(db.Runs) != 1 {
		t.Fatal(len(db.Runs), err)
	}
	for _, run := range db.Runs {
		if run.Revision != 8 || len(run.Seen) != 8 {
			t.Fatal("cross-process update lost", run.Revision, len(run.Seen))
		}
	}
	if caps.Load() != 1 || states.Load() != 1 {
		t.Fatal("cross-process cache or state claim duplicated transport", caps.Load(), states.Load())
	}
}
