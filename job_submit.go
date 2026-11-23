package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const quickJobFrameLimit = 2 << 20
const quickCommandLimit = 1 << 20
const quickNativeBackend = "quick_command_native_v1"

type quickStart struct {
	quickResultIdentity
	PairID         string `json:"pair_id"`
	EventID        string `json:"event_id"`
	CommandName    string `json:"command_name"`
	CommandB64     string `json:"command_b64"`
	ResultKey      string `json:"result_key"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	SubmitUntil    int64  `json:"submit_until"`
	Challenge      string `json:"challenge"`
}

type quickJobReply struct {
	quickResultIdentity
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
	OccurredAt int64  `json:"occurred_at,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Signal     *int   `json:"signal,omitempty"`
}

func quickScript(encoded string) (string, error) {
	if encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(quickCommandLimit) {
		return "", errors.New("invalid_command")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > quickCommandLimit || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 ||
		base64.StdEncoding.EncodeToString(raw) != encoded {
		return "", errors.New("invalid_command")
	}
	return string(raw), nil
}
func (q quickStart) digest() string {
	return digest(strings.Join([]string{"mobterm/quick-command-start/1", q.InstallationID, q.JobID, q.CommandID, q.PairID, q.EventID,
		base64.StdEncoding.EncodeToString([]byte(q.CommandName)), strconv.Itoa(q.TimeoutSeconds), q.CommandB64}, "\n"))
}

func (q quickStart) validate() error {

	if !q.quickResultIdentity.valid() || !validQuickCommandName(q.CommandName) || !opaqueID.MatchString(q.PairID) || !opaqueID.MatchString(q.EventID) ||
		q.TimeoutSeconds < 1 || q.TimeoutSeconds > 604800 || q.RequestDigest != q.digest() {
		return errors.New("invalid_request")
	}
	if _, err := decode64(q.ResultKey, 32); err != nil {
		return errors.New("invalid_request")
	}
	_, err := quickScript(q.CommandB64)
	return err
}

// Resolve the actual UID's account shell through the platform account database.
// Do not consult mutable shell or user environment hints.
func quickAccountShell(ctx context.Context) (string, error) {
	if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return "", errors.New("unsupported_shell")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	uid := strconv.Itoa(os.Getuid())
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/dscacheutil", "-q", "user", "-a", "uid", uid)
	case "linux":
		cmd = exec.CommandContext(ctx, "/usr/bin/getent", "passwd", uid)
	default:
		return "", errors.New("unsupported_shell")
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return "", errors.New("unsupported_shell")
	}
	raw, err := io.ReadAll(io.LimitReader(pipe, 8193))
	pipe.Close()
	if waitErr := cmd.Wait(); err != nil || waitErr != nil || len(raw) > 8192 {
		return "", errors.New("unsupported_shell")
	}
	shell := ""
	if runtime.GOOS == "darwin" {
		gotUID := ""
		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			switch key {
			case "uid":
				if gotUID != "" {
					return "", errors.New("unsupported_shell")
				}
				gotUID = value
			case "shell":
				if shell != "" {
					return "", errors.New("unsupported_shell")
				}
				shell = value
			}
		}
		if gotUID != uid {
			return "", errors.New("unsupported_shell")
		}
	} else {
		fields := strings.Split(strings.TrimSpace(string(raw)), ":")
		if len(fields) != 7 || fields[2] != uid {
			return "", errors.New("unsupported_shell")
		}
		shell = fields[6]
	}
	return quickSupportedShell(shell)
}

func quickSupportedShell(shell string) (string, error) {
	switch filepath.Base(shell) {
	case "bash":
		// BASH_ENV explicitly requests an additional startup script for every
		// non-interactive invocation. Re-entering it can repeat side effects
		// already performed by sshd's outer account shell.
		if os.Getenv("BASH_ENV") != "" {
			return "", errors.New("unsupported_shell")
		}
	case "zsh", "sh", "dash":
	default:
		return "", errors.New("unsupported_shell")
	}
	info, err := os.Stat(shell)
	if !filepath.IsAbs(shell) || err != nil || !info.Mode().IsRegular() || syscall.Access(shell, 1) != nil {
		return "", errors.New("unsupported_shell")
	}
	return shell, nil
}

func readQuickFrame(ctx context.Context, in io.Reader, target any) error {
	var raw json.RawMessage
	if readStrictFrame(ctx, in, &raw, quickJobFrameLimit) != nil || decodeQuickResultObject(raw, target) != nil {
		return errors.New("invalid_request")
	}
	return nil
}

func quickSpawnError(err error) error {
	if errors.Is(err, syscall.E2BIG) {
		return errors.New("argument_too_large")
	}
	return errors.New("spawn_failed")
}

func submitQuickJob(ctx context.Context, s Store, in io.Reader, out io.Writer) (any, error) {
	if _, err := quickAccountShell(ctx); err != nil {
		return nil, err
	}
	id, err := s.installation()
	if err != nil {
		return nil, errors.New("installation_unavailable")
	}
	now := time.Now().Unix()
	until, challenge := now+120, randomID()
	if err := json.NewEncoder(out).Encode(map[string]any{"v": 1, "state": "prepared", "installation_id": id,
		"server_time": now, "submit_until": until, "challenge": challenge}); err != nil {
		return nil, errors.New("prepare_failed")
	}
	var q quickStart
	if readQuickFrame(ctx, in, &q) != nil || q.validate() != nil ||
		q.InstallationID != id || q.Challenge != challenge || q.SubmitUntil != until {
		return nil, errors.New("invalid_request")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, errors.New("worker_unavailable")
	}
	reply, err := handoffQuickJob(ctx, exe, q)
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// Both inherited descriptors are private pipes. The worker never inherits
// SSH stdio, its PTY, its cancellation scope, or a shell command in argv/env.
func handoffQuickJob(ctx context.Context, exe string, q quickStart) (quickJobReply, error) {
	var reply quickJobReply
	request, send, err := os.Pipe()
	if err != nil {
		return reply, errors.New("worker_unavailable")
	}
	defer request.Close()
	defer send.Close()
	recv, ready, err := os.Pipe()
	if err != nil {
		return reply, errors.New("worker_unavailable")
	}
	defer recv.Close()
	defer ready.Close()
	cmd := exec.Command(exe, "job", "worker")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = []*os.File{request, ready}
	if err := cmd.Start(); err != nil {
		return reply, errors.New("worker_unavailable")
	}
	request.Close()
	ready.Close()
	go func() { _ = cmd.Wait() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = send.SetWriteDeadline(deadline)
		_ = recv.SetReadDeadline(deadline)
	}
	if err := json.NewEncoder(send).Encode(q); err != nil {
		return reply, errors.New("handoff_unknown")
	}
	send.Close()
	raw, err := io.ReadAll(io.LimitReader(recv, 4097))
	if err != nil || len(raw) > 4096 || interactionJSON(raw, &reply) != nil || reply.quickResultIdentity != q.quickResultIdentity {
		return quickJobReply{}, errors.New("handoff_unknown")
	}
	if reply.State == "error" {
		return quickJobReply{}, errors.New(safeCode(reply.Error))
	}
	return reply, nil
}
