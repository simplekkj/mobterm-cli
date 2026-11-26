package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// lsof's txt record describes the running image's vnode. Reject an updated
// path which now refers to another inode, even if both versions are installed.
func claudeImage(ctx context.Context, pid int) (string, error) {
	var budget = 65536
	b, err := probeCommand(exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-F", "fDin"), &budget)
	if err != nil {
		return "", err
	}
	var device, inode uint64
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'f':
			device, inode = 0, 0
		case 'D':
			device, _ = strconv.ParseUint(line[1:], 0, 64)
		case 'i':
			inode, _ = strconv.ParseUint(line[1:], 10, 64)
		case 'n':
			path := line[1:]
			if !claudeBinaryName(path) {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if ok && inode != 0 && device == uint64(stat.Dev) && inode == stat.Ino {
				return path, nil
			}
		}
	}
	return "", errors.New("claude_host_unverified")
}
