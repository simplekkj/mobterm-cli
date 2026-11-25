package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type forbiddenHelpInput struct{}

func (forbiddenHelpInput) Read([]byte) (int, error) { panic("help tried to read stdin") }

func TestRunHelpWithoutConfiguration(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("MOBTERM_CONFIG_DIR", "")
	for _, args := range [][]string{nil, {"-h"}, {"help", "upgrade"}, {"upgrade", "--help"}, {"hooks", "install", "-h", "--plain"}, {"hooks", "install", "--agent", "codex", "--help"}, {"upgrade", "--version", "v1.0.0", "-h"}, {"job", "submit", "--help"}} {
		var out, diagnostic bytes.Buffer
		if code := run(args, forbiddenHelpInput{}, &out, &diagnostic); code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), "Usage") {
			t.Fatalf("%v returned %d: %s / %s", args, code, &out, &diagnostic)
		}
	}
}

func TestDisplayOptionOwnership(t *testing.T) {
	for _, tc := range []struct {
		args, clean []string
		human       bool
	}{
		{[]string{"status", "--pair", "--plain"}, []string{"status", "--pair", "--plain"}, true},
		{[]string{"upgrade", "--version=--plain", "--progress=never"}, []string{"upgrade", "--version=--plain"}, true},
		{[]string{"status", "--plain", "-installation-json", "--read-only"}, []string{"status", "-installation-json", "--read-only"}, false},
		{[]string{"hooks", "install", "--plain", "--agent=codex", "-json=true"}, []string{"hooks", "install", "--agent=codex", "-json=true"}, false},
		{[]string{"test", "--", "--plain"}, []string{"test", "--", "--plain"}, true},
		{[]string{"notify", "--source", "codex", "--plain"}, []string{"notify", "--source", "codex", "--plain"}, false},
	} {
		got, _, human := resultOptions(tc.args)
		if !slices.Equal(got, tc.clean) || human != tc.human {
			t.Errorf("%q => %q human=%t", tc.args, got, human)
		}
	}
}

func TestPipedMachineOutputWithTerminalEnvironment(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("MOBTERM_CONFIG_DIR", filepath.Join(t.TempDir(), "cli"))
	for _, args := range [][]string{{"capabilities", "--json"}, {"status", "--installation-json", "--color=always"}, {"upgrade", "--version", "invalid", "--plain"}, {"upgrade", "--json", "--version", "invalid", "--progress=plain"}, {"target", "verify", "--stdin-frame"}} {
		var out, diag bytes.Buffer
		code := run(args, strings.NewReader(""), &out, &diag)
		if !json.Valid(out.Bytes()) || strings.ContainsAny(out.String()+diag.String(), "\x1b\r") {
			t.Fatalf("%v produced %q / %q", args, &out, &diag)
		}
		if args[0] == "capabilities" && (code != 0 || diag.Len() != 0) {
			t.Fatalf("combined capability probe: %d %s", code, &diag)
		}
		if args[0] == "upgrade" && (code != 1 || !strings.Contains(out.String(), `"upgraded":false`)) {
			t.Fatalf("unexpected upgrade result %d %s", code, &out)
		}
	}
}

func TestColorPrecedence(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("CLICOLOR", "0")
	t.Setenv("NO_COLOR", "0")
	t.Setenv("COLORTERM", "truecolor")
	var out bytes.Buffer
	if detectTerminal(&out, displayOptions{}).ColorDepth != 0 {
		t.Fatal("NO_COLOR=0 lost precedence")
	}
	if got := detectTerminal(&out, displayOptions{color: "always"}); got.ColorDepth != 24 || got.Dynamic || got.TTY {
		t.Fatal(got)
	}
	t.Setenv("NO_COLOR", "")
	for _, tc := range []struct {
		opts    displayOptions
		colored bool
	}{
		{displayOptions{}, true}, {displayOptions{color: "never"}, false}, {displayOptions{plain: true, color: "always"}, false},
	} {
		if got := detectTerminal(&out, tc.opts); (got.ColorDepth > 0) != tc.colored {
			t.Fatal(tc, got)
		}
	}
}
