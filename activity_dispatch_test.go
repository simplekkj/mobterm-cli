package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Dispatch children of unit tests must execute the CLI, not recursively run
// the test suite. The same subprocess path is used by the built executable.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "activity" && os.Args[2] == "dispatch" {
		if path := os.Getenv("MOBTERM_TEST_DISPATCH_STARTS"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(1)
			}
			fmt.Fprintln(f, os.Getpid())
			f.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), activityDispatchBudget)
		defer cancel()
		s, err := defaultStore()
		if err == nil {
			err = dispatchActivity(ctx, s, newAPI(), os.NewFile(3, "activity-pending-lock"))
		}
		if err != nil {
			if path := os.Getenv("MOBTERM_TEST_DISPATCH_STARTS"); path != "" {
				f, e := os.OpenFile(path+".errors", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if e == nil {
					fmt.Fprintln(f, safeCode(err.Error()))
					f.Close()
				}
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOrdinaryCodexObservationIsDurableAndNetworkFree(t *testing.T) {
	claudeNotifyEnvironment(t)
	s := testStore(t)
	id, _ := s.installation()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	if err := s.savePair(context.Background(), testPair(id, server.URL)); err != nil {
		t.Fatal(err)
	}
	// Retain the pending worker's admission slot so the test distinguishes
	// synchronous I/O from the intentionally separate worker process.
	unlock, err := lockFile(context.Background(), filepath.Join(s.Dir, ".activity-pending-lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, event := range []string{"PreToolUse", "PostToolUse", "PreToolUse"} {
		raw := []byte(fmt.Sprintf(`{"hook_event_name":%q,"session_id":"session","turn_id":"turn","tool_name":"exec_command","tool_use_id":"tool","tool_input":{"cmd":"fixture"}}`, event))
		if _, err := observe(context.Background(), s, API{server.Client()}, "codex", raw); err != nil {
			t.Fatal(err)
		}
	}
	db, err := loadRuns(s)
	if err != nil || len(db.Runs) != 1 || len(db.Intents) != 1 || calls.Load() != 0 {
		t.Fatal("ordinary observation did not stay local/durable", err, len(db.Runs), len(db.Intents), calls.Load())
	}
	for _, r := range db.Runs {
		for _, tool := range r.Tools {
			if tool.Status != "returned" {
				t.Fatal("late pre regressed returned tool", tool.Status)
			}
		}
	}
	// Capacity recovery is also strictly local: retain the prior obligations
	// and report failed admission rather than flushing them on the Hook thread.
	for i := len(db.Runs); i < 128; i++ {
		db.Runs[fmt.Sprint(i)] = StoredRun{State: "running"}
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	l := fixtureLocator()
	l.InstallationID = id
	err = receiveActivity(context.Background(), s, l, Activity{Agent: "codex", Session: "new", Run: "new", Occurrence: "new", State: "tool_pending"})
	if err == nil || err.Error() != "run_capacity_retry_after_flush" || calls.Load() != 0 {
		t.Fatal("capacity recovery performed network or hid failed reception", err, calls.Load())
	}
}

func TestActivityDispatchBoundsChildrenAndPreservesExitWakeup(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	starts := filepath.Join(t.TempDir(), "starts")
	t.Setenv("MOBTERM_TEST_DISPATCH_STARTS", starts)
	var active, maximum, events atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	p := testPair(id, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		first.Do(func() { close(entered); <-release })
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			printJSON(w, activityCaps(p))
			return
		}
		var envelope Envelope
		if json.NewDecoder(r.Body).Decode(&envelope) != nil {
			t.Error("bad worker envelope")
		}
		events.Add(1)
		printJSON(w, map[string]any{"v": 2, "event_id": envelope.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p.APIURL = server.URL
	if err := s.savePair(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	l := fixtureLocator()
	l.InstallationID = id
	admit := func(run string) {
		t.Helper()
		if _, _, _, err := applyActivity(context.Background(), s, l, Activity{Agent: "codex", Session: "session", Run: run, Occurrence: run, State: "turn_completed", Reminder: "turn_completed"}); err != nil {
			t.Fatal(err)
		}
	}
	admit("first")
	if err := startActivityFlush(s); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		close(release)
		t.Fatal("worker did not start")
	}
	admit("last-arrival")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := startActivityFlush(s); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	raw, err := os.ReadFile(starts)
	close(release)
	if err != nil || len(strings.Fields(string(raw))) != 2 {
		t.Fatalf("expected one active and one pending child, got %q: %v", raw, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for events.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if events.Load() != 2 || maximum.Load() != 1 {
		diagnostic, _ := os.ReadFile(starts + ".errors")
		db, _ := loadRuns(s)
		t.Fatal("lost last wakeup or concurrent network workers", events.Load(), maximum.Load(), string(diagnostic), len(db.Intents))
	}
	// Wait for both bounded children to release their locks before cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	for _, name := range []string{".activity-pending-lock", ".activity-active-lock"} {
		unlock, err := lockFile(ctx, filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
}

func TestActivityDispatchPendingCrashReleasesAdmission(t *testing.T) {
	s := testStore(t)
	if _, err := s.installation(); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(t.TempDir(), "starts")
	t.Setenv("MOBTERM_TEST_DISPATCH_STARTS", starts)
	// Hold the sender slot so the owned child cannot finish before we kill it.
	active, err := lockFile(context.Background(), filepath.Join(s.Dir, ".activity-active-lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer active()
	if err := startActivityFlush(s); err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		raw, _ := os.ReadFile(starts)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("pending worker did not start")
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Kill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pending, err := lockFile(ctx, filepath.Join(s.Dir, ".activity-pending-lock"))
	if err != nil {
		t.Fatal("crashed child retained admission lock", err)
	}
	pending()
	// A later producer can acquire the slot without a lease timeout or reset.
	if err = launchActivityFlush(s, filepath.Join(t.TempDir(), "missing-binary")); err == nil {
		t.Fatal("failed launch was reported as successful")
	}
	pending, err = lockFile(ctx, filepath.Join(s.Dir, ".activity-pending-lock"))
	if err != nil {
		t.Fatal("failed launch retained admission lock", err)
	}
	pending()
}

func TestHookInstallationReadDoesNotWaitForRunWriter(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Hold the write lock throughout: this would exhaust the old two-second
	// lock budget even though the existing atomic identity is already readable.
	current, err := s.installation()
	if err != nil || current != id {
		t.Fatal("existing identity blocked behind activity writer", err)
	}
	if err := s.writeJSON("installation.json", map[string]any{"v": 1, "installation_id": "invalid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.installation(); err == nil {
		t.Fatal("fast identity read accepted corrupt identity")
	}
}

func TestHookConcurrentInstallationInitializationKeepsOneIdentity(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.installation()
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	expected := <-ids
	for id := range ids {
		if id != expected {
			t.Fatal("concurrent initialization split installation identity")
		}
	}
}

func TestCancelledStoreWaiterCannotLeakOrStealLock(t *testing.T) {
	s := testStore(t)
	owner, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if unlock, err := s.lock(ctx); err == nil {
		unlock()
		owner()
		t.Fatal("contended store ignored caller budget")
	}
	owner()
	// The expired kernel waiter may acquire next, but must release without
	// handing the lock to an abandoned caller or performing any state write.
	nextCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	next, err := s.lock(nextCtx)
	if err != nil {
		t.Fatal("cancelled waiter leaked the store lock", err)
	}
	next()
}
