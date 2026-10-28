package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type stableVersion [3]uint64

func (v stableVersion) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }
func (v stableVersion) less(other stableVersion) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

type agentCompatibility struct {
	minimum   stableVersion
	reference string // Reproducible validation sample, never an upper bound.
	prefix    string
	suffix    string
}

var agentCompatibilities = map[string]agentCompatibility{
	"codex":    {stableVersion{0, 100, 0}, "0.163.0", "codex-cli ", ""},
	"claude":   {stableVersion{2, 0, 36}, "2.1.301", "", " (Claude Code)"},
	"opencode": {stableVersion{2, 0, 0}, "2.1.0", "opencode v", ""},
	"pi":       {stableVersion{1, 1, 0}, "1.3.0", "", ""},
}

// Match a whole official version token. An optional v and SemVer build metadata
// are accepted; prereleases are parsed only to give a precise refusal reason.
var agentVersionToken = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func (p agentCompatibility) evaluate(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 100 || !strings.HasPrefix(output, p.prefix) || !strings.HasSuffix(output, p.suffix) {
		return "unknown_output"
	}
	token := strings.TrimSuffix(strings.TrimPrefix(output, p.prefix), p.suffix)
	if (p.prefix == "opencode v" || (p.prefix == "" && p.suffix == "")) && strings.HasPrefix(token, "v") {
		return "unknown_output"
	}
	m := agentVersionToken.FindStringSubmatch(token)
	if m == nil {
		return "unknown_output"
	}
	var actual stableVersion
	for i := range actual {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return "version_overflow"
		}
		actual[i] = n
	}
	if m[4] != "" {
		return "prerelease"
	}
	if actual.less(p.minimum) {
		return "below_minimum"
	}
	return ""
}

type probeStatus string

const (
	probeOK        probeStatus = "ok"
	probeNotFound  probeStatus = "not_found"
	probeFailed    probeStatus = "failed"
	probeTimeout   probeStatus = "timeout"
	probeCancelled probeStatus = "cancelled"
)

// Only the initial Agent entry lookup may produce this typed absence result.
var errAgentNotFound = errors.New("agent entry not found")

type agentProbe struct {
	status    probeStatus
	actual    string
	installed bool
	reason    string
}

func probeAgent(ctx context.Context, agent string) agentProbe {
	p, ok := agentCompatibilities[agent]
	if !ok {
		return agentProbe{status: probeFailed, reason: "unknown_agent"}
	}
	// Older official JavaScript CLIs need more than one second to start. Keep
	// the same bounded budget in install, notify and status. Status probes four
	// Agents concurrently within the remaining command context.
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := agentVersionOutput(query, agent)
	if err != nil {
		if errors.Is(query.Err(), context.DeadlineExceeded) {
			return agentProbe{status: probeTimeout, reason: "probe_timeout"}
		}
		if errors.Is(query.Err(), context.Canceled) {
			return agentProbe{status: probeCancelled, reason: "probe_failed"}
		}
		if errors.Is(err, errAgentNotFound) {
			return agentProbe{status: probeNotFound, reason: "probe_failed"}
		}
		return agentProbe{status: probeFailed, reason: "probe_failed"}
	}
	actual := strings.TrimSpace(string(output))
	result := agentProbe{status: probeOK, installed: true, reason: p.evaluate(actual)}
	if len(actual) <= 100 {
		result.actual = actual
	}
	return result
}

// Resolve launchers in the caller's selection context before isolating the Agent.
// Even --version in older Claude releases migrates user configuration.
// Terminal queries keep commandOutput's semantics.
func agentVersionOutput(ctx context.Context, agent string) ([]byte, error) {
	// Pi resolves its package override against the caller's HOME/cwd. Pin that
	// selection before replacing either, without changing the parent environment.
	piPackage := os.Getenv("PI_PACKAGE_DIR")
	if agent == "pi" && piPackage != "" {
		var err error
		piPackage, err = absolutePiPackageDir(piPackage)
		if err != nil {
			return nil, err
		}
	}
	budget := 2 * 1024 * 1024
	launch, err := resolveAgentLaunch(ctx, agent, &budget)
	if err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, launch.executable, append(launch.args, "--version")...)
	dir, err := os.MkdirTemp("", "mobterm-agent-version-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	c.Dir = dir
	env := c.Environ()
	if agent == "opencode" || agent == "pi" {
		filtered := make([]string, 0, len(env))
		for _, entry := range env {
			key, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(key, "OPENCODE_") || strings.HasPrefix(key, "OTEL_") || key == "PI_MANAGED_INSTALL_ROOT" {
				continue
			}
			if agent == "pi" && key == "PI_PACKAGE_DIR" && piPackage != "" {
				entry = "PI_PACKAGE_DIR=" + piPackage
			}
			filtered = append(filtered, entry)
		}
		env = append(filtered, "NO_COLOR=1", "OPENCODE_CONFIG_DIR="+filepath.Join(dir, "opencode"), "OPENCODE_DISABLE_AUTOUPDATE=1",
			"XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"), "XDG_STATE_HOME="+filepath.Join(dir, "state"), "TMPDIR="+dir,
			"PI_CODING_AGENT_DIR="+filepath.Join(dir, "pi"), "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
	}
	c.Env = append(env, "PATH="+launch.path, "HOME="+dir, "CODEX_HOME="+filepath.Join(dir, "codex"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(dir, "claude"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"DISABLE_UPDATES=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	return probeCommand(c, &budget)
}

// Mirrors Pi 1.1.0/1.3.0 normalizePath's POSIX defaults: no whitespace trim,
// only ~ and ~/ expand, and only file:// invokes fileURLToPath. Relative paths
// then become absolute in the original cwd. Do not stat or resolve symlinks:
// Pi itself must read the selected package.json and report its actual VERSION.
func absolutePiPackageDir(input string) (string, error) {
	invalid := errors.New("invalid Pi package selection")
	if input == "~" || strings.HasPrefix(input, "~/") {
		home, present := os.LookupEnv("HOME")
		if !present {
			// Node os.homedir falls back to the OS account when HOME is unset.
			account, err := user.Current()
			if err != nil || account.HomeDir == "" {
				return "", invalid
			}
			home = account.HomeDir
		}
		if input == "~" {
			input = home
		} else {
			input = filepath.Join(home, input[2:])
		}
	} else if strings.HasPrefix(input, "file://") {
		// WHATWG file URLs remove ASCII tabs/newlines and trailing C0 spaces,
		// and treat backslashes in the URL path as separators on POSIX too.
		raw := strings.TrimRightFunc(input, func(r rune) bool { return r <= 0x20 })
		raw = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(raw)
		pathEnd := strings.IndexAny(raw, "?#")
		if pathEnd < 0 {
			pathEnd = len(raw)
		}
		raw = strings.ReplaceAll(raw[:pathEnd], "\\", "/") + raw[pathEnd:]
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) || strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || !utf8.ValidString(u.Path) {
			return "", invalid
		}
		input = u.Path
		if input == "" {
			input = "/"
		}
	}
	if strings.ContainsRune(input, 0) {
		return "", invalid
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", invalid
	}
	return absolute, nil
}

func hookVersions(ctx context.Context, agent string) error {
	probe := probeAgent(ctx, agent)
	if probe.reason != "" {
		p, ok := agentCompatibilities[agent]
		if !ok {
			return fmt.Errorf("agent must be codex, claude, opencode or pi")
		}
		return fmt.Errorf("%s requires stable >=%s: %s; hook configuration unchanged", agent, p.minimum, probe.reason)
	}
	return nil
}
