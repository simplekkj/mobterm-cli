package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"strings"
)

func processIdentity(pid int) (string, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || strings.TrimSpace(boot) == "" {
		return "", errors.New("process_boot_identity_unavailable")
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p == nil || p.Proc.P_pid != int32(pid) || p.Proc.P_starttime.Sec <= 0 {
		return "", errors.New("process_birth_unavailable")
	}
	return "tmux-proc-v1:" + digest(fmt.Sprintf("darwin\n%s\n%d\n%d:%d", strings.ToLower(strings.TrimSpace(boot)), pid, p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec)), nil
}
