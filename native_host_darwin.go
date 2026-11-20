package main

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Confirm the named file is open in the specific local OpenCode server.
// lsof is restricted to that PID and file and emits descriptor fields only.
func nativeOpenDatabase(ctx context.Context, pid int, path string) bool {
	if pid <= 1 || !filepath.IsAbs(path) {
		return false
	}
	var budget = 4096
	b, err := probeCommand(exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-Fpf", "--", path), &budget)
	return err == nil && strings.HasPrefix(string(b), "p"+strconv.Itoa(pid)+"\nf")
}

func nativeLoopbackConnection(ctx context.Context, host, server int) bool {
	budget := 32768
	raw, err := probeCommand(exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(host)+","+strconv.Itoa(server), "-iTCP", "-sTCP:ESTABLISHED", "-Fpn"), &budget)
	return err == nil && connectedLoopbackPair(string(raw), host, server)
}

func connectedLoopbackPair(fields string, host, server int) bool {
	var pid int
	client, service := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(fields, "\n") {
		if len(line) < 2 {
			continue
		}
		if line[0] == 'p' {
			pid, _ = strconv.Atoi(line[1:])
			continue
		}
		if line[0] != 'n' || (pid != host && pid != server) {
			continue
		}
		left, right, ok := strings.Cut(line[1:], "->")
		if !ok {
			continue
		}
		local, _, le := net.SplitHostPort(left)
		remote, _, re := net.SplitHostPort(right)
		if le != nil || re != nil || !net.ParseIP(local).IsLoopback() || !net.ParseIP(remote).IsLoopback() {
			continue
		}
		if pid == host {
			client[left+"->"+right] = true
		} else {
			service[right+"->"+left] = true
		}
	}
	for pair := range client {
		if service[pair] {
			return true
		}
	}
	return false
}
