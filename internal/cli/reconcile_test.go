package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runReconcileTest(t *testing.T, check bool, code int) reconcileReceipt {
	t.Helper()
	args := []string{"hooks", "reconcile", "--json"}
	if check {
		args = append(args, "--check")
	}
	var out, diag bytes.Buffer
	if got := run(args, nil, &out, &diag); got != code || diag.Len() != 0 || out.Len() > maxReconcileReceipt || bytes.Contains(out.Bytes(), []byte{27}) {
		t.Fatalf("exit=%d out=%s diagnostic=%s", got, out.String(), diag.String())
	}
	var r reconcileReceipt
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.V != 1 || len(r.Agents) != 4 || r.CLIVersion != version || !filepath.IsAbs(r.CLIPath) {
		t.Fatalf("invalid receipt %v: %+v", err, r)
	}
	return r
}

func TestReconcileNoIdentityOrUnownedProbes(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	for _, check := range []bool{true, false} {
		r := runReconcileTest(t, check, 0)
		if r.RequiresAction || r.Overall != "complete" {
			t.Fatal(r)
		}
		for _, a := range r.Agents {
			if a.Status != "skipped" {
				t.Fatal(a)
			}
			for _, c := range a.Components {
				if c.Reason != "unmanaged" {
					t.Fatal(c)
				}
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), a.Agent+".probes")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unmanaged agent was probed")
			}
		}
		if _, err := os.Lstat(s.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unmanaged account was changed")
		}
	}
}

func TestReconcileIndependentProbeFailures(t *testing.T) {
	s, bin := discoveryEnvironment(t)
	if err := ensureDir(s.Dir, true); err != nil {
		t.Fatal(err)
	}
	for _, spec := range reconcileAgents {
		name := nativeRecordName(spec.name)
		if spec.name == "codex" || spec.name == "claude" {
			name = "hooks-" + spec.name + ".json"
		}
		if err := s.writeJSON(name, map[string]string{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	writeLayoutFile(t, filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0700)
	writeLayoutFile(t, filepath.Join(bin, "opencode"), []byte("#!/bin/sh\necho 'opencode v1.0.0'\n"), 0700)
	for _, check := range []bool{true, false} {
		r := runReconcileTest(t, check, 2)
		if r.Overall != "attention" || !r.RequiresAction {
			t.Fatal(r)
		}
		for i, want := range []string{"missing_agent", "probe_failed", "unsupported", "ownership_conflict"} {
			if got := r.Agents[i].Components[0].Reason; got != want {
				t.Errorf("%s reason %s, want %s", r.Agents[i].Agent, got, want)
			}
		}
	}
}

func TestReconcilePreservesPartialAgentFacts(t *testing.T) {
	r := reconcileReceipt{Agents: []reconcileAgent{{Agent: "claude", Components: []reconcileComponent{
		{"hooks", "updated", "updated", "restart_required"}, {"adapter", "attention", "ownership_conflict", "unknown"},
	}}}}
	r.summarize()
	if r.Overall != "attention" || !r.RequiresAction || r.Agents[0].Status != "partial" || r.Agents[0].Components[0].Status != "updated" {
		t.Fatal(r)
	}
}

func TestReplacedCLIReconcile(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	path := filepath.Join(t.TempDir(), "custom cli")
	const target = "v1.2.3"
	capabilities := fmt.Sprintf(`{"v":1,"version":"v1.2.3","hooks_reconcile":1,"hooks_reconcile_path":%q}`, path)
	r := reconcile(context.Background(), s, path, false)
	r.CLIVersion = target
	receipt, _ := json.Marshal(r)
	r.Agents[0].Components[0] = reconcileComponent{"hooks", "attention", "ownership_conflict", "unknown"}
	r.summarize()
	partial, _ := json.Marshal(r)
	for _, tc := range []struct {
		name, banner, caps, receipt string
		exit                        int
		verified                    bool
		state                       string
	}{
		{"old", target, `{"v":1,"version":"v1.2.3"}`, "", 0, true, "unsupported"},
		{"different-version", "v1.0.0", capabilities, "", 0, false, "unconfirmed"},
		{"different-capability-path", target, strings.Replace(capabilities, path, "/other", 1), string(receipt), 0, true, "unconfirmed"},
		{"complete", target, capabilities, string(receipt), 0, true, "complete"},
		{"partial", target, capabilities, string(partial), 2, true, "attention"},
		{"broken-output", target, capabilities, "{", 2, true, "unconfirmed"},
		{"wrong-path", target, capabilities, strings.Replace(string(receipt), path, "/other", 1), 0, true, "unconfirmed"},
		{"bad-exit", target, capabilities, string(receipt), 1, true, "unconfirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\nversion) echo 'mobterm-cli %s commit %s/%s';;\n'capabilities --json') echo %s;;\n'hooks reconcile --json') echo %s; exit %d;;\n*) exit 1;;\nesac\n", tc.banner, platformOS, platformArch, shellQuote(tc.caps), shellQuote(tc.receipt), tc.exit)
			writeLayoutFile(t, path, []byte(source), 0700)
			result := upgradeIntegrations(context.Background(), path, target)
			if !result.Upgraded || result.Verified != tc.verified || result.IntegrationState != tc.state {
				t.Fatal(result)
			}
			if tc.state == "attention" && (result.Receipt == nil || result.Receipt.Agents[0].Components[0].Reason != "ownership_conflict") {
				t.Fatal("partial receipt lost")
			}
		})
	}
}
