package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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
	for _, scenario := range []string{"lost_reply", "expired", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "expired" {
					w.WriteHeader(410)
					printJSON(w, map[string]string{"error": "pair_expired"})
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
			case "expired":
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
