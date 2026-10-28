package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A real blocked version process consumes the auxiliary budget. No timed sleep
// or production test hook is needed: the command's context releases the probe.
func TestPairStatusSurvivesAuxiliaryDeadline(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	fifo := filepath.Join(t.TempDir(), "blocked-probe")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAIR_TEST_FIFO", fifo)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nread value < \"$PAIR_TEST_FIFO\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		printJSON(w, map[string]any{"v": 1, "pair_id": "pair_fixture_000000000000000001", "installation_id": id, "status": "active"})
	}))
	defer server.Close()
	p := testPair(id, server.URL)
	if err := s.savePair(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	result, err := status(ctx, s, API{server.Client()}, p.PairID, false)
	if err != nil || result["status"] != "active" || calls.Load() != 1 {
		t.Fatalf("healthy pair lost to auxiliary budget: status=%v calls=%d err=%v", result["status"], calls.Load(), err)
	}
	for _, field := range []string{"capabilities", "hooks", "health", "outbox"} {
		if result[field] == nil {
			t.Fatalf("lost public diagnostic field %s", field)
		}
	}
	probe := result["capabilities"].(map[string]any)["codex"].(map[string]any)
	if probe["probe_status"] != probeTimeout || result["outbox"].(map[string]any)["error"] == nil {
		t.Fatal("unfinished auxiliary work must remain visible")
	}
}

func TestPairStatusCoreFailuresStayFailures(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, scenario := range []string{"missing_local", "local_identity", "unreachable", "timeout", "unauthorized", "revoked", "expired", "remote_identity", "missing_identity", "wrong_pair", "missing_status", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			p := testPair(id, "https://api.invalid")
			if scenario == "local_identity" {
				p.InstallationID = "different_installation_0000000000"
			}
			if scenario != "missing_local" {
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			a := API{&http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if scenario == "unreachable" {
					return nil, errors.New("private network failure")
				}
				if scenario == "timeout" {
					return nil, context.DeadlineExceeded
				}
				body := map[string]any{"pair_id": p.PairID, "installation_id": id, "status": "active"}
				httpStatus := 200
				switch scenario {
				case "unauthorized":
					httpStatus, body = 401, map[string]any{"error": "unauthorized"}
				case "revoked", "expired":
					httpStatus, body = 410, map[string]any{"error": "pair_" + scenario}
				case "remote_identity":
					body["installation_id"] = "different_installation_0000000000"
				case "missing_identity":
					delete(body, "installation_id")
				case "wrong_pair":
					body["pair_id"] = "different_pair_0000000000"
				case "missing_status":
					delete(body, "status")
				case "pending":
					body["status"] = "pending"
				}
				b, _ := json.Marshal(body)
				return &http.Response{StatusCode: httpStatus, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b))}, nil
			})}}
			result, err := status(context.Background(), s, a, p.PairID, false)
			if err == nil || result["outbox"] == nil {
				t.Fatal("core failure hidden or diagnostics removed", err)
			}
			if (scenario == "missing_local" || scenario == "local_identity") && calls != 0 {
				t.Fatal("invalid local identity reached API")
			}
			if scenario == "unreachable" && pairingFailureMetadata(err).Code != "api_unavailable" || scenario == "timeout" && pairingFailureMetadata(err).Code != "timeout" {
				t.Fatal("transport classification lost", err)
			}
			if scenario == "missing_identity" && pairingFailureMetadata(err).Code != "pair_status_failed" {
				t.Fatal("missing identity is not evidence of a changed installation", err)
			}
		})
	}
}

func TestPairStatusExplicitCancellationIsNotSuccess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	if err := s.savePair(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := API{&http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
		cancel() // A late cancellation still wins over the already received body.
		b, _ := json.Marshal(map[string]any{"pair_id": p.PairID, "installation_id": id, "status": "active"})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}}
	_, err := status(ctx, s, a, p.PairID, false)
	if !errors.Is(err, context.Canceled) || pairingFailureMetadata(err).Code != "cancelled" {
		t.Fatal("explicit cancellation became success", err)
	}
}

func TestPairingRunEmitsOneSafeFailure(t *testing.T) {
	const secret = "PRIVATE_ERROR_AND_TOKEN_DO_NOT_DISPLAY"
	for _, scenario := range []struct {
		name, stage, code string
		http              int
	}{
		{"bad_frame", "setup_frame", "invalid_frame", 0},
		{"default_store", "configuration", "local_config_failed", 0},
		{"installation", "installation", "local_config_failed", 0},
		{"identity", "installation", "installation_changed", 0},
		{"local_save", "local_save", "local_save_failed", 0},
		{"unreachable", "claim", "api_unavailable", 0},
		{"timeout", "claim", "timeout", 0},
		{"typed_timeout", "claim", "timeout", 0},
		{"body_timeout", "claim", "timeout", 0},
		{"body_cancelled", "claim", "cancelled", 0},
		{"http_unknown", "claim", "api_rejected", 503},
		{"rate_limit", "claim", "rate_limited", 429},
		{"unknown", "claim", "unknown", 0},
		{"status_http", "pair_status", "api_rejected", 401},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := testStore(t)
			t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
			t.Setenv("PATH", t.TempDir())
			id, _ := s.installation()
			p := testPair(id, "https://api.invalid")
			args := []string{"setup", "--stdin-frame"}
			input := framed(p.SetupPacket)
			switch scenario.name {
			case "bad_frame":
				input = framed(map[string]any{"v": 1, "secret": secret})
			case "default_store":
				t.Setenv("MOBTERM_CONFIG_DIR", "")
				t.Setenv("HOME", "")
			case "installation":
				args = []string{"status", "--installation-json"}
				if err := os.WriteFile(filepath.Join(s.Dir, "installation.json"), []byte(secret), 0600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				p.InstallationID = "different_installation_0000000000"
				input = framed(p.SetupPacket)
			case "local_save":
				if err := os.WriteFile(filepath.Join(s.Dir, "pair-history.json"), []byte(secret), 0600); err != nil {
					t.Fatal(err)
				}
			case "status_http":
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				args = []string{"status", "--json", "--pair", p.PairID}
			}
			oldTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
				if scenario.name == "unreachable" {
					return nil, errors.New(secret)
				}
				if scenario.name == "timeout" {
					return nil, context.DeadlineExceeded
				}
				if scenario.name == "typed_timeout" {
					return nil, fixtureTimeout{}
				}
				if scenario.name == "body_timeout" || scenario.name == "body_cancelled" {
					var cause error = fixtureTimeout{}
					if scenario.name == "body_cancelled" {
						cause = errors.Join(errors.New(secret), context.Canceled)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(failureReader{cause})}, nil
				}
				if scenario.name == "unknown" {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(secret))}, nil
				}
				statusCode := scenario.http
				if statusCode == 0 {
					t.Error("unexpected API call")
					statusCode = 500
				}
				body, _ := json.Marshal(map[string]string{"error": secret})
				return &http.Response{StatusCode: statusCode, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			var out, diag bytes.Buffer
			if code := run(args, bytes.NewReader(input), &out, &diag); code != 1 {
				t.Fatal("failure did not exit 1", code)
			}
			if scenario.name == "typed_timeout" || scenario.name == "body_timeout" || scenario.name == "body_cancelled" {
				for _, marker := range []string{secret, "PRIVATE_NETWORK_SENTINEL", p.SendToken, p.EncryptionKey, p.APIURL} {
					if strings.Contains(out.String(), marker) || strings.Contains(diag.String(), marker) {
						t.Fatal("private data reached stdout or stderr")
					}
				}
			}
			var response map[string]json.RawMessage
			decoder := json.NewDecoder(&out)
			if err := decoder.Decode(&response); err != nil {
				t.Fatal("missing JSON failure", err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatal("more than one JSON response", err)
			}
			var failure PairingFailure
			if err := json.Unmarshal(response["failure"], &failure); err != nil || failure.Stage != scenario.stage || failure.Code != scenario.code || failure.HTTPStatus != scenario.http {
				t.Fatalf("unexpected failure: %+v err=%v", failure, err)
			}
			if strings.Contains(string(response["failure"]), secret) || strings.Contains(diag.String(), secret) || bytes.Contains(response["failure"], []byte(p.SendToken)) || bytes.Contains(response["failure"], []byte(p.EncryptionKey)) {
				t.Fatal("private data reached new diagnostics")
			}
			if scenario.name == "status_http" && (response["pair_id"] == nil || response["status"] == nil || response["local_saved"] == nil || response["outbox"] == nil) {
				t.Fatal("status result was replaced by a generic error")
			}
		})
	}
}
