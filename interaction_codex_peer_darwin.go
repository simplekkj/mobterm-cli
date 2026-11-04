package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
)

func codexSocketPeer(c *net.UnixConn) (pid, uid int, err error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var inner error
	err = raw.Control(func(fd uintptr) {
		var cred *unix.Xucred
		cred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if inner == nil {
			uid = int(cred.Uid)
			pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	})
	if err == nil {
		err = inner
	}
	if err == nil && pid <= 0 {
		err = errors.New("codex_peer_identity_unavailable")
	}
	return
}
func codexProcessParent(pid int) (int, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if p == nil || p.Proc.P_pid != int32(pid) {
		return 0, errors.New("codex_parent_unavailable")
	}
	return int(p.Eproc.Ppid), nil
}
