package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var tmuxPane = regexp.MustCompile(`^%[0-9]+$`)
var tmuxSession = regexp.MustCompile(`^\$[0-9]+$`)
var tmuxWindow = regexp.MustCompile(`^@[0-9]+$`)
var herdrSession = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type targetFailure struct{ code, reason string }

func (e targetFailure) Error() string   { return e.reason }
func invalidTarget(reason string) error { return targetFailure{"target_invalid", reason} }

func targetSocketInfo(path string) (os.FileInfo, error) {
	i, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, invalidTarget("terminal socket missing")
	}
	if err != nil {
		return nil, err
	}
	if i.Mode()&os.ModeSocket == 0 {
		return nil, invalidTarget("terminal socket replaced")
	}
	return i, nil
}

type limitBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("command output limit")
	}
	return b.Buffer.Write(p)
}

var errTmuxQuery = errors.New("target query failed")

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	out := &limitBuffer{limit: 2 * 1024 * 1024}
	c.Stdout = out
	c.Stderr = io.Discard
	if err := c.Run(); err != nil {
		var exited *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exited) {
			return nil, errTmuxQuery
		}
		return nil, errors.New("could not execute target query")
	}
	return out.Bytes(), nil
}
func captureTarget(ctx context.Context, id string) (Locator, error) {
	tm := os.Getenv("TMUX")
	hp := os.Getenv("HERDR_PANE_ID")
	if tm != "" && hp != "" {
		return Locator{}, errors.New("nested tmux/Herdr target is ambiguous")
	}
	if hp != "" {
		ns := os.Getenv("HERDR_SESSION")
		if ns == "" {
			ns = "default"
		}
		path, err := herdrSocket(ns)
		if err != nil {
			return Locator{}, err
		}
		if inherited := os.Getenv("HERDR_SOCKET_PATH"); inherited != "" && filepath.Clean(inherited) != path {
			return Locator{}, errors.New("custom Herdr socket cannot be mapped to a saved namespace")
		}
		return captureHerdr(ctx, id, ns, hp)
	}
	if tm == "" || os.Getenv("TMUX_PANE") == "" {
		return Locator{}, errors.New("no unambiguous agent terminal environment")
	}
	parts := strings.Split(tm, ",")
	if len(parts) < 3 {
		return Locator{}, errors.New("invalid tmux context")
	}
	socket := strings.Join(parts[:len(parts)-2], ",")
	return captureTmux(ctx, id, socket, os.Getenv("TMUX_PANE"))
}
func captureTmux(ctx context.Context, id, socket, pane string) (Locator, error) {
	if !filepath.IsAbs(socket) || strings.ContainsAny(socket, "\r\n\t\x00") || !tmuxPane.MatchString(pane) {
		return Locator{}, errors.New("invalid tmux environment")
	}
	l, _, err := captureTmuxProof(ctx, id, socket, pane)
	return l, err
}
func captureTmuxProof(ctx context.Context, id, socket, pane string) (Locator, int, error) {
	return captureTmuxProcess(ctx, id, socket, pane, processIdentity)
}
func captureTmuxProcess(ctx context.Context, id, socket, pane string, birth func(int) (string, error)) (Locator, int, error) {
	if !filepath.IsAbs(socket) || strings.ContainsAny(socket, "\r\n\t\x00") || !tmuxPane.MatchString(pane) {
		return Locator{}, 0, errors.New("invalid tmux environment")
	}
	query := func() (Locator, int, error) {
		out, err := commandOutput(ctx, "tmux", "-u", "-S", socket, "list-panes", "-a", "-F", "#{socket_path}\t#{pid}:#{start_time}:#{session_created}\t#{session_id}\t#{window_id}\t#{pane_id}\t#{pane_pid}")
		if err != nil {
			if errors.Is(err, errTmuxQuery) {
				if _, socketErr := targetSocketInfo(socket); socketErr != nil {
					return Locator{}, 0, socketErr
				}
			}
			return Locator{}, 0, err
		}
		var lines []string
		pid := 0
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 6 {
				return Locator{}, 0, errors.New("malformed tmux process query")
			}
			if f[4] == pane {
				pid, err = strconv.Atoi(f[5])
				if err != nil || pid <= 0 {
					return Locator{}, 0, errors.New("process_birth_unavailable")
				}
			}
			lines = append(lines, strings.Join(f[:5], "\t"))
		}
		l, err := parseTmux(id, socket, pane, strings.Join(lines, "\n"))
		return l, pid, err
	}
	before, pid, err := query()
	if err != nil {
		return Locator{}, 0, err
	}
	proof, err := birth(pid)
	if err != nil {
		return Locator{}, 0, err
	}
	after, nextPID, err := query()
	if err != nil {
		return Locator{}, 0, err
	}
	nextProof, err := birth(nextPID)
	if err != nil {
		return Locator{}, 0, err
	}
	// A pane may move between windows while its identity is sampled.
	before.ViewID = after.ViewID
	if before != after || pid != nextPID || proof != nextProof {
		return Locator{}, 0, invalidTarget("terminal_target_changed")
	}
	after.OccupantID = proof
	return after, pid, nil
}

func parseTmux(id, socket, pane, out string) (Locator, error) {
	matches := []Locator{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			return Locator{}, errors.New("malformed tmux query")
		}
		parts := strings.Split(f[1], ":")
		if len(parts) != 3 || !intText(parts[0]) || !intText(parts[1]) || !intText(parts[2]) || !tmuxSession.MatchString(f[2]) || !tmuxWindow.MatchString(f[3]) || !filepath.IsAbs(f[0]) || !tmuxPane.MatchString(f[4]) {
			return Locator{}, errors.New("invalid tmux identity")
		}
		if filepath.Clean(f[0]) != filepath.Clean(socket) {
			return Locator{}, errors.New("tmux socket identity mismatch")
		}
		if f[4] != pane {
			continue
		}
		matches = append(matches, Locator{V: 1, InstallationID: id, Backend: "tmux", Namespace: f[0], ServerIdentity: f[1], WorkspaceID: f[2], ViewID: f[3], PaneID: f[4]})
	}
	if len(matches) != 1 {
		return Locator{}, invalidTarget("tmux pane missing or has ambiguous membership")
	}
	return matches[0], nil
}
func herdrSocket(ns string) (string, error) {
	if !herdrSession.MatchString(ns) || ns == "." || ns == ".." {
		return "", errors.New("invalid Herdr namespace")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(h, ".config")
	}
	p := filepath.Join(base, "herdr")
	if ns != "default" {
		p = filepath.Join(p, "sessions", ns)
	}
	return filepath.Join(p, "herdr.sock"), nil
}

type herdrPaneRecord struct {
	PaneID      string `json:"pane_id"`
	TerminalID  string `json:"terminal_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
}
type herdrSnapshot struct {
	Version  string            `json:"version"`
	Protocol int               `json:"protocol"`
	Panes    []herdrPaneRecord `json:"panes"`
}

func captureHerdr(ctx context.Context, id, namespace, pane string) (Locator, error) {
	return captureHerdrTarget(ctx, id, namespace, pane, "")
}
func captureHerdrTarget(ctx context.Context, id, namespace, pane, terminal string) (Locator, error) {
	return queryHerdrTarget(ctx, id, namespace, pane, terminal, "")
}
func queryHerdrTarget(ctx context.Context, id, namespace, pane, terminal, expectedSocket string) (Locator, error) {
	path, err := herdrSocket(namespace)
	if err != nil {
		return Locator{}, err
	}
	before, err := socketIdentity(path)
	if err != nil {
		return Locator{}, err
	}
	if ctx.Err() != nil {
		return Locator{}, ctx.Err()
	}
	if expectedSocket != "" && before != expectedSocket {
		return Locator{}, invalidTarget("Herdr instance changed")
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return Locator{}, errors.New("Herdr namespace unavailable")
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	if _, err = io.WriteString(conn, "{\"id\":\"mobterm_snapshot\",\"method\":\"session.snapshot\",\"params\":{}}\n"); err != nil {
		return Locator{}, errors.New("Herdr query failed")
	}
	reader := bufio.NewReader(io.LimitReader(conn, 2*1024*1024+1))
	data, err := reader.ReadBytes('\n')
	if err != nil || len(data) > 2*1024*1024 {
		return Locator{}, errors.New("invalid Herdr response")
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			Type     string        `json:"type"`
			Snapshot herdrSnapshot `json:"snapshot"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || response.ID != "mobterm_snapshot" || response.Result.Type != "session_snapshot" {
		return Locator{}, errors.New("invalid Herdr snapshot")
	}
	after, err := socketIdentity(path)
	if err != nil {
		return Locator{}, err
	}
	if before != after {
		return Locator{}, invalidTarget("Herdr instance changed")
	}
	return locateHerdr(id, namespace, pane, terminal, before, response.Result.Snapshot)
}
func locatorFromHerdr(id, ns, pane, instance string, s herdrSnapshot) (Locator, error) {
	return locateHerdr(id, ns, pane, "", instance, s)
}
func locateHerdr(id, ns, pane, terminal, instance string, s herdrSnapshot) (Locator, error) {
	if (s.Version != "0.9.3" && s.Version != "0.9.1") || s.Protocol != 22 {
		return Locator{}, errors.New("Herdr notification locator requires verified 0.9.1 or 0.9.3 protocol 22")
	}
	if s.Panes == nil {
		return Locator{}, errors.New("incomplete Herdr snapshot")
	}
	var found []Locator
	terminals, panes := map[string]int{}, map[string]int{}
	duplicate := false
	for _, p := range s.Panes {
		l := Locator{V: 1, InstallationID: id, Backend: "herdr", Namespace: ns, ServerIdentity: instance, WorkspaceID: p.WorkspaceID, ViewID: p.TabID, PaneID: p.PaneID, TerminalID: p.TerminalID, OccupantID: p.TerminalID}
		if err := l.validate(); err != nil {
			return Locator{}, err
		}
		terminals[p.TerminalID]++
		panes[p.PaneID]++
		duplicate = duplicate || terminals[p.TerminalID] > 1 || panes[p.PaneID] > 1
		if terminal != "" && p.TerminalID == terminal || terminal == "" && p.PaneID == pane {
			found = append(found, l)
		}
	}
	if len(found) > 1 || len(found) == 1 && (panes[found[0].PaneID] > 1 || terminals[found[0].TerminalID] > 1) {
		return Locator{}, invalidTarget("ambiguous Herdr occupant")
	}
	if duplicate {
		return Locator{}, errors.New("inconsistent Herdr snapshot")
	}
	if len(found) == 0 {
		return Locator{}, invalidTarget("Herdr target missing")
	}
	return found[0], nil
}
func verifyTarget(ctx context.Context, s Store, in io.Reader) (map[string]any, error) {
	return checkTarget(ctx, s.installation, in, false)
}
func resolveTarget(ctx context.Context, s Store, in io.Reader) (map[string]any, error) {
	return checkTarget(ctx, s.installation, in, true)
}
func checkTarget(ctx context.Context, installation func() (string, error), in io.Reader, resolve bool) (map[string]any, error) {
	var expected Locator
	if err := readFrameContext(ctx, in, &expected); err != nil {
		return nil, err
	}
	if err := expected.validate(); err != nil {
		return nil, err
	}
	id, err := installation()
	if err != nil {
		return nil, err
	}
	if expected.InstallationID != id {
		return nil, targetFailure{"installation_replaced", "installation replaced"}
	}
	var actual Locator
	panePID := 0
	switch expected.Backend {
	case "tmux":
		if !strongTmuxOccupant.MatchString(expected.OccupantID) {
			return nil, targetFailure{"target_upgrade_required", "target_upgrade_required"}
		}
		actual, panePID, err = captureTmuxProof(ctx, id, expected.Namespace, expected.PaneID)
		// tmux can leave a socket inode after its last session exits. A rejected
		// connection alone is inconclusive; verify the captured server PID is gone.
		if errors.Is(err, errTmuxQuery) && ctx.Err() == nil {
			parts := strings.Split(expected.ServerIdentity, ":")
			if len(parts) == 3 {
				pid, parseErr := strconv.Atoi(parts[0])
				if parseErr == nil && pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
					return nil, invalidTarget("tmux server exited")
				}
			}
		}
	case "herdr":
		actual, err = queryHerdrTarget(ctx, id, expected.Namespace, "", expected.TerminalID, expected.ServerIdentity)
	default:
		return nil, errors.New("test has no terminal target")
	}
	if err != nil {
		return nil, err
	}
	comparison := actual
	comparison.ViewID = expected.ViewID
	if expected.Backend == "herdr" {
		comparison.WorkspaceID = expected.WorkspaceID
		comparison.PaneID = expected.PaneID
	}
	if comparison != expected {
		return nil, invalidTarget("terminal target replaced")
	}
	if !resolve && actual != expected {
		return nil, errors.New("terminal moved during verification")
	}
	result := map[string]any{"v": 1, "status": "valid", "locator": actual}
	if panePID > 0 {
		result["tmux_pane_pid"] = panePID
	}
	return result, nil
}
func identityText(dev, ino uint64, sec, nsec int64) string {
	return "socket-v1:" + digest(fmt.Sprintf("%d:%d:%d:%d", dev, ino, sec, nsec))
}

func targetErrorCode(err error) string {
	var failure targetFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "target_query_unavailable"
}
