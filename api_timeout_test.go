package main

import (
	"context"
	"encoding/json"
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

func TestAPIRequestSixtySecondBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		delay, parent time.Duration
		wantTimeout   bool
	}{
		{"slow_success", 59 * time.Second, networkCommandTimeout, false},
		{"request_limit", 61 * time.Second, networkCommandTimeout, true},
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
					expected = 60 * time.Second
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
		if got := commandTimeout(args); got != 125*time.Second {
			t.Fatalf("%v has budget %s", args, got)
		}
	}
	for _, args := range [][]string{{"hooks", "install"}, {"status", "--agents-json"}, {"status", "--agents-json=true"}} {
		if got := commandTimeout(args); got != 25*time.Second {
			t.Fatalf("%v has budget %s", args, got)
		}
	}
	for _, args := range [][]string{nil, {"target", "verify"}, {"hooks", "uninstall"}, {"status", "--installation-json=true"}, {"outbox", "status"}, {"pairs", "retire"}, {"pairs", "abandon"}} {
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
			case <-time.After(59 * time.Second):
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
		time.Sleep(40 * time.Second)
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

// Exercise the actual capability -> encrypted reminder -> durable receipt path.
// Each response fits its request budget, while the total exceeds sixty seconds.
func TestCompletionNegotiationAndSendBudget(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "network_command", true: "short_observer"}[short], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := testStore(t)
				id, _ := s.installation()
				p := testPair(id, "https://api.invalid")
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				a := newAPI()
				posts := 0
				a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
					delay := 35 * time.Second
					if r.Method == "POST" {
						delay = 59 * time.Second
					}
					select {
					case <-time.After(delay):
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					var value any = activityCaps(p)
					if r.Method == "POST" {
						var e Envelope
						if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
							t.Fatal(err)
						}
						posts++
						value = map[string]any{"v": 2, "event_id": e.EventID, "state": "apns_accepted"}
					}
					raw, _ := json.Marshal(value)
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				})
				budget := commandTimeout([]string{"notify"})
				if short {
					budget = commandTimeout([]string{"observe"})
				}
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				l := fixtureLocator()
				l.InstallationID = id
				activity := Activity{Agent: "codex", Session: "session", Instance: "instance", Run: "run", Occurrence: "completed", State: "turn_completed", Reminder: "turn_completed"}
				started := time.Now()
				err := publishActivity(ctx, s, a, l, activity)
				db, readErr := loadRuns(s)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if short {
					if err == nil || posts != 0 || len(db.Intents) != 1 || time.Since(started) > budget {
						t.Fatalf("short observer lost obligation or exceeded deadline: %v", err)
					}
					return
				}
				records, readErr := outboxRecords(context.Background(), s, p.PairID, false)
				if err != nil || readErr != nil || posts != 1 || len(db.Intents) != 0 || len(records) != 1 || records[0].State != "apns_accepted" || time.Since(started) != 94*time.Second {
					t.Fatalf("slow completion not committed: err=%v read=%v posts=%d records=%v elapsed=%s", err, readErr, posts, records, time.Since(started))
				}
			})
		})
	}
}
