package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The run order is assigned at source observation, before per-pair policy,
// encryption, outbox scheduling or transport. Revision orders events in one run.
type noticeSource struct {
	Session  string `json:"session"`
	RunOrder int64  `json:"run_order"`
	Retires  string `json:"retires,omitempty"`
}

type visibleNotice struct {
	LegacyAudience string `json:"legacy_audience,omitempty"`
	RunOrder       int64  `json:"run_order"`
	Revision       int64  `json:"revision"`
	Event          string `json:"event"`
	Envelope       string `json:"envelope"`
	Until          int64  `json:"until"`
}

func logicalSession(agent, session string) (string, error) {
	var path string
	var err error
	if agent == "codex" {
		path, err = configPath(agent)
	} else {
		path, err = nativeConfigPath(agent)
	}
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// OpenCode stores sessions independently of the plugin configuration.
	storage := ""
	if agent == "opencode" {
		storage = os.Getenv("XDG_DATA_HOME")
		if storage == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return "", e
			}
			storage = filepath.Join(home, ".local", "share")
		}
	}
	return digest(strings.Join([]string{agent, path, storage, session}, "\x00")), nil
}

// Scope the same native ID independently in each actual session file or database.
// The configuration-scoped digest remains the key for pre-storage history.
func sessionWithStorage(base, storage string) string {
	if storage == "" {
		return base
	}
	return digest(base + "\x00native-storage\x00" + storage)
}

func (a *Activity) identifySession() error {
	if a.LogicalSession != "" {
		return nil
	}
	base, err := logicalSession(a.Agent, a.Session)
	if err != nil {
		return err
	}
	a.storageID = nativeStorageKey(a.Agent, a.NativeStorage)
	a.LogicalSession = sessionWithStorage(base, a.storageID)
	if a.storageID != "" {
		a.legacySession = base
	}
	return nil
}

func audienceSession(p Pair, session string) string {
	return digest(strings.Join([]string{strings.TrimSuffix(p.APIURL, "/"), p.InstallationID, p.PairID, session}, "\x00"))
}

func (db *RunStore) recordVisible(e Envelope, p RunPayload, now int64) error {
	if p.Source == nil || e.Kind == "state" || e.Kind == "test" {
		return nil
	}
	if db.Visible == nil {
		db.Visible = map[string]visibleNotice{}
	}
	old := db.Visible[p.Source.Session]
	if old.RunOrder > p.Source.RunOrder || old.RunOrder == p.Source.RunOrder && old.Revision > p.Revision {
		return nil
	}
	body := envelopeBodyDigest(e)
	if old.RunOrder == p.Source.RunOrder && old.Revision == p.Revision && (old.Event != e.EventID || old.Envelope != body) {
		old.Event, old.Envelope = "", ""
		old.Until = max(old.Until, e.ExpiresAt+outboxRetention)
		db.Visible[p.Source.Session] = old
		return errors.New("visible_source_conflict")
	}
	if len(db.Visible) >= 4096 && old.Event == "" {
		for key, head := range db.Visible {
			if head.Until <= now {
				delete(db.Visible, key)
			}
		}
		if len(db.Visible) >= 4096 {
			return errors.New("visible_source_capacity")
		}
	}
	db.Visible[p.Source.Session] = visibleNotice{LegacyAudience: p.Source.Retires, RunOrder: p.Source.RunOrder, Revision: p.Revision,
		Event: e.EventID, Envelope: body, Until: e.ExpiresAt + outboxRetention}
	return nil
}

func visibleSourceCurrent(s Store, f bodyIdentity, scope string) bool {
	if scope == "" {
		return false
	}
	db, err := loadRuns(s)
	head := db.Visible[scope]
	if err != nil || head.Event != f.Event || head.Envelope != f.Envelope {
		return false
	}
	if head.LegacyAudience == "" {
		// A pre-storage delivery might have been encoded after a newer notice.
		// Its old scope cannot distinguish which physical file produced it.
		for _, qualified := range db.Visible {
			if qualified.LegacyAudience == scope {
				return false
			}
		}
	}
	return true
}
