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

func TestStructuredClaudeHealthAndSafeRepair(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	path := filepath.Join(root, "settings.json")
	exe, _ := os.Executable()
	if err := changeHooks(context.Background(), s, "claude", "install", path, exe, false); err != nil {
		t.Fatal(err)
	}
	if h := inspectHook(s, "claude"); h.State != "complete" {
		t.Fatal(h)
	}
	baseline, _ := os.ReadFile(path)
	if err := changeHooks(context.Background(), s, "claude", "install", path, "/different/mobterm-cli", false); err != nil {
		t.Fatal("repeat install must retain the proven owned executable", err)
	}
	if repeated, _ := os.ReadFile(path); !bytes.Equal(baseline, repeated) {
		t.Fatal("repeat install changed owned command paths")
	}
	for _, change := range []string{"missing", "matcher", "timeout", "moved", "duplicate", "command"} {
		t.Run(change, func(t *testing.T) {
			var root map[string]any
			_ = json.Unmarshal(baseline, &root)
			hooks := root["hooks"].(map[string]any)
			entry := hooks["Notification"].([]any)[0].(map[string]any)
			switch change {
			case "missing":
				delete(hooks, "UserPromptSubmit")
			case "matcher":
				entry["matcher"] = "idle_prompt"
			case "timeout":
				entry["hooks"].([]any)[0].(map[string]any)["timeout"] = 11
			case "moved":
				hooks["Stop"] = hooks["Notification"]
				delete(hooks, "Notification")
			case "duplicate":
				hooks["Notification"] = append(hooks["Notification"].([]any), entry)
			case "command":
				entry["hooks"].([]any)[0].(map[string]any)["command"] = "user replacement"
			}
			b, _ := json.Marshal(root)
			_ = os.WriteFile(path, b, 0600)
			h := inspectHook(s, "claude")
			if h.State == "complete" || len(h.Issues) == 0 {
				t.Fatal("broken hook reported complete", h)
			}
			err := changeHooks(context.Background(), s, "claude", "install", path, exe, false)
			if change == "missing" {
				if err != nil {
					t.Fatal(err)
				}
				if h = inspectHook(s, "claude"); h.State != "complete" {
					t.Fatal(h)
				}
			} else {
				if err == nil {
					t.Fatal("user modification overwritten")
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(after, b) {
					t.Fatal("config changed")
				}
			}
		})
	}
}
func TestStructuredCodexCommentRootAndNotifier(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	path := filepath.Join(root, "config.toml")
	exe, _ := os.Executable()
	_ = os.WriteFile(path, []byte("notify = [\"/bin/echo\",\"keep\"]\nother = 42\n"), 0600)
	if err := changeHooks(context.Background(), s, "codex", "install", path, exe, false); err != nil {
		t.Fatal(err)
	}
	if h := inspectHook(s, "codex"); h.State != "complete" {
		t.Fatal(h)
	}
	b, _ := os.ReadFile(path)
	userNote := `note = """
# mobterm-cli managed notify v1
user text
# end mobterm-cli managed notify
"""
`
	b = append(b, []byte(userNote)...)
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "notify =") {
			lines[i] = "# " + line
		}
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
	if h := inspectHook(s, "codex"); h.State != "partial" {
		t.Fatal("comment counted as notify", h)
	}
	if err := changeHooks(context.Background(), s, "codex", "install", path, exe, false); err != nil {
		t.Fatal(err)
	}
	if h := inspectHook(s, "codex"); h.State != "complete" {
		t.Fatal(h)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Contains(after, []byte(userNote)) {
		t.Fatal("repair altered marker text inside user TOML value")
	}
	var r HookRecord
	_ = s.readJSON("hooks-codex.json", &r)
	if len(r.Chain) != 2 || r.Chain[1] != "keep" {
		t.Fatal("original notifier lost")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	if h := inspectHook(s, "codex"); h.State != "conflict" {
		t.Fatal("root drift ignored", h)
	}
}
func TestStructuredNativeAssetsAndRepair(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s := testStore(t)
			root := t.TempDir()
			t.Setenv("OPENCODE_CONFIG_DIR", root)
			t.Setenv("PI_CODING_AGENT_DIR", root)
			exe, _ := os.Executable()
			if err := changeNativeHooks(context.Background(), s, agent, "install", "", exe, false); err != nil {
				t.Fatal(err)
			}
			if h := inspectHook(s, agent); h.State != "complete" || !h.RestartRequired {
				t.Fatal(h)
			}
			var r nativeHookRecord
			_ = s.readJSON(nativeRecordName(agent), &r)
			for path := range r.Files {
				b, _ := os.ReadFile(path)
				_ = os.Remove(path)
				if h := inspectHook(s, agent); h.State != "partial" {
					t.Fatal("missing asset ignored", h)
				}
				if err := changeNativeHooks(context.Background(), s, agent, "install", "", exe, false); err != nil {
					t.Fatal(err)
				}
				if h := inspectHook(s, agent); h.State != "complete" {
					t.Fatal(h)
				}
				_ = os.WriteFile(path, append(b, []byte("// user edit")...), 0600)
				if h := inspectHook(s, agent); h.State != "modified" {
					t.Fatal(h)
				}
				if err := changeNativeHooks(context.Background(), s, agent, "install", "", exe, false); err == nil {
					t.Fatal("user edit replaced")
				}
				break
			}
		})
	}
}
