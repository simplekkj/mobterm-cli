package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPiPersonalExtensionOverrides(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	asset := filepath.Join(base, "extensions", "mobterm-jumpto.ts")
	const rel = "extensions/mobterm-jumpto.ts"
	for _, tc := range []struct {
		name      string
		overrides []string
		want      error
	}{
		{"unrelated exact", []string{"-extensions/other.ts"}, nil},
		{"exact", []string{"-" + rel}, errAdapterDisabled},
		{"explicit relative", []string{"-./" + rel}, errAdapterDisabled},
		{"absolute", []string{"-" + asset}, errAdapterDisabled},
		{"tilde", []string{"-~/" + rel}, errAdapterDisabled},
		{"bare exact compares relative path", []string{"-mobterm-jumpto.ts"}, nil},
		{"basename exclusion", []string{"!mobterm*.ts"}, errAdapterDisabled},
		{"relative question glob", []string{"!extensions/mobterm-jump?o.ts"}, errAdapterDisabled},
		{"absolute wildcard", []string{"!" + base + "/extensions/*.ts"}, errAdapterDisabled},
		{"globstar", []string{"!**/mobterm-*.ts"}, errAdapterDisabled},
		{"descendant-only glob", []string{"!**/mobterm-jumpto.ts/**"}, nil},
		{"unrelated glob", []string{"!extensions/other-*.ts"}, nil},
		{"literal include cannot override", []string{"!*.ts", rel}, errAdapterDisabled},
		{"force include", []string{"+" + rel, "!*.ts"}, nil},
		{"force exclude wins", []string{"-" + rel, "!*.ts", "+" + rel}, errAdapterDisabled},
		{"force include trailing slash", []string{"!*.ts", "+~/" + rel + "/"}, errAdapterDisabled},
		{"uncertain glob", []string{"!@(private|mobterm)-*.ts"}, errAdapterSettings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"extensions": tc.overrides, "theme": "unchanged"})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(base, "settings.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if got := checkNativeSettings(context.Background(), "pi", asset); !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReconcileNativeDisabledSettingsPreserveIntent(t *testing.T) {
	for _, tc := range []struct{ agent, settings, reason string }{
		{"pi", `{"extensions":["-extensions/mobterm-jumpto.ts"]}`, "disabled"},
		{"pi", `{"extensions":["!**/*.ts"]}`, "disabled"},
		{"pi", `{"extensions":["!{private,mobterm}*.ts"]}`, "ownership_conflict"},
		{"pi", `[]`, "ownership_conflict"},
		{"claude", `null`, "ownership_conflict"},
		{"claude", `{"disableAllHooks":true}`, "disabled"},
		{"claude", `{"allowManagedHooksOnly":true}`, "disabled"},
	} {
		t.Run(tc.agent+"/"+tc.settings, func(t *testing.T) {
			f := newNativeMigrationFixture(t, tc.agent)
			path := f.path
			if f.agent == "pi" {
				path = filepath.Join(filepath.Dir(filepath.Dir(f.path)), "settings.json")
			}
			f.record.Revision--
			f.saveRecord(t)
			if plan := f.run(true); plan.Status != "available" {
				t.Fatal(plan)
			}
			prepared, planned := planNativeReconcile(context.Background(), f.s, f.agent, f.path, reconcileNativeTestCLI)
			if planned.Status != "available" {
				t.Fatal(planned)
			}
			if err := os.WriteFile(path, []byte(tc.settings), 0600); err != nil {
				t.Fatal(err)
			}
			before := f.snapshot(t)
			before[path], _ = os.Stat(path)
			if err := prepared.apply(context.Background(), f.s); err == nil {
				t.Fatal("stale plan applied despite disabled settings")
			}
			for _, check := range []bool{true, false} {
				if got := f.run(check); got.Status != "attention" || got.Reason != tc.reason {
					t.Fatal(got)
				}
			}
			assertNativeFilesUntouched(t, before)
			f.record.Pending = "install"
			f.record.ReconcileVersion = version
			f.saveRecord(t)
			before = f.snapshot(t)
			before[path], _ = os.Stat(path)
			for _, check := range []bool{true, false} {
				if got := f.run(check); got.Reason != tc.reason {
					t.Fatal(got)
				}
			}
			if err := changeNativeHooks(context.Background(), f.s, f.agent, "install", "", reconcileNativeTestCLI, false); err == nil {
				t.Fatal("explicit install resumed disabled migration")
			}
			assertNativeFilesUntouched(t, before)
		})
	}
}

func TestReconcileNativeEnabledSettings(t *testing.T) {
	for _, agent := range []string{"pi", "claude"} {
		t.Run(agent, func(t *testing.T) {
			f := newNativeMigrationFixture(t, agent)
			path, settings := f.path, `{"disableAllHooks":false,"theme":"dark"}`
			if agent == "pi" {
				path = filepath.Join(filepath.Dir(filepath.Dir(f.path)), "settings.json")
				settings = `{"theme":"dark","extensions":["!extensions/other-*"]}`
			}
			if err := os.WriteFile(path, []byte(settings), 0600); err != nil {
				t.Fatal(err)
			}
			f.record.Revision--
			f.saveRecord(t)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.run(false); got.Status != "updated" {
				t.Fatal(got)
			}
			assertNativeFilesUntouched(t, map[string]os.FileInfo{path: info})
		})
	}
}
