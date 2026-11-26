package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func classicResult(status, reason, activation string) reconcileComponent {
	return reconcileComponent{Component: "hooks", Status: status, Reason: reason, Activation: activation}
}

func classicAttention(reason string) reconcileComponent {
	return classicResult("attention", reason, "unknown")
}

// Codex keys trust and enablement by source path, event, group and handler
// index. Never let a changed template transfer someone else's stored choice.
func codexStateChange(before, after, beforeJSON, afterJSON []byte, path, executable string) string {
	var oldConfig, newConfig struct{ Hooks map[string]any }
	if toml.Unmarshal(before, &oldConfig) != nil || toml.Unmarshal(after, &newConfig) != nil {
		return "ownership_conflict"
	}
	states, _ := oldConfig.Hooks["state"].(map[string]any)
	if len(states) == 0 {
		return ""
	}
	var oldJSON, newJSON struct{ Hooks map[string]any }
	if len(beforeJSON) > 0 && json.Unmarshal(beforeJSON, &oldJSON) != nil || len(afterJSON) > 0 && json.Unmarshal(afterJSON, &newJSON) != nil {
		return "ownership_conflict"
	}
	for key, entry := range states {
		key = strings.TrimPrefix(key, "file:")
		for _, source := range []struct {
			path      string
			old, next map[string]any
		}{{path, oldConfig.Hooks, newConfig.Hooks}, {filepath.Join(filepath.Dir(path), "hooks.json"), oldJSON.Hooks, newJSON.Hooks}} {
			position, ok := strings.CutPrefix(key, source.path+":")
			if !ok {
				continue
			}
			oldGroup := codexStateGroup(source.old, position)
			newGroup := codexStateGroup(source.next, position)
			owned := containsCodexNativeCommand(oldGroup, executable)
			willOwn := containsCodexNativeCommand(newGroup, executable)
			state, _ := entry.(map[string]any)
			if (owned || willOwn) && state["enabled"] == false {
				return "disabled"
			}
			if oldGroup != nil && !(owned && willOwn) && !reflect.DeepEqual(oldGroup, newGroup) {
				return "ownership_conflict"
			}
		}
	}
	return ""
}

func codexStateGroup(hooks map[string]any, position string) any {
	fields := strings.Split(position, ":")
	if len(fields) != 3 {
		return nil
	}
	group, err := strconv.Atoi(fields[1])
	if err != nil || group < 0 {
		return nil
	}
	var event strings.Builder
	for _, word := range strings.Split(fields[0], "_") {
		if len(word) == 0 {
			return nil
		}
		event.WriteString(strings.ToUpper(word[:1]))
		event.WriteString(word[1:])
	}
	groups, _ := hooks[event.String()].([]any)
	if group >= len(groups) {
		return nil
	}
	return groups[group]
}

// Reject a corrupt historical record that claims an unrelated command. An
// earlier timeout or event set is valid when the owned command is recognizable.
func codexOwnedCommands(r HookRecord, executable string) bool {
	if r.CodexNative == "" && r.CodexJSON == "" {
		return true
	}
	type handler struct{ Type, Command string }
	type group struct{ Hooks []handler }
	var events map[string][]group
	if r.CodexNative != "" {
		var block struct{ Hooks map[string][]group }
		if toml.Unmarshal([]byte(r.CodexNative), &block) != nil || r.CodexJSON != "" {
			return false
		}
		events = block.Hooks
	} else if json.Unmarshal([]byte(r.CodexJSON), &events) != nil {
		return false
	}
	for event, groups := range events {
		want := shellQuote(executable) + " observe --source codex"
		if event == "PermissionRequest" {
			want = shellQuote(executable) + " approval wait --source codex"
		}
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command != want {
			return false
		}
	}
	return len(events) > 0
}

// Planning operates on a pair of snapshots, so recovery can validate the
// journal's intended after-state even when only one of its files was written.
func planClassic(r HookRecord, executable string, probe agentProbe, data, jsonData []byte, file hookFileChange) (hookTransaction, reconcileComponent) {
	var tx hookTransaction
	var result []byte
	var next HookRecord
	var jsonChange *hookFileChange
	var err error
	activation := "restart_required"

	switch r.Agent {
	case "codex":
		_, want, e := editCodex(nil, "install", r.Path, executable, HookRecord{})
		start, end, argv, parseErr := rootNotify(data)
		if e != nil || parseErr != nil || r.Managed != want.Managed || !codexOwnedCommands(r, executable) {
			return tx, classicAttention("ownership_conflict")
		}
		if len(argv) == 0 {
			return tx, classicAttention("disabled")
		}
		fragment := bytes.Index(data, []byte(r.Managed))
		if !reflect.DeepEqual(argv, []string{executable, "notify", "--source", "codex", "--mobterm-chain"}) || bytes.Count(data, []byte(r.Managed)) != 1 || start < fragment || end > fragment+len(r.Managed) {
			return tx, classicAttention("ownership_conflict")
		}
		// editCodex normally restores a missing wrapper on explicit install.
		// Reconciliation never takes that branch or replaces the original chain.
		_, _, previous, originalErr := rootNotify([]byte(r.Original))
		if originalErr != nil || !reflect.DeepEqual(previous, r.Chain) {
			return tx, classicAttention("ownership_conflict")
		}
		if !codexNativeVersion(probe.actual) {
			return tx, classicResult("skipped", "unsupported", "unknown")
		}
		if codexNativeDisabled(data) {
			return tx, classicAttention("disabled")
		}
		result, next, jsonChange, err = renderCodexLayout(data, jsonData, file, "install", r.Path, executable, r)
		if err == nil && (r.CodexJSON != "" && next.CodexJSON == "" || r.CodexNative != "" && next.CodexNative == "") {
			// A different active event source appeared after installation.
			return tx, classicAttention("ownership_conflict")
		}
		if err == nil {
			if reflect.DeepEqual(r, next) {
				result, jsonChange.After = data, jsonData
			} else if r.CodexNative != "" {
				// Preserve the position of installed handlers relative to user groups.
				result = bytes.Replace(data, []byte(r.CodexNative), []byte(next.CodexNative), 1)
				_, err = stripCodexNative(result, next.CodexNative)
			} else if r.CodexJSON != "" {
				jsonChange.After, err = updateCodexGroups(jsonData, r.CodexJSON, next.CodexJSON)
				jsonChange.AfterSHA = digest(string(jsonChange.After))
			}
			if reason := codexStateChange(data, result, jsonData, jsonChange.After, r.Path, executable); reason != "" {
				return tx, classicAttention(reason)
			}
		}
		activation = "native_review_required"
	case "claude":
		if r.Managed != shellQuote(executable)+" notify --source claude" || len(r.ClaudeOwned) == 0 {
			return tx, classicAttention("ownership_conflict")
		}
		root, parseErr := parseJSONC(data)
		if parseErr != nil || root.kind != '{' {
			return tx, classicAttention("ownership_conflict")
		}
		for _, key := range []string{"disableAllHooks", "allowManagedHooksOnly"} {
			if setting := root.keys[key]; setting != nil && setting.kind != 'f' {
				return tx, classicAttention("disabled")
			}
		}
		policy := agentCompatibilities["claude"]
		policy.minimum = stableVersion{2, 1, 301}
		native := policy.evaluate(probe.actual) == ""
		_, target, e := editClaudeWithNative(nil, "install", r.Path, executable, HookRecord{}, native)
		if e != nil {
			return tx, classicAttention("ownership_conflict")
		}
		for event := range r.ClaudeOwned {
			if _, ok := target.ClaudeOwned[event]; !ok {
				return tx, classicAttention("unsupported")
			}
		}
		result, next, err = editClaudeWithNative(data, "reconcile", r.Path, executable, r, native)
		if errors.Is(err, errClaudeHookMissing) {
			return tx, classicAttention("disabled")
		}
		if err == nil {
			for event := range r.ClaudeOwned {
				if !equalHookJSON([]byte(next.ClaudeOwned[event]), []byte(target.ClaudeOwned[event])) {
					return tx, classicAttention("ownership_conflict")
				}
			}
		}
	default:
		return tx, classicAttention("ownership_conflict")
	}
	if err != nil || len(result) > 2*1024*1024 {
		return tx, classicAttention("ownership_conflict")
	}
	// Rendering can move an unchanged hook past a later user hook. If the
	// verified owned fragments already match the target, keep every byte.
	if reflect.DeepEqual(r, next) {
		return tx, classicResult("current", "current", "unknown")
	}
	tx = hookTransaction{ReconcileVersion: version, V: 3, Agent: r.Agent, Path: r.Path, Action: "install", BeforeSHA: digest(string(data)), BeforeExists: true, After: result, AfterSHA: digest(string(result)), BeforeRecord: &r, AfterRecord: &next}
	if jsonChange != nil {
		tx.JSONFile, tx.JSONFirst = jsonChange, r.CodexJSON != ""
	}
	return tx, classicResult("available", "template_changed", activation)
}

func reconcileClassic(ctx context.Context, s Store, agent, executable string, probe agentProbe, check bool) reconcileComponent {
	path, err := configPath(agent)
	if err != nil {
		return classicAttention("ownership_conflict")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return classicAttention("ownership_conflict")
	}
	_, _, planned := planClassicReconcile(s, agent, path, executable, probe)
	if check || planned.Status != "available" {
		return planned
	}
	if nativeMigrationNode(filepath.Join(s.Dir, ".lock"), true) != nil || nativeMigrationNode(path+".mobterm-lock", true) != nil {
		return classicAttention("ownership_conflict")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return classicAttention("write_failed")
	}
	defer unlock()
	if nativeMigrationNode(path+".mobterm-lock", true) != nil {
		return classicAttention("ownership_conflict")
	}
	unlockConfig, err := lockFile(ctx, path+".mobterm-lock")
	if err != nil {
		return classicAttention("write_failed")
	}
	defer unlockConfig()
	tx, pending, planned := planClassicReconcile(s, agent, path, executable, probe)
	if planned.Status != "available" {
		return planned
	}
	if !pending {
		if ctx.Err() != nil || writeHookTransaction(s, tx) != nil {
			return classicAttention("write_failed")
		}
		tx, err = readHookTransaction(s, agent)
		if err != nil {
			return classicAttention("write_failed")
		}
	}
	if ctx.Err() != nil || finishHookTransaction(s, tx) != nil {
		return classicAttention("write_failed")
	}
	if pending {
		// Re-read the committed record and both files after recovering a
		// compatible journal, including journals predating reconciliation.
		_, _, current := planClassicReconcile(s, agent, path, executable, probe)
		if current.Status != "current" {
			return current
		}
	}
	return classicResult("updated", "updated", planned.Activation)
}

func planClassicReconcile(s Store, agent, path, executable string, probe agentProbe) (hookTransaction, bool, reconcileComponent) {
	var tx hookTransaction
	ok, err := reconcileCandidate(s, agent, "hooks")
	if err != nil {
		return tx, false, classicAttention("ownership_conflict")
	}
	if !ok {
		return tx, false, classicResult("skipped", "unmanaged", "unknown")
	}
	if nativeMigrationNode(path, false) != nil {
		return tx, false, classicAttention("ownership_conflict")
	}
	var jsonData []byte
	var file hookFileChange
	if agent == "codex" {
		jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
		if nativeMigrationNode(jsonPath, false) != nil {
			return tx, false, classicAttention("ownership_conflict")
		}
		jsonData, file, err = readHookFile(jsonPath)
		if err != nil {
			return tx, false, classicAttention("ownership_conflict")
		}
	}

	tx, err = readHookTransaction(s, agent)
	var planned reconcileComponent
	if err == nil {
		if tx.Path != path || tx.Action != "install" || tx.AfterRecord == nil || tx.Mode&0022 != 0 || tx.ReconcileVersion != "" && tx.ReconcileVersion != version {
			return tx, false, classicAttention("pending_conflict")
		}
		if _, _, err = hookTransactionState(s, tx); err != nil {
			return tx, false, classicAttention("pending_conflict")
		}
		if f := tx.JSONFile; f != nil {
			if f.Mode&0022 != 0 {
				return tx, false, classicAttention("pending_conflict")
			}
			jsonData = f.After
			file = hookFileChange{Path: f.Path, BeforeSHA: f.AfterSHA, BeforeExists: f.AfterExists, After: f.After, AfterSHA: f.AfterSHA, AfterExists: f.AfterExists, Mode: f.Mode}
		}
		_, verified := planClassic(*tx.AfterRecord, executable, probe, tx.After, jsonData, file)
		if verified.Status != "current" {
			return tx, false, classicAttention("pending_conflict")
		}
		activation := "restart_required"
		if agent == "codex" && (tx.AfterRecord.CodexNative != "" || tx.AfterRecord.CodexJSON != "") {
			activation = "native_review_required"
		}
		return tx, true, classicResult("available", "template_changed", activation)
	} else if errors.Is(err, os.ErrNotExist) {
		var record HookRecord
		if s.readJSON("hooks-"+agent+".json", &record) != nil || record.Agent != agent || record.Path != path {
			return tx, false, classicAttention("ownership_conflict")
		}
		data, primary, err := readHookFile(path)
		if err != nil {
			return tx, false, classicAttention("ownership_conflict")
		}
		if !primary.BeforeExists {
			return tx, false, classicAttention("disabled")
		}
		tx, planned = planClassic(record, executable, probe, data, jsonData, file)
		tx.Mode = primary.Mode
		return tx, false, planned
	}
	return tx, false, classicAttention("pending_conflict")
}
