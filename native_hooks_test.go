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

func TestNativeJSONCPreservesUserSyntax(t *testing.T) {
	reference := "/a path/'quoted'/plugin"
	samples := []string{
		"{ // heading\n\"theme\": \"dark\", /* end */ }\n",
		"{\"plugins\": [\"other\", // retained\n], \"disabled\": {\"unrelated\": true,},}",
		"{\"plugins\":[{\"package\":\"unrelated\",\"options\":{\"enabled\":false}}],\"url\":\"https://example.test\"}",
		"{\"plugins\":[],}",
	}
	for _, sample := range samples {
		original := []byte(sample)
		installed, err := editNativePlugin(original, reference, "install", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = parseJSONC(installed); err != nil {
			t.Fatalf("invalid edit: %s: %v", installed, err)
		}
		again, err := editNativePlugin(installed, reference, "install", true, false)
		if err != nil || !bytes.Equal(installed, again) {
			t.Fatal("non-idempotent install", err)
		}
		removed, err := editNativePlugin(installed, reference, "uninstall", true, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = parseJSONC(removed); err != nil {
			t.Fatalf("invalid remove: %s: %v", removed, err)
		}
		for _, preserve := range []string{"// heading", "/* end */", "// retained", "\"enabled\":false", "https://example.test", "\"unrelated\": true,"} {
			if strings.Contains(sample, preserve) && !bytes.Contains(removed, []byte(preserve)) {
				t.Fatalf("lost user syntax %s", preserve)
			}
		}
	}
}
func TestNativeJSONCRejectsAmbiguity(t *testing.T) {
	for _, sample := range []string{
		`{"plugins":null}`, `{"plugins":[42]}`, `{"plugins":["owned","owned"]}`, `{"plugins":[{"package":"owned","options":{}}]}`,
		`{"plugins":[],"plugins":[]}`, `{"plugins":[{"package":"other","bogus":false}]}`, `{"plugins":["owned"]} trailing`,
	} {
		if _, err := editNativePlugin([]byte(sample), "owned", "install", true, false); err == nil {
			t.Fatal("accepted", sample)
		}
	}
	if _, err := editNativePlugin([]byte(`{"plugins":["owned"]}`), "owned", "install", false, false); err == nil {
		t.Fatal("adopted unowned reference")
	}
	if _, err := editNativePlugin([]byte(`{"plugins":[]}`), "owned", "install", true, false); err == nil {
		t.Fatal("restored user removed reference")
	}
}
func setupNativeTest(t *testing.T, agent string) (Store, string) {
	t.Helper()
	s := testStore(t)
	if err := ensureDir(s.Dir, true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if agent == "opencode" {
		t.Setenv("OPENCODE_CONFIG_DIR", root)
	} else {
		t.Setenv("PI_CODING_AGENT_DIR", root)
	}
	path, err := nativeConfigPath(agent)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}
func TestNativeInstallUninstallAndRootDrift(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s, path := setupNativeTest(t, agent)
			ctx := context.Background()
			cli := "/opt/a path/'quoted'/mobterm-cli"
			if agent == "opencode" {
				if err := os.WriteFile(path, []byte("{\"plugins\":[\"other\",], // keep\n\"theme\":\"dark\",}\n"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := changeHooks(ctx, s, agent, "install", "", cli, false); err != nil {
					t.Fatal(err)
				}
			}
			if !nativeHooksInstalled(s, agent) {
				t.Fatal("installed assets not reported")
			}
			var record nativeHookRecord
			if err := s.readJSON(nativeRecordName(agent), &record); err != nil {
				t.Fatal(err)
			}
			for asset := range record.Files {
				data, _ := os.ReadFile(asset)
				if bytes.Contains(data, []byte("__MOBTERM_")) {
					t.Fatal("unexpanded template")
				}
			}
			// Upgrade only owned assets to a newly selected absolute CLI path.
			if err := changeHooks(ctx, s, agent, "install", "", "/new path/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
			if agent == "opencode" {
				t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
			} else {
				t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
			}
			if nativeHooksInstalled(s, agent) {
				t.Fatal("root drift reported active")
			}
			if err := changeHooks(ctx, s, agent, "install", "", cli, false); err == nil {
				t.Fatal("root drift installed")
			}
			// Uninstall uses its recorded root and requires neither PATH nor Agent.
			t.Setenv("PATH", "")
			if err := changeHooks(ctx, s, agent, "uninstall", "", "", true); err != nil {
				t.Fatal(err)
			}
			for asset := range record.Files {
				if _, err := os.Lstat(asset); !os.IsNotExist(err) {
					t.Fatal("owned asset remains")
				}
			}
			if agent == "opencode" {
				data, _ := os.ReadFile(path)
				if !bytes.Contains(data, []byte("// keep")) || !bytes.Contains(data, []byte("\"other\"")) {
					t.Fatal("lost config")
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0640 {
					t.Fatal("config mode changed")
				}
			}
			if err := changeHooks(ctx, s, agent, "uninstall", "", "", true); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeProtectsUnownedAndModifiedAssets(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s, path := setupNativeTest(t, agent)
			files, _, _ := nativeRenderedFiles(s, agent, path, "/bin/mobterm-cli")
			var asset string
			for p := range files {
				asset = p
				break
			}
			if err := os.MkdirAll(filepath.Dir(asset), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(asset, []byte("user content"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := changeHooks(context.Background(), s, agent, "install", "", "/bin/mobterm-cli", false); err == nil {
				t.Fatal("adopted unowned asset")
			}
			_ = os.Remove(asset)
			if err := changeHooks(context.Background(), s, agent, "install", "", "/bin/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(asset, []byte("user edit"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"install", "uninstall"} {
				if err := changeHooks(context.Background(), s, agent, action, "", "/bin/mobterm-cli", false); err == nil {
					t.Fatal("overwrote user edit", action)
				}
			}
			data, _ := os.ReadFile(asset)
			if string(data) != "user edit" {
				t.Fatal("modified asset altered")
			}
		})
	}
}
func TestNativeRejectsFileAndParentSymlinks(t *testing.T) {
	for _, parent := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "parent"}[parent], func(t *testing.T) {
			s, path := setupNativeTest(t, "pi")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if parent {
				_ = os.Remove(filepath.Dir(path))
				if err := os.Symlink(t.TempDir(), filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(t.TempDir(), "target")
				_ = os.WriteFile(target, []byte("keep"), 0600)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := changeHooks(context.Background(), s, "pi", "install", "", "/bin/mobterm-cli", false); err == nil {
				t.Fatal("followed symlink")
			}
		})
	}
}
func TestNativeInterruptedInstallAndUninstallAreRetryable(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s, path := setupNativeTest(t, agent)
			cli := "/bin/mobterm-cli"
			files, reference, err := nativeRenderedFiles(s, agent, path, cli)
			if err != nil {
				t.Fatal(err)
			}
			record := nativeHookRecord{V: 1, Agent: agent, Path: path, Reference: reference, Executable: cli, Store: s.Dir, Revision: 1, Pending: "install", Files: map[string]nativeOwnedFile{}}
			for asset, data := range files {
				record.Files[asset] = nativeOwnedFile{SHA: nativeDigest(data)}
			}
			if err = s.writeJSON(nativeRecordName(agent), record); err != nil {
				t.Fatal(err)
			}
			if err = changeHooks(context.Background(), s, agent, "install", "", cli, false); err != nil {
				t.Fatal(err)
			}
			if !nativeHooksInstalled(s, agent) {
				t.Fatal("retry incomplete")
			}
			if err = s.readJSON(nativeRecordName(agent), &record); err != nil {
				t.Fatal(err)
			}
			record.Pending = "uninstall"
			if err = s.writeJSON(nativeRecordName(agent), record); err != nil {
				t.Fatal(err)
			}
			if agent == "opencode" {
				data, _ := os.ReadFile(path)
				data, err = editNativePlugin(data, reference, "uninstall", true, false)
				if err != nil {
					t.Fatal(err)
				}
				_ = os.WriteFile(path, data, 0600)
			}
			for asset := range record.Files {
				_ = os.Remove(asset)
				break
			}
			if err = changeHooks(context.Background(), s, agent, "uninstall", "", "", false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativePiEffectiveDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, setting := range []string{"", "~/custom pi", "~"} {
		t.Setenv("PI_CODING_AGENT_DIR", setting)
		path, err := nativeConfigPath("pi")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".pi", "agent", "extensions", "mobterm-jumpto.ts")
		if setting == "~/custom pi" {
			want = filepath.Join(home, "custom pi", "extensions", "mobterm-jumpto.ts")
		} else if setting == "~" {
			want = filepath.Join(home, "extensions", "mobterm-jumpto.ts")
		}
		if path != want {
			t.Fatal(path, want)
		}
	}
}
func TestNativeJSONCEscapedReference(t *testing.T) {
	ref := "/a/\"quote\"/\\backslash"
	encoded, _ := json.Marshal(ref)
	data := []byte(`{"plugins":[` + string(encoded) + `, /* user's trailing comment */]}`)
	removed, err := editNativePlugin(data, ref, "uninstall", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(removed, []byte("/* user's trailing comment */")) {
		t.Fatal("comment removed")
	}
	if _, err = parseJSONC(removed); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMissingVersionDoesNotCreateConfiguration(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s := testStore(t)
			root := filepath.Join(t.TempDir(), "not-created")
			t.Setenv("PATH", "")
			if agent == "opencode" {
				t.Setenv("OPENCODE_CONFIG_DIR", root)
			} else {
				t.Setenv("PI_CODING_AGENT_DIR", root)
			}
			if err := changeHooks(context.Background(), s, agent, "install", "", "/bin/mobterm-cli", true); err == nil {
				t.Fatal("installed missing agent")
			}
			for _, path := range []string{root, s.Dir} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("created %s before version accepted", path)
				}
			}
		})
	}
}
func TestNativeSeparateStoresDoNotCoOwnIntegration(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s, path := setupNativeTest(t, agent)
			other := testStore(t)
			if err := changeHooks(context.Background(), s, agent, "install", "", "/bin/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if err := changeHooks(context.Background(), other, agent, "install", "", "/bin/mobterm-cli", false); err == nil {
				t.Fatal("second store installed same integration")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("second store changed integration")
			}
			if err := changeHooks(context.Background(), other, agent, "uninstall", "", "", false); err != nil {
				t.Fatal(err)
			}
			if !nativeHooksInstalled(s, agent) {
				t.Fatal("unowned uninstall changed integration")
			}
		})
	}
}
