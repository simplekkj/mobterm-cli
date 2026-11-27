package cli

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/simplekkj/mobterm-cli/internal/doctor"
)

func runProviderReport(t *testing.T, s Store, a API, o doctorOptions) doctor.Report {
	t.Helper()
	if o.Scope == "" {
		o.Scope = "all"
	}
	r, err := doctor.Run(t.Context(), doctorProviders(s, a, o), o.Scope, strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range r.Checks {
		if row.Reason == "invalid_evidence" || row.Reason == "check_timeout" {
			t.Fatalf("unexpected %s: %s", row.ID, row.Reason)
		}
	}
	return r
}
func providerFinding(t *testing.T, r doctor.Report, id, scope string) doctor.Finding {
	t.Helper()
	for _, row := range r.Checks {
		if row.ID == id && (scope == "" || row.Scope == scope) {
			return row
		}
	}
	t.Fatalf("missing %s/%s", id, scope)
	return doctor.Finding{}
}
func providerSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		b := []byte{}
		if info.Mode().IsRegular() {
			b, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		files[path] = fmt.Sprintf("%s:%d:%s", info.Mode(), info.ModTime().UnixNano(), digest(string(b)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

type doctorTransport func(*http.Request) (*http.Response, error)

func (f doctorTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDoctorProvidersFreshAndCorruptStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	a := API{&http.Client{Transport: doctorTransport(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected") })}}
	s := testStore(t)
	for _, o := range []doctorOptions{{}, {Deep: true}, {Offline: true}, {Offline: true, Deep: true}} {
		r := runProviderReport(t, s, a, o)
		if f := providerFinding(t, r, "pair.local", ""); f.Status != "skipped" || f.Reason != "pair_not_configured" {
			t.Fatal(f)
		}
		if f := providerFinding(t, r, "workspace.target", ""); f.Status != "skipped" {
			t.Fatal(f)
		}
		if _, err := os.Lstat(s.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Doctor initialized %s", s.Dir)
		}
	}
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "pairs.json"), []byte("invalid-json-CANARY"), 0600); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	r := runProviderReport(t, s, a, doctorOptions{Deep: true})
	if f := providerFinding(t, r, "storage.pairs", ""); f.Reason != "pair_store_invalid" {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "pair.local", ""); f.Status != "blocked" {
		t.Fatal(f)
	}
	b, _ := json.Marshal(r)
	if bytes.Contains(b, []byte("CANARY")) {
		t.Fatal("raw corrupt configuration escaped")
	}
	if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) || calls.Load() != 0 {
		t.Fatal("Doctor contacted network or changed store")
	}
}

func TestDoctorProvidersPairScopeReceiptsAndAuthority(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var health, queries atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected %s", r.Method)
		}
		if r.URL.Path == "/health" {
			health.Add(1)
			if r.Header.Get("Authorization") != "" {
				t.Error("health received pair credential")
			}
			w.Header().Set("X-Request-ID", "fa3c66bb-08a5-4694-a42d-b68c36a6d4a9")
			printJSON(w, map[string]any{"service": "mobterm-api", "protocol": 1})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/pairs/") || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("unexpected API request")
			return
		}
		queries.Add(1)
		pair := strings.TrimPrefix(r.URL.Path, "/v1/pairs/")
		state := "active"
		if strings.HasSuffix(pair, "2") {
			state = "revoked"
		}
		printJSON(w, map[string]any{"pair_id": pair, "installation_id": id, "status": state, "notifications_enabled": false})
	}))
	defer server.Close()
	p := testPair(id, server.URL)
	q := p
	q.PairID = randomID() + "2"
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.savePair(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	a := API{server.Client()}
	r := runProviderReport(t, s, a, doctorOptions{Scope: "pair"})
	if health.Load() != 1 || queries.Load() != 2 {
		t.Fatalf("health=%d pairs=%d", health.Load(), queries.Load())
	}
	if f := providerFinding(t, r, "network.service", ""); f.Details["request_id"] != "fa3c66bb-08a5-4694-a42d-b68c36a6d4a9" {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "pair.remote", doctorPairScope(p.PairID)); f.Reason != "pair_active_notifications_disabled" {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "pair.remote", doctorPairScope(q.PairID)); f.Reason != "pair_revoked" {
		t.Fatal(f)
	}
	r = runProviderReport(t, s, a, doctorOptions{Scope: "pair", Pair: p.PairID})
	if health.Load() != 2 || queries.Load() != 3 {
		t.Fatal("scope executed another pair")
	}
	r = runProviderReport(t, s, a, doctorOptions{Offline: true, Deep: true})
	if health.Load() != 2 || queries.Load() != 3 {
		t.Fatal("offline contacted API")
	}
	b, _ := json.Marshal(r)
	if bytes.Contains(b, []byte(p.SendToken)) || bytes.Contains(b, []byte(p.EncryptionKey)) {
		t.Fatal("pair credential escaped")
	}
	if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("Doctor persisted authenticated state or created files")
	}
}

func TestDoctorProvidersNetworkRootsAndJobIsolation(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "https://doctor.invalid")
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		reason string
		err    error
	}{
		{"dns_failed", &net.DNSError{Err: "no such host"}},
		{"tcp_failed", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}},
		{"tls_failed", x509.UnknownAuthorityError{}},
		{"request_timed_out", context.DeadlineExceeded},
	} {
		t.Run(c.reason, func(t *testing.T) {
			var calls atomic.Int32
			a := API{&http.Client{Transport: doctorTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, c.err
			})}}
			r := runProviderReport(t, s, a, doctorOptions{})
			health := providerFinding(t, r, "network.service", "")
			if health.Reason != c.reason {
				t.Fatal(health)
			}
			pair := providerFinding(t, r, "pair.remote", "")
			if pair.Status != "blocked" || len(pair.BlockedBy) != 1 || pair.BlockedBy[0] != doctor.Key(health.ID, health.Scope) {
				t.Fatal(pair)
			}
			if f := providerFinding(t, r, "job.shell", ""); f.Status == "blocked" {
				t.Fatal(f)
			}
			if calls.Load() != 1 {
				t.Fatal("unnecessary pair request")
			}
		})
	}
}

func TestDoctorProvidersExpiredOutboxInterruptedTransactionAndReusedPID(t *testing.T) {
	s := testStore(t)
	inst, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(inst, "https://doctor.invalid")
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	seedOutbox(t, s, p, 3, "pending", time.Now().Unix()-3700)
	q := quickTestStart(p, 7, "SECRET COMMAND")
	record := quickJobRecord{quickJobReply: quickJobReply{quickResultIdentity: q.quickResultIdentity, State: "running"}, PairID: p.PairID, Owner: os.Getuid(), Worker: os.Getpid(), WorkerBirth: "stale birth", CommandName: "SECRET JOB NAME"}
	if err := record.save(s); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	r := runProviderReport(t, s, newAPI(), doctorOptions{Offline: true, Deep: true})
	if f := providerFinding(t, r, "job.records", ""); f.Reason != "worker_identity_unconfirmed" || f.Counts["worker_unconfirmed"] != 1 {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "delivery.outbox", ""); f.Reason != "outbox_expired_pending" || f.Counts["expired_pending"] != 3 {
		t.Fatal(f)
	}
	b, _ := json.Marshal(r)
	if bytes.Contains(b, []byte("SECRET")) {
		t.Fatal("command escaped")
	}
	if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("job/outbox state was changed")
	}
	tx := filepath.Join(s.Dir, "outbox-v1", "transaction.json")
	if err := os.WriteFile(tx, []byte("INTERRUPTED"), 0600); err != nil {
		t.Fatal(err)
	}
	r = runProviderReport(t, s, newAPI(), doctorOptions{Scope: "delivery", Offline: true, Deep: true})
	if f := providerFinding(t, r, "delivery.outbox", ""); f.Reason != "outbox_transaction_pending" {
		t.Fatal(f)
	}
	if b, err := os.ReadFile(tx); err != nil || string(b) != "INTERRUPTED" {
		t.Fatal("transaction modified")
	}
}

func TestDoctorProvidersUnsafePairStorage(t *testing.T) {
	for _, kind := range []string{"symlink", "permissions", "retired", "wrong_installation"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			id, err := s.installation()
			if err != nil {
				t.Fatal(err)
			}
			p := testPair(id, "https://doctor.invalid")
			if kind == "wrong_installation" {
				p.InstallationID = randomID()
			}
			if err := s.savePair(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Dir, "pairs.json")
			switch kind {
			case "symlink":
				other := filepath.Join(s.Dir, "other")
				if os.Rename(path, other) != nil || os.Symlink(other, path) != nil {
					t.Fatal("fixture setup failed")
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "retired":
				if err := s.writeJSON("pair-history.json", []retiredPair{{PairRef: digest(p.PairID), Status: "revoked"}}); err != nil {
					t.Fatal(err)
				}
			}
			a := API{&http.Client{Transport: doctorTransport(func(req *http.Request) (*http.Response, error) {
				t.Error("request sent despite untrusted pair")
				return nil, errors.New("unexpected")
			})}}
			r := runProviderReport(t, s, a, doctorOptions{Scope: "pair"})
			if f := providerFinding(t, r, "pair.local", ""); f.Status != "blocked" && f.Status != "fail" {
				t.Fatal(f)
			}
		})
	}
}

func TestDoctorProvidersDeepGrantsAndLostClaimReadOnly(t *testing.T) {
	s := testStore(t)
	inst, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var p Pair
	var caps, grants atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		switch r.URL.Path {
		case "/health":
			printJSON(w, map[string]any{"service": "mobterm-api", "protocol": 1})
		case "/v1/pairs/" + p.PairID:
			printJSON(w, map[string]any{"pair_id": p.PairID, "installation_id": inst, "status": "active", "notifications_enabled": true})
		case "/v2/pairs/" + p.PairID + "/interaction-capabilities":
			grants.Add(1)
			printJSON(w, interactionTestGrant(p))
		case "/v2/pairs/" + p.PairID + "/capabilities":
			caps.Add(1)
			m := activityCaps(p)
			m["notifications_enabled"] = true
			m["quick_command_v3"] = true
			m["quick_command_completed"] = false
			printJSON(w, m)
		default:
			t.Error("unexpected path")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	p = testPair(inst, server.URL)
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{p.PairID: {"claude": {"question": 3}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("reply-activations-v1.json", replyActivations{p.PairID: {"claude": {Token: randomID(), Revision: 1, Generation: 3}}}); err != nil {
		t.Fatal(err)
	}
	// A real bound record without an acknowledged result must not be replayed.
	record := InteractionRecord{V: 1, RequestID: randomID(), Locator: Locator{InstallationID: inst}, State: "dispatching", Pairs: map[string]int64{p.PairID: 3}, Agent: "claude", Kind: "question"}
	record.Frame = &InteractionFrame{V: 1, PairID: p.PairID, RequestID: record.RequestID}
	if err := s.writeJSON("interaction-"+record.RequestID+".json", record); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	r := runProviderReport(t, s, API{server.Client()}, doctorOptions{Deep: true, Agent: "claude"})
	if f := providerFinding(t, r, "interaction.policy", ""); f.Reason != "interaction_result_unknown" || f.Counts["result_unknown"] != 1 || f.Details["claude"] != "question,reply" {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "interaction.service", ""); f.Reason != "interaction_grants_read" {
		t.Fatal(f)
	}
	if f := providerFinding(t, r, "delivery.policy", ""); f.Reason != "capability_policy_read" {
		t.Fatal(f)
	}
	if caps.Load() != 1 || grants.Load() != 1 || !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("unexpected probes or mutations")
	}
	if err := s.transitionPair(t.Context(), p, "pending_review"); err != nil {
		t.Fatal(err)
	}
	before = providerSnapshot(t, s.Dir)
	r = runProviderReport(t, s, API{server.Client()}, doctorOptions{Deep: true})
	if f := providerFinding(t, r, "pair.remote", ""); f.Reason != "pair_reconcile_needed" {
		t.Fatal(f)
	}
	if caps.Load() != 1 || grants.Load() != 1 || !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("Doctor implicitly activated pair")
	}
}

func TestDoctorProvidersLargeHistoricalCatalog(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]Pair{}
	for n := 0; n < 65; n++ {
		p := testPair(id, "https://doctor.invalid")
		p.PairID = fmt.Sprintf("historical_pair_%04d", n)
		pairs[p.PairID] = p
	}
	if err := s.writeJSON("pairs.json", pairs); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"all", "pair", "network", "interaction", "delivery"} {
		t.Run(scope, func(t *testing.T) {
			r := runProviderReport(t, s, newAPI(), doctorOptions{Scope: scope, Offline: true, Deep: true})
			if r.Complete {
				t.Fatal("partial catalog marked complete")
			}
			if f := providerFinding(t, r, "pair.limit", doctorHost); f.Status != "warn" || f.Counts["remaining_pairs"] != 49 {
				t.Fatal(f)
			}
			if f := providerFinding(t, r, "storage.pairs", doctorHost); f.Status != "pass" || f.Counts["pairs"] != 65 {
				t.Fatal(f)
			}
			if scope == "pair" {
				var found int
				for _, f := range r.Checks {
					if f.ID == "pair.local" {
						found++
						if f.Details["pair_id"] > "historical_pair_0015" {
							t.Fatalf("unexpected expansion: %s", f.Details["pair_id"])
						}
					}
				}
				if found != 16 {
					t.Fatalf("expanded %d of 65", found)
				}
			}
		})
	}
	last := "historical_pair_0064"
	r := runProviderReport(t, s, newAPI(), doctorOptions{Scope: "pair", Pair: last, Offline: true})
	if !r.Complete {
		t.Fatal("explicit pair considered truncated")
	}
	if f := providerFinding(t, r, "pair.local", doctorPairScope(last)); f.Status != "pass" || f.Details["pair_id"] != last {
		t.Fatal(f)
	}
	for _, f := range r.Checks {
		if f.ID == "pair.limit" {
			t.Fatal("unnecessary catalog expansion")
		}
	}
}

func TestDoctorProvidersMissingAndEmptyPairCatalog(t *testing.T) {
	s := testStore(t)
	if _, err := s.installation(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "empty"} {
		t.Run(name, func(t *testing.T) {
			if name == "empty" {
				if err := s.writeJSON("pairs.json", map[string]Pair{}); err != nil {
					t.Fatal(err)
				}
			}
			r := runProviderReport(t, s, newAPI(), doctorOptions{Scope: "storage"})
			if f := providerFinding(t, r, "storage.pairs", doctorHost); f.Status != "skipped" || f.Reason != "pair_not_configured" || f.Counts["pairs"] != 0 {
				t.Fatal(f)
			}
		})
	}
}

func TestDoctorProvidersPartialJobInventoryKeepsInspectedRecords(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "https://doctor.invalid")
	for n := 0; n < 129; n++ {
		q := quickTestStart(p, n, "fixture")
		rec := quickJobRecord{quickJobReply: quickJobReply{quickResultIdentity: q.quickResultIdentity, State: "not_started"}, Owner: os.Getuid(), PairID: p.PairID}
		if err := rec.save(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, deep := range []bool{false, true} {
		r := runProviderReport(t, s, newAPI(), doctorOptions{Scope: "job", Deep: deep})
		n := 128
		if deep {
			n = 129
		}
		if f := providerFinding(t, r, "job.records", doctorHost); r.Complete != deep || f.Counts["not_started"] != n {
			t.Fatalf("deep=%v complete=%v finding=%v", deep, r.Complete, f)
		}
	}
}
