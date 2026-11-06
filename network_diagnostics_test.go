package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func diagnosticRecords(t *testing.T, b *bytes.Buffer) []networkRecord {
	t.Helper()
	var records []networkRecord
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var r networkRecord
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "mobterm-cli network: ")), &r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	return records
}
func TestNetworkDiagnosticsStagesAndRedaction(t *testing.T) {
	for _, phase := range []string{"dns", "tcp", "tls", "request_write", "response_headers", "response_body", "invalid_json", "http_error", "error_body_read"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var log bytes.Buffer
				ctx := withNetworkDiagnostics(context.Background(), &log, false)
				a := newAPI()
				a.Client.Transport = releaseTransport(func(r *http.Request) (*http.Response, error) {
					tr := httptrace.ContextClientTrace(r.Context())
					tr.DNSStart(httptrace.DNSStartInfo{Host: "PRIVATE_DNS"})
					time.Sleep(time.Millisecond)
					if phase == "dns" {
						e := &net.DNSError{Err: "PRIVATE_ERROR", Name: "PRIVATE_DNS"}
						tr.DNSDone(httptrace.DNSDoneInfo{Err: e})
						return nil, e
					}
					tr.DNSDone(httptrace.DNSDoneInfo{})
					tr.ConnectStart("tcp", "PRIVATE_ADDRESS")
					time.Sleep(time.Millisecond)
					if phase == "tcp" {
						return nil, fixtureTimeout{}
					}
					tr.ConnectDone("tcp", "PRIVATE_ADDRESS", nil)
					tr.TLSHandshakeStart()
					time.Sleep(time.Millisecond)
					if phase == "tls" {
						tr.TLSHandshakeDone(tls.ConnectionState{}, fixtureTimeout{})
						return nil, fixtureTimeout{}
					}
					tr.TLSHandshakeDone(tls.ConnectionState{NegotiatedProtocol: "h2"}, nil)
					tr.GotConn(httptrace.GotConnInfo{Reused: true})
					if phase == "request_write" {
						tr.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("PRIVATE_ERROR")})
						return nil, errors.New("PRIVATE_ERROR")
					}
					tr.WroteRequest(httptrace.WroteRequestInfo{})
					if phase == "response_headers" {
						return nil, fixtureTimeout{}
					}
					tr.GotFirstResponseByte()
					headers := http.Header{"X-Request-Id": []string{"PRIVATE_HEADER"}, "Authorization": []string{"PRIVATE_TOKEN"}}
					var body io.Reader = strings.NewReader("PRIVATE_RESPONSE")
					if phase == "response_body" {
						body = failureReader{fixtureTimeout{}}
					}
					status := 200
					if phase == "error_body_read" {
						status = 503
						body = failureReader{errors.New("PRIVATE_READ_ERROR")}
					}
					if phase == "http_error" {
						status = 503
						body = strings.NewReader(`{"error":"PRIVATE_CODE"}`)
					}
					return &http.Response{StatusCode: status, Proto: "HTTP/2.0", Header: headers, Body: io.NopCloser(body)}, nil
				})
				p := testPair("PRIVATE_INSTALLATION", "https://PRIVATE_URL")
				p.SendToken = "PRIVATE_TOKEN"
				_, err := a.request(ctx, p, "POST", "/PRIVATE_PATH", map[string]string{"secret": "PRIVATE_PAYLOAD"})
				if err == nil {
					t.Fatal("expected failure")
				}
				if strings.Contains(log.String(), "PRIVATE_") {
					t.Fatal("private data in diagnostics")
				}
				records := diagnosticRecords(t, &log)
				if len(records) != 1 {
					t.Fatal(records)
				}
				want := phase
				if phase == "invalid_json" || phase == "error_body_read" {
					want = "response_body"
				}
				if phase == "http_error" {
					want = "complete"
				}
				if records[0].Phase != want || records[0].RequestID != "" {
					t.Fatal(records[0])
				}
				switch phase {
				case "error_body_read":
					if records[0].Outcome != "response_read_error" {
						t.Fatal(records[0])
					}
				case "tcp", "tls", "response_headers", "response_body":
					if records[0].Outcome != "timeout" {
						t.Fatal(records[0])
					}
				}
			})
		})
	}
}
func TestNetworkDiagnosticsRealReuseAndOptIn(t *testing.T) {
	const requestID = "12345678-1234-4234-8234-123456789012"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", requestID)
		io.WriteString(w, `{"status":"active"}`)
	}))
	defer server.Close()
	a := newAPI()
	a.Client.Transport = server.Client().Transport
	var log bytes.Buffer
	p := testPair("installation_fixture_00000000", server.URL)
	if _, err := a.request(withNetworkDiagnostics(context.Background(), &log, false), p, "GET", "/", nil); err != nil {
		t.Fatal(err)
	}
	if log.Len() != 0 {
		t.Fatal("success logged without opt-in")
	}
	ctx := withNetworkDiagnostics(context.Background(), &log, true)
	for i := 0; i < 2; i++ {
		if _, err := a.request(ctx, p, "GET", "/", nil); err != nil {
			t.Fatal(err)
		}
	}
	records := diagnosticRecords(t, &log)
	if len(records) != 2 || records[1].Reused == nil || !*records[1].Reused || records[1].RequestID != requestID || records[1].Outcome != "success" {
		t.Fatal(records)
	}
}
func TestNetworkDiagnosticsConcurrentAndCancellation(t *testing.T) {
	var log bytes.Buffer
	ctx := withNetworkDiagnostics(context.Background(), &log, false)
	a := newAPI()
	a.Client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.request(ctx, testPair("installation_fixture_00000000", "https://api.invalid"), "GET", "/", nil)
		}()
	}
	wg.Wait()
	records := diagnosticRecords(t, &log)
	if len(records) != 16 {
		t.Fatal(len(records))
	}
	for _, r := range records {
		if r.Outcome != "cancelled" {
			t.Fatal(r)
		}
	}
}
