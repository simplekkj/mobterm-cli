package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func agentOutput(agent, token string) string {
	p := agentCompatibilities[agent]
	return p.prefix + token + p.suffix
}

func compatibilitySamples(t *testing.T) map[string]map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/compatibility-samples.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples map[string]map[string]string
	if err := json.Unmarshal(b, &samples); err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestCompatibilitySamplesMatchPolicy(t *testing.T) {
	samples := compatibilitySamples(t)
	doc, err := os.ReadFile("docs/compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	for agent, policy := range agentCompatibilities {
		if samples["minimum"][agent] != policy.minimum.String() || samples["current"][agent] != policy.reference {
			t.Fatal("update policy, testdata/compatibility-samples.json and compatibility documentation together")
		}
		for _, group := range []string{"minimum", "historical", "current"} {
			v := samples[group][agent]
			if policy.evaluate(agentOutput(agent, v)) != "" || !strings.Contains(string(doc), v) {
				t.Fatalf("missing/unsupported documented %s/%s sample", group, agent)
			}
		}
	}
}

func TestStableAgentVersions(t *testing.T) {
	for agent, p := range agentCompatibilities {
		t.Run(agent, func(t *testing.T) {
			below := map[string]string{"codex": "0.99.0", "claude": "2.0.35"}[agent]
			middle := map[string]string{"codex": "0.120.0", "claude": "2.1.10"}[agent]
			for _, token := range []string{p.minimum.String(), middle, p.reference, "99.10.10", "v" + p.minimum.String(), p.minimum.String() + "+build.7"} {
				if got := p.evaluate(" \t" + agentOutput(agent, token) + "\r\n"); got != "" {
					t.Fatalf("stable %q refused: %s", token, got)
				}
			}
			if p.evaluate(agentOutput(agent, below)) != "below_minimum" {
				t.Fatal("lower boundary accepted")
			}
			for _, suffix := range []string{"-alpha.1", "-beta", "-rc.2", "-nightly", "-dev+build.3"} {
				if p.evaluate(agentOutput(agent, "99.0.0"+suffix)) != "prerelease" {
					t.Fatal("prerelease accepted")
				}
			}
			for _, token := range []string{"", "2", "2.1", "2.1.1.1", "-2.1.1", "01.2.3", "2.01.3", "2.1.03", "2.1.1+", "2.1.1+build..1", "2.1.1+build_1", "18446744073709551616.0.0", "99.18446744073709551616.0", "99.0.18446744073709551616", "99.1.1 garbage", "99.1.1\n99.1.2"} {
				if p.evaluate(agentOutput(agent, token)) == "" {
					t.Fatalf("malformed token accepted: %q", token)
				}
			}
			for _, output := range []string{"banner\n" + agentOutput(agent, p.reference), agentOutput(agent, p.reference) + "\nbanner", p.reference, "codex-cli 99.0.0\n2.1.301 (Claude Code)", strings.Repeat("x", 101)} {
				if p.evaluate(output) == "" {
					t.Fatalf("ambiguous output accepted: %q", output)
				}
			}
		})
	}
	if !(stableVersion{2, 1, 9}).less(stableVersion{2, 1, 10}) || (stableVersion{2, 1, 10}).less(stableVersion{2, 1, 9}) {
		t.Fatal("version comparison is not numeric")
	}
}

func versionEnvironment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	for _, agent := range []string{"codex", "claude"} {
		script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf 'probe\\n' >> \"$FIXTURE_PROBES\"\nprintf '%s\\n' \"$FIXTURE_VERSION\"\nexit \"$FIXTURE_EXIT\"\n"
		if err := os.WriteFile(filepath.Join(dir, agent), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FIXTURE_PROBES", filepath.Join(t.TempDir(), "probes"))
	t.Setenv("FIXTURE_EXIT", "0")
	if mode := os.Getenv("FIXTURE_LAUNCHER"); mode != "" {
		versionLauncherFixture(t, dir, mode)
	}
	return dir
}

// A selection-sensitive launcher, rather than a shim that just echoes a version.
// The real asdf/Node/npm path is exercised separately by tests/asdf_probe_test.py.
func versionLauncherFixture(t *testing.T, dir, mode string) {
	t.Helper()
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	home := os.Getenv("HOME")
	data := filepath.Join(home, ".asdf")
	if strings.Contains(mode, "explicit") {
		data = filepath.Join(t.TempDir(), "data with ' spaces")
		t.Setenv("ASDF_DATA_DIR", data)
	} else {
		t.Setenv("ASDF_DATA_DIR", "")
	}
	write(filepath.Join(home, ".tool-versions"), "nodejs home\n")
	selected := "home"
	project := t.TempDir()
	t.Chdir(project)
	if strings.Contains(mode, "project") {
		write(filepath.Join(project, ".tool-versions"), "nodejs project\n")
		selected = "project"
	}
	t.Setenv("FIXTURE_SELECTED", filepath.Join(data, "installs", "nodejs", selected, "bin"))
	t.Setenv("FIXTURE_REAL_HOME", home)
	t.Setenv("FIXTURE_HOMES", filepath.Join(t.TempDir(), "homes"))
	// The asdf fixture implements only read-only which/env; it must run before
	// isolation and resolve the same home/project version that exec would use.
	write(filepath.Join(dir, "asdf"), `#!/bin/sh
[ "$HOME" = "$FIXTURE_REAL_HOME" ] || exit 9
[ "$FIXTURE_RESOLVE_FAIL" != 1 ] || exit 8
data=${ASDF_DATA_DIR:-$HOME/.asdf}
file=$HOME/.tool-versions
[ ! -f "$PWD/.tool-versions" ] || file=$PWD/.tool-versions
read tool version < "$file"
bin=$data/installs/$tool/$version/bin
case "$1" in
which) [ -x "$bin/$2" ] || exit 8; printf '%s\n' "$bin/$2" ;;
env) printf '%s\n' "$bin:$PATH" ;;
exec) shift; command=$1; shift; PATH=$bin:$PATH exec "$bin/$command" "$@" ;;
*) exit 7 ;;
esac
`)
	for _, version := range []string{"home", "project"} {
		bin := filepath.Join(data, "installs", "nodejs", version, "bin")
		write(filepath.Join(bin, "node"), `#!/bin/sh
[ "${0%/*}" = "$FIXTURE_SELECTED" ] || exit 6
if [ "$FIXTURE_PREFLIGHT" = 1 ]; then exec /bin/sh "$@"; fi
[ "$HOME" != "$FIXTURE_REAL_HOME" ] && [ "$PWD" = "$HOME" ] || exit 5
printf 'migration' > "$HOME/.claude.json"
printf '%s\n' "$HOME" >> "$FIXTURE_HOMES"
exec /bin/sh "$@"
`)
		for _, agent := range []string{"codex", "claude"} {
			original, err := os.ReadFile(filepath.Join(dir, agent))
			if err != nil {
				t.Fatal(err)
			}
			script := strings.Replace(string(original), "#!/bin/sh", "#!/usr/bin/env node", 1)
			write(filepath.Join(bin, agent), script)
			if strings.HasPrefix(mode, "node") {
				write(filepath.Join(dir, agent), script)
			}
		}
	}
	for _, name := range []string{"node", "codex", "claude"} {
		if strings.HasPrefix(mode, "node") && name != "node" {
			continue
		}
		write(filepath.Join(data, "shims", name), "#!/usr/bin/env bash\n# asdf-plugin: nodejs home\n# asdf-plugin: nodejs project\nexec asdf exec \""+name+"\" \"$@\"")
	}
	// A competing installation must never be substituted for the selected one.
	global := t.TempDir()
	for _, name := range []string{"node", "codex", "claude"} {
		write(filepath.Join(global, name), "#!/bin/sh\nexit 4\n")
	}
	t.Setenv("PATH", filepath.Join(data, "shims")+string(os.PathListSeparator)+dir+string(os.PathListSeparator)+global+":/usr/bin:/bin")
	for _, agent := range []string{"codex", "claude"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c := exec.CommandContext(ctx, agent, "--version")
		c.WaitDelay = 100 * time.Millisecond
		c.Env = append(os.Environ(), "FIXTURE_PREFLIGHT=1", "FIXTURE_VERSION="+agentOutput(agent, agentCompatibilities[agent].reference))
		out, err := c.Output()
		cancel()
		if err != nil || strings.TrimSpace(string(out)) != agentOutput(agent, agentCompatibilities[agent].reference) {
			t.Fatal("original launcher context is not usable", err)
		}
	}
	if err := os.Remove(os.Getenv("FIXTURE_PROBES")); err != nil {
		t.Fatal(err)
	}
	sentinels := map[string][]byte{}
	for _, path := range []string{filepath.Join(home, ".tool-versions"), filepath.Join(project, ".tool-versions"), filepath.Join(home, ".claude.json")} {
		if filepath.Base(path) == ".claude.json" {
			write(path, "user-owned")
		}
		if b, err := os.ReadFile(path); err == nil {
			sentinels[path] = b
		}
	}
	t.Cleanup(func() {
		for path, before := range sentinels {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Error("probe changed selection or user configuration")
			}
		}
	})
}

func TestAsdfCompatibilityEntrypoints(t *testing.T) {
	for _, mode := range []string{"agent-home", "agent-project-explicit", "node-home-explicit", "node-project"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FIXTURE_LAUNCHER", mode)
			t.Run("entrypoints", TestCompatibilityEntrypoints)
			t.Run("notify", TestRangeNotifyEncryptedIdentity)
			t.Run("chain", TestRejectedCodexVersionPreservesNotifierChain)
			t.Run("installation-only", TestInstallationIdentityDoesNotProbeAgents)
			t.Run("one-probe", TestCompatibilityUsesOneProbePerAgentOperation)
		})
	}
}

func TestAsdfProbeIsolationAndFailure(t *testing.T) {
	t.Setenv("FIXTURE_LAUNCHER", "agent-project-explicit")
	versionEnvironment(t)
	t.Setenv("FIXTURE_VERSION", "2.0.36 (Claude Code)")
	s := testStore(t)
	if _, err := s.installation(); err != nil {
		t.Fatal(err)
	}
	parentEnv := os.Environ()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := status(context.Background(), s, newAPI(), "", false); err != nil {
		t.Fatal(err)
	}
	afterCwd, err := os.Getwd()
	if err != nil || afterCwd != cwd || !slices.Equal(parentEnv, os.Environ()) {
		t.Fatal("concurrent probes changed parent selection context")
	}
	homes, err := os.ReadFile(os.Getenv("FIXTURE_HOMES"))
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, home := range strings.Fields(string(homes)) {
		if used[home] {
			t.Fatal("concurrent probes shared a disposable home")
		}
		used[home] = true
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("probe home not cleaned")
		}
	}
	if len(used) != 2 {
		t.Fatal("both concurrent probes did not execute")
	}
	if err := changeHooks(context.Background(), s, "claude", "install", "", "/fixture/mobterm-cli", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bin/list-bin-paths", "bin/exec-path", "bin/exec-env", "shims/claude"} {
		callback := filepath.Join(probeAsdfDataDir(), "plugins", "nodejs", path)
		if err := os.MkdirAll(filepath.Dir(callback), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(callback, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if p := probeAgent(context.Background(), "claude"); p.installed || p.reason != "probe_failed" {
			t.Fatal("custom selection callback accepted", path)
		}
		if err := os.Remove(callback); err != nil {
			t.Fatal(err)
		}
	}
	config, _ := configPath("claude")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_RESOLVE_FAIL", "1")
	if p := probeAgent(context.Background(), "claude"); p.installed || p.reason != "probe_failed" {
		t.Fatal("failed launcher accepted")
	}
	if err := changeHooks(context.Background(), s, "claude", "install", "", "/fixture/mobterm-cli", true); err == nil {
		t.Fatal("failed launcher installed")
	}
	after, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed launcher changed hook config")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p := probeAgent(ctx, "claude"); p.installed || p.reason != "probe_failed" {
		t.Fatal("cancelled launcher accepted")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if p := probeAgent(ctx, "claude"); p.installed || p.reason != "probe_timeout" {
		t.Fatal("expired launcher accepted")
	}
	if err := changeHooks(context.Background(), s, "claude", "uninstall", "", "/fixture/mobterm-cli", true); err != nil {
		t.Fatal("failed launcher blocked uninstall", err)
	}
}

func TestAgentProbeFailures(t *testing.T) {
	dir := versionEnvironment(t)
	t.Setenv("FIXTURE_VERSION", "codex-cli 0.163.0")
	t.Setenv("FIXTURE_EXIT", "7")
	if probe := probeAgent(context.Background(), "codex"); probe.installed || probe.reason != "probe_failed" || probe.actual != "" {
		t.Fatal("nonzero version command accepted")
	}
	if err := os.Remove(filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	if probeAgent(context.Background(), "codex").reason != "probe_failed" {
		t.Fatal("missing executable accepted")
	}
	// An already-expired context exercises cancellation without a real sleep.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if probeAgent(ctx, "claude").reason != "probe_timeout" {
		t.Fatal("timed out probe accepted")
	}
	t.Setenv("FIXTURE_EXIT", "0")
	t.Setenv("FIXTURE_VERSION", strings.Repeat("x", 101))
	if p := probeAgent(context.Background(), "claude"); p.reason != "unknown_output" || p.actual != "" {
		t.Fatal("diagnostic output not bounded")
	}
}

func TestVersionProbeIsolatesAgentConfiguration(t *testing.T) {
	dir := versionEnvironment(t)
	userConfig := filepath.Join(os.Getenv("HOME"), ".claude.json")
	if err := os.WriteFile(userConfig, []byte("user-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	// Model the real old Claude bootstrap writing metadata before --version.
	// Every config environment variable and cwd must refer to the disposable home.
	script := `#!/bin/sh
[ "$PWD" = "$HOME" ] || exit 1
for config in "$CODEX_HOME" "$CLAUDE_CONFIG_DIR" "$XDG_CONFIG_HOME"; do
  case "$config" in "$HOME"/*) ;; *) exit 1 ;; esac
done
[ "$DISABLE_AUTOUPDATER" = 1 ] && [ "$CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC" = 1 ] || exit 1
printf 'native migration' > "$HOME/.claude.json"
printf '%s' "$HOME" > "$FIXTURE_PROBES"
printf '2.0.36 (Claude Code)\n'
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if p := probeAgent(context.Background(), "claude"); p.reason != "" {
		t.Fatal("isolated probe failed", p.reason)
	}
	data, err := os.ReadFile(userConfig)
	if err != nil || string(data) != "user-owned" {
		t.Fatal("version probe changed user configuration")
	}
	probeHome, err := os.ReadFile(os.Getenv("FIXTURE_PROBES"))
	if err != nil || len(probeHome) == 0 {
		t.Fatal("probe fixture did not run")
	}
	if _, err := os.Stat(string(probeHome)); !os.IsNotExist(err) {
		t.Fatal("probe configuration was not removed")
	}
}

func TestCompatibilityEntrypoints(t *testing.T) {
	samples := compatibilitySamples(t)
	versionEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { printJSON(w, map[string]string{"status": "active"}) }))
	defer server.Close()
	cases := []string{"minimum", "middle", "historical", "current", "future", "below", "unknown", "prerelease", "nonzero", "missing"}
	if mode := os.Getenv("FIXTURE_LAUNCHER"); mode != "" && mode != "agent-project-explicit" {
		cases = []string{"minimum", "below"}
	}
	for _, agent := range []string{"codex", "claude"} {
		for _, sample := range cases {
			t.Run(agent+"/"+sample, func(t *testing.T) {
				version := samples[sample][agent]
				supported := true
				switch sample {
				case "middle":
					version = map[string]string{"codex": "0.120.0", "claude": "2.1.10"}[agent]
				case "future":
					version = "99.1.0" // Synthetic: proves only absence of an upper bound.
				case "below":
					version = map[string]string{"codex": "0.99.0", "claude": "2.0.35"}[agent]
					supported = false
				case "unknown", "prerelease", "nonzero", "missing":
					version, supported = "99.0.0-rc.1", false
				}
				output := agentOutput(agent, version)
				if sample == "unknown" {
					output = "PRIVATE_UNKNOWN_BANNER"
				}
				t.Setenv("FIXTURE_VERSION", output)
				if sample == "nonzero" {
					t.Setenv("FIXTURE_EXIT", "7")
				}
				if sample == "missing" {
					t.Setenv("PATH", t.TempDir())
				}
				s := testStore(t)
				t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
				id, err := s.installation()
				if err != nil {
					t.Fatal(err)
				}
				p := testPair(id, server.URL)
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				config, _ := configPath(agent)
				// Seed a genuinely owned config first; refusal must preserve both it and its record.
				if err := changeHooks(context.Background(), s, agent, "install", "", "/fixture/mobterm-cli", false); err != nil {
					t.Fatal(err)
				}
				paths := []string{config, filepath.Join(s.Dir, "hooks-"+agent+".json"), filepath.Join(s.Dir, "pairs.json")}
				before := make([][]byte, len(paths))
				for i, path := range paths {
					before[i], err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				var out, diag bytes.Buffer
				code := run([]string{"hooks", "install", "--agent", agent, "--json"}, strings.NewReader(""), &out, &diag)
				if (code == 0) != supported || (!supported && (!strings.Contains(diag.String(), "requires stable >=") || strings.Contains(diag.String(), "PRIVATE_"))) {
					t.Fatal("installation decision/diagnostic disagrees with policy")
				}
				out.Reset()
				diag.Reset()
				if run([]string{"status", "--json"}, strings.NewReader(""), &out, &diag) != 0 || diag.Len() != 0 || out.Len() > 16384 {
					t.Fatal("status contract changed")
				}
				var result struct {
					V            int    `json:"v"`
					Installation string `json:"installation_id"`
					Capabilities map[string]struct {
						Supported bool   `json:"supported"`
						Minimum   string `json:"minimum_version"`
						Rejection string `json:"version_rejection"`
					} `json:"capabilities"`
				}
				decoder := json.NewDecoder(&out)
				if err := decoder.Decode(&result); err != nil || result.V != 1 || result.Installation != id || result.Capabilities[agent].Supported != supported || result.Capabilities[agent].Minimum != agentCompatibilities[agent].minimum.String() {
					t.Fatal("status decision/identity disagrees with policy", err)
				}
				if decoder.Decode(new(any)) != io.EOF {
					t.Fatal("status emitted more than one JSON result")
				}
				if !supported && result.Capabilities[agent].Rejection == "" {
					t.Fatal("missing refusal diagnostic")
				}
				for i, path := range paths {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(before[i], after) {
						t.Fatal("install/status modified config, ownership or credentials")
					}
				}
				// Uninstall must remain usable with every rejected/missing executable.
				if err := changeHooks(context.Background(), s, agent, "uninstall", "", "/fixture/mobterm-cli", true); err != nil {
					t.Fatal("uninstall gated by version", err)
				}
			})
		}
	}
}

func TestInstallationIdentityDoesNotProbeAgents(t *testing.T) {
	versionEnvironment(t)
	t.Setenv("MOBTERM_CONFIG_DIR", testStore(t).Dir)
	var out, diag bytes.Buffer
	if run([]string{"status", "--installation-json"}, strings.NewReader(""), &out, &diag) != 0 {
		t.Fatal("installation identity failed")
	}
	if _, err := os.Stat(os.Getenv("FIXTURE_PROBES")); !os.IsNotExist(err) {
		t.Fatal("installation-only status probed Agents")
	}
}

func TestRejectedInstallDoesNotCreateConfiguration(t *testing.T) {
	versionEnvironment(t)
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("FIXTURE_VERSION", agentOutput(agent, "0.0.1"))
			s := testStore(t)
			path := filepath.Join(t.TempDir(), "absent", "config")
			if err := changeHooks(context.Background(), s, agent, "install", path, "/fixture/mobterm-cli", true); err == nil {
				t.Fatal("unsupported install succeeded")
			}
			for _, dir := range []string{filepath.Dir(path), s.Dir} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("refusal created config/ownership directory")
				}
			}
		})
	}
}

func TestRejectedCodexVersionPreservesNotifierChain(t *testing.T) {
	versionEnvironment(t)
	s := testStore(t)
	if _, err := s.installation(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	path := filepath.Join(t.TempDir(), "chain.sh")
	output := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FIXTURE_CHAIN_OUTPUT", output)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FIXTURE_CHAIN_OUTPUT\"\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("hooks-codex.json", HookRecord{Chain: []string{path, "literal $() argument"}}); err != nil {
		t.Fatal(err)
	}
	payload := `{"type":"agent-turn-complete","thread-id":"thread","turn-id":"turn","last-assistant-message":"PRIVATE_PAYLOAD"}`
	for _, version := range []string{"codex-cli 0.99.0", "unknown", "codex-cli 99.0.0-rc.1"} {
		t.Setenv("FIXTURE_VERSION", version)
		var out, diag bytes.Buffer
		code := run([]string{"notify", "--source", "codex", "--mobterm-chain", payload}, strings.NewReader(""), &out, &diag)
		argv, err := os.ReadFile(output)
		if err != nil || string(argv) != "literal $() argument\n"+payload+"\n" || code != 7 || out.Len() != 0 || strings.Contains(diag.String(), "PRIVATE_") {
			t.Fatal("refused version swallowed/changed original notifier or exposed payload", err)
		}
	}
}

func TestCompatibilityUsesOneProbePerAgentOperation(t *testing.T) {
	versionEnvironment(t)
	t.Setenv("FIXTURE_VERSION", "codex-cli 0.163.0")
	s := testStore(t)
	if err := changeHooks(context.Background(), s, "codex", "install", "", "/fixture/mobterm-cli", true); err != nil {
		t.Fatal(err)
	}
	probes, _ := os.ReadFile(os.Getenv("FIXTURE_PROBES"))
	if string(probes) != "probe\n" {
		t.Fatal("install repeated the probe for diagnostics")
	}
	if _, err := status(context.Background(), s, newAPI(), "", false); err != nil {
		t.Fatal(err)
	}
	probes, _ = os.ReadFile(os.Getenv("FIXTURE_PROBES"))
	if string(probes) != strings.Repeat("probe\n", 3) {
		t.Fatal("status did not use exactly one probe per Agent")
	}
}

func TestRangeNotifyEncryptedIdentity(t *testing.T) {
	samples := compatibilitySamples(t)
	dir := versionEnvironment(t)
	t.Setenv("TMUX", "/fixture/tmux.sock,123,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("HERDR_PANE_ID", "")
	tmux := "#!/bin/sh\nprintf '%s\\t%s\\t%s\\t%s\\t%s\\n' /fixture/tmux.sock 123:456:789 '$1' '@2' '%3'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(tmux), 0700); err != nil {
		t.Fatal(err)
	}
	cases := []string{"minimum", "middle", "historical", "current", "future", "below", "unknown", "prerelease", "nonzero", "missing", "invalid_event", "invalid_target", "ambiguous_target"}
	if mode := os.Getenv("FIXTURE_LAUNCHER"); mode != "" && mode != "agent-project-explicit" {
		cases = []string{"minimum", "below"}
	}
	for _, agent := range []string{"codex", "claude"} {
		for _, sample := range cases {
			t.Run(agent+"/"+sample, func(t *testing.T) {
				v := samples[sample][agent]
				allowed := true
				switch sample {
				case "middle":
					v = map[string]string{"codex": "0.120.0", "claude": "2.1.10"}[agent]
				case "future":
					v = "99.1.0"
				case "below":
					v, allowed = map[string]string{"codex": "0.99.0", "claude": "2.0.35"}[agent], false
				case "unknown", "prerelease", "nonzero", "missing":
					v, allowed = "99.0.0-rc.1", false
				case "invalid_event", "invalid_target", "ambiguous_target":
					v, allowed = samples["current"][agent], false
				}
				t.Setenv("FIXTURE_VERSION", agentOutput(agent, v))
				if sample == "unknown" {
					t.Setenv("FIXTURE_VERSION", "unknown")
				}
				if sample == "nonzero" {
					t.Setenv("FIXTURE_EXIT", "1")
				}
				if sample == "missing" {
					t.Setenv("PATH", t.TempDir())
				}
				if sample == "invalid_target" {
					t.Setenv("TMUX_PANE", "%99")
				}
				if sample == "ambiguous_target" {
					t.Setenv("HERDR_PANE_ID", "w:p")
				}
				s := testStore(t)
				t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
				id, err := s.installation()
				if err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var bodies [][]byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if !allowed || r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/events") {
						t.Error("unexpected API request")
					}
					mu.Lock()
					bodies = append(bodies, body)
					attempt := len(bodies)
					mu.Unlock()
					if attempt%2 == 1 {
						w.WriteHeader(503)
						return
					}
					printJSON(w, map[string]string{"state": "apns_accepted"})
				}))
				defer server.Close()
				p := testPair(id, server.URL)
				if err := s.savePair(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				for invocation := 0; invocation < 3; invocation++ {
					turn := "opaque-before-restart"
					if invocation == 2 {
						turn = "opaque-after-resume" // Model of the upstream UUID change; not a live resume.
					}
					payload := fmt.Sprintf(`{"type":"agent-turn-complete","thread-id":"same-thread","turn-id":%q,"last-assistant-message":"PRIVATE_PAYLOAD"}`, turn)
					args := []string{"notify", "--source", agent, payload}
					input := ""
					if agent == "claude" {
						// Create the actual completion association through the production entrypoint.
						if invocation != 1 {
							var out, diag bytes.Buffer
							code := run([]string{"notify", "--source", agent}, strings.NewReader(`{"session_id":"session","hook_event_name":"UserPromptSubmit"}`), &out, &diag)
							if code != 0 || out.Len() != 0 || (allowed && diag.Len() != 0) {
								t.Fatal("prompt association failed")
							}
						}
						args = args[:3]
						input = `{"session_id":"session","hook_event_name":"Notification","notification_type":"idle_prompt","message":"PRIVATE_PAYLOAD"}`
					}
					if sample == "invalid_event" {
						if agent == "codex" {
							args[3] = `{"type":"agent-turn-complete","thread-id":"t"}`
						} else {
							input = `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`
						}
					}
					var out, diag bytes.Buffer
					code := run(args, strings.NewReader(input), &out, &diag)
					if strings.Contains(diag.String(), "PRIVATE_") {
						t.Fatal("hook leaked fixture payload")
					}
					if code != 0 || out.Len() != 0 || (allowed && diag.Len() != 0) {
						t.Fatalf("hook failed: exit=%d stdout_bytes=%d diagnostic=%q", code, out.Len(), diag.String())
					}
				}
				mu.Lock()
				defer mu.Unlock()
				if !allowed {
					if len(bodies) != 0 {
						t.Fatal("rejected event sent")
					}
					return
				}
				if len(bodies) != 6 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[0], bodies[2]) || !bytes.Equal(bodies[2], bodies[3]) || !bytes.Equal(bodies[4], bodies[5]) {
					t.Fatal("event reentry/HTTP retry did not preserve exact ciphertext")
				}
				key, _ := decode64(p.EncryptionKey, 32)
				var first Envelope
				for _, index := range []int{0, 4} {
					var envelope Envelope
					if err := json.Unmarshal(bodies[index], &envelope); err != nil {
						t.Fatal(err)
					}
					l, err := openEnvelope(key, envelope, time.Now())
					want := Locator{V: 1, InstallationID: id, Backend: "tmux", Namespace: "/fixture/tmux.sock", ServerIdentity: "123:456:789", WorkspaceID: "$1", ViewID: "@2", PaneID: "%3"}
					if err != nil || l != want || envelope.Kind != "turn_completed" {
						t.Fatal("encrypted event target mismatch", err)
					}
					if index == 0 {
						first = envelope
					} else if envelope.EventID == first.EventID || envelope.Nonce == first.Nonce || envelope.Ciphertext == first.Ciphertext {
						t.Fatal("different turns collapsed across resume boundary")
					}
					for _, private := range []string{"PRIVATE_", "same-thread", "/fixture/tmux.sock", p.SendToken, p.EncryptionKey} {
						if bytes.Contains(bodies[index], []byte(private)) {
							t.Fatal("plaintext secret in request")
						}
					}
				}
			})
		}
	}
}
