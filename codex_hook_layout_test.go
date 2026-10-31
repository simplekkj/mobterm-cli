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

const userCodexJSON = `{"description":"keep", "large":9007199254740993,"hooks":{"SessionStart":[{"matcher":"startup", "hooks":[{"type":"command","command":"user-command","timeout":42}]}]}}`
const userCodexTOML = "\n[[hooks.SessionStart]]\nmatcher='startup'\n[[hooks.SessionStart.hooks]]\ntype='command'\ncommand='user-command' # keep comment\ntimeout=42\n"
const trustCodexTOML = "\n[hooks.state.user]\nenabled=false\ntrusted_hash='keep'\n"

func codexLayoutEnvironment(t *testing.T) (Store, string, string) {
	t.Helper()
	s, bin := discoveryEnvironment(t)
	writeLayoutFile(t, filepath.Join(bin, "codex"), []byte("#!/bin/sh\nprintf 'codex-cli 0.166.0\\n'\n"), 0700)
	path, err := configPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = ensureDir(filepath.Dir(path), false); err != nil {
		t.Fatal(err)
	}
	if err = ensureDir(s.Dir, true); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return s, path, executable
}

func writeLayoutFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func TestCodexLayoutSelectionPreservationAndUninstall(t *testing.T) {
	for _, tc := range []struct{ name, toml, json, want string }{
		{"empty", "model='keep'\n", "", "json"},
		{"json", "model='keep'\n", userCodexJSON, "json"},
		{"toml", "model='keep'\n" + userCodexTOML, "", "toml"},
		{"trust_is_not_an_event", "model='keep'\n" + trustCodexTOML, userCodexJSON, "json"},
		{"empty_json_does_not_override_toml", userCodexTOML, `{"hooks":{}}`, "toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
			writeLayoutFile(t, path, []byte(tc.toml), 0640)
			if tc.json != "" {
				writeLayoutFile(t, jsonPath, []byte(tc.json), 0644)
			}
			if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
				t.Fatal(err)
			}
			var record HookRecord
			if err := s.readJSON("hooks-codex.json", &record); err != nil {
				t.Fatal(err)
			}
			if (record.CodexJSON != "") != (tc.want == "json") || (record.CodexNative != "") != (tc.want == "toml") {
				t.Fatal("wrong representation", record)
			}
			if h := inspectHook(s, "codex"); h.ConfigurationState != "complete" || h.NativeTrust != "review_required" {
				t.Fatal(h)
			}
			installedTOML, _ := os.ReadFile(path)
			installedJSON, _ := os.ReadFile(jsonPath)
			tomlInfo, _ := os.Stat(path)
			jsonInfo, _ := os.Stat(jsonPath)
			if !bytes.Contains(installedTOML, []byte(tc.toml)) {
				t.Fatal("unrelated TOML changed")
			}
			if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
				t.Fatal(err)
			}
			againTOML, _ := os.ReadFile(path)
			againJSON, _ := os.ReadFile(jsonPath)
			againInfo, _ := os.Stat(path)
			if !bytes.Equal(installedTOML, againTOML) || !bytes.Equal(installedJSON, againJSON) || !tomlInfo.ModTime().Equal(againInfo.ModTime()) {
				t.Fatal("repeat install rewrote config")
			}
			if jsonInfo != nil {
				info, _ := os.Stat(jsonPath)
				if !info.ModTime().Equal(jsonInfo.ModTime()) || info.Mode() != jsonInfo.Mode() {
					t.Fatal("repeat install rewrote JSON")
				}
			}
			// Later user changes must survive removal of only our groups.
			writeLayoutFile(t, path, append(againTOML, []byte("\n[later]\nkeep=true\n")...), 0640)
			if err := changeHooks(context.Background(), s, "codex", "uninstall", path, exe, false); err != nil {
				t.Fatal(err)
			}
			removedTOML, _ := os.ReadFile(path)
			removedJSON, _ := os.ReadFile(jsonPath)
			if string(removedTOML) != tc.toml+"\n[later]\nkeep=true\n" {
				t.Fatal("lost TOML edits")
			}
			if tc.json != "" && !equalHookJSON(removedJSON, []byte(tc.json)) {
				t.Fatalf("lost JSON values: %s", removedJSON)
			}
			if bytes.Contains(removedJSON, []byte("observe --source codex")) {
				t.Fatal("owned JSON left installed")
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0640 {
				t.Fatal("lost TOML mode")
			}
			if tc.json != "" {
				info, _ = os.Stat(jsonPath)
				if info.Mode().Perm() != 0644 {
					t.Fatal("lost JSON mode")
				}
			}
		})
	}
}

func TestCodexLayoutMigratesOnlyOwnedHooks(t *testing.T) {
	for _, direction := range []string{"toml_to_json", "json_to_toml"} {
		t.Run(direction, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
			original := []byte("model='keep'\n" + trustCodexTOML)
			if direction == "toml_to_json" {
				old, record, err := editCodexWithNative(original, "install", path, exe, HookRecord{}, true)
				if err != nil {
					t.Fatal(err)
				}
				writeLayoutFile(t, path, old, 0640)
				if err = s.writeJSON("hooks-codex.json", record); err != nil {
					t.Fatal(err)
				}
				writeLayoutFile(t, jsonPath, []byte(userCodexJSON), 0644)
			} else {
				writeLayoutFile(t, path, original, 0640)
				if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(path)
				writeLayoutFile(t, path, append(data, []byte(userCodexTOML)...), 0640)
			}
			h := inspectHook(s, "codex")
			if h.ConfigurationState != "partial" || h.Issues[0].Code != "native_hook_migration_required" {
				t.Fatal("App would skip migration", h)
			}
			if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
				t.Fatal(err)
			}
			if h = inspectHook(s, "codex"); h.ConfigurationState != "complete" {
				t.Fatal(h)
			}
			tomlData, _ := os.ReadFile(path)
			jsonData, _ := os.ReadFile(jsonPath)
			if !bytes.Contains(tomlData, []byte(trustCodexTOML)) {
				t.Fatal("trust changed")
			}
			if bytes.Contains(tomlData, []byte("observe --source codex")) == bytes.Contains(jsonData, []byte("observe --source codex")) {
				t.Fatal("handlers missing or duplicated across sources")
			}
			if direction == "toml_to_json" && (!bytes.Contains(jsonData, []byte("user-command")) || bytes.Contains(tomlData, []byte("[[hooks."))) {
				t.Fatal("migration lost JSON user hook or retained TOML source")
			}
			if direction == "json_to_toml" && !bytes.Contains(tomlData, []byte(userCodexTOML)) {
				t.Fatal("migration changed user TOML")
			}
			if err := changeHooks(context.Background(), s, "codex", "uninstall", path, exe, false); err != nil {
				t.Fatal(err)
			}
			if direction == "toml_to_json" {
				jsonData, _ = os.ReadFile(jsonPath)
				if !equalHookJSON(jsonData, []byte(userCodexJSON)) {
					t.Fatal("migration uninstall lost user JSON")
				}
			}
		})
	}
}

func TestCodexLayoutRefusesAmbiguousAndEditedJSONWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"mixed_user", "invalid", "null", "duplicate_keys", "unknown_ownership", "edited", "duplicate_owned", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
			writeLayoutFile(t, path, []byte("model='keep'\n"), 0600)
			var jsonData []byte
			switch scenario {
			case "mixed_user":
				writeLayoutFile(t, path, []byte(userCodexTOML), 0600)
				jsonData = []byte(userCodexJSON)
			case "invalid":
				jsonData = []byte("{")
			case "null":
				jsonData = []byte(`{"hooks":null}`)
			case "duplicate_keys":
				jsonData = []byte(`{"hooks":{},"hooks":{}}`)
			case "unknown_ownership":
				jsonData = []byte(`{"hooks":` + codexJSONBlock(exe) + `}`)
			case "edited", "duplicate_owned":
				if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
					t.Fatal(err)
				}
				jsonData, _ = os.ReadFile(jsonPath)
				if scenario == "edited" {
					jsonData = bytes.ReplaceAll(jsonData, []byte("130"), []byte("99"))
				} else {
					root, hooks, _ := parseCodexJSON(jsonData)
					hooks["PermissionRequest"] = append(hooks["PermissionRequest"], hooks["PermissionRequest"][0])
					jsonData = encodeCodexJSON(root, hooks)
				}
			case "symlink":
				other := filepath.Join(t.TempDir(), "hooks.json")
				writeLayoutFile(t, other, []byte(userCodexJSON), 0600)
				if err := os.Symlink(other, jsonPath); err != nil {
					t.Fatal(err)
				}
			}
			if jsonData != nil {
				writeLayoutFile(t, jsonPath, jsonData, 0600)
			}
			beforeTOML, _ := os.ReadFile(path)
			beforeJSON, _ := os.ReadFile(jsonPath)
			beforeRecord, _ := os.ReadFile(filepath.Join(s.Dir, "hooks-codex.json"))
			if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err == nil {
				t.Fatal("accepted conflict")
			}
			afterTOML, _ := os.ReadFile(path)
			afterJSON, _ := os.ReadFile(jsonPath)
			afterRecord, _ := os.ReadFile(filepath.Join(s.Dir, "hooks-codex.json"))
			if !bytes.Equal(beforeTOML, afterTOML) || !bytes.Equal(beforeJSON, afterJSON) || !bytes.Equal(beforeRecord, afterRecord) {
				t.Fatal("conflict changed config or ownership")
			}
			if scenario == "edited" || scenario == "duplicate_owned" {
				if h := inspectHook(s, "codex"); h.ConfigurationState != "conflict" {
					t.Fatal(h)
				}
				if err := changeHooks(context.Background(), s, "codex", "uninstall", path, exe, false); err == nil {
					t.Fatal("uninstall accepted edited hooks")
				}
			}
		})
	}
}

func TestCodexNotifyOnlyUpgradeAdvertisesRepairAndJSONActivation(t *testing.T) {
	s, path, exe := codexLayoutEnvironment(t)
	if err := changeHooks(context.Background(), s, "codex", "install", path, exe, false); err != nil {
		t.Fatal(err)
	}
	diagnostics := agentDiagnostics(context.Background(), s)
	h := diagnostics["health"].(map[string]agentHealth)["codex"]
	if h.ConfigurationState != "partial" || h.RunState != "not_configured" || diagnostics["hooks"].(map[string]bool)["codex"] != true {
		t.Fatal("legacy migration cannot be requested by App", h)
	}
	var out, diag bytes.Buffer
	if run([]string{"hooks", "install", "--agent", "codex", "--json"}, strings.NewReader(""), &out, &diag) != 0 {
		t.Fatal(diag.String())
	}
	var receipt map[string]any
	if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt["activation"] != "native_review_required" || receipt["ok"] != true {
		t.Fatal(out.String())
	}
	if h = inspectHook(s, "codex"); h.ConfigurationState != "complete" || h.RunState != "configured_unverified" {
		t.Fatal(h)
	}
}

func TestCodexJSONUninstallPreservesLaterUserGroupsAndMixedSources(t *testing.T) {
	s, path, exe := codexLayoutEnvironment(t)
	if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	root, hooks, err := parseCodexJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	userGroup := json.RawMessage(`{"matcher":"Bash","hooks":[{"type":"command","command":"later-user-command"}],"metadata":9007199254740993}`)
	hooks["PermissionRequest"] = append(hooks["PermissionRequest"], userGroup)
	root["later"] = json.RawMessage(`{"keep":9007199254740993}`)
	writeLayoutFile(t, jsonPath, encodeCodexJSON(root, hooks), 0600)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeLayoutFile(t, path, append(data, []byte(userCodexTOML)...), 0600)
	h := inspectHook(s, "codex")
	found := false
	for _, issue := range h.Issues {
		found = found || issue.Code == "native_hook_sources_mixed"
	}
	if h.ConfigurationState != "conflict" || !found {
		t.Fatal("mixed user sources not reported", h)
	}
	if err := changeHooks(context.Background(), s, "codex", "uninstall", path, exe, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(jsonPath)
	root, hooks, err = parseCodexJSON(data)
	if err != nil || len(hooks) != 1 || len(hooks["PermissionRequest"]) != 1 || !equalHookJSON(hooks["PermissionRequest"][0], userGroup) || !equalHookJSON(root["later"], []byte(`{"keep":9007199254740993}`)) {
		t.Fatal("uninstall changed later JSON edits", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != userCodexTOML {
		t.Fatal("uninstall changed user TOML")
	}
}
