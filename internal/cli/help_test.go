package cli

import (
	"strings"
	"testing"
)

func TestHelpTopics(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want []string
	}{
		{nil, []string{"Common commands", "App protocol", "Agent callbacks (automatic)", "mobterm-cli <command> --help"}},
		{[]string{"hooks"}, []string{"Subcommands", "install", "uninstall", "mobterm-cli hooks install --help"}},
		{[]string{"hooks", "install"}, []string{"--agent NAME", "pi", "mobterm-cli hooks install --agent codex", "--plain"}},
		{[]string{"upgrade"}, []string{"--version TAG", "vX.Y.Z", "--json", "--progress=MODE", "https://github.com/simplekkj/mobterm-cli/releases"}},
		{[]string{"status"}, []string{"status --installation-json [--read-only]", "status --agents-json"}},
		{[]string{"pairs", "retire"}, []string{"--stdin-frame --json", "length-prefixed", "machine-readable"}},
		{[]string{"target", "resolve"}, []string{"--stdin-frame [--read-only]", "existing state"}},
	} {
		t.Run(strings.Join(tc.path, "/"), func(t *testing.T) {
			var text strings.Builder
			if !renderHelp(&text, terminalCaps{Width: 90}, tc.path) {
				t.Fatalf("missing help for %v", tc.path)
			}
			for _, expected := range tc.want {
				if !strings.Contains(text.String(), expected) {
					t.Errorf("missing %q in:\n%s", expected, text.String())
				}
			}
		})
	}
	for _, path := range [][]string{{"unknown"}, {"hooks", "invalid"}, {"hooks install"}, {"job", "status", "typo"}} {
		var text strings.Builder
		if renderHelp(&text, terminalCaps{Width: 80}, path) || text.Len() != 0 {
			t.Errorf("invalid help topic accepted: %v", path)
		}
	}
}

func TestHelpNarrowLayout(t *testing.T) {
	var text strings.Builder
	h := helpLayout{&text, terminalCaps{Width: 24}}
	h.rows("Commands", []helpRow{{"status", "Read status from 开发终端 with e\u0301."}})
	output := text.String()
	if !strings.Contains(output, "\n  status\n    Read") || !strings.Contains(output, "开发终端") || !strings.Contains(output, "e\u0301.") {
		t.Errorf("stacked description lost content:\n%s", output)
	}
	text.Reset()
	if !renderHelp(&text, h.caps, []string{"upgrade"}) {
		t.Fatal("missing upgrade help")
	}
	url := "https://github.com/simplekkj/mobterm-cli/releases"
	for _, line := range strings.Split(text.String(), "\n") {
		if cellWidth(line) > h.caps.Width && line != "  "+url {
			t.Errorf("help exceeds %d columns: %q", h.caps.Width, line)
		}
	}
	if !strings.Contains(text.String(), "\n  "+url+"\n") {
		t.Error("narrow help broke the release URL")
	}
	text.Reset()
	h.caps.Hyperlinks = true
	renderHelp(&text, h.caps, []string{"upgrade"})
	if !strings.Contains(text.String(), "\x1b]8;;"+url+"\x1b\\"+url+"\x1b]8;;") {
		t.Error("narrow help lost its hyperlink or visible address")
	}
}
