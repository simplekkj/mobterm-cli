package main

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const replyPurpose = "mobterm/reply/1"
const replyTextLimit = 16 << 10

type replyFrame struct {
	bodyIdentity
	SourceID         string `json:"source_id,omitempty"`
	Cursor           string `json:"cursor,omitempty"`
	Agent            string `json:"agent,omitempty"`
	Action           string `json:"action,omitempty"`
	ResponseDigest   string `json:"response_digest,omitempty"`
	DecisionID       string `json:"decision_id,omitempty"`
	Text             string `json:"text,omitempty"`
	PolicyGeneration int64  `json:"policy_generation,omitempty"`
	Expires          int64  `json:"expires_at"`
	Proof            string `json:"proof,omitempty"`
}

func replyMAC(key []byte, op string, f replyFrame) (string, error) {
	return replyProof(key, f.Pair, []string{replyPurpose, op, f.Origin, f.Installation, f.Pair, f.Event, f.Envelope, f.Nonce, f.SourceID, f.Cursor, f.Agent, f.Action, f.ResponseDigest, f.DecisionID, strconv.FormatInt(f.PolicyGeneration, 10), strconv.FormatInt(f.Expires, 10)})
}

func replyProof(key []byte, pair string, fields []string) (string, error) {
	k, err := hkdf.Key(sha256.New, key, []byte(pair), replyPurpose, 32)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, k)
	for _, part := range fields {
		_ = binary.Write(m, binary.BigEndian, uint32(len(part)))
		_, _ = io.WriteString(m, part)
	}
	return base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}

type replyActivation struct {
	Revision   int64  `json:"revision"`
	Generation int64  `json:"policy_generation"`
	Token      string `json:"token"`
}
type replyActivations map[string]map[string]replyActivation

func localReplyActivations(s Store) (replyActivations, error) {
	m := replyActivations{}
	err := s.readJSON("reply-activations-v1.json", &m)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if m == nil {
		return nil, errors.New("invalid_reply_configuration")
	}
	return m, err
}
func removeReplyActivationLocked(s Store, pair, agent string) error {
	m, err := localReplyActivations(s)
	if err != nil {
		return err
	}
	if len(m[pair]) == 0 {
		return nil
	}
	if agent == "" {
		delete(m, pair)
	} else {
		a := m[pair][agent]
		a.Token = ""
		a.Revision++
		m[pair][agent] = a
	}
	return s.writeJSON("reply-activations-v1.json", m)
}

func replyPair(s Store, id string) (Pair, error) {
	pairs, err := s.pairs()
	if err != nil {
		return Pair{}, err
	}
	p, ok := pairs[id]
	if !ok || p.PairID != id || p.Status != "active" {
		return Pair{}, errors.New("pair_not_active")
	}
	installation, err := s.existingInstallation()
	if err != nil || p.InstallationID != installation {
		return Pair{}, errors.New("installation_changed")
	}
	var history []retiredPair
	if err = s.readJSON("pair-history.json", &history); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Pair{}, err
	}
	for _, h := range history {
		if h.PairRef == digest(id) {
			return Pair{}, errors.New("pair_not_active")
		}
	}
	return p, nil
}

type replyConfigFrame struct {
	V            int    `json:"v"`
	Agent        string `json:"agent"`
	Pair         string `json:"pair_id"`
	Enabled      *bool  `json:"enabled"`
	Revision     int    `json:"activation_revision"`
	Generation   int64  `json:"policy_generation"`
	Origin       string `json:"api_url"`
	Installation string `json:"installation_id"`
	Nonce        string `json:"nonce"`
	Expires      int64  `json:"expires_at"`
	Expected     *int64 `json:"expected_revision"`
	Proof        string `json:"proof"`
}

func (f replyConfigFrame) mac(key []byte) (string, error) {
	if f.Expected == nil || f.Enabled == nil {
		return "", errors.New("invalid_reply_configuration")
	}
	return replyProof(key, f.Pair, []string{replyPurpose, "configure", f.Origin, f.Installation, f.Pair, f.Agent, strconv.FormatBool(*f.Enabled), strconv.Itoa(f.Revision), strconv.FormatInt(f.Generation, 10), strconv.FormatInt(*f.Expected, 10), f.Nonce, strconv.FormatInt(f.Expires, 10)})
}

func replyConfiguration(ctx context.Context, s Store, api API, op string, in io.Reader) (any, error) {
	if op == "configuration" {
		var f struct {
			V    int    `json:"v"`
			Pair string `json:"pair_id"`
		}
		if err := readStrictFrame(ctx, in, &f, 128<<10); err != nil {
			return nil, err
		}
		if f.V != 1 || !opaqueID.MatchString(f.Pair) {
			return nil, errors.New("invalid_reply_configuration")
		}
		unlock, err := s.lockBodyRead(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		p, err := replyPair(s, f.Pair)
		if err != nil {
			return nil, err
		}
		m, err := localReplyActivations(s)
		if err != nil {
			return nil, err
		}
		values := map[string]int64{}
		revisions := map[string]int64{}
		for agent, a := range m[f.Pair] {
			revisions[agent] = a.Revision
			if a.Token != "" {
				values[agent] = a.Generation
			}
		}
		return map[string]any{"v": 1, "pair_id": f.Pair, "installation_id": p.InstallationID, "activations": values, "revisions": revisions}, nil
	}
	var f replyConfigFrame
	if err := readStrictFrame(ctx, in, &f, 128<<10); err != nil {
		return nil, err
	}
	if f.V != 1 || !opaqueID.MatchString(f.Pair) || f.Enabled == nil || f.Revision != 1 || f.Generation < 1 || !interactionAgent(f.Agent) || f.Agent == "codex" && *f.Enabled {
		return nil, errors.New("invalid_reply_configuration")
	}
	p, err := replyPair(s, f.Pair)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if f.Expected == nil || *f.Expected < 0 || f.Origin != strings.TrimSuffix(p.APIURL, "/") || f.Installation != p.InstallationID || !opaqueID.MatchString(f.Nonce) || f.Expires <= now || f.Expires > now+60 {
		return nil, errors.New("invalid_reply_configuration")
	}
	key, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return nil, err
	}
	proof, err := f.mac(key)
	if err != nil || !hmac.Equal([]byte(proof), []byte(f.Proof)) {
		return nil, errors.New("reply_proof_mismatch")
	}
	if *f.Enabled {
		grant, err := interactionCapabilities(ctx, s, api, p)
		if err != nil {
			return nil, err
		}
		if !grant.Enabled || grant.PolicyGeneration != f.Generation || !grant.Interactions[f.Agent]["followupMessage"] {
			return nil, errors.New("reply_policy_unconfirmed")
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := replyPair(s, f.Pair)
	if err != nil || current != p {
		return nil, errors.New("pair_changed")
	}
	m, err := localReplyActivations(s)
	if err != nil {
		return nil, err
	}
	if m[f.Pair] == nil {
		m[f.Pair] = map[string]replyActivation{}
	}
	a := m[f.Pair][f.Agent]
	if a.Generation > f.Generation {
		return nil, errors.New("reply_policy_changed")
	}
	if *f.Expected != a.Revision || f.Expires <= time.Now().Unix() {
		return nil, errors.New("reply_configuration_changed")
	}
	if !*f.Enabled {
		a.Token = ""
	} else if a.Token == "" || a.Generation != f.Generation {
		a.Token = randomID()
	}
	a.Revision++
	a.Generation = f.Generation
	m[f.Pair][f.Agent] = a
	if err = s.writeJSON("reply-activations-v1.json", m); err != nil {
		return nil, err
	}
	return map[string]any{"v": 1, "pair_id": p.PairID, "installation_id": p.InstallationID, "agent": f.Agent, "activation_revision": 1, "policy_generation": f.Generation, "enabled": *f.Enabled, "api_url": f.Origin, "nonce": f.Nonce, "configuration_revision": a.Revision, "configuration_saved": true, "loaded": "unknown"}, nil
}

func sameReplyDecision(a, b replyFrame) bool {
	return a.Origin == b.Origin && a.Installation == b.Installation && a.Pair == b.Pair && a.Event == b.Event && a.Envelope == b.Envelope && a.SourceID == b.SourceID && a.Cursor == b.Cursor && a.Agent == b.Agent && a.PolicyGeneration == b.PolicyGeneration && a.Action == b.Action && a.DecisionID == b.DecisionID && a.ResponseDigest == b.ResponseDigest
}

func verifyReply(op string, f replyFrame, p Pair) error {
	now := time.Now().Unix()
	if (RetirePacket{f.V, f.Origin, f.Pair, f.Installation}).validate() != nil || f.Origin != strings.TrimSuffix(p.APIURL, "/") || strings.HasSuffix(f.Origin, "/") || f.Installation != p.InstallationID || f.Pair != p.PairID || !opaqueID.MatchString(f.Event) || !bodyHashPattern.MatchString(f.Envelope) || !opaqueID.MatchString(f.Nonce) || f.Expires <= now || f.Expires > now+60 {
		return errors.New("invalid_reply_identity")
	}
	if op == "inspect" || op == "latest" {
		if f.SourceID != "" || f.Agent != "" || f.Cursor != "" || f.Action != "" || f.ResponseDigest != "" || f.DecisionID != "" || f.Text != "" || f.PolicyGeneration != 0 {
			return errors.New("invalid_reply_inspect")
		}
	} else {
		if !bodyHashPattern.MatchString(f.SourceID) || !bodyHashPattern.MatchString(f.Cursor) || !interactionAgent(f.Agent) || f.Action != "send" || !opaqueID.MatchString(f.DecisionID) || !bodyHashPattern.MatchString(f.ResponseDigest) || f.PolicyGeneration < 1 {
			return errors.New("invalid_reply_decision")
		}
		if op == "send" && (len(f.Text) > replyTextLimit || strings.TrimSpace(f.Text) == "" || digest(f.Text) != f.ResponseDigest) || op == "status" && f.Text != "" {
			return errors.New("invalid_reply_text")
		}
	}
	key, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return err
	}
	expect, err := replyMAC(key, op, f)
	if err != nil || !hmac.Equal([]byte(expect), []byte(f.Proof)) {
		return errors.New("reply_proof_mismatch")
	}
	return nil
}

type replyResult struct {
	bodyIdentity
	Locator            *Locator `json:"locator,omitempty"`
	SourceID           string   `json:"source_id,omitempty"`
	Cursor             string   `json:"cursor,omitempty"`
	Agent              string   `json:"agent,omitempty"`
	State              string   `json:"state"`
	Reason             string   `json:"reason,omitempty"`
	Expires            int64    `json:"expires_at"`
	OpportunityExpires int64    `json:"opportunity_expires_at,omitempty"`
	Generation         int64    `json:"policy_generation"`
	Decision           string   `json:"decision_id,omitempty"`
	Action             string   `json:"action,omitempty"`
	Digest             string   `json:"response_digest,omitempty"`
}

func replyReceipt(result replyResult, r replyRecord, f replyFrame, op string) (replyResult, error) {
	if r.Frame.Pair != f.Pair {
		result.State = "other_decision"
		return result, nil
	}
	if op != "inspect" && !sameReplyDecision(*r.Frame, f) {
		return result, errors.New("reply_decision_conflict")
	}
	result.Decision = r.Frame.DecisionID
	result.Digest = r.Frame.ResponseDigest
	result.Action = r.Frame.Action
	result.Generation = r.Frame.PolicyGeneration
	result.State = r.State
	if r.State == "dispatching" && (!r.alive(false) || time.Now().Unix() >= r.Expires) {
		result.State = "delivery_unknown"
	}
	return result, nil
}

// Replay protection for a decision ID also covers attempts to move it to a different source.
func unusedReplyDecision(s Store, id, source string) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "reply-source-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, "reply-source-"), ".json")
		if key == source {
			continue
		}
		r, err := readReply(s, key)
		if err != nil {
			return err
		}
		if r.Frame != nil && r.Frame.DecisionID == id {
			return errors.New("reply_decision_conflict")
		}
	}
	return nil
}

func replyGrantReason(s Store, r replyRecord, p Pair, grant InteractionCapabilities) string {
	m, err := localReplyActivations(s)
	if err != nil {
		return "policy_unconfirmed"
	}
	a := m[p.PairID][r.Agent]
	if !grant.Enabled || !grant.Interactions[r.Agent]["followupMessage"] {
		return "reply_disabled"
	}
	if a.Generation != grant.PolicyGeneration || a.Token == "" {
		return "activation_required"
	}
	if r.Pairs[p.PairID] != a.Token {
		return "activation_changed"
	}
	return ""
}

func checkRemoteReplyPair(ctx context.Context, s Store, api API, p Pair) error {
	remote, err := api.pairQuery(ctx, s, p, "GET", "/v1/pairs/"+p.PairID, nil)
	if err != nil {
		return err
	}
	if remote["status"] != "active" || remote["pair_id"] != p.PairID || remote["installation_id"] != p.InstallationID || !interactionCurrentPair(s, p) {
		return errors.New("pair_not_active")
	}
	return nil
}

// The exact winning tuple is itself durable proof of the original participant,
// including its envelope digest. Its receipt survives routine outbox cleanup.
func replyStoredStatus(ctx context.Context, s Store, api API, p Pair, f replyFrame) (replyResult, bool, error) {
	r, err := readReply(s, f.SourceID)
	if errors.Is(err, os.ErrNotExist) {
		return replyResult{}, false, nil
	}
	if err != nil {
		return replyResult{}, false, err
	}
	if r.Frame == nil || !sameReplyDecision(*r.Frame, f) {
		return replyResult{}, false, nil
	}
	if err = checkRemoteReplyPair(ctx, s, api, p); err != nil {
		return replyResult{}, false, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return replyResult{}, false, err
	}
	defer unlock()
	r, err = readReply(s, f.SourceID)
	if err != nil {
		return replyResult{}, false, err
	}
	if r.Frame == nil || !sameReplyDecision(*r.Frame, f) || !interactionCurrentPair(s, p) {
		return replyResult{}, false, errors.New("reply_decision_conflict")
	}
	if r.State == "decision_recorded" && (r.Expires <= time.Now().Unix() || !r.alive(true)) {
		r.State = "not_dispatched"
		r.Frame.Text = ""
		if err = saveReply(s, r); err != nil {
			return replyResult{}, false, err
		}
	}
	result := replyResult{bodyIdentity: f.bodyIdentity, SourceID: r.SourceID, Cursor: r.Cursor, Agent: r.Agent, Generation: r.Frame.PolicyGeneration, OpportunityExpires: r.Expires, Expires: r.Expires}
	result, err = replyReceipt(result, r, f, "status")
	return result, true, err
}

func replyCommand(ctx context.Context, s Store, api API, op string, in io.Reader) (any, error) {
	switch op {
	case "configure", "configuration":
		return replyConfiguration(ctx, s, api, op, in)
	case "navigation-observe", "navigation-confirm":
		return observeNavigation(ctx, s, op, in)
	case "host":
		return registerNativeHost(ctx, s, in)
	case "rebind":
		return rebindReply(ctx, s, api, in)
	case "reopen":
		return reopenReply(ctx, s, api, in)
	case "authorize", "receipt":
		return replyNativeCommand(ctx, s, api, op, in)
	case "inspect", "send", "status", "latest":
	default:
		return nil, errors.New("invalid_reply_command")
	}
	var f replyFrame
	if err := readStrictFrame(ctx, in, &f, 128<<10); err != nil {
		return nil, err
	}
	p, err := replyPair(s, f.Pair)
	if err != nil {
		return nil, err
	}
	if err = verifyReply(op, f, p); err != nil {
		return nil, err
	}
	if op == "status" {
		if saved, found, e := replyStoredStatus(ctx, s, api, p, f); found || e != nil {
			return saved, e
		}
	}
	result := replyResult{bodyIdentity: f.bodyIdentity, State: "unavailable", Reason: "source_binding_missing"}
	// A read joins the outbox writer lock to authenticate the exact original envelope.
	unlock, err := s.lockBodyRead(ctx)
	if err != nil {
		return nil, err
	}
	if err = checkBodyDir(filepath.Join(s.Dir, "outbox-v1")); err != nil {
		unlock()
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return nil, err
	}
	k := deliveryKey(f.Pair, f.Event)
	sh, err := s.loadShard(k)
	unlock()
	if err != nil {
		return nil, err
	}
	d, ok := sh.Records[k]
	if op == "latest" {
		if ok && d.PairID == p.PairID && d.EventID == f.Event && visibleSourceCurrent(s, f.bodyIdentity, d.VisibleSession) {
			if err := checkRemoteReplyPair(ctx, s, api, p); err != nil {
				return nil, err
			}
			target, bindingErr := confirmNavigation(ctx, s, p, d, f.Expires)
			if bindingErr != nil {
				result.Reason = "navigation_binding_unconfirmed"
				return result, nil
			}
			local, pairErr := replyPair(s, p.PairID)
			if pairErr == nil && local == p && visibleSourceCurrent(s, f.bodyIdentity, d.VisibleSession) {
				result.Locator = &target
				result.State, result.Reason, result.Expires = "current", "", min(f.Expires, time.Now().Unix()+5)
			}
		}
		return result, nil
	}
	if !ok || d.Reply == nil {
		return result, nil
	}
	b := d.Reply
	if b.Envelope != f.Envelope || b.Origin != f.Origin || b.Installation != f.Installation || d.PairID != f.Pair || d.EventID != f.Event {
		return nil, errors.New("reply_binding_mismatch")
	}
	result.SourceID, result.Cursor, result.Agent = b.SourceID, b.Cursor, b.Agent
	if op != "inspect" && (b.SourceID != f.SourceID || b.Cursor != f.Cursor || b.Agent != f.Agent) {
		return nil, errors.New("reply_source_mismatch")
	}
	if b.Agent == "codex" {
		result.Reason = "ordinary_reply_unavailable"
		return result, nil
	}
	r, err := readReply(s, b.SourceID)
	if errors.Is(err, os.ErrNotExist) {
		result.Reason = "native_adapter_unavailable"
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.OpportunityExpires = r.Expires
	result.Expires = r.Expires
	result.Reason = ""
	if r.Agent != b.Agent || r.Cursor != b.Cursor || r.Locator.InstallationID != f.Installation {
		return nil, errors.New("reply_binding_mismatch")
	}
	if r.Frame != nil || op == "status" {
		// The participant may read its responsibility after grant expiry, revocation
		// of the action, and native exit. A revoked pairing loses read authority.
		if err := checkRemoteReplyPair(ctx, s, api, p); err != nil {
			return nil, err
		}
		if r.Frame != nil {
			return replyReceipt(result, r, f, op)
		}
	}
	if op == "status" {
		result.Reason = "decision_unconfirmed"
		return result, nil
	}
	grant, err := interactionCapabilities(ctx, s, api, p)
	if err != nil {
		return nil, err
	}
	result.Generation = grant.PolicyGeneration
	result.Expires = min(grant.ExpiresAt, r.Expires)
	if reason := replyGrantReason(s, r, p, grant); reason != "" {
		result.Reason = reason
		return result, nil
	}
	if !visibleSourceCurrent(s, f.bodyIdentity, d.VisibleSession) {
		result.Reason = "source_changed"
		return result, nil
	}
	if r.Renewable && r.Frame == nil && (r.State == "expired" || !r.alive(true) || r.Expires <= time.Now().Unix()) {
		r, err = wakeReply(ctx, s, r, min(f.Expires, grant.ExpiresAt))
		if err != nil {
			result.Reason = safeCode(err.Error())
			return result, nil
		}
		result.OpportunityExpires = r.Expires
		result.Expires = min(grant.ExpiresAt, r.Expires)
	}
	if reason := replyOpportunityReason(s, r, true); reason != "" {
		result.Reason = reason
		return result, nil
	}
	if !r.Renewable {
		if err = approvalTarget(ctx, s, r.Locator); err != nil {
			result.Reason = "target_unconfirmed"
			return result, nil
		}
	}
	unlock, err = s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err = readReply(s, b.SourceID)
	if err != nil {
		return nil, err
	}
	if r.Frame != nil {
		return replyReceipt(result, r, f, op)
	}
	current, err := replyPair(s, p.PairID)
	if err != nil || current != p {
		return nil, errors.New("pair_changed")
	}
	if reason := replyGrantReason(s, r, p, grant); reason != "" {
		result.Reason = reason
		return result, nil
	}
	if !visibleSourceCurrent(s, f.bodyIdentity, d.VisibleSession) {
		result.Reason = "source_changed"
		return result, nil
	}
	if reason := replyOpportunityReason(s, r, true); reason != "" {
		result.Reason = reason
		return result, nil
	}
	if r.State != "pending" || min(f.Expires, result.Expires) <= time.Now().Unix() {
		result.Reason = "opportunity_expired"
		return result, nil
	}
	if op == "inspect" {
		result.State = "eligible"
		return result, nil
	}
	if f.PolicyGeneration != grant.PolicyGeneration || f.Expires > result.Expires {
		return nil, errors.New("reply_policy_changed")
	}
	if err = unusedReplyDecision(s, f.DecisionID, r.SourceID); err != nil {
		return nil, err
	}
	r.State = "decision_recorded"
	f.Proof = ""
	r.VisibleSession = d.VisibleSession
	r.Frame = &f
	if err = saveReply(s, r); err != nil {
		return nil, err
	}
	return replyReceipt(result, r, f, op)
}
