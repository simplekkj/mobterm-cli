package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type HookRecord struct {
	Agent       string            `json:"agent"`
	Path        string            `json:"path"`
	Original    string            `json:"original,omitempty"`
	Managed     string            `json:"managed"`
	Chain       []string          `json:"chain,omitempty"`
	ClaudeOwned map[string]string `json:"claude_owned,omitempty"`
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func configPath(agent string) (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch agent {
	case "codex":
		base := os.Getenv("CODEX_HOME")
		if base == "" {
			base = filepath.Join(h, ".codex")
		}
		return filepath.Join(base, "config.toml"), nil
	case "claude":
		base := os.Getenv("CLAUDE_CONFIG_DIR")
		if base == "" {
			base = filepath.Join(h, ".claude")
		}
		return filepath.Join(base, "settings.json"), nil
	}
	return "", errors.New("agent must be codex or claude")
}
func changeHooks(ctx context.Context, s Store, agent, action, path, executable string, checkVersion bool) error {
	if agent == "opencode" || agent == "pi" {
		return changeNativeHooks(ctx, s, agent, action, path, executable, checkVersion)
	}
	if agent != "codex" && agent != "claude" {
		return errors.New("agent must be codex or claude")
	}
	if action != "install" && action != "uninstall" {
		return errors.New("use hooks install or uninstall")
	}
	if checkVersion && action == "install" {
		if err := hookVersions(ctx, agent); err != nil {
			return err
		}
	}
	if path == "" {
		var err error
		path, err = configPath(agent)
		if err != nil {
			return err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = nativeRealParents(path); err != nil {
		return err
	}
	if err = nativeRealParents(filepath.Join(s.Dir, "ownership")); err != nil {
		return err
	}
	if err = ensureDir(filepath.Dir(path), false); err != nil {
		return err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// A second, adjacent lock serializes separate Mobterm state directories targeting the same agent config.
	configUnlock, err := lockFile(ctx, path+".mobterm-lock")
	if err != nil {
		return err
	}
	defer configUnlock()

	tx, pendingErr := readHookTransaction(s, agent)
	if pendingErr == nil {
		if tx.Path != path {
			return errors.New("hook recovery config root changed; unchanged")
		}
		if tx.Action != action {
			return errors.New("hook operation incomplete; finish the pending operation first")
		}
		return finishHookTransaction(s, tx)
	}
	if !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	data, err := secureRead(path, 2*1024*1024)
	exists := !errors.Is(err, os.ErrNotExist)
	mode := os.FileMode(0600)
	if !exists {
		data = nil
	} else if err != nil {
		return err
	} else {
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		mode = info.Mode().Perm()
	}
	recordName := "hooks-" + agent + ".json"
	var record HookRecord
	readErr := s.readJSON(recordName, &record)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	var before *HookRecord
	if readErr == nil {
		if record.Path != path || record.Agent != agent {
			return errors.New("agent already managed at another path or invalid ownership")
		}
		previous := record
		before = &previous
	}
	// An old Codex record can prove the original notify fragment. Treat it as
	// an unfinished operation only when the exact original fragment and argv
	// agree; unrelated content is retained. Claude's old records lack this proof.
	legacyRestored := false
	if agent == "codex" && record.Managed != "" && record.Original != "" {
		start, end, argv, e := rootNotify(data)
		legacyRestored = e == nil && start >= 0 && string(data[start:end]) == record.Original && reflect.DeepEqual(argv, record.Chain)
		if legacyRestored {
			record = HookRecord{}
		}
	}
	var result []byte
	if agent == "codex" {
		result, record, err = editCodex(data, action, path, executable, record)
	} else {
		result, record, err = editClaude(data, action, path, executable, record)
	}
	if err != nil {
		return err
	}
	if bytes.Equal(result, data) && !(action == "uninstall" && before != nil && legacyRestored) {
		return nil
	}
	if len(result) > 2*1024*1024 {
		return errors.New("hook result exceeds configuration size limit")
	}
	var after *HookRecord
	if action == "install" {
		after = &record
	}
	tx = hookTransaction{V: 1, Agent: agent, Path: path, Action: action, BeforeSHA: digest(string(data)), BeforeExists: exists, After: result, AfterSHA: digest(string(result)), Mode: uint32(mode), BeforeRecord: before, AfterRecord: after}
	if err = writeHookTransaction(s, tx); err != nil {
		return err
	}
	tx, err = readHookTransaction(s, agent)
	if err != nil {
		return err
	}
	return finishHookTransaction(s, tx)
}
func rootNotify(data []byte) (start, end int, argv []string, err error) {
	var decoded map[string]any
	if err = toml.Unmarshal(data, &decoded); err != nil {
		return 0, 0, nil, errors.New("invalid TOML; config unchanged")
	}
	value, exists := decoded["notify"]
	if !exists {
		return -1, -1, nil, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return 0, 0, nil, errors.New("existing notify must be an argv array")
	}
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return 0, 0, nil, errors.New("notify argv contains non-string")
		}
		argv = append(argv, s)
	}
	var parser unstable.Parser
	parser.Reset(data)
	inTable := false
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			inTable = true
		}
		if inTable || n.Kind != unstable.KeyValue {
			continue
		}
		keys := n.Key()
		if !keys.Next() || string(keys.Node().Data) != "notify" {
			continue
		}
		key := keys.Node()
		if keys.Next() {
			continue
		}
		start = int(key.Raw.Offset)
		for start > 0 && data[start-1] != '\n' {
			start--
		}
		for end = start; end <= len(data); end++ {
			if end < len(data) && data[end] != '\n' {
				continue
			}
			finish := end
			if end < len(data) {
				finish++
			}
			var candidate map[string]any
			if toml.Unmarshal(data[start:finish], &candidate) == nil {
				if _, found := candidate["notify"]; found {
					return start, finish, argv, nil
				}
			}
		}
		return 0, 0, nil, errors.New("cannot isolate notify safely")
	}
	return 0, 0, nil, errors.New("root notify cannot be safely edited")
}
func editCodex(data []byte, action, path, executable string, r HookRecord) ([]byte, HookRecord, error) {
	if r.Managed != "" {
		_, _, actual, parseErr := rootNotify(data)
		_, _, expected, recordErr := rootNotify([]byte(r.Managed))
		if bytes.Count(data, []byte(r.Managed)) == 1 && parseErr == nil && recordErr == nil && reflect.DeepEqual(actual, expected) {
			if action == "install" {
				return data, r, nil
			}
			return bytes.Replace(data, []byte(r.Managed), []byte(r.Original), 1), r, nil
		}
		if action == "uninstall" || strings.Contains(string(data), "# mobterm-cli managed notify") && !(action == "install" && parseErr == nil && len(actual) == 0) {
			return nil, r, errors.New("managed notify was edited; config left unchanged")
		}
	}
	if action == "uninstall" {
		return data, r, nil
	}
	start, end, chain, err := rootNotify(data)
	if err != nil {
		return nil, r, err
	}
	for _, arg := range chain {
		if arg == "--mobterm-chain" || filepath.Base(arg) == "mobterm-cli" {
			return nil, r, errors.New("refusing recursive notify wrapper")
		}
	}
	if r.Managed != "" {
		if len(chain) > 0 {
			return nil, r, errors.New("managed notify was replaced; config unchanged")
		}
		chain = r.Chain
	}
	args := []string{executable, "notify", "--source", "codex", "--mobterm-chain"}
	encoded, _ := json.Marshal(args)
	managed := "# mobterm-cli managed notify v1\nnotify = " + string(encoded) + "\n# end mobterm-cli managed notify\n"
	original := r.Original
	r = HookRecord{Agent: "codex", Path: path, Managed: managed, Chain: chain, Original: original}
	if start >= 0 {
		r.Original = string(data[start:end])
		return append(append(append([]byte{}, data[:start]...), managed...), data[end:]...), r, nil
	}
	return append([]byte(managed), data...), r, nil
}
func editClaude(data []byte, action, path, executable string, r HookRecord) ([]byte, HookRecord, error) {
	if r.Managed != "" && r.ClaudeOwned == nil {
		return nil, r, errors.New("legacy Claude hook ownership is unknown; manually review hooks before reinstalling or removing; config left unchanged")
	}
	var root map[string]json.RawMessage
	if len(data) == 0 {
		root = map[string]json.RawMessage{}
	} else if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, r, errors.New("invalid Claude JSON; config unchanged")
	}
	hooks := map[string][]map[string]json.RawMessage{}
	if raw, ok := root["hooks"]; ok {
		if json.Unmarshal(raw, &hooks) != nil || hooks == nil {
			return nil, r, errors.New("unsupported Claude hook shape")
		}
	}
	for _, entries := range hooks {
		if entries == nil {
			return nil, r, errors.New("unsupported Claude hook shape")
		}
		for _, entry := range entries {
			if entry == nil {
				return nil, r, errors.New("unsupported Claude hook entry")
			}
		}
	}
	// Without an installation record, existing commands belong to the user.
	if action == "uninstall" && r.Managed == "" {
		return data, r, nil
	}
	command := shellQuote(executable) + " notify --source claude"
	if r.Managed != "" {
		command = r.Managed
	}
	events := []struct{ name, matcher string }{
		{"UserPromptSubmit", ""},
		{"Notification", "^(permission_prompt|elicitation_dialog|idle_prompt)$"},
	}
	if r.Managed != "" {
		for event, entries := range hooks {
			for _, entry := range entries {
				var cmds []map[string]json.RawMessage
				_ = json.Unmarshal(entry["hooks"], &cmds)
				for _, cmd := range cmds {
					var text string
					_ = json.Unmarshal(cmd["command"], &text)
					if text == command && (event != "UserPromptSubmit" && event != "Notification") || text != command && looksManagedClaude(text, command) {
						return nil, r, errors.New("managed Claude command was moved or modified; config unchanged")
					}
				}
			}
		}
	}
	owned := make(map[string]string, len(r.ClaudeOwned))
	for event, entry := range r.ClaudeOwned {
		owned[event] = entry
	}
	managedCommand, _ := json.Marshal(map[string]any{"type": "command", "command": command, "timeout": 10})
	changed := false
	for _, event := range events {
		managedEntry := map[string]json.RawMessage{"hooks": json.RawMessage("[" + string(managedCommand) + "]")}
		if event.matcher != "" {
			managedEntry["matcher"], _ = json.Marshal(event.matcher)
		}
		encodedEntry, _ := json.Marshal(managedEntry)
		var expected map[string]json.RawMessage
		if encoded, ok := owned[event.name]; ok {
			if json.Unmarshal([]byte(encoded), &expected) != nil || expected == nil {
				return nil, r, errors.New("invalid Claude hook ownership record")
			}
		}
		entries := hooks[event.name]
		found := false
		ownedMatches := 0
		out := make([]map[string]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			var commands []map[string]json.RawMessage
			if json.Unmarshal(entry["hooks"], &commands) != nil || commands == nil {
				return nil, r, errors.New("unsupported Claude hook entry")
			}
			kept := make([]map[string]json.RawMessage, 0, len(commands))
			for _, cmd := range commands {
				if cmd == nil {
					return nil, r, errors.New("unsupported Claude hook command")
				}
				var text string
				_ = json.Unmarshal(cmd["command"], &text)
				if text == command {
					found = true
					if expected != nil {
						ownedMatches++
						encodedCommand, _ := json.Marshal(cmd)
						candidate := make(map[string]json.RawMessage, len(entry))
						for key, value := range entry {
							candidate[key] = value
						}
						candidate["hooks"] = json.RawMessage("[" + string(encodedCommand) + "]")
						actualEntry, _ := json.Marshal(candidate)
						expectedEntry, _ := json.Marshal(expected)
						if !bytes.Equal(actualEntry, expectedEntry) || ownedMatches > 1 {
							return nil, r, errors.New("managed Claude hook was edited or duplicated; config left unchanged")
						}
						if action == "uninstall" {
							changed = true
							continue
						}
					}
				}
				kept = append(kept, cmd)
			}
			if len(kept) == 0 && len(commands) > 0 {
				continue
			}
			if len(kept) != len(commands) {
				entry["hooks"], _ = json.Marshal(kept)
			}
			out = append(out, entry)
		}
		if expected != nil && ownedMatches == 0 && action == "install" {
			for _, entry := range entries {
				var actualMatcher, wantMatcher string
				_ = json.Unmarshal(entry["matcher"], &actualMatcher)
				_ = json.Unmarshal(expected["matcher"], &wantMatcher)
				if actualMatcher == wantMatcher {
					return nil, r, errors.New("managed Claude entry may have been replaced; config unchanged")
				}
			}
		}
		if expected != nil && ownedMatches == 0 && action != "install" {
			return nil, r, errors.New("managed Claude hook was edited or removed; config left unchanged")
		}
		if action == "install" && !found {
			out = append(out, managedEntry)
			owned[event.name] = string(encodedEntry)
			changed = true
		}
		if len(out) == 0 {
			delete(hooks, event.name)
		} else {
			hooks[event.name] = out
		}
	}
	if !changed {
		return data, r, nil
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"], _ = json.Marshal(hooks)
	}
	result, err := json.MarshalIndent(root, "", "  ")
	return append(result, '\n'), HookRecord{Agent: "claude", Path: path, Managed: command, ClaudeOwned: owned}, err
}
func hooksContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
