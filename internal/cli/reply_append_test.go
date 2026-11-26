package cli

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
	"sync/atomic"
	"testing"
	"time"
)

func TestAppendWireVector(t *testing.T) {
	raw, err := os.ReadFile(testdataDir + "/reply-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Key       string `json:"key_base64"`
		Frame     replyFrame
		Configure replyConfigFrame
		Inspect   string `json:"inspect_proof"`
		Status    string `json:"status_proof"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	key, _ := decode64(v.Key, 32)
	mac, _ := replyMAC(key, "send", v.Frame)
	if mac != v.Frame.Proof || digest(v.Frame.Text) != v.Frame.ResponseDigest {
		t.Fatal("send vector")
	}
	mac, _ = v.Configure.mac(key)
	if mac != v.Configure.Proof {
		t.Fatal("configuration vector")
	}
	f := v.Frame
	f.Text = ""
	mac, _ = replyMAC(key, "status", f)
	if mac != v.Status {
		t.Fatal("status vector")
	}
	f = replyFrame{bodyIdentity: f.bodyIdentity, Agent: "codex", Mode: appendMode, Expires: f.Expires}
	mac, _ = replyMAC(key, "inspect", f)
	if mac != v.Inspect {
		t.Fatal("inspect vector")
	}
}

const appendThreadFixture = "01980001-aaaa-7001-8000-000000000001"
const appendTurnFixture = "01980001-aaaa-7001-8000-000000000002"

func TestAppendStableSlotRetentionAndIsolation(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	rt := codexRuntimeSource{Installation: id, Home: t.TempDir(), Socket: "/fixture/socket", UID: os.Geteuid(), PeerPID: 22, PeerBirth: "birth"}
	now := time.Now().Unix()
	first, err := registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, now, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, now, now+1800)
	if err != nil || first != again {
		t.Fatal("retry renewed or lost slot", err)
	}
	changed := rt
	changed.PeerBirth += "restart"
	if _, err = registerAppendSource(s, changed, appendThreadFixture, appendTurnFixture, now, now); err == nil {
		t.Fatal("runtime rebound")
	}
	changed.Home = t.TempDir()
	other, err := registerAppendSource(s, changed, appendThreadFixture, appendTurnFixture, now, now)
	if err != nil || other == first {
		t.Fatal("namespace collision", err)
	}
	if _, err = registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, now, now+appendSourceTTL); err == nil {
		t.Fatal("source age renewed")
	}
	if _, err = registerAppendSource(s, rt, appendTurnFixture, appendThreadFixture, now+appendSourceTTL, now+appendSourceTTL); err != nil {
		t.Fatal(err)
	}
	if _, err = registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, now+appendSourceTTL, now+appendSourceTTL); err == nil {
		t.Fatal("replay after GC")
	}
	path := filepath.Join(s.Dir, "reply-append-v2.json")
	for _, bad := range []string{`{"v":7,"sources":{},"retired":{}}`, `{"v":2,"sources":{"bad":null},"retired":{}}`, "{"} {
		if err = os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = loadAppendStore(s); err == nil {
			t.Fatal("corrupt schema accepted")
		}
		out, _ := os.ReadFile(path)
		if string(out) != bad {
			t.Fatal("corrupt responsibility reset")
		}
	}
}

// Exercise encrypted notification fan-out and both Pair claims under the same
// writer lock used by independent SSH exec processes. Native ownership is the
// already-verified input to prepareAppend; real socket I/O has separate tests.
func TestAppendNotificationClaimAndQuery(t *testing.T) {
	fx := newReplyFixture(t)
	ctx := context.Background()
	now := time.Now().Unix()
	created := now - 1800
	rt := codexRuntimeSource{Installation: fx.l.InstallationID, Home: t.TempDir(), Socket: "/fixture/socket", UID: os.Geteuid(), PeerPID: 22, PeerBirth: "birth"}
	a := Activity{Agent: "codex", Session: appendThreadFixture, Run: appendTurnFixture, Instance: digest("incarnation"), Occurrence: digest("occurrence"), State: "turn_completed", Reminder: "turn_completed", CodexRoot: true, CodexRuntime: &rt, appendCreated: &created}
	if _, _, _, err := applyActivity(ctx, fx.s, Locator{}, a); err != nil {
		t.Fatal(err)
	}
	runs, err := loadRuns(fx.s)
	if err != nil {
		t.Fatal(err)
	}
	setConfig := func(p Pair, revision int64, enabled bool) {
		t.Helper()
		f := replyTestConfig(p, 3, revision, enabled)
		f.V, f.Mode, f.Agent = 2, appendMode, "codex"
		key, _ := decode64(p.EncryptionKey, 32)
		f.Proof, _ = f.mac(key)
		if _, err := replyConfiguration(ctx, fx.s, fx.api, "configure", bytes.NewReader(interactionFrameBytes(f))); err != nil {
			t.Fatal(err)
		}
	}
	frames := make([]replyFrame, 2)
	source := ""
	for i, p := range fx.pairs {
		// Pair A explicitly enabled the pre-existing async follow-up scope.
		// Pair B starts with both local scopes off.
		if i == 0 {
			asyncFrame := map[string]any{"v": 1, "pair_id": p.PairID, "agent": "codex", "kind": "followupMessage", "enabled": true, "policy_generation": 3}
			if _, err = interactionConfigure(ctx, fx.s, fx.api, bytes.NewReader(interactionFrameBytes(asyncFrame))); err != nil {
				t.Fatal(err)
			}
		}
		key := digest(p.PairID + "\x00" + a.Occurrence)
		intent, err := encodeRunIntent(ctx, fx.s, key, runs.Intents[key], RunCapabilities{CapabilityGeneration: 1}, p)
		if err != nil || intent.AppendSource == "" {
			t.Fatal("missing verified source", err)
		}
		if source != "" && source != intent.AppendSource {
			t.Fatal("fanout split source")
		}
		source = intent.AppendSource
		d, err := enqueueRunReminder(ctx, fx.s, intent)
		if err != nil {
			t.Fatal(err)
		}
		f := replyFrame{bodyIdentity: bodyIdentity{2, p.APIURL, p.InstallationID, p.PairID, d.EventID, envelopeBodyDigest(*intent.Envelope), randomID()}, Mode: appendMode, Agent: "codex", Expires: now + 30}
		v, err := replyRequest(ctx, fx.s, fx.api, p, "inspect", f)
		if err != nil || v.Reason != "activation_required" {
			t.Fatal("old cloud/async authorized append", v.Reason, err)
		}
		setConfig(p, 0, true)
		cfg, err := replyConfiguration(ctx, fx.s, fx.api, "configuration", bytes.NewReader(interactionFrameBytes(map[string]any{"v": 2, "mode": appendMode, "pair_id": p.PairID})))
		if err != nil || cfg.(map[string]any)["async_enabled"] != (i == 0) {
			t.Fatal("append enable changed async scope", err)
		}
		v, _, err = prepareAppend(fx.s, p, "inspect", f, interactionTestGrant(p), rt, replyResult{SourceID: source, OpportunityExpires: created + appendSourceTTL})
		if err != nil || v.State != "eligible" || v.OpportunityExpires != created+appendSourceTTL {
			t.Fatal("valid old notification could not obtain lease", v.State, err)
		}
		f.SourceID, f.Lease, f.PolicyGeneration, f.ActivationRevision = source, v.Lease, 3, v.ActivationRevision
		f.Action, f.DecisionID, f.ClientID, f.Text = "send", randomID(), randomID(), "  原文 🧑‍💻\n"
		f.ResponseDigest = digest(f.Text)
		frames[i] = f
	}
	// The stable participant bindings remain valid after routine run/outbox cleanup.
	if err = os.Remove(filepath.Join(fx.s.Dir, "runs-v2.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(fx.s.Dir, "outbox-v1")); err != nil {
		t.Fatal(err)
	}
	p := fx.pairs[0]
	setConfig(p, 1, false)
	async, err := localInteractions(fx.s)
	if err != nil || async[p.PairID]["codex"]["followupMessage"] != 3 {
		t.Fatal("append disable revoked async scope", err)
	}
	setConfig(p, 2, true)
	res, claimed, err := prepareAppend(fx.s, p, "send", frames[0], interactionTestGrant(p), rt, replyResult{SourceID: source})
	if err != nil || claimed || res.Reason != "lease_expired" {
		t.Fatal("disable/reenable revived old lease", err)
	}
	inspect := replyFrame{bodyIdentity: frames[0].bodyIdentity, Agent: "codex", Mode: appendMode, Expires: now + 30}
	res, _, err = prepareAppend(fx.s, p, "inspect", inspect, interactionTestGrant(p), rt, replyResult{SourceID: source})
	if err != nil || res.State != "eligible" {
		t.Fatal(err)
	}
	frames[0].Lease, frames[0].ActivationRevision = res.Lease, res.ActivationRevision
	var wg sync.WaitGroup
	results := make([]replyResult, 2)
	winners := make([]bool, 2)
	for i := range frames {
		wg.Go(func() {
			unlock, e := fx.s.lock(ctx)
			if e != nil {
				t.Error(e)
				return
			}
			defer unlock()
			results[i], winners[i], e = prepareAppend(fx.s, fx.pairs[i], "send", frames[i], interactionTestGrant(fx.pairs[i]), rt, replyResult{SourceID: source})
			if e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if winners[0] == winners[1] {
		t.Fatal("claim did not have exactly one winner")
	}
	win, lose := 0, 1
	if winners[1] {
		win, lose = 1, 0
	}
	if results[lose].State != "other_decision" || results[lose].Decision != "" || results[lose].Digest != "" || results[lose].ClientID != "" {
		t.Fatal("loser received decision details")
	}
	f := frames[win]
	raw, err := os.ReadFile(filepath.Join(fx.s.Dir, "reply-append-v2.json"))
	if err != nil || strings.Contains(string(raw), "原文") {
		t.Fatal("stored plaintext")
	}
	saved, err := replyRequest(ctx, fx.s, fx.api, fx.pairs[win], "send", f)
	if err != nil || saved.State != "delivery_unknown" {
		t.Fatal("restart redispatched prior claim", err)
	}
	f.Text = ""
	if _, err = updateAppendEvidence(ctx, fx.s, fx.pairs[win], f, saved, "queued", "fixture-queue", "", true); err != nil {
		t.Fatal(err)
	}
	setConfig(fx.pairs[win], f.ActivationRevision, false)
	saved, err = replyRequest(ctx, fx.s, fx.api, fx.pairs[win], "status", f)
	if err != nil || saved.State != "queued" || !saved.AckConfirmed || saved.Reason != "current_state_unconfirmed" {
		t.Fatal("daemon loss destroyed durable ACK", saved.State, err)
	}
	f.ResponseDigest = digest("changed")
	if _, err = replyRequest(ctx, fx.s, fx.api, fx.pairs[win], "status", f); err == nil {
		t.Fatal("same decision with changed body accepted")
	}
}

func TestAppendCapacityPreservesActiveSources(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	rt := codexRuntimeSource{Installation: id, Home: t.TempDir(), Socket: "/fixture/socket", UID: os.Geteuid(), PeerPID: 22, PeerBirth: "birth"}
	now := time.Now().Unix()
	db, err := loadAppendStore(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < appendCapacity; i++ {
		turn := fmt.Sprintf("01980001-aaaa-7001-8000-%012x", i)
		key := appendSourceID(rt, appendThreadFixture, turn)
		db.Sources[key] = &appendSource{Runtime: rt, Thread: appendThreadFixture, Turn: turn, Created: now, Participants: map[string]appendParticipant{}}
	}
	if err = db.save(s); err != nil {
		t.Fatal(err)
	}
	if _, err = registerAppendSource(s, rt, appendTurnFixture, appendThreadFixture, now, now); err == nil || err.Error() != "append_store_capacity" {
		t.Fatal("eligible record evicted", err)
	}
	after, err := loadAppendStore(s)
	if err != nil || len(after.Sources) != appendCapacity || len(after.Retired) != 0 {
		t.Fatal("full store changed", err)
	}
}

func TestAppendInputValidation(t *testing.T) {
	p := testPair("install_fixture_0000000000000001", "https://fixture.invalid")
	now := time.Now().Unix()
	f := replyFrame{bodyIdentity: bodyIdentity{2, p.APIURL, p.InstallationID, p.PairID, randomID(), digest("envelope"), randomID()}, Mode: appendMode, Agent: "codex", Action: "send", DecisionID: randomID(), ClientID: randomID(), SourceID: digest("source"), Lease: digest("lease"), ActivationRevision: 1, PolicyGeneration: 2, Expires: now + 30}
	key, _ := decode64(p.EncryptionKey, 32)
	for _, tc := range []struct {
		text  string
		valid bool
	}{{strings.Repeat("🙂", 4096), true}, {strings.Repeat("🙂", 4096) + "x", false}, {" \n　", false}, {"\n  原文 \n", true}} {
		f.Text, f.ResponseDigest = tc.text, digest(tc.text)
		f.Proof, _ = replyMAC(key, "send", f)
		if (verifyAppend("send", f, p) == nil) != tc.valid {
			t.Fatal("literal body validity changed")
		}
	}
	for _, mutate := range []func(*replyFrame){func(v *replyFrame) { v.Mode = "ordinary" }, func(v *replyFrame) { v.Lease = digest("other") }, func(v *replyFrame) { v.ClientID = randomID() }, func(v *replyFrame) { v.ActivationRevision++ }, func(v *replyFrame) { v.Nonce = randomID() }, func(v *replyFrame) { v.V = 1 }} {
		v := f
		mutate(&v)
		if verifyAppend("send", v, p) == nil || verifyReply("send", v, p) == nil {
			t.Fatal("mutation crossed wire boundary")
		}
	}
}

func TestExpiredAppendChecksRemotePair(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	installation, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var requested atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Store(true)
		w.WriteHeader(http.StatusGone)
		printJSON(w, map[string]any{"error": "pair_revoked"})
	}))
	defer server.Close()
	p := testPair(installation, server.URL)
	if err = s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	created := time.Now().Unix() - appendSourceTTL - 1
	rt := codexRuntimeSource{Installation: installation, Home: t.TempDir(), Socket: "/fixture/socket", UID: os.Geteuid(), PeerPID: 22, PeerBirth: "birth"}
	source, err := registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, created, created)
	if err != nil {
		t.Fatal(err)
	}
	e := Envelope{V: 2, PairID: p.PairID, EventID: randomID(), Kind: "turn_completed"}
	if err = bindAppendDelivery(s, source, p.APIURL, e); err != nil {
		t.Fatal(err)
	}
	f := replyFrame{bodyIdentity: bodyIdentity{2, p.APIURL, installation, p.PairID, e.EventID, envelopeBodyDigest(e), randomID()}, Mode: appendMode, Agent: "codex", Expires: time.Now().Unix() + 30}
	if _, err = replyRequest(ctx, s, API{server.Client()}, p, "inspect", f); err == nil || !requested.Load() {
		t.Fatal("expired source disclosed without remote Pair validation")
	}
}
