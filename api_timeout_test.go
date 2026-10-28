package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestAPIRequestThirtySecondBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		delay, parent time.Duration
		wantTimeout   bool
	}{
		{"slow_success", 29 * time.Second, networkCommandTimeout, false},
		{"request_limit", 31 * time.Second, networkCommandTimeout, true},
		{"parent_reserves_commit", 12 * time.Second, 12 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := newAPI()
				a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
					select {
					case <-time.After(tc.delay):
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"active"}`))}, nil
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				})
				ctx, cancel := context.WithTimeout(context.Background(), tc.parent)
				defer cancel()
				started := time.Now()
				_, err := a.request(ctx, testPair("installation_fixture_00000000", "https://api.invalid"), "GET", "/v1/pairs/test", nil)
				if errors.Is(err, context.DeadlineExceeded) != tc.wantTimeout || (!tc.wantTimeout && err != nil) {
					t.Fatalf("unexpected request result: %v", err)
				}
				expected := tc.delay
				if tc.wantTimeout {
					expected = 30 * time.Second
					if tc.parent-time.Second < expected {
						expected = tc.parent - time.Second
					}
				}
				if time.Since(started) != expected {
					t.Fatalf("elapsed %s, want %s", time.Since(started), expected)
				}
			})
		})
	}
}

// Real TLS IO proves that a response beyond the former four-second timeout is
// usable, independently of the simulated deadline tests above.
func TestAPISlowTLSResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
			_, _ = io.WriteString(w, `{"status":"active"}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	a := newAPI()
	a.Client.Transport = server.Client().Transport
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout([]string{"setup"}))
	defer cancel()
	result, err := a.request(ctx, testPair("installation_fixture_00000000", server.URL), "GET", "/v1/pairs/test", nil)
	if err != nil || result["status"] != "active" {
		t.Fatalf("slow TLS request failed: %v", err)
	}
}

func TestNetworkAndLocalCommandBudgets(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"notify"}, {"test"}, {"status", "--json"}, {"status", "--installation-json=false"}, {"status", "--pair", "agents-json"}, {"status", "--pair", "installation-json"}, {"outbox", "flush"}, {"outbox", "refresh"}, {"pairs", "reconcile"}} {
		if got := commandTimeout(args); got != 65*time.Second {
			t.Fatalf("%v has budget %s", args, got)
		}
	}
	for _, args := range [][]string{nil, {"target", "verify"}, {"hooks", "install"}, {"status", "--agents-json"}, {"status", "--installation-json=true"}, {"outbox", "status"}, {"pairs", "retire"}, {"pairs", "abandon"}} {
		if got := commandTimeout(args); got != 10*time.Second {
			t.Fatalf("%v has budget %s", args, got)
		}
	}
	if got := commandTimeout([]string{"upgrade"}); got != 2*time.Minute {
		t.Fatalf("upgrade budget changed: %s", got)
	}
}

func TestOutboxLeaseProtectsSlowRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t)
		id, err := s.installation()
		if err != nil {
			t.Fatal(err)
		}
		p := testPair(id, "https://api.invalid")
		p.Status = "active"
		if err := s.savePair(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		seedOutbox(t, s, p, 1, "pending", time.Now().Unix())
		key := deliveryKey(p.PairID, digest("0"))
		sh, err := s.loadShard(key)
		if err != nil {
			t.Fatal(err)
		}
		d := sh.Records[key]
		var calls atomic.Int32
		a := newAPI()
		a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			select {
			case <-time.After(29 * time.Second):
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"event_id":"` + d.EventID + `","state":"apns_accepted"}`))}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), networkCommandTimeout)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := deliverOne(ctx, s, a, d); done <- err }()
		synctest.Wait()
		time.Sleep(20 * time.Second)
		if _, err := deliverOne(ctx, s, a, d); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatal("second worker took over the slow request")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		sh, err = s.loadShard(key)
		if err != nil {
			t.Fatal(err)
		}
		if got := sh.Records[key]; got.State != "apns_accepted" || got.Posts != 1 || got.Lease != 0 {
			t.Fatalf("slow request result not committed: state=%s posts=%d lease=%d", got.State, got.Posts, got.Lease)
		}
	})
}
