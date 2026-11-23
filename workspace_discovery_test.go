package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWorkspaceCapabilitiesNoInitialization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uninitialized")
	t.Setenv("MOBTERM_CONFIG_DIR", dir)
	var out, diag bytes.Buffer
	if run([]string{"capabilities", "--json"}, nil, &out, &diag) != 0 {
		t.Fatal(diag.String())
	}
	var v map[string]any
	if json.Unmarshal(out.Bytes(), &v) != nil || v["v"] != float64(1) || v["installation_id"] != nil || len(v["operations"].([]any)) != 4 {
		t.Fatal("invalid capability response")
	}
	if backends, ok := v["execution_backends"].([]any); !ok || (len(backends) > 1 || len(backends) == 1 && backends[0] != quickNativeBackend) {
		t.Fatal("unexpected execution backend")
	}
	if protocols, ok := v["quick_command_result_protocols"].([]any); !ok || len(protocols) != 1 || protocols[0] != float64(1) {
		t.Fatal("missing read-only result protocol")
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only probe wrote state")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := (Store{dir}).writeJSON("installation.json", storedInstallation{1, "test_installation_a"}); err != nil {
		t.Fatal(err)
	}
	v, err := readCapabilities(Store{dir})
	if err != nil || v["installation_id"] != "test_installation_a" {
		t.Fatal("existing identity unavailable")
	}
	if err := os.WriteFile(filepath.Join(dir, "installation.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCapabilities(Store{dir}); err == nil {
		t.Fatal("corrupt installation reported as absent")
	}
}

func TestLiveTmuxDirectory(t *testing.T) {
	exe, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("MOBTERM_REQUIRE_TMUX") == "1" {
			t.Fatal(err)
		}
		t.Skip("tmux unavailable")
	}
	socket := filepath.Join(t.TempDir(), "test.sock")
	ctx := context.Background()
	q := workspaceQuery{V: 1, Executable: exe, Socket: socket}
	query := func() []discoveryPane {
		t.Helper()
		v, e := discoverWorkspace(ctx, "tmux.discovery", bytes.NewReader(framed(q)))
		if e != nil {
			t.Fatal(e)
		}
		return v["panes"].([]discoveryPane)
	}
	if len(query()) != 0 {
		t.Fatal("absent server reported panes")
	}
	if _, err = os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery created socket")
	}
	if err = exec.Command(exe, "-S", socket, "new-session", "-d", "-s", "test").Run(); err != nil {
		t.Fatal(err)
	}
	defer exec.Command(exe, "-S", socket, "kill-server").Run()
	panes := query()
	if len(panes) != 1 || panes[0].SessionName != "test" || !panes[0].WindowActive || !panes[0].PaneActive || panes[0].ServerIdentity == "" {
		t.Fatal("incorrect tmux topology")
	}
	if _, err = discoverWorkspace(ctx, "tmux.discovery", bytes.NewReader(append(framed(q), 'x'))); err == nil {
		t.Fatal("trailing frame accepted")
	}
}

func TestLiveHerdrDirectory(t *testing.T) {
	binary := os.Getenv("MOBTERM_DISCOVERY_TEST_HERDR")
	if binary == "" {
		t.Skip("requires a pinned Herdr fixture executable")
	}
	root, e := os.MkdirTemp("/tmp", "mt-herdr-dir-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	for i, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		p := filepath.Join(root, strconv.Itoa(i))
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
		t.Setenv(k, p)
	}
	conf := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "herdr")
	if e = os.Mkdir(conf, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(conf, "config.toml"), []byte("onboarding = false\n"), 0600); e != nil {
		t.Fatal(e)
	}
	q := workspaceQuery{V: 1, Executable: binary, Namespace: "fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Explicit namespace plus removal of the calling terminal's inherited context.
	t.Setenv("HERDR_SESSION", "wrong")
	t.Setenv("HERDR_SOCKET_PATH", "/invalid")
	t.Setenv("HERDR_PANE_ID", "unrelated")
	call := func(operation string) map[string]any {
		t.Helper()
		v, e := discoverWorkspace(ctx, operation, bytes.NewReader(framed(q)))
		if e != nil {
			t.Fatal(operation, e)
		}
		return v
	}
	if sessions := call("herdr.list")["sessions"].([]directorySession); len(sessions) == 0 {
		t.Fatal("missing default namespace")
	}
	var cmd *exec.Cmd
	for _, key := range []string{"HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(key, "")
	}
	cmd = exec.CommandContext(ctx, binary, "--session", "fixture", "server")
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	path, e := herdrSocket("fixture")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100; i++ {
		if _, e = socketIdentity(path); e == nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("server socket not ready", e)
	}
	t.Setenv("HERDR_SESSION", "wrong")
	t.Setenv("HERDR_SOCKET_PATH", "/invalid")
	probe := call("herdr.probe")
	status := probe["status"].(directoryStatus)
	if status.Running == nil || !*status.Running || status.Session != "fixture" {
		t.Fatal("incorrect running namespace")
	}
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	if _, e = exec.CommandContext(ctx, binary, "--session", "fixture", "workspace", "create", "--label", "fixture workspace").Output(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("HERDR_SESSION", "wrong")
	t.Setenv("HERDR_SOCKET_PATH", "/invalid")
	v := call("herdr.snapshot")
	var snap herdrSnapshot
	if json.Unmarshal(v["snapshot"].(json.RawMessage), &snap) != nil || len(snap.Panes) == 0 || snap.Panes[0].TerminalID == "" || v["server_identity"] == "" {
		t.Fatal("missing socket or terminal identity")
	}
}
