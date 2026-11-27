package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mobterm/mobterm-cli/internal/doctor"
)

func TestDoctorOfflineNoInitializationAndProtocol(t *testing.T) {
	s, _ := discoveryEnvironment(t)
	var out, diag bytes.Buffer
	args := []string{"doctor", "--scope", "agent", "--offline", "--deep", "--json"}
	if code := run(args, nil, &out, &diag); code != 0 {
		t.Fatal(code, out.String(), diag.String())
	}
	var r doctor.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || len(r.Checks) != 9 || !r.Complete || !bodyHashPattern.MatchString(r.Target) || len(r.Actions) != 0 {
		t.Fatal(err, r)
	}
	if _, err := os.Lstat(s.Dir); !os.IsNotExist(err) {
		t.Fatal("initialized store", err)
	}
	for _, agent := range reconcileAgents {
		if _, err := os.Lstat(filepath.Join(os.Getenv("HOME"), agent.name+".probes")); !os.IsNotExist(err) {
			t.Fatal("offline ran Agent", err)
		}
	}
	out.Reset()
	if code := run([]string{"doctor", "--scope", "agent", "--offline", "--report"}, nil, &out, &diag); code != 0 || strings.Contains(out.String(), r.Target) || strings.Contains(out.String(), s.Dir) || strings.Contains(out.String(), "codex") {
		t.Fatal(code, out.String(), diag.String())
	}
	out.Reset()
	t.Setenv("MOBTERM_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if code := run([]string{"doctor", "--protocol"}, nil, &out, &diag); code != 0 || !strings.Contains(out.String(), `"doctor_protocols":[1]`) {
		t.Fatal(code, out.String(), diag.String())
	}
}

func TestDoctorShareIncludesOnlyKnownCauses(t *testing.T) {
	report := doctor.Report{Checks: []doctor.Finding{
		{Status: "fail", Reason: "tls_failed", Scope: "private-host"},
		{Status: "warn", Reason: "server_controlled_code", Details: map[string]string{"cli_path": "/private/path"}},
	}}
	data, err := json.Marshal(doctorShare(report))
	if err != nil || !bytes.Contains(data, []byte(`"tls_failed":1`)) ||
		bytes.Contains(data, []byte("private")) || bytes.Contains(data, []byte("server_controlled")) {
		t.Fatal(string(data), err)
	}
}

func TestDoctorFrameAndNoImplicitConfirmation(t *testing.T) {
	discoveryEnvironment(t)
	for _, args := range [][]string{{"doctor", "--fix"}, {"doctor", "--fix", "--stdin-frame"}, {"doctor", "--fix", "--yes"}, {"doctor", "--json", "--scope", "bogus"}} {
		var out, diag bytes.Buffer
		if code := run(args, strings.NewReader("yes\n"), &out, &diag); code != 1 || out.Len() != 0 {
			t.Fatal(args, code, out.String())
		}
	}
}

func doctorFrameForTest(t *testing.T, req doctorRepairRequest) *bytes.Buffer {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	_ = binary.Write(&input, binary.BigEndian, uint32(len(raw)))
	input.Write(raw)
	return &input
}

func TestDoctorScopedRepairAndStalePlan(t *testing.T) {
	s, _ := discoveryEnvironment(t)
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
	exe, _ := os.Executable()
	old, record, err := editCodex([]byte("model='keep'\n"), "install", path, exe, HookRecord{})
	if err != nil || s.writeJSON("hooks-codex.json", record) != nil {
		t.Fatal(err)
	}
	writeLayoutFile(t, path, old, 0600)
	var out, diag bytes.Buffer
	get := func() doctorRepairRequest {
		t.Helper()
		out.Reset()
		diag.Reset()
		if code := run([]string{"doctor", "--scope", "agent", "--agent", "codex", "--json"}, nil, &out, &diag); code != 2 {
			t.Fatalf("exit %d: %s %s", code, &out, &diag)
		}
		var report doctor.Report
		if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Actions) != 1 {
			t.Fatalf("no plan: %s", &out)
		}
		a := report.Actions[0]
		return doctorRepairRequest{1, a.ID, a.Scope, report.Target, a.Plan}
	}
	req := get()
	writeLayoutFile(t, path, append(old, []byte("# user edit\n")...), 0600)
	apply := func(req doctorRepairRequest, wantStatus string, wantCode int) {
		t.Helper()
		out.Reset()
		diag.Reset()
		code := run([]string{"doctor", "--fix", "--stdin-frame"}, doctorFrameForTest(t, req), &out, &diag)
		var receipt doctorRepairReceipt
		if code != wantCode || json.Unmarshal(out.Bytes(), &receipt) != nil || receipt.Status != wantStatus {
			t.Fatalf("%d: %s %s", code, &out, &diag)
		}
	}
	apply(req, "stale", 2)
	req = get()
	apply(req, "manual_required", 0)
	apply(req, "stale", 2)
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(b, []byte("# user edit")) {
		t.Fatal("lost user change")
	}
	current := (doctorComponent{"codex", "hooks"}).inspect(context.Background(), s, exe, classicProbe("codex"), false)
	if current.Status != "pass" || len(current.Actions) != 0 || current.Reason != "native_review_required" {
		t.Fatal(current)
	}
}

func TestDoctorNativePlanDetectsFileReplacement(t *testing.T) {
	f := newNativeMigrationFixture(t, "pi")
	f.record.Revision--
	f.saveRecord(t)
	c := doctorComponent{"pi", "adapter"}
	// The fixture's executable is virtual; direct planNative exercises the
	// actual ownership checks, while the fingerprint binds the running CLI.
	plan, row := planNativeReconcile(t.Context(), f.s, "pi", f.path, reconcileNativeTestCLI)
	if row.Status != "available" {
		t.Fatal(row)
	}
	exe, _ := os.Executable()
	files := c.nativeFiles(f.s, f.path, plan)
	before := c.fingerprint(f.s, exe, f.probe, c.nativeEvidence(plan), files, 1)
	if before == "" {
		t.Fatal("no fingerprint")
	}
	f.saveRecord(t)
	after := c.fingerprint(f.s, exe, f.probe, c.nativeEvidence(plan), files, 1)
	if after == before {
		t.Fatal("plan survived replacement of its ownership record")
	}
}

func TestDoctorNativeRepairRespectsNewDisabledSettings(t *testing.T) {
	s, path := setupNativeTest(t, "pi")
	exe, _ := os.Executable()
	if err := changeNativeHooks(t.Context(), s, "pi", "install", "", exe, false); err != nil {
		t.Fatal(err)
	}
	var record nativeHookRecord
	if err := s.readJSON(nativeRecordName("pi"), &record); err != nil {
		t.Fatal(err)
	}
	record.Revision--
	if err := s.writeJSON(nativeRecordName("pi"), record); err != nil {
		t.Fatal(err)
	}
	c := doctorComponent{"pi", "adapter"}
	p := classicProbe("pi")
	r := c.inspect(t.Context(), s, exe, p, false)
	if len(r.Actions) != 1 {
		t.Fatal(r)
	}
	target, _ := doctorTarget(s, exe)
	req := doctorRepairRequest{1, doctorActionID, c.scope(), target, r.Actions[0].Plan}
	settings := filepath.Join(filepath.Dir(filepath.Dir(path)), "settings.json")
	data, _ := json.Marshal(map[string]any{"extensions": []string{"-" + path}})
	if err := os.WriteFile(settings, data, 0600); err != nil {
		t.Fatal(err)
	}
	got := applyDoctorPlan(t.Context(), s, exe, c, p, req)
	if got.Status != "stale" {
		t.Fatal(got)
	}
	if err := s.readJSON(nativeRecordName("pi"), &record); err != nil || record.Revision == nativeAdapterRevision {
		t.Fatal("modified disabled adapter", err)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	r = c.inspect(t.Context(), s, exe, p, false)
	if len(r.Actions) != 1 {
		t.Fatal(r)
	}
	req.Plan = r.Actions[0].Plan
	got = applyDoctorPlan(t.Context(), s, exe, c, p, req)
	if got.Status != "manual_required" {
		t.Fatal(got)
	}
	if current := c.inspect(t.Context(), s, exe, p, false); current.Status != "pass" || len(current.Actions) > 0 {
		t.Fatal(current)
	}
}

func TestDoctorFixtureAndDeclinedRepair(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/doctor-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var r doctor.Report
	if err := interactionJSON(raw, &r); err != nil {
		t.Fatal(err)
	}
	a := r.Actions[0]
	if !(doctorRepairRequest{1, a.ID, a.Scope, r.Target, a.Plan}).valid() {
		t.Fatal("invalid shared wire fixture")
	}
	var out bytes.Buffer
	if code := selectDoctorRepair(t.Context(), Store{}, "", strings.NewReader("1\nno\n"), humanResult{&out, terminalCaps{Width: 80}}, r); code != 130 || !strings.Contains(out.String(), a.Scope) {
		t.Fatal(code, out.String())
	}
}

func TestDoctorInstallationGeneration(t *testing.T) {
	for _, change := range []string{"same_id", "new_inode_aba", "original_inode_aba", "while_waiting_for_lock", "no_identity", "replace_store"} {
		t.Run(change, func(t *testing.T) {
			s, path, exe := codexLayoutEnvironment(t)
			old, record, err := editCodex([]byte("model='keep'\n"), "install", path, exe, HookRecord{})
			if err != nil || s.writeJSON("hooks-codex.json", record) != nil {
				t.Fatal(err)
			}
			writeLayoutFile(t, path, old, 0600)
			identity := storedInstallation{V: 1, ID: randomID()}
			writeIdentity := func(id storedInstallation) {
				t.Helper()
				if err := s.writeJSON("installation.json", id); err != nil {
					t.Fatal(err)
				}
			}
			if change != "no_identity" && change != "replace_store" {
				writeIdentity(identity)
			}
			c := doctorComponent{"codex", "hooks"}
			probe := classicProbe("codex")
			plan := func() doctorRepairRequest {
				t.Helper()
				target, safe := doctorTarget(s, exe)
				r := c.inspect(t.Context(), s, exe, probe, false)
				if !safe || len(r.Actions) != 1 {
					t.Fatalf("valid repair unavailable %+v", r)
				}
				return doctorRepairRequest{V: 1, ID: doctorActionID, Scope: c.scope(), Target: target, Plan: r.Actions[0].Plan}
			}
			req := plan()
			idPath := filepath.Join(s.Dir, "installation.json")
			switch change {
			case "same_id":
				writeIdentity(identity)
			case "new_inode_aba":
				writeIdentity(storedInstallation{V: 1, ID: randomID()})
				writeIdentity(identity)
			case "original_inode_aba":
				if err := os.Rename(idPath, idPath+".previous"); err != nil {
					t.Fatal(err)
				}
				writeIdentity(storedInstallation{V: 1, ID: randomID()})
				if err := os.Rename(idPath+".previous", idPath); err != nil {
					t.Fatal(err)
				}
			case "while_waiting_for_lock":
				done := make(chan doctorRepairReceipt, 1)
				func() {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					unlock, err := lockFile(ctx, path+".mobterm-lock")
					if err != nil {
						t.Fatal(err)
					}
					defer unlock()
					go func() { done <- applyDoctorPlan(t.Context(), s, exe, c, probe, req) }()
					// The old plan passed preflight and now owns the store
					// lock, but cannot yet enter the config write section.
					for doctorLock(s).Reason != "store_busy" {
						select {
						case <-ctx.Done():
							t.Fatal("repair never acquired store lock")
						case <-time.After(time.Millisecond):
						}
					}
					writeIdentity(identity)
				}()
				if receipt := <-done; receipt.Status != "stale" {
					t.Fatalf("missed change under lock: %+v", receipt)
				}
			case "replace_store":
				if err := os.Rename(s.Dir, s.Dir+".previous"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(s.Dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(s.Dir+".previous", "hooks-codex.json"), filepath.Join(s.Dir, "hooks-codex.json")); err != nil {
					t.Fatal(err)
				}
			}
			if change != "no_identity" {
				current := plan()
				if current.Target == req.Target {
					t.Fatal("replaced identity retained target")
				}
				got := applyDoctorPlan(t.Context(), s, exe, c, probe, req)
				if got.Status != "stale" {
					t.Fatalf("obsolete plan %+v", got)
				}
				now, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(now, old) {
					t.Fatal("stale plan changed hook")
				}
				req = current
			}
			got := applyDoctorPlan(t.Context(), s, exe, c, probe, req)
			if got.Status != "manual_required" {
				t.Fatal(got)
			}
			target, safe := doctorTarget(s, exe)
			if !safe || target != req.Target {
				t.Fatal("repair invalidated its target")
			}
			if current := c.inspect(t.Context(), s, exe, probe, false); current.Status != "pass" || len(current.Actions) != 0 {
				t.Fatal(current)
			}
		})
	}
}
