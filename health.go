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
		if c["installed"] == false {
			h.State = "not_installed"
		} else if c["supported"] == false {
			h.State = "unsupported"
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
				if text != r.Managed {
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
				validMatcher := event == "UserPromptSubmit" && matcher == "" || event == "Notification" && matcher == "^(permission_prompt|elicitation_dialog|idle_prompt)$"
				if typ != "command" || timeout != 10 || !validMatcher {
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
		b, exists, _, err := nativeRead(path)
		if err != nil {
			h.issue("unknown", "asset_unreadable", "all", capability)
		} else if !exists {
			h.issue("partial", "asset_missing", "all", capability)
		} else if nativeDigest(b) != file.SHA || !bytes.Equal(b, files[path]) {
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
			}
		}
	}
	return h
}
