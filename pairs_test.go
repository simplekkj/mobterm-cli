package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndependentPairCapacityAndAbandon(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	ctx := context.Background()
	for i := 0; i < 64; i++ {
		p := testPair(id, "https://api.invalid")
		p.PairID = fmt.Sprintf("pair_%025d", i)
		if i >= 32 {
			p.Status = "pending"
		}
		if err := s.savePair(ctx, p); err != nil {
			t.Fatal("active/pending limits not independent", i, err)
		}
	}
	p := testPair(id, "https://api.invalid")
	p.PairID = "pair_overflow000000000000"
	if err := s.savePair(ctx, p); err == nil {
		t.Fatal("active 33 accepted")
	}
	p.Status = "pending"
	if err := s.savePair(ctx, p); err == nil {
		t.Fatal("pending 33 accepted")
	}
	if err := s.retirePair(ctx, fmt.Sprintf("pair_%025d", 0), "abandoned", true); err == nil {
		t.Fatal("abandon deleted active")
	}
	abandoned := fmt.Sprintf("pair_%025d", 32)
	pairs, _ := s.pairs()
	old := pairs[abandoned]
	if err := s.retirePair(ctx, abandoned, "abandoned", true); err != nil {
		t.Fatal(err)
	}
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal("abandon failed to free pending capacity", err)
	}
	if err := s.savePair(ctx, old); err == nil {
		t.Fatal("late result reactivated abandoned credentials")
	}
	pairs, _ = s.pairs()
	if len(pairs) != 64 || pairs[abandoned].SendToken != "" {
		t.Fatal("credentials retained or another device lost")
	}
}
func TestPairReconciliationTrustAndLostClaim(t *testing.T) {
	for _, scenario := range []string{"active", "revoked", "expired", "unknown", "network"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "active":
					printJSON(w, map[string]string{"status": "active", "installation_id": id})
				case "revoked", "expired":
					w.WriteHeader(410)
					printJSON(w, map[string]string{"error": "pair_" + scenario})
				case "unknown":
					w.WriteHeader(404)
					printJSON(w, map[string]string{"error": "not_found"})
				case "network":
					w.WriteHeader(503)
					printJSON(w, map[string]string{"error": "unavailable"})
				}
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			p.Status = "pending"
			if scenario == "network" {
				p.Status = "active"
			}
			_ = s.savePair(context.Background(), p)
			_, _ = reconcilePairs(context.Background(), s, API{server.Client()}, p.PairID)
			pairs, _ := s.pairs()
			switch scenario {
			case "active":
				if pairs[p.PairID].Status != "active" {
					t.Fatal("lost claim not recovered")
				}
			case "revoked", "expired":
				if _, ok := pairs[p.PairID]; ok {
					t.Fatal("confirmed inactive not reclaimed")
				}
			case "unknown":
				if pairs[p.PairID].Status != "pending_review" || pairs[p.PairID].SendToken != p.SendToken {
					t.Fatal("unknown credentials lost")
				}
			case "network":
				if pairs[p.PairID].Status != "active" {
					t.Fatal("network disabled effective pair")
				}
			}
		})
	}
}

func TestSetupLostReplyAndConfirmedExpiry(t *testing.T) {
	for _, scenario := range []string{"lost_reply", "expired", "revoked", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "expired" || scenario == "revoked" {
					w.WriteHeader(410)
					printJSON(w, map[string]string{"error": "pair_" + scenario})
					return
				}
				if r.Method == "POST" {
					w.WriteHeader(503)
					printJSON(w, map[string]string{"error": "unavailable"})
					return
				}
				if scenario == "unknown" {
					w.WriteHeader(404)
					printJSON(w, map[string]string{"error": "not_found"})
					return
				}
				printJSON(w, map[string]string{"status": "active", "installation_id": id})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			_, err := setup(context.Background(), s, API{server.Client()}, bytes.NewReader(framed(p.SetupPacket)))
			pairs, _ := s.pairs()
			switch scenario {
			case "lost_reply":
				if err != nil || pairs[p.PairID].Status != "active" {
					t.Fatal("lost reply not reconciled", err)
				}
			case "expired", "revoked":
				if err == nil {
					t.Fatal("expired setup succeeded")
				}
				if _, ok := pairs[p.PairID]; ok {
					t.Fatal("confirmed expired attempt retained credentials")
				}
			case "unknown":
				if err == nil || pairs[p.PairID].SendToken != p.SendToken {
					t.Fatal("unknown setup discarded credentials", err)
				}
			}
		})
	}
}

// Device revocation keeps the original sender-facing Pair contract. One local
// installation can have multiple pairs for that device and another live device.
func TestDeviceRevocationReconcilesOnlyAffectedPairs(t *testing.T) {
	s := testStore(t)
	installation, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	pairs := make(map[string]Pair)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/pairs/")
		p, ok := pairs[id]
		if r.Method != "GET" || !ok || r.Header.Get("Authorization") != "Bearer "+p.SendToken {
			t.Error("reconcile changed the original pair route or credentials")
			w.WriteHeader(http.StatusUnauthorized)
			printJSON(w, map[string]string{"error": "unauthorized"})
			return
		}
		switch id {
		case "pair_device_revoked_000000001":
			printJSON(w, map[string]string{"pair_id": id, "installation_id": installation, "status": "revoked"})
		case "pair_device_revoked_000000002":
			w.WriteHeader(http.StatusGone)
			printJSON(w, map[string]string{"error": "pair_revoked"})
		default:
			printJSON(w, map[string]string{"pair_id": id, "installation_id": installation, "status": "active"})
		}
	}))
	defer server.Close()
	for i, id := range []string{"pair_device_revoked_000000001", "pair_device_revoked_000000002", "pair_other_device_00000000001"} {
		p := testPair(installation, server.URL)
		p.PairID = id
		p.SendToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 1)}, 32))
		p.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 10)}, 32))
		pairs[id] = p
		if err := s.savePair(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	// Reopen the durable store as a later short-lived CLI invocation would. The
	// API's retention across actual cleanup is verified by its local D1 suite.
	for attempt := 0; attempt < 2; attempt++ {
		s = Store{Dir: s.Dir}
		if _, err := reconcilePairs(context.Background(), s, API{server.Client()}, ""); err != nil {
			t.Fatal(err)
		}
		remaining, err := s.pairs()
		other := pairs["pair_other_device_00000000001"]
		if err != nil || len(remaining) != 1 || remaining[other.PairID] != other {
			t.Fatal("device retirement changed another device or retained retired credentials", err)
		}
		var history []retiredPair
		if err := s.readJSON("pair-history.json", &history); err != nil || len(history) != 2 {
			t.Fatal("retirement summaries missing or duplicated", err)
		}
		for _, h := range history {
			if h.Status != "revoked" || (h.PairRef != digest("pair_device_revoked_000000001") && h.PairRef != digest("pair_device_revoked_000000002")) {
				t.Fatal("wrong retired pair or reason")
			}
		}
		for _, name := range []string{"pairs.json", "pair-history.json"} {
			data, err := os.ReadFile(filepath.Join(s.Dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for id, p := range pairs {
				if id != other.PairID && (bytes.Contains(data, []byte(p.SendToken)) || bytes.Contains(data, []byte(p.EncryptionKey))) {
					t.Fatal("retired sender credential or encryption key remains on disk")
				}
			}
		}
	}
}

func TestUnconfirmedDeviceRetirementRetainsPairCredentials(t *testing.T) {
	for _, action := range []string{"reconcile", "send"} {
		for _, tc := range []struct {
			name   string
			status int
			code   string
		}{
			{"unauthorized", 401, "unauthorized"},
			{"forbidden", 403, "forbidden"},
			{"missing", 404, "not_found"},
			{"unavailable", 503, "unavailable"},
			{"wrong_route_code", 410, "device_revoked"},
			{"unknown_gone", 410, "unknown"},
			{"wrong_status", 401, "pair_revoked"},
			{"timeout", 0, ""},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				s := testStore(t)
				installation, err := s.installation()
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.status == 0 {
						_, _ = io.Copy(io.Discard, r.Body)
						<-r.Context().Done()
						return
					}
					w.WriteHeader(tc.status)
					printJSON(w, map[string]string{"error": tc.code})
				}))
				defer server.Close()
				client := server.Client()
				if tc.status == 0 {
					client.Timeout = 50 * time.Millisecond
				}
				p := testPair(installation, server.URL)
				other := p
				other.PairID = "pair_other_device_00000000001"
				for _, pair := range []Pair{p, other} {
					if err := s.savePair(context.Background(), pair); err != nil {
						t.Fatal(err)
					}
				}
				if action == "reconcile" {
					if _, err := reconcilePairs(context.Background(), s, API{client}, p.PairID); err == nil {
						t.Fatal("unconfirmed reconciliation reported success")
					}
				} else {
					locator := fixtureLocator()
					locator.InstallationID = installation
					d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", locator, "codex")
					if err != nil {
						t.Fatal(err)
					}
					_, _ = deliverOne(context.Background(), s, API{client}, d)
				}
				remaining, err := s.pairs()
				if err != nil || len(remaining) != 2 || remaining[p.PairID].SetupPacket != p.SetupPacket || remaining[other.PairID] != other {
					t.Fatal("unconfirmed result deleted/rebound credentials or changed another device", err)
				}
				if _, err := os.Stat(filepath.Join(s.Dir, "pair-history.json")); !os.IsNotExist(err) {
					t.Fatal("unconfirmed response recorded a retirement", err)
				}
			})
		}
	}
}

func TestReconcileLateResultCannotReviveAbandonedAttempt(t *testing.T) {
	for _, remote := range []string{"active", "revoked"} {
		t.Run(remote, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			entered := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				printJSON(w, map[string]string{"status": remote, "installation_id": id})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			p.Status = "pending"
			if err := s.savePair(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := reconcilePairs(context.Background(), s, API{server.Client()}, p.PairID)
				finished <- err
			}()
			<-entered
			if err := s.retirePair(context.Background(), p.PairID, "abandoned", true); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-finished; err == nil {
				t.Fatal("stale reconciliation acknowledged")
			}
			pairs, err := s.pairs()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := pairs[p.PairID]; ok {
				t.Fatal("abandoned credentials resurrected")
			}
			var history []retiredPair
			if err = s.readJSON("pair-history.json", &history); err != nil || len(history) != 1 || history[0].Status != "abandoned" {
				t.Fatal("stale retirement changed user abandonment", err)
			}
		})
	}
}
