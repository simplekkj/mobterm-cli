package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplyReopenAfterHalfHour(t *testing.T) {
	fx := newReplyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	r := fx.record
	r.Renewable = true
	r.Expires = time.Now().Add(-30 * time.Minute).Unix()
	r.State = "expired"
	r.Birth = "departed-registration-helper"
	if err := saveReply(fx.s, r); err != nil {
		t.Fatal(err)
	}
	out := make(chan replyResult, 1)
	errs := make(chan error, 2)
	go func() {
		v, err := replyRequest(ctx, fx.s, fx.api, fx.pairs[0], "inspect", fx.frames[0])
		errs <- err
		out <- v
	}()
	marker := filepath.Join(fx.s.Dir, wakeFile(r.SourceID))
	for {
		if _, err := os.Lstat(marker); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-time.After(time.Millisecond * 5):
		case <-ctx.Done():
			t.Fatal("inspect never requested wake")
		}
	}
	body := map[string]any{"v": 1, "source_id": r.SourceID, "owner_token": "incorrect"}
	if _, err := reopenReply(ctx, fx.s, fx.api, bytes.NewReader(interactionFrameBytes(body))); err == nil {
		t.Fatal("foreign host token claimed wake")
	}
	body["owner_token"] = r.OwnerToken
	claimed := make(chan any, 1)
	go func() {
		v, err := reopenReply(ctx, fx.s, fx.api, bytes.NewReader(interactionFrameBytes(body)))
		errs <- err
		claimed <- v
	}()
	result := <-out
	if err := <-errs; err != nil || result.State != "eligible" || result.Expires <= time.Now().Unix() {
		t.Fatalf("renewed inspect: %s %s %v", result.State, result.Reason, err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker left after claim: %v", err)
	}
	if _, err := reopenReply(ctx, fx.s, fx.api, bytes.NewReader(interactionFrameBytes(body))); err == nil {
		t.Fatal("duplicate wake acquired helper")
	}
	frame := replySendFrame(fx.frames[0], r, " original body 中文 ")
	frame.Expires = result.Expires
	result, err := replyRequest(ctx, fx.s, fx.api, fx.pairs[0], "send", frame)
	if err != nil || result.State != "decision_recorded" {
		t.Fatalf("send: %s %s %v", result.State, result.Reason, err)
	}
	v := <-claimed
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	got := v.(map[string]any)
	if got["decision_id"] != frame.DecisionID || got["text"] != frame.Text {
		t.Fatal("claimed a different decision or message")
	}
	disk, err := readReply(fx.s, r.SourceID)
	if err != nil || disk.Frame.Text != "" || disk.State != "dispatching" {
		t.Fatal("claimed body not durably removed")
	}
}

func TestReplyRegistrationHandsOffToRunningHost(t *testing.T) {
	fx := newReplyFixture(t)
	t.Setenv("MOBTERM_CONFIG_DIR", fx.s.Dir)
	now := time.Now()
	payload := map[string]any{"runtime_version": "1.5.2", "session_id": "registered_session", "run_id": "registered_run", "event_id": "registered_event", "instance_id": "registered_instance", "expires_at": now.Add(4 * time.Second).Unix(), "renewable": true}
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	if code := run([]string{"reply", "wait", "--source", "pi"}, bytes.NewReader(input), &output, &diagnostic); code != 0 {
		t.Fatalf("registration failed: %s", diagnostic.String())
	}
	var response struct {
		V      int    `json:"v"`
		Wake   bool   `json:"wake"`
		Source string `json:"source_id"`
		Owner  string `json:"owner_token"`
	}
	if err := interactionJSON(output.Bytes(), &response); err != nil || !response.Wake || response.V != 1 {
		t.Fatal("invalid wake handle", err)
	}
	record, err := readReply(fx.s, response.Source)
	if err != nil || !record.Renewable || record.State != "expired" || record.Frame != nil || record.OwnerToken != response.Owner {
		t.Fatal("registration helper did not leave a dormant, source-bound handle", err)
	}
}

func TestReplyWatermarkFollowsSessionAcrossPanesAndSSHProfiles(t *testing.T) {
	originalConfig := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", originalConfig)
	fx := newReplyFixture(t)
	record := fx.record
	moved := fx.l
	moved.ViewID = "@99"
	moved.PaneID = "%99"
	origin := Activity{Agent: record.Agent, Session: record.Session, Run: record.Run, Instance: record.Instance, Occurrence: record.Cursor, Reminder: "turn_completed", State: "turn_completed"}
	first := replyIntentBinding(origin, moved, fx.pairs[0], record.ParentBirth)
	if first == nil || first.SourceID != record.SourceID {
		t.Fatal("moving the original source changed its reply identity")
	}

	secondConfig := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", secondConfig)
	other := replyIntentBinding(origin, moved, fx.pairs[0], record.ParentBirth)
	if other == nil || other.SourceID == record.SourceID {
		t.Fatal("equal native IDs from different Agent environments collided")
	}
	activity := Activity{Agent: record.Agent, Session: record.Session, Run: record.Run, Instance: record.Instance, Occurrence: digest("other-input"), State: "starting"}
	_, otherRun, changed, err := applyActivity(context.Background(), fx.s, fx.l, activity)
	if err != nil || !changed || otherRun.LogicalSession == record.LogicalSession {
		t.Fatal("new namespace merged with original run", err)
	}
	if got := replyCursorReason(fx.s, record); got != "" {
		t.Fatal("SSH re-derived the source namespace", got)
	}
	t.Setenv("PI_CODING_AGENT_DIR", originalConfig)
	activity.Run = "later-turn"
	_, _, changed, err = applyActivity(context.Background(), fx.s, moved, activity)
	if err != nil || !changed {
		t.Fatal("moved native input was lost", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", secondConfig)
	if got := replyCursorReason(fx.s, record); got != "source_changed" {
		t.Fatal("new input in the moved pane left old reply eligible", got)
	}

	id := randomID()
	t.Setenv("PI_CODING_AGENT_DIR", originalConfig)
	activity.Occurrence = digest("request-on-moved-pane")
	activity.State = "waiting_input"
	activity.Reminder = "needs_input"
	activity.Interaction = &InteractionHint{RequestID: id}
	db, err := loadRuns(fx.s)
	if err != nil {
		t.Fatal(err)
	}
	if !advanceReplyHead(&db, activity, replyScope(moved.InstallationID, record.LogicalSession), time.Now().Unix()) {
		t.Fatal("original request did not advance")
	}
	if err := saveRuns(fx.s, db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", secondConfig)
	if !nativeRequestCurrent(fx.s, record.Agent, record.Session, id, fx.l, record.LogicalSession) ||
		nativeRequestCurrent(fx.s, record.Agent, record.Session, "different-request", fx.l, record.LogicalSession) {
		t.Fatal("SSH profile or moved pane changed original request ownership")
	}
}

func TestClaudePromptCycleStaysInOriginatingNamespace(t *testing.T) {
	configs := []string{t.TempDir(), t.TempDir()}
	s := testStore(t)
	ctx := context.Background()
	const session = "same-native-session"
	cycles := make([]string, 2)
	for i, config := range configs {
		t.Setenv("CLAUDE_CONFIG_DIR", config)
		input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": session, "prompt_id": "same-prompt"})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = parseEvent(ctx, s, "claude", input); err != nil {
			t.Fatal(err)
		}
		cycle, _, _, err := claudeCycleEvent(ctx, s, session, "stop", "", "same-prompt")
		if err != nil || cycle == "" {
			t.Fatal("missing captured cycle", err)
		}
		cycles[i] = cycle
	}
	if cycles[0] == cycles[1] {
		t.Fatal("independent configurations shared a Claude cycle")
	}
	for i, config := range configs {
		t.Setenv("CLAUDE_CONFIG_DIR", config)
		cycle, stopped, _, err := claudeCycleEvent(ctx, s, session, "read", "", "same-prompt")
		if err != nil || !stopped || cycle != cycles[i] {
			t.Fatal("other namespace displaced the source cycle", err)
		}
	}
}
