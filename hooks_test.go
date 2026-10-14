package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexInstallPreservesCommentsChainAndUserEdits(t *testing.T) {
	data := []byte("# global comment\nmodel = 'example'\nnotify = [\n  'old-notify', # argv comment\n  '--flag',\n]\n[unrelated]\nvalue = 42 # keep\n")
	after, r, err := editCodex(data, "install", "/tmp/config", "/opt/bin/mobterm-cli", HookRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Chain) != 2 || r.Chain[0] != "old-notify" || !bytes.Contains(after, []byte("# global comment")) || !bytes.Contains(after, []byte("value = 42 # keep")) {
		t.Fatal("lost existing config")
	}
	again, _, err := editCodex(after, "install", "/tmp/config", "/opt/bin/mobterm-cli", r)
	if err != nil || !bytes.Equal(again, after) {
		t.Fatal("install not idempotent")
	}
	after = append(after, []byte("added = 'later'\n")...)
	removed, _, err := editCodex(after, "uninstall", "/tmp/config", "/opt/bin/mobterm-cli", r)
	if err != nil || !bytes.Equal(removed, append(data, []byte("added = 'later'\n")...)) {
		t.Fatal("uninstall overwrote user edits", err)
	}
	edited := bytes.Replace(after, []byte("--source"), []byte("--user-edit"), 1)
	if _, _, err = editCodex(edited, "uninstall", "/tmp/config", "/opt/bin/mobterm-cli", r); err == nil {
		t.Fatal("modified owned entry overwritten")
	}
}
func TestCodexConflictsAndQuotedNotify(t *testing.T) {
	for _, raw := range []string{"notify = 'bad'\n", "notify = ['mobterm-cli']\n", "notify = [12]\n", "notify = ['first']\nnotify = ['second']\n"} {
		if _, _, err := editCodex([]byte(raw), "install", "/tmp/config", "/bin/mobterm-cli", HookRecord{}); err == nil {
			t.Fatal("conflict accepted", raw)
		}
	}
	for _, raw := range []string{"#notify = bad\n[x]\nnotify=['unrelated']\n", "\"notify\"=['notify-bin','hello ] # world'] # preserve\n[x]\na=1\n", "notify = [\"\"\"multi\nline\"\"\"]\n"} {
		after, r, err := editCodex([]byte(raw), "install", "p", "/bin/mobterm-cli", HookRecord{})
		if err != nil {
			t.Fatal(err)
		}
		restored, _, err := editCodex(after, "uninstall", "p", "/bin/mobterm-cli", r)
		if err != nil || string(restored) != raw {
			t.Fatal("TOML did not round trip", err)
		}
	}
}
func TestClaudePreservesOtherHooksAndIdempotence(t *testing.T) {
	data := []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-stop"}]}],"Notification":[{"matcher":"auth_success","hooks":[{"type":"command","command":"other-notify"}]}]}}`)
	after, r, err := editClaude(data, "install", "p", "/opt/bin/mobterm-cli", HookRecord{})
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := editClaude(after, "install", "p", "/opt/bin/mobterm-cli", r)
	if err != nil || !bytes.Equal(again, after) {
		t.Fatal("Claude not idempotent")
	}
	var original, current map[string]any
	_ = json.Unmarshal(data, &original)
	_ = json.Unmarshal(after, &current)
	current["new_setting"] = "user-added"
	edited, _ := json.Marshal(current)
	removed, _, err := editClaude(edited, "uninstall", "p", "/opt/bin/mobterm-cli", r)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(removed, &current)
	if current["new_setting"] != "user-added" || !bytes.Contains(removed, []byte("other-stop")) || !bytes.Contains(removed, []byte("other-notify")) || bytes.Contains(removed, []byte("mobterm-cli")) {
		t.Fatal("Claude user hooks damaged")
	}
}
func TestConfigLifecyclePermissionsAndSymlink(t *testing.T) {
	s := testStore(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("# user\nmodel='example'\n")
	if err := os.WriteFile(path, original, 0640); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"install", "install", "uninstall", "uninstall"} {
		if err := changeHooks(context.Background(), s, "codex", action, path, "/bin/mobterm-cli", false); err != nil {
			t.Fatal(action, err)
		}
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("config not restored")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("mode changed")
	}
	link := filepath.Join(t.TempDir(), "symlink")
	_ = os.Symlink(path, link)
	if changeHooks(context.Background(), s, "codex", "install", link, "/bin/mobterm-cli", false) == nil {
		t.Fatal("symlink config followed")
	}
}
func TestNotifyChainPreservesArgvAndExitCode(t *testing.T) {
	s := testStore(t)
	_, _ = s.installation()
	script := filepath.Join(t.TempDir(), "old-notify.sh")
	result := filepath.Join(t.TempDir(), "result")
	body := "#!/bin/sh\n[ \"$1\" = '--existing-argument' ] || exit 8\n[ \"$2\" = 'raw payload with $() and quotes' ] || exit 9\nprintf ok > '" + result + "'\nexit 7\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("hooks-codex.json", HookRecord{Chain: []string{script, "--existing-argument"}}); err != nil {
		t.Fatal(err)
	}
	if code := runNotifyChain(s, "raw payload with $() and quotes"); code != 7 {
		t.Fatal("exit code changed", code)
	}
	b, _ := os.ReadFile(result)
	if strings.TrimSpace(string(b)) != "ok" {
		t.Fatal("argv changed")
	}
}

func TestClaudeDoesNotAdoptExistingMatchingCommands(t *testing.T) {
	command := shellQuote("/opt/bin/mobterm-cli") + " notify --source claude"
	for _, includeNotification := range []bool{false, true} {
		hooks := map[string]any{
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}, "user_metadata": "keep"}},
		}
		if includeNotification {
			hooks["Notification"] = []any{map[string]any{"matcher": "user_matcher", "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 99}}}}
		}
		original, _ := json.Marshal(map[string]any{"theme": "dark", "hooks": hooks})
		untouched, _, err := editClaude(original, "uninstall", "p", "/opt/bin/mobterm-cli", HookRecord{})
		if err != nil || !bytes.Equal(untouched, original) {
			t.Fatal("uninstall without ownership changed user hooks", err)
		}
		after, record, err := editClaude(original, "install", "p", "/opt/bin/mobterm-cli", HookRecord{})
		if err != nil {
			t.Fatal(err)
		}
		if _, owned := record.ClaudeOwned["UserPromptSubmit"]; owned {
			t.Fatal("adopted existing user hook")
		}
		again, record, err := editClaude(after, "install", "p", "/opt/bin/mobterm-cli", record)
		if err != nil || !bytes.Equal(again, after) {
			t.Fatal("partial ownership was not idempotent", err)
		}
		removed, _, err := editClaude(after, "uninstall", "p", "/opt/bin/mobterm-cli", record)
		if err != nil {
			t.Fatal(err)
		}
		var beforeJSON, afterJSON any
		_ = json.Unmarshal(original, &beforeJSON)
		_ = json.Unmarshal(removed, &afterJSON)
		before, _ := json.Marshal(beforeJSON)
		afterNormalized, _ := json.Marshal(afterJSON)
		if !bytes.Equal(before, afterNormalized) {
			t.Fatalf("user hooks changed: %s", removed)
		}
	}
}

func TestClaudeManagedEditsLeaveFileAndRecordUnchanged(t *testing.T) {
	for _, edit := range []string{"timeout", "matcher", "metadata", "command", "duplicate"} {
		t.Run(edit, func(t *testing.T) {
			s := testStore(t)
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := changeHooks(context.Background(), s, "claude", "install", path, "/opt/bin/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			var root map[string]any
			_ = json.Unmarshal(data, &root)
			hooks := root["hooks"].(map[string]any)
			entry := hooks["Notification"].([]any)[0].(map[string]any)
			command := entry["hooks"].([]any)[0].(map[string]any)
			switch edit {
			case "timeout":
				command["timeout"] = 99
			case "matcher":
				entry["matcher"] = "permission_prompt"
			case "metadata":
				entry["user_metadata"] = "keep"
			case "command":
				command["command"] = "user replacement"
			case "duplicate":
				hooks["Notification"] = append(hooks["Notification"].([]any), entry)
			}
			edited, _ := json.Marshal(root)
			if err := os.WriteFile(path, edited, 0600); err != nil {
				t.Fatal(err)
			}
			recordPath := filepath.Join(s.Dir, "hooks-claude.json")
			recordBefore, _ := os.ReadFile(recordPath)
			for _, action := range []string{"install", "uninstall"} {
				if err := changeHooks(context.Background(), s, "claude", action, path, "/opt/bin/mobterm-cli", false); err == nil {
					t.Fatal("managed edit accepted", action)
				}
				current, _ := os.ReadFile(path)
				recordAfter, _ := os.ReadFile(recordPath)
				if !bytes.Equal(current, edited) || !bytes.Equal(recordBefore, recordAfter) {
					t.Fatal("managed edit or ownership record overwritten", action)
				}
			}
		})
	}
}

func TestClaudeUninstallPreservesCommandsAddedToManagedEntry(t *testing.T) {
	after, record, err := editClaude(nil, "install", "p", "/opt/bin/mobterm-cli", HookRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(after, &root)
	hooks := root["hooks"].(map[string]any)
	entry := hooks["Notification"].([]any)[0].(map[string]any)
	entry["hooks"] = append(entry["hooks"].([]any), map[string]any{"type": "command", "command": "user-added", "timeout": 42})
	edited, _ := json.Marshal(root)
	removed, _, err := editClaude(edited, "uninstall", "p", "/opt/bin/mobterm-cli", record)
	if err != nil || !bytes.Contains(removed, []byte("user-added")) || bytes.Contains(removed, []byte("mobterm-cli")) || !bytes.Contains(removed, []byte(`"timeout": 42`)) {
		t.Fatal("uninstall lost additional user command", err, string(removed))
	}
}

func TestClaudeLegacyOwnershipRequiresManualReview(t *testing.T) {
	for _, modified := range []bool{false, true} {
		s := testStore(t)
		path := filepath.Join(t.TempDir(), "settings.json")
		data, record, err := editClaude([]byte(`{"theme":"dark"}`), "install", path, "/opt/bin/mobterm-cli", HookRecord{})
		if err != nil {
			t.Fatal(err)
		}
		if modified {
			data = bytes.Replace(data, []byte(`"timeout": 10`), []byte(`"timeout": 99`), 1)
		}
		record.ClaudeOwned = nil // Earlier records cannot distinguish user hooks.
		if err := ensureDir(s.Dir, true); err != nil {
			t.Fatal(err)
		}
		if err := s.writeJSON("hooks-claude.json", record); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		recordPath := filepath.Join(s.Dir, "hooks-claude.json")
		recordBefore, _ := os.ReadFile(recordPath)
		for _, action := range []string{"install", "uninstall"} {
			err := changeHooks(context.Background(), s, "claude", action, path, "/opt/bin/mobterm-cli", false)
			if err == nil || !strings.Contains(err.Error(), "manually review") {
				t.Fatal("legacy ownership did not require manual review", action, err)
			}
			current, _ := os.ReadFile(path)
			recordAfter, _ := os.ReadFile(recordPath)
			if !bytes.Equal(current, data) || !bytes.Equal(recordBefore, recordAfter) {
				t.Fatal("legacy configuration or record changed", action)
			}
		}
	}
}

func TestClaudeRejectsUnknownNullShapes(t *testing.T) {
	for _, data := range []string{
		`{"hooks":null}`,
		`{"hooks":{"Notification":null}}`,
		`{"hooks":{"Stop":null}}`,
		`{"hooks":{"Notification":[null]}}`,
		`{"hooks":{"Notification":[{"hooks":null}]}}`,
		`{"hooks":{"Notification":[{"hooks":[null]}]}}`,
	} {
		if _, _, err := editClaude([]byte(data), "install", "p", "/opt/bin/mobterm-cli", HookRecord{}); err == nil {
			t.Fatal("unsupported hook shape accepted", data)
		}
	}
}
