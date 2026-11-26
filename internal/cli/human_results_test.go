package cli

import (
	"strings"
	"testing"
)

func humanOutput(command string, object map[string]any) string {
	var out strings.Builder
	writeHumanResult(&out, terminalCaps{Width: 80}, []string{command}, object)
	return out.String()
}

func TestHumanTestDistinguishesDeliveryStages(t *testing.T) {
	first := deliveryResult(delivery{PairID: "first_pair_123456", State: "handed_off"})
	second := deliveryResult(delivery{PairID: "second_pair_12345", State: "apns_accepted"})
	failed := deliveryResult(delivery{PairID: "failed_pair_12345", State: "permanent_failed", Error: "pair_not_active"})
	for _, single := range []map[string]any{first, second, failed} {
		if !strings.Contains(humanOutput("test", single), single["pair_id"].(string)) {
			t.Fatal("missing pair in single-pair receipt")
		}
	}
	out := humanOutput("test", map[string]any{"results": []map[string]any{first, second, failed}})
	for _, evidence := range []string{"first_pair_123456  Accepted by API", "APNs acceptance unconfirmed", "second_pair_12345  Accepted by APNs", "Check the phone", "Notification failed permanently", "pair not active"} {
		if !strings.Contains(out, evidence) {
			t.Errorf("missing %q in %q", evidence, out)
		}
	}
}

func TestHumanStatusSeparatesLocalStateAndRemoteFailure(t *testing.T) {
	out := humanOutput("status", map[string]any{
		"pairs": []map[string]any{
			{"pair_id": "first_pair_123456", "status": "api_unavailable", "local_status": "active"},
			{"pair_id": "second_pair_12345", "status": "active", "local_status": "active"},
		},
		"health": map[string]agentHealth{
			"pi": {State: "not_installed", ConfigurationState: "partial"},
		},
		"device_token": "private-token",
	})
	for _, text := range []string{"API unavailable (local: active)", "active at API", "Hooks on disk: partial (Agent not found)"} {
		if !strings.Contains(out, text) {
			t.Errorf("missing %q in %q", text, out)
		}
	}
	if strings.Contains(out, "private-token") {
		t.Fatal("unexpected field in status")
	}
	out = humanOutput("status", map[string]any{
		"pair_id": "malformed\x1b[31m", "local_status": "active", "status": "active",
		"failure": &PairingFailure{Stage: "pair_status", Code: "installation_changed"},
	})
	if !strings.Contains(out, "API status unconfirmed") || strings.Contains(out, "active at API") || strings.ContainsRune(out, '\x1b') {
		t.Fatal(out)
	}
}

func TestHumanHooksRequiredActivation(t *testing.T) {
	for _, tt := range []struct {
		agent, action, activation, want string
	}{
		{"codex", "install", "native_review_required", "review the native hooks in /hooks"},
		{"pi", "install", "restart_required", "Restart pi to load the hooks"},
		{"opencode", "uninstall", "", "configuration removed"},
	} {
		out := humanOutput("hooks", map[string]any{"ok": true, "agent": tt.agent, "action": tt.action, "activation": tt.activation})
		if !strings.Contains(out, tt.want) {
			t.Errorf("missing %q in %q", tt.want, out)
		}
	}
	out := humanOutput("hooks", map[string]any{"ok": false, "agent": "codex", "action": "install"})
	if strings.Contains(out, "configured on disk") || !strings.Contains(out, "did not complete") {
		t.Fatal(out)
	}
}
