package main

import (
	"context"
	"os"
	"path/filepath"
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
