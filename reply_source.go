package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Kept only in the local intent/outbox. No native control identity is sent to the Worker.
type replyBinding struct {
	Agent        string `json:"agent"`
	SourceID     string `json:"source_id"`
	Cursor       string `json:"cursor"`
	Origin       string `json:"api_url"`
	Installation string `json:"installation_id"`
	Envelope     string `json:"envelope_digest,omitempty"`
}

type replyHead struct {
	Run      string `json:"run,omitempty"`
	Instance string `json:"instance,omitempty"`
	Cursor   string `json:"cursor"`
	Request  string `json:"request,omitempty"`
	Until    int64  `json:"until"`
}

// Classic Hooks may run below a shell while Mod process.spawn is a direct
// child. Resolve only Claude's own attested ancestor, never a PATH process.
func replyParent(agent string) int {
	if agent == "claude" {
		pid, e := strconv.Atoi(os.Getenv("CLAUDE_PID"))
		if e == nil && pid > 1 && pid != os.Getpid() && codexIsAncestor(pid) {
			return pid
		}
	}
	return os.Getppid()
}

func replyScope(agent, session string, l Locator) string {
	return nativeTargetIdentity(agent+"\x00"+session, l)
}

func replySourceID(agent, session, run, instance, cursor, owner string, l Locator) string {
	parts, _ := json.Marshal([]string{agent, session, run, instance, cursor, owner, replyScope(agent, session, l)})
	return digest(string(parts))
}

// Only native input/turn boundaries and actionable events advance the cursor.
// For an ambiguous or evicted old scope, a helper can never synthesize a head.
func advanceReplyHead(db *RunStore, a Activity, l Locator, now int64) bool {
	if a.ChildWait != nil || a.Agent == "codex" || l.OccupantID == "" || a.Occurrence == "" {
		return false
	}
	if a.Reminder == "" && a.State != "starting" && !(a.State == "running" && a.ToolID == "" && !a.ResolveRequest && a.ToolStatus == "") {
		return false
	}
	if db.ReplyHeads == nil {
		db.ReplyHeads = map[string]replyHead{}
	}
	scope := replyScope(a.Agent, a.Session, l)
	if len(db.ReplyHeads) >= 1024 {
		for key, head := range db.ReplyHeads {
			if head.Until <= now {
				delete(db.ReplyHeads, key)
			}
		}
		if _, ok := db.ReplyHeads[scope]; !ok && len(db.ReplyHeads) >= 1024 {
			return false
		}
	}
	request := ""
	if a.Interaction != nil {
		request = a.Interaction.RequestID
	}
	if a.Approval != nil {
		request = a.Approval.RequestID
	}
	// A late completion of an older run cannot replace a later input or turn.
	// The adapter checks its own activation epoch for inputs not yet in this ledger.
	old := db.ReplyHeads[scope]
	if a.Reminder == "turn_completed" && old.Run != "" && (old.Run != a.Run || old.Instance != a.Instance) {
		return false
	}
	db.ReplyHeads[scope] = replyHead{Run: a.Run, Instance: a.Instance, Cursor: a.Occurrence, Request: request, Until: now + 86400}
	return true
}

func replyIntentBinding(a Activity, l Locator, pair Pair, parentBirth string) *replyBinding {
	if a.Reminder != "turn_completed" || a.ChildWait != nil || l.OccupantID == "" {
		return nil
	}
	if a.Agent != "codex" && parentBirth == "" {
		return nil
	}
	return &replyBinding{Agent: a.Agent, SourceID: replySourceID(a.Agent, a.Session, a.Run, a.Instance, a.Occurrence, parentBirth, l), Cursor: a.Occurrence, Origin: strings.TrimSuffix(pair.APIURL, "/"), Installation: pair.InstallationID}
}
func bindReply(ref *replyBinding, e Envelope) *replyBinding {
	if ref == nil || e.Kind != "turn_completed" {
		return nil
	}
	r := *ref
	r.Envelope = envelopeBodyDigest(e)
	return &r
}

func newReplyOccurrence(agent, event, session, id, instance string) string {
	return digest(agent + "\x00" + event + "\x00" + session + "\x00" + id + "\x00" + instance)
}

func claudeCompletionOccurrence(session, cycle string) string {
	return digest("claude\x00" + digest(session) + "\x00" + cycle + "\x00completed")
}

type replyRecord struct {
	V           int               `json:"v"`
	SourceID    string            `json:"source_id"`
	Cursor      string            `json:"cursor"`
	Agent       string            `json:"agent"`
	Session     string            `json:"session_id"`
	Run         string            `json:"run_id"`
	Instance    string            `json:"instance_id"`
	Locator     Locator           `json:"locator"`
	PID         int               `json:"pid"`
	Birth       string            `json:"birth"`
	ParentPID   int               `json:"parent_pid"`
	ParentBirth string            `json:"parent_birth"`
	Pairs       map[string]string `json:"pairs"`
	OwnerToken  string            `json:"owner_token"`
	Expires     int64             `json:"expires_at"`
	State       string            `json:"state"`
	Authorized  bool              `json:"native_authorized,omitempty"`
	Frame       *replyFrame       `json:"frame,omitempty"`
}

func (r replyRecord) alive(wait bool) bool {
	parent, err := processIdentity(r.ParentPID)
	if err != nil || parent != r.ParentBirth {
		return false
	}
	if !wait {
		return true
	}
	child, err := processIdentity(r.PID)
	return err == nil && child == r.Birth
}
func replyFile(id string) string { return "reply-source-" + id + ".json" }
func readReply(s Store, id string) (replyRecord, error) {
	var r replyRecord
	if !bodyHashPattern.MatchString(id) {
		return r, errors.New("invalid_reply_source")
	}
	raw, mode, err := secureReadWithMode(filepath.Join(s.Dir, replyFile(id)), 128<<10)
	if err != nil {
		return r, err
	}
	if mode.Perm()&0077 != 0 || json.Unmarshal(raw, &r) != nil || r.V != 1 || r.SourceID != id || r.Expires <= 0 || !bodyHashPattern.MatchString(r.Cursor) || r.Locator.OccupantID == "" {
		return r, errors.New("invalid_reply_record")
	}
	switch r.State {
	case "pending", "expired":
		if r.Frame != nil {
			return r, errors.New("invalid_reply_record")
		}
	case "decision_recorded", "dispatching", "not_dispatched", "native_rejected", "delivery_unknown", "native_accepted", "native_consumed":
		if r.Frame == nil || r.Frame.SourceID != id || r.Frame.Cursor != r.Cursor || r.Frame.Agent != r.Agent || r.Frame.ResponseDigest == "" {
			return r, errors.New("invalid_reply_record")
		}
	default:
		return r, errors.New("invalid_reply_record")
	}
	return r, nil
}
func saveReply(s Store, r replyRecord) error { return s.writeJSON(replyFile(r.SourceID), r) }

func replyOpportunityReason(s Store, r replyRecord, waiting bool) string {
	if r.Expires <= time.Now().Unix() {
		return "opportunity_expired"
	}
	if !r.alive(false) {
		return "native_session_unavailable"
	}
	if waiting && !r.alive(true) {
		return "reply_channel_unavailable"
	}
	db, err := loadRuns(s)
	if err != nil {
		return "source_unconfirmed"
	}
	if db.ReplyHeads[replyScope(r.Agent, r.Session, r.Locator)].Cursor != r.Cursor {
		return "source_changed"
	}
	return ""
}

// A legacy caller retains its own waiter and release semantics. A known later
// ordinary source cannot be answered through an old native request ID.
func nativeRequestCurrent(s Store, agent, session, id string, l Locator) bool {
	if agent == "codex" || session == "" {
		return true
	}
	db, err := loadRuns(s)
	if err != nil {
		return false
	}
	h, ok := db.ReplyHeads[replyScope(agent, session, l)]
	return !ok || h.Request == id
}

func registerReplyLocked(s Store, r replyRecord) error {
	if _, err := readReply(s, r.SourceID); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("reply_already_registered")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	count, active := 0, 0
	now := time.Now().Unix()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "reply-source-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "reply-source-"), ".json")
		old, err := readReply(s, id)
		if err != nil {
			return err
		}
		if old.Expires+86400 < now && !old.alive(false) {
			if err = os.Remove(filepath.Join(s.Dir, name)); err != nil {
				return err
			}
			continue
		}
		if old.State == "decision_recorded" && (old.Expires <= now || !old.alive(true)) {
			old.State = "not_dispatched"
			old.Frame.Text = ""
			if err = saveReply(s, old); err != nil {
				return err
			}
		}
		count++
		if old.Expires > now && (old.State == "pending" || old.State == "decision_recorded") && old.alive(true) {
			active++
		}
	}
	if active >= 8 || count >= 1024 {
		return errors.New("reply_capacity")
	}
	return saveReply(s, r)
}

func interactionSourceCurrent(s Store, r InteractionRecord) bool {
	if r.ChildRunKey != "" || r.Agent == "codex" {
		return true
	}
	var spec InteractionSpec
	if r.RequestJSON == "" {
		return true
	} // Already-settled receipt is read-only.
	if interactionJSON([]byte(r.RequestJSON), &spec) != nil {
		return false
	}
	return nativeRequestCurrent(s, r.Agent, spec.SessionID, r.RequestID, r.Locator)
}
