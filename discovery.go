package main

import (
	"context"
	"strings"
)

// agentDiagnostics only probes isolated runtimes and reads owned hook configuration.
// It must never initialize installation identity or access pairs, outbox or API.
func agentDiagnostics(ctx context.Context, s Store) map[string]any {
	result := map[string]any{}
	result["capabilities"] = map[string]any{"codex": map[string]any{"turn_completed": "agent-turn-complete", "needs_input": false, "reason": "PermissionRequest is pre-decision; no verified external human-wait event"}, "claude": map[string]any{"turn_completed": "native idle_prompt after user prompt; delayed by Claude", "needs_input": []string{"permission_prompt", "elicitation_dialog"}}, "herdr": map[string]any{"versions": []string{"0.9.1", "0.9.3"}, "source": "native Agent hooks only", "protocol": 22}}
	type probeResult struct {
		agent string
		probe agentProbe
	}
	capabilities := result["capabilities"].(map[string]any)
	capabilities["target"] = map[string]any{"resolve": true, "tmux_occupant": "tmux-proc-v1"}
	capabilities["opencode"] = map[string]any{"turn_completed": "session.execution.succeeded: visible root only", "needs_input": []string{"permission.asked", "form.created"}, "managed_hooks": true, "adapter_revision": 1}
	capabilities["pi"] = map[string]any{"turn_completed": "agent_settled: aborted=false in TUI", "needs_input": false, "reason": "No authoritative pending human-request API", "managed_hooks": true, "adapter_revision": 1}
	probes := make(chan probeResult, 4)
	for _, agent := range []string{"codex", "claude", "opencode", "pi"} {
		go func(agent string) { probes <- probeResult{agent, probeAgent(ctx, agent)} }(agent)
	}
	for range 4 {
		completed := <-probes
		agent, probe := completed.agent, completed.probe
		capability := result["capabilities"].(map[string]any)[agent].(map[string]any)
		policy := agentCompatibilities[agent]
		capability["version"] = policy.reference
		capability["version_role"] = "validation_reference"
		capability["validation_reference"] = policy.reference
		capability["minimum_version"] = policy.minimum.String()
		capability["supported_range"] = ">=" + policy.minimum.String() + " stable"
		capability["probe_status"] = probe.status
		capability["installed"] = probe.installed
		capability["actual_version"] = probe.actual
		capability["supported"] = probe.reason == ""
		if probe.reason != "" {
			capability["version_rejection"] = probe.reason
		}
	}
	installed := map[string]bool{}
	for _, agent := range []string{"codex", "claude"} {
		installed[agent] = false
		var r HookRecord
		if s.readJSON("hooks-"+agent+".json", &r) == nil {
			b, e := secureRead(r.Path, 2*1024*1024)
			installed[agent] = e == nil && strings.Contains(string(b), r.Managed)
		}
	}
	for _, agent := range []string{"opencode", "pi"} {
		installed[agent] = nativeHooksInstalled(s, agent)
	}
	result["hooks"] = installed
	result["health"] = hookHealth(s, capabilities)
	return result
}

func discoverAgents(ctx context.Context, s Store) (map[string]any, error) {
	result := agentDiagnostics(ctx, s)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capabilities := result["capabilities"].(map[string]any)
	delete(capabilities, "herdr")
	delete(capabilities, "target")
	result["v"] = 1
	result["scope"] = "agents"
	result["discovery_version"] = 1
	result["version"] = version
	result["commit"] = commit
	return result, nil
}
