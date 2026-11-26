package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplyWireVector(t *testing.T) {
	raw, err := os.ReadFile(testdataDir + "/reply-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Key       string           `json:"key_base64"`
		Op        string           `json:"operation"`
		Frame     replyFrame       `json:"frame"`
		Configure replyConfigFrame `json:"configure"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	key, err := decode64(v.Key, 32)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := replyMAC(key, v.Op, v.Frame)
	if err != nil || mac != v.Frame.Proof || digest(v.Frame.Text) != v.Frame.ResponseDigest {
		t.Fatalf("wire vector differs: %v, %v", err, mac == v.Frame.Proof)
	}
	configMAC, e := v.Configure.mac(key)
	if e != nil || configMAC != v.Configure.Proof {
		t.Fatal("configuration proof vector differs", e)
	}
	for _, op := range []string{"inspect", "status", "configure"} {
		m, _ := replyMAC(key, op, v.Frame)
		if m == mac {
			t.Fatal("operation not bound")
		}
	}
	for _, mutate := range []func(*replyFrame){func(f *replyFrame) { f.Nonce += "x" }, func(f *replyFrame) { f.SourceID += "x" }, func(f *replyFrame) { f.Cursor += "x" }, func(f *replyFrame) { f.Origin += "x" }, func(f *replyFrame) { f.Installation += "x" }, func(f *replyFrame) { f.Envelope += "x" }, func(f *replyFrame) { f.DecisionID += "x" }, func(f *replyFrame) { f.ResponseDigest += "x" }, func(f *replyFrame) { f.Agent = "claude" }, func(f *replyFrame) { f.PolicyGeneration++ }, func(f *replyFrame) { f.Expires++ }} {
		other := v.Frame
		mutate(&other)
		m, _ := replyMAC(key, v.Op, other)
		if m == mac {
			t.Fatal("unsigned field")
		}
	}
}

type replyFixture struct {
	s      Store
	api    API
	pairs  []Pair
	frames []replyFrame
	record replyRecord
	l      Locator
	grant  *atomic.Int64
}

func replyTestConfig(p Pair, generation, revision int64, enabled bool) replyConfigFrame {
	f := replyConfigFrame{V: 1, Agent: "pi", Pair: p.PairID, Enabled: &enabled, Revision: 1, Generation: generation, Origin: p.APIURL, Installation: p.InstallationID, Nonce: randomID(), Expires: time.Now().Unix() + 30, Expected: &revision}
	key, _ := decode64(p.EncryptionKey, 32)
	f.Proof, _ = f.mac(key)
	return f
}

func newReplyFixture(t *testing.T) replyFixture {
	return newReplyFixtureWithStorage(t, "")
}

func newReplyFixtureWithStorage(t *testing.T, storage string) replyFixture {
	t.Helper()
	ctx := context.Background()
	claudeNotifyEnvironment(t)
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	gen := &atomic.Int64{}
	gen.Store(3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		parts := strings.Split(req.URL.Path, "/")
		if len(parts) < 4 {
			t.Error("unexpected fixture route")
			return
		}
		p := testPair(id, "")
		p.PairID = parts[3]
		if strings.HasSuffix(req.URL.Path, "/interaction-capabilities") {
			g := interactionTestGrant(p)
			g.PolicyGeneration = gen.Load()
			printJSON(w, g)
			return
		}
		if req.URL.Path == "/v1/pairs/"+p.PairID {
			printJSON(w, map[string]any{"pair_id": p.PairID, "installation_id": id, "status": "active"})
			return
		}
		t.Error("unexpected fixture route")
	}))
	t.Cleanup(server.Close)
	api := API{server.Client()}
	p := testPair(id, server.URL)
	q := p
	q.PairID = randomID()
	fx := replyFixture{s: s, api: api, pairs: []Pair{p, q}, l: l, grant: gen}
	for _, p := range fx.pairs {
		if err = s.savePair(ctx, p); err != nil {
			t.Fatal(err)
		}
		cfg := replyTestConfig(p, 3, 0, true)
		if _, err = replyCommand(ctx, s, api, "configure", bytes.NewReader(interactionFrameBytes(cfg))); err != nil {
			t.Fatal(err)
		}
	}
	input := replyWaitInput{"1.5.2", "original_session", "original_run", "original_event", "activation_id", time.Now().Unix() + 295, storage, "native-assistant-entry"}
	// Registration precedes the completion observation; it must not emit a notification itself.
	unlock, err := s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fx.record, err = replyRegistration(s, "pi", input, l, storage != "")
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(s)
	if err != nil || len(db.Intents) != 0 {
		t.Fatal("registration emitted an activity")
	}
	a := Activity{NativeStorage: storage, Agent: "pi", Session: input.Session, Run: input.Run, Instance: input.Instance, Occurrence: fx.record.Cursor, State: "turn_completed", Reminder: "turn_completed", Body: assistantBody{TooLarge: true}}
	_, _, _, err = applyActivity(ctx, s, l, a)
	if err != nil {
		t.Fatal(err)
	}
	db, err = loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range fx.pairs {
		ik := digest(pair.PairID + "\x00" + a.Occurrence)
		intent, err := encodeRunIntent(ctx, s, ik, db.Intents[ik], RunCapabilities{CapabilityGeneration: 1}, pair)
		if err != nil {
			t.Fatal(err)
		}
		d, err := enqueueRunReminder(ctx, s, intent)
		if err != nil {
			t.Fatal(err)
		}
		if d.Reply == nil || d.Reply.SourceID != fx.record.SourceID || d.Body == nil || d.Body.Ref.Status != "too_large" {
			t.Fatal("original source did not reach outbox independently")
		}
		fx.frames = append(fx.frames, replyFrame{bodyIdentity: bodyIdentity{1, pair.APIURL, id, pair.PairID, d.EventID, d.Reply.Envelope, randomID()}, Expires: time.Now().Unix() + 50})
	}
	return fx
}
func replyRequest(ctx context.Context, s Store, api API, p Pair, op string, f replyFrame) (replyResult, error) {
	f.Nonce = randomID()
	key, _ := decode64(p.EncryptionKey, 32)
	f.Proof, _ = replyMAC(key, op, f)
	v, err := replyCommand(ctx, s, api, op, bytes.NewReader(interactionFrameBytes(f)))
	if err != nil {
		return replyResult{}, err
	}
	return v.(replyResult), nil
}
func replySendFrame(f replyFrame, r replyRecord, text string) replyFrame {
	f.SourceID = r.SourceID
	f.Agent = r.Agent
	f.Cursor = r.Cursor
	f.Action = "send"
	f.Text = text
	f.ResponseDigest = digest(text)
	f.DecisionID = randomID()
	f.PolicyGeneration = 3
	return f
}

func TestReplyCrossPairClaimAndReceipts(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	for i, p := range fx.pairs {
		got, err := replyRequest(ctx, fx.s, fx.api, p, "inspect", fx.frames[i])
		if err != nil || got.State != "eligible" || got.OpportunityExpires != fx.record.Expires || got.Expires > got.OpportunityExpires {
			t.Fatalf("inspect %v %#v", err, got)
		}
	}
	var wg sync.WaitGroup
	states := make([]string, 2)
	frames := make([]replyFrame, 2)
	for i, p := range fx.pairs {
		frames[i] = replySendFrame(fx.frames[i], fx.record, []string{"  第一条🧑‍💻\n$(echo ignored)  ", "另一决定"}[i])
		wg.Go(func() {
			r, err := replyRequest(ctx, fx.s, fx.api, p, "send", frames[i])
			if err != nil {
				t.Error(err)
				return
			}
			states[i] = r.State
			if r.State == "other_decision" && (r.Decision != "" || r.Digest != "") {
				t.Error("foreign decision disclosed")
			}
		})
	}
	wg.Wait()
	win := 0
	if states[1] == "decision_recorded" {
		win = 1
	}
	if states[win] != "decision_recorded" || states[1-win] != "other_decision" {
		t.Fatal(states)
	}
	p := fx.pairs[win]
	f := frames[win]
	changed := f
	changed.Text += "change"
	changed.ResponseDigest = digest(changed.Text)
	if _, err := replyRequest(ctx, fx.s, fx.api, p, "send", changed); err == nil {
		t.Fatal("same ID different bytes")
	}
	r, err := readReply(fx.s, fx.record.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := fx.s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := claimReplyLocked(fx.s, r, p, interactionTestGrant(p))
	unlock()
	if err != nil || out.(map[string]any)["text"] != f.Text {
		t.Fatal("claim", err)
	}
	if _, err = claimReplyLocked(fx.s, r, p, interactionTestGrant(p)); err == nil {
		t.Fatal("duplicate dispatch")
	}
	owner := replyOwnerFrame{V: 1, Source: r.SourceID, Owner: r.OwnerToken, Decision: f.DecisionID}
	auth := func() bool {
		v, err := replyNativeCommand(ctx, fx.s, fx.api, "authorize", bytes.NewReader(interactionFrameBytes(owner)))
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)["allowed"].(bool)
	}
	if !auth() || auth() {
		t.Fatal("native authorization token was not single-use")
	}
	owner.State = "delivery_unknown"
	if _, err = replyNativeCommand(ctx, fx.s, fx.api, "receipt", bytes.NewReader(interactionFrameBytes(owner))); err != nil {
		t.Fatal(err)
	}
	r, _ = readReply(fx.s, r.SourceID)
	if r.Frame.Text != "" || r.State != "delivery_unknown" {
		t.Fatal("claim retained text or changed dispatch state")
	}
	// Exit the owner and change the cloud policy. The original receipt stays queryable.
	r.ParentBirth = "reused"
	if err = saveReply(fx.s, r); err != nil {
		t.Fatal(err)
	}
	fx.grant.Store(4)
	f.Text = ""
	// A pre-upgrade receipt has no logical scope. A status query still owns
	// the frozen decision even when the original packet is gone.
	r, err = readReply(fx.s, fx.record.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	r.LogicalSession = ""
	r.VisibleSession = ""
	if err = saveReply(fx.s, r); err != nil {
		t.Fatal(err)
	}
	duplicate := fx.record
	duplicate.SourceID = digest("registration-with-a-new-scope")
	duplicate.State = "pending"
	duplicate.Frame = nil
	if err = registerReplyLocked(fx.s, duplicate); err == nil || err.Error() != "reply_already_registered" {
		t.Fatal("legacy frozen decision did not block duplicate registration", err)
	}

	// Outbox expiry must not discard responsibility while its replay record survives.
	if err = os.Rename(filepath.Join(fx.s.Dir, "outbox-v1"), filepath.Join(fx.s.Dir, "pruned-test-outbox")); err != nil {
		t.Fatal(err)
	}
	got, err := replyRequest(ctx, fx.s, fx.api, p, "status", f)
	if err != nil || got.State != "delivery_unknown" || got.Decision != f.DecisionID {
		t.Fatal(got, err)
	}
	if err = os.Rename(filepath.Join(fx.s.Dir, "pruned-test-outbox"), filepath.Join(fx.s.Dir, "outbox-v1")); err != nil {
		t.Fatal(err)
	}
	got, err = replyRequest(ctx, fx.s, fx.api, p, "send", frames[win])
	if err != nil || got.State != "delivery_unknown" {
		t.Fatal("new lease reopened dispatch")
	}
}

func TestReplyGateTracksOriginalSourceAndNativeRequest(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	r := fx.record
	p := fx.pairs[0]
	a := Activity{Agent: r.Agent, Session: r.Session, Instance: r.Instance, Run: "next-run", State: "starting", Occurrence: digest("user-input")}
	_, _, _, err := applyActivity(ctx, fx.s, fx.l, a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := replyRequest(ctx, fx.s, fx.api, p, "inspect", fx.frames[0])
	if err != nil || got.Reason != "source_changed" {
		t.Fatal(got, err)
	}
	db, _ := loadRuns(fx.s)
	old := Activity{Agent: r.Agent, Session: r.Session, Instance: r.Instance, Run: r.Run, State: "turn_completed", Reminder: "turn_completed", Occurrence: r.Cursor}
	if advanceReplyHead(&db, old, replyScope(fx.l.InstallationID, r.LogicalSession), time.Now().Unix()) {
		t.Fatal("late completion reclaimed newer run")
	}
	tool := a
	tool.State = "running"
	tool.ToolID = "tool"
	tool.Occurrence = digest("tool heartbeat")
	if advanceReplyHead(&db, tool, replyScope(fx.l.InstallationID, r.LogicalSession), time.Now().Unix()) {
		t.Fatal("heartbeat became a source")
	}
	req := a
	req.Reminder = "needs_input"
	req.State = "waiting_input"
	req.Interaction = &InteractionHint{RequestID: randomID()}
	req.Occurrence = digest("question")
	if !advanceReplyHead(&db, req, replyScope(fx.l.InstallationID, r.LogicalSession), time.Now().Unix()) || saveRuns(fx.s, db) != nil {
		t.Fatal("request not registered")
	}
	if !nativeRequestCurrent(fx.s, r.Agent, r.Session, req.Interaction.RequestID, fx.l, r.LogicalSession) {
		t.Fatal("current request blocked")
	}
	newer := old
	newer.Run = req.Run
	newer.Occurrence = digest("M2")
	if !advanceReplyHead(&db, newer, replyScope(fx.l.InstallationID, r.LogicalSession), time.Now().Unix()) || saveRuns(fx.s, db) != nil {
		t.Fatal("new completion not registered")
	}
	if nativeRequestCurrent(fx.s, r.Agent, r.Session, req.Interaction.RequestID, fx.l, r.LogicalSession) {
		t.Fatal("old wire retained new decision authority")
	}
}

func TestReplyActivationExpiryPrivacyAndLimits(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	p := fx.pairs[0]
	f := fx.frames[0]
	fx.grant.Store(5)
	got, err := replyRequest(ctx, fx.s, fx.api, p, "inspect", f)
	if err != nil || got.Reason != "activation_required" {
		t.Fatal(got, err)
	}
	cfg := replyTestConfig(p, 5, 1, true)
	if _, err = replyCommand(ctx, fx.s, fx.api, "configure", bytes.NewReader(interactionFrameBytes(cfg))); err != nil {
		t.Fatal(err)
	}
	got, err = replyRequest(ctx, fx.s, fx.api, p, "inspect", f)
	if err != nil || got.Reason != "activation_changed" {
		t.Fatal("old opportunity resurrected", got, err)
	}
	r := fx.record
	r.Expires = time.Now().Unix() - 1
	if err = saveReply(fx.s, r); err != nil {
		t.Fatal(err)
	}
	fx.grant.Store(3)
	for range 2 {
		got, err = replyRequest(ctx, fx.s, fx.api, fx.pairs[1], "inspect", fx.frames[1])
		if err != nil || got.Reason != "opportunity_expired" {
			t.Fatal(got, err)
		}
	}
	if info, err := os.Stat(filepath.Join(fx.s.Dir, replyFile(r.SourceID))); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe record")
	}
	if err = os.Symlink(filepath.Join(fx.s.Dir, replyFile(r.SourceID)), filepath.Join(fx.s.Dir, replyFile(digest("link")))); err != nil {
		t.Fatal(err)
	}
	if _, err = readReply(fx.s, digest("link")); err == nil {
		t.Fatal("followed symlink")
	}
	text := strings.Repeat("\x01", replyTextLimit)
	full := replySendFrame(f, r, text)
	key, _ := decode64(p.EncryptionKey, 32)
	full.Proof, _ = replyMAC(key, "send", full)
	var out replyFrame
	if err = readStrictFrame(ctx, bytes.NewReader(interactionFrameBytes(full)), &out, 128<<10); err != nil || verifyReply("send", out, p) != nil {
		t.Fatal("escaped boundary text", err)
	}
	full.Text += "x"
	full.ResponseDigest = digest(full.Text)
	full.Proof, _ = replyMAC(key, "send", full)
	if verifyReply("send", full, p) == nil {
		t.Fatal("oversize permitted")
	}
}

func TestReplyCommandsExerciseImmutableSourceAndNativeClaim(t *testing.T) {
	fx := newReplyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Setenv("MOBTERM_CONFIG_DIR", fx.s.Dir)
	var out, diag bytes.Buffer
	if code := run([]string{"reply", "configuration", "--stdin-frame"}, bytes.NewReader(interactionFrameBytes(map[string]any{"v": 1, "pair_id": fx.pairs[0].PairID})), &out, &diag); code != 0 {
		t.Fatal(code, diag.String())
	}
	var conf struct {
		Activations map[string]int64 `json:"activations"`
	}
	if err := json.Unmarshal(out.Bytes(), &conf); err != nil || conf.Activations["pi"] != 3 {
		t.Fatal("CLI did not report configured generation", err)
	}
	input := replyWaitInput{"1.5.1", "second-session", "second-run", "second-event", "second-activation", time.Now().Unix() + 100, "", ""}
	p := fx.pairs[0]
	l := fx.l
	logical, err := logicalSession("pi", input.Session)
	if err != nil {
		t.Fatal(err)
	}
	cursor := digest(logical + "\x00" + newReplyOccurrence("pi", "agent_settled", input.Session, input.Event, input.Instance))
	parentBirth, err := processIdentity(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	source := replySourceID(replyScope(l.InstallationID, logical), input.Run, input.Instance, cursor, parentBirth)
	raw, _ := json.Marshal(input)
	done := make(chan struct{})
	var decision any
	var waitErr error
	go func() {
		defer close(done)
		decision, waitErr = replyWait(ctx, fx.s, fx.api, "pi", bytes.NewReader(raw))
	}()
	defer func() { cancel(); <-done }()
	for {
		if _, err = readReply(fx.s, source); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native waiter did not register")
		case <-time.After(10 * time.Millisecond):
		}
	}
	a := Activity{Agent: "pi", Session: input.Session, Run: input.Run, Instance: input.Instance, Occurrence: cursor, State: "turn_completed", Reminder: "turn_completed"}
	_, _, _, err = applyActivity(ctx, fx.s, l, a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(fx.s)
	if err != nil {
		t.Fatal(err)
	}
	ik := digest(p.PairID + "\x00" + cursor)
	intent, err := encodeRunIntent(ctx, fx.s, ik, db.Intents[ik], RunCapabilities{CapabilityGeneration: 1}, p)
	if err != nil {
		t.Fatal(err)
	}
	d, err := enqueueRunReminder(ctx, fx.s, intent)
	if err != nil {
		t.Fatal(err)
	}
	f := replyFrame{bodyIdentity: bodyIdentity{1, p.APIURL, p.InstallationID, p.PairID, d.EventID, d.Reply.Envelope, randomID()}, Expires: time.Now().Unix() + 30}
	r, err := readReply(fx.s, source)
	if err != nil {
		t.Fatal(err)
	}
	f = replySendFrame(f, r, "  literal\n/thing ${quoted}  ")
	res, err := replyRequest(ctx, fx.s, fx.api, p, "send", f)
	if err != nil || res.State != "decision_recorded" {
		t.Fatal(res, err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("waiter did not return")
	}
	if waitErr != nil || decision.(map[string]any)["text"] != f.Text {
		t.Fatal("native decision bytes changed", waitErr)
	}
	// A native request appears between the durable claim and final native authorization.
	a.Run = "next"
	a.Occurrence = digest("question")
	a.Reminder = "needs_input"
	a.State = "waiting_input"
	a.Interaction = &InteractionHint{RequestID: randomID()}
	a.RequestID = a.Interaction.RequestID
	_, _, _, err = applyActivity(ctx, fx.s, l, a)
	if err != nil {
		t.Fatal(err)
	}
	owner := replyOwnerFrame{V: 1, Source: r.SourceID, Owner: r.OwnerToken, Decision: f.DecisionID}
	v, err := replyNativeCommand(ctx, fx.s, fx.api, "authorize", bytes.NewReader(interactionFrameBytes(owner)))
	if err != nil || v.(map[string]any)["allowed"] != false {
		t.Fatal("stale dispatch received approval", err)
	}
	owner.State = "not_dispatched"
	_, err = replyNativeCommand(ctx, fx.s, fx.api, "receipt", bytes.NewReader(interactionFrameBytes(owner)))
	if err != nil {
		t.Fatal(err)
	}
	r, err = readReply(fx.s, source)
	if err != nil || r.State != "not_dispatched" || r.Frame.Text != "" {
		t.Fatal("definitive proof was not persisted")
	}
	other := replySendFrame(f, r, "different")
	if _, err = replyRequest(ctx, fx.s, fx.api, p, "send", other); err == nil {
		t.Fatal("negative receipt reopened source")
	}
}

func TestReplyStoreFailureAndCodexFallback(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	p := fx.pairs[0]
	f := replySendFrame(fx.frames[0], fx.record, "frozen")
	res, err := replyRequest(ctx, fx.s, fx.api, p, "send", f)
	if err != nil || res.State != "decision_recorded" {
		t.Fatal(res, err)
	}
	r, err := readReply(fx.s, fx.record.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	// Alter the original owner at the last durable claim. No bytes escape.
	expected := r
	expected.OwnerToken = randomID()
	if data, err := claimReplyLocked(fx.s, expected, p, interactionTestGrant(p)); err == nil || data != nil {
		t.Fatal("disclosed bytes after owner change")
	}
	// A symlink injected at the persistence destination must fail before output.
	path := filepath.Join(fx.s.Dir, replyFile(r.SourceID))
	backup := path + ".backup"
	if err = os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(backup, path); err != nil {
		t.Fatal(err)
	}
	if data, err := claimReplyLocked(fx.s, r, p, interactionTestGrant(p)); err == nil || data != nil {
		t.Fatal("dispatched with unsafe record")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	// A loaded logical Codex thread, even on a live target, never acquires a reply grant.
	a := Activity{Agent: "codex", Session: "shared", Run: "turn", Instance: "shared", Occurrence: digest("codex-turn"), Reminder: "turn_completed", State: "turn_completed"}
	b := replyIntentBinding(a, fx.l, p, "")
	d, err := enqueueBoundDelivery(ctx, fx.s, p, a.Reminder, a.Occurrence, fx.l, a.Agent, "", nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	f = replyFrame{bodyIdentity: bodyIdentity{1, p.APIURL, p.InstallationID, p.PairID, d.EventID, d.Reply.Envelope, randomID()}, Expires: time.Now().Unix() + 30}
	res, err = replyRequest(ctx, fx.s, fx.api, p, "inspect", f)
	if err != nil || res.State != "unavailable" || res.Reason != "ordinary_reply_unavailable" {
		t.Fatal(res, err)
	}
}

func TestReplyConfigurationProofAndSameGenerationReplay(t *testing.T) {
	fx := newReplyFixture(t)
	p := fx.pairs[0]
	update := func(f replyConfigFrame) error {
		_, err := replyCommand(context.Background(), fx.s, fx.api, "configure", bytes.NewReader(interactionFrameBytes(f)))
		return err
	}
	oldEnable := replyTestConfig(p, 3, 1, true)
	disable := replyTestConfig(p, 3, 1, false)
	forged := disable
	forged.Proof = oldEnable.Proof
	if err := update(forged); err == nil {
		t.Fatal("unsigned change accepted")
	}
	if err := update(disable); err != nil {
		t.Fatal(err)
	}
	if err := update(oldEnable); err == nil {
		t.Fatal("old same-generation enable revived disabled activation")
	}
	fresh := replyTestConfig(p, 3, 2, true)
	if err := update(fresh); err != nil {
		t.Fatal(err)
	}
	if err := update(fresh); err == nil {
		t.Fatal("configure nonce replay")
	}
	got, err := replyRequest(context.Background(), fx.s, fx.api, p, "inspect", fx.frames[0])
	if err != nil || got.Reason != "activation_changed" {
		t.Fatal("restored old opportunity", got, err)
	}
}

// Drive the real producer and receiver independently. In particular no test
// computes a helper ID and injects that value into the notification.
func TestReplyNativeCompletionRendezvous(t *testing.T) {
	for _, tc := range []struct {
		agent, version string
		helperFirst    bool
	}{
		{"claude", "2.1.326", true}, {"claude", "2.1.328", false},
		{"opencode", "2.3.2", true}, {"pi", "1.5.2", false},
	} {
		name := tc.agent + "/after-notification"
		if tc.helperFirst {
			name = tc.agent + "/before-notification"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			claudeNotifyEnvironment(t)
			s := testStore(t)
			id, err := s.installation()
			if err != nil {
				t.Fatal(err)
			}
			l, err := captureTarget(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			copies := map[string][]Envelope{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				parts := strings.Split(req.URL.Path, "/")
				if len(parts) < 4 {
					t.Error("bad request path")
					return
				}
				p := testPair(id, "")
				p.PairID = parts[3]
				if strings.HasSuffix(req.URL.Path, "/interaction-capabilities") {
					printJSON(w, interactionTestGrant(p))
					return
				}
				if strings.HasSuffix(req.URL.Path, "/capabilities") {
					printJSON(w, activityCaps(p))
					return
				}
				var e Envelope
				if json.NewDecoder(req.Body).Decode(&e) != nil {
					t.Error("invalid envelope")
					return
				}
				if strings.Contains(req.URL.Path, "/snapshots") {
					printJSON(w, map[string]any{"v": 2, "stream_id": e.StreamID, "revision": e.Revision})
					return
				}
				mu.Lock()
				copies[p.PairID] = append(copies[p.PairID], e)
				mu.Unlock()
				printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			q := p
			q.PairID = randomID()
			api := API{server.Client()}
			for _, pair := range []Pair{p, q} {
				if err = s.savePair(ctx, pair); err != nil {
					t.Fatal(err)
				}
				config := replyTestConfig(pair, 3, 0, true)
				config.Agent = tc.agent
				key, _ := decode64(pair.EncryptionKey, 32)
				config.Proof, _ = config.mac(key)
				if _, err = replyCommand(ctx, s, api, "configure", bytes.NewReader(interactionFrameBytes(config))); err != nil {
					t.Fatal(err)
				}
			}
			f := replyWaitInput{tc.version, "fixture-session", "fixture-run", "fixture-event", "fixture-activation", time.Now().Unix() + 280, "", ""}
			emit := func(payload map[string]any) {
				t.Helper()
				raw, _ := json.Marshal(payload)
				if _, e := observe(ctx, s, api, tc.agent, raw); e != nil {
					t.Fatal(e)
				}
			}
			var finish func()
			switch tc.agent {
			case "claude":
				f.Run = "fixture-prompt"
				f.Event = f.Run
				claude := func(hook string) {
					emit(map[string]any{"hook_event_name": hook, "session_id": f.Session, "prompt_id": f.Run, "notification_type": "idle_prompt"})
				}
				claude("UserPromptSubmit")
				finish = func() { claude("Stop"); claude("Notification"); claude("Notification") }
			case "pi", "opencode":
				event, start := "agent_settled", "agent_start"
				if tc.agent == "opencode" {
					event, start = "session.execution.succeeded", "session.execution.started"
				}
				packet := map[string]any{"runtime_version": tc.version, "adapter_revision": 2, "session_id": f.Session, "run_id": f.Run, "instance_id": f.Instance, "event_id": "start", "event": start}
				emit(packet)
				packet["event_id"] = f.Event
				packet["event"] = event
				if tc.agent == "pi" {
					packet["aborted"] = false
				}
				finish = func() { emit(packet); emit(packet) }
			}
			if !tc.helperFirst {
				finish()
			}
			unlock, err := s.lock(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r, err := replyRegistration(s, tc.agent, f, l, false)
			unlock()
			if err != nil {
				t.Fatal(err)
			}
			if tc.helperFirst {
				finish()
			}
			for range 3 {
				if e := flushRunSnapshots(ctx, s, api); e != nil {
					t.Fatal(e)
				}
			}
			mu.Lock()
			receipts := map[string][]Envelope{}
			for k, v := range copies {
				receipts[k] = append([]Envelope{}, v...)
			}
			mu.Unlock()
			for _, pair := range []Pair{p, q} {
				es := receipts[pair.PairID]
				if len(es) != 1 || es[0].Kind != "turn_completed" {
					t.Fatalf("unexpected reminders for %s: %d", tc.agent, len(es))
				}
				e := es[0]
				f := replyFrame{bodyIdentity: bodyIdentity{1, pair.APIURL, id, pair.PairID, e.EventID, envelopeBodyDigest(e), randomID()}, Expires: time.Now().Unix() + 30}
				got, err := replyRequest(ctx, s, api, pair, "inspect", f)
				if err != nil || got.State != "eligible" || got.SourceID != r.SourceID || got.Cursor != r.Cursor {
					t.Fatalf("mismatched native source: state=%s reason=%s err=%v", got.State, got.Reason, err)
				}
			}
			if receipts[p.PairID][0].EventID == receipts[q.PairID][0].EventID {
				t.Fatal("Pair copies reused event IDs")
			}
		})
	}
}

func TestVisibleNoticeOrderAndAudienceAtSSHEntry(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	check := func(pairIndex int, want string) {
		t.Helper()
		result, err := replyRequest(ctx, fx.s, fx.api, fx.pairs[pairIndex], "latest", fx.frames[pairIndex])
		if err != nil || result.State != "unavailable" || result.Reason != want {
			t.Fatalf("pair %d: %s, %v", pairIndex, result.Reason, err)
		}
	}
	check(0, "navigation_binding_unconfirmed")
	check(1, "navigation_binding_unconfirmed")
	p := fx.pairs[0]
	a := Activity{Agent: "pi", Session: fx.record.Session, Instance: fx.record.Instance, Run: "later_run", Occurrence: "later_occurrence", State: "turn_completed", Reminder: "turn_completed"}
	if _, _, _, err := applyActivity(ctx, fx.s, fx.l, a); err != nil {
		t.Fatal(err)
	}
	db, err := loadRuns(fx.s)
	if err != nil {
		t.Fatal(err)
	}
	key := digest(p.PairID + "\x00" + a.Occurrence)
	intent := db.Intents[key]
	// An observation awaiting audience policy cannot replace a visible reminder.
	check(0, "navigation_binding_unconfirmed")
	if _, err = encodeRunIntent(ctx, fx.s, key, intent, RunCapabilities{CapabilityGeneration: 1}, p); err != nil {
		t.Fatal(err)
	}
	check(0, "source_binding_missing")
	check(1, "navigation_binding_unconfirmed")
	previous := fx.frames[0]
	result, err := replyRequest(ctx, fx.s, fx.api, p, "inspect", previous)
	if err != nil || result.State != "unavailable" {
		t.Fatalf("old reply: %+v %v", result, err)
	}
}
