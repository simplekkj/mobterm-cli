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
