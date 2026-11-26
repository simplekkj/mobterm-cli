package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The classic wire has no host-version field. Probe the executable of the
// actual ancestor identified by Claude; PATH selection is installation-only.
var claudeHookVersion = runningClaudeVersion

func runningClaudeVersion(ctx context.Context) string {
	pid, err := strconv.Atoi(os.Getenv("CLAUDE_PID"))
	if err != nil || pid <= 1 || pid == os.Getpid() || !codexIsAncestor(pid) {
		return ""
	}
	birth, err := processIdentity(pid)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	defer cancel()
	image, err := claudeImage(ctx, pid)
	if err != nil {
		return ""
	}
	before, err := os.Stat(image)
	if err != nil || !before.Mode().IsRegular() {
		return ""
	}
	out, err := probeSelectedAgent(ctx, "claude", "", agentLaunch{executable: image, path: os.Getenv("PATH")}, 4096)
	after, statErr := os.Stat(image)
	now, birthErr := processIdentity(pid)
	if err != nil || statErr != nil || birthErr != nil || now != birth || !os.SameFile(before, after) {
		return ""
	}
	actual := strings.TrimSpace(string(out))
	policy := agentCompatibilities["claude"]
	if policy.evaluate(actual) != "" {
		return ""
	}
	return strings.TrimSuffix(actual, policy.suffix)
}

func claudeBinaryName(path string) bool {
	base := filepath.Base(path)
	return base == "claude" || base == "claude.exe" ||
		filepath.Base(filepath.Dir(path)) == "versions" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "claude" && nativeInteractionVersion("claude", base)
}
