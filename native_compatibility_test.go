package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeProbeIsolation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())
	for key, value := range map[string]string{"OPENCODE_SSH_ASKPASS_PORT": "123", "OPENCODE_CONFIG_CONTENT": "PRIVATE", "OPENCODE_LOG_LEVEL": "debug", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://private.invalid", "OTEL_EXPORTER_OTLP_HEADERS": "PRIVATE", "PI_MANAGED_INSTALL_ROOT": "/private", "PI_PACKAGE_DIR": "/nix/store/selected-pi"} {
		t.Setenv(key, value)
	}
	for _, source := range []string{"opencode", "pi"} {
		script := `#!/bin/sh
[ "$PWD" = "$HOME" ] || exit 1
[ -z "$OPENCODE_SSH_ASKPASS_PORT$OPENCODE_CONFIG_CONTENT$OPENCODE_LOG_LEVEL$OTEL_EXPORTER_OTLP_ENDPOINT$OTEL_EXPORTER_OTLP_HEADERS$PI_MANAGED_INSTALL_ROOT" ] || exit 2
[ "$PI_PACKAGE_DIR" = /nix/store/selected-pi ] || exit 3
[ "$PI_OFFLINE" = 1 ] && [ "$PI_SKIP_VERSION_CHECK" = 1 ] && [ "$NO_COLOR" = 1 ] && [ "$OPENCODE_DISABLE_AUTOUPDATE" = 1 ] || exit 4
for path in "$PI_CODING_AGENT_DIR" "$OPENCODE_CONFIG_DIR" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME"; do
 case "$path" in "$HOME"/*) ;; *) exit 5 ;; esac
done
[ "$TMPDIR" = "$HOME" ] || exit 6
printf '%s\n' '` + agentOutput(source, agentCompatibilities[source].reference) + `'
`
		if err := os.WriteFile(filepath.Join(dir, source), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"opencode", "pi"} {
		if p := probeAgent(context.Background(), source); p.reason != "" {
			t.Fatalf("%s isolation failed: %s", source, p.reason)
		}
	}
	result, err := status(context.Background(), testStore(t), newAPI(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"opencode", "pi"} {
		capability := result["capabilities"].(map[string]any)[source].(map[string]any)
		if capability["supported"] != true || capability["managed_hooks"] != true || capability["minimum_version"] != agentCompatibilities[source].minimum.String() || capability["validation_reference"] != agentCompatibilities[source].reference {
			t.Fatal("native status capability mismatch")
		}
		if _, present := result["hooks"].(map[string]bool)[source]; !present {
			t.Fatal("native hooks boolean missing")
		}
	}
	if os.Getenv("OPENCODE_SSH_ASKPASS_PORT") != "123" || os.Getenv("PI_MANAGED_INSTALL_ROOT") != "/private" {
		t.Fatal("parent environment changed")
	}
}

func TestNativeOfficialVersionForms(t *testing.T) {
	for _, sample := range []struct {
		source, output string
		ok             bool
	}{
		{"opencode", "opencode v2.0.0", true}, {"opencode", "opencode v2.1.0+build.7", true}, {"opencode", "opencode 2.1.0", false}, {"opencode", "opencode vv2.1.0", false},
		{"pi", "1.1.0", true}, {"pi", "1.3.0+build.7", true}, {"pi", "v1.3.0", false}, {"pi", "pi 1.3.0", false}, {"pi", "1.3.0 (node)", false},
	} {
		if got := agentCompatibilities[sample.source].evaluate(sample.output); (got == "") != sample.ok {
			t.Fatalf("%s %q: %s", sample.source, sample.output, got)
		}
	}
}

// This subprocess reads VERSION from the selected package.json, including Pi's
// missing-file 0.0.0 behavior. It never echoes a predetermined version, so moving
// HOME/cwd before fixing PI_PACKAGE_DIR changes the observed result.
func TestPiPackageVersionProcess(t *testing.T) {
	if os.Getenv("MOBTERM_TEST_PI_PACKAGE_PROCESS") != "1" {
		return
	}
	home := os.Getenv("HOME")
	cwd, err := os.Getwd()
	if err != nil || cwd != home || home == os.Getenv("MOBTERM_TEST_ORIGINAL_HOME") || cwd == os.Getenv("MOBTERM_TEST_ORIGINAL_CWD") {
		t.Fatal("probe HOME/cwd were not isolated")
	}
	for _, key := range []string{"PI_CODING_AGENT_DIR", "OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if !strings.HasPrefix(os.Getenv(key), home+string(os.PathSeparator)) {
			t.Fatal("probe configuration was not isolated")
		}
	}
	if os.Getenv("TMPDIR") != home || os.Getenv("PI_OFFLINE") != "1" || os.Getenv("PI_SKIP_VERSION_CHECK") != "1" || os.Getenv("PI_MANAGED_INSTALL_ROOT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_HEADERS") != "" || os.Getenv("OPENCODE_CONFIG_CONTENT") != "" {
		t.Fatal("probe environment isolation changed")
	}
	selection := os.Getenv("PI_PACKAGE_DIR")
	if selection == "" {
		selection = os.Getenv("MOBTERM_TEST_PI_FALLBACK")
	}
	if !filepath.IsAbs(selection) || selection != os.Getenv("MOBTERM_TEST_PI_EXPECTED_PACKAGE") {
		t.Fatal("probe selected a different package")
	}
	var pkg struct{ Version string }
	data, err := os.ReadFile(filepath.Join(selection, "package.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal("package read failed")
	}
	if err == nil {
		if err := json.Unmarshal(data, &pkg); err != nil {
			t.Fatal("invalid package metadata")
		}
	}
	if pkg.Version == "" {
		pkg.Version = "0.0.0"
	}
	fmt.Println(pkg.Version)
	os.Exit(0)
}

func TestPiPackageSelectionStatusAndInstall(t *testing.T) {
	root := t.TempDir()
	home, cwd, bin := filepath.Join(root, "home"), filepath.Join(root, "cwd"), filepath.Join(root, "bin")
	for _, dir := range []string{home, cwd, bin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("PI_MANAGED_INSTALL_ROOT", filepath.Join(home, "managed"))
	t.Setenv("OPENCODE_CONFIG_CONTENT", "private configuration")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "private telemetry")
	t.Setenv("MOBTERM_TEST_PI_PACKAGE_PROCESS", "1")
	t.Setenv("MOBTERM_TEST_ORIGINAL_HOME", home)
	t.Setenv("MOBTERM_TEST_ORIGINAL_CWD", cwd)
	t.Setenv("MOBTERM_TEST_PI_FALLBACK", bin)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\nexec "+shellQuote(executable)+" -test.run=^TestPiPackageVersionProcess$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	writePackage := func(dir, version string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]string{"version": version})
		if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An accidental dropped override must not silently select this stable version.
	writePackage(bin, "99.0.0")
	selected := filepath.Join(cwd, "selected package")
	fileURL := (&url.URL{Scheme: "file", Path: selected}).String()
	for _, sample := range []struct {
		name, selection, dir, version, reason string
	}{
		{"absolute", selected, selected, "1.3.0", ""},
		{"relative", "./selected package", selected, "1.1.0", ""},
		{"parent relative", "../home/package", filepath.Join(home, "package"), "1.3.0", ""},
		{"tilde", "~/package", filepath.Join(home, "package"), "1.3.0", ""},
		{"home itself", "~", home, "1.3.0", ""},
		{"file URL", fileURL, selected, "1.3.0", ""},
		{"localhost file URL", strings.Replace(fileURL, "file:///", "file://LOCALHOST/", 1), selected, "1.3.0", ""},
		{"file URL dot segments", fileURL + "/%2e%2e/selected%20package", selected, "1.3.0", ""},
		{"file URL query and fragment", fileURL + "?ignored#ignored", selected, "1.3.0", ""},
		{"file URL encoded backslash", fileURL + "%5Cpackage", selected + "\\package", "1.3.0", ""},
		{"literal surrounding spaces", " selected package ", filepath.Join(cwd, " selected package "), "1.3.0", ""},
		{"literal unicode space", "selected\u00a0package", filepath.Join(cwd, "selected\u00a0package"), "1.3.0", ""},
		{"literal tilde user", "~someone/package", filepath.Join(cwd, "~someone/package"), "1.3.0", ""},
		{"literal file prefix", "file:package", filepath.Join(cwd, "file:package"), "1.3.0", ""},
		{"empty override", "", bin, "99.0.0", ""},
		{"unset override", "", bin, "99.0.0", ""},
		{"missing package", "missing private package", filepath.Join(cwd, "missing private package"), "", "below_minimum"},
		{"low version", "./selected package", selected, "1.0.99", "below_minimum"},
		{"prerelease", "~/package", filepath.Join(home, "package"), "1.3.0-rc.1", "prerelease"},
		{"invalid file host", "file://private.invalid/package", "", "", "probe_failed"},
		{"invalid file escape", fileURL + "%invalid", "", "", "probe_failed"},
		{"invalid file encoding", fileURL + "%FF", "", "", "probe_failed"},
		{"encoded separator", fileURL + "%2fpackage", "", "", "probe_failed"},
		{"invalid file credentials", "file://secret:private@localhost/package", "", "", "probe_failed"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if sample.version != "" {
				writePackage(sample.dir, sample.version)
			}
			t.Setenv("PI_PACKAGE_DIR", sample.selection)
			if sample.name == "unset override" {
				if err := os.Unsetenv("PI_PACKAGE_DIR"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("MOBTERM_TEST_PI_EXPECTED_PACKAGE", sample.dir)
			s, configPath := setupNativeTest(t, "pi")
			markers := map[string]string{
				filepath.Join(home, ".pi", "agent", "settings.json"):                      `{"userSetting":"preserve default config"}`,
				filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "settings.json"):          `{"userSetting":"preserve selected config"}`,
				filepath.Join(os.Getenv("PI_MANAGED_INSTALL_ROOT"), "install-state.json"): `{"selectedVersion":"preserve managed install"}`,
			}
			for path, content := range markers {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			checkMarkers := func() {
				t.Helper()
				for path, original := range markers {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != original {
						t.Fatal("probe changed user configuration or managed-install contents")
					}
				}
			}
			originalEnv := os.Environ()
			result, err := status(context.Background(), s, newAPI(), "", false)
			if err != nil {
				t.Fatal(err)
			}
			checkMarkers()
			capability := result["capabilities"].(map[string]any)["pi"].(map[string]any)
			if capability["supported"] != (sample.reason == "") {
				t.Fatalf("unexpected support: %#v", capability)
			}
			if sample.reason != "" && capability["version_rejection"] != sample.reason {
				t.Fatalf("unexpected refusal: %#v", capability)
			}
			if sample.version != "" && capability["actual_version"] != sample.version {
				t.Fatalf("did not read selected package VERSION: %#v", capability)
			}
			err = changeHooks(context.Background(), s, "pi", "install", "", executable, true)
			checkMarkers()
			if sample.reason == "" {
				if err != nil || !nativeHooksInstalled(s, "pi") {
					t.Fatalf("selected package did not install: %v", err)
				}
				if err := changeHooks(context.Background(), s, "pi", "uninstall", "", "", true); err != nil {
					t.Fatal(err)
				}
			} else {
				want := "pi requires stable >=1.1.0: " + sample.reason + "; hook configuration unchanged"
				if err == nil || err.Error() != want {
					t.Fatalf("unsafe/incorrect install refusal: %v", err)
				}
				if _, err := os.Stat(configPath); !os.IsNotExist(err) {
					t.Fatal("refused install changed configuration")
				}
				if _, err := os.Stat(filepath.Join(s.Dir, nativeRecordName("pi"))); !os.IsNotExist(err) {
					t.Fatal("refused install recorded ownership")
				}
			}
			if strings.Join(originalEnv, "\x00") != strings.Join(os.Environ(), "\x00") {
				t.Fatal("probe changed parent environment")
			}
		})
	}
}
