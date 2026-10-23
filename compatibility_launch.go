package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type agentLaunch struct {
	executable string
	args       []string
	path       string
}

// Both read-only launcher queries and the one Agent execution share the caller's
// deadline and output budget. Never expose their output through error messages.
func probeCommand(c *exec.Cmd, budget *int) ([]byte, error) {
	c.WaitDelay = 100 * time.Millisecond
	out := &limitBuffer{limit: *budget}
	c.Stdout, c.Stderr = out, io.Discard
	err := c.Run()
	*budget -= out.Len()
	if err != nil {
		return nil, errors.New("agent probe command failed")
	}
	return out.Bytes(), nil
}

func resolveAgentLaunch(ctx context.Context, agent string, budget *int) (agentLaunch, error) {
	searchPath, err := absoluteProbePath(os.Getenv("PATH"))
	if err != nil {
		return agentLaunch{}, err
	}
	entry, err := probeLookPath(agent, searchPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errAgentNotFound
		}
		return agentLaunch{}, err
	}
	entry, searchPath, err = resolveAsdfEntry(ctx, entry, searchPath, budget)
	if err != nil {
		return agentLaunch{}, err
	}
	launch := agentLaunch{executable: entry, path: searchPath}
	// Official npm entry points use this shebang, including through npm symlinks.
	// Pin the interpreter too: env node must not re-enter a shim after HOME/cwd
	// change. For an Agent shim, use asdf's execution PATH, not a competing Node.
	header, err := probeHeader(entry)
	if err != nil {
		return agentLaunch{}, err
	}
	if strings.SplitN(header, "\n", 2)[0] == "#!/usr/bin/env node" {
		node, err := probeLookPath("node", searchPath)
		if err != nil {
			return agentLaunch{}, err
		}
		node, searchPath, err = resolveAsdfEntry(ctx, node, searchPath, budget)
		if err != nil {
			return agentLaunch{}, err
		}
		launch = agentLaunch{executable: node, args: []string{entry}, path: searchPath}
	}
	return launch, nil
}

// Recognize the generated asdf shim by BOTH its configured location and complete
// script shape. Do not execute/eval a wrapper or infer asdf from its basename.
func isAsdfShim(entry string) bool {
	expected := filepath.Join(probeAsdfDataDir(), "shims", filepath.Base(entry))
	actualInfo, err := os.Stat(entry)
	if err != nil {
		return false
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil || !os.SameFile(actualInfo, expectedInfo) {
		return false
	}
	header, err := probeHeader(entry)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSuffix(header, "\n"), "\n")
	if len(lines) < 3 || lines[0] != "#!/usr/bin/env bash" || lines[len(lines)-1] != `exec asdf exec "`+filepath.Base(entry)+`" "$@"` {
		return false
	}
	for _, line := range lines[1 : len(lines)-1] {
		if !strings.HasPrefix(line, "# asdf-plugin: ") || len(strings.Fields(line)) != 4 {
			return false
		}
	}
	return true
}

func resolveAsdfEntry(ctx context.Context, entry, searchPath string, budget *int) (string, string, error) {
	if !isAsdfShim(entry) {
		return entry, searchPath, nil
	}
	// The standard Node plugin needs no selection callbacks. which/env can run
	// arbitrary plugin callbacks otherwise; do not run those in the real HOME.
	header, err := probeHeader(entry)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSuffix(header, "\n"), "\n")
	for _, line := range lines[1 : len(lines)-1] {
		if strings.Fields(line)[2] != "nodejs" {
			return "", "", errors.New("unsupported asdf plugin")
		}
	}
	plugin := filepath.Join(probeAsdfDataDir(), "plugins", "nodejs")
	for _, path := range []string{"bin/list-bin-paths", "bin/exec-path", "bin/exec-env", "shims/" + filepath.Base(entry)} {
		if _, err := os.Lstat(filepath.Join(plugin, path)); !os.IsNotExist(err) {
			return "", "", errors.New("unsupported asdf selection callback")
		}
	}
	asdf, err := probeLookPath("asdf", searchPath)
	if err != nil {
		return "", "", err
	}
	query := func(args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, asdf, args...)
		c.Env = append(c.Environ(), "PATH="+searchPath)
		return probeCommand(c, budget)
	}
	out, err := query("which", filepath.Base(entry))
	if err != nil {
		return "", "", err
	}
	// which prints exactly one absolute executable path. Spaces and quotes inside
	// it are data, never shell syntax. A recursive/unresolved shim is a failure.
	resolved := strings.TrimSuffix(string(out), "\n")
	if !filepath.IsAbs(resolved) || strings.ContainsAny(resolved, "\r\n\x00") || isAsdfShim(resolved) {
		return "", "", errors.New("invalid asdf executable")
	}
	if _, err := exec.LookPath(resolved); err != nil {
		return "", "", err
	}
	// asdf env supplies the same bin-path selection as exec without invoking the
	// Agent or its pre-exec hook. Capture only PATH, not the user's environment.
	out, err = query("env", filepath.Base(entry), "/usr/bin/printenv", "PATH")
	if err != nil {
		return "", "", err
	}
	path, err := absoluteProbePath(strings.TrimSuffix(string(out), "\n"))
	if err != nil {
		return "", "", err
	}
	return resolved, path, nil
}

func probeAsdfDataDir() string {
	if data := os.Getenv("ASDF_DATA_DIR"); data != "" {
		return data
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".asdf")
}

func probeHeader(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	return string(b), err
}

// Freeze relative PATH entries before the child moves to a disposable cwd.
func absoluteProbePath(path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "\r\n\x00") {
		return "", errors.New("invalid probe PATH")
	}
	parts := filepath.SplitList(path)
	for i, part := range parts {
		absolute, err := filepath.Abs(part)
		if err != nil {
			return "", err
		}
		parts[i] = absolute
	}
	return strings.Join(parts, string(os.PathListSeparator)), nil
}

func probeLookPath(name, path string) (string, error) {
	var entryError error
	for _, dir := range filepath.SplitList(path) {
		entry := filepath.Join(dir, name)
		if found, err := exec.LookPath(entry); err == nil {
			return found, nil
		} else if _, statErr := os.Lstat(entry); !errors.Is(statErr, os.ErrNotExist) {
			// A present but non-executable entry (or unreadable parent) is not absence.
			entryError = err
		}
	}
	if entryError != nil {
		return "", entryError
	}
	return "", exec.ErrNotFound
}
