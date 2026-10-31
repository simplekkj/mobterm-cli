package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

type healthIssue struct {
	Code       string `json:"code"`
	Event      string `json:"event"`
	Capability string `json:"capability"`
}
type agentHealth struct {
	State              string        `json:"state"`
	ConfigurationState string        `json:"configuration_state"`
	Issues             []healthIssue `json:"issues"`
	RestartRequired    bool          `json:"restart_required"`
	Loaded             string        `json:"loaded"`
	RecoveryAction     string        `json:"recovery_action,omitempty"`
	RunState           string        `json:"run_state,omitempty"`
	MobileApproval     string        `json:"mobile_approval,omitempty"`
	NativeTrust        string        `json:"native_trust,omitempty"`
}

func (h *agentHealth) issue(state, code, event, capability string) {
	if event != "all" && event != "UserPromptSubmit" && event != "Notification" && event != "agent-turn-complete" {
		event = "unknown"
	}
	if len(h.Issues) < 12 {
		h.Issues = append(h.Issues, healthIssue{code, event, capability})
	}
	rank := map[string]int{"complete": 0, "not_configured": 1, "partial": 2, "unknown": 3, "modified": 4, "conflict": 5}
	if rank[state] >= rank[h.ConfigurationState] {
		h.ConfigurationState = state
	}
	h.State = h.ConfigurationState
}
func executableAvailable(path string) bool {
	p, e := exec.LookPath(path)
	if e != nil {
		return false
	}
	i, e := os.Stat(p)
	return e == nil && i.Mode().IsRegular() && i.Mode().Perm()&0111 != 0
}
func hookHealth(s Store, capabilities map[string]any) map[string]agentHealth {
	result := map[string]agentHealth{}
	for _, agent := range []string{"codex", "claude", "opencode", "pi"} {
		h := inspectHook(s, agent)
		c, _ := capabilities[agent].(map[string]any)
		if status, typed := c["probe_status"].(probeStatus); typed && status != probeOK {
			if status == probeNotFound {
				h.State = "not_installed"
			} else {
				h.State = "unknown"
			}
		} else if c["installed"] == false {
			h.State = "not_installed"
		} else if c["supported"] == false {
			h.State = "unsupported"
		}
		if agent == "codex" {
			actual, _ := c["actual_version"].(string)
			if !codexNativeVersion(actual) {
				h.RunState, h.MobileApproval = "unsupported_version", "unsupported_version"
			} else if h.ConfigurationState == "complete" && h.RunState == "not_configured" {
				h.issue("partial", "native_hooks_missing", "all", "run_state")
			}
		}
		result[agent] = h
	}
	return result
}
func inspectHook(s Store, agent string) agentHealth {
	h := agentHealth{State: "complete", ConfigurationState: "complete", Issues: []healthIssue{}, Loaded: "unknown"}
	if agent == "pi" || agent == "opencode" {
		return inspectNativeHook(s, agent, h)
	}

	if tx, e := readHookTransaction(s, agent); e == nil {
		path, pathErr := configPath(agent)
		path, _ = filepath.Abs(path)
		_, _, stateErr := hookTransactionState(s, tx)
		if pathErr != nil || path != tx.Path || stateErr != nil {
			h.issue("conflict", "recovery_conflict", "all", "all")
			h.RecoveryAction = "review_conflict"
		} else {
			h.issue("partial", "operation_incomplete", "all", "all")
			h.RecoveryAction = "continue_install"
			if tx.Action == "uninstall" {
				h.RecoveryAction = "finish_uninstall"
			}
		}
		h.RestartRequired = true
		return h
	} else if !errors.Is(e, os.ErrNotExist) {
		h.issue("conflict", "recovery_invalid", "all", "all")
		h.RecoveryAction = "review_conflict"
		return h
	}
	var r HookRecord
	if err := s.readJSON("hooks-"+agent+".json", &r); err != nil {
		state := "unknown"
		if errors.Is(err, os.ErrNotExist) {
			state = "not_configured"
		}
		h.issue(state, "ownership_unavailable", "all", "all")
		return h
	}
	path, err := configPath(agent)
	if err != nil {
		h.issue("unknown", "config_root_unavailable", "all", "all")
		return h
	}
	path, _ = filepath.Abs(path)
	if path != r.Path {
		h.issue("conflict", "config_root_changed", "all", "all")
		return h
	}
	data, err := secureRead(path, 2*1024*1024)
	if err != nil {
		state := "unknown"
		if errors.Is(err, os.ErrNotExist) {
			state = "partial"
		}
		h.issue(state, "config_missing_or_unreadable", "all", "all")
		return h
	}

	if agent == "codex" && r.Agent == agent && r.Managed != "" && r.Original != "" {
		start, end, argv, e := rootNotify(data)
		if e == nil && start >= 0 && string(data[start:end]) == r.Original && reflect.DeepEqual(argv, r.Chain) {
			h.issue("partial", "legacy_operation_incomplete", "agent-turn-complete", "turn_completed")
			h.RecoveryAction = "uninstall_before_install"
			return h
		}
	}
	if agent == "codex" {
		_, _, argv, err := rootNotify(data)
		_, _, expected, recordErr := rootNotify([]byte(r.Managed))
		if err != nil || recordErr != nil || len(expected) != 5 || expected[1] != "notify" || expected[2] != "--source" || expected[3] != "codex" || expected[4] != "--mobterm-chain" {
			h.issue("modified", "notify_argv_invalid", "agent-turn-complete", "turn_completed")
			return h
		}
		if len(argv) == 0 {
			h.issue("partial", "notify_missing", "agent-turn-complete", "turn_completed")
		} else if !reflect.DeepEqual(argv, expected) {
			h.issue("conflict", "notify_modified", "agent-turn-complete", "turn_completed")
		} else if !executableAvailable(argv[0]) {
			h.issue("partial", "cli_entry_unavailable", "agent-turn-complete", "turn_completed")
		}
		if len(r.Chain) > 0 && !executableAvailable(r.Chain[0]) {
			h.issue("partial", "original_notifier_unavailable", "agent-turn-complete", "turn_completed")
		}
		h.RunState, h.MobileApproval = "not_configured", "not_configured"
		if r.CodexNative != "" || r.CodexJSON != "" {
			layout, err := inspectCodexLayout(data, r, expected[0])
			if err != nil {
				h.issue("conflict", "native_hooks_modified", "all", "all")
				if errors.Is(err, errCodexMixedHooks) {
					h.issue("conflict", "native_hook_sources_mixed", "all", "run_state")
				}
				h.RunState, h.MobileApproval = "configuration_conflict", "configuration_conflict"
			} else {
				h.RunState, h.MobileApproval = "configured_unverified", "configured_default_off"
				h.NativeTrust = "review_required"
				h.RestartRequired = true
				if layout == "json" && r.CodexJSON == "" || layout == "toml" && r.CodexNative == "" {
					h.issue("partial", "native_hook_migration_required", "all", "run_state")
				}
				if codexNativeDisabled(data) {
					h.RunState, h.MobileApproval, h.NativeTrust = "disabled_by_configuration", "disabled_by_configuration", "disabled_by_configuration"
				}
			}
		}
		return h
	}
	prefix := strings.TrimSuffix(r.Managed, " notify --source claude")
	executable := ""
	if strings.HasPrefix(prefix, "'") && strings.HasSuffix(prefix, "'") {
		executable = strings.ReplaceAll(prefix[1:len(prefix)-1], "'\"'\"'", "'")
	}
	if executable == "" || shellQuote(executable) != prefix {
		h.issue("conflict", "command_entry_invalid", "all", "all")
	} else if !executableAvailable(executable) {
		h.issue("partial", "cli_entry_unavailable", "all", "all")
	}
	if r.ClaudeOwned == nil {
		h.issue("conflict", "ownership_unknown", "all", "all")
		return h
	}
	var root map[string]json.RawMessage
	var hooks map[string][]map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || json.Unmarshal(root["hooks"], &hooks) != nil || hooks == nil {
		h.issue("modified", "hooks_shape_invalid", "all", "all")
		return h
	}
	expectedEvents := map[string]string{"UserPromptSubmit": "turn_completed", "Notification": "turn_completed,needs_input"}
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop"} {
		if _, ok := r.ClaudeOwned[event]; ok {
			expectedEvents[event] = "run_state"
		}
	}
	h.RunState = "not_configured"
	h.MobileApproval = "default_off"
	if len(expectedEvents) > 2 {
		h.RunState = "configured_unverified"
		h.RestartRequired = true
	}
	counts := map[string]int{}
	for event, entries := range hooks {
		for _, entry := range entries {
			var commands []map[string]json.RawMessage
			if json.Unmarshal(entry["hooks"], &commands) != nil {
				h.issue("unknown", "hooks_shape_invalid", event, expectedEvents[event])
				continue
			}
			for _, cmd := range commands {
				var text string
				_ = json.Unmarshal(cmd["command"], &text)
				wantCommand := r.Managed
				if event == "PermissionRequest" {
					wantCommand = shellQuote(executable) + " approval wait --source claude"
				} else if event != "UserPromptSubmit" && event != "Notification" {
					wantCommand = shellQuote(executable) + " observe --source claude"
				}
				if text != wantCommand {
					if looksManagedClaude(text, r.Managed) {
						h.issue("modified", "command_modified", event, expectedEvents[event])
					}
					continue
				}
				counts[event]++
				capability, known := expectedEvents[event]
				if !known {
					h.issue("conflict", "event_moved", event, "all")
					continue
				}
				var typ string
				var timeout int
				var matcher string
				_ = json.Unmarshal(cmd["type"], &typ)
				_ = json.Unmarshal(cmd["timeout"], &timeout)
				_ = json.Unmarshal(entry["matcher"], &matcher)
				validMatcher := event != "Notification" && matcher == "" || event == "Notification" && matcher == "^(permission_prompt|elicitation_dialog|idle_prompt)$"
				wantTimeout := 10
				if event == "PermissionRequest" {
					wantTimeout = 130
				}
				if typ != "command" || timeout != wantTimeout || !validMatcher {
					h.issue("modified", "event_conditions_modified", event, capability)
				}
				candidate := map[string]json.RawMessage{}
				for k, v := range entry {
					candidate[k] = v
				}
				candidate["hooks"], _ = json.Marshal([]map[string]json.RawMessage{cmd})
				actual, _ := json.Marshal(candidate)
				var expected map[string]json.RawMessage
				if json.Unmarshal([]byte(r.ClaudeOwned[event]), &expected) != nil {
					h.issue("conflict", "ownership_unknown", event, capability)
				} else {
					want, _ := json.Marshal(expected)
					if !bytes.Equal(actual, want) {
						h.issue("modified", "owned_entry_modified", event, capability)
					}
				}
			}
		}
	}
	for event, capability := range expectedEvents {
		if counts[event] == 0 {
			h.issue("partial", "event_missing", event, capability)
			for _, entry := range hooks[event] {
				var matcher string
				_ = json.Unmarshal(entry["matcher"], &matcher)
				if event == "UserPromptSubmit" && matcher == "" || event == "Notification" && matcher == "^(permission_prompt|elicitation_dialog|idle_prompt)$" {
					h.issue("conflict", "event_ownership_uncertain", event, capability)
				}
			}
		} else if counts[event] > 1 {
			h.issue("conflict", "event_duplicate", event, capability)
		}
	}
	return h
}
func looksManagedClaude(command, owned string) bool {
	if command == "" {
		return false
	}
	prefix := strings.TrimSuffix(owned, " notify --source claude")
	return prefix != "" && strings.Contains(command, prefix) || strings.Contains(command, "mobterm-cli") && strings.Contains(command, "notify")
}
func inspectNativeHook(s Store, agent string, h agentHealth) agentHealth {
	var r nativeHookRecord
	if err := s.readJSON(nativeRecordName(agent), &r); err != nil {
		state := "unknown"
		if errors.Is(err, os.ErrNotExist) {
			state = "not_configured"
		}
		h.issue(state, "ownership_unavailable", "all", "all")
		return h
	}
	h.RestartRequired = true
	if nativeValidateRecord(s, r, agent) != nil {
		h.issue("conflict", "ownership_invalid", "all", "all")
		return h
	}
	path, err := nativeConfigPath(agent)
	if err != nil || path != r.Path {
		h.issue("conflict", "config_root_changed", "all", "all")
	}
	if r.Pending != "" {
		h.RecoveryAction = "continue_install"
		if r.Pending == "uninstall" {
			h.RecoveryAction = "finish_uninstall"
		} else if executable, e := os.Executable(); e != nil || executable != r.Executable || r.Revision != nativeAdapterRevision || err != nil || path != r.Path {
			h.RecoveryAction = "uninstall_before_install"
		}
		h.issue("partial", "installation_incomplete", "all", "all")
	}
	if r.Revision != nativeAdapterRevision {
		h.issue("modified", "adapter_revision_changed", "all", "all")
	}
	if !executableAvailable(r.Executable) {
		h.issue("partial", "cli_entry_unavailable", "all", "all")
	}
	files, _, err := nativeRenderedFiles(s, agent, r.Path, r.Executable)
	if err != nil {
		h.issue("unknown", "adapter_unavailable", "all", "all")
		return h
	}
	capability := "turn_completed"
	if agent == "opencode" {
		capability += ",needs_input"
	}
	for path, file := range r.Files {
		if r.Pending == "install" && nativeDigest(files[path]) != file.SHA && h.RecoveryAction != "review_conflict" {
			h.RecoveryAction = "uninstall_before_install"
		}
		b, exists, _, err := nativeRead(path)
		if err != nil {
			h.issue("unknown", "asset_unreadable", "all", capability)
		} else if !exists {
			h.issue("partial", "asset_missing", "all", capability)
		} else if nativeDigest(b) != file.SHA || !bytes.Equal(b, files[path]) {
			if r.Pending != "" && !nativeAllowed(b, exists, file, r.Pending) {
				h.RecoveryAction = "review_conflict"
			}
			h.issue("modified", "asset_modified", "all", capability)
		}
	}
	if agent == "opencode" {
		b, exists, _, err := nativeRead(r.Path)
		if err != nil || !exists {
			h.issue("partial", "plugin_reference_missing", "all", capability)
		} else if _, err = editNativePlugin(b, r.Reference, "install", true, false); err != nil {
			if strings.HasPrefix(err.Error(), "managed plugin reference removed") {
				h.issue("partial", "plugin_reference_missing", "all", capability)
			} else {
				h.issue("conflict", "plugin_reference_modified", "all", capability)
				if r.Pending != "" {
					h.RecoveryAction = "review_conflict"
				}
			}
		}
	}
	return h
}
