package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTmuxExactInactiveAndAmbiguity(t *testing.T) {
	id := fixtureLocator().InstallationID
	out := "/tmp/sock\t123:10:11\t$1\t@2\t%3\n/tmp/sock\t123:10:11\t$1\t@2\t%4\n"
	l, err := parseTmux(id, "/tmp/sock", "%3", out)
	if err != nil || l.PaneID != "%3" {
		t.Fatal("explicit inactive pane failed")
	}
	for _, bad := range []string{out + "/tmp/sock\t123:10:12\t$2\t@2\t%3\n", strings.ReplaceAll(out, "/tmp/sock", "/tmp/other"), strings.ReplaceAll(out, "%3", "%9")} {
		if _, err = parseTmux(id, "/tmp/sock", "%3", bad); err == nil {
			t.Fatal("ambiguous/wrong/missing target accepted")
		}
	}
}
func TestLiveTmuxIdentity(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("MOBTERM_REQUIRE_TMUX") == "1" {
			t.Fatal("tmux required but unavailable; live fixture must execute")
		}
		t.Skip("tmux unavailable; live fixture not executed")
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, append([]string{"-S", socket}, args...)...)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("tmux fixture: %v", err)
		}
		return strings.TrimSpace(string(b))
	}
	pane := run("new-session", "-d", "-s", "fixture", "-P", "-F", "#{pane_id}")
	defer exec.Command(binary, "-S", socket, "kill-server").Run()
	run("split-window", "-t", pane, "-d")
	l, err := captureTmux(ctx, fixtureLocator().InstallationID, socket, pane)
	if err != nil {
		t.Fatal(err)
	}
	run("rename-session", "-t", l.WorkspaceID, "renamed")
	current, err := captureTmux(ctx, l.InstallationID, socket, pane)
	if err != nil || current != l {
		t.Fatal("rename changed stable locator")
	}
	run("kill-pane", "-t", pane)
	if _, err = captureTmux(ctx, l.InstallationID, socket, pane); err == nil {
		t.Fatal("deleted pane accepted")
	}
}
func TestHerdrNamespaceSocketReplacementAndMembership(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mt-herdr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	socket, err := herdrSocket("work")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serve := func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_, _ = bufio.NewReader(c).ReadString('\n')
		printJSON(c, map[string]any{"id": "mobterm_snapshot", "result": map[string]any{"type": "session_snapshot", "snapshot": herdrSnapshot{Version: "0.9.3", Protocol: 22, Panes: []herdrPaneRecord{{PaneID: "w1:p1", TerminalID: "term_123", WorkspaceID: "w1", TabID: "w1:t1"}}}}})
	}
	go serve()
	l, err := captureHerdr(context.Background(), fixtureLocator().InstallationID, "work", "w1:p1")
	if err != nil {
		t.Fatal(err)
	}
	if l.Namespace != "work" || l.OccupantID != "term_123" || !strings.HasPrefix(l.ServerIdentity, "socket-v1:") {
		t.Fatal("bad Herdr identity")
	}
	first := l.ServerIdentity
	// Retain the old inode: unlink/rebind can reuse both inode and ctime on
	// filesystems with coarse timestamps. This fixture requires a distinct socket.
	if err = os.Rename(socket, socket+".old"); err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	second, err := socketIdentity(socket)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("socket replacement not detected")
	}
	snapshot := herdrSnapshot{Version: "0.9.3", Protocol: 22, Panes: []herdrPaneRecord{{"w1:p1", "term_123", "w1", "w1:t1"}, {"w1:p2", "term_123", "w1", "w1:t1"}}}
	if _, err = locatorFromHerdr(l.InstallationID, "work", "w1:p1", first, snapshot); err == nil {
		t.Fatal("duplicated occupant accepted")
	}
	snapshot.Version = "0.9.2"
	snapshot.Panes = snapshot.Panes[:1]
	if _, err = locatorFromHerdr(l.InstallationID, "work", "w1:p1", first, snapshot); err == nil {
		t.Fatal("unverified version accepted")
	}
}
