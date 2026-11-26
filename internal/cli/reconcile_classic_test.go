package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func classicProbe(agent string) agentProbe {
	return agentProbe{status: probeOK, actual: agentOutput(agent, agentCompatibilities[agent].reference)}
}

func TestReconcileClassicNotifyOnly(t *testing.T) {
	for _, source := range []string{"toml", "json"} {
		t.Run(source, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
			original := "notify = ['third-party', '--flag']\nmodel='keep'\n" + trustCodexTOML
			if source == "toml" {
				original += userCodexTOML
			} else {
				writeLayoutFile(t, jsonPath, []byte(userCodexJSON), 0640)
			}
			before, record, err := editCodex([]byte(original), "install", path, exe, HookRecord{})
			if err != nil || s.writeJSON("hooks-codex.json", record) != nil {
				t.Fatal(err)
			}
			writeLayoutFile(t, path, before, 0640)
			reconcile := func(check bool) reconcileComponent {
				return reconcileClassic(context.Background(), s, "codex", exe, classicProbe("codex"), check)
			}
			if result := reconcile(true); result.Status != "available" || result.Activation != "native_review_required" {
				t.Fatal(result)
			}
			if _, err = os.Lstat(path + ".mobterm-lock"); !os.IsNotExist(err) {
				t.Fatal("check created config lock")
			}
			result := reconcile(false)
			if result.Status != "updated" || result.Activation != "native_review_required" {
				t.Fatal(result)
			}
			var afterRecord HookRecord
			if err = s.readJSON("hooks-codex.json", &afterRecord); err != nil || afterRecord.Managed != record.Managed || !reflect.DeepEqual(record.Chain, afterRecord.Chain) || afterRecord.Original != record.Original {
				t.Fatal("lost notifier or chain", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Contains(after, before) || !bytes.Contains(after, []byte(trustCodexTOML)) {
				t.Fatal("changed unrelated configuration")
			}
			mtimes := map[string]int64{}
			for _, name := range []string{path, jsonPath, filepath.Join(s.Dir, "hooks-codex.json")} {
				if i, err := os.Stat(name); err == nil {
					mtimes[name] = i.ModTime().UnixNano()
				}
			}
			// A reconnect has no copy of the original write receipt.
			for _, check := range []bool{true, false} {
				if recovered := reconcile(check); recovered.Status != "current" || recovered.Activation != "native_review_required" {
					t.Fatalf("lost disk or trust guidance on reconnect: %+v", recovered)
				}
			}
			for name, stamp := range mtimes {
				i, err := os.Stat(name)
				if err != nil || i.ModTime().UnixNano() != stamp {
					t.Fatal("rewrote", name)
				}
			}
		})
	}
}

func TestReconcileClassicPreservesCodexIntent(t *testing.T) {
	for _, change := range []string{"notify_removed", "notify_edited", "global_disabled", "owned_disabled", "json_removed", "mixed", "changed_path", "unsafe_mode"} {
		t.Run(change, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
			if err := changeClassicHooks(context.Background(), s, "codex", "install", path, exe, true, nil); err != nil {
				t.Fatal(err)
			}
			var r HookRecord
			if err := s.readJSON("hooks-codex.json", &r); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			switch change {
			case "notify_removed":
				writeLayoutFile(t, path, nil, 0600)
			case "notify_edited":
				writeLayoutFile(t, path, bytes.ReplaceAll(data, []byte("--mobterm-chain"), []byte("--user")), 0600)
			case "global_disabled":
				writeLayoutFile(t, path, append(data, []byte("[features]\nhooks=false\n")...), 0600)
			case "owned_disabled":
				writeLayoutFile(t, path, append(data, []byte("[hooks.state.\""+jsonPath+":pre_tool_use:0:0\"]\nenabled=false\n")...), 0600)
			case "mixed":
				writeLayoutFile(t, path, append(data, []byte(userCodexTOML)...), 0600)
			case "json_removed":
				if err := os.Remove(jsonPath); err != nil {
					t.Fatal(err)
				}
			case "changed_path":
				r.Path = filepath.Join(t.TempDir(), "config.toml")
				if err := s.writeJSON("hooks-codex.json", r); err != nil {
					t.Fatal(err)
				}
			case "unsafe_mode":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			beforeJSON, _ := os.ReadFile(jsonPath)
			for _, check := range []bool{true, false} {
				if result := reconcileClassic(context.Background(), s, "codex", exe, classicProbe("codex"), check); result.Status != "attention" {
					t.Fatal(result)
				}
				got, _ := os.ReadFile(path)
				gotJSON, _ := os.ReadFile(jsonPath)
				if !bytes.Equal(before, got) || !bytes.Equal(beforeJSON, gotJSON) {
					t.Fatal("overwrote config")
				}
			}
		})
	}
}

func TestReconcileClassicClaudeRecord(t *testing.T) {
	for _, change := range []string{"old_owned", "old_unproven", "deleted", "disabled", "edited", "unrelated_record", "duplicate_keys"} {
		t.Run(change, func(t *testing.T) {
			s, _, exe := codexLayoutEnvironment(t)
			path, _ := configPath("claude")
			if err := ensureDir(filepath.Dir(path), false); err != nil {
				t.Fatal(err)
			}
			current, record, err := editClaude([]byte(`{"other":9007199254740993,"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"user-bin","type":"command"}]}]}}`), "install", path, exe, HookRecord{})
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]json.RawMessage
			var hooks map[string][]json.RawMessage
			_ = json.Unmarshal(current, &root)
			_ = json.Unmarshal(root["hooks"], &hooks)
			owned := record.ClaudeOwned["PreToolUse"]
			old := bytes.ReplaceAll([]byte(owned), []byte("interaction wait --source claude"), []byte("observe --source claude"))
			old = bytes.ReplaceAll(old, []byte("330"), []byte("10"))
			hooks["PreToolUse"][0] = old
			record.ClaudeOwned["PreToolUse"] = string(old)
			switch change {
			case "old_unproven":
				record.ClaudeOwned = nil
			case "deleted":
				delete(hooks, "PermissionRequest")
			case "disabled":
				root["disableAllHooks"] = json.RawMessage("true")
			case "unrelated_record":
				old = bytes.ReplaceAll(old, []byte(shellQuote(exe)+" observe --source claude"), []byte("other-hook"))
				hooks["PreToolUse"][0] = old
				record.ClaudeOwned["PreToolUse"] = string(old)
			case "edited":
				hooks["PreToolUse"][0] = bytes.ReplaceAll(old, []byte("10"), []byte("42"))
			}
			root["hooks"], _ = json.Marshal(hooks)
			data, _ := json.Marshal(root)
			if change == "duplicate_keys" {
				data = append([]byte(`{"hooks":{},`), data[1:]...)
			}
			writeLayoutFile(t, path, data, 0640)
			if err = s.writeJSON("hooks-claude.json", record); err != nil {
				t.Fatal(err)
			}
			for _, check := range []bool{true, false} {
				got := reconcileClassic(context.Background(), s, "claude", exe, classicProbe("claude"), check)
				want := "attention"
				if change == "old_owned" {
					want = "updated"
					if check {
						want = "available"
					}
				}
				if got.Status != want {
					t.Fatalf("want %v got %+v", want, got)
				}
				after, _ := os.ReadFile(path)
				if want != "updated" && !bytes.Equal(data, after) {
					t.Fatal("config changed without approval")
				}
				if want == "updated" && (!bytes.Contains(after, []byte("interaction wait --source claude")) || !bytes.Contains(after, []byte("user-bin")) || !bytes.Contains(after, []byte("9007199254740993"))) {
					t.Fatal("bad migration")
				}
			}
		})
	}
}

func TestReconcileClassicPending(t *testing.T) {
	for _, stage := range []string{"journal", "ownership", "first_file", "both_files", "incompatible", "other_version"} {
		t.Run(stage, func(t *testing.T) {
			s, tx, exe := codexLayoutJournal(t, "install")
			advanceCodexJournal(t, s, tx, stage)
			if stage == "other_version" {
				tx.V, tx.ReconcileVersion = 3, "different-release"
				if err := writeHookTransaction(s, tx); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "incompatible" {
				tx.AfterRecord.CodexJSON = string(bytes.ReplaceAll([]byte(tx.AfterRecord.CodexJSON), []byte("130"), []byte("99")))
				if err := writeHookTransaction(s, tx); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(s.Dir, hookTransactionName("codex")))
			if stage == "other_version" && changeClassicHooks(context.Background(), s, "codex", "install", tx.Path, exe, false, nil) == nil {
				t.Fatal("explicit install replayed a migration from a different release")
			}
			for _, check := range []bool{true, false} {
				got := reconcileClassic(context.Background(), s, "codex", exe, classicProbe("codex"), check)
				stillPending, err := os.ReadFile(filepath.Join(s.Dir, hookTransactionName("codex")))
				if stage == "incompatible" || stage == "other_version" {
					if got.Reason != "pending_conflict" || !bytes.Equal(before, stillPending) {
						t.Fatal(got, err)
					}
				} else if check {
					if got.Status != "available" || !bytes.Equal(before, stillPending) {
						t.Fatal(got, err)
					}
				} else if got.Status != "updated" || !os.IsNotExist(err) {
					t.Fatal(got, err)
				}
			}
		})
	}
}

func TestReconcileClassicCodexPreservesLaterHookPosition(t *testing.T) {
	s, path, exe := codexLayoutEnvironment(t)
	jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
	notify, record, err := editCodex(nil, "install", path, exe, HookRecord{})
	if err != nil {
		t.Fatal(err)
	}
	record.CodexJSON = string(bytes.ReplaceAll([]byte(codexJSONBlock(exe)), []byte(`"timeout":130`), []byte(`"timeout":120`)))
	if err = s.writeJSON("hooks-codex.json", record); err != nil {
		t.Fatal(err)
	}
	writeLayoutFile(t, path, append(notify, []byte("\n[hooks.state.\""+jsonPath+":permission_request:1:0\"]\nenabled=false\n")...), 0600)
	var groups map[string][]json.RawMessage
	_ = json.Unmarshal([]byte(record.CodexJSON), &groups)
	user := json.RawMessage(`{"hooks":[{"type":"command","command":"user-notification"}]}`)
	groups["PermissionRequest"] = append(groups["PermissionRequest"], user)
	writeLayoutFile(t, jsonPath, encodeCodexJSON(map[string]json.RawMessage{}, groups), 0600)
	got := reconcileClassic(context.Background(), s, "codex", exe, classicProbe("codex"), false)
	if got.Status != "updated" {
		t.Fatal(got)
	}
	result, err := os.ReadFile(jsonPath)
	_, after, parseErr := parseCodexJSON(result)
	if err != nil || parseErr != nil || !equalHookJSON(after["PermissionRequest"][1], user) || !bytes.Contains(after["PermissionRequest"][0], []byte("130")) {
		t.Fatal("changed the identity of a user's later disabled handler")
	}
}
