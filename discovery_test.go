package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func discoveryEnvironment(t *testing.T) (Store, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", root)
	t.Setenv("MOBTERM_CONFIG_DIR", filepath.Join(root, "mobterm"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(root, "opencode"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	t.Setenv("PI_PACKAGE_DIR", "")
	for agent, policy := range agentCompatibilities {
		// User configuration writes made by --version remain in the temporary HOME.
		script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf 'probe\\n' >> " + shellQuote(filepath.Join(root, agent+".probes")) + "\nprintf 'native migration' > \"$HOME/migration\"\nprintf '%s\\n' " + shellQuote(agentOutput(agent, policy.reference)) + "\n"
		if err := os.WriteFile(filepath.Join(bin, agent), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := defaultStore()
	if err != nil {
		t.Fatal(err)
	}
	return s, bin
}

func TestAgentDiscoveryContractAndNoInitialization(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	var out, diag bytes.Buffer
	if code := run([]string{"status", "--agents-json"}, strings.NewReader(""), &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("discovery failed: %d %s", code, diag.String())
	}
	if out.Len() > 16384 {
		t.Fatal("unbounded response")
	}
	if _, err := os.Lstat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery initialized persistent state", err)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv("HOME"), "migration")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("probe changed real HOME", err)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatal("probe temporary directories were not removed", err)
	}
	var actual map[string]any
	if json.Unmarshal(out.Bytes(), &actual) != nil {
		t.Fatal("invalid JSON")
	}
	for agent := range agentCompatibilities {
		probes, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), agent+".probes"))
		if err != nil || string(probes) != "probe\n" {
			t.Fatal("agent must be probed exactly once", agent, err)
		}
	}
	// This exact fixture is also consumed by the App parser tests.
	fixture, err := os.ReadFile("testdata/agents-discovery-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if json.Unmarshal(fixture, &expected) != nil {
		t.Fatal("invalid shared fixture")
	}
	actual["version"], actual["commit"] = "dev", "unknown"
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("discovery contract drift:\n%s", out.String())
	}
}

func TestAgentDiscoveryDoesNotReadPairsOrOutboxOrRequestAPI(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	// The original status would reject this identity and cannot safely decode
	// these unrelated files. Discovery must not depend on any of them.
	files := map[string][]byte{}
	for _, name := range []string{"installation.json", "pairs.json", "events.json", "outbox"} {
		files[name] = []byte("not JSON; preserve user data")
		if err := os.WriteFile(filepath.Join(s.Dir, name), files[name], 0600); err != nil {
			t.Fatal(err)
		}
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	requests := 0
	http.DefaultTransport = releaseTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected API request")
	})
	var out, diag bytes.Buffer
	if run([]string{"status", "--agents-json"}, strings.NewReader(""), &out, &diag) != 0 {
		t.Fatal(diag.String())
	}
	if requests != 0 {
		t.Fatal("discovery accessed notification API")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != len(files) {
		t.Fatal("discovery created state files", err)
	}
	for name, before := range files {
		after, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("discovery modified unrelated state", name, err)
		}
	}
}

func TestAgentDiscoveryRejectsMixedFlagsBeforeSideEffects(t *testing.T) {
	for _, args := range [][]string{
		{"--agents-json", "--json"}, {"--json", "--agents-json"}, {"--agents-json", "--installation-json"},
		{"--agents-json", "--pair", "pair"}, {"--agents-json", "--pair="}, {"--agents-json", "--json=false"},
		{"--agents-json", "--agents-json"}, {"--agents-json=false"}, {"--agents-json", "extra"}, {"--agents-json", "--unknown"},
		{"--json", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s, _ := discoveryEnvironment(t)
			var out, diag bytes.Buffer
			if run(append([]string{"status"}, args...), strings.NewReader(""), &out, &diag) == 0 {
				t.Fatal("invalid arguments accepted")
			}
			if _, err := os.Lstat(s.Dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid arguments initialized state")
			}
			for agent := range agentCompatibilities {
				if _, err := os.Lstat(filepath.Join(os.Getenv("HOME"), agent+".probes")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("invalid arguments probed agent")
				}
			}
		})
	}
}

func TestAgentDiscoveryMixedResults(t *testing.T) {
	s, bin := discoveryEnvironment(t)
	if err := os.Remove(filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\nprintf 'opencode v1.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := discoverAgents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := result["capabilities"].(map[string]any)
	for agent, expected := range map[string]probeStatus{"codex": probeOK, "claude": probeNotFound, "opencode": probeOK, "pi": probeFailed} {
		capability := capabilities[agent].(map[string]any)
		if capability["probe_status"] != expected {
			t.Fatal(agent, capability)
		}
	}
	if capabilities["codex"].(map[string]any)["supported"] != true || capabilities["opencode"].(map[string]any)["version_rejection"] != "below_minimum" {
		t.Fatal("version policy changed")
	}
	health := result["health"].(map[string]agentHealth)
	if health["claude"].State != "not_installed" || health["pi"].State != "unknown" || health["opencode"].State != "unsupported" {
		t.Fatal("runtime failure misclassified", health)
	}
	for _, h := range health {
		if h.ConfigurationState != "not_configured" || h.Loaded != "unknown" {
			t.Fatal("runtime overwrote configuration", h)
		}
	}
}

func TestAgentProbeTypedFailures(t *testing.T) {
	for _, kind := range []string{"missing", "permission", "node", "execution", "pi_package", "timeout", "cancelled", "unknown_output", "prerelease"} {
		t.Run(kind, func(t *testing.T) {
			_, bin := discoveryEnvironment(t)
			agent := "codex"
			expected := probeFailed
			ctx := context.Background()
			var cancel context.CancelFunc
			switch kind {
			case "missing":
				os.Remove(filepath.Join(bin, agent))
				expected = probeNotFound
			case "permission":
				os.Chmod(filepath.Join(bin, agent), 0600)
			case "node":
				os.WriteFile(filepath.Join(bin, agent), []byte("#!/usr/bin/env node\n"), 0700)
			case "execution":
				os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nexit 7\n"), 0700)
			case "pi_package":
				agent = "pi"
				t.Setenv("PI_PACKAGE_DIR", "file://remote.invalid/path")
			case "timeout":
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				expected = probeTimeout
			case "cancelled":
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				expected = probeCancelled
			case "unknown_output":
				os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nprintf 'unknown version\\n'\n"), 0700)
				expected = probeOK
			case "prerelease":
				os.WriteFile(filepath.Join(bin, agent), []byte("#!/bin/sh\nprintf 'codex-cli 99.0.0-beta\\n'\n"), 0700)
				expected = probeOK
			}
			if cancel != nil {
				defer cancel()
			}
			probe := probeAgent(ctx, agent)
			if probe.status != expected || probe.reason == "" {
				t.Fatalf("%s classified as %#v", kind, probe)
			}
		})
	}
}

func TestAgentDiscoveryCancellationDoesNotPublishSnapshot(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := discoverAgents(ctx, s)
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatal("cancelled discovery published a snapshot")
	}
}

func TestAgentDiscoveryKeepsInstalledConfigurationWithoutRuntime(t *testing.T) {
	s, bin := discoveryEnvironment(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := changeHooks(context.Background(), s, "codex", "install", "", exe, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	result, err := discoverAgents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	health := result["health"].(map[string]agentHealth)["codex"]
	if health.State != "not_installed" || health.ConfigurationState != "complete" || !result["hooks"].(map[string]bool)["codex"] {
		t.Fatal("missing runtime erased installed integration", health)
	}
	if err := changeHooks(context.Background(), s, "codex", "uninstall", "", exe, true); err != nil {
		t.Fatal("missing runtime prevented removal", err)
	}
}

func TestAgentDiscoveryLocalTimeoutRetainsOtherAgents(t *testing.T) {
	s, bin := discoveryEnvironment(t)
	// Exercise the real per-Agent budget, independently of the overall context.
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := discoverAgents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := result["capabilities"].(map[string]any)
	if capabilities["pi"].(map[string]any)["probe_status"] != probeTimeout || capabilities["codex"].(map[string]any)["supported"] != true {
		t.Fatal("one timeout swallowed successful probes")
	}
	if time.Since(started) > 9*time.Second {
		t.Fatal("probe exceeded bounded cleanup budget")
	}
}
