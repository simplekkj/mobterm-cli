package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	revertedThread  = "019ab332-57c1-7223-8700-b7e55d40a880"
	revertRolloutID = "019ab33b-4802-70cb-a556-18a9d3b0fe69"
)

func codexMetadataFixture(t *testing.T, thread string, fields map[string]any) string {
	fixtureCodexHost(t)
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
	return writeCodexMetadata(t, root, thread, fields)
}
func writeCodexMetadata(t *testing.T, root, thread string, fields map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", "2026", "11", "17")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if fields == nil {
		fields = map[string]any{"source": "cli", "thread_source": "user"}
	}
	fields["id"] = thread
	raw, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": fields})
	path := filepath.Join(dir, "rollout-2026-11-17T12-00-00-"+thread+".jsonl")
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestCodexCompletionRequiresRootSource(t *testing.T) {
	for _, rich := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "rich"}[rich], func(t *testing.T) {
			s := testStore(t)
			codexMetadataFixture(t, "root", nil)
			writeCodexMetadata(t, os.Getenv("CODEX_HOME"), "child", map[string]any{"source": map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "root", "depth": 1}}}})
			for _, thread := range []string{"hidden", "child", "root"} {
				raw, _ := json.Marshal(map[string]string{"type": "agent-turn-complete", "thread-id": thread, "turn-id": "turn"})
				var kind string
				var err error
				if rich {
					a, input, e := parseActivity(context.Background(), s, "codex", raw)
					if e == nil {
						allowed, gateErr := admitCodexActivity(context.Background(), s, fixtureLocator(), &a, input)
						e = gateErr
						if !allowed {
							a.Reminder = ""
						}
					}
					kind, err = a.Reminder, e
				} else {
					kind, _, err = parseEvent(context.Background(), s, "codex", raw)
				}
				if thread == "root" {
					if err != nil || kind != "turn_completed" {
						t.Fatal(thread, kind, err)
					}
				} else if kind != "" {
					t.Fatal("non-root completion accepted", thread)
				}
			}
		})
	}
}
func TestClaudeSubagentCannotReplaceRootCycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"UserPromptSubmit","agent_type":"custom-root"}`))
	before, _, _ := claudeActivityCycle(ctx, s, "root", "", false)
	for _, name := range []string{"UserPromptSubmit", "Stop", "PreToolUse"} {
		raw, _ := json.Marshal(map[string]string{"session_id": "root", "hook_event_name": name, "agent_id": "child", "tool_use_id": "tool", "tool_name": "Bash"})
		a, _, err := parseActivity(ctx, s, "claude", raw)
		if err != nil || a.State != "" {
			t.Fatal("child polluted root", name, a, err)
		}
	}
	after, stopped, err := claudeActivityCycle(ctx, s, "root", "", false)
	if err != nil || before == "" || after != before || stopped {
		t.Fatal("child changed root cycle", before, after, stopped, err)
	}
}

func TestCodexMetadataBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   codexOrigin
	}{
		{"old-cli", map[string]any{"source": "cli"}, codexOriginRoot},
		{"root-fork", map[string]any{"source": "cli", "thread_source": "user", "forked_from_id": "previous"}, codexOriginRoot},
		{"exec", map[string]any{"source": "exec"}, codexOriginRoot},
		{"no-source", map[string]any{"originator": "codex_cli_rs"}, codexOriginUnknown},
		{"unknown-source", map[string]any{"source": "unknown"}, codexOriginUnknown},
		{"source-conflict", map[string]any{"source": "cli", "parent_thread_id": "parent"}, codexOriginUnknown},
		{"thread-conflict", map[string]any{"source": map[string]any{"subagent": "review"}, "thread_source": "user"}, codexOriginUnknown},
		{"feature", map[string]any{"source": "cli", "thread_source": "title_generation"}, codexOriginUnknown},
		{"internal", map[string]any{"source": map[string]any{"internal": "guardian"}}, codexOriginNonRoot},
		{"subagent", map[string]any{"source": map[string]any{"subagent": "compact"}}, codexOriginNonRoot},
		{"session-mismatch", map[string]any{"source": "cli", "session_id": "other"}, codexOriginUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codexMetadataFixture(t, "thread", c.fields)
			if got := codexCompletionOrigin(context.Background(), "thread"); got != c.want {
				t.Fatal(got, c.want)
			}
		})
	}
	for _, name := range []string{"wrong-id", "duplicate-key", "oversize", "conflicting-candidates", "symlink-file", "symlink-directory", "symlink-root", "symlink-ancestor", "unknown-layout", "body-not-read", "canceled", "candidate-budget", "directory-budget"} {
		t.Run(name, func(t *testing.T) {
			path := codexMetadataFixture(t, revertedThread, nil)
			root := os.Getenv("CODEX_HOME")
			want := codexOriginUnknown
			ctx := context.Background()
			switch name {
			case "wrong-id":
				os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"other","source":"cli"}}`), 0600)
			case "duplicate-key":
				os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"`+revertedThread+`","source":{"subagent":"review"},"source":"cli"}}`), 0600)
			case "oversize":
				os.WriteFile(path, []byte(strings.Repeat(" ", codexMetadataLineBytes+1)), 0600)
			case "conflicting-candidates":
				err := os.WriteFile(strings.TrimSuffix(path, ".jsonl")+"_"+revertRolloutID+".jsonl", []byte(`{"type":"session_meta","payload":{"id":"`+revertedThread+`","source":{"subagent":"review"}}}`), 0600)
				if err != nil {
					t.Fatal(err)
				}
			case "symlink-file":
				os.Remove(path)
				os.Symlink(filepath.Join(root, "outside"), path)
			case "symlink-root":
				alias := filepath.Join(t.TempDir(), "alias")
				os.Symlink(root, alias)
				t.Setenv("CODEX_HOME", alias)
			case "symlink-ancestor":
				alias := filepath.Join(t.TempDir(), "alias")
				os.Symlink(filepath.Dir(root), alias)
				t.Setenv("CODEX_HOME", filepath.Join(alias, filepath.Base(root)))
			case "symlink-directory":
				os.Rename(filepath.Join(root, "sessions"), filepath.Join(root, "outside"))
				os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "sessions"))
			case "unknown-layout":
				if err := os.Rename(path, filepath.Join(root, "sessions", "2026", filepath.Base(path))); err != nil {
					t.Fatal(err)
				}
			case "body-not-read":
				f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
				f.WriteString(strings.Repeat("PRIVATE_BODY", 20000))
				f.Close()
				want = codexOriginRoot
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "candidate-budget":
				raw, _ := os.ReadFile(path)
				for i := 0; i < codexMetadataCandidates; i++ {
					name := fmt.Sprintf("rollout-2026-11-17T12-01-%02d-%s.jsonl", i, revertedThread)
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "directory-budget":
				for i := 0; i < codexMetadataEntries; i++ {
					os.WriteFile(filepath.Join(root, "sessions", fmt.Sprintf("unrelated-%d", i)), nil, 0600)
				}
			}
			if got := codexCompletionOrigin(ctx, revertedThread); got != want {
				t.Fatal(got, want)
			}
		})
	}
}

func TestCodexRevertFilenameIdentifiesOnlyTheThread(t *testing.T) {
	nearThread := revertedThread[:35] + "1"
	for _, tc := range []struct {
		name, layout, ids string
		want              codexOrigin
	}{
		{"dated", "sessions/2026/11/17", revertedThread + "_" + revertRolloutID, codexOriginRoot},
		{"legacy", "sessions", revertedThread + "_" + revertRolloutID, codexOriginRoot},
		{"archived", "archived_sessions", revertedThread + "_" + revertRolloutID, codexOriginRoot},
		{"rollout-id-only", "sessions", revertRolloutID + "_" + revertedThread, codexOriginUnknown},
		{"near-thread", "sessions", nearThread + "_" + revertRolloutID, codexOriginUnknown},
		{"wrong-thread", "sessions", revertRolloutID, codexOriginUnknown},
		{"hyphen-instead-of-underscore", "sessions", revertedThread + "-" + revertRolloutID, codexOriginUnknown},
		{"missing-rollout-id", "sessions", revertedThread + "_", codexOriginUnknown},
		{"malformed-rollout-id", "sessions", revertedThread + "_not-a-uuid", codexOriginUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Even a header claiming the queried thread cannot turn a different
			// thread's filename into an eligible source.
			path := codexMetadataFixture(t, revertedThread, nil)
			dir := filepath.Join(os.Getenv("CODEX_HOME"), tc.layout)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, filepath.Join(dir, "rollout-2026-11-17T12-00-00-"+tc.ids+".jsonl")); err != nil {
				t.Fatal(err)
			}
			if got := codexCompletionOrigin(context.Background(), revertedThread); got != tc.want {
				t.Fatalf("origin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaudeSummaryBelongsToStoppedPrompt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	event := func(name, prompt, summary string) Activity {
		raw, _ := json.Marshal(map[string]string{"session_id": "session", "hook_event_name": name, "prompt_id": prompt, "notification_type": "idle_prompt", "last_assistant_message": summary, "tool_use_id": "tool", "tool_name": "Bash"})
		a, _, err := parseActivity(ctx, s, "claude", raw)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	event("UserPromptSubmit", "p1", "")
	event("Stop", "p1", "First reply")
	if a := event("Notification", "p1", ""); a.State != "turn_completed" || a.Summary != "First reply" {
		t.Fatal(a)
	}
	event("PreToolUse", "p1", "")
	if a := event("Notification", "p1", ""); a.State != "" || a.Summary != "" {
		t.Fatal("continued cycle reused old Stop", a)
	}
	event("Stop", "p1", "Updated reply")
	if a := event("Notification", "p1", ""); a.Summary != "Updated reply" {
		t.Fatal(a)
	}
	event("UserPromptSubmit", "p2", "")
	event("Stop", "p1", "Late old reply")
	if a := event("Notification", "p1", ""); a.State != "" || a.Summary != "" {
		t.Fatal("old prompt leaked", a)
	}
	if a := event("Notification", "p2", ""); a.State != "" {
		t.Fatal("late Stop ended next prompt", a)
	}
	event("Stop", "p2", "")
	if a := event("Notification", "p2", ""); a.State != "turn_completed" || a.Summary != "" {
		t.Fatal("missing summary inherited old text", a)
	}
}

func TestCodexOwnershipThroughDurableNotification(t *testing.T) {
	for _, rich := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "rich"}[rich], func(t *testing.T) {
			claudeNotifyEnvironment(t)
			executable := t.TempDir()
			if err := os.WriteFile(filepath.Join(executable, "codex"), []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf 'codex-cli 0.169.0\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", executable+string(os.PathListSeparator)+os.Getenv("PATH"))
			metadata := codexMetadataFixture(t, "root", nil)
			writeCodexMetadata(t, os.Getenv("CODEX_HOME"), "child", map[string]any{"source": map[string]any{"subagent": "review"}})
			s := testStore(t)
			id, _ := s.installation()
			pair := testPair(id, "")
			var events []Envelope
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "capabilities") {
					printJSON(w, activityCaps(pair))
					return
				}
				if r.Method == "PUT" {
					printJSON(w, map[string]any{"v": 2})
					return
				}
				var e Envelope
				if json.NewDecoder(r.Body).Decode(&e) != nil {
					t.Error("invalid envelope")
				}
				events = append(events, e)
				if e.V == 2 {
					printJSON(w, map[string]any{"v": 2, "event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
				} else {
					printJSON(w, map[string]any{"event_id": e.EventID, "state": "apns_accepted"})
				}
			}))
			defer server.Close()
			pair.APIURL = server.URL
			if err := s.savePair(context.Background(), pair); err != nil {
				t.Fatal(err)
			}
			api := API{server.Client()}
			ctx := context.Background()
			workerUnlock, err := lockFile(ctx, filepath.Join(s.Dir, ".activity-pending-lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer workerUnlock()
			if rich {
				s.writeJSON("hooks-codex.json", HookRecord{CodexNative: "fixture"})
				if _, err := observe(ctx, s, api, "codex", []byte(`{"session_id":"root","turn_id":"one","hook_event_name":"UserPromptSubmit"}`)); err != nil {
					t.Fatal(err)
				}
				// The trusted native association survives loss of the metadata file and
				// cannot be recreated merely from a previously existing old StoredRun.
				os.Remove(metadata)
			}
			complete := func(thread, turn, summary string) error {
				raw, _ := json.Marshal(map[string]string{"type": "agent-turn-complete", "thread-id": thread, "turn-id": turn, "last-assistant-message": summary})
				_, err := notify(ctx, s, api, "codex", raw, "", false)
				return err
			}
			if err := complete("hidden", "helper", "Hidden title"); err == nil || err.Error() != "codex_completion_origin_unknown" {
				t.Fatal("missing metadata did not remain unknown", err)
			}
			if err := complete("child", "helper", "Child text"); err != nil {
				t.Fatal(err)
			}
			if len(events) != 0 {
				t.Fatal("helper produced notification")
			}
			if rich {
				raw := []byte(`{"session_id":"root","turn_id":"one","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tool"}`)
				if _, err := observe(ctx, s, api, "codex", raw); err != nil {
					t.Fatal(err)
				}
				db, err := loadRuns(s)
				if err != nil || len(db.Runs) != 1 {
					t.Fatal("helper created snapshot", len(db.Runs), err)
				}
				for _, run := range db.Runs {
					if run.State != "running" || !run.CodexRoot {
						t.Fatal("root lost state/proof", run.State, run.CodexRoot)
					}
				}
			}
			if err := complete("root", "one", "First root reply"); err != nil {
				t.Fatal(err)
			}
			if err := complete("root", "one", "Different duplicate text"); err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || summaryPlaintext(t, pair, events[0])["summary"] != "First root reply" {
				t.Fatal("root notification lost/frozen content replaced", len(events))
			}
			checkTestBody(t, s, pair, events[0], "First root reply")
			// A resume/fork-style completion without starting Hooks is independently
			// admitted from root metadata, even immediately after another short turn.
			writeCodexMetadata(t, os.Getenv("CODEX_HOME"), "root", map[string]any{"source": "cli", "forked_from_id": "prior"})
			if err := complete("root", "two", "Second root reply"); err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 || events[0].EventID == events[1].EventID || summaryPlaintext(t, pair, events[1])["summary"] != "Second root reply" {
				t.Fatal("short turns merged or inherited summary", len(events))
			}
			// This independent thread has never emitted a start Hook. Its sole
			// source of ownership evidence is the canonical reverted rollout.
			path := writeCodexMetadata(t, os.Getenv("CODEX_HOME"), revertedThread, nil)
			if err := os.Rename(path, strings.TrimSuffix(path, ".jsonl")+"_"+revertRolloutID+".jsonl"); err != nil {
				t.Fatal(err)
			}
			if err := complete(revertedThread, "after-revert", strings.Repeat("文", 130)); err != nil {
				t.Fatal(err)
			}
			if err := complete(revertedThread, "after-revert", "Duplicate"); err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 || summaryPlaintext(t, pair, events[2])["summary"] != strings.Repeat("文", 120) {
				t.Fatal("revert-only completion count or summary", len(events))
			}
			records, err := outboxRecords(ctx, s, "", false)
			if err != nil || len(records) != 3 {
				t.Fatal("durable completion count", len(records), err)
			}
		})
	}
}

func TestCodexOldRunAndChildCannotGrantRootProof(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	codexMetadataFixture(t, "unrelated", nil)
	l := fixtureLocator()
	a := Activity{Agent: "codex", Session: "root", Instance: "root", Run: "one", Occurrence: "old-completion", State: "turn_completed", Reminder: "turn_completed"}
	if _, _, _, err := applyActivity(ctx, s, l, a); err != nil {
		t.Fatal(err)
	}
	a.State = "starting"
	a.Occurrence = "new-start"
	a.Reminder = ""
	allowed, err := admitCodexActivity(ctx, s, l, &a, activityInput{HookEventName: "UserPromptSubmit"})
	if allowed || err == nil || a.CodexRoot {
		t.Fatal("old orphan became trusted", allowed, err)
	}
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Interrupt", "Stop"} {
		raw, _ := json.Marshal(map[string]string{"session_id": "root", "turn_id": "one", "agent_id": "child", "hook_event_name": event, "tool_name": "Bash"})
		got, _, err := parseActivity(ctx, s, "codex", raw)
		if err != nil || got.State != "" || got.CodexRoot {
			t.Fatal("child polluted root", event, got, err)
		}
	}
	raw := []byte(`{"session_id":"root","turn_id":"one","agent_id":"child","hook_event_name":"PreToolUse","tool_name":"request_user_input","tool_use_id":"request"}`)
	child, _, err := parseActivity(ctx, s, "codex", raw)
	if err != nil || child.Reminder != "needs_input" || child.Session == "root" || child.CodexRoot {
		t.Fatal("child interaction lost or shared root", child, err)
	}
}

func TestCodexConflictingCompletionIdentities(t *testing.T) {
	codexMetadataFixture(t, "root", nil)
	s := testStore(t)
	ctx := context.Background()
	for _, fields := range []map[string]string{
		{"session_id": "other"}, {"turn_id": "other"}, {"agent_id": "child"},
	} {
		raw := map[string]string{"type": "agent-turn-complete", "thread-id": "root", "turn-id": "turn"}
		for k, v := range fields {
			raw[k] = v
		}
		b, _ := json.Marshal(raw)
		if kind, _, _ := parseEvent(ctx, s, "codex", b); kind != "" {
			t.Fatal("legacy accepted contradictory provenance", fields)
		}
		a, _, _ := parseActivity(ctx, s, "codex", b)
		if a.State != "" || a.Reminder != "" {
			t.Fatal("rich accepted contradictory provenance", fields)
		}
	}
}

func TestClaudeOptionalPromptIdentityAndChildInteraction(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"UserPromptSubmit","prompt_id":"prompt","agent_type":"custom-root"}`))
	parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"Stop","last_assistant_message":"Root text"}`))
	a, _, err := parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"Notification","notification_type":"idle_prompt"}`))
	if err != nil || a.State != "turn_completed" || a.Summary != "Root text" {
		t.Fatal("optional prompt_id became mandatory", a, err)
	}
	child, _, err := parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"PreToolUse","agent_id":"child","tool_name":"AskUserQuestion","tool_use_id":"request"}`))
	if err != nil || child.Reminder != "needs_input" || child.Session != "child" || child.Instance == a.Instance {
		t.Fatal("child question lost or reused root", child, err)
	}
}

func TestClaudeChildQuestionKeepsNativeConfirmation(t *testing.T) {
	s, api, pair, _, notices := reviewInteractionSetup(t)
	if err := s.writeJSON("interaction-policy-v1.json", InteractionPolicy{pair.PairID: {"claude": {"question": 3}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx, waitSenders := withInteractionSenders(ctx)
	defer waitSenders()
	parseActivity(ctx, s, "claude", []byte(`{"session_id":"root","hook_event_name":"UserPromptSubmit"}`))
	rootCycle, _, _ := claudeActivityCycle(ctx, s, "root", "", false)
	raw := []byte(`{"session_id":"root","agent_id":"child","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_use_id":"call","tool_input":{"questions":[{"question":"Choice?","header":"Choice","options":[{"label":"A"},{"label":"B"}]}]}}`)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		value, err := observe(ctx, s, api, "claude", raw)
		if err == nil {
			err = writeInteractionOutput(io.Discard, s, value)
		}
		done <- err
	}()
	defer func() {
		cancel()
		for range done {
		}
	}()
	notice := reviewNotice(t, notices, done)
	if notice.SessionID != "child" {
		t.Fatal("child question shared root session")
	}
	reviewDecision(t, s, api, pair, notice, "answer", `{"answers":{"q0":"A"}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	record, err := interactionRead(s, notice.Interaction.RequestID)
	if err != nil || record.ExpectedSession != "child" {
		t.Fatal("wrong native confirmation identity", record.ExpectedSession, err)
	}
	_, err = observe(ctx, s, api, "claude", []byte(`{"session_id":"root","agent_id":"child","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"call","tool_input":{"answers":{"Choice?":"A"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	record, err = interactionRead(s, notice.Interaction.RequestID)
	if err != nil || record.State != "native_confirmed" {
		t.Fatal("child native answer not confirmed", record.State, err)
	}
	db, err := loadRuns(Store{Dir: s.Dir})
	if err != nil || len(db.Runs) != 1 {
		t.Fatal("child Post must reuse the persisted run", len(db.Runs), err)
	}
	for _, run := range db.Runs {
		if run.Run != notice.RunID || run.Session != "child" || len(run.Requests) != 0 || run.State != "running" || run.Tools["call"].Status != "returned" {
			t.Fatal("original question still waiting after native confirmation", run.State, len(run.Requests))
		}
	}
	for _, intent := range db.Intents {
		if intent.Kind == "turn_completed" {
			t.Fatal("child created completion reminder")
		}
	}
	cycle, stopped, err := claudeActivityCycle(ctx, s, "root", "", false)
	if err != nil || cycle != rootCycle || stopped {
		t.Fatal("child question modified root cycle", err)
	}
}

func TestUnknownCodexOriginPreservesNotifyChain(t *testing.T) {
	versionEnvironment(t)
	t.Setenv("FIXTURE_VERSION", "codex-cli 0.169.0")
	codexMetadataFixture(t, "unrelated", nil)
	s := testStore(t)
	s.installation()
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	script := filepath.Join(t.TempDir(), "chain.sh")
	output := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FIXTURE_CHAIN_OUTPUT", output)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FIXTURE_CHAIN_OUTPUT\"\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("hooks-codex.json", HookRecord{Chain: []string{script, "literal $() argument"}}); err != nil {
		t.Fatal(err)
	}
	payload := `{"type":"agent-turn-complete","thread-id":"hidden","turn-id":"turn","last-assistant-message":"PRIVATE_SUMMARY"}`
	var out, diag bytes.Buffer
	code := run([]string{"notify", "--source", "codex", "--mobterm-chain", payload}, strings.NewReader(""), &out, &diag)
	argv, err := os.ReadFile(output)
	if err != nil || code != 7 || out.Len() != 0 || string(argv) != "literal $() argument\n"+payload+"\n" || strings.Contains(diag.String(), "PRIVATE_") || !strings.Contains(diag.String(), "codex_completion_origin_unknown") {
		t.Fatal("origin filtering changed chain or diagnostic contract", code, err)
	}
}
