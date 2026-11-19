package main

import (
	"context"
	"crypto/hmac"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
)

type replyWaitInput struct {
	Version  string `json:"runtime_version"`
	Session  string `json:"session_id"`
	Run      string `json:"run_id"`
	Event    string `json:"event_id"`
	Instance string `json:"instance_id"`
	Expires  int64  `json:"expires_at"`
}

func replyRegistration(s Store, agent string, f replyWaitInput, l Locator) (replyRecord, error) {
	now := time.Now().Unix()
	if f.Expires <= now || f.Expires > now+300 || !ordinaryReplyVersion(agent, f.Version) || l.validate() != nil || l.OccupantID == "" {
		return replyRecord{}, errors.New("reply_opportunity_unavailable")
	}
	for _, id := range []string{f.Session, f.Run, f.Event, f.Instance} {
		if id == "" || len(id) > 256 || strings.IndexFunc(id, unicode.IsControl) >= 0 {
			return replyRecord{}, errors.New("invalid_native_reply_identity")
		}
	}
	r := replyRecord{V: 1, Agent: agent, Session: f.Session, Run: f.Run, Instance: f.Instance, Locator: l, PID: os.Getpid(), ParentPID: replyParent(agent), Expires: f.Expires, State: "pending", OwnerToken: randomID(), Pairs: map[string]string{}}
	var err error
	if r.ParentBirth, err = processIdentity(r.ParentPID); err != nil {
		return r, err
	}
	if r.Birth, err = processIdentity(r.PID); err != nil {
		return r, err
	}
	sourceInstance := f.Instance
	switch agent {
	case "opencode", "pi":
		event := "agent_settled"
		if agent == "opencode" {
			event = "session.execution.succeeded"
		}
		r.Cursor = nativeTargetIdentity(newReplyOccurrence(agent, event, f.Session, f.Event, f.Instance), l)
	case "claude":
		var cycles map[string]claudeCycle
		if err = s.readJSON("claude-cycles.json", &cycles); err != nil {
			return r, err
		}
		c := cycles[digest(f.Session)]
		if c.Cycle == "" || c.PromptID != f.Event || f.Run != f.Event {
			return r, errors.New("reply_prompt_changed")
		}
		r.Cursor = claudeCompletionOccurrence(f.Session, c.Cycle)
		r.Run = c.Cycle
		// The classic notification carries session+cycle; the Mod also retains
		// its activation ID and verifies it inside its original host before I/O.
		sourceInstance = f.Session
	default:
		return r, errors.New("ordinary_reply_unavailable")
	}
	r.SourceID = replySourceID(agent, f.Session, r.Run, sourceInstance, r.Cursor, r.ParentBirth, l)
	m, err := localReplyActivations(s)
	if err != nil {
		return r, err
	}
	pairs, err := s.pairs()
	if err != nil {
		return r, err
	}
	for id, p := range pairs {
		a := m[id][agent]
		if p.Status == "active" && p.InstallationID == l.InstallationID && a.Generation > 0 && a.Token != "" {
			r.Pairs[id] = a.Token
		}
	}
	if len(r.Pairs) == 0 {
		return r, errors.New("reply_disabled")
	}
	return r, registerReplyLocked(s, r)
}

func replyCheckNative(ctx context.Context, s Store, api API, r replyRecord) (Pair, InteractionCapabilities, error) {
	var empty InteractionCapabilities
	p, err := replyPair(s, r.Frame.Pair)
	if err != nil {
		return p, empty, err
	}
	c, err := interactionCapabilities(ctx, s, api, p)
	if err != nil {
		return p, c, err
	}
	if reason := replyGrantReason(s, r, p, c); reason != "" {
		return p, c, errors.New(reason)
	}
	if c.PolicyGeneration != r.Frame.PolicyGeneration {
		return p, c, errors.New("reply_policy_changed")
	}
	if err = approvalTarget(ctx, s, r.Locator); err != nil {
		return p, c, err
	}
	return p, c, nil
}

// Once this returns text, the persisted decision is already dispatching. A
// killed helper or broken stdout cannot hand that text to another waiter.
func replyWait(ctx context.Context, s Store, api API, agent string, in io.Reader) (any, error) {
	raw, err := io.ReadAll(io.LimitReader(in, 8193))
	if err != nil || len(raw) > 8192 {
		return nil, errors.New("invalid_native_reply")
	}
	var f replyWaitInput
	if interactionJSON(raw, &f) != nil {
		return nil, errors.New("invalid_native_reply")
	}
	id, err := s.existingInstallation()
	if err != nil {
		return nil, err
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	r, err := replyRegistration(s, agent, f, l)
	unlock()
	if err != nil {
		return nil, err
	}
	defer finishUnclaimedReply(s, r)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Until(time.Unix(r.Expires, 0)))
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("opportunity_expired")
		case <-ticker.C:
		}
		current, err := readReply(s, r.SourceID)
		if err != nil {
			return nil, err
		}
		if !current.alive(true) {
			return nil, errors.New("native_session_unavailable")
		}
		if current.State == "pending" {
			continue
		}
		if current.State != "decision_recorded" || current.Frame == nil {
			return nil, errors.New("reply_unavailable")
		}
		p, c, err := replyCheckNative(ctx, s, api, current)
		if err != nil {
			return nil, err
		}
		unlock, err = s.lock(ctx)
		if err != nil {
			return nil, err
		}
		result, err := claimReplyLocked(s, current, p, c)
		unlock()
		return result, err
	}
}

func claimReplyLocked(s Store, expected replyRecord, p Pair, c InteractionCapabilities) (any, error) {
	r, err := readReply(s, expected.SourceID)
	if err != nil {
		return nil, err
	}
	if r.State != "decision_recorded" || r.Frame == nil || expected.Frame == nil || !sameReplyDecision(*r.Frame, *expected.Frame) || r.PID != os.Getpid() || r.OwnerToken != expected.OwnerToken || !interactionCurrentPair(s, p) || min(c.ExpiresAt, r.Frame.Expires) <= time.Now().Unix() {
		return nil, errors.New("reply_claim_conflict")
	}
	if reason := replyGrantReason(s, r, p, c); reason != "" {
		return nil, errors.New(reason)
	}
	if reason := replyOpportunityReason(s, r, true); reason != "" {
		return nil, errors.New(reason)
	}
	text := r.Frame.Text
	if strings.TrimSpace(text) == "" || digest(text) != r.Frame.ResponseDigest {
		return nil, errors.New("invalid_reply_text")
	}
	r.Frame.Text = ""
	r.State = "dispatching"
	if err = saveReply(s, r); err != nil {
		return nil, err
	}
	return map[string]any{"v": 1, "source_id": r.SourceID, "cursor": r.Cursor, "owner_token": r.OwnerToken, "decision_id": r.Frame.DecisionID, "text": text}, nil
}

func finishUnclaimedReply(s Store, expected replyRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	r, err := readReply(s, expected.SourceID)
	if err != nil || r.OwnerToken != expected.OwnerToken {
		return
	}
	switch r.State {
	case "pending":
		r.State = "expired"
	case "decision_recorded":
		r.State = "not_dispatched"
		r.Frame.Text = ""
	default:
		return
	}
	_ = saveReply(s, r)
}

type replyOwnerFrame struct {
	V        int    `json:"v"`
	Source   string `json:"source_id"`
	Owner    string `json:"owner_token"`
	Decision string `json:"decision_id"`
	State    string `json:"state,omitempty"`
}

func ownsReply(r replyRecord, f replyOwnerFrame) bool {
	return f.V == 1 && r.Frame != nil && r.ParentPID == replyParent(r.Agent) && r.alive(false) && f.Decision == r.Frame.DecisionID && hmac.Equal([]byte(r.OwnerToken), []byte(f.Owner))
}
func replyNativeCommand(ctx context.Context, s Store, api API, op string, in io.Reader) (any, error) {
	var f replyOwnerFrame
	if err := readInteractionFrame(ctx, in, &f); err != nil {
		return nil, err
	}
	r, err := readReply(s, f.Source)
	if err != nil {
		return nil, err
	}
	if !ownsReply(r, f) {
		return nil, errors.New("reply_owner_mismatch")
	}
	if op == "receipt" {
		unlock, err := s.lock(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		r, err = readReply(s, f.Source)
		if err != nil {
			return nil, err
		}
		if !ownsReply(r, f) {
			return nil, errors.New("reply_owner_mismatch")
		}
		if err = replyTransition(&r, f.State); err != nil {
			return nil, err
		}
		if err = saveReply(s, r); err != nil {
			return nil, err
		}
		return map[string]any{"v": 1, "state": r.State}, nil
	}
	if f.State != "" {
		return nil, errors.New("invalid_reply_authorization")
	}
	denied := map[string]any{"v": 1, "allowed": false}
	if r.State != "dispatching" || r.Authorized || replyOpportunityReason(s, r, false) != "" {
		return denied, nil
	}
	p, c, err := replyCheckNative(ctx, s, api, r)
	if err != nil {
		return denied, nil
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err = readReply(s, f.Source)
	if err != nil {
		return nil, err
	}
	if !ownsReply(r, f) || r.State != "dispatching" || r.Authorized || replyOpportunityReason(s, r, false) != "" || !interactionCurrentPair(s, p) || replyGrantReason(s, r, p, c) != "" || min(c.ExpiresAt, r.Frame.Expires) <= time.Now().Unix() {
		return denied, nil
	}
	r.Authorized = true
	if err = saveReply(s, r); err != nil {
		return nil, err
	}
	return map[string]any{"v": 1, "allowed": true}, nil
}

func replyTransition(r *replyRecord, to string) error {
	switch to {
	case "not_dispatched", "native_rejected", "delivery_unknown", "native_accepted", "native_consumed":
	default:
		return errors.New("invalid_reply_receipt")
	}
	if r.State == to {
		return nil
	}
	if r.State == "native_consumed" && (to == "native_accepted" || to == "delivery_unknown") || r.State == "native_accepted" && to == "delivery_unknown" {
		return nil
	}
	if r.State != "dispatching" && !(r.State == "delivery_unknown" && (to == "native_consumed" || to == "native_accepted")) && !(r.State == "native_accepted" && to == "native_consumed") {
		return errors.New("reply_receipt_conflict")
	}
	if (to == "native_accepted" || to == "native_consumed" || to == "native_rejected") && !r.Authorized {
		return errors.New("reply_not_authorized")
	}
	if to == "native_consumed" && r.Agent != "opencode" {
		return errors.New("reply_evidence_unavailable")
	}
	r.State = to
	return nil
}
