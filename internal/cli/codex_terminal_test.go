package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func fixtureCodexHost(t *testing.T) {
	t.Helper()
	previous := terminalCodexHost
	terminalCodexHost = func(context.Context, int, int) bool { return true }
	t.Cleanup(func() { terminalCodexHost = previous })
}

// Exercise real kernel sessions. The detached host remains a descendant of
// the original pane, just like a daemon first launched by that pane's TUI.
func TestCodexHookTerminalProcessSessions(t *testing.T) {
	fixtureCodexHost(t)
	const stageKey = "MOBTERM_FIXTURE_HOOK_STAGE"
	stage := os.Getenv(stageKey)
	pane := os.Getpid()
	if stage != "" {
		var err error
		pane, err = strconv.Atoi(os.Getenv("MOBTERM_FIXTURE_PANE_PROCESS"))
		if err != nil {
			t.Fatal(err)
		}
	}
	spawnSession := func(next string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestCodexHookTerminalProcessSessions$")
		cmd.Env = append(os.Environ(), stageKey+"="+next, "MOBTERM_FIXTURE_PANE_PROCESS="+strconv.Itoa(pane))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %s (%v)", next, output, err)
		}
	}

	switch stage {
	case "":
		if !codexPaneOwner(context.Background(), pane, false) {
			t.Fatal("pane-local completion refused")
		}
		spawnSession("detached")
	case "detached":
		if codexPaneOwner(context.Background(), pane, false) || !codexPaneOwner(context.Background(), pane, true) {
			t.Fatal("direct completion and native command session were not distinguished")
		}
		spawnSession("daemon-native-hook")
	case "daemon-native-hook":
		if codexPaneOwner(context.Background(), pane, true) || codexPaneOwner(context.Background(), pane, false) {
			t.Fatal("detached shared host borrowed original pane")
		}
	default:
		t.Fatal("unexpected fixture stage")
	}
}

func TestCodexHerdrRechecksOccupantAfterProcessQuery(t *testing.T) {
	fixtureCodexHost(t)
	// Short path for sockaddr_un on Darwin.
	root, err := os.MkdirTemp("/tmp", "mt-occupant-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HERDR_SESSION", "work")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("TMUX", "")
	socket, err := herdrSocket("work")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_SOCKET_PATH", socket)
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		terminal := "term_first"
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req struct{ ID, Method string }
			json.NewDecoder(conn).Decode(&req)
			var result any
			switch req.Method {
			case "session.snapshot":
				result = map[string]any{"type": "session_snapshot", "snapshot": herdrSnapshot{
					Version: "0.9.3", Protocol: new(uint32(22)), Panes: []herdrPaneRecord{{"w1:p1", terminal, "w1", "w1:t1"}},
				}}
			case "pane.process_info":
				result = map[string]any{"type": "pane_process_info", "process_info": map[string]any{"pane_id": "w1:p1", "shell_pid": os.Getpid()}}
				terminal = "term_replacement"
			}
			printJSON(conn, map[string]any{"id": req.ID, "result": result})
			conn.Close()
		}
	}()
	_, err = captureHookTarget(context.Background(), fixtureLocator().InstallationID, "codex", false)
	if !errors.Is(err, errCodexPaneOwner) {
		t.Fatal("replacement during the owner check was not refused", err)
	}
	// The following publication can use the now-stable replacement.
	l, err := captureHookTarget(context.Background(), fixtureLocator().InstallationID, "codex", false)
	if err != nil || l.TerminalID != "term_replacement" || !strings.HasPrefix(l.ServerIdentity, "socket-v1:") {
		t.Fatal("stable, pane-local target not admitted", err)
	}
}

func TestCodexUnboundCompletionRecordsOnlyRefusal(t *testing.T) {
	s, path, exe := codexLayoutEnvironment(t)
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_PANE_ID", "")
	if err := changeHooks(context.Background(), s, "codex", "install", path, exe, true); err != nil {
		t.Fatal(err)
	}
	h := inspectHook(s, "codex")
	if h.OwnerRefusalAt != 0 || h.ConfigurationState != "complete" {
		t.Fatal(h)
	}
	payload := `{"type":"agent-turn-complete","thread-id":"sample","turn-id":"turn","last-assistant-message":"PRIVATE"}`
	var out, diagnostic bytes.Buffer
	if code := run([]string{"notify", "--source", "codex", payload}, strings.NewReader(""), &out, &diagnostic); code != 0 ||
		!strings.Contains(diagnostic.String(), errCodexPaneOwner.Error()) || strings.Contains(diagnostic.String(), "PRIVATE") {
		t.Fatal("fail-open diagnostic changed", code)
	}
	h = inspectHook(s, "codex")
	if h.OwnerRefusalAt <= 0 || h.ConfigurationState != "complete" || h.Loaded != "unknown" {
		t.Fatal(h)
	}
	records, err := outboxRecords(context.Background(), s, "", false)
	if err != nil || len(records) != 0 {
		t.Fatal("unbound completion entered delivery", err)
	}
}

func TestCodexDispatchCommandRoles(t *testing.T) {
	for _, sample := range []struct {
		args     []string
		terminal bool
	}{
		{[]string{}, true},
		{[]string{"--no-daemon", "read the app-server notes"}, true},
		{[]string{"exec", "explain app-server"}, true},
		{[]string{"app-server", "--listen", "unix:///private/fixture"}, false},
		{[]string{"--no-daemon", "app-server", "--listen", "stdio://"}, false},
		{[]string{"-c", "x = 1", "app-server"}, false},
		{[]string{"-c", "x --config", "app-server"}, false},
		{[]string{"--config=x = 1", "app-server"}, false},
		{[]string{"--remote", "unix:///private/fixture"}, false},
	} {
		argv := strings.Join(append([]string{"codex"}, sample.args...), "\x00") + "\x00"
		if got := codexTerminalArgs(strings.NewReader(argv), true); got != sample.terminal {
			t.Fatalf("structured command %#v: %v", sample.args, got)
		}
		if got := codexTerminalArgs(strings.NewReader(strings.Join(sample.args, " ")), false); got != sample.terminal {
			t.Fatalf("args-only command %#v: %v", sample.args, got)
		}
	}
}
