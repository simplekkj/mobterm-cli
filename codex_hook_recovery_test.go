package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func codexLayoutJournal(t *testing.T, scenario string) (Store, hookTransaction, string) {
	t.Helper()
	s, path, exe := codexLayoutEnvironment(t)
	jsonPath := filepath.Join(filepath.Dir(path), "hooks.json")
	original := []byte("model='keep'\n" + trustCodexTOML)
	writeLayoutFile(t, path, original, 0640)
	action := "install"
	var beforeRecord *HookRecord
	if scenario == "migrate_to_json" {
		data, r, err := editCodexWithNative(original, "install", path, exe, HookRecord{}, true)
		if err != nil {
			t.Fatal(err)
		}
		writeLayoutFile(t, path, data, 0640)
		writeLayoutFile(t, jsonPath, []byte(userCodexJSON), 0644)
		if err = s.writeJSON("hooks-codex.json", r); err != nil {
			t.Fatal(err)
		}
		beforeRecord = &r
	} else if scenario == "migrate_to_toml" || scenario == "uninstall" {
		if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
			t.Fatal(err)
		}
		var r HookRecord
		if err := s.readJSON("hooks-codex.json", &r); err != nil {
			t.Fatal(err)
		}
		beforeRecord = &r
		if scenario == "migrate_to_toml" {
			data, _ := os.ReadFile(path)
			writeLayoutFile(t, path, append(data, []byte(userCodexTOML)...), 0640)
		} else {
			action = "uninstall"
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := HookRecord{}
	if beforeRecord != nil {
		r = *beforeRecord
	}
	after, record, file, err := editCodexLayout(before, action, path, exe, r, true)
	if err != nil || file == nil {
		t.Fatal("missing JSON transaction", err)
	}
	tx := hookTransaction{V: 2, Agent: "codex", Path: path, Action: action, BeforeSHA: digest(string(before)), BeforeExists: true, After: after, AfterSHA: digest(string(after)), Mode: 0640, BeforeRecord: beforeRecord, JSONFile: file, JSONFirst: r.CodexJSON != ""}
	if action == "install" {
		tx.AfterRecord = &record
	}
	if err = writeHookTransaction(s, tx); err != nil {
		t.Fatal(err)
	}
	tx, err = readHookTransaction(s, "codex")
	if err != nil {
		t.Fatal(err)
	}
	return s, tx, exe
}

func advanceCodexJournal(t *testing.T, s Store, tx hookTransaction, stage string) {
	t.Helper()
	if stage == "journal" {
		return
	}
	if tx.Action == "install" {
		if err := s.writeJSON("hooks-codex.json", tx.AfterRecord); err != nil {
			t.Fatal(err)
		}
	}
	if stage == "ownership" {
		return
	}
	writeTOML := func() {
		if err := atomicWrite(tx.Path, tx.After, os.FileMode(tx.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON := func() {
		if err := tx.JSONFile.finish(); err != nil {
			t.Fatal(err)
		}
	}
	if tx.JSONFirst {
		writeJSON()
	} else {
		writeTOML()
	}
	if stage == "first_file" {
		return
	}
	if tx.JSONFirst {
		writeTOML()
	} else {
		writeJSON()
	}
	if stage == "ownership_done" && tx.Action == "uninstall" {
		if err := removeHookFile(filepath.Join(s.Dir, "hooks-codex.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexLayoutRecoveryEveryWriteBoundary(t *testing.T) {
	for _, scenario := range []string{"install", "migrate_to_json", "migrate_to_toml", "uninstall"} {
		for _, stage := range []string{"journal", "ownership", "first_file", "both_files", "ownership_done"} {
			t.Run(scenario+"/"+stage, func(t *testing.T) {
				s, tx, exe := codexLayoutJournal(t, scenario)
				advanceCodexJournal(t, s, tx, stage)
				beforeHealth, _ := os.ReadFile(filepath.Join(s.Dir, hookTransactionName("codex")))
				h := inspectHook(s, "codex")
				wantRecovery := "continue_install"
				if tx.Action == "uninstall" {
					wantRecovery = "finish_uninstall"
				}
				if h.ConfigurationState != "partial" || h.RecoveryAction != wantRecovery {
					t.Fatal(h)
				}
				afterHealth, _ := os.ReadFile(filepath.Join(s.Dir, hookTransactionName("codex")))
				if !bytes.Equal(beforeHealth, afterHealth) {
					t.Fatal("discovery wrote journal")
				}
				// Even at the interrupted boundary, our handlers are never in both sources.
				tomlData, _ := os.ReadFile(tx.Path)
				jsonData, _ := os.ReadFile(tx.JSONFile.Path)
				if bytes.Contains(tomlData, []byte("observe --source codex")) && bytes.Contains(jsonData, []byte("observe --source codex")) {
					t.Fatal("migration duplicated handlers")
				}
				opposite := "uninstall"
				if tx.Action == opposite {
					opposite = "install"
				}
				if changeHooks(context.Background(), s, "codex", opposite, tx.Path, exe, false) == nil {
					t.Fatal("opposite action consumed pending migration")
				}
				if err := changeHooks(context.Background(), s, "codex", tx.Action, tx.Path, exe, false); err != nil {
					t.Fatal(err)
				}
				tomlData, _ = os.ReadFile(tx.Path)
				jsonData, _ = os.ReadFile(tx.JSONFile.Path)
				if !bytes.Equal(tomlData, tx.After) || !bytes.Equal(jsonData, tx.JSONFile.After) {
					t.Fatal("recovery changed planned result")
				}
				for path, mode := range map[string]uint32{tx.Path: tx.Mode, tx.JSONFile.Path: tx.JSONFile.Mode} {
					info, err := os.Stat(path)
					if err != nil || uint32(info.Mode().Perm()) != mode {
						t.Fatal("recovery lost file mode", path, err)
					}
				}
				if _, err := readHookTransaction(s, "codex"); !os.IsNotExist(err) {
					t.Fatal("journal retained", err)
				}
				if tx.Action == "install" {
					if h = inspectHook(s, "codex"); h.ConfigurationState != "complete" {
						t.Fatal(h)
					}
				} else if h = inspectHook(s, "codex"); h.ConfigurationState != "not_configured" {
					t.Fatal(h)
				}
			})
		}
	}
}

func TestCodexLayoutRecoveryRefusesConcurrentChanges(t *testing.T) {
	for _, stage := range []string{"journal", "first_file"} {
		for _, change := range []string{"toml", "json", "json_mode", "json_symlink", "json_removed"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				s, tx, exe := codexLayoutJournal(t, "migrate_to_json")
				advanceCodexJournal(t, s, tx, stage)
				switch change {
				case "toml":
					data, _ := os.ReadFile(tx.Path)
					writeLayoutFile(t, tx.Path, append(data, []byte("\n[later]\nuser=true\n")...), 0640)
				case "json":
					writeLayoutFile(t, tx.JSONFile.Path, []byte(`{"hooks":{},"later":"keep"}`), 0644)
				case "json_mode":
					if err := os.Chmod(tx.JSONFile.Path, 0600); err != nil {
						t.Fatal(err)
					}
				case "json_symlink":
					other := filepath.Join(t.TempDir(), "hooks.json")
					if err := os.Rename(tx.JSONFile.Path, other); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(other, tx.JSONFile.Path); err != nil {
						t.Fatal(err)
					}
				case "json_removed":
					if err := os.Remove(tx.JSONFile.Path); err != nil {
						t.Fatal(err)
					}
				}
				paths := []string{tx.Path, tx.JSONFile.Path, filepath.Join(s.Dir, "hooks-codex.json"), filepath.Join(s.Dir, hookTransactionName("codex"))}
				before := map[string][]byte{}
				for _, path := range paths {
					before[path], _ = os.ReadFile(path)
				}
				if h := inspectHook(s, "codex"); h.ConfigurationState != "conflict" || h.RecoveryAction != "review_conflict" {
					t.Fatal(h)
				}
				if err := changeHooks(context.Background(), s, "codex", "install", tx.Path, exe, false); err == nil {
					t.Fatal("recovery overwrote user edit")
				}
				for _, path := range paths {
					after, _ := os.ReadFile(path)
					if !bytes.Equal(before[path], after) {
						t.Fatal("conflict changed file", path)
					}
				}
			})
		}
	}
}

func TestCodexLayoutJournalRejectsUnexpectedPathAndVersion(t *testing.T) {
	for _, change := range []string{"path", "version", "hash", "agent", "oversize"} {
		t.Run(change, func(t *testing.T) {
			s, tx, _ := codexLayoutJournal(t, "migrate_to_json")
			switch change {
			case "path":
				tx.JSONFile.Path = filepath.Join(t.TempDir(), "unrelated.json")
			case "version":
				tx.V = 1
			case "hash":
				tx.JSONFile.AfterSHA = tx.BeforeSHA
			case "agent":
				tx.Agent = "claude"
			case "oversize":
				tx.JSONFile.After = bytes.Repeat([]byte(" "), 2*1024*1024+1)
				tx.JSONFile.AfterSHA = digest(string(tx.JSONFile.After))
			}
			if err := writeHookTransaction(s, tx); err != nil {
				t.Fatal(err)
			}
			if _, err := readHookTransaction(s, tx.Agent); err == nil {
				t.Fatal("accepted invalid multi-file journal")
			}
		})
	}
}
