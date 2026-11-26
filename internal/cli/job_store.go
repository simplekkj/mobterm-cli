package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const quickRecordSize = 4096

type quickJobRecord struct {
	quickJobReply
	PairID      string `json:"pair_id"`
	EventID     string `json:"event_id"`
	CommandName string `json:"command_name"`
	Owner       int    `json:"owner"`
	KeyDigest   string `json:"key_digest"`
	Worker      int    `json:"worker,omitempty"`
	WorkerBirth string `json:"worker_birth,omitempty"`
	Child       int    `json:"child,omitempty"`
	ChildBirth  string `json:"child_birth,omitempty"`
	SubmitUntil int64  `json:"submit_until"`
	Deadline    int64  `json:"deadline"`
	Stop        bool   `json:"stop,omitempty"`
	Delivery    string `json:"notification_stage,omitempty"`
}

func (r quickJobRecord) filename() string { return "quick-job-" + r.JobID + ".json" }
func (r quickJobRecord) save(s Store) error {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > quickRecordSize {
		return errors.New("job_store_failed")
	}
	if err := atomicWrite(filepath.Join(s.Dir, r.filename()), raw, 0600); err != nil {
		return errors.New("job_store_failed")
	}
	return nil
}
func getQuickJob(s Store, id quickResultIdentity) (quickJobRecord, error) {
	var r quickJobRecord
	if !id.valid() {
		return r, errors.New("invalid_request")
	}
	installation, err := s.existingInstallation()
	if err != nil || installation != id.InstallationID {
		return r, errors.New("identity_mismatch")
	}
	if err := s.readJSON("quick-job-"+id.JobID+".json", &r); err != nil {
		return r, err
	}
	if r.quickResultIdentity != id || r.Owner != os.Getuid() {
		return r, errors.New("identity_mismatch")
	}
	return r, nil
}
func sameQuickProcess(pid int, birth string) bool {
	if pid <= 1 || birth == "" {
		return false
	}
	current, err := processIdentity(pid)
	return err == nil && current == birth
}
func goneQuickProcess(pid int, birth string) bool {
	if pid == 0 {
		return true
	}
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	current, err := processIdentity(pid)
	return err == nil && birth != "" && current != birth
}
func quickRunning(state string) bool { return state == "accepted" || state == "running" }
func quickStatus(ctx context.Context, s Store, id quickResultIdentity, stop bool) (quickJobReply, error) {
	var empty quickJobReply
	if stop {
		unlock, err := s.lock(ctx)
		if err != nil {
			return empty, errors.New("job_store_failed")
		}
		defer unlock()
	}
	r, err := getQuickJob(s, id)
	if errors.Is(err, os.ErrNotExist) {
		return empty, errors.New("job_unavailable")
	}
	if err != nil {
		return empty, errors.New("identity_mismatch")
	}
	if r.OccurredAt > 0 && r.OccurredAt <= time.Now().Add(-quickResultLifetime).Unix() {
		return empty, errors.New("job_expired")
	}
	if quickRunning(r.State) {
		if !sameQuickProcess(r.Worker, r.WorkerBirth) {
			r.State = "unknown"
		} else if stop {
			r.Stop = true
			if err := r.save(s); err != nil {
				return empty, err
			}
		}
	}
	return r.quickJobReply, nil
}

func quickStoreName(name, prefix string) bool {
	id, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, ".json")
	return ok && notificationUUID.MatchString(id) && id == strings.ToLower(id)
}

// Hold the short store transaction while collecting. Unexpired dedup entries
// never lose their protection under admission pressure. Reserve room for the
// at most eight active entries to add their terminal facts.
func makeQuickJobRoom(s Store, installation string, now int64) error {
	files, err := os.ReadDir(s.Dir)
	if err != nil {
		return errors.New("job_store_failed")
	}
	var total, active, count int
	for _, file := range files {
		if !quickStoreName(file.Name(), "quick-job-") {
			continue
		}
		info, err := file.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("job_store_failed")
		}
		var r quickJobRecord
		if err := s.readJSON(file.Name(), &r); err != nil || !r.quickResultIdentity.valid() || r.filename() != file.Name() {
			return errors.New("job_store_failed")
		}
		end := r.OccurredAt
		if end == 0 {
			end = r.Deadline
		}
		if end+int64(quickResultLifetime/time.Second) <= now && now > r.SubmitUntil &&
			(!quickRunning(r.State) || goneQuickProcess(r.Worker, r.WorkerBirth) && goneQuickProcess(r.Child, r.ChildBirth)) {
			if err := os.Remove(filepath.Join(s.Dir, file.Name())); err != nil {
				return errors.New("job_store_failed")
			}
			continue
		}
		if quickRunning(r.State) && r.Worker != 0 && goneQuickProcess(r.Worker, r.WorkerBirth) && r.Child != 0 && goneQuickProcess(r.Child, r.ChildBirth) {
			r.State = "unknown"
			r.Worker = 0
			r.WorkerBirth = ""
			r.Child = 0
			r.ChildBirth = ""
			if err := r.save(s); err != nil {
				return err
			}
		}
		count++
		total += int(info.Size())
		if r.InstallationID == installation && quickRunning(r.State) {
			active++
		}
	}
	if active >= 8 || count >= 4096 || total+(active+1)*quickRecordSize > 1<<20 {
		return errors.New("job_limit")
	}
	return nil
}

func saveQuickReceipt(s Store, r quickResultReceipt, now int64) error {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > maxFrame {
		return errors.New("invalid_result")
	}
	list, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	type item struct {
		name     string
		occurred int64
		size     int
	}
	items := make([]item, 0)
	total := len(raw)
	dest := "quick-result-" + r.JobID + ".json"
	for _, f := range list {
		if f.Name() == dest || !quickStoreName(f.Name(), "quick-result-") {
			continue
		}
		path := filepath.Join(s.Dir, f.Name())
		b, err := secureRead(path, maxFrame)
		if err != nil {
			return err
		}
		var header struct {
			OccurredAt int64 `json:"occurred_at"`
		}
		if json.Unmarshal(b, &header) != nil {
			return errors.New("invalid_result")
		}
		items = append(items, item{f.Name(), header.OccurredAt, len(b)})
		total += len(b)
	}
	slices.SortFunc(items, func(a, b item) int {
		if a.occurred < b.occurred {
			return -1
		}
		if a.occurred > b.occurred {
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	for i, f := range items {
		if f.occurred > now-int64(quickResultLifetime/time.Second) && len(items)-i+1 <= 500 && total <= 4<<20 {
			break
		}
		if err := os.Remove(filepath.Join(s.Dir, f.name)); err != nil {
			return err
		}
		total -= f.size
	}
	return atomicWrite(filepath.Join(s.Dir, dest), raw, 0600)
}
