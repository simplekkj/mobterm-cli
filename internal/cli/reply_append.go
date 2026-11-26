package cli

import (
	"context"
	"crypto/hmac"
	"errors"
	"strings"
	"time"
)

func appendConfiguration(s Store, p Pair, m replyActivations) (any, error) {
	a := m[p.PairID][appendScope]
	values := map[string]int64{}
	if a.Token != "" {
		values["codex"] = a.Generation
	}
	async, err := localInteractions(s)
	if err != nil {
		return nil, err
	}
	return map[string]any{"v": 2, "mode": appendMode, "pair_id": p.PairID, "installation_id": p.InstallationID, "activations": values, "revisions": map[string]int64{"codex": a.Revision}, "async_enabled": async[p.PairID]["codex"]["followupMessage"] > 0}, nil
}

func verifyAppend(op string, f replyFrame, p Pair) error {
	now := time.Now().Unix()
	if f.V != 2 || f.Mode != appendMode || f.Agent != "codex" || f.Cursor != "" || (RetirePacket{1, f.Origin, f.Pair, f.Installation}).validate() != nil || f.Origin != strings.TrimSuffix(p.APIURL, "/") || f.Installation != p.InstallationID || f.Pair != p.PairID || !opaqueID.MatchString(f.Event) || !bodyHashPattern.MatchString(f.Envelope) || !opaqueID.MatchString(f.Nonce) || f.Expires <= now || f.Expires > now+60 {
		return errors.New("invalid_reply_identity")
	}
	switch op {
	case "inspect":
		if f.SourceID != "" || f.Lease != "" || f.Action != "" || f.DecisionID != "" || f.ClientID != "" || f.ResponseDigest != "" || f.Text != "" || f.ActivationRevision != 0 || f.PolicyGeneration != 0 {
			return errors.New("invalid_reply_inspect")
		}
	case "send", "status":
		if !bodyHashPattern.MatchString(f.SourceID) || !bodyHashPattern.MatchString(f.Lease) || f.Action != "send" || !opaqueID.MatchString(f.DecisionID) || !opaqueID.MatchString(f.ClientID) || !bodyHashPattern.MatchString(f.ResponseDigest) || f.ActivationRevision < 1 || f.PolicyGeneration < 1 {
			return errors.New("invalid_reply_decision")
		}
		if op == "status" && f.Text != "" || op == "send" && (strings.TrimSpace(f.Text) == "" || len(f.Text) > replyTextLimit || digest(f.Text) != f.ResponseDigest) {
			return errors.New("invalid_reply_text")
		}
	default:
		return errors.New("invalid_reply_command")
	}
	key, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return err
	}
	proof, err := replyMAC(key, op, f)
	if err != nil || !hmac.Equal([]byte(proof), []byte(f.Proof)) {
		return errors.New("reply_proof_mismatch")
	}
	return nil
}

func sameAppendDecision(a, b replyFrame) bool {
	return sameReplyDecision(a, b) && a.V == 2 && a.V == b.V && a.Mode == b.Mode && a.Lease == b.Lease && a.ActivationRevision == b.ActivationRevision && a.ClientID == b.ClientID
}

func appendReceipt(result replyResult, r *appendSource, f replyFrame, op string) (replyResult, error) {
	d := r.Decision
	if d.Pair != f.Pair || op == "inspect" {
		result.State, result.Reason = "other_decision", ""
		return result, nil
	}
	if !sameAppendDecision(*d, f) {
		return result, errors.New("reply_decision_conflict")
	}
	result.State, result.Reason = r.State, r.Reason
	result.Decision, result.ClientID, result.Digest = d.DecisionID, d.ClientID, d.ResponseDigest
	result.Action, result.Generation, result.ActivationRevision, result.Lease = d.Action, d.PolicyGeneration, d.ActivationRevision, d.Lease
	result.QueueID, result.AckConfirmed = r.QueueID, r.Ack
	if result.State == "dispatching" {
		result.State = "delivery_unknown"
	}
	return result, nil
}

func appendAllowed(s Store, p Pair, g InteractionCapabilities) (replyActivation, string) {
	m, err := localReplyActivations(s)
	if err != nil {
		return replyActivation{}, "policy_unconfirmed"
	}
	a := m[p.PairID][appendScope]
	if !g.Enabled || !g.Interactions["codex"]["followupMessage"] {
		return a, "reply_disabled"
	}
	if a.Token == "" || a.Generation != g.PolicyGeneration {
		return a, "activation_required"
	}
	return a, ""
}

func appendUnavailable(result replyResult, op string, f replyFrame, reason string) replyResult {
	result.State, result.Reason = "unavailable", reason
	if op == "send" {
		// Another SSH exec may already have crossed the claim boundary for
		// these same bytes while this one was checking authorization or the
		// daemon. Only a durable winning claim can issue not_dispatched.
		result.State = "delivery_unknown"
		result.Decision, result.ClientID, result.Digest, result.Action = f.DecisionID, f.ClientID, f.ResponseDigest, f.Action
		result.Generation, result.ActivationRevision, result.Lease = f.PolicyGeneration, f.ActivationRevision, f.Lease
	}
	return result
}

func appendCommand(ctx context.Context, s Store, api API, p Pair, op string, f replyFrame) (replyResult, error) {
	result := replyResult{bodyIdentity: f.bodyIdentity, Mode: appendMode, Agent: "codex", State: "unavailable", Reason: "source_binding_missing"}
	if err := verifyAppend(op, f, p); err != nil {
		return result, err
	}
	db, err := loadAppendStore(s)
	if err != nil {
		return result, err
	}
	id, r := db.source(f)
	if r == nil {
		return result, nil
	}
	result.SourceID = id
	result.OpportunityExpires = r.Created + appendSourceTTL
	if r.Decision != nil || op == "status" {
		if err = checkRemoteReplyPair(ctx, s, api, p); err != nil {
			return result, err
		}
		if r.Decision == nil {
			result.Reason = "decision_unconfirmed"
			return result, nil
		}
		if op != "status" || r.Decision.Pair != p.PairID {
			return appendReceipt(result, r, f, op)
		}
		if !sameAppendDecision(*r.Decision, f) {
			return result, errors.New("reply_decision_conflict")
		}
		return appendStatus(ctx, s, p, r, f, result)
	}
	// Validate the remote Pair before disclosing even an expired source's
	// immutable identity and deadline.
	grant, err := interactionCapabilities(ctx, s, api, p)
	if err != nil {
		return result, err
	}
	if result.OpportunityExpires <= time.Now().Unix() {
		return appendUnavailable(result, op, f, "source_expired"), nil
	}
	if _, why := appendAllowed(s, p, grant); why != "" {
		return appendUnavailable(result, op, f, why), nil
	}
	client, threadState, err := openAppendNative(ctx, r.Runtime, r.Thread, true)
	if err != nil {
		return appendUnavailable(result, op, f, safeCode(err.Error())), nil
	}
	defer client.conn.Close()
	result.ThreadState = threadState
	if op == "send" {
		grant, err = interactionCapabilities(ctx, s, api, p)
		if err != nil {
			return result, err
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return result, err
	}
	// Re-read both the immutable delivery and current activation after network I/O.
	if err = client.checkAppendOwner(ctx, r.Runtime); err != nil {
		unlock()
		return appendUnavailable(result, op, f, "codex_daemon_owner_changed"), nil
	}
	result, claimed, err := prepareAppend(s, p, op, f, grant, r.Runtime, result)
	unlock()
	if err != nil || !claimed {
		return result, err
	}
	// The durable dispatching boundary has been passed. Never retry this RPC.
	dispatchCtx, cancel := context.WithDeadline(ctx, time.Unix(min(f.Expires, grant.ExpiresAt, result.OpportunityExpires), 0))
	defer cancel()
	state, qid, reason := client.appendOnce(dispatchCtx, r.Thread, f.ClientID, f.Text)
	return updateAppendEvidence(ctx, s, p, f, result, state, qid, reason, state == "queued")
}

func prepareAppend(s Store, p Pair, op string, f replyFrame, g InteractionCapabilities, verified codexRuntimeSource, result replyResult) (replyResult, bool, error) {
	db, err := loadAppendStore(s)
	if err != nil {
		return result, false, err
	}
	id, r := db.source(f)
	current, err := replyPair(s, p.PairID)
	if err != nil || current != p || r == nil || r.Runtime != verified || id != result.SourceID {
		return result, false, errors.New("pair_or_source_changed")
	}
	if r.Decision != nil {
		v, err := appendReceipt(result, r, f, op)
		return v, false, err
	}
	a, reason := appendAllowed(s, p, g)
	if reason != "" {
		return appendUnavailable(result, op, f, reason), false, nil
	}
	now := time.Now().Unix()
	result.Generation, result.ActivationRevision = g.PolicyGeneration, a.Revision
	expires := min(g.ExpiresAt, f.Expires, r.Created+appendSourceTTL)
	if expires <= now {
		return appendUnavailable(result, op, f, "opportunity_expired"), false, nil
	}
	b := r.Participants[f.Pair]
	if op == "inspect" {
		// Repeated inspections may reuse a still-current lease. Issue a fresh one
		// near expiry so a user returning later can send from this same source.
		if b.Lease == "" || b.Revision != a.Revision || b.Generation != g.PolicyGeneration || b.Expires <= now+5 {
			b.Lease = digest(randomID())
			b.Revision, b.Generation, b.Expires = a.Revision, g.PolicyGeneration, expires
			r.Participants[f.Pair] = b
			if err = db.save(s); err != nil {
				return result, false, err
			}
		}
		result.State, result.Reason, result.Lease, result.Expires = "eligible", "", b.Lease, min(expires, b.Expires)
		return result, false, nil
	}
	if f.Lease != b.Lease || f.ActivationRevision != a.Revision || b.Revision != a.Revision || f.PolicyGeneration != g.PolicyGeneration || b.Generation != g.PolicyGeneration || b.Expires < f.Expires || b.Expires <= now {
		return appendUnavailable(result, op, f, "lease_expired"), false, nil
	}
	if err = db.unusedDecision(f.DecisionID, f.ClientID); err != nil {
		return result, false, err
	}
	if err = unusedReplyDecision(s, f.DecisionID, id); err != nil {
		return result, false, err
	}
	f.Text, f.Proof = "", ""
	r.Decision, r.State, r.ClaimedAt = &f, "dispatching", now
	if err = db.save(s); err != nil {
		return result, false, err
	}
	result, err = appendReceipt(result, r, f, op)
	return result, true, err
}

// Reading a decision needs the current paired identity but no fresh send lease.
// Preserve prior ACK/admission when later observation has no matching evidence.
func appendStatus(ctx context.Context, s Store, p Pair, r *appendSource, f replyFrame, result replyResult) (replyResult, error) {
	if r.State == "not_dispatched" || r.State == "admitted" {
		return appendReceipt(result, r, f, "status")
	}
	state, qid := "", ""
	client, threadState, err := openAppendNative(ctx, r.Runtime, r.Thread, false)
	if err == nil {
		defer client.conn.Close()
		result.ThreadState = threadState
		state, qid = client.appendEvidence(ctx, r.Thread, f.ClientID, r.QueueID, f.ResponseDigest)
	}
	return updateAppendEvidence(ctx, s, p, f, result, state, qid, "", false)
}

func updateAppendEvidence(ctx context.Context, s Store, p Pair, f replyFrame, result replyResult, state, qid, reason string, ack bool) (replyResult, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return result, err
	}
	defer unlock()
	db, err := loadAppendStore(s)
	if err != nil {
		return result, err
	}
	_, r := db.source(f)
	current, err := replyPair(s, p.PairID)
	if err != nil || current != p || r == nil || r.Decision == nil || !sameAppendDecision(*r.Decision, f) {
		return result, errors.New("pair_or_decision_changed")
	}
	priorState, priorReason, priorQueue, priorAck := r.State, r.Reason, r.QueueID, r.Ack
	if qid != "" && r.QueueID != "" && qid != r.QueueID {
		state, qid, reason, ack = "delivery_unknown", "", "queue_identity_unconfirmed", false
	}
	if qid != "" {
		r.QueueID = qid
	}
	r.Ack = r.Ack || ack
	switch {
	case r.State == "admitted" || r.State == "not_dispatched":
	case state == "queued" && (r.State == "waiting" || r.State == "content_changed"):
	case state == "", state == "delivery_unknown":
		switch r.State {
		case "dispatching", "delivery_unknown":
			r.State = "delivery_unknown"
		case "waiting":
			r.State = "queued"
		}
		r.Reason = "current_state_unconfirmed"
	case state == "admitted" || state == "queued" || state == "waiting" || state == "content_changed" || state == "not_dispatched":
		r.State, r.Reason = state, reason
	default:
		return result, errors.New("invalid_append_evidence")
	}
	if r.State != priorState || r.Reason != priorReason || r.QueueID != priorQueue || r.Ack != priorAck {
		if err = db.save(s); err != nil {
			return result, err
		}
	}
	return appendReceipt(result, r, f, "status")
}
