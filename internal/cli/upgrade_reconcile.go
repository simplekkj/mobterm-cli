package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type integrationUpgrade struct {
	Version          string            `json:"version"`
	Path             string            `json:"path"`
	Upgraded         bool              `json:"upgraded"`
	Verified         bool              `json:"verified"`
	IntegrationState string            `json:"integration_state"`
	Receipt          *reconcileReceipt `json:"integrations,omitempty"`
}

// Only inspect the freshly installed binary. Do not consult PATH or use the
// running (old) process's templates to interpret on-disk ownership.
func installedCLI(ctx context.Context, path string, args ...string) ([]byte, int) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	out := &limitBuffer{limit: maxReconcileReceipt}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil && !out.exceeded {
			return out.Bytes(), exit.ExitCode()
		}
		return nil, -1
	}
	return out.Bytes(), 0
}

func upgradeIntegrations(ctx context.Context, path, target string) integrationUpgrade {
	result := integrationUpgrade{Version: target, Path: path, Upgraded: true, IntegrationState: "unconfirmed"}
	query, cancel := context.WithTimeout(ctx, 10*time.Second)
	banner, code := installedCLI(query, path, "version")
	fields := strings.Fields(string(banner))
	if code != 0 || len(fields) != 4 || fields[0] != "mobterm-cli" || fields[1] != target || fields[3] != platformOS+"/"+platformArch {
		cancel()
		return result
	}
	result.Verified = true
	wire, code := installedCLI(query, path, "capabilities", "--json")
	cancel()
	var caps struct {
		V        int    `json:"v"`
		Version  string `json:"version"`
		Protocol int    `json:"hooks_reconcile"`
		Path     string `json:"hooks_reconcile_path"`
	}
	if code != 0 || json.Unmarshal(wire, &caps) != nil || caps.V != 1 || caps.Version != target {
		return result
	}
	if caps.Protocol != 1 {
		result.IntegrationState = "unsupported"
		return result
	}
	if caps.Path != path {
		return result
	}
	migrate, stop := context.WithTimeout(ctx, reconcileTimeout+2*time.Second)
	defer stop()
	wire, code = installedCLI(migrate, path, "hooks", "reconcile", "--json")
	var receipt reconcileReceipt
	if json.Unmarshal(wire, &receipt) != nil || !receipt.validUpgrade(path, target, code) {
		return result
	}
	result.Receipt = &receipt
	result.IntegrationState = receipt.Overall
	return result
}

func (r reconcileReceipt) validUpgrade(path, target string, code int) bool {
	if r.V != 1 || r.Operation != "reconcile" || r.CLIPath != path || r.CLIVersion != target ||
		len(r.Agents) != len(reconcileAgents) || !(code == 0 && r.Overall == "complete" || code == 2 && r.Overall == "attention") {
		return false
	}
	for i, a := range r.Agents {
		spec := reconcileAgents[i]
		if spec.name != a.Agent || len(spec.components) != len(a.Components) || !slices.Contains([]string{"partial", "attention", "updated", "current", "skipped"}, a.Status) {
			return false
		}
		for j, c := range a.Components {
			if c.Component != spec.components[j] || !slices.Contains([]string{"updated", "attention", "current", "skipped"}, c.Status) ||
				!slices.Contains([]string{"current", "updated", "unmanaged", "missing_agent", "probe_failed", "unsupported", "ownership_conflict", "disabled", "pending_conflict", "write_failed"}, c.Reason) ||
				!slices.Contains([]string{"unknown", "restart_required", "native_review_required"}, c.Activation) {
				return false
			}
		}
	}
	return true
}

func (r integrationUpgrade) display(h humanResult) {
	if !r.Verified {
		h.line("warning", "CLI replaced; version unconfirmed. Integrations were not run.")
		return
	}
	h.line("success", "Verified mobterm-cli "+r.Version)
	h.line("", "Path: "+terminalText(r.Path))
	if r.Receipt != nil {
		r.Receipt.display(h)
		return
	}
	if r.IntegrationState == "unsupported" {
		h.line("warning", "Integration migration unsupported. Review existing integrations in Agent management.")
		return
	}
	h.line("warning", "Integration result unconfirmed. Check with hooks reconcile --check.")
}
