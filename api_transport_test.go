package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Delay ClientHello processing, before the server sends its TLS handshake.
// Only the loopback certificate trust is added to newAPI's production transport.
func TestAPIRealDelayedTLSHandshake(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	originalTimeout := base.TLSHandshakeTimeout
	for _, legacy := range []bool{true, false} {
		name := "production"
		if legacy {
			name = "legacy_ten_seconds"
		}
		t.Run(name, func(t *testing.T) {
			var handled atomic.Bool
			stop := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handled.Store(true)
				_, _ = io.WriteString(w, `{"status":"active"}`)
			}))
			server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				select {
				case <-time.After(11 * time.Second):
				case <-stop:
				}
				return nil, nil
			}}
			server.StartTLS()
			defer server.Close()
			defer close(stop)
			a := newAPI()
			transport := a.Client.Transport.(*http.Transport)
			defer transport.CloseIdleConnections()
			if transport == base {
				t.Fatal("API changed the shared transport")
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport.TLSClientConfig = &tls.Config{RootCAs: roots}
			if legacy {
				transport.TLSHandshakeTimeout = 10 * time.Second
			}
			started := time.Now()
			result, err := a.request(context.Background(), testPair("installation_fixture_00000000", server.URL), "GET", "/", nil)
			elapsed := time.Since(started)
			if legacy {
				var network net.Error
				if !errors.As(err, &network) || !network.Timeout() || errors.Is(err, context.DeadlineExceeded) || handled.Load() {
					t.Fatalf("expected TLS-only timeout before HTTP: %v", err)
				}
				if pairingFailureMetadata(pairingFailure("claim", "unknown", err)).Code != "timeout" {
					t.Fatal("real TLS timeout misclassified")
				}
			} else if err != nil || result["status"] != "active" || !handled.Load() || elapsed < 11*time.Second || elapsed >= apiRequestTimeout {
				t.Fatalf("delayed TLS request failed: elapsed=%s err=%v", elapsed, err)
			}
			t.Logf("legacy=%v elapsed=%s", legacy, elapsed)
		})
	}
	if base.TLSHandshakeTimeout != originalTimeout {
		t.Fatal("global TLS budget changed")
	}
}

type fixtureTimeout struct{}

func (fixtureTimeout) Error() string   { return "PRIVATE_NETWORK_SENTINEL" }
func (fixtureTimeout) Timeout() bool   { return true }
func (fixtureTimeout) Temporary() bool { return true }

type failureReader struct{ err error }

func (r failureReader) Read([]byte) (int, error) { return 0, r.err }

func TestPairingTypedTimeoutClassification(t *testing.T) {
	wrapped := &url.Error{Op: "Post", URL: "https://PRIVATE_NETWORK_SENTINEL", Err: fixtureTimeout{}}
	if errors.Is(wrapped, context.DeadlineExceeded) {
		t.Fatal("fixture must distinguish typed timeout from context deadline")
	}
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"typed", &apiConnectionError{wrapped}, "timeout"},
		{"cancel_precedes_timeout", errors.Join(wrapped, context.Canceled), "cancelled"},
		{"eof", &apiConnectionError{io.EOF}, "api_unavailable"},
		{"certificate", &apiConnectionError{x509.UnknownAuthorityError{}}, "api_unavailable"},
		{"text_is_not_type", &apiConnectionError{errors.New("timeout PRIVATE_NETWORK_SENTINEL")}, "api_unavailable"},
		{"unauthorized", &APIError{Status: 401}, "api_rejected"},
		{"limited", &APIError{Status: 429}, "rate_limited"},
		{"expired", &APIError{Status: 410, Code: "pair_expired"}, "pair_expired"},
		{"revoked", &APIError{Status: 410, Code: "pair_revoked"}, "pair_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := pairingFailureMetadata(pairingFailure("claim", "unknown", tc.err))
			if failure.Code != tc.code || strings.Contains(failure.Error(), "PRIVATE_NETWORK_SENTINEL") {
				t.Fatal("wrong or unsafe classification", failure)
			}
		})
	}
}

func TestAPIResponseReadEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  io.Reader
		cause error
		code  string
	}{
		{"typed", failureReader{fixtureTimeout{}}, fixtureTimeout{}, "timeout"},
		{"deadline", failureReader{context.DeadlineExceeded}, context.DeadlineExceeded, "timeout"},
		{"cancelled", failureReader{context.Canceled}, context.Canceled, "cancelled"},
		{"ordinary", failureReader{errors.New("PRIVATE_NETWORK_SENTINEL")}, nil, "unknown"},
		{"json", strings.NewReader("PRIVATE_NETWORK_SENTINEL"), nil, "unknown"},
		{"oversize", strings.NewReader(strings.Repeat("x", 16385)), nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI()
			a.Client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(tc.body)}, nil
			})
			_, err := a.request(context.Background(), testPair("installation_fixture_00000000", "https://api.invalid"), "GET", "/", nil)
			if err == nil || err.Error() != "invalid API response" || (tc.cause != nil && !errors.Is(err, tc.cause)) || (tc.cause == nil && errors.Unwrap(err) != nil) {
				t.Fatal("unsafe response or lost cause")
			}
			if pairingFailureMetadata(pairingFailure("claim", "unknown", err)).Code != tc.code {
				t.Fatal("wrong body failure classification")
			}
		})
	}
}

func TestAPIRealBodyCancellationAndDeadline(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancelled"}[explicit], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			a := API{server.Client()}
			// Cancel after the transport receives headers, before body consumption.
			base := a.Client.Transport
			a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if explicit && err == nil {
					cancel()
				}
				return response, err
			})
			started := time.Now()
			_, err := a.request(ctx, testPair("installation_fixture_00000000", server.URL), "GET", "/", nil)
			cause, code := context.DeadlineExceeded, "timeout"
			if explicit {
				cause, code = context.Canceled, "cancelled"
			}
			var responseError *apiResponseError
			if !errors.As(err, &responseError) || !errors.Is(err, cause) || pairingFailureMetadata(pairingFailure("claim", "unknown", err)).Code != code || time.Since(started) >= 2*time.Second {
				t.Fatalf("body cause or persistence reserve lost: %v", err)
			}
		})
	}
}

func TestSetupTypedTimeoutRecovery(t *testing.T) {
	for _, scenario := range []string{"active", "query_failure", "wrong_installation", "pending", "revoked", "local_save_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			p := testPair(id, "https://api.invalid")
			calls := 0
			a := newAPI()
			a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if r.Method != "POST" || r.URL.Path != "/v1/pairs/"+p.PairID+"/claim" {
						t.Error("wrong claim")
					}
					return nil, fixtureTimeout{}
				}
				if calls != 2 || r.Method != "GET" || r.URL.Path != "/v1/pairs/"+p.PairID {
					t.Error("unexpected recovery or retry")
				}
				if scenario == "query_failure" {
					return nil, io.EOF
				}
				body := map[string]string{"status": "active", "installation_id": id}
				status := 200
				switch scenario {
				case "wrong_installation":
					body["installation_id"] = "other_installation"
				case "pending":
					body["status"] = "pending"
				case "revoked":
					status, body = 410, map[string]string{"error": "pair_revoked"}
				case "local_save_failure":
					if err := s.retirePairIfCurrent(context.Background(), p.PairID, "local_retired", false, nil); err != nil {
						t.Fatal(err)
					}
				}
				var buffer bytes.Buffer
				printJSON(&buffer, body)
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(&buffer)}, nil
			})
			result, err := setup(context.Background(), s, a, bytes.NewReader(framed(p.SetupPacket)))
			pairs, readErr := s.pairs()
			if calls != 2 || readErr != nil {
				t.Fatal("invalid recovery count or store")
			}
			switch scenario {
			case "active":
				if err != nil || result["local_saved"] != true || pairs[p.PairID].Status != "active" {
					t.Fatal("active recovery failed", err)
				}
			case "local_save_failure":
				if err == nil || pairingFailureMetadata(err).Code != "local_save_failed" {
					t.Fatal("local failure became success", err)
				}
			default:
				if err == nil || pairingFailureMetadata(err).Code != "timeout" {
					t.Fatal("original claim cause lost", err)
				}
				if scenario == "revoked" {
					if _, ok := pairs[p.PairID]; ok {
						t.Fatal("confirmed revoked pair retained")
					}
				} else if pairs[p.PairID].Status != "pending" || pairs[p.PairID].SendToken != p.SendToken {
					t.Fatal("pending responsibility lost")
				}
			}
		})
	}
}

func TestAPICompletedResponseKeepsSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newAPI()
	a.Client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"active"}`))}, nil
	})
	result, err := a.request(ctx, testPair("installation_fixture_00000000", "https://api.invalid"), "GET", "/", nil)
	if err != nil || result["status"] != "active" {
		t.Fatal("successful body replaced by late context state", err)
	}
}
