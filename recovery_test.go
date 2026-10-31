package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalRetireExactIdentityAndCapacity(t *testing.T) {
	s := testStore(t)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var originals []Pair
	for i := 0; i < 32; i++ {
		p := testPair(id, "https://api.invalid")
		p.PairID = fmt.Sprintf("pending_%024d", i)
		p.Status = "pending"
		if err = s.savePair(ctx, p); err != nil {
			t.Fatal(err)
		}
		originals = append(originals, p)
	}
	other := testPair(id, "https://other.invalid")
	other.PairID = "other_active_pair_000000"
	if err = s.savePair(ctx, other); err != nil {
		t.Fatal(err)
	}
	p := originals[0]
	packet := RetirePacket{1, p.APIURL, p.PairID, id}
	for _, mutation := range []string{"origin", "installation", "version", "http", "unknown_field"} {
		bad := packet
		switch mutation {
		case "origin":
			bad.APIURL = "https://other.invalid"
		case "installation":
			bad.InstallationID = randomID()
		case "version":
			bad.V = 2
		case "http":
			bad.APIURL = "http://api.invalid"
		}
		frame := framed(bad)
		if mutation == "unknown_field" {
			frame = framed(map[string]any{"v": 1, "api_url": p.APIURL, "pair_id": p.PairID, "installation_id": id, "send_token": "never accepted"})
		}
		if _, err = retireLocalPair(ctx, s, bytes.NewReader(frame)); err == nil {
			t.Fatal("accepted", mutation)
		}
	}
	for i := 0; i < 2; i++ {
		result, e := retireLocalPair(ctx, Store{s.Dir}, bytes.NewReader(framed(packet)))
		if e != nil {
			t.Fatal(e)
		}
		want := "local_retired"
		if i > 0 {
			want = "local_absent"
		}
		if result["status"] != want || result["pair_id"] != p.PairID || result["installation_id"] != id {
			t.Fatal(result)
		}
	}
	if err = s.savePair(ctx, p); err == nil {
		t.Fatal("late setup resurrected retired ID")
	}
	replacement := p
	replacement.PairID = randomID()
	if err = s.savePair(ctx, replacement); err != nil {
		t.Fatal("retirement did not free pending capacity", err)
	}
	remaining, _ := s.pairs()
	if len(remaining) != 33 || remaining[other.PairID] != other {
		t.Fatal("other pair changed")
	}
	// The absent setup path is cancelled before its first local save.
	absent := packet
	absent.PairID = randomID()
	if _, err = retireLocalPair(ctx, s, bytes.NewReader(framed(absent))); err != nil {
		t.Fatal(err)
	}
	late := p
	late.PairID = absent.PairID
	if err = s.savePair(ctx, late); err == nil {
		t.Fatal("late first save resurrected absent ID")
	}
	var history []retiredPair
	if err = s.readJSON("pair-history.json", &history); err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Dir, "pair-history.json"))
	if bytes.Contains(raw, []byte(p.SendToken)) || bytes.Contains(raw, []byte(p.EncryptionKey)) {
		t.Fatal("secret in summary")
	}
	// Failure before the pairs commit cannot be acknowledged; a later invocation
	// repeats the same durable marker and completes only this exact pair.
	blocked := originals[1]
	blockedPacket := RetirePacket{1, blocked.APIURL, blocked.PairID, id}
	before, _ := os.ReadFile(filepath.Join(s.Dir, "pairs.json"))
	if err = os.Remove(filepath.Join(s.Dir, "pairs.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(s.Dir, "pairs.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = retireLocalPair(ctx, s, bytes.NewReader(framed(blockedPacket))); err == nil {
		t.Fatal("storage failure acknowledged")
	}
	if err = os.Remove(filepath.Join(s.Dir, "pairs.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir, "pairs.json"), before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = retireLocalPair(ctx, s, bytes.NewReader(framed(blockedPacket))); err != nil {
		t.Fatal(err)
	}
}
func TestRetirementMarkerAdmissionDoesNotEvictOrChargeRetries(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	ctx := context.Background()
	packet := RetirePacket{1, "https://api.invalid", randomID(), id}
	if _, err := retireLocalPair(ctx, s, bytes.NewReader(framed(packet))); err != nil {
		t.Fatal(err)
	}
	var history []retiredPair
	_ = s.readJSON("pair-history.json", &history)
	for len(history) < maxRetiredPairs {
		history = append(history, retiredPair{PairRef: digest(fmt.Sprint(len(history))), Status: "revoked"})
	}
	if err := s.writeJSON("pair-history.json", history); err != nil {
		t.Fatal(err)
	}
	if _, err := retireLocalPair(ctx, s, bytes.NewReader(framed(packet))); err != nil {
		t.Fatal("retry charged new identity", err)
	}
	packet.PairID = randomID()
	if _, err := retireLocalPair(ctx, s, bytes.NewReader(framed(packet))); err == nil {
		t.Fatal("unbounded new identity")
	}
	var after []retiredPair
	_ = s.readJSON("pair-history.json", &after)
	if !reflect.DeepEqual(history, after) {
		t.Fatal("markers evicted")
	}
}

func classicFixture(t *testing.T, agent string) (Store, string, []byte, []byte, HookRecord) {
	t.Helper()
	s := testStore(t)
	if err := ensureDir(s.Dir, true); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	original := []byte("model='keep'\nnotify=['other-notifier', '--flag']\n")
	if agent == "claude" {
		t.Setenv("CLAUDE_CONFIG_DIR", root)
		path = filepath.Join(root, "settings.json")
		original = []byte(`{"theme":"keep","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"user-hook"}]}],"Notification":[{"matcher":"^(permission_prompt|elicitation_dialog|idle_prompt)$","hooks":[{"type":"command","command":"other-hook"}]}]}}`)
	} else {
		t.Setenv("CODEX_HOME", root)
	}
	var installed []byte
	var record HookRecord
	var err error
	if agent == "codex" {
		installed, record, err = editCodex(original, "install", path, "/bin/mobterm-cli", HookRecord{})
	} else {
		installed, record, err = editClaude(original, "install", path, "/bin/mobterm-cli", HookRecord{})
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, path, original, installed, record
}
func TestClassicHookJournalResumesEveryCommitBoundary(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, action := range []string{"install", "uninstall"} {
			for _, stage := range []string{"journal", "record", "config", "ownership_done"} {
				t.Run(agent+"/"+action+"/"+stage, func(t *testing.T) {
					s, path, original, installed, record := classicFixture(t, agent)
					before, after := original, installed
					var beforeRecord, afterRecord *HookRecord
					afterRecord = &record
					if action == "uninstall" {
						before = installed
						beforeRecord = &record
						afterRecord = nil
						var err error
						if agent == "codex" {
							after, _, err = editCodex(installed, action, path, "/bin/mobterm-cli", record)
						} else {
							after, _, err = editClaude(installed, action, path, "/bin/mobterm-cli", record)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					tx := hookTransaction{V: 1, Agent: agent, Path: path, Action: action, BeforeSHA: digest(string(before)), BeforeExists: true, After: after, AfterSHA: digest(string(after)), Mode: 0640, BeforeRecord: beforeRecord, AfterRecord: afterRecord}
					if err := os.WriteFile(path, before, 0640); err != nil {
						t.Fatal(err)
					}
					if beforeRecord != nil {
						if err := s.writeJSON("hooks-"+agent+".json", beforeRecord); err != nil {
							t.Fatal(err)
						}
					}
					if err := writeHookTransaction(s, tx); err != nil {
						t.Fatal(err)
					}
					if stage != "journal" && action == "install" {
						if err := s.writeJSON("hooks-"+agent+".json", afterRecord); err != nil {
							t.Fatal(err)
						}
					}
					if stage == "config" || stage == "ownership_done" {
						if err := atomicWrite(path, after, 0640); err != nil {
							t.Fatal(err)
						}
					}
					if stage == "ownership_done" && action == "uninstall" {
						if err := os.Remove(filepath.Join(s.Dir, "hooks-"+agent+".json")); err != nil {
							t.Fatal(err)
						}
					}
					snapshot, _ := os.ReadFile(filepath.Join(s.Dir, hookTransactionName(agent)))
					h := inspectHook(Store{s.Dir}, agent)
					want := "continue_install"
					if action == "uninstall" {
						want = "finish_uninstall"
					}
					if h.RecoveryAction != want {
						t.Fatal(h)
					}
					untouched, _ := os.ReadFile(filepath.Join(s.Dir, hookTransactionName(agent)))
					if !bytes.Equal(snapshot, untouched) {
						t.Fatal("read-only health changed journal")
					}
					opposite := "uninstall"
					if action == opposite {
						opposite = "install"
					}
					if changeHooks(context.Background(), s, agent, opposite, path, "/bin/mobterm-cli", false) == nil {
						t.Fatal("opposite operation skipped pending intent")
					}
					if err := changeHooks(context.Background(), Store{s.Dir}, agent, action, path, "/bin/mobterm-cli", false); err != nil {
						t.Fatal(err)
					}
					actual, _ := os.ReadFile(path)
					if !bytes.Equal(actual, after) {
						t.Fatal("recovery altered user notifier or hooks")
					}
					info, _ := os.Stat(path)
					if info.Mode().Perm() != 0640 {
						t.Fatal("permission changed")
					}
					if _, err := os.Stat(filepath.Join(s.Dir, hookTransactionName(agent))); !os.IsNotExist(err) {
						t.Fatal("journal not removed", err)
					}
					if err := changeHooks(context.Background(), s, agent, action, path, "/bin/mobterm-cli", false); err != nil {
						t.Fatal("not idempotent", err)
					}
				})
			}
		}
	}
}
func TestClassicJournalConflictNeverOverwrites(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, scenario := range []string{"edit", "symlink", "root", "corrupt", "ownership", "journal_ownership"} {
			t.Run(agent+"/"+scenario, func(t *testing.T) {
				s, path, before, after, record := classicFixture(t, agent)
				tx := hookTransaction{V: 1, Agent: agent, Path: path, Action: "install", BeforeSHA: digest(string(before)), BeforeExists: true, After: after, AfterSHA: digest(string(after)), Mode: 0600, AfterRecord: &record}
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
				if err := writeHookTransaction(s, tx); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "edit":
					_ = os.WriteFile(path, []byte("user changed content"), 0600)
				case "symlink":
					_ = os.Remove(path)
					target := filepath.Join(t.TempDir(), "target")
					_ = os.WriteFile(target, before, 0600)
					_ = os.Symlink(target, path)
				case "root":
					path = filepath.Join(t.TempDir(), "new-config")
				case "corrupt":
					_ = os.WriteFile(filepath.Join(s.Dir, hookTransactionName(agent)), []byte("{"), 0600)
				case "journal_ownership":
					durable, e := readHookTransaction(s, agent)
					if e != nil {
						t.Fatal(e)
					}
					durable.AfterRecord.Managed = "corrupted but valid JSON"
					if e = s.writeJSON(hookTransactionName(agent), durable); e != nil {
						t.Fatal(e)
					}
				case "ownership":
					record.Managed = "user change"
					_ = s.writeJSON("hooks-"+agent+".json", record)
				}
				snapshot, _ := os.ReadFile(path)
				if err := changeHooks(context.Background(), s, agent, "install", path, "/bin/mobterm-cli", false); err == nil {
					t.Fatal("conflict accepted")
				}
				actual, _ := os.ReadFile(path)
				if !bytes.Equal(snapshot, actual) {
					t.Fatal("user config overwritten")
				}
			})
		}
	}
}
func TestLegacyCodexOriginalEvidenceAndClaudeUncertainOwnership(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			s, path, original, _, record := classicFixture(t, agent)
			_ = s.writeJSON("hooks-"+agent+".json", record)
			_ = os.WriteFile(path, original, 0600)

			if agent == "codex" {
				if h := inspectHook(s, agent); h.RecoveryAction != "uninstall_before_install" {
					t.Fatal("legacy safe cleanup hidden", h)
				}
			}
			err := changeHooks(context.Background(), s, agent, "install", path, "/bin/mobterm-cli", false)
			if agent == "claude" {
				if err == nil {
					t.Fatal("unproven old ownership adopted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = changeHooks(context.Background(), s, agent, "uninstall", path, "/bin/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
			current, _ := os.ReadFile(path)
			if !bytes.Equal(current, original) {
				t.Fatal("original notify lost")
			}
			// Legacy uninstall already restored configuration but retained the record.
			_ = s.writeJSON("hooks-"+agent+".json", record)
			if err = changeHooks(context.Background(), s, agent, "uninstall", path, "/bin/mobterm-cli", false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRetireCommandFrameAndReceipt(t *testing.T) {
	s := testStore(t)
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	id, _ := s.installation()
	packet := RetirePacket{1, "https://api.invalid", randomID(), id}
	var out, diagnostic bytes.Buffer
	if code := run([]string{"pairs", "retire", "--stdin-frame", "--json"}, bytes.NewReader(framed(packet)), &out, &diagnostic); code != 0 {
		t.Fatal(code, diagnostic.String())
	}
	var receipt map[string]any
	if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt["ok"] != true || receipt["status"] != "local_absent" {
		t.Fatal(out.String())
	}
}

func TestNativePendingRecoveryActionsRemainReachable(t *testing.T) {
	for _, agent := range []string{"opencode", "pi"} {
		t.Run(agent, func(t *testing.T) {
			s, path := setupNativeTest(t, agent)
			cli, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			files, reference, err := nativeRenderedFiles(s, agent, path, cli)
			if err != nil {
				t.Fatal(err)
			}
			record := nativeHookRecord{V: 1, Agent: agent, Path: path, Reference: reference, Executable: cli, Store: s.Dir, Revision: nativeAdapterRevision, Pending: "install", Files: map[string]nativeOwnedFile{}}
			for asset, data := range files {
				record.Files[asset] = nativeOwnedFile{SHA: nativeDigest(data)}
			}
			if err = s.writeJSON(nativeRecordName(agent), record); err != nil {
				t.Fatal(err)
			}
			if nativeHooksInstalled(s, agent) {
				t.Fatal("fixture unexpectedly installed")
			}
			if h := inspectHook(s, agent); h.RecoveryAction != "continue_install" {
				t.Fatal(h)
			}
			record.Pending = "uninstall"
			_ = s.writeJSON(nativeRecordName(agent), record)
			if h := inspectHook(s, agent); h.RecoveryAction != "finish_uninstall" {
				t.Fatal(h)
			}
			record.Pending = "install"
			record.Executable = "/previous/mobterm-cli"
			_ = s.writeJSON(nativeRecordName(agent), record)
			if h := inspectHook(s, agent); h.RecoveryAction != "uninstall_before_install" {
				t.Fatal(h)
			}
			if err = changeHooks(context.Background(), s, agent, "uninstall", "", cli, false); err != nil {
				t.Fatal("safe uninstall of pending install unavailable", err)
			}
		})
	}
}

func TestExactRetirementFencesLateClaim(t *testing.T) {
	s := testStore(t)
	installation, _ := s.installation()
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		printJSON(w, map[string]any{"status": "active", "installation_id": installation})
	}))
	defer server.Close()
	p := testPair(installation, server.URL)
	finished := make(chan error, 1)
	go func() {
		_, err := setup(context.Background(), s, API{server.Client()}, bytes.NewReader(framed(p.SetupPacket)))
		finished <- err
	}()
	<-entered
	_, err := retireLocalPair(context.Background(), s, bytes.NewReader(framed(RetirePacket{1, p.APIURL, p.PairID, installation})))
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err == nil {
		t.Fatal("late claim acknowledged after retirement")
	}
	persisted, _ := s.pairs()
	if _, ok := persisted[p.PairID]; ok {
		t.Fatal("retired pair resurrected")
	}
}

func TestExactRetirementResumesPersistedMarkerBeforeCredentialCommit(t *testing.T) {
	s := testStore(t)
	installation, _ := s.installation()
	p := testPair(installation, "https://api.invalid")
	ctx := context.Background()
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// This is the on-disk state after the first fsync/rename succeeded and the
	// subsequent credential commit failed or the short-lived process stopped.
	err = s.recordRetirementLocked(p.PairID, "local_retired", p.APIURL, p.InstallationID)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := s.pairs()
	if persisted[p.PairID] != p {
		t.Fatal("fixture lost credentials before commit")
	}
	receipt, err := retireLocalPair(ctx, Store{s.Dir}, bytes.NewReader(framed(RetirePacket{1, p.APIURL, p.PairID, installation})))
	if err != nil || receipt["status"] != "local_retired" {
		t.Fatal(receipt, err)
	}
	persisted, _ = s.pairs()
	if _, ok := persisted[p.PairID]; ok {
		t.Fatal("active credentials retained")
	}
	var history []retiredPair
	if err = s.readJSON("pair-history.json", &history); err != nil || len(history) != 1 {
		t.Fatal("recovery duplicated marker", err)
	}
}

func TestClassicHookRejectsUnrecoverableOversizedOwnership(t *testing.T) {
	s := testStore(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("notify=['" + strings.Repeat("x", 1100000) + "']\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := changeHooks(context.Background(), s, "codex", "install", path, "/bin/mobterm-cli", false); err == nil {
		t.Fatal("oversized duplicated ownership accepted")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, original) {
		t.Fatal("oversized operation mutated config")
	}
	for _, name := range []string{"hooks-codex.json", hookTransactionName("codex")} {
		if _, err := os.Stat(filepath.Join(s.Dir, name)); !os.IsNotExist(err) {
			t.Fatal("unreadable ownership/journal persisted", name, err)
		}
	}
}

func TestClassicJournalPreservesTightenedPermissions(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, action := range []string{"install", "uninstall"} {
			for _, stage := range []string{"journal", "record", "config", "ownership_done"} {
				t.Run(agent+"/"+action+"/"+stage, func(t *testing.T) {
					s, path, original, installed, record := classicFixture(t, agent)
					before, after := original, installed
					var beforeRecord, afterRecord *HookRecord
					afterRecord = &record
					if action == "uninstall" {
						before, after = installed, original
						beforeRecord, afterRecord = &record, nil
					}
					tx := hookTransaction{V: 1, Agent: agent, Path: path, Action: action, BeforeSHA: digest(string(before)), BeforeExists: true, After: after, AfterSHA: digest(string(after)), Mode: 0644, BeforeRecord: beforeRecord, AfterRecord: afterRecord}
					if err := atomicWrite(path, before, 0644); err != nil {
						t.Fatal(err)
					}
					if beforeRecord != nil {
						if err := s.writeJSON("hooks-"+agent+".json", beforeRecord); err != nil {
							t.Fatal(err)
						}
					}
					// The existing v1 encoding is deliberately unchanged.
					if err := writeHookTransaction(s, tx); err != nil {
						t.Fatal(err)
					}
					if stage != "journal" && action == "install" {
						if err := s.writeJSON("hooks-"+agent+".json", afterRecord); err != nil {
							t.Fatal(err)
						}
					}
					configDone := stage == "config" || stage == "ownership_done"
					if configDone {
						if err := atomicWrite(path, after, 0644); err != nil {
							t.Fatal(err)
						}
					}
					recordPath := filepath.Join(s.Dir, "hooks-"+agent+".json")
					if stage == "ownership_done" && action == "uninstall" {
						if err := os.Remove(recordPath); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Chmod(path, 0600); err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					contents, _ := os.ReadFile(path)
					ownership, _ := os.ReadFile(recordPath)
					journalPath := filepath.Join(s.Dir, hookTransactionName(agent))
					journal, _ := os.ReadFile(journalPath)
					// Health is read-only, including when the permission conflict blocks recovery.
					inspectHook(Store{s.Dir}, agent)
					unchanged, _ := os.ReadFile(journalPath)
					if !bytes.Equal(journal, unchanged) {
						t.Fatal("health changed journal")
					}
					err = changeHooks(context.Background(), Store{s.Dir}, agent, action, path, "/bin/mobterm-cli", false)
					if configDone && err != nil {
						t.Fatal(err)
					}
					if !configDone && (err == nil || !strings.Contains(err.Error(), "permissions")) {
						t.Fatal("expected permission conflict", err)
					}
					current, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					actual, _ := os.ReadFile(path)
					if current.Mode().Perm() != 0600 || !os.SameFile(info, current) || !bytes.Equal(contents, actual) {
						t.Fatal("recovery rewrote configuration or permissions")
					}
					if configDone {
						if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
							t.Fatal("journal not finished", err)
						}
						var saved HookRecord
						err := s.readJSON("hooks-"+agent+".json", &saved)
						if action == "install" && (err != nil || hookRecordDigest(&saved) != hookRecordDigest(afterRecord)) {
							t.Fatal("ownership not committed", err)
						}
						if action == "uninstall" && !os.IsNotExist(err) {
							t.Fatal("ownership not removed", err)
						}
						if err := changeHooks(context.Background(), Store{s.Dir}, agent, action, path, "/bin/mobterm-cli", false); err != nil {
							t.Fatal("not idempotent", err)
						}
					} else {
						actualJournal, _ := os.ReadFile(journalPath)
						actualOwnership, _ := os.ReadFile(recordPath)
						if !bytes.Equal(journal, actualJournal) || !bytes.Equal(ownership, actualOwnership) {
							t.Fatal("conflict modified recovery evidence")
						}
					}
				})
			}
		}
	}
}
