package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
)

// Most official distributions call the executable codex. npm can also launch
// its unrenamed target-triple executable.
func codexExecutable(name string) bool {
	name = filepath.Base(name)
	switch name {
	case "codex", "codex-x86_64-apple-darwin", "codex-aarch64-apple-darwin", "codex-x86_64-unknown-linux-musl", "codex-aarch64-unknown-linux-musl":
		return true
	}
	return false
}

// Classify the dispatching process, including an app-server manually launched
// in the foreground of the original pane. Follow a native shell only up to its
// proven pane process; never skip a recognized server to reach another TUI.
func codexTerminalHost(ctx context.Context, hostPID, panePID int) bool {
	for depth := 0; depth < 8 && hostPID > 1; depth++ {
		birth, err := processIdentity(hostPID)
		if err != nil {
			return false
		}
		known, terminal := codexProcessRole(ctx, hostPID)
		if known {
			if !terminal || !codexIsAncestor(hostPID) {
				return false
			}
			current, err := processIdentity(hostPID)
			return err == nil && current == birth
		}
		if hostPID == panePID {
			break
		}
		parent, err := codexProcessParent(hostPID)
		if err != nil || parent == hostPID {
			break
		}
		hostPID = parent
	}
	return false
}

func codexTerminalArgs(reader io.Reader, structured bool) bool {
	const budget = 64 * 1024
	limited := &io.LimitedReader{R: reader, N: budget}
	s := bufio.NewScanner(limited)
	if structured {
		s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := bytes.IndexByte(data, 0); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) != 0 {
				return 0, nil, io.ErrUnexpectedEOF
			}
			return 0, nil, nil
		})
	} else {
		s.Split(bufio.ScanWords)
	}
	// Linux supplies NUL-separated argv including argv[0]. On Darwin the
	// separately queried executable prefix has already been consumed.
	if structured && (!s.Scan() || !codexExecutable(s.Text())) {
		return false
	}
	// Darwin's args-only ps field has no argument separators. After an
	// option with a separate value, spaces in that value are ambiguous.
	// Refuse any possible server command in the remaining prefix.
	ambiguous := false
	for s.Scan() {
		arg := s.Bytes()
		switch string(arg) {
		case "app-server", "exec-server", "mcp-server", "remote-control", "--remote":
			return false
		}
		if bytes.HasPrefix(arg, []byte("--remote=")) {
			return false
		}
		if ambiguous {
			continue
		}
		switch string(arg) {
		case "-c", "--config", "-p", "--profile", "-m", "--model", "-a", "--ask-for-approval", "-s", "--sandbox", "-C", "--cd", "-i", "--image", "--enable", "--disable", "--add-dir", "--local-provider", "--remote-auth-token-env":
			if !s.Scan() {
				return false
			}
			ambiguous = !structured
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			// For an unknown flag the next word might be its value.
			if !codexBooleanFlag(arg) && (!structured || !bytes.ContainsRune(arg, '=')) {
				ambiguous = true
			}
			continue
		}
		// First positional is a terminal command or the initial prompt.
		return true
	}
	return s.Err() == nil && limited.N > 0
}

func codexBooleanFlag(arg []byte) bool {
	switch string(arg) {
	case "--no-daemon", "--no-alt-screen", "--strict-config", "--oss", "--search", "--worktree", "--approve-for-me", "--dangerously-bypass-hook-trust", "--dangerously-bypass-approvals-and-sandbox":
		return true
	}
	return false
}

func codexShortExecutable(name string) bool {
	// Darwin's kernel comm field truncates longer target-triple names.
	return name == "codex" || strings.HasPrefix(name, "codex-x86_64-") || strings.HasPrefix(name, "codex-aarch64-")
}

func codexHookShell(name string) bool {
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh", "fish":
		return true
	}
	return false
}
