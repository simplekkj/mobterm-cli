package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func activityCaps(p Pair) map[string]any {
	return map[string]any{"v": 2, "pair_id": p.PairID, "installation_id": p.InstallationID, "enabled": true, "states": true, "inbox": true, "capability_generation": 1, "policy_generation": 3, "expires_at": time.Now().Unix() + 60, "policy": map[string]any{"turn_completed": true, "needs_input": false, "abnormal_end": false}, "approvals": map[string]any{"codex": true, "claude": false, "opencode": false, "pi": false}}
}
func TestActivitySchemas(t *testing.T) {
	raw, err := os.ReadFile("testdata/activity-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Source  string          `json:"source"`
		Version string          `json:"version"`
		Payload json.RawMessage `json:"payload"`
		State   string          `json:"state"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Source+"_"+c.State, func(t *testing.T) {
			s := testStore(t)
			s.installation()
			a, _, err := parseActivity(context.Background(), s, c.Source, c.Payload)
			if err != nil || a.State != c.State {
				t.Fatal(c.Version, a.State, err)
			}
			if c.State == "running" && a.ToolStatus == "failed" && a.Reminder != "" {
				t.Fatal("tool failure became run failure")
			}
		})
	}
}
func TestActivityOfflineAdmissionAndSnapshotRecovery(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "")
	fail := true
	posts := 0
	states := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			if fail {
				w.WriteHeader(503)
				printJSON(w, map[string]any{"error": "unavailable"})
				return
			}
			printJSON(w, activityCaps(p))
			return
		}
		var e Envelope
		if json.NewDecoder(r.Body).Decode(&e) != nil {
			t.Error("invalid envelope")
		}
		if strings.Contains(r.URL.Path, "snapshots") {
			states++
			printJSON(w, map[string]any{"v": 2, "stream_id": e.StreamID, "revision": e.Revision})
			return
		}
		posts++
		printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p.APIURL = server.URL
	s.savePair(context.Background(), p)
	api := API{server.Client()}
	l := fixtureLocator()
	l.InstallationID = id
	a := Activity{Agent: "codex", Session: "session", Instance: "instance", Run: "run", Occurrence: "completed", State: "turn_completed", Reminder: "turn_completed"}
	if err := publishActivity(context.Background(), s, api, l, a); err == nil {
		t.Fatal("network failure not reported")
	}
	db, _ := loadRuns(s)
	if len(db.Intents) != 1 || posts != 0 {
		t.Fatal("offline critical event lost")
	}
	fail = false
	if err := flushRunSnapshots(context.Background(), s, api); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || states != 0 {
		t.Fatal("reminder should itself restore snapshot", posts, states)
	}
	if err := publishActivity(context.Background(), s, api, l, a); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatal("duplicate reminder")
	}
}
func TestMalformedCapabilitiesNeverDowngrade(t *testing.T) {
	p := testPair("install_fixture_0000000000000001", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		printJSON(w, map[string]any{"v": 2, "expires_at": time.Now().Unix() + 60})
	}))
	defer server.Close()
	p.APIURL = server.URL
	if _, err := runCapabilities(context.Background(), API{server.Client()}, p); err == nil {
		t.Fatal("malformed receipt treated as legacy")
	}
}
func TestToolAggregationLongRunAndDuplicateStart(t *testing.T) {
	s := testStore(t)
	s.installation()
	ctx := context.Background()
	l := fixtureLocator()
	a := Activity{Agent: "claude", Session: "session", Instance: "instance", Occurrence: "one", State: "starting"}
	key, r, _, err := applyActivity(ctx, s, l, a)
	if err != nil {
		t.Fatal(err)
	}
	other, again, changed, err := applyActivity(ctx, s, l, a)
	if err != nil || changed || other != key || again.Run != r.Run {
		t.Fatal("duplicate start created run")
	}
	a.Run = r.Run
	for i := 0; i < 40; i++ {
		a.ToolID = fmt.Sprint(i)
		a.Occurrence = "start" + a.ToolID
		a.State = "tool_running"
		a.ToolStatus = "running"
		_, _, _, err = applyActivity(ctx, s, l, a)
		if err != nil {
			t.Fatal(err)
		}
		a.Occurrence = "end" + a.ToolID
		a.State = "running"
		a.ToolStatus = "failed"
		_, r, _, err = applyActivity(ctx, s, l, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	if terminalRun(r.State) {
		t.Fatal("tool failure ended run")
	}
	a.Occurrence = "late-start"
	a.ToolStatus = "running"
	a.State = "tool_running"
	_, _, changed, err = applyActivity(ctx, s, l, a)
	if err != nil || changed {
		t.Fatal("old tool restarted")
	}
}
func TestApprovalCASAcrossPairsAndDisableRelease(t *testing.T) {
	claudeNotifyEnvironment(t)
	s := testStore(t)
	id, _ := s.installation()
	ctx := context.Background()
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	second := p
	second.PairID = "pair_fixture_000000000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pair := p
		if strings.Contains(r.URL.Path, second.PairID) {
			pair = second
		}
		printJSON(w, activityCaps(pair))
	}))
	defer server.Close()
	p.APIURL = server.URL
	second.APIURL = server.URL
	s.savePair(ctx, p)
	s.savePair(ctx, second)
	s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 1}, second.PairID: {"codex": 2}})
	birth, _ := processIdentity(os.Getpid())
	parent, _ := processIdentity(os.Getppid())
	request := ApprovalRequest{V: 3, RequestID: randomID(), Digest: digest("{}"), OperationJSON: "{}", Agent: "codex", Locator: l, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 120, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parent, State: "pending", Pairs: map[string]int64{p.PairID: 3, second.PairID: 3}}
	s.writeJSON("approval-"+request.RequestID+".json", request)
	api := API{server.Client()}
	frames := make([]ApprovalFrame, 2)
	for i, pair := range []Pair{p, second} {
		frames[i] = ApprovalFrame{V: 3, PairID: pair.PairID, RequestID: request.RequestID, Digest: request.Digest, DecisionID: randomID(), Action: []string{"allow_once", "deny"}[i], PolicyGeneration: 3, ExpiresAt: request.ExpiresAt}
		key, _ := decode64(pair.EncryptionKey, 32)
		frames[i].Proof, _ = approvalMAC(key, frames[i])
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range frames {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = approvalCommand(ctx, s, api, "decide", bytes.NewReader(framed(frames[i])))
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatal("expected exactly one CAS winner", errs)
	}
	winner := 0
	if errs[1] == nil {
		winner = 1
	}
	if _, err = approvalCommand(ctx, s, api, "decide", bytes.NewReader(framed(frames[winner]))); err != nil {
		t.Fatal("idempotent decision failed", err)
	}
	unlock, _ := s.lock(ctx)
	err = releaseDisabledApprovalsLocked(s, map[string]map[string]int64{})
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := approvalRead(s, request.RequestID)
	if err != nil || saved.State != "released" || saved.OperationJSON != "" {
		t.Fatal("disable failed to release and scrub", saved.State, err)
	}
	frames[winner].Proof = "forged"
	if _, err = approvalCommand(ctx, s, api, "decide", bytes.NewReader(framed(frames[winner]))); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatal("forged proof accepted", err)
	}
}
func TestClaudeLateIdleCannotCompleteNewCycle(t *testing.T) {
	s := testStore(t)
	s.installation()
	ctx := context.Background()
	event := func(name string) []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "session", "hook_event_name": name, "notification_type": "idle_prompt"})
		return b
	}
	parseActivity(ctx, s, "claude", event("UserPromptSubmit"))
	parseActivity(ctx, s, "claude", event("Stop"))
	a, _, err := parseActivity(ctx, s, "claude", event("Notification"))
	if err != nil || a.State != "turn_completed" {
		t.Fatal(a, err)
	}
	parseActivity(ctx, s, "claude", event("UserPromptSubmit"))
	a, _, err = parseActivity(ctx, s, "claude", event("Notification"))
	if err != nil || a.State != "" {
		t.Fatal("late idle ended next run", a, err)
	}
}
func TestApprovalInputRefusesUnknownAndOversize(t *testing.T) {
	for _, p := range []activityInput{{ToolName: "Unknown", ToolInput: json.RawMessage(`{"command":"x"}`)}, {ToolName: "Bash", ToolInput: json.RawMessage(`{"command":12}`)}, {ToolName: "Bash", ToolInput: json.RawMessage(strings.Repeat("x", 6145))}} {
		if approvalInputSupported("codex", p) {
			t.Fatal("unsafe approval input")
		}
	}
	if !approvalInputSupported("codex", activityInput{Cwd: "/work/project", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"pwd"}`)}) {
		t.Fatal("valid input refused")
	}
}

func TestNativeWaitConsumedWithoutReplay(t *testing.T) {
	claudeNotifyEnvironment(t)
	s := testStore(t)
	id, _ := s.installation()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			printJSON(w, activityCaps(p))
			return
		}
		var e Envelope
		_ = json.NewDecoder(r.Body).Decode(&e)
		printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p.APIURL = server.URL
	s.savePair(ctx, p)
	s.writeJSON("approval-policy-v2.json", map[string]map[string]int64{p.PairID: {"codex": 1}})
	api := API{server.Client()}
	a := Activity{Agent: "codex", Session: "session", Instance: "instance", Run: "run", Occurrence: "native-wait", State: "waiting_approval", Reminder: "needs_input"}
	input := activityInput{Cwd: "/work/project", HookEventName: "PermissionRequest", ToolName: "Bash", ToolUseID: "native-request-1", ToolInput: json.RawMessage(`{"command":"printf fixture"}`)}
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() { v, e := approvalWait(ctx, s, api, l, a, input); done <- result{v, e} }()
	var request ApprovalRequest
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(s.Dir, "approval-*.json"))
		for _, file := range files {
			name := filepath.Base(file)
			if stringsHasApproval(name) {
				request, err = approvalRead(s, name[9:len(name)-5])
				if err == nil {
					break
				}
			}
		}
		if request.RequestID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if request.RequestID == "" {
		t.Fatal("waiter not created")
	}
	f := ApprovalFrame{V: 3, PairID: p.PairID, RequestID: request.RequestID, Digest: request.Digest, DecisionID: randomID(), Action: "allow_once", PolicyGeneration: 3, ExpiresAt: request.ExpiresAt}
	key, _ := decode64(p.EncryptionKey, 32)
	f.Proof, _ = approvalMAC(key, f)
	if _, err = approvalCommand(ctx, s, api, "decide", bytes.NewReader(framed(f))); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		raw, _ := json.Marshal(r.value)
		if !bytes.Contains(raw, []byte(`"behavior":"allow"`)) {
			t.Fatal("native output not allow")
		}
	case <-ctx.Done():
		t.Fatal("native waiter did not consume")
	}
	stored, err := approvalRead(s, request.RequestID)
	if err != nil || stored.State != "consumed" || stored.OperationJSON != "" {
		t.Fatal("consume missing/sensitive snapshot retained", err)
	}
	v, err := approvalWait(ctx, s, api, l, a, input)
	if err != nil || v != nil {
		t.Fatal("known native request replayed", err)
	}
}
