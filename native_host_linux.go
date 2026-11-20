package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func nativeOpenDatabase(_ context.Context, pid int, path string) bool {
	if pid <= 1 || !filepath.IsAbs(path) {
		return false
	}
	want, err := os.Stat(path)
	if err != nil || !want.Mode().IsRegular() {
		return false
	}
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, fd := range fds {
		if current, e := os.Stat(filepath.Join(dir, fd.Name())); e == nil && os.SameFile(want, current) {
			return true
		}
	}
	return false
}

// Match only sockets actually held by these two processes in their own
// network namespace. Proc tables contain socket metadata and no payload.
func nativeLoopbackConnection(_ context.Context, host, server int) bool {
	sockets := func(pid int) map[string]bool {
		dir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		found := make(map[string]bool)
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil {
				continue
			}
			if number, ok := strings.CutPrefix(link, "socket:["); ok {
				found[strings.TrimSuffix(number, "]")] = true
			}
		}
		return found
	}
	tui, service := sockets(host), sockets(server)
	if len(tui) == 0 || len(service) == 0 {
		return false
	}
	for _, protocol := range []string{"tcp", "tcp6"} {
		file, err := os.Open(fmt.Sprintf("/proc/%d/net/%s", host, protocol))
		if err != nil {
			continue
		}
		matches := pairedProcSockets(file, tui, service)
		file.Close()
		if matches {
			return true
		}
	}
	return false
}

func pairedProcSockets(table io.Reader, client, service map[string]bool) bool {
	loopback := func(endpoint string) bool {
		addr, port, ok := strings.Cut(endpoint, ":")
		if !ok || len(port) != 4 {
			return false
		}
		if len(addr) == 32 {
			if addr == "00000000000000000000000001000000" {
				return true
			}
			if !strings.HasPrefix(addr, "0000000000000000FFFF0000") {
				return false
			}
			addr = addr[24:]
		}
		// Linux prints each 32-bit address word in native order.
		if len(addr) != 8 {
			return false
		}
		word, err := strconv.ParseUint(addr, 16, 32)
		if err != nil {
			return false
		}
		return byte(word) == 127
	}
	scan := bufio.NewScanner(io.LimitReader(table, 4<<20))
	clients, servers := map[string]bool{}, map[string]bool{}
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) < 10 || f[3] != "01" || (!client[f[9]] && !service[f[9]]) || !loopback(f[1]) || !loopback(f[2]) {
			continue
		}
		if client[f[9]] {
			clients[f[1]+" "+f[2]] = true
		}
		if service[f[9]] {
			servers[f[2]+" "+f[1]] = true
		}
	}
	for pair := range clients {
		if servers[pair] {
			return true
		}
	}
	return false
}
