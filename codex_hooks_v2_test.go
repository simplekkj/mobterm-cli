package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexNativeVersionGate(t *testing.T) {
	for _, sample := range []struct {
		version string
		want    bool
	}{{"codex-cli 0.100.0", false}, {"codex-cli 0.163.0", false}, {"codex-cli 0.166.0", true}, {"codex-cli 0.166.0-beta", false}, {"codex-cli 0.167.0", true}, {"0.166.0", false}} {
		if got := codexNativeVersion(sample.version); got != sample.want {
			t.Fatalf("%s: %v", sample.version, got)
		}
	}
}
func TestCodexNativeOneTransactionPreservesOtherHooksAndNotify(t *testing.T) {
	original := []byte("# keep\nnotify=['other','--arg'] # exact\n[[hooks.PreToolUse]]\nmatcher='Bash'\n[[hooks.PreToolUse.hooks]]\ntype='command'\ncommand='third-party' # keep hook\n")
	after, r, err := editCodexWithNative(original, "install", "/tmp/config.toml", "/opt/bin/mobterm-cli", HookRecord{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("# keep hook")) || len(r.Chain) != 2 || r.CodexNative == "" {
		t.Fatal("lost config")
	}
	var parsed map[string]any
	if err = toml.Unmarshal(after, &parsed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "hooks.Stop") || strings.Contains(string(after), "trust") {
		t.Fatal("installed duplicate completion or trust")
	}
	hooks := parsed["hooks"].(map[string]any)
	permission := hooks["PermissionRequest"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if permission["timeout"] != int64(130) || !strings.Contains(permission["command"].(string), "approval wait --source codex") {
		t.Fatal("invalid native wait configuration")
	}
	again, r, err := editCodexWithNative(after, "install", "/tmp/config.toml", "/opt/bin/mobterm-cli", r, true)
	if err != nil || !bytes.Equal(after, again) {
		t.Fatal("not idempotent", err)
	}
	userEdit := []byte("\n[unrelated]\nsetting = 42 # later\n")
	removed, _, err := editCodexWithNative(append(after, userEdit...), "uninstall", "/tmp/config.toml", "/opt/bin/mobterm-cli", r, false)
	if err != nil || !bytes.Equal(removed, append(original, userEdit...)) {
		t.Fatal("lost user edits", err)
	}
}
func TestCodexNativeConflictsDoNotAlterNotify(t *testing.T) {
	after, r, err := editCodexWithNative([]byte("model='keep'\n"), "install", "p", "/opt/bin/mobterm-cli", HookRecord{}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{bytes.Replace(after, []byte("timeout = 130"), []byte("timeout = 99"), 1), append(append([]byte{}, after...), []byte("userField='keep'\n")...)} {
		if _, _, err := editCodexWithNative(data, "uninstall", "p", "/opt/bin/mobterm-cli", r, false); err == nil {
			t.Fatal("accepted ambiguous edit")
		}
	}
	if _, _, err := editCodexWithNative(after, "install", "p", "/opt/bin/mobterm-cli", HookRecord{}, true); err == nil {
		t.Fatal("adopted unowned hooks")
	}
}
func TestCodexNativeOldVersionKeepsLegacyNotify(t *testing.T) {
	after, r, err := editCodexWithNative([]byte("model='keep'\n"), "install", "p", "/opt/bin/mobterm-cli", HookRecord{}, false)
	if err != nil || r.Managed == "" || r.CodexNative != "" || bytes.Contains(after, []byte("[[hooks.")) {
		t.Fatal("legacy support changed", err)
	}
	upgraded, r, err := editCodexWithNative(after, "install", "p", "/opt/bin/mobterm-cli", r, true)
	if err != nil || r.CodexNative == "" {
		t.Fatal("upgrade failed", err)
	}
	restored, _, err := editCodexWithNative(upgraded, "uninstall", "p", "/opt/bin/mobterm-cli", r, false)
	if err != nil || string(restored) != "model='keep'\n" {
		t.Fatal("upgrade uninstall not reversible", err)
	}
}

func TestCodexNativeInstalledTransactionAndHealth(t *testing.T) {
	s, bin := discoveryEnvironment(t)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nprintf 'codex-cli 0.166.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	for _, action := range []string{"install", "install"} {
		if err := changeHooks(context.Background(), s, "codex", action, "", exe, true); err != nil {
			t.Fatal(err)
		}
	}
	var r HookRecord
	if err := s.readJSON("hooks-codex.json", &r); err != nil || r.CodexNative == "" {
		t.Fatal("missing native ownership", err)
	}
	h := inspectHook(s, "codex")
	if h.State != "complete" || h.NativeTrust != "review_required" || h.MobileApproval != "configured_default_off" || !h.RestartRequired {
		t.Fatal("disk install falsely claimed loaded", h)
	}
	path, _ := configPath("codex")
	before, _ := os.ReadFile(path)
	if err := changeHooks(context.Background(), s, "codex", "uninstall", "", exe, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if bytes.Contains(after, []byte("mobterm-cli")) || len(before) == 0 {
		t.Fatal("uninstall left owned entries")
	}
	if _, err := readHookTransaction(s, "codex"); !os.IsNotExist(err) {
		t.Fatal("journal not completed", err)
	}
}

func TestCodexNativeDisabledSettingsAreNotRewritten(t *testing.T) {
	for _, config := range []string{"allow_managed_hooks_only=true\n", "[features]\nhooks=false\n", "[features]\ncodex_hooks=false\n"} {
		if !codexNativeDisabled([]byte(config)) {
			t.Fatal("disabled hooks not detected")
		}
		after, r, err := editCodexWithNative([]byte(config), "install", "p", "/opt/bin/mobterm-cli", HookRecord{}, true)
		if err != nil || !bytes.Contains(after, []byte(config)) || !codexNativeDisabled(after) {
			t.Fatal("install bypassed disabled hook setting", err)
		}
		removed, _, err := editCodexWithNative(after, "uninstall", "p", "/opt/bin/mobterm-cli", r, false)
		if err != nil || string(removed) != config {
			t.Fatal("settings not preserved", err)
		}
	}
	if codexNativeDisabled([]byte("[features]\nhooks=true\ncodex_hooks=false\n")) {
		t.Fatal("deprecated alias overrode explicit current feature")
	}
}
