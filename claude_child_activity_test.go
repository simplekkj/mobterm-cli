package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func childHook(root, child, event, tool, id string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"session_id": root, "agent_id": child, "hook_event_name": event,
		"tool_name": tool, "tool_use_id": id, "tool_input": map[string]any{
			"questions": []any{map[string]any{"question": "Choice?", "header": "Choice", "options": []any{map[string]string{"label": "A"}, map[string]string{"label": "B"}}}},
			"answers":   map[string]string{"Choice?": "A"}, "plan": "Local plan",
		},
	})
	return raw
}

func childRunFor(t *testing.T, s Store, session, toolID string) StoredRun {
	t.Helper()
	db, err := loadRuns(Store{Dir: s.Dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range db.Runs {
		if run.Session == session && run.Tools[toolID].InvocationID == toolID {
			return run
		}
	}
	t.Fatal("missing child run", session, toolID)
	return StoredRun{}
}

func TestClaudeChildDisabledQuestionAndPlanRemainIndependent(t *testing.T) {
	s, api, _, locator, _ := reviewInteractionSetup(t)
	holdApprovalDispatch(t, s)
	ctx := context.Background()
	event := func(root, child, hook, tool, id string) {
		t.Helper()
		if _, err := observe(ctx, Store{Dir: s.Dir}, api, "claude", childHook(root, child, hook, tool, id)); err != nil {
			t.Fatal(err)
		}
	}
	parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"UserPromptSubmit"}`))
	cycle, _, _, err := claudeCycleEvent(ctx, s, "root", "stop", "existing root summary", "")
	if err != nil || cycle == "" {
		t.Fatal(err)
	}
	event("root", "one", "PreToolUse", "AskUserQuestion", "same")
	event("root", "two", "PreToolUse", "AskUserQuestion", "same")
	event("root", "one", "PreToolUse", "ExitPlanMode", "plan")
	first, second, plan := childRunFor(t, s, "one", "same"), childRunFor(t, s, "two", "same"), childRunFor(t, s, "one", "plan")
	if first.Run == second.Run || first.Run == plan.Run || first.Interaction != nil || plan.Interaction == nil || plan.Interaction.UnavailableReason != "plan_approval_mode_unverified" {
		t.Fatal("disabled question or read-only plan was misattributed")
	}
	// Same child/tool on another root or captured target cannot consume it.
	event("other-root", "one", "PostToolUse", "AskUserQuestion", "same")
	a, p, err := parseActivity(ctx, s, "claude", childHook("root", "one", "PostToolUse", "AskUserQuestion", "same"))
	if err != nil {
		t.Fatal(err)
	}
	other := locator
	other.OccupantID = "other"
	if err = bindClaudeChildWait(&a, p, other); err != nil {
		t.Fatal(err)
	}
	if err = finishClaudeChildTool(ctx, s, other, a, p); err != nil {
		t.Fatal(err)
	}
	if len(childRunFor(t, s, "one", "same").Requests) != 1 {
		t.Fatal("wrong owner or target cleared the request")
	}

	event("root", "one", "PostToolUseFailure", "AskUserQuestion", "same")
	after := childRunFor(t, s, "one", "same")
	if len(after.Requests) != 0 || after.State != "running" || after.Tools["same"].Status != "failed" {
		t.Fatal("native failure did not settle original run", after.State)
	}
	event("root", "one", "PostToolUse", "AskUserQuestion", "same")
	event("root", "one", "PostToolUseFailure", "AskUserQuestion", "same")
	if childRunFor(t, s, "one", "same").Revision != after.Revision || len(childRunFor(t, s, "two", "same").Requests) != 1 || len(childRunFor(t, s, "one", "plan").Requests) != 1 {
		t.Fatal("late result affected another request")
	}
	event("root", "one", "PostToolUse", "ExitPlanMode", "plan")
	if len(childRunFor(t, s, "one", "plan").Requests) != 0 {
		t.Fatal("read-only plan remained pending")
	}
	event("root", "one", "Stop", "", "")
	if len(childRunFor(t, s, "two", "same").Requests) != 1 {
		t.Fatal("companion child Stop consumed wrong request")
	}
	event("root", "two", "Stop", "", "")
	if r := childRunFor(t, s, "two", "same"); r.State != "interrupted" || len(r.Requests) != 0 {
		t.Fatal("child Stop left an orphan", r.State)
	}
	got, stopped, summary, err := claudeCycleEvent(ctx, s, "root", "read", "", "")
	if err != nil || got != cycle || !stopped || summary != "existing root summary" {
		t.Fatal("child events modified root cycle or summary")
	}
	db, err := loadRuns(s)
	if err != nil || len(db.Runs) != 3 {
		t.Fatal("unrelated run created", len(db.Runs), err)
	}
	for _, intent := range db.Intents {
		if intent.Kind == "turn_completed" {
			t.Fatal("child emitted root completion")
		}
	}
}

func TestClaudeChildCancelledWaitAndExpiryReclaim(t *testing.T) {
	s, api, pair, _, notices := reviewInteractionSetup(t)
	holdApprovalDispatch(t, s)
	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{pair.PairID: {"claude": {"question": 3}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, join := withInteractionSenders(ctx)
	defer join()
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		_, err := observe(ctx, s, api, "claude", childHook("root", "child", "PreToolUse", "AskUserQuestion", "cancel"))
		done <- err
	}()
	defer func() {
		cancel()
		for range done {
		}
	}()
	notice := reviewNotice(t, notices, done)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	join()
	r, err := interactionRead(s, notice.Interaction.RequestID)
	run := childRunFor(t, s, "child", "cancel")
	if err != nil || r.State != "native_cancelled" || run.State != "interrupted" || len(run.Requests) != 0 {
		t.Fatal("cancelled waiter still active", r.State, run.State, err)
	}

	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := observe(context.Background(), s, api, "claude", childHook("root", "child", "PreToolUse", "AskUserQuestion", "orphan")); err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	now := time.Now().Unix()
	for k, r := range db.Runs {
		if _, ok := r.Requests["orphan"]; ok {
			key = k
			r.ChildWait.Until = now - 1
			db.Runs[k] = r
		}
	}
	if key == "" {
		t.Fatal("missing expiring run")
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	if err := flushRunSnapshots(context.Background(), s, api); err != nil {
		t.Fatal(err)
	}
	db, err = loadRuns(s)
	closed := db.Runs[key]
	if err != nil || closed.State != "interrupted" || len(closed.Requests) != 0 {
		t.Fatal("missed native end did not expire", closed.State, err)
	}
	// A durable intent keeps the snapshot alive. Once handed off, both the
	// settled run and its replay tombstone obey the ordinary retention policy.
	closed.Updated = now - 86401
	db.Runs[key] = closed
	db.Intents["unsent"] = RunIntent{Key: key, ExpiresAt: now + 1}
	reclaimRuns(&db, now, true)
	if _, ok := db.Runs[key]; !ok {
		t.Fatal("lost unsent responsibility")
	}
	for k, i := range db.Intents {
		if i.Key == key {
			delete(db.Intents, k)
		}
	}
	reclaimRuns(&db, now, true)
	if _, ok := db.Runs[key]; ok || db.Retired[key] <= now {
		t.Fatal("child run failed to release capacity")
	}
}

func TestClaudeChildNativeOwnerAndHistoricalRun(t *testing.T) {
	s, api, pair, l, notices := reviewInteractionSetup(t)
	// Exercise the independent sender through a real request that takes
	// longer than the 600ms synchronous observation budget.
	transport := api.Client.Transport
	var delay sync.Once
	api.Client = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/capabilities") {
			delay.Do(func() { time.Sleep(700 * time.Millisecond) })
		}
		return transport.RoundTrip(req)
	})}
	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{pair.PairID: {"claude": {"question": 3}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, join := withInteractionSenders(ctx)
	defer join()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		v, err := observe(ctx, s, api, "claude", childHook("root", "child", "PreToolUse", "AskUserQuestion", "live"))
		if err == nil {
			err = writeInteractionOutput(io.Discard, s, v)
		}
		done <- err
	}()
	defer func() {
		cancel()
		for range done {
		}
	}()
	n := reviewNotice(t, notices, done)
	reviewDecision(t, s, api, pair, n, "answer", `{"answers":{"q0":"A"}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	join()
	record, err := interactionRead(s, n.Interaction.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	owner := record.ParentBirth
	record.ParentBirth = "different-process"
	if err = s.writeJSON("interaction-"+record.RequestID+".json", record); err != nil {
		t.Fatal(err)
	}
	post := childHook("root", "child", "PostToolUse", "AskUserQuestion", "live")
	if _, err = observe(ctx, s, api, "claude", post); err != nil {
		t.Fatal(err)
	}
	got, _ := interactionRead(s, record.RequestID)
	if got.State != "response_written" || len(childRunFor(t, s, "child", "live").Requests) != 1 {
		t.Fatal("wrong native owner accepted")
	}
	// Recreate the previous release's scrubbed record and random run binding.
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	run := db.Runs[record.ChildRunKey]
	delete(db.Runs, record.ChildRunKey)
	run.Run = randomID()
	run.ChildWait = nil
	a := Activity{Agent: run.Agent, Session: run.Session, Instance: run.Instance, Run: run.Run}
	db.Runs[childRunKey(a, l)] = run
	record.ParentBirth = owner
	record.ChildRunKey = ""
	record.NativeIdentity = nativeInteractionIdentity(a, "live", l, owner)
	if err = s.writeJSON("interaction-"+record.RequestID+".json", record); err != nil {
		t.Fatal(err)
	}
	if err = saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	if _, err = observe(ctx, Store{Dir: s.Dir}, api, "claude", post); err != nil {
		t.Fatal(err)
	}
	got, _ = interactionRead(s, record.RequestID)
	closed := childRunFor(t, s, "child", "live")
	if got.State != "native_confirmed" || len(closed.Requests) != 0 || closed.Run != run.Run || closed.ChildWait == nil {
		t.Fatal("historical proven question not resolved")
	}
}

func TestClaudeChildRegistrationCapacityRetry(t *testing.T) {
	s, api, pair, _, notices := reviewInteractionSetup(t)
	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{pair.PairID: {"claude": {"question": 3}}}); err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		db.Runs[randomID()] = StoredRun{State: "running"}
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	pre := childHook("root", "child", "PreToolUse", "AskUserQuestion", "retry")
	if _, err := observe(context.Background(), s, api, "claude", pre); err == nil || err.Error() != "run_capacity_retry_after_flush" {
		t.Fatal("expected capacity failure", err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, ok := interactionFileID(entry.Name()); ok {
			t.Fatal("failed registration left a replay tombstone")
		}
	}
	for key := range db.Runs {
		delete(db.Runs, key)
		break
	}
	if err := saveRuns(s, db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, join := withInteractionSenders(ctx)
	defer join()
	defer cancel()
	done := make(chan error, 1)
	go func() { defer close(done); _, err := observe(ctx, s, api, "claude", pre); done <- err }()
	defer func() {
		cancel()
		for range done {
		}
	}()
	n := reviewNotice(t, notices, done)
	if n.Interaction == nil {
		t.Fatal("retry lost actionable question")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
