package cli

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
	PermissionGroup string `json:"permission_group,omitempty"`
	LegacyScope     string `json:"legacy_scope,omitempty"`
	SessionHint     string `json:"session_hint,omitempty"`
	Run             string `json:"run,omitempty"`
	Instance        string `json:"instance,omitempty"`
	Cursor          string `json:"cursor"`
	Request         string `json:"request,omitempty"`
	Until           int64  `json:"until"`
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

// The scope is captured in the Agent process environment before persisting a
// source. A later SSH command may use an entirely different HOME or profile.
func replyScope(installation, session string) string {
	return digest(installation + "\x00" + session)
}

func replySourceID(scope, run, instance, cursor, owner string) string {
	parts, _ := json.Marshal([]string{scope, run, instance, cursor, owner})
	return digest(string(parts))
}

// Only native input/turn boundaries and actionable events advance the cursor.
// For an ambiguous or evicted old scope, a helper can never synthesize a head.
func advanceReplyHead(db *RunStore, a Activity, scope string, now int64) bool {
	if a.ChildWait != nil || a.Agent == "codex" || scope == "" || a.Occurrence == "" {
		return false
	}
	if a.Reminder == "" && a.State != "starting" && !(a.State == "running" && a.ToolID == "" && !a.ResolveRequest && a.ToolStatus == "") {
		return false
	}
	if db.ReplyHeads == nil {
		db.ReplyHeads = map[string]replyHead{}
	}
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
	// A different pending permission can arrive between a user's signed decision
	// and the native reject. Remember only contiguous permission observations
	// in the same activation/turn. Ordinary input and completion break the group.
	var permissionGroup string
	if a.Agent == "opencode" && a.Reminder == "needs_input" && a.State == "waiting_approval" {
		permissionGroup = a.Occurrence
		if old.Run == a.Run && old.Instance == a.Instance && old.PermissionGroup != "" {
			permissionGroup = old.PermissionGroup
		}
	}
	db.ReplyHeads[scope] = replyHead{PermissionGroup: permissionGroup, SessionHint: digest(a.Agent + "\x00" + a.Session), Run: a.Run, Instance: a.Instance, Cursor: a.Occurrence, Request: request, Until: now + 86400}
	return true
}

func replyIntentBinding(a Activity, l Locator, pair Pair, parentBirth string) *replyBinding {
	if a.Reminder != "turn_completed" || a.ChildWait != nil || l.OccupantID == "" {
		return nil
	}
	if a.Agent != "codex" && parentBirth == "" {
		return nil
	}
	if err := a.identifySession(); err != nil {
		return nil
	}
	logical := a.LogicalSession
	return &replyBinding{Agent: a.Agent, SourceID: replySourceID(replyScope(l.InstallationID, logical), a.Run, a.Instance, a.Occurrence, parentBirth), Cursor: a.Occurrence, Origin: strings.TrimSuffix(pair.APIURL, "/"), Installation: pair.InstallationID}
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
	NativeStorage  string            `json:"native_storage,omitempty"`
	NativeTurn     string            `json:"native_turn,omitempty"`
	LogicalSession string            `json:"logical_session,omitempty"`
	Renewable      bool              `json:"renewable,omitempty"`
	VisibleSession string            `json:"visible_session,omitempty"`
	V              int               `json:"v"`
	SourceID       string            `json:"source_id"`
	Cursor         string            `json:"cursor"`
	Agent          string            `json:"agent"`
	Session        string            `json:"session_id"`
	Run            string            `json:"run_id"`
	Instance       string            `json:"instance_id"`
	Locator        Locator           `json:"locator"`
	PID            int               `json:"pid"`
	Birth          string            `json:"birth"`
	ParentPID      int               `json:"parent_pid"`
	ParentBirth    string            `json:"parent_birth"`
	Pairs          map[string]string `json:"pairs"`
	OwnerToken     string            `json:"owner_token"`
	Expires        int64             `json:"expires_at"`
	State          string            `json:"state"`
	Authorized     bool              `json:"native_authorized,omitempty"`
	Frame          *replyFrame       `json:"frame,omitempty"`
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
	if r.Frame != nil && !visibleSourceCurrent(s, r.Frame.bodyIdentity, r.VisibleSession) {
		return "source_changed"
	}
	if r.Expires <= time.Now().Unix() {
		return "opportunity_expired"
	}
	if !r.alive(false) {
		return "native_session_unavailable"
	}
	if waiting && !r.alive(true) {
		return "reply_channel_unavailable"
	}
	if r.NativeStorage != "" && !nativeReplyOwnerCurrent(s, r) {
		return "native_session_unavailable"
	}
	return replyCursorReason(s, r)
}

// A configuration-only source can no longer prove which physical file owns
// it once this installation has observed a qualified cursor for that session.
func (db *RunStore) retiredReplyScope(scope string) bool {
	if db.ReplyHeads[scope].LegacyScope != "" {
		return false
	}
	for _, head := range db.ReplyHeads {
		if head.LegacyScope == scope {
			return true
		}
	}
	return false
}

func replyCursorReason(s Store, r replyRecord) string {
	db, err := loadRuns(s)
	if err != nil {
		return "source_unconfirmed"
	}
	if r.LogicalSession == "" {
		return "source_unconfirmed"
	}
	scope := replyScope(r.Locator.InstallationID, r.LogicalSession)
	if db.ReplyHeads[scope].Cursor != r.Cursor || db.retiredReplyScope(scope) {
		return "source_changed"
	}
	return ""
}

// A legacy caller retains its own waiter and release semantics. A known later
// ordinary source cannot be answered through an old native request ID.
func nativeRequestCurrent(s Store, agent, session, id string, l Locator, logical string) bool {
	if agent == "codex" || session == "" {
		return true
	}
	db, err := loadRuns(s)
	if err != nil {
		return false
	}
	if logical == "" {
		// Pre-upgrade records only attest the old pane-bound source. New
		// observations of that native ID cannot safely be assigned to it.
		for _, head := range db.ReplyHeads {
			if head.SessionHint == digest(agent+"\x00"+session) && (head.LegacyScope != "" || head.Request != id) {
				return false
			}
		}
		h, ok := db.ReplyHeads[nativeTargetIdentity(agent+"\x00"+session, l)]
		return !ok || h.Request == id
	}
	scope := replyScope(l.InstallationID, logical)
	h, ok := db.ReplyHeads[scope]
	return (!ok || h.Request == id) && !db.retiredReplyScope(scope)
}

func registerReplyLocked(s Store, r replyRecord) error {
	if _, err := readReply(s, r.SourceID); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("reply_already_registered")
	}
	if err := replyCapacityLocked(s, &r); err != nil {
		return err
	}
	return saveReply(s, r)
}

func replyCapacityLocked(s Store, registration *replyRecord) error {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	var unscoped string
	if registration != nil {
		unscoped, err = logicalSession(registration.Agent, registration.Session)
		if err != nil {
			return err
		}
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
		// A source registered before logical IDs or physical storage proof
		// still owns its decision after the key format changes.
		if registration != nil && old.Agent == registration.Agent &&
			old.Session == registration.Session && old.Run == registration.Run &&
			old.Instance == registration.Instance &&
			old.Locator.InstallationID == registration.Locator.InstallationID {
			oldScope := old.LogicalSession
			if oldScope == "" || oldScope == unscoped && registration.LogicalSession != unscoped ||
				registration.LogicalSession == unscoped && oldScope == sessionWithStorage(unscoped, old.NativeStorage) && oldScope != unscoped {
				return errors.New("reply_already_registered")
			}
		}
		if old.Expires+86400 < now && !old.alive(false) {
			if old.Renewable {
				if err = os.Remove(filepath.Join(s.Dir, wakeFile(id))); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
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
	if active >= 8 || registration != nil && count >= 1024 {
		return errors.New("reply_capacity")
	}
	return nil
}

// Eligibility to start a decision still belongs to the latest request. Only
// its already recorded deny may continue across adjacent permission reminders.
func openCodeDispatchGroup(s Store, r InteractionRecord, spec InteractionSpec) string {
	if r.LogicalSession == "" || !spec.openCodeDenyValid() {
		return ""
	}
	db, err := loadRuns(s)
	scope := replyScope(r.Locator.InstallationID, r.LogicalSession)
	h := db.ReplyHeads[scope]
	if err != nil || db.retiredReplyScope(scope) || h.Run != spec.RunID || h.Instance != spec.RuntimeIdentity {
		return ""
	}
	return h.PermissionGroup
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
	if r.Frame != nil && r.Frame.Action == "deny" && r.PermissionGroup != "" {
		return openCodeDispatchGroup(s, r, spec) == r.PermissionGroup
	}
	return nativeRequestCurrent(s, r.Agent, spec.SessionID, r.RequestID, r.Locator, r.LogicalSession)
}
