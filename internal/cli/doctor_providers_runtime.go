package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/simplekkj/mobterm-cli/internal/doctor"
)

var errDoctorLimit = errors.New("diagnostic snapshot limit")

// Limit directory enumeration as well as the number of actual store records
// opened. Never recurse into the account's other directories.
func doctorRecordNames(ctx context.Context, dir string, limit int, match func(string) bool) ([]string, error) {
	if err := doctorDirectory(dir); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var names []string
	for n := 0; n < 16384; n += 128 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := file.ReadDir(128)
		for _, e := range entries {
			if match(e.Name()) {
				if len(names) >= limit {
					slices.Sort(names)
					return names, errDoctorLimit
				}
				names = append(names, e.Name())
			}
		}
		if errors.Is(err, io.EOF) {
			slices.Sort(names)
			return names, nil
		}
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(names)
	return names, errDoctorLimit
}

func doctorJobShell(ctx context.Context) doctor.Result {
	r := doctorFact("pass", "job_backend_supported", "account")
	shell, err := quickAccountShell(ctx)
	r.Features = []doctor.Feature{doctorFeature("job.foreground_backend", doctorHost, "ready"), doctorFeature("job.worker_backend", doctorHost, "ready")}
	if err != nil {
		r.Status, r.Reason = "warn", "job_backend_unsupported"
		for i := range r.Features {
			r.Features[i].Status = "unavailable"
		}
		return r
	}
	r.Details = map[string]string{"shell": filepath.Base(shell), "backend": quickNativeBackend}
	return r
}

func doctorJobs(ctx context.Context, s Store, installation string, o doctorOptions) doctor.Result {
	r := doctorFact("pass", "job_records_inspected", "store_snapshot")
	r.Counts = map[string]int{}
	limit := 128
	if o.Deep {
		limit = 4096
	}
	names, err := doctorRecordNames(ctx, s.Dir, limit, func(name string) bool { return quickStoreName(name, "quick-job-") })
	if errors.Is(err, os.ErrNotExist) || err == nil && len(names) == 0 {
		return doctorFact("skipped", "no_job_records", "filesystem")
	}
	if errors.Is(err, errDoctorLimit) {
		r.Partial, r.Status, r.Reason = true, "warn", "use_deep_for_inventory"
		if o.Deep {
			r.Reason = "inventory_limited"
		}
	} else if err != nil {
		return doctorFact("unknown", "job_store_unreadable", "filesystem")
	}
	now := time.Now()
	for _, name := range names {
		if ctx.Err() != nil {
			r.Partial, r.Status, r.Reason = true, "unknown", "inventory_interrupted"
			return r
		}
		var rec quickJobRecord
		if doctorReadJSON(filepath.Join(s.Dir, name), quickRecordSize, &rec) != nil || !rec.quickResultIdentity.valid() || rec.filename() != name || rec.Owner != os.Getuid() || !opaqueID.MatchString(rec.PairID) {
			r.Counts["invalid"]++
			continue
		}
		if o.Pair != "" && o.Pair != rec.PairID {
			continue
		}
		if installation != rec.InstallationID {
			r.Counts["other_installation"]++
			continue
		}
		if rec.OccurredAt != 0 && rec.OccurredAt <= now.Add(-quickResultLifetime).Unix() {
			r.Counts["expired"]++
			continue
		}
		switch {
		case quickRunning(rec.State):
			if sameQuickProcess(rec.Worker, rec.WorkerBirth) {
				r.Counts["live_workers"]++
			} else {
				r.Counts["worker_unconfirmed"]++
			}
		case rec.State == "unknown":
			r.Counts["worker_unconfirmed"]++
		case rec.State == "not_started":
			r.Counts["not_started"]++
		case rec.OccurredAt > 0 && validQuickCommandTermination(rec.State, rec.ExitCode, rec.Signal):
			r.Counts["finished"]++
			switch rec.Delivery {
			case "pending", "outbox_saved":
				r.Counts["notification_pending"]++
			case "pair_unavailable", "outbox_unconfirmed", "permanent_failed", "policy_unavailable", "revoked":
				r.Counts["notification_unconfirmed"]++
			}
			if o.Deep {
				// Validate the encrypted receipt and its complete identity without
				// opening a key or decoding the command's output.
				if _, err := readQuickResult(ctx, s, bytes.NewReader(interactionFrameBytes(rec.quickResultIdentity)), time.Now); err != nil {
					r.Counts["result_unavailable"]++
				} else {
					r.Counts["result_receipts"]++
				}
			}
		default:
			r.Counts["invalid"]++
		}
	}
	switch {
	case r.Counts["invalid"] > 0:
		r.Status, r.Reason = "fail", "job_record_invalid"
	case r.Counts["worker_unconfirmed"] > 0:
		r.Status, r.Reason = "warn", "worker_identity_unconfirmed"
	case r.Counts["result_unavailable"] > 0:
		r.Status, r.Reason = "warn", "job_result_unavailable"
	case r.Counts["notification_unconfirmed"] > 0:
		r.Status, r.Reason = "warn", "job_notification_unconfirmed"
	case r.Counts["notification_pending"] > 0:
		r.Status, r.Reason = "warn", "job_notification_pending"
	case !r.Partial && len(r.Counts) == 0:
		r.Status, r.Reason = "skipped", "no_job_records"
	}
	return r
}

func doctorInteractions(ctx context.Context, s Store, p Pair, o doctorOptions) doctor.Result {
	scope := doctorPairScope(p.PairID)
	r := doctorFact("pass", "local_grants_read", "configuration")
	policies := InteractionPolicy{}
	approvals := map[string]map[string]int64{}
	replies := replyActivations{}
	for _, entry := range []struct {
		name string
		dst  any
	}{
		{"interaction-policy-v1.json", &policies}, {"approval-policy-v2.json", &approvals}, {"reply-activations-v1.json", &replies},
	} {
		if err := doctorReadJSON(filepath.Join(s.Dir, entry.name), 2<<20, entry.dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return doctorFact("fail", "interaction_policy_invalid", "filesystem")
		}
	}
	if policies == nil || approvals == nil || replies == nil {
		return doctorFact("fail", "interaction_policy_invalid", "filesystem")
	}
	r.Counts = map[string]int{"interaction_wire": 1, "approval_wire": 3, "reply_wire": 2}
	r.Details = map[string]string{}
	features := map[string]bool{"approval": false, "question": false, "reply": false, "form": false, "elicitation": false, "planApproval": false, "followupMessage": false}
	for _, agent := range []string{"claude", "codex", "opencode", "pi"} {
		if o.Agent != "" && agent != o.Agent {
			continue
		}
		var enabled []string
		for _, kind := range []string{"approval", "question", "form", "elicitation", "planApproval", "followupMessage"} {
			grant := policies[p.PairID][agent][kind]
			if grant < 0 {
				return doctorFact("fail", "interaction_policy_invalid", "filesystem")
			}
			if grant > 0 || kind == "approval" && approvals[p.PairID][agent] > 0 {
				features[kind] = true
				enabled = append(enabled, kind)
			}
		}
		key := agent
		if agent == "codex" {
			key = appendScope
		}
		activation := replies[p.PairID][key]
		if activation.Token != "" {
			if activation.Generation <= 0 || activation.Revision <= 0 || !opaqueID.MatchString(activation.Token) {
				return doctorFact("fail", "reply_activation_invalid", "configuration")
			}
			features["reply"] = true
			enabled = append(enabled, "reply")
		}
		if len(enabled) > 0 {
			r.Details[agent] = strings.Join(enabled, ",")
		}
	}
	for _, k := range []string{"approval", "question", "reply", "form", "elicitation", "planApproval", "followupMessage"} {
		state := "disabled"
		if features[k] {
			state = "unknown"
		} // A local grant alone cannot verify runtime loading and binding.
		r.Features = append(r.Features, doctorFeature("interaction."+k, scope, state))
	}
	if len(r.Details) == 0 {
		r.Reason = "local_grants_disabled"
	}
	if !o.Deep {
		return r
	}
	names, err := doctorRecordNames(ctx, s.Dir, 256, func(n string) bool { _, ok := interactionFileID(n); return ok })
	if err != nil {
		r.Partial, r.Status, r.Reason = true, "unknown", "interaction_inventory_limited"
		if !errors.Is(err, errDoctorLimit) {
			return r
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			r.Partial, r.Status, r.Reason = true, "unknown", "inventory_interrupted"
			return r
		}
		id, _ := interactionFileID(name)
		// Decode only identity/status fields. Request text, answers and the
		// contents of a decision frame are never retained or rendered.
		var row struct {
			V                  int
			RequestID          string `json:"request_id"`
			Pairs              map[string]int64
			Agent, Kind, State string
			Locator            Locator
			PID                int
			Birth              string
			ParentPID          int    `json:"parent_pid"`
			ParentBirth        string `json:"parent_birth"`
			ExpiresAt          int64  `json:"expires_at"`
		}
		if doctorReadJSON(filepath.Join(s.Dir, name), 2<<20, &row) != nil || row.V != 1 || row.RequestID != id {
			r.Status, r.Reason = "unknown", "interaction_record_invalid"
			continue
		}
		if row.Pairs[p.PairID] == 0 || row.Locator.InstallationID != p.InstallationID || o.Agent != "" && row.Agent != o.Agent {
			continue
		}
		switch row.State {
		case "pending", "decision_recorded":
			if row.ExpiresAt <= time.Now().Unix() {
				r.Counts["expired_bindings"]++
			} else if interactionAlive(InteractionRecord{PID: row.PID, Birth: row.Birth, ParentPID: row.ParentPID, ParentBirth: row.ParentBirth}) && row.Locator.validate() == nil {
				r.Counts["live_bindings"]++
			} else {
				r.Counts["unconfirmed_bindings"]++
			}
		case "dispatching", "delivery_unknown":
			r.Counts["result_unknown"]++
		}
	}
	if r.Counts["result_unknown"] > 0 {
		r.Status, r.Reason = "warn", "interaction_result_unknown"
	}
	return r
}

func doctorOutbox(ctx context.Context, s Store, o doctorOptions, catalog doctorCatalog) (result doctor.Result) {
	dir := filepath.Join(s.Dir, "outbox-v1")
	_, legacyErr := os.Lstat(filepath.Join(s.Dir, "events.json"))
	legacy := legacyErr == nil
	if err := doctorDirectory(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if legacy {
				return doctorFact("warn", "legacy_outbox_present", "filesystem")
			}
			return doctorFact("skipped", "outbox_not_configured", "filesystem")
		}
		return doctorFact("fail", "outbox_directory_invalid", "filesystem")
	}
	// Writers replace the index atomically after replacing the shard. Keep its
	// descriptor pinned and bracket the snapshot with the transaction marker.
	// A concurrent writer never waits for a potentially large Doctor deep read.
	txPath, indexPath := filepath.Join(dir, "transaction.json"), filepath.Join(dir, "index.json")
	if _, err := os.Lstat(txPath); err == nil {
		return doctorFact("warn", "outbox_transaction_pending", "filesystem")
	} else if !errors.Is(err, os.ErrNotExist) {
		return doctorFact("fail", "outbox_unreadable", "filesystem")
	}
	f, err := doctorPrivateFile(indexPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return doctorFact("fail", "outbox_index_invalid", "filesystem")
	}
	var before os.FileInfo
	if f != nil {
		defer f.Close()
		before, err = f.Stat()
		if err != nil {
			return doctorFact("unknown", "outbox_unreadable", "filesystem")
		}
	}
	defer func() {
		after, e := os.Lstat(indexPath)
		_, txErr := os.Lstat(txPath)
		if !errors.Is(txErr, os.ErrNotExist) || before == nil && !errors.Is(e, os.ErrNotExist) || before != nil && (e != nil || !os.SameFile(before, after)) {
			result = doctorFact("unknown", "outbox_snapshot_changed", "store_snapshot")
		}
	}()
	var idx outboxIndex
	if f != nil {
		raw, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		if e != nil || len(raw) > 2<<20 {
			return doctorFact("fail", "outbox_index_invalid", "filesystem")
		}
		err = json.Unmarshal(raw, &idx)
	}
	if errors.Is(err, os.ErrNotExist) {
		names, err := doctorRecordNames(ctx, dir, 256, func(n string) bool { return len(n) == 7 && strings.HasSuffix(n, ".json") })
		if err != nil || len(names) > 0 {
			return doctorFact("fail", "outbox_index_missing", "filesystem")
		}
		if legacy {
			return doctorFact("warn", "legacy_outbox_present", "filesystem")
		}
		return doctorFact("skipped", "outbox_empty", "filesystem")
	}
	if err != nil || idx.V != 1 || len(idx.Shards) > 256 || idx.Pairs == nil || idx.Shards == nil || idx.Legacy == nil || len(idx.Legacy) > 512 {
		return doctorFact("fail", "outbox_index_invalid", "filesystem")
	}
	c := idx.Total
	if o.Pair != "" {
		c = idx.Pairs[o.Pair]
	}
	if c.Records < 0 || c.Pending < 0 || c.HandedOff < 0 || c.Terminated < 0 || c.Pending+c.HandedOff+c.Terminated != c.Records {
		return doctorFact("fail", "outbox_index_invalid", "filesystem")
	}
	r := doctorFact("pass", "outbox_snapshot", "store_snapshot")
	r.Counts = map[string]int{"records": c.Records, "pending": c.Pending, "handed_off": c.HandedOff, "terminated": c.Terminated}
	if c.Pending > 0 {
		r.Status, r.Reason = "warn", "outbox_pending"
	}
	if !idx.Migrated && legacy {
		r.Status, r.Reason = "warn", "legacy_outbox_present"
	}
	if !o.Deep && idx.Total.Records > 128 {
		if c.Records > 0 {
			r.Partial = true
			if r.Status == "pass" {
				r.Status, r.Reason = "warn", "use_deep_for_inventory"
			}
		}
		return r
	}
	now := time.Now().Unix()
	latest := int64(0)
	scanned := 0
	for prefix, expected := range idx.Shards {
		if len(prefix) != 2 || strings.Trim(prefix, "0123456789abcdef") != "" || expected < 0 {
			return doctorFact("fail", "outbox_index_invalid", "filesystem")
		}
		if expected == 0 {
			continue
		}
		if ctx.Err() != nil {
			r.Partial, r.Status, r.Reason = true, "unknown", "inventory_interrupted"
			return r
		}
		// Use the store's full pair/event identity and envelope validation.
		file, err := doctorPrivateFile(filepath.Join(dir, prefix+".json"))
		if err != nil {
			return doctorFact("fail", "outbox_shard_invalid", "filesystem")
		}
		file.Close()
		sh, err := s.loadShard(prefix)
		if err != nil || len(sh.Records) != expected {
			return doctorFact("fail", "outbox_shard_invalid", "filesystem")
		}
		for _, d := range sh.Records {
			if o.Pair != "" && d.PairID != o.Pair {
				continue
			}
			scanned++
			if d.State == "pending" && d.Expires <= now {
				r.Counts["expired_pending"]++
			}
			if d.State == "apns_accepted" {
				r.Counts["apns_accepted"]++
			}
			if d.State == "permanent_failed" {
				r.Counts["permanent_failed"]++
			}
			if d.Body != nil {
				switch d.Body.Ref.Status {
				case "available":
					r.Counts["body_references"]++
					if o.Deep {
						p, ok := catalog.pairs[d.PairID]
						if ok && catalog.pairReason(d.PairID, p) == "" && doctorBodyFile(s, p, d.Body.Ref, now) {
							r.Counts["body_files_present"]++
						} else {
							r.Counts["body_unavailable"]++
						}
					}
				case "missing", "save_failed", "too_large":
					r.Counts["body_unavailable"]++
				}
			}
			if code := doctorDeliveryError(d.Error); code != "" && d.Created > latest {
				latest = d.Created
				r.Details = map[string]string{"recent_error": code}
			}
		}
	}
	if scanned != c.Records {
		return doctorFact("fail", "outbox_index_invalid", "filesystem")
	}
	switch {
	case r.Counts["expired_pending"] > 0:
		r.Status, r.Reason = "warn", "outbox_expired_pending"
	case r.Counts["permanent_failed"] > 0:
		r.Status, r.Reason = "warn", "delivery_failure_recorded"
	}
	if r.Details == nil {
		r.Details = map[string]string{}
	}
	r.Details["device_display"] = "unobserved"
	return r
}

// Observe the exact private file's extent, never read its conversation text.
func doctorBodyFile(s Store, p Pair, ref bodyRef, now int64) bool {
	if !opaqueID.MatchString(ref.ID) || !bodyHashPattern.MatchString(ref.Digest) || ref.Bytes <= 0 || ref.Bytes > bodyLimit || ref.Created <= 0 || ref.Created > now || ref.Created+bodyTTL <= now {
		return false
	}
	f, err := doctorPrivateFile(filepath.Join(s.bodyDir(), bodyName(ref, p)))
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && st.Size() == int64(ref.Bytes)
}

func doctorDeliveryError(code string) string {
	switch code {
	case "pair_revoked", "pair_expired", "notifications_disabled", "notification_disabled", "expired", "invalid_envelope", "apns_failed", "api_unavailable", "retry_limit", "rate_limited", "token_invalid":
		return code
	}
	if suffix, ok := strings.CutPrefix(code, "http_"); ok && len(suffix) == 3 {
		i, err := strconv.Atoi(suffix)
		if err == nil && i >= 400 && i <= 599 {
			return "http_" + strconv.Itoa(i)
		}
	}
	return ""
}
