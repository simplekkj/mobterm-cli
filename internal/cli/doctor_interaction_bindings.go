package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mobterm/mobterm-cli/internal/doctor"
)

// Deliberately project only attribution, deadlines, durable states and process
// identity. Operation JSON, reply text, proofs and owner tokens never enter
// Doctor's model, even if an in-flight decision still contains them on disk.
type doctorDecisionOwner struct {
	V              int    `json:"v"`
	Pair           string `json:"pair_id"`
	Request        string `json:"request_id"`
	Source         string `json:"source_id"`
	Installation   string `json:"installation_id"`
	Origin         string `json:"api_url"`
	Agent          string `json:"agent"`
	Mode           string `json:"mode"`
	Cursor         string `json:"cursor"`
	Event          string `json:"event_id"`
	Envelope       string `json:"envelope_digest"`
	Lease          string `json:"lease"`
	Revision       int64  `json:"activation_revision"`
	Generation     int64  `json:"policy_generation"`
	ResponseDigest string `json:"response_digest"`
}

type doctorBindingRecord struct {
	V            int                        `json:"v"`
	Request      string                     `json:"request_id"`
	Source       string                     `json:"source_id"`
	Cursor       string                     `json:"cursor"`
	Agent        string                     `json:"agent"`
	State        string                     `json:"state"`
	Locator      Locator                    `json:"locator"`
	Expires      int64                      `json:"expires_at"`
	PID          int                        `json:"pid"`
	Birth        string                     `json:"birth"`
	ParentPID    int                        `json:"parent_pid"`
	ParentBirth  string                     `json:"parent_birth"`
	Pairs        map[string]json.RawMessage `json:"pairs"` // generations for requests, activations for reply v1
	DecisionPair string                     `json:"deciding_pair"`
	Frame        *doctorDecisionOwner       `json:"frame"`
}

func doctorBindingName(name string) (kind, id string) {
	id, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return "", ""
	}
	if id, ok := strings.CutPrefix(id, "reply-source-"); ok && bodyHashPattern.MatchString(id) {
		return "reply", id
	}
	if id, ok := interactionFileID(name); ok {
		return "interaction", id
	}
	if stringsHasApproval(name) {
		id = strings.TrimPrefix(id, "approval-")
		if opaqueID.MatchString(id) {
			return "approval", id
		}
	}
	return "", ""
}

func doctorInspectBindings(ctx context.Context, s Store, p Pair, agent string, r *doctor.Result) {
	now := time.Now().Unix()
	names, err := doctorRecordNames(ctx, s.Dir, 256, func(n string) bool { k, _ := doctorBindingName(n); return k != "" })
	if err != nil {
		r.Partial, r.Status, r.Reason = true, "unknown", "interaction_inventory_limited"
		if !errors.Is(err, errDoctorLimit) {
			return
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			r.Partial, r.Status, r.Reason = true, "unknown", "inventory_interrupted"
			return
		}
		kind, id := doctorBindingName(name)
		var binding doctorBindingRecord
		if err := doctorReadJSON(filepath.Join(s.Dir, name), 128<<10, &binding); err != nil {
			r.Status, r.Reason = "unknown", "interaction_record_invalid"
			continue
		}
		if binding.Locator.InstallationID != p.InstallationID || agent != "" && binding.Agent != agent {
			continue
		}
		if metric := binding.metric(kind, id, p, now); metric != "" {
			r.Counts[metric]++
		}
	}
	if agent == "" || agent == "codex" {
		if err := doctorAppendBindings(ctx, s, p, now, r.Counts); err != nil {
			r.Status, r.Reason = "unknown", "interaction_record_invalid"
			if ctx.Err() != nil {
				r.Partial, r.Reason = true, "inventory_interrupted"
			}
		}
	}
	switch {
	case r.Counts["result_unknown"] > 0:
		r.Status, r.Reason = "warn", "interaction_result_unknown"
	case r.Status == "unknown": // retain inventory failures
	case r.Counts["unconfirmed_bindings"] > 0:
		r.Status, r.Reason = "unknown", "interaction_bindings_unconfirmed"
	case r.Counts["expired_bindings"] > 0:
		r.Reason = "interaction_bindings_expired"
	}
}

func (b doctorBindingRecord) metric(kind, id string, p Pair, now int64) string {
	member := b.Pairs[p.PairID]
	if member == nil {
		return ""
	}
	decided := false
	switch kind {
	case "reply":
		var activation string
		if json.Unmarshal(member, &activation) != nil || !opaqueID.MatchString(activation) {
			return ""
		}
		if b.Frame != nil && b.Frame.Pair != p.PairID {
			return ""
		}
		if b.V != 1 || b.Source != id || b.Agent == "codex" || !interactionAgent(b.Agent) {
			return "unconfirmed_bindings"
		}
		if d := b.Frame; d != nil {
			if d.Installation != p.InstallationID || d.Origin != strings.TrimSuffix(p.APIURL, "/") {
				return ""
			}
			if d.Source != id || d.V != 1 || d.Agent != b.Agent || d.Cursor != b.Cursor || !bodyHashPattern.MatchString(d.Cursor) || !bodyHashPattern.MatchString(d.ResponseDigest) {
				return "unconfirmed_bindings"
			}
			decided = true
		}
	case "approval", "interaction":
		var generation int64
		if json.Unmarshal(member, &generation) != nil || generation <= 0 {
			return ""
		}
		if kind == "approval" {
			if b.DecisionPair != "" && b.DecisionPair != p.PairID {
				return ""
			}
			if (b.V != 2 && b.V != 3) || b.Request != id {
				return "unconfirmed_bindings"
			}
			decided = b.DecisionPair == p.PairID
		} else {
			if b.Frame != nil && b.Frame.Pair != p.PairID {
				return ""
			}
			if b.V != 1 || b.Request != id {
				return "unconfirmed_bindings"
			}
			if b.Frame != nil {
				if b.Frame.V != 1 || b.Frame.Request != id {
					return "unconfirmed_bindings"
				}
				decided = true
			}
		}
	}
	switch b.State {
	case "dispatching", "delivery_unknown":
		if !decided {
			return "unconfirmed_bindings"
		}
		return "result_unknown"
	case "consumed", "native_accepted", "native_consumed", "response_written", "native_confirmed":
		if !decided {
			return "unconfirmed_bindings"
		}
		return "native_handoffs"
	case "expired":
		return "expired_bindings"
	case "pending", "decision_recorded":
		if b.State == "decision_recorded" && !decided || b.Expires <= 0 {
			return "unconfirmed_bindings"
		}
		if b.Expires <= now {
			return "expired_bindings"
		}
		if b.Locator.validate() != nil || !sameQuickProcess(b.PID, b.Birth) || !sameQuickProcess(b.ParentPID, b.ParentBirth) {
			return "unconfirmed_bindings"
		}
		return "live_bindings"
	default:
		return ""
	}
}

type doctorAppendSource struct {
	Runtime      codexRuntimeSource           `json:"runtime"`
	Thread       string                       `json:"thread"`
	Turn         string                       `json:"turn"`
	Created      int64                        `json:"created"`
	State        string                       `json:"state"`
	Participants map[string]appendParticipant `json:"participants"`
	Decision     *doctorDecisionOwner         `json:"decision"`
}

func doctorAppendBindings(ctx context.Context, s Store, p Pair, now int64, counts map[string]int) error {
	var db struct {
		V       int                            `json:"v"`
		Sources map[string]*doctorAppendSource `json:"sources"`
	}
	err := doctorReadJSON(filepath.Join(s.Dir, "reply-append-v2.json"), appendStoreLimit, &db)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if db.V != 2 || db.Sources == nil || len(db.Sources) > appendCapacity {
		return errDoctorUnsafe
	}
	for id, b := range db.Sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if b == nil {
			return errDoctorUnsafe
		}
		part, ok := b.Participants[p.PairID]
		if !ok || b.Runtime.Installation != p.InstallationID || part.Origin != strings.TrimSuffix(p.APIURL, "/") || b.Decision != nil && b.Decision.Pair != p.PairID {
			continue
		}
		if id != appendSourceID(b.Runtime, b.Thread, b.Turn) || !opaqueID.MatchString(part.Event) || !bodyHashPattern.MatchString(part.Envelope) || b.Created <= 0 || b.Created > now+1 {
			counts["unconfirmed_bindings"]++
			continue
		}
		if d := b.Decision; d != nil {
			if d.Source != id || d.Agent != "codex" || d.Mode != appendMode || d.V != 2 || d.Installation != p.InstallationID || d.Origin != part.Origin || d.Event != part.Event || d.Envelope != part.Envelope || d.Lease != part.Lease || d.Revision != part.Revision || d.Generation != part.Generation || !bodyHashPattern.MatchString(d.ResponseDigest) {
				counts["unconfirmed_bindings"]++
				continue
			}
			switch b.State {
			case "dispatching", "delivery_unknown":
				counts["result_unknown"]++
			case "admitted", "queued", "waiting":
				counts["native_handoffs"]++
			}
			continue
		}
		switch {
		case b.Created+appendSourceTTL <= now || part.Expires > 0 && part.Expires <= now:
			counts["expired_bindings"]++
		case part.Expires > now && bodyHashPattern.MatchString(part.Lease) && part.Revision > 0 && part.Generation > 0 && b.Runtime.UID == os.Geteuid() && sameQuickProcess(b.Runtime.PeerPID, b.Runtime.PeerBirth):
			counts["live_bindings"]++ // Only the scoped lease and captured daemon process.
		default:
			counts["unconfirmed_bindings"]++
		}
	}
	return nil
}
