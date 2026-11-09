package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func interactionTestGrant(p Pair) InteractionCapabilities {
	m := map[string]map[string]bool{}
	for _, agent := range []string{"codex", "claude", "opencode", "pi"} {
		m[agent] = map[string]bool{}
		for _, kind := range []string{"approval", "question", "form", "elicitation", "planApproval", "followupMessage"} {
			m[agent][kind] = true
		}
	}
	return InteractionCapabilities{V: 1, PairID: p.PairID, InstallationID: p.InstallationID, Enabled: true, Protocols: []int{1}, PolicyGeneration: 3, CapabilityGeneration: 1, ExpiresAt: time.Now().Unix() + 60, Interactions: m}
}
func interactionTestSetup(t *testing.T) (Store, API, []Pair, InteractionRecord) {
	t.Helper()
	claudeNotifyEnvironment(t)
	s := testStore(t)
	id, e := s.installation()
	if e != nil {
		t.Fatal(e)
	}
	l, e := captureTarget(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	p := testPair(id, "")
	p2 := p
	p2.PairID = "pair_fixture_000000000000000002"
	pairs := []Pair{p, p2}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pair := pairs[0]
		if strings.Contains(r.URL.Path, pairs[1].PairID) {
			pair = pairs[1]
		}
		if strings.HasSuffix(r.URL.Path, "interaction-capabilities") {
			printJSON(w, interactionTestGrant(pair))
			return
		}
		printJSON(w, activityCaps(pair))
	}))
	t.Cleanup(server.Close)
	policy := InteractionPolicy{}
	for i := range pairs {
		pairs[i].APIURL = server.URL
		if e = s.savePair(context.Background(), pairs[i]); e != nil {
			t.Fatal(e)
		}
		policy[pairs[i].PairID] = map[string]map[string]int64{"claude": {"question": 3}}
	}
	if e = s.writeJSON("interaction-policy-v1.json", policy); e != nil {
		t.Fatal(e)
	}
	birth, _ := processIdentity(os.Getpid())
	parent, _ := processIdentity(os.Getppid())
	spec := InteractionSpec{V: 1, RequestID: randomID(), Kind: "question", Agent: "claude", Fields: []InteractionField{{ID: "q0", Type: "string", Label: "Question", Required: true}}, SupportedActions: []string{"answer"}}
	raw, _ := json.Marshal(spec)
	r := InteractionRecord{V: 1, RequestID: spec.RequestID, Digest: digest(string(raw)), Kind: "question", Agent: "claude", RequestJSON: string(raw), Locator: l, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parent, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Unix() + 120, Revision: 1, State: "pending", Pairs: map[string]int64{pairs[0].PairID: 3, pairs[1].PairID: 3}, OwnerToken: randomID()}
	if e = s.writeJSON("interaction-"+r.RequestID+".json", r); e != nil {
		t.Fatal(e)
	}
	return s, API{server.Client()}, pairs, r
}
func interactionTestFrame(p Pair, r InteractionRecord) InteractionFrame {
	f := InteractionFrame{V: 1, PairID: p.PairID, RequestID: r.RequestID, Digest: r.Digest, Kind: r.Kind, Action: "answer", ResponseJSON: `{"answers":{"q0":"你好, world"}}`, DecisionID: randomID(), PolicyGeneration: 3, RequestRevision: 1, ExpiresAt: r.ExpiresAt}
	f.ResponseDigest = digest(f.ResponseJSON)
	key, _ := decode64(p.EncryptionKey, 32)
	f.Proof, _ = interactionMAC(key, f)
	return f
}
func TestInteractionFramesRejectAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"v":1,"v":1}`, `{"answers":{"x":1,"x":2}}`, `{} {}`, `{"x":"\ud800"}`, `{"x":"\udfff"}`, "{\"x\":\"\xff\"}"} {
		var v any
		if interactionJSON([]byte(raw), &v) == nil {
			t.Fatalf("accepted ambiguous JSON %q", raw)
		}
	}
	for _, raw := range [][]byte{append(interactionFrameBytes(map[string]int{"v": 1}), 0), {0, 1, 0, 1}, {0, 0, 0, 5, '{'}} {
		var v any
		if readInteractionFrame(context.Background(), bytes.NewReader(raw), &v) == nil {
			t.Fatal("accepted invalid frame")
		}
	}
	var v any
	if readInteractionFrame(context.Background(), bytes.NewReader(interactionFrameBytes(map[string]int{"v": 1})), &v) != nil {
		t.Fatal("valid frame rejected")
	}
}
func TestInteractionTypedAnswers(t *testing.T) {
	min := 1
	spec := InteractionSpec{Kind: "form", SupportedActions: []string{"answer"}, Fields: []InteractionField{{ID: "on", Type: "boolean", Label: "On", Required: true}, {ID: "n", Type: "number", Label: "Number", Required: true}, {ID: "choices", Type: "multiselect", Label: "Choose", Options: []InteractionOption{{ID: "a", Label: "A,B"}}, MinItems: &min}, {ID: "detail", Type: "string", Label: "Detail", Required: true, When: []InteractionCondition{{FieldID: "choices", Op: "eq", Value: json.RawMessage(`"a"`)}}}}}
	if e := interactionSchema(spec.Fields); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"answers":{"on":false,"n":0}}`, `{"answers":{"on":true,"n":0.1,"choices":["a"],"detail":""}}`} {
		if e := interactionAnswers(spec, "answer", raw); e != nil {
			t.Fatal(raw, e)
		}
	}
	for _, raw := range []string{`{"answers":{"on":false,"n":null}}`, `{"answers":{"on":false,"n":9007199254740990.1}}`, `{"answers":{"on":false,"n":0,"choices":["a"]}}`, `{"answers":{"on":false,"n":0,"detail":"inactive"}}`, `{"answers":{"on":false,"n":0,"choices":[]}}`, `{"answers":{"on":false,"n":0,"extra":1}}`, `{"answers":{"on":false,"n":0,"n":1}}`} {
		if e := interactionAnswers(spec, "answer", raw); e == nil {
			t.Fatal("invalid answer accepted", raw)
		}
	}
	max := 1
	f := InteractionField{ID: "a", Type: "string", Label: "A", MaxLength: &max}
	if interactionFieldValue(f, "😀") != nil {
		t.Fatal("unicode scalar length")
	}
	f.LengthUnit = "utf16"
	if interactionFieldValue(f, "😀") == nil {
		t.Fatal("UTF16 native constraint ignored")
	}
}
func TestInteractionMACBindsEveryField(t *testing.T) {
	f := InteractionFrame{PairID: "pair_fixture_000000000000000001", RequestID: "request_fixture_00000000000001", Digest: strings.Repeat("a", 64), Kind: "question", Action: "answer", ResponseDigest: strings.Repeat("b", 64), DecisionID: "decision_fixture_0000000000001", PolicyGeneration: 3, RequestRevision: 1, ExpiresAt: 1234567890}
	key := bytes.Repeat([]byte{7}, 32)
	want, _ := interactionMAC(key, f)
	mutations := []func(*InteractionFrame){func(f *InteractionFrame) { f.PairID += "x" }, func(f *InteractionFrame) { f.RequestID += "x" }, func(f *InteractionFrame) { f.Digest += "x" }, func(f *InteractionFrame) { f.Kind = "form" }, func(f *InteractionFrame) { f.Action = "cancel" }, func(f *InteractionFrame) { f.ResponseDigest += "x" }, func(f *InteractionFrame) { f.DecisionID += "x" }, func(f *InteractionFrame) { f.PolicyGeneration++ }, func(f *InteractionFrame) { f.RequestRevision++ }, func(f *InteractionFrame) { f.ExpiresAt++ }}
	for _, mutate := range mutations {
		changed := f
		mutate(&changed)
		got, _ := interactionMAC(key, changed)
		if want == got {
			t.Fatal("unsigned field")
		}
	}
}
func TestInteractionCASRetryAndStatusPrivacy(t *testing.T) {
	s, api, pairs, r := interactionTestSetup(t)
	frames := []InteractionFrame{interactionTestFrame(pairs[0], r), interactionTestFrame(pairs[1], r)}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range frames {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(frames[i])))
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatal("must have exactly one winner", errs)
	}
	winner := 0
	if errs[1] == nil {
		winner = 1
	}
	f := frames[winner]
	if _, e := interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); e != nil {
		t.Fatal("stable retry", e)
	}
	f.ResponseJSON = `{"answers":{"q0":"different"}}`
	f.ResponseDigest = digest(f.ResponseJSON)
	key, _ := decode64(pairs[winner].EncryptionKey, 32)
	f.Proof, _ = interactionMAC(key, f)
	if _, e := interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); e == nil {
		t.Fatal("same decision different body accepted")
	}
	saved, _ := interactionRead(s, r.RequestID)
	saved.State = "dispatching"
	saved.PID = 99999999
	s.writeJSON("interaction-"+r.RequestID+".json", saved)
	for i, p := range pairs {
		v, e := interactionCommand(context.Background(), s, api, "status", bytes.NewReader(interactionFrameBytes(InteractionFrame{V: 1, PairID: p.PairID, RequestID: r.RequestID})))
		if e != nil {
			t.Fatal("exited target receipt unavailable", e)
		}
		m := v.(map[string]any)
		if m["state"] != "delivery_unknown" || m["request_json"] != nil {
			t.Fatal("dishonest receipt", m)
		}
		if i != winner && m["decision_id"] != nil {
			t.Fatal("other pair receipt disclosure")
		}
	}
	release := frames[winner]
	release.DecisionID = randomID()
	release.Action = "release"
	release.ResponseJSON = "{}"
	release.ResponseDigest = digest("{}")
	release.Proof, _ = interactionMAC(key, release)
	if _, e := interactionCommand(context.Background(), s, api, "release", bytes.NewReader(interactionFrameBytes(release))); e == nil {
		t.Fatal("dispatching released")
	}
}
func TestInteractionDisableCancelsButPreservesReceipt(t *testing.T) {
	s, api, pairs, r := interactionTestSetup(t)
	f := interactionTestFrame(pairs[0], r)
	if _, e := interactionCommand(context.Background(), s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); e != nil {
		t.Fatal(e)
	}
	config := map[string]any{"v": 1, "pair_id": pairs[0].PairID, "agent": "claude", "kind": "question", "enabled": false, "policy_generation": 3}
	if _, e := interactionCommand(context.Background(), s, api, "configure", bytes.NewReader(interactionFrameBytes(config))); e != nil {
		t.Fatal(e)
	}
	saved, e := interactionRead(s, r.RequestID)
	if e != nil || saved.State != "native_cancelled" || saved.RequestJSON != "" || saved.Frame.ResponseJSON != "" {
		t.Fatal("disable did not scrub/cancel", e)
	}
	if _, e = interactionCommand(context.Background(), s, api, "status", bytes.NewReader(interactionFrameBytes(InteractionFrame{V: 1, PairID: pairs[0].PairID, RequestID: r.RequestID}))); e != nil {
		t.Fatal("disable lost original receipt", e)
	}
}

type interactionBrokenWriter struct{}

func (interactionBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestInteractionWriteCrashBoundaries(t *testing.T) {
	s, _, _, r := interactionTestSetup(t)
	// Pre-dispatch failure can terminate but never invent native delivery.
	if e := interactionComplete(s, r.RequestID, "response_written"); e == nil {
		t.Fatal("pending became written")
	}
	r.State = "dispatching"
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	if e := writeInteractionOutput(interactionBrokenWriter{}, s, InteractionOutput{Value: map[string]string{"answer": "yes"}, RequestID: r.RequestID}); !errors.Is(e, io.ErrClosedPipe) {
		t.Fatal(e)
	}
	saved, _ := interactionRead(s, r.RequestID)
	if saved.State != "delivery_unknown" || saved.RequestJSON != "" {
		t.Fatal("failed stdout was reported delivered")
	}
	if e := interactionComplete(s, r.RequestID, "response_written"); e == nil {
		t.Fatal("unknown was replayed")
	}
}
func TestActivityReminderKeepsItsOwnRequest(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://fixture.invalid")
	s.savePair(context.Background(), p)
	l := fixtureLocator()
	l.InstallationID = id
	for _, id := range []string{"z-first", "a-second"} {
		a := Activity{Agent: "claude", Session: "s", Run: "r", Instance: "i", Occurrence: id, RequestID: id, State: "waiting_input", Reminder: "needs_input", Interaction: &InteractionHint{V: 1, RequestID: id, Digest: strings.Repeat("a", 64), Kind: "question", ExpiresAt: time.Now().Unix() + 120}}
		if _, _, _, e := applyActivity(context.Background(), s, l, a); e != nil {
			t.Fatal(e)
		}
	}
	db, e := loadRuns(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(db.Intents) != 2 {
		t.Fatal("requests coalesced")
	}
	for _, i := range db.Intents {
		if i.Payload.Interaction == nil || i.Payload.Interaction.RequestID != i.Occurrence {
			t.Fatal("reminder rebound to aggregate", i.Occurrence)
		}
	}
}

func TestInteractionCrossLanguageGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/interaction-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Key     string           `json:"key_base64"`
		Request string           `json:"request_json"`
		Frame   InteractionFrame `json:"frame"`
		Input   string           `json:"signature_input_base64"`
	}
	if err = interactionJSON(raw, &vector); err != nil {
		t.Fatal(err)
	}
	key, err := decode64(vector.Key, 32)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := interactionMAC(key, vector.Frame)
	if err != nil || proof != vector.Frame.Proof || digest(vector.Request) != vector.Frame.Digest || digest(vector.Frame.ResponseJSON) != vector.Frame.ResponseDigest {
		t.Fatal("independent Python/Go signature mismatch", err)
	}
	var spec InteractionSpec
	if err = interactionJSON([]byte(vector.Request), &spec); err != nil {
		t.Fatal(err)
	}
	if err = interactionAnswers(spec, vector.Frame.Action, vector.Frame.ResponseJSON); err != nil {
		t.Fatal(err)
	}
}

func TestInteractionHeldDispatchNeverReplays(t *testing.T) {
	testInteractionHeldDispatch(t, "")
}

func TestInteractionHeldSenderCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "cancel"} {
		t.Run(mode, func(t *testing.T) { testInteractionHeldDispatch(t, mode) })
	}
}

func testInteractionHeldDispatch(t *testing.T, delayed string) {
	t.Helper()
	s, api, pairs, existing := interactionTestSetup(t)
	// Remove the unrelated seeded fixture, leaving a real held waiter to register.
	if e := os.Remove(s.Dir + "/interaction-" + existing.RequestID + ".json"); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ctx, waitSenders := withInteractionSenders(ctx)
	senderStarted, releaseSender, transportDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var gate sync.Once
	if delayed != "" {
		base := api.Client.Transport
		api.Client = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
			held := false
			if strings.HasSuffix(req.URL.Path, "/capabilities") {
				gate.Do(func() {
					held = true
					close(senderStarted)
					// Hold the actual sender after its durable apply, independently
					// of cancellation, until the resource owner begins teardown.
					<-releaseSender
				})
			}
			if held {
				defer close(transportDone)
			}
			return base.RoundTrip(req)
		})}
	}
	spec := InteractionSpec{Kind: "question", NativeID: "native/question/one", AgentVersion: "2.1.317", Fields: []InteractionField{{ID: "q0", Type: "string", Label: "Q", Required: true}}, SupportedActions: []string{"answer", "release"}, TTL: 120 * time.Second}
	activity := Activity{Agent: "claude", Session: "session", Run: "turn", Instance: "runtime", State: "waiting_input", Reminder: "needs_input", Occurrence: "observed"}
	type result struct {
		d *InteractionDecision
		e error
	}
	done := make(chan result, 1)
	waiterDone := make(chan struct{})
	t.Cleanup(func() {
		if delayed != "complete" {
			cancel()
		}
		<-waiterDone
		close(releaseSender)
		waitSenders()
		cancel()
		// This runs before server.Close and TempDir cleanup, including on Fatal.
		if delayed != "" {
			select {
			case <-senderStarted:
				select {
				case <-transportDone:
				default:
					t.Error("sender join returned before transport settled")
				}
			default:
			}
		}
		if _, e := loadRuns(s); e != nil {
			t.Error("sender did not leave a readable store before cleanup", e)
		}
	})
	go func() {
		defer close(waiterDone)
		d, e := interactionNativeWait(ctx, s, api, existing.Locator, activity, spec)
		done <- result{d, e}
	}()
	var r InteractionRecord
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		entries, _ := os.ReadDir(s.Dir)
		for _, entry := range entries {
			if id, ok := interactionFileID(entry.Name()); ok {
				r, _ = interactionRead(s, id)
				break
			}
		}
		if r.RequestID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.RequestID == "" {
		t.Fatal("no held request")
	}
	if delayed != "" {
		select {
		case <-senderStarted:
		case <-ctx.Done():
			t.Fatal("sender did not reach controlled transport gate")
		}
		db, e := loadRuns(s)
		if e != nil || len(db.Intents) == 0 {
			t.Fatal("blocked sender has no durable reminder responsibility", e)
		}
	}
	f := interactionTestFrame(pairs[0], r)
	if _, e := interactionCommand(ctx, s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); e != nil {
		t.Fatal(e)
	}
	var delivered *InteractionDecision
	select {
	case got := <-done:
		if got.e != nil || got.d == nil {
			t.Fatal(got.e)
		}
		delivered = got.d
	case <-ctx.Done():
		t.Fatal("waiter did not settle")
	}
	saved, _ := interactionRead(s, r.RequestID)
	if saved.State != "dispatching" || delivered.ResponseJSON != f.ResponseJSON {
		t.Fatal("dispatch not durably claimed")
	}
	// Model a crash after CAS, before native I/O: a re-enumerated native request
	// cannot earn a second dispatch permission, even with a fresh local UUID.
	if d, e := interactionNativeWait(ctx, s, api, existing.Locator, activity, spec); e == nil || d != nil {
		t.Fatal("native replay acquired another dispatch")
	}
	if _, e := interactionCommand(ctx, s, api, "respond", bytes.NewReader(interactionFrameBytes(f))); e != nil {
		t.Fatal("receipt retry should be safe", e)
	}
	if e := writeInteractionOutput(io.Discard, s, InteractionOutput{Value: map[string]any{"answer": "fixture"}, RequestID: r.RequestID}); e != nil {
		t.Fatal(e)
	}
	saved, _ = interactionRead(s, r.RequestID)
	if saved.State != "response_written" || saved.RequestJSON != "" || saved.Frame.ResponseJSON != "" {
		t.Fatal("delivery receipt/scrub missing")
	}
	if delayed != "" {
		select {
		case <-transportDone:
			t.Fatal("sender escaped its gate before native output and cleanup")
		default:
		}
	}
}

func TestInteractionNativeEvidenceCannotInventConsumption(t *testing.T) {
	s, _, _, r := interactionTestSetup(t)
	r.State = "dispatching"
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	expected := `{"Question":"chosen"}`
	if e := interactionSetExpected(s, r.RequestID, "session", "native/call", digest(expected)); e != nil {
		t.Fatal(e)
	}
	if e := interactionConfirmClaude(s, "other-session", "native/call", json.RawMessage(expected)); e != nil {
		t.Fatal(e)
	}
	saved, _ := interactionRead(s, r.RequestID)
	if saved.State != "dispatching" {
		t.Fatal("wrong session confirmed")
	}
	if e := interactionConfirmClaude(s, "session", "native/call", json.RawMessage(expected)); e != nil {
		t.Fatal(e)
	}
	if e := interactionComplete(s, r.RequestID, "response_written"); e != nil {
		t.Fatal(e)
	}
	saved, _ = interactionRead(s, r.RequestID)
	if saved.State != "native_confirmed" || saved.RequestJSON != "" {
		t.Fatal("late stdout receipt downgraded native evidence")
	}
}

func TestInteractionReadOnlyContextAndExpiryCleanup(t *testing.T) {
	s, api, pairs, r := interactionTestSetup(t)
	r.State = "unavailable"
	r.Pairs[pairs[0].PairID] = 0
	r.UnavailableReason = "native_owner_unavailable"
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	v, e := interactionCommand(context.Background(), s, api, "inspect", bytes.NewReader(interactionFrameBytes(InteractionFrame{V: 1, PairID: pairs[0].PairID, RequestID: r.RequestID})))
	if e != nil {
		t.Fatal(e)
	}
	m := v.(map[string]any)
	if m["request_json"] != r.RequestJSON || m["unavailable_reason"] != r.UnavailableReason {
		t.Fatal("read-only context missing")
	}
	r.State = "dispatching"
	r.ExpiresAt = time.Now().Unix() - 1
	r.Frame = &InteractionFrame{ResponseJSON: `{"answers":{"secret":"erase"}}`}
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	if e = interactionCleanup(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	saved, e := interactionRead(s, r.RequestID)
	if e != nil || saved.State != "delivery_unknown" || saved.RequestJSON != "" || saved.Frame.ResponseJSON != "" || saved.Digest == "" {
		t.Fatal("expiry scrub lost tombstone or retained secret", e)
	}
}
func TestInteractionPluginAuthorizeCannotAllocateDispatch(t *testing.T) {
	s, api, pairs, r := interactionTestSetup(t)
	input := map[string]any{"v": 1, "request_id": r.RequestID, "owner_token": r.OwnerToken}
	v, e := interactionCommand(context.Background(), s, api, "authorize", bytes.NewReader(interactionFrameBytes(input)))
	if e != nil || v.(map[string]any)["allowed"] != false {
		t.Fatal("pending native caller allocated dispatch", e)
	}
	r.State = "dispatching"
	f := interactionTestFrame(pairs[0], r)
	r.Frame = &f
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	v, e = interactionCommand(context.Background(), s, api, "authorize", bytes.NewReader(interactionFrameBytes(input)))
	if e != nil || v.(map[string]any)["allowed"] != true {
		t.Fatal("valid plugin grant failed", e)
	}
	s.writeJSON("interaction-policy-v1.json", InteractionPolicy{})
	v, e = interactionCommand(context.Background(), s, api, "authorize", bytes.NewReader(interactionFrameBytes(input)))
	if e != nil || v.(map[string]any)["allowed"] != false {
		t.Fatal("disabled plugin could deliver", e)
	}
	input["owner_token"] = "forged"
	if _, e = interactionCommand(context.Background(), s, api, "authorize", bytes.NewReader(interactionFrameBytes(input))); e == nil {
		t.Fatal("plugin owner forgery")
	}
}

func TestInteractionNativeOwnerMustMatchBirth(t *testing.T) {
	s, api, _, r := interactionTestSetup(t)
	spec := InteractionSpec{Kind: "question", Fields: []InteractionField{{ID: "q0", Type: "string", Label: "Q", Required: true}}, SupportedActions: []string{"answer"}, OwnerPID: os.Getppid(), OwnerBirth: "different-runtime"}
	a := Activity{Agent: "claude", Session: "s", Run: "r", Instance: "i"}
	if _, e := interactionNativeWait(context.Background(), s, api, r.Locator, a, spec); e == nil || e.Error() != "interaction_owner_mismatch" {
		t.Fatal("untrusted native owner accepted", e)
	}
}

func TestInteractionNewGrantDoesNotLoseHintToOldCapabilityCache(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "")
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "interaction-capabilities") {
			printJSON(w, interactionTestGrant(p))
			return
		}
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			printJSON(w, activityCaps(p))
			return
		}
		var event Envelope
		if json.NewDecoder(r.Body).Decode(&event) != nil {
			t.Error("bad envelope")
		}
		if event.Kind == "needs_input" {
			posts++
		}
		printJSON(w, map[string]any{"v": 2, "event_id": event.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p.APIURL = server.URL
	s.savePair(context.Background(), p)
	stale := RunCapabilities{V: 2, Enabled: true, States: true, Inbox: true, PolicyGeneration: 2, CapabilityGeneration: 1, ExpiresAt: time.Now().Unix() + 60, Approvals: map[string]bool{}}
	cacheKey := digest(p.APIURL + "\x00" + p.PairID + "\x00" + p.InstallationID + "\x00" + p.SendToken)
	s.writeJSON("capabilities-cache-v2.json", map[string]capabilityCacheEntry{cacheKey: {Value: stale, Until: time.Now().Unix() + 60}})
	l := fixtureLocator()
	l.InstallationID = id
	r := InteractionRecord{V: 1, RequestID: randomID(), Digest: digest("{}"), Kind: "question", Agent: "claude", RequestJSON: "{}", Locator: l, State: "pending", ExpiresAt: time.Now().Unix() + 120, Pairs: map[string]int64{p.PairID: 3}}
	s.writeJSON("interaction-"+r.RequestID+".json", r)
	a := Activity{Agent: "claude", Session: "s", Run: "r", Instance: "i", Occurrence: "fresh-grant", RequestID: "native-one", State: "waiting_input", Reminder: "needs_input", Interaction: &InteractionHint{V: 1, RequestID: r.RequestID, Digest: r.Digest, Kind: r.Kind, ExpiresAt: r.ExpiresAt}}
	if e := publishActivity(context.Background(), s, API{server.Client()}, l, a); e != nil {
		t.Fatal(e)
	}
	if posts != 1 {
		t.Fatal("fresh interaction grant was downgraded by stale observation cache", posts)
	}
}

func TestInteractionNumericConditionUsesNativeValueEquality(t *testing.T) {
	spec := InteractionSpec{Kind: "form", SupportedActions: []string{"answer"}, Fields: []InteractionField{{ID: "n", Type: "number", Label: "N", Required: true}, {ID: "text", Type: "string", Label: "Text", Required: true, When: []InteractionCondition{{FieldID: "n", Op: "eq", Value: json.RawMessage(`1.0`)}}}}}
	if e := interactionAnswers(spec, "answer", `{"answers":{"n":1,"text":"active"}}`); e != nil {
		t.Fatal(e)
	}
	if e := interactionAnswers(spec, "answer", `{"answers":{"n":1}}`); e == nil {
		t.Fatal("numeric lexical difference hid a required field")
	}
}
