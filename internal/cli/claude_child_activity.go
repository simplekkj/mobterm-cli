package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// These runs track one native child request, independently of the root cycle.
// A missing Post (including a killed hook) must not reserve a run slot forever.
type claudeChildWait struct {
	Owner string `json:"owner"`
	Until int64  `json:"until"`
}

func bindClaudeChildWait(a *Activity, p activityInput, l Locator) error {
	owner, err := processIdentity(os.Getppid())
	if err != nil {
		return err
	}
	kind, id := p.HookEventName, p.ToolUseID
	switch kind {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
		kind = p.ToolName
	case "Elicitation":
		id = p.RequestID
	}
	if id == "" {
		// Notifications and some elicitations have no native invocation ID.
		// Each such observation has its own lease and cannot consume a sibling.
		id = a.Occurrence
	}
	if !nativeInteractionID(id) && p.HookEventName != "Stop" && p.HookEventName != "SubagentStop" {
		return errors.New("invalid_request_identity")
	}
	parts, _ := json.Marshal([]string{"claude-child", a.Instance, p.TurnID, kind, id, owner})
	target, _ := json.Marshal(l)
	a.Run = digest(string(parts) + "\x00" + string(target))
	a.Occurrence = digest(a.Run + "\x00" + p.HookEventName)
	a.RequestID = id
	a.ChildWait = &claudeChildWait{Owner: owner, Until: time.Now().Unix() + 300}
	return nil
}

func childRunKey(a Activity, l Locator) string {
	instance := activityInstanceKey(a.Agent, a.Session, a.Instance, "", l)
	return digest(instance + "\x00" + a.Run)
}

func finishClaudeChildTool(ctx context.Context, s Store, l Locator, a Activity, p activityInput) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return err
	}
	key := childRunKey(a, l)
	run, ok := db.Runs[key]
	if !ok {
		// Before child leases, actionable questions had a random run ID.
		// Their persisted native identity still proves the exact owner and run,
		// even after the original request body has been scrubbed.
		for oldKey, old := range db.Runs {
			hint := old.Requests[a.RequestID].Interaction
			if old.ChildWait != nil || old.Agent != a.Agent || old.Instance != a.Instance || old.Session != a.Session || old.Locator != l || hint == nil || hint.Kind != "question" || p.ToolName != "AskUserQuestion" {
				continue
			}
			record, err := interactionRead(s, hint.RequestID)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			candidate := a
			candidate.Run = old.Run
			if record.Digest != hint.Digest || record.Locator != l || record.NativeIdentity != nativeInteractionIdentity(candidate, p.ToolUseID, l, a.ChildWait.Owner) {
				continue
			}
			if ok {
				return nil
			}
			key, run, ok = oldKey, old, true
		}
	}
	request, pending := run.Requests[a.RequestID]
	if !ok || !pending || request.Approval != nil || run.ChildWait != nil && run.ChildWait.Owner != a.ChildWait.Owner {
		return nil
	}
	if hint := request.Interaction; hint != nil {
		matched, err := finishClaudeChildReceiptLocked(s, key, l, a, p, hint)
		if err != nil || !matched {
			return err
		}
	}
	if run.ChildWait == nil && len(run.Requests) > 1 {
		a.ChildWait = nil
	}
	a.Run = run.Run
	a.ResolveRequest = true
	_, _, _, err = applyActivityLocked(s, l, a)
	return err
}

func finishClaudeChildReceiptLocked(s Store, key string, l Locator, a Activity, p activityInput, hint *InteractionHint) (bool, error) {
	r, err := interactionRead(s, hint.RequestID)
	if errors.Is(err, os.ErrNotExist) && hint.ExpiresAt <= time.Now().Unix() {
		return true, nil // Expired read-only context may already have been pruned.
	}
	if err != nil {
		return false, err
	}
	if r.ParentPID != 0 && (r.ParentPID != os.Getppid() || r.ParentBirth != a.ChildWait.Owner) {
		return false, nil
	}
	if r.Digest != hint.Digest || r.Locator != l || r.ChildRunKey != "" && r.ChildRunKey != key {
		return false, nil
	}
	var input struct {
		Answers json.RawMessage `json:"answers"`
	}
	if p.HookEventName == "PostToolUse" && r.Kind == "question" && r.ExpectedSession == a.Session && r.ExpectedNativeID == p.ToolUseID && r.ParentPID == os.Getppid() && r.ParentBirth == a.ChildWait.Owner && r.ExpiresAt > time.Now().Unix() && json.Unmarshal(p.ToolInput, &input) == nil {
		if err := confirmClaudeAnswers(s, &r, input.Answers); err != nil {
			return false, err
		}
	}
	if err := cancelChildInteractionLocked(s, r); err != nil {
		return false, err
	}
	return true, nil
}

// A native end invalidates only the still-live mobile waiter. Preserve any
// response already written and its evidence, including scrubbed receipts.
func cancelChildInteractionLocked(s Store, r InteractionRecord) error {
	switch r.State {
	case "pending", "decision_recorded":
		r.State = "native_cancelled"
		if r.ExpiresAt <= time.Now().Unix() {
			r.State = "expired"
		}
	case "dispatching":
		r.State = "delivery_unknown"
	default:
		return nil
	}
	r.RequestJSON = ""
	if r.Frame != nil {
		r.Frame.ResponseJSON = ""
	}
	return s.writeJSON("interaction-"+r.RequestID+".json", r)
}

func settleChildRequestLocked(s Store, run StoredRun, id, state string) error {
	activity := Activity{LogicalSession: run.LogicalSession, ChildWait: run.ChildWait, Agent: run.Agent, Session: run.Session, Instance: run.Instance, Run: run.Run, RequestID: id, ResolveRequest: true,
		State: state, Occurrence: digest(run.Run + "\x00end\x00" + id)}
	if tool, ok := run.Tools[id]; ok {
		activity.ToolID, activity.ToolCategory, activity.ToolStatus = id, tool.Category, "interrupted"
	}
	_, _, _, err := applyActivityLocked(s, run.Locator, activity)
	return err
}

// Existing flush/cleanup calls reconcile missing native callbacks. A scoped
// child Stop can retire its observations sooner. Neither creates a reminder.
func finishClaudeChildWaits(ctx context.Context, s Store, child *Activity, l Locator) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, run := range db.Runs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if run.ChildWait == nil || len(run.Requests) == 0 || run.Approval != nil {
			continue
		}
		if child == nil {
			if run.ChildWait.Until > now {
				continue
			}
		} else if run.Agent != child.Agent || run.Session != child.Session || run.Instance != child.Instance || run.Locator != l || run.ChildWait.Owner != child.ChildWait.Owner {
			continue
		}
		for id, request := range run.Requests {
			// Approval v3 owns its registered request lifecycle.
			if request.Approval != nil {
				continue
			}
			if hint := request.Interaction; hint != nil {
				r, err := interactionRead(s, hint.RequestID)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err == nil && r.Digest == hint.Digest && r.Locator == run.Locator {
					if err := cancelChildInteractionLocked(s, r); err != nil {
						return err
					}
				}
			}
			if err := settleChildRequestLocked(s, run, id, "interrupted"); err != nil {
				return err
			}
		}
		// Amortize crash recovery over normal flushes. Never hold up a full
		// send budget processing a backlog of expired local observations.
		if child == nil {
			break
		}
	}
	return nil
}

// The caller holds the Store lock. Persist the terminal receipt first so a
// repeated Post can never turn a cancelled mobile decision into confirmation.
func saveInteractionTransitionLocked(s Store, r InteractionRecord) error {
	if err := s.writeJSON("interaction-"+r.RequestID+".json", r); err != nil {
		return err
	}
	if r.ChildRunKey == "" {
		return nil
	}
	state := "interrupted"
	switch r.State {
	case "response_written":
		if r.Kind == "question" {
			return nil
		}
		state = "running"
	case "native_cancelled", "expired", "delivery_unknown":
	default:
		return nil
	}
	db, err := loadRuns(s)
	if err != nil {
		return err
	}
	run := db.Runs[r.ChildRunKey]
	if run.ChildWait == nil || run.Locator != r.Locator {
		return nil
	}
	for id, request := range run.Requests {
		if hint := request.Interaction; hint != nil && hint.RequestID == r.RequestID && hint.Digest == r.Digest {
			return settleChildRequestLocked(s, run, id, state)
		}
	}
	return nil
}
