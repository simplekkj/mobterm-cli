package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os/exec"
	"strconv"
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

// ps emits argv only. KERN_PROCARGS2 also returns the process environment.
// Inspect only a positively identified Codex image and never retain ps output.
func codexDaemonImage(ctx context.Context, pid int) bool {
	cmd := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "comm=")
	output := &limitBuffer{limit: 4096}
	cmd.Stdout = output
	return cmd.Run() == nil && codexExecutable(string(bytes.TrimSpace(output.Bytes())))
}

func codexProcessRole(ctx context.Context, pid int) (known, terminal bool) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p == nil || p.Proc.P_pid != int32(pid) {
		return true, false
	}
	name := unix.ByteSliceToString(p.Proc.P_comm[:])
	if !codexShortExecutable(name) {
		return !codexHookShell(name), false
	}
	// Separate comm from args so a launcher path containing spaces cannot
	// turn one of its components into an apparent command.
	image := &limitBuffer{limit: 4096}
	command := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "comm=")
	command.Stdout = image
	if command.Run() != nil {
		return true, false
	}
	prefix := bytes.TrimSuffix(image.Bytes(), []byte{'\n'})
	if !codexExecutable(string(prefix)) {
		return true, false
	}
	cmd := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "args=")
	stream, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return true, false
	}
	reader := bufio.NewReaderSize(stream, 8192)
	start, err := reader.Peek(len(prefix))
	if err == nil && bytes.Equal(start, prefix) {
		reader.Discard(len(prefix))
		separator, err := reader.ReadByte()
		terminal = (err == io.EOF || err == nil && (separator == ' ' || separator == '\n') && codexTerminalArgs(reader, false))
	}
	stream.Close()
	err = cmd.Wait()
	return true, terminal && err == nil
}
