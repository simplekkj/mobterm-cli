package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"strconv"
	"strings"
)

func codexSocketPeer(c *net.UnixConn) (pid, uid int, err error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var inner error
	err = raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		cred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if inner == nil {
			pid = int(cred.Pid)
			uid = int(cred.Uid)
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
	raw, err := secureRead(fmt.Sprintf("/proc/%d/stat", pid), 8192)
	if err != nil {
		return 0, err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, errors.New("codex_parent_unavailable")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 2 {
		return 0, errors.New("codex_parent_unavailable")
	}
	return strconv.Atoi(fields[1])
}
