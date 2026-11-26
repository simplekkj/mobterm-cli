package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run by a witness extension at session_start in the isolated Pi fixture.
func TestNavigationEventSource(t *testing.T) {
	if os.Getenv("MOBTERM_NAV_FIXTURE_CHILD") != "1" {
		t.Skip("invoked by the live navigation fixture")
	}
	ctx := context.Background()
	s, err := defaultStore()
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.existingInstallation()
	if err != nil {
		t.Fatal(err)
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := Activity{Agent: "pi", Session: os.Getenv("MOBTERM_NAV_FIXTURE_SESSION"), NativeStorage: os.Getenv("MOBTERM_NAV_FIXTURE_FILE"), Instance: "fixture_activation", Run: "fixture_run", Occurrence: "fixture_event", State: "turn_completed", Reminder: "turn_completed"}
	if _, _, _, err = applyActivity(ctx, s, l, a); err != nil {
		t.Fatal(err)
	}
}

// No credentials or model requests. The second extension supplies one synthetic
// reminder using the real TUI's current session and parent PID. The installed
// production adapter alone can answer the subsequent signed navigation probes.
func TestLivePiNavigation(t *testing.T) {
	pi, cli := os.Getenv("PI_BINARY"), os.Getenv("MOBTERM_NAV_CLI")
	if pi == "" || cli == "" {
		t.Skip("set PI_BINARY and MOBTERM_NAV_CLI for the real interactive fixture")
	}
	root, err := os.MkdirTemp("/tmp", "mt-nav-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s := Store{filepath.Join(root, "store")}
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	p := testPair(id, "")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/pairs/"+p.PairID {
			http.NotFound(w, r)
			return
		}
		printJSON(w, map[string]string{"status": "active", "pair_id": p.PairID, "installation_id": id})
	}))
	defer server.Close()
	p.APIURL = server.URL
	if err = s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	config, home, socket := filepath.Join(root, "config"), filepath.Join(root, "home"), filepath.Join(root, "tmux.sock")
	for _, dir := range []string{config, home} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(pi, filepath.Join(bin, "pi")); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "TERM=xterm-256color", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_CODING_AGENT_DIR=" + config, "MOBTERM_CONFIG_DIR=" + s.Dir, "MOBTERM_NAV_FIXTURE_CHILD=1", "MOBTERM_NAV_TEST_EXE=" + exe, "MOBTERM_NAV_WITNESS=" + filepath.Join(root, "session")}
	run := func(cmd string, args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, cmd, args...)
		c.Dir = root
		c.Env = env
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%s: %v: %s", filepath.Base(cmd), e, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(cli, "hooks", "install", "--agent", "pi", "--json")
	witness := `import {spawnSync} from "node:child_process";
import {existsSync,writeFileSync} from "node:fs";
export default function(pi) {
  pi.on("session_start",(_,ctx)=>{
    const path=process.env.MOBTERM_NAV_WITNESS, session=ctx.sessionManager.getSessionId(), file=ctx.sessionManager.getSessionFile();
    if (!existsSync(path)) {
      const child=spawnSync(process.env.MOBTERM_NAV_TEST_EXE,["-test.run","^TestNavigationEventSource$"],{env:{...process.env,MOBTERM_NAV_FIXTURE_SESSION:session,MOBTERM_NAV_FIXTURE_FILE:file}});
      if (child.status!==0) throw new Error("source helper failed");
    }
    writeFileSync(path,JSON.stringify({session,file,pid:process.pid}),{mode:0o600});
  });
}`
	if err = os.WriteFile(filepath.Join(config, "extensions", "witness.ts"), []byte(witness), 0600); err != nil {
		t.Fatal(err)
	}
	tmux := func(args ...string) string {
		return run("tmux", append([]string{"-u", "-f", "/dev/null", "-S", socket}, args...)...)
	}
	// An existing empty file lets Pi create and persist its own native session
	// header without a provider request. Subsequent invocations reopen that file.
	sessionFile := filepath.Join(root, "source.jsonl")
	if err = os.WriteFile(sessionFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	resume := func(file string) string { return "exec " + shellQuote(pi) + " --session " + shellQuote(file) }
	pane := tmux("new-session", "-d", "-P", "-F", "#{pane_id}", "-x", "120", "-y", "32", "-c", root, resume(sessionFile))
	defer exec.Command("tmux", "-S", socket, "kill-server").Run()
	type active struct {
		Session string
		File    string
		PID     int
	}
	observe := func(previous active) active {
		t.Helper()
		for ctx.Err() == nil {
			raw, e := os.ReadFile(filepath.Join(root, "session"))
			var a active
			if e == nil && json.Unmarshal(raw, &a) == nil && a.Session != "" && a != previous && a.PID > 1 {
				return a
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("no distinct real Pi session reported")
		return active{}
	}
	first := observe(active{})
	db, err := loadRuns(s)
	if err != nil {
		t.Fatal(err)
	}
	key := digest(p.PairID + "\x00" + "fixture_event")
	intent := db.Intents[key]
	if intent.Navigation == nil || intent.Navigation.PID != first.PID {
		t.Fatal("notice failed to record its native owner")
	}
	intent, err = encodeRunIntent(ctx, s, key, intent, RunCapabilities{CapabilityGeneration: 1}, p)
	if err != nil {
		t.Fatal(err)
	}
	d, err := enqueueRunReminder(ctx, s, intent)
	if err != nil {
		t.Fatal(err)
	}
	f := replyFrame{bodyIdentity: bodyIdentity{1, p.APIURL, id, p.PairID, d.EventID, envelopeBodyDigest(*intent.Envelope), randomID()}, Expires: time.Now().Unix() + 30}
	check := func(expectedPane string) *Locator {
		t.Helper()
		want := "current"
		if expectedPane == "" {
			want = "unavailable"
		}
		f.Expires = time.Now().Add(30 * time.Second).Unix()
		res, e := replyRequest(ctx, s, API{server.Client()}, p, "latest", f)
		if e != nil || res.State != want {
			t.Fatalf("latest: %s %s %v (foreground %v pid %d)", res.State, res.Reason, e, navigationForeground(ctx, first.PID), first.PID)
		}
		if want == "current" && (res.Locator == nil || res.Locator.PaneID != expectedPane || res.Locator.OccupantID == "" || res.Expires > time.Now().Unix()+5) {
			t.Fatal("current response missing verified locator or lease")
		}
		if want != "current" && (res.Locator != nil || res.Reason != "navigation_binding_unconfirmed") {
			t.Fatal("unconfirmed returned a target")
		}
		return res.Locator
	}
	firstTarget := check(pane)
	destination := tmux("new-window", "-d", "-P", "-F", "#{window_id}", "-t", firstTarget.WorkspaceID)
	tmux("join-pane", "-d", "-s", pane, "-t", destination)
	movedTarget := check(pane)
	if movedTarget.ViewID != destination || movedTarget.ViewID == firstTarget.ViewID || movedTarget.OccupantID != firstTarget.OccupantID {
		t.Fatal("moved pane returned stale location or a different terminal")
	}
	tmux("send-keys", "-t", pane, "-l", "/new")
	tmux("send-keys", "-t", pane, "Enter")
	second := observe(first)
	if second.PID != first.PID || second.Session == first.Session {
		t.Fatal("fixture expected same Pi process after the switch")
	}
	check("")
	openHost := func(file string) string {
		return tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-c", root, resume(file))
	}
	otherFile := filepath.Join(root, "other.jsonl")
	raw, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(otherFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	otherPane := openHost(otherFile)
	other := observe(second)
	if other.Session != first.Session || other.File == first.File {
		t.Fatal("fixture expected the same session ID in independent storage")
	}
	check("")
	tmux("kill-pane", "-t", otherPane)
	restoredPane := openHost(sessionFile)
	restored := observe(other)
	if restored.Session != first.Session || restored.File != first.File || restored.PID == first.PID || restoredPane == pane {
		t.Fatal("fixture did not restore the source file in a different Pi process and pane")
	}
	current := check(restoredPane)
	if nativeTargetIdentity("", *current) == nativeTargetIdentity("", *firstTarget) {
		t.Fatal("restored Pi returned the original strong target")
	}
	if !navigationForeground(ctx, first.PID) {
		t.Fatal("fixture expected the switched original Pi to remain live")
	}
	tmux("respawn-pane", "-k", "-t", pane, "exec /bin/sleep 120")
	check(restoredPane)
	alias := filepath.Join(root, "same-inode.jsonl")
	if err = os.Link(sessionFile, alias); err != nil {
		t.Fatal(err)
	}
	duplicatePane := openHost(alias)
	duplicate := observe(restored)
	if duplicate.Session != restored.Session || duplicate.File == restored.File || duplicate.PID == restored.PID {
		t.Fatal("fixture did not open two hosts for the same inode under different filenames")
	}
	check("")
	tmux("kill-pane", "-t", duplicatePane)
	for ctx.Err() == nil {
		if _, e := processIdentity(duplicate.PID); e != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	check(restoredPane)
	t.Log("signed latest followed the moved pane, rejected /new and reuse, rebound to a different foreground Pi and pane, and denied two live copies until only one remained")
}
