package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

const reconcileNativeTestCLI = "/installed/bin/mobterm-cli"

type nativeMigrationFixture struct {
	s                       Store
	agent, path, recordPath string
	record                  nativeHookRecord
	probe                   agentProbe
}

func newNativeMigrationFixture(t *testing.T, agent string) nativeMigrationFixture {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, path := setupNativeTest(t, agent)
	if agent == "opencode" {
		if err := os.WriteFile(path, []byte("{\n// user settings\n\"plugins\":[\"unrelated\",],\n}"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err := changeNativeHooks(context.Background(), s, agent, "install", "", reconcileNativeTestCLI, false); err != nil {
		t.Fatal(err)
	}
	fixture := nativeMigrationFixture{s: s, agent: agent, path: path, recordPath: filepath.Join(s.Dir, nativeRecordName(agent))}
	if err := s.readJSON(nativeRecordName(agent), &fixture.record); err != nil {
		t.Fatal(err)
	}
	policy := agentCompatibilities[agent]
	fixture.probe = agentProbe{status: probeOK, actual: policy.prefix + policy.reference + policy.suffix}
	return fixture
}

func (f nativeMigrationFixture) run(check bool) reconcileComponent {
	return reconcileNative(context.Background(), f.s, f.agent, reconcileNativeTestCLI, f.probe, check)
}

func (f nativeMigrationFixture) saveRecord(t *testing.T) {
	t.Helper()
	if err := f.s.writeJSON(nativeRecordName(f.agent), f.record); err != nil {
		t.Fatal(err)
	}
}

func (f nativeMigrationFixture) snapshot(t *testing.T) map[string]os.FileInfo {
	t.Helper()
	paths := []string{f.recordPath}
	if f.agent == "opencode" {
		paths = append(paths, f.path)
	}
	for path := range f.record.Files {
		paths = append(paths, path)
	}
	result := map[string]os.FileInfo{}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = info
	}
	return result
}

func assertNativeFilesUntouched(t *testing.T, before map[string]os.FileInfo) {
	t.Helper()
	for path, old := range before {
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(current, old) || !current.ModTime().Equal(old.ModTime()) {
			t.Fatalf("modified %s: %v", path, err)
		}
	}
}

func TestReconcileNativeOwnedOldAssetsAndIdempotence(t *testing.T) {
	for _, agent := range []string{"opencode", "pi", "claude"} {
		t.Run(agent, func(t *testing.T) {
			f := newNativeMigrationFixture(t, agent)
			f.record.Revision--
			f.record.HostToken = ""
			for path := range f.record.Files {
				old := []byte("old owned adapter\n")
				if err := os.WriteFile(path, old, 0600); err != nil {
					t.Fatal(err)
				}
				f.record.Files[path] = nativeOwnedFile{SHA: nativeDigest(old)}
			}
			f.saveRecord(t)
			old := f.snapshot(t)
			if result := f.run(true); result.Status != "available" {
				t.Fatal(result)
			}
			assertNativeFilesUntouched(t, old)
			if result := f.run(false); result.Status != "updated" || result.Activation != "restart_required" {
				t.Fatal(result)
			}
			if f.agent == "opencode" {
				assertNativeFilesUntouched(t, map[string]os.FileInfo{f.path: old[f.path]})
			}
			if err := f.s.readJSON(nativeRecordName(agent), &f.record); err != nil || !opaqueID.MatchString(f.record.HostToken) || f.record.Pending != "" {
				t.Fatal("incomplete native migration", err)
			}
			current := f.snapshot(t)
			if result := f.run(false); result.Status != "current" {
				t.Fatal(result)
			}
			assertNativeFilesUntouched(t, current)
		})
	}
}

func TestReconcileNativePreservesDeletedAndEditedConfiguration(t *testing.T) {
	for _, test := range []struct{ agent, change, reason string }{
		{"pi", "delete asset", "disabled"}, {"claude", "delete asset", "disabled"},
		{"pi", "edit asset", "ownership_conflict"}, {"opencode", "delete reference", "disabled"},
		{"opencode", "replace reference", "ownership_conflict"}, {"opencode", "change root", "ownership_conflict"},
	} {
		t.Run(test.agent+"/"+test.change, func(t *testing.T) {
			f := newNativeMigrationFixture(t, test.agent)
			switch test.change {
			case "delete asset", "edit asset":
				for path := range f.record.Files {
					var err error
					if test.change == "delete asset" {
						err = os.Remove(path)
					} else {
						err = os.WriteFile(path, []byte("user change"), 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
					break
				}
			case "change root":
				t.Setenv("OPENCODE_CONFIG_DIR", t.TempDir())
			default:
				data := []byte(`{"plugins":["third-party"]}`)
				if test.change == "replace reference" {
					data = []byte(`{"plugins":[{"package":"` + f.record.Reference + `","options":{}}]}`)
				}
				if err := os.WriteFile(f.path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			info, err := os.Stat(f.recordPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []bool{true, false} {
				if result := f.run(check); result.Status != "attention" || result.Reason != test.reason {
					t.Fatal(result)
				}
			}
			assertNativeFilesUntouched(t, map[string]os.FileInfo{f.recordPath: info})
		})
	}
}

func TestReconcileNativePendingProof(t *testing.T) {
	for _, scenario := range []string{"interrupted migration", "legacy compatible", "legacy incompatible", "different target version", "uninstall pending", "missing prior asset"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeMigrationFixture(t, "pi")
			f.record.Pending = "install"
			f.record.ReferenceRequired = true
			file := f.record.Files[f.path]
			before := []byte("known previous owned adapter")
			file.PreviousSHA = nativeDigest(before)
			if err := os.WriteFile(f.path, before, 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "interrupted migration":
				f.record.ReconcileVersion = version
			case "legacy compatible":
				f.record.Revision--
			case "legacy incompatible":
				file.SHA = nativeDigest([]byte("old CLI target"))
			case "different target version":
				f.record.ReconcileVersion = version + "-other"
			case "uninstall pending":
				f.record.Pending = "uninstall"
			case "missing prior asset":
				if err := os.Remove(f.path); err != nil {
					t.Fatal(err)
				}
			}
			f.record.Files[f.path] = file
			f.saveRecord(t)
			recordBefore, err := os.ReadFile(f.recordPath)
			if err != nil {
				t.Fatal(err)
			}
			canFinish := scenario == "interrupted migration" || scenario == "legacy compatible"
			if check := f.run(true); (check.Status == "available") != canFinish {
				t.Fatal(check)
			}
			recordAfter, err := os.ReadFile(f.recordPath)
			if err != nil || !bytes.Equal(recordBefore, recordAfter) {
				t.Fatal("read-only check changed pending record")
			}
			result := f.run(false)
			if canFinish {
				if result.Status != "updated" || f.run(false).Status != "current" {
					t.Fatal(result)
				}
			} else {
				if result.Reason != "pending_conflict" {
					t.Fatal(result)
				}
				recordAfter, err = os.ReadFile(f.recordPath)
				if err != nil || !bytes.Equal(recordBefore, recordAfter) {
					t.Fatal("incompatible journal overwritten")
				}
			}
		})
	}
}

func TestReconcileNativeRefusesWritableParentsAndUnsafeLocks(t *testing.T) {
	for _, tc := range []struct{ agent, problem string }{
		{"pi", "config parent"}, {"opencode", "config parent"}, {"pi", "config ancestor"},
		{"opencode", "asset parent"}, {"pi", "config lock"}, {"pi", "store lock"},
	} {
		t.Run(tc.agent+"/"+tc.problem, func(t *testing.T) {
			f := newNativeMigrationFixture(t, tc.agent)
			f.record.Revision--
			f.saveRecord(t)
			var path string
			switch tc.problem {
			case "config parent":
				path = filepath.Dir(f.path)
			case "config ancestor":
				path = filepath.Dir(filepath.Dir(f.path))
			case "asset parent":
				path = f.record.Reference
			case "config lock":
				path = f.path + ".mobterm-lock"
			case "store lock":
				path = filepath.Join(f.s.Dir, ".lock")
			}
			old, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(path, old.Mode().Perm()|0020); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(path, old.Mode().Perm())
			before := f.snapshot(t)
			for _, check := range []bool{true, false} {
				if got := f.run(check); got.Reason != "ownership_conflict" {
					t.Fatal(got)
				}
			}
			assertNativeFilesUntouched(t, before)
		})
	}
}

func TestReconcileNativeRechecksAfterStoreLock(t *testing.T) {
	f := newNativeMigrationFixture(t, "pi")
	f.record.Revision--
	f.saveRecord(t)
	if result := f.run(true); result.Status != "available" {
		t.Fatal(result)
	}
	unlock, err := f.s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan reconcileComponent, 1)
	go func() { result <- f.run(false) }()
	if err = os.Remove(f.path); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if got := <-result; got.Reason != "disabled" {
		t.Fatal(got)
	}
	if _, err = os.Lstat(f.path); !os.IsNotExist(err) {
		t.Fatal("deleted extension recreated", err)
	}
}

func TestReconcileNativeFinishesPendingFirstInstall(t *testing.T) {
	f := newNativeMigrationFixture(t, "opencode")
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := editNativePlugin(data, f.record.Reference, "uninstall", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.path, config, 0600); err != nil {
		t.Fatal(err)
	}
	for path := range f.record.Files {
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	f.record.Pending = "install"
	f.record.ReferenceRequired = false
	f.saveRecord(t)
	if got := f.run(true); got.Status != "available" {
		t.Fatal(got)
	}
	for path := range f.record.Files {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only resumed %s", path)
		}
	}
	if got := f.run(false); got.Status != "updated" {
		t.Fatal(got)
	}
	if !nativeHooksInstalled(f.s, f.agent) || f.run(false).Status != "current" {
		t.Fatal("pending assets/reference not finalized")
	}
}

func TestNativeInstallDoesNotReplaceReconcileTransaction(t *testing.T) {
	for _, scenario := range []string{"different version", "removed reference", "compatible"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeMigrationFixture(t, "opencode")
			f.record.Pending = "install"
			f.record.ReferenceRequired = true
			f.record.ReconcileVersion = version
			if scenario == "different version" {
				f.record.ReconcileVersion += "-previous"
			}
			if scenario == "removed reference" {
				if err := os.WriteFile(f.path, []byte(`{"plugins":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.saveRecord(t)
			before := f.snapshot(t)
			err := changeNativeHooks(context.Background(), f.s, f.agent, "install", "", reconcileNativeTestCLI, false)
			if scenario == "compatible" {
				if err != nil || f.run(true).Status != "current" {
					t.Fatal("compatible intent not completed", err)
				}
			} else {
				if err == nil {
					t.Fatal("install replaced an incompatible migration")
				}
				assertNativeFilesUntouched(t, before)
			}
		})
	}
}
