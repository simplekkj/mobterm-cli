package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	appendMode          = "thread_append"
	appendPurpose       = "mobterm/reply/2"
	appendScope         = "codex.thread_append"
	appendSourceTTL     = 7 * 86400
	appendDecisionTTL   = 90 * 86400
	appendCapacity      = 2048
	appendFenceCapacity = 32768
	appendStoreLimit    = 8 << 20
)

type appendParticipant struct {
	Origin     string `json:"origin"`
	Event      string `json:"event"`
	Envelope   string `json:"envelope"`
	Lease      string `json:"lease,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Expires    int64  `json:"expires,omitempty"`
}

type appendSource struct {
	Runtime      codexRuntimeSource           `json:"runtime"`
	Thread       string                       `json:"thread"`
	Turn         string                       `json:"turn"`
	Created      int64                        `json:"created"`
	Participants map[string]appendParticipant `json:"participants"`
	Decision     *replyFrame                  `json:"decision,omitempty"`
	ClaimedAt    int64                        `json:"claimed_at,omitempty"`
	State        string                       `json:"state,omitempty"`
	QueueID      string                       `json:"queue_id,omitempty"`
	Reason       string                       `json:"reason,omitempty"`
	Ack          bool                         `json:"ack,omitempty"`
}

type appendStore struct {
	V       int                      `json:"v"`
	Sources map[string]*appendSource `json:"sources"`
	// Permanent digest-only fences. A replay from a replacement runtime can
	// never create a second slot, even after claim metadata is collected.
	Retired map[string]struct{} `json:"retired"`
}

func loadAppendStore(s Store) (appendStore, error) {
	var db appendStore
	raw, mode, err := secureReadWithMode(filepath.Join(s.Dir, "reply-append-v2.json"), appendStoreLimit)
	if errors.Is(err, os.ErrNotExist) {
		return appendStore{2, map[string]*appendSource{}, map[string]struct{}{}}, nil
	}
	if err != nil {
		return db, err
	}
	if mode != 0600 || json.Unmarshal(raw, &db) != nil || db.V != 2 || db.Sources == nil || db.Retired == nil || len(db.Sources) > appendCapacity || len(db.Retired) > appendFenceCapacity {
		return db, errors.New("invalid_append_store")
	}
	for id, r := range db.Sources {
		if r == nil || id != appendSourceID(r.Runtime, r.Thread, r.Turn) || r.Created <= 0 || r.Participants == nil || !r.validDecision(id) {

			return db, errors.New("invalid_append_store")
		}
	}
	return db, nil
}

func (r appendSource) validDecision(id string) bool {
	if r.Decision == nil {
		return r.State == "" && r.ClaimedAt == 0 && r.QueueID == "" && !r.Ack
	}
	d := r.Decision
	b, ok := r.Participants[d.Pair]
	if !ok || d.V != 2 || d.Mode != appendMode || d.SourceID != id || d.Installation != r.Runtime.Installation ||
		d.Text != "" || d.Proof != "" || d.ClientID == "" || d.DecisionID == "" || d.Origin != b.Origin ||
		d.Event != b.Event || d.Envelope != b.Envelope || d.Lease != b.Lease || d.ActivationRevision != b.Revision ||
		d.PolicyGeneration != b.Generation || !bodyHashPattern.MatchString(d.ResponseDigest) || r.ClaimedAt <= 0 {
		return false
	}
	switch r.State {
	case "dispatching", "delivery_unknown", "not_dispatched", "admitted":
		return true
	case "queued", "waiting", "content_changed":
		return r.QueueID != ""
	default:
		return false
	}
}

// All writes run under Store.lock and atomically replace the complete bounded record.
func (db appendStore) save(s Store) error {
	raw, err := json.Marshal(db)
	if err != nil {
		return err
	}
	if len(raw) > appendStoreLimit {
		return errors.New("append_store_capacity")
	}
	return atomicWrite(filepath.Join(s.Dir, "reply-append-v2.json"), raw, 0600)
}

func appendSourceID(rt codexRuntimeSource, thread, turn string) string {
	return digest(strings.Join([]string{appendPurpose, rt.Installation, rt.Home, thread, turn}, "\x00"))
}

func registerAppendSource(s Store, rt codexRuntimeSource, thread, turn string, created, now int64) (string, error) {
	if !filepath.IsAbs(rt.Home) || !filepath.IsAbs(rt.Socket) || rt.UID != os.Geteuid() || rt.PeerPID <= 1 || rt.PeerBirth == "" || !opaqueID.MatchString(rt.Installation) || !codexRolloutUUID.MatchString(thread) || !codexRolloutUUID.MatchString(turn) || created <= 0 || created > now+1 || created+appendSourceTTL <= now {
		return "", errors.New("invalid_append_source")
	}
	db, err := loadAppendStore(s)
	if err != nil {
		return "", err
	}
	id := appendSourceID(rt, thread, turn)
	if r := db.Sources[id]; r != nil {
		if r.Runtime != rt || r.Created != created {
			return "", errors.New("append_source_changed")
		}
		return id, nil
	}
	if _, retired := db.Retired[id]; retired {
		return "", errors.New("append_source_expired")
	}
	for k, r := range db.Sources {
		if r.Created+appendSourceTTL > now || r.Decision != nil && r.ClaimedAt+appendDecisionTTL > now {
			continue
		}
		if len(db.Retired) >= appendFenceCapacity {
			break
		}
		db.Retired[k] = struct{}{}
		delete(db.Sources, k)
	}
	if len(db.Sources) >= appendCapacity {
		return "", errors.New("append_store_capacity")
	}
	db.Sources[id] = &appendSource{Runtime: rt, Thread: thread, Turn: turn, Created: created, Participants: map[string]appendParticipant{}}
	return id, db.save(s)
}

func bindAppendDelivery(s Store, id, origin string, e Envelope) error {
	db, err := loadAppendStore(s)
	if err != nil {
		return err
	}
	r := db.Sources[id]
	if r == nil || origin == "" {
		return errors.New("append_source_missing")
	}
	b := appendParticipant{Origin: origin, Event: e.EventID, Envelope: envelopeBodyDigest(e)}
	for otherID, other := range db.Sources {
		if prior, ok := other.Participants[e.PairID]; ok && otherID != id && prior.Event == e.EventID {
			return errors.New("append_binding_changed")
		}
	}
	if existing, ok := r.Participants[e.PairID]; ok {
		if b.Origin != existing.Origin || b.Event != existing.Event || b.Envelope != existing.Envelope {
			return errors.New("append_binding_changed")
		}
		return nil
	}
	r.Participants[e.PairID] = b
	return db.save(s)
}

func (db appendStore) source(f replyFrame) (string, *appendSource) {
	for id, r := range db.Sources {
		if f.SourceID != "" && id != f.SourceID {
			continue
		}
		b, ok := r.Participants[f.Pair]
		if ok && b.Origin == f.Origin && r.Runtime.Installation == f.Installation && b.Event == f.Event && b.Envelope == f.Envelope {
			return id, r
		}
	}
	return "", nil
}

func (db appendStore) unusedDecision(id, client string) error {
	for _, r := range db.Sources {
		if r.Decision != nil && (r.Decision.DecisionID == id || client != "" && r.Decision.ClientID == client) {
			return errors.New("reply_decision_conflict")
		}
	}
	return nil
}
