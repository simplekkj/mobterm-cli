package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestDeliveryCommandRetries(t *testing.T) {
	for _, scenario := range []string{"network", "lost_receipt", "503", "429", "long_retry_after", "handoff", "unauthorized", "cancel", "exhausted", "short", "capability"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := testStore(t)
				id, _ := s.installation()
				p := testPair(id, "https://api.invalid")
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				l := fixtureLocator()
				l.InstallationID = id
				d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "fixture", l, "codex")
				if err != nil {
					t.Fatal(err)
				}
				budget := networkCommandTimeout
				if scenario == "short" {
					budget = 2 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				ctx = withDeliveryRetries(ctx)
				api := newAPI()
				var envelopes []Envelope
				var times []time.Time
				capCalls := 0
				api.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "capabilities") {
						capCalls++
						if capCalls == 1 {
							return nil, io.ErrUnexpectedEOF
						}
						b, _ := json.Marshal(activityCaps(p))
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
					}
					var e Envelope
					if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
						t.Fatal(err)
					}
					envelopes = append(envelopes, e)
					times = append(times, time.Now())
					if scenario == "cancel" {
						cancel()
					}
					if len(envelopes) == 1 || scenario == "exhausted" {
						switch scenario {
						case "network", "cancel", "exhausted", "short":
							return nil, io.ErrUnexpectedEOF
						case "lost_receipt":
							return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"broken":`))}, nil
						case "503", "429", "long_retry_after", "unauthorized", "handoff":
							status := 503
							headers := http.Header{}
							if scenario == "429" {
								status = 429
								headers.Set("Retry-After", "20")
							}
							if scenario == "long_retry_after" {
								status = 429
								headers.Set("Retry-After", "3600")
							}
							if scenario == "unauthorized" {
								status = 401
							}
							body := `{"error":"unauthorized"}`
							if scenario == "handoff" {
								body = `{"event_id":"` + e.EventID + `","state":"accepted","outbox_handoff":true}`
							}
							return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"event_id":"` + e.EventID + `","state":"apns_accepted"}`))}, nil
				})
				if scenario == "capability" {
					if _, err := completionCapabilities(ctx, s, api, p); err != nil || capCalls != 2 {
						t.Fatal("capability recovery", err, capCalls)
					}
				}
				result, err := deliverOne(ctx, s, api, d)
				want := 2
				switch scenario {
				case "long_retry_after", "handoff", "unauthorized", "cancel", "short", "capability":
					want = 1
				}
				if len(envelopes) != want {
					t.Fatalf("posts=%d want=%d result=%v err=%v", len(envelopes), want, result, err)
				}
				if want == 2 {
					if !reflect.DeepEqual(envelopes[0], envelopes[1]) {
						t.Fatal("retry changed ciphertext or identity")
					}
					if times[1].Sub(times[0]) < 15*time.Second {
						t.Fatal("backoff skipped")
					}
					if scenario == "429" && times[1].Sub(times[0]) < 20*time.Second {
						t.Fatal("Retry-After shortened")
					}
				}
				if scenario == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancel lost", err)
				}
				switch scenario {
				case "network", "lost_receipt", "503", "429", "capability":
					if err != nil || result["state"] != "apns_accepted" {
						t.Fatal(result, err)
					}
				}
				if scenario == "exhausted" {
					time.Sleep(40 * time.Second)
					_, _ = flushOutbox(ctx, s, api, p.PairID, false)
					if len(envelopes) != 2 {
						t.Fatal("later flush bypassed per-command attempt limit")
					}
				}
			})
		})
	}
}
func TestDeliveryRetryJitterBounds(t *testing.T) {
	values := map[time.Duration]bool{}
	for i := 0; i < 32; i++ {
		d := deliveryRetryDelay(1, 0)
		if d < 15*time.Second || d > 18750*time.Millisecond {
			t.Fatal(d)
		}
		values[d] = true
	}
	if len(values) < 2 {
		t.Fatal("backoff has no jitter")
	}
	if got := deliveryRetryDelay(12, time.Hour); got != time.Hour {
		t.Fatal("Retry-After changed", got)
	}
}

func TestMalformedErrorResponsePreservesRetryAfter(t *testing.T) {
	for _, bodyFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "html", true: "body_timeout"}[bodyFailure], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := testStore(t)
				id, _ := s.installation()
				p := testPair(id, "https://api.invalid")
				s.savePair(context.Background(), p)
				l := fixtureLocator()
				l.InstallationID = id
				d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "fixture", l, "codex")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), networkCommandTimeout)
				defer cancel()
				ctx = withDeliveryRetries(ctx)
				a := newAPI()
				calls := 0
				started := time.Now()
				a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						time.Sleep(250 * time.Millisecond)
						var body io.Reader = strings.NewReader("<html>PRIVATE_EDGE_ERROR</html>")
						if bodyFailure {
							body = failureReader{fixtureTimeout{}}
						}
						return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"20"}}, Body: io.NopCloser(body)}, nil
					}
					if time.Since(started) < 20250*time.Millisecond {
						t.Fatal("fractional Retry-After was shortened")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"event_id":"` + d.EventID + `","state":"apns_accepted"}`))}, nil
				})
				result, err := deliverOne(ctx, s, a, d)
				if err != nil || calls != 2 || result["state"] != "apns_accepted" {
					t.Fatal(result, err, calls)
				}
			})
		})
	}
}

func TestRetryCooperatesWithAnotherSender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t)
		id, _ := s.installation()
		p := testPair(id, "https://api.invalid")
		s.savePair(context.Background(), p)
		l := fixtureLocator()
		l.InstallationID = id
		d, _ := enqueueDelivery(context.Background(), s, p, "turn_completed", "fixture", l, "codex")
		ctx, cancel := context.WithTimeout(context.Background(), networkCommandTimeout)
		defer cancel()
		ctx = withDeliveryRetries(ctx)
		a := newAPI()
		calls := 0
		a.Client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.ErrUnexpectedEOF })
		go func() {
			time.Sleep(5 * time.Second)
			_, _ = updateDelivery(context.Background(), s, p.PairID, d.EventID, func(d *delivery) error { d.State = "apns_accepted"; d.Envelope = nil; d.Next = 0; return nil })
		}()
		result, err := deliverOne(ctx, s, a, d)
		if err != nil || calls != 1 || result["state"] != "apns_accepted" {
			t.Fatal("retry ignored persisted receipt", result, err, calls)
		}
	})
}
