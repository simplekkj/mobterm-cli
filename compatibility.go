package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	"codex":  {stableVersion{0, 100, 0}, "0.163.0", "codex-cli ", ""},
	"claude": {stableVersion{2, 0, 36}, "2.1.301", "", " (Claude Code)"},
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

type agentProbe struct {
	actual    string
	installed bool
	reason    string
}

func probeAgent(ctx context.Context, agent string) agentProbe {
	p, ok := agentCompatibilities[agent]
	if !ok {
		return agentProbe{reason: "unknown_agent"}
	}
	// Older official JavaScript CLIs need more than one second to start. Keep
	// the same bounded budget in install, notify and status. Status probes both
	// Agents concurrently to leave time for pair readback in its ten-second context.
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := agentVersionOutput(query, agent)
	if err != nil {
		if errors.Is(query.Err(), context.DeadlineExceeded) {
			return agentProbe{reason: "probe_timeout"}
		}
		return agentProbe{reason: "probe_failed"}
	}
	actual := strings.TrimSpace(string(output))
	result := agentProbe{installed: true, reason: p.evaluate(actual)}
	if len(actual) <= 100 {
		result.actual = actual
	}
	return result
}

// Even --version in older Claude releases migrates user configuration. Resolve
// the selected executable on the ordinary PATH, but let it load/write only a
// disposable home/configuration. Terminal queries keep commandOutput's semantics.
func agentVersionOutput(ctx context.Context, agent string) ([]byte, error) {
	c := exec.CommandContext(ctx, agent, "--version")
	dir, err := os.MkdirTemp("", "mobterm-agent-version-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	c.Dir = dir
	c.Env = append(c.Environ(), "HOME="+dir, "CODEX_HOME="+filepath.Join(dir, "codex"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(dir, "claude"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"DISABLE_UPDATES=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	c.WaitDelay = 100 * time.Millisecond
	out := &limitBuffer{limit: 2 * 1024 * 1024}
	c.Stdout, c.Stderr = out, io.Discard
	if err := c.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func hookVersions(ctx context.Context, agent string) error {
	probe := probeAgent(ctx, agent)
	if probe.reason != "" {
		p, ok := agentCompatibilities[agent]
		if !ok {
			return fmt.Errorf("agent must be codex or claude")
		}
		return fmt.Errorf("%s requires stable >=%s: %s; hook configuration unchanged", agent, p.minimum, probe.reason)
	}
	return nil
}
