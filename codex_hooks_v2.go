package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This is a validated baseline, not a claim about the first release with hooks.
func codexNativeVersion(actual string) bool {
	compatibility := agentCompatibilities["codex"]
	compatibility.minimum = stableVersion{0, 166, 0}
	return compatibility.evaluate(actual) == ""
}

func codexNativeDisabled(data []byte) bool {
	var config map[string]any
	if toml.Unmarshal(data, &config) != nil {
		return true
	}
	if config["allow_managed_hooks_only"] == true {
		return true
	}
	features, _ := config["features"].(map[string]any)
	if enabled, present := features["hooks"]; present {
		return enabled == false
	}
	return features["codex_hooks"] == false
}

func codexNativeBlock(executable string) string {
	var b strings.Builder
	b.WriteString("\n# mobterm-cli managed native hooks v2\n")
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Interrupt", "SessionStart", "SessionEnd"} {
		command := shellQuote(executable) + " observe --source codex"
		timeout := "3"
		if event == "PermissionRequest" {
			command = shellQuote(executable) + " approval wait --source codex"
			timeout = "130"
		}
		encoded, _ := json.Marshal(command)
		b.WriteString("[[hooks." + event + "]]\n[[hooks." + event + ".hooks]]\ntype = \"command\"\ncommand = " + string(encoded) + "\ntimeout = " + timeout + "\n")
	}
	b.WriteString("# end mobterm-cli managed native hooks\n")
	return b.String()
}

// Remove only proven owned commands and compare the resulting semantic document.
// This rejects edits after the block that accidentally rely on its table scope.
func stripCodexNative(data []byte, owned string) ([]byte, error) {
	if owned == "" {
		return data, nil
	}
	if bytes.Count(data, []byte(owned)) != 1 {
		return nil, errors.New("managed Codex native hooks edited; config unchanged")
	}
	var actual, block map[string]any
	if toml.Unmarshal(data, &actual) != nil || toml.Unmarshal([]byte(owned), &block) != nil {
		return nil, errors.New("invalid native hook ownership")
	}
	hooks, ok := actual["hooks"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid hooks table")
	}
	wanted, ok := block["hooks"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid owned hooks")
	}
	for event, entries := range wanted {
		expected, ok := entries.([]any)
		if !ok || len(expected) != 1 {
			return nil, errors.New("invalid native hook record")
		}
		current, ok := hooks[event].([]any)
		if !ok {
			return nil, errors.New("native hook shape changed")
		}
		kept := make([]any, 0, len(current))
		count := 0
		for _, entry := range current {
			if reflect.DeepEqual(entry, expected[0]) {
				count++
			} else {
				kept = append(kept, entry)
			}
		}
		if count != 1 {
			return nil, errors.New("native hook ownership ambiguous")
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if len(hooks) == 0 {
		delete(actual, "hooks")
	}
	result := bytes.Replace(data, []byte(owned), nil, 1)
	var decoded map[string]any
	if toml.Unmarshal(result, &decoded) != nil || !reflect.DeepEqual(actual, decoded) {
		return nil, errors.New("native hook removal would change unrelated configuration")
	}
	return result, nil
}

func editCodexWithNative(data []byte, action, path, executable string, r HookRecord, installNative bool) ([]byte, HookRecord, error) {
	original := r
	if r.CodexNative == "" && bytes.Contains(data, []byte("# mobterm-cli managed native hooks")) {
		return nil, r, errors.New("Codex native hook ownership missing; config unchanged")
	}
	// Existing native definitions are retained on a repeat install, even when the
	// current version cannot prove new capabilities. Health reports the limitation.
	native := r.CodexNative
	clean, err := stripCodexNative(data, native)
	if err != nil {
		return nil, r, err
	}
	r.CodexNative = ""
	result, record, err := editCodex(clean, action, path, executable, r)
	if err != nil {
		return nil, original, err
	}
	if action == "uninstall" {
		return result, record, nil
	}
	if native == "" && !installNative {
		return result, record, nil
	}
	block := codexNativeBlock(executable)
	var parsed map[string]any
	if toml.Unmarshal(result, &parsed) != nil {
		return nil, original, errors.New("invalid Codex configuration")
	}
	// Never adopt or duplicate a same-looking command without ownership.
	if containsCodexNativeCommand(parsed, executable) {
		return nil, original, errors.New("existing Codex native commands have unknown ownership")
	}
	candidate := append(append([]byte{}, result...), []byte(block)...)
	parsed = nil
	if toml.Unmarshal(candidate, &parsed) != nil {
		return nil, original, errors.New("Codex hook shape cannot be safely extended")
	}
	// Validate a semantic round trip before journaling either config or ownership.
	if _, err = stripCodexNative(candidate, block); err != nil {
		return nil, original, err
	}
	record.CodexNative = block
	return candidate, record, nil
}
func containsCodexNativeCommand(value any, executable string) bool {
	switch value := value.(type) {
	case map[string]any:
		if command, ok := value["command"].(string); ok && (strings.Contains(command, "mobterm-cli") || strings.Contains(command, shellQuote(executable))) && (strings.Contains(command, "observe --source codex") || strings.Contains(command, "approval wait --source codex")) {
			return true
		}
		for _, child := range value {
			if containsCodexNativeCommand(child, executable) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsCodexNativeCommand(child, executable) {
				return true
			}
		}
	}
	return false
}
