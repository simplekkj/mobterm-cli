package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoctorDeepInteractionDecisionStores(t *testing.T) {
	s := testStore(t)
	installation, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(installation, "https://example.invalid")
	other := p
	other.PairID = randomID()
	ctx := t.Context()
	now := time.Now().Unix()
	birth, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	parentBirth, err := processIdentity(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	l := Locator{V: 1, InstallationID: installation, Backend: "tmux", Namespace: "/tmp/fixture.socket", ServerIdentity: "1:2:3", WorkspaceID: "$1", ViewID: "@1", PaneID: "%1", OccupantID: "tmux-proc-v1:" + digest("occupant")}
	const secret = "doctor-must-never-report-this-user-content"
	// Both pairs were eligible. Only the winning pair owns the lost receipt.
	record := replyRecord{V: 1, SourceID: digest("ordinary"), Cursor: digest("cursor"), Agent: "claude", Locator: l, State: "delivery_unknown", Expires: now + 50, Pairs: map[string]string{p.PairID: randomID(), other.PairID: randomID()}, NativeStorage: secret}
	record.Frame = &replyFrame{bodyIdentity: bodyIdentity{V: 1, Origin: p.APIURL, Installation: installation, Pair: p.PairID}, Agent: record.Agent, SourceID: record.SourceID, Cursor: record.Cursor, ResponseDigest: digest(secret), DecisionID: randomID()}
	if err := saveReply(s, record); err != nil {
		t.Fatal(err)
	}
	if _, err := readReply(s, record.SourceID); err != nil {
		t.Fatal(err)
	}
	request := ApprovalRequest{V: 3, RequestID: randomID(), Agent: "claude", Locator: l, State: "pending", ExpiresAt: now + 60, PID: os.Getpid(), Birth: birth, ParentPID: os.Getppid(), ParentBirth: parentBirth, OperationJSON: secret, Digest: digest(secret), Pairs: map[string]int64{p.PairID: 3, other.PairID: 3}}
	saveApproval := func(r ApprovalRequest) {
		t.Helper()
		if err := s.writeJSON("approval-"+r.RequestID+".json", r); err != nil {
			t.Fatal(err)
		}
		if _, err := approvalRead(s, r.RequestID); err != nil {
			t.Fatal(err)
		}
	}
	saveApproval(request)
	request.RequestID = randomID()
	request.ExpiresAt = now - 1
	saveApproval(request)
	request.RequestID = randomID()
	request.State = "consumed"
	request.DecidingPair = other.PairID
	request.DecisionID = randomID()
	request.OperationJSON = ""
	saveApproval(request)
	// Another installation's persisted record cannot contribute a handoff.
	request.RequestID = randomID()
	request.DecidingPair = p.PairID
	request.Locator.InstallationID = randomID()
	saveApproval(request)
	// The independent Codex append ledger can retain the dispatch claim well
	// after the original opportunity expires. Other participants cannot see it.
	rt := codexRuntimeSource{Installation: installation, Home: "/tmp/fixture-home", Socket: "/tmp/fixture-socket", UID: os.Geteuid(), PeerPID: os.Getpid(), PeerBirth: birth}
	id, err := registerAppendSource(s, rt, appendThreadFixture, appendTurnFixture, now, now)
	if err != nil {
		t.Fatal(err)
	}
	db, err := loadAppendStore(s)
	if err != nil {
		t.Fatal(err)
	}
	src := db.Sources[id]
	part := appendParticipant{Origin: p.APIURL, Event: randomID(), Envelope: digest("envelope"), Lease: digest("lease"), Revision: 1, Generation: 3, Expires: now + 30}
	src.Participants[p.PairID], src.Participants[other.PairID] = part, part
	src.State, src.ClaimedAt = "dispatching", now
	src.Decision = &replyFrame{Mode: appendMode, bodyIdentity: bodyIdentity{V: 2, Installation: installation, Origin: p.APIURL, Pair: p.PairID, Event: part.Event, Envelope: part.Envelope}, Agent: "codex", SourceID: id, Lease: part.Lease, PolicyGeneration: part.Generation, ActivationRevision: part.Revision, ClientID: randomID(), DecisionID: randomID(), ResponseDigest: digest(secret)}
	if err := db.save(s); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAppendStore(s); err != nil {
		t.Fatal(err)
	}
	// Real interaction frames also carry the deciding participant.
	ir := InteractionRecord{V: 1, RequestID: randomID(), Locator: l, State: "dispatching", Pairs: map[string]int64{p.PairID: 3, other.PairID: 3}, Agent: "pi", Kind: "question"}
	ir.Frame = &InteractionFrame{V: 1, PairID: other.PairID, RequestID: ir.RequestID, ResponseJSON: secret}
	if err := s.writeJSON("interaction-"+ir.RequestID+".json", ir); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	for _, tc := range []struct {
		pair                             Pair
		agent                            string
		unknown, live, expired, handoffs int
	}{
		{p, "claude", 1, 1, 1, 0}, {p, "codex", 1, 0, 0, 0}, {p, "pi", 0, 0, 0, 0}, {p, "", 2, 1, 1, 0},
		{other, "claude", 0, 1, 1, 1}, {other, "codex", 0, 0, 0, 0}, {other, "pi", 1, 0, 0, 0},
	} {
		r := doctorInteractions(ctx, s, tc.pair, doctorOptions{Deep: true, Agent: tc.agent})
		if r.Counts["result_unknown"] != tc.unknown || r.Counts["live_bindings"] != tc.live || r.Counts["expired_bindings"] != tc.expired || r.Counts["native_handoffs"] != tc.handoffs {
			t.Fatalf("agent %q result %v", tc.agent, r)
		}
		if tc.unknown > 0 && (r.Reason != "interaction_result_unknown" || r.Status != "warn") {
			t.Fatal("lost receipt hidden", r)
		}
		b, _ := json.Marshal(r)
		if bytes.Contains(b, []byte(secret)) || bytes.Contains(b, []byte(record.Frame.DecisionID)) || bytes.Contains(b, []byte(src.Decision.Lease)) {
			t.Fatal("private data escaped")
		}
	}
	if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("diagnosis mutated stored decisions")
	}
}

func TestDoctorRemoteInteractionAgentAndDefaultOff(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var p Pair
	var isEnabled atomic.Bool
	isEnabled.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/interaction-capabilities") {
			t.Error("unexpected request")
		}
		c := interactionTestGrant(p)
		c.Enabled = isEnabled.Load()
		for agent, kinds := range c.Interactions {
			for kind := range kinds {
				kinds[kind] = c.Enabled && agent == "claude" && kind == "question"
			}
		}
		if !c.Enabled {
			c.CapabilityGeneration = 0
		}
		printJSON(w, c)
	}))
	defer server.Close()
	p = testPair(id, server.URL)
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex", ""} {
		r := doctorCapabilities(t.Context(), s, API{server.Client()}, p, "interaction.service", agent)
		if r.Status != "pass" {
			t.Fatal(r)
		}
		if agent == "codex" {
			if len(r.Details) != 0 || r.Reason != "remote_grants_disabled" {
				t.Fatal(r)
			}
		} else if len(r.Details) != 1 || r.Details["claude"] != "question" || r.Reason != "interaction_grants_read" {
			t.Fatal(r)
		}
	}
	isEnabled.Store(false)
	r := doctorCapabilities(t.Context(), s, API{server.Client()}, p, "interaction.service", "claude")
	if r.Status != "pass" || r.Reason != "remote_grants_disabled" {
		t.Fatal(r)
	}
	m, err := doctorPairGET(t.Context(), s, API{server.Client()}, p, "/v2/pairs/"+p.PairID+"/interaction-capabilities")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseInteractionCapabilities(m, p); err == nil {
		t.Fatal("zero-generation evidence passed active operation parser")
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { delete(m, "enabled") },
		func(m map[string]any) { delete(m, "capability_generation") },
		func(m map[string]any) { m["enabled"] = true },
		func(m map[string]any) { m["enabled"] = nil },
		func(m map[string]any) { m["installation_id"] = randomID() },
		func(m map[string]any) { m["pair_id"] = randomID() },
		func(m map[string]any) { m["interaction_protocols"] = []int{2} },
		func(m map[string]any) { m["expires_at"] = time.Now().Unix() - 1 },
		func(m map[string]any) { m["policy_generation"] = 0 },
		func(m map[string]any) { m["capability_generation"] = -1 },
		func(m map[string]any) { m["interactions"].(map[string]any)["pi"].(map[string]any)["approval"] = nil },
	} {
		raw, _ := json.Marshal(m)
		var copy map[string]any
		_ = json.Unmarshal(raw, &copy)
		mutate(copy)
		if _, err := parseInteractionCapabilityEvidence(copy, p); err == nil {
			t.Fatal("malformed evidence accepted")
		}
	}
}
