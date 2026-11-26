package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	reconcileTimeout     = time.Minute
	reconcileWriteBudget = 8 * time.Second
	maxReconcileReceipt  = 32 * 1024
)

type reconcileComponent struct {
	Component  string `json:"component"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Activation string `json:"activation"`
}
type reconcileAgent struct {
	Agent      string               `json:"agent"`
	Status     string               `json:"status"`
	Components []reconcileComponent `json:"components"`
}
type reconcileReceipt struct {
	V              int              `json:"v"`
	Operation      string           `json:"operation"`
	CLIVersion     string           `json:"cli_version"`
	CLIPath        string           `json:"cli_path"`
	Overall        string           `json:"overall"`
	RequiresAction bool             `json:"requires_action"`
	Agents         []reconcileAgent `json:"agents"`
}

var reconcileAgents = []struct {
	name       string
	components []string
}{
	{"codex", []string{"hooks"}},
	{"claude", []string{"hooks", "adapter"}},
	{"opencode", []string{"adapter"}},
	{"pi", []string{"adapter"}},
}

// SSH clients bind the receipt to the absolute spelling they executed. Keep
// that spelling for system aliases only after checking file identity. Owned
// configuration still uses os.Executable, as it did at the original install.
func reconcileInvocationPath(executable, invoked string) string {
	if executable == invoked || !filepath.IsAbs(invoked) {
		return executable
	}
	running, err := os.Stat(executable)
	if err != nil {
		return executable
	}
	selected, err := os.Stat(invoked)
	if err == nil && os.SameFile(running, selected) {
		return invoked
	}
	return executable
}

// Check the directory and the bounded set of ownership records without
// initializing a store, following a symlink or opening a special file.
func reconcileCandidate(s Store, agent, component string) (bool, error) {
	if err := nativeRealParents(filepath.Join(s.Dir, "ownership")); err != nil {
		return false, err
	}
	checkNode := func(path string, directory bool) (bool, error) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
			return false, errors.New("unsafe ownership record")
		}
		return true, nil
	}
	if exists, err := checkNode(s.Dir, true); err != nil || !exists {
		return false, err
	}
	if err := reconcileSafeParents(filepath.Join(s.Dir, "ownership")); err != nil {
		return false, err
	}
	names := []string{nativeRecordName(agent)}
	if component == "hooks" {
		names = []string{"hooks-" + agent + ".json", hookTransactionName(agent)}
	}
	found := false
	for _, name := range names {
		exists, err := checkNode(filepath.Join(s.Dir, name), false)
		if err != nil {
			return false, err
		}
		found = found || exists
	}
	return found, nil
}

func reconcile(ctx context.Context, s Store, executable string, check bool) reconcileReceipt {
	result := reconcileReceipt{V: 1, Operation: "reconcile", CLIPath: reconcileInvocationPath(executable, os.Args[0]), CLIVersion: version}
	if check {
		result.Operation = "check"
	}
	probes := make([]agentProbe, len(reconcileAgents))
	var pending sync.WaitGroup
	for i, spec := range reconcileAgents {
		agent := reconcileAgent{Agent: spec.name}
		needsProbe := false
		for _, component := range spec.components {
			row := reconcileComponent{Component: component, Activation: "unknown"}
			switch exists, err := reconcileCandidate(s, spec.name, component); {
			case err != nil:
				row.Status, row.Reason = "attention", "ownership_conflict"
			case !exists:
				row.Status, row.Reason = "skipped", "unmanaged"
			default:
				needsProbe = true
			}
			agent.Components = append(agent.Components, row)
		}
		result.Agents = append(result.Agents, agent)
		if needsProbe {
			pending.Go(func() { probes[i] = probeAgent(ctx, spec.name) })
		}
	}
	pending.Wait()
	for i := range result.Agents {
		a := &result.Agents[i]
		writeCtx, cancel := context.WithTimeout(ctx, reconcileWriteBudget)
		for j, row := range a.Components {
			if row.Status != "" {
				continue
			}
			p := probes[i]
			switch {
			case p.status == probeNotFound:
				row.Status, row.Reason = "skipped", "missing_agent"
			case p.status != probeOK || p.reason == "unknown_output":
				row.Status, row.Reason = "skipped", "probe_failed"
			case p.reason != "":
				row.Status, row.Reason = "skipped", "unsupported"
			case row.Component == "hooks":
				row = reconcileClassic(writeCtx, s, a.Agent, executable, p, check)
			default:
				row = reconcileNative(writeCtx, s, a.Agent, executable, p, check)
			}
			a.Components[j] = row
		}
		cancel()
	}
	result.summarize()
	return result
}

func (r *reconcileReceipt) summarize() {
	r.Overall, r.RequiresAction = "complete", false
	for i := range r.Agents {
		a := &r.Agents[i]
		var attention, done bool
		a.Status = "skipped"
		for _, c := range a.Components {
			switch c.Status {
			case "attention":
				a.Status = "attention"
				attention = true
			case "skipped":
				attention = attention || c.Reason != "unmanaged"
			case "updated", "available":
				done = true
				a.Status = c.Status
			case "current":
				done = true
				if a.Status == "skipped" {
					a.Status = "current"
				}
			}
			if c.Status == "available" {
				r.RequiresAction = true
			}
		}
		if attention {
			r.Overall, r.RequiresAction = "attention", true
			if done {
				a.Status = "partial"
			}
		}
	}
}

func (r reconcileReceipt) display(h humanResult) {
	h.line("title", "Agent integrations ("+r.Operation+")")
	for _, a := range r.Agents {
		for _, c := range a.Components {
			line := fmt.Sprintf("%s %s: %s (%s)", a.Agent, c.Component, c.Status, strings.ReplaceAll(c.Reason, "_", " "))
			switch c.Activation {
			case "native_review_required":
				line += ". Verify trust in Codex /hooks. Runtime loading unknown."
			case "restart_required":
				line += ". Restart the Agent."
			default:
				if c.Reason != "unmanaged" {
					line += ". Runtime loading unknown."
				}
			}
			if c.Reason == "pending_conflict" {
				line += " Inspect the pending operation with its original CLI."
			}
			h.line("", line)
		}
	}
}
