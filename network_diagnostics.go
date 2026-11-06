package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"sync"
	"time"
)

type networkDiagnosticsKey struct{}
type networkDiagnostics struct {
	mu        sync.Mutex
	writer    io.Writer
	successes bool
}

func withNetworkDiagnostics(ctx context.Context, w io.Writer, successes bool) context.Context {
	return context.WithValue(ctx, networkDiagnosticsKey{}, &networkDiagnostics{writer: w, successes: successes})
}

// All strings in a record are fixed vocabulary except a strictly validated
// server UUID. Never retain URLs, addresses, headers, payloads or error text.
type networkRecord struct {
	Event       string `json:"event"`
	Method      string `json:"method"`
	Phase       string `json:"phase"`
	Outcome     string `json:"outcome"`
	DurationMS  int64  `json:"duration_ms"`
	DNSMS       *int64 `json:"dns_ms,omitempty"`
	ConnectMS   *int64 `json:"connect_ms,omitempty"`
	TLSMS       *int64 `json:"tls_ms,omitempty"`
	FirstByteMS *int64 `json:"first_byte_ms,omitempty"`
	BodyMS      *int64 `json:"body_ms,omitempty"`
	Reused      *bool  `json:"connection_reused,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Status      int    `json:"http_status,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}
type requestTrace struct {
	connected                        bool
	bodyFailed                       bool
	mu                               sync.Mutex
	sink                             *networkDiagnostics
	record                           networkRecord
	started, dns, connect, tls, body time.Time
	stage                            int
}

var diagnosticRequestID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var networkPhases = [...]string{"connection", "dns", "tcp", "tls", "request_write", "response_headers", "response_body", "complete"}

func (t *requestTrace) advance(stage int) {
	if stage > t.stage {
		t.stage = stage
	}
}
func durationMS(start time.Time) *int64 {
	if start.IsZero() {
		return nil
	}
	n := time.Since(start).Milliseconds()
	return &n
}
func traceRequest(ctx context.Context, method string) (context.Context, *requestTrace) {
	sink, _ := ctx.Value(networkDiagnosticsKey{}).(*networkDiagnostics)
	if sink == nil {
		return ctx, nil
	}
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
	default:
		method = "other"
	}
	t := &requestTrace{sink: sink, started: time.Now(), record: networkRecord{Event: "network_request", Method: method}}
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.mu.Lock(); defer t.mu.Unlock(); t.dns = time.Now(); t.advance(1) },
		DNSDone:  func(httptrace.DNSDoneInfo) { t.mu.Lock(); defer t.mu.Unlock(); t.record.DNSMS = durationMS(t.dns) },
		ConnectStart: func(string, string) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.connect.IsZero() {
				t.connect = time.Now()
			}
			t.advance(2)
		},
		ConnectDone: func(_ string, _ string, err error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.connected {
				t.record.ConnectMS = durationMS(t.connect)
				t.connected = err == nil
			}
		},
		TLSHandshakeStart: func() { t.mu.Lock(); defer t.mu.Unlock(); t.tls = time.Now(); t.advance(3) },
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.record.TLSMS = durationMS(t.tls)
			if err == nil {
				t.advance(4)
			}
			switch s.NegotiatedProtocol {
			case "h2":
				t.record.Protocol = "HTTP/2.0"
			case "http/1.1":
				t.record.Protocol = "HTTP/1.1"
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			v := info.Reused
			t.record.Reused = &v
			t.advance(4)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if info.Err == nil {
				t.advance(5)
			}
		},
		GotFirstResponseByte: func() { t.mu.Lock(); defer t.mu.Unlock(); t.record.FirstByteMS = durationMS(t.started); t.advance(5) },
	}
	return httptrace.WithClientTrace(ctx, trace), t
}
func (t *requestTrace) response(resp *http.Response) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.advance(6)
	t.body = time.Now()
	if resp.StatusCode >= 100 && resp.StatusCode <= 599 {
		t.record.Status = resp.StatusCode
	}
	switch resp.Proto {
	case "HTTP/1.0", "HTTP/1.1", "HTTP/2.0":
		t.record.Protocol = resp.Proto
	}
	if id := resp.Header.Get("X-Request-ID"); diagnosticRequestID.MatchString(id) {
		t.record.RequestID = id
	}
}
func (t *requestTrace) bodyResult(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bodyFailed = err != nil
}
func (t *requestTrace) finish(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.record.DurationMS = time.Since(t.started).Milliseconds()
	t.record.BodyMS = durationMS(t.body)
	t.record.Outcome = "success"
	switch {
	case err == nil:
		t.stage = 7
	case errors.Is(err, context.Canceled):
		t.record.Outcome = "cancelled"
	case errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err):
		t.record.Outcome = "timeout"
	default:
		var dns *net.DNSError
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		var hostname x509.HostnameError
		var api *APIError
		switch {
		case errors.As(err, &dns):
			t.record.Outcome = "dns_error"
		case errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname):
			t.record.Outcome = "certificate_error"
		case t.bodyFailed:
			t.record.Outcome = "response_read_error"
		case errors.As(err, &api):
			t.record.Outcome = "http_error"
			t.stage = 7
		case t.stage == 6:
			t.record.Outcome = "invalid_response"
		default:
			t.record.Outcome = "connection_error"
		}
	}
	t.record.Phase = networkPhases[t.stage]
	raw, _ := json.Marshal(t.record)
	t.mu.Unlock()
	if err == nil && !t.sink.successes {
		return
	}
	t.sink.mu.Lock()
	defer t.sink.mu.Unlock()
	_, _ = fmt.Fprintf(t.sink.writer, "mobterm-cli network: %s\n", raw)
}
