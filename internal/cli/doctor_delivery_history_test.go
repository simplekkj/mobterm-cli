package cli

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestDoctorOutboxErrorUsesLatestEventAndStableTie(t *testing.T) {
	s, p := quickTestPair(t)
	now := time.Now().Unix()
	seedOutbox(t, s, p, 3, "permanent_failed", now)
	events := []string{digest("0"), digest("1"), digest("2")}
	if deliveryKey(p.PairID, events[0]) < deliveryKey(p.PairID, events[1]) {
		events[1], events[0] = events[0], events[1]
	}
	record := func(id string, created int64, code string) {
		t.Helper()
		_, err := updateDelivery(t.Context(), s, p.PairID, id, func(d *delivery) error {
			d.Created, d.Error = created, code
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	record(events[0], now, "attempts_exhausted")
	record(events[1], now, "retry_after_expiry")
	record(events[2], now-1, "service_handoff_unconfirmed")
	check := func(want string) {
		t.Helper()
		before := providerSnapshot(t, s.Dir)
		for range 4 {
			got := doctorOutbox(t.Context(), s, doctorOptions{Offline: true, Deep: true}, doctorReadCatalog(s))
			if got.Details["recent_error"] != want || got.Reason != "delivery_failure_recorded" {
				t.Fatalf("expected %s: %#v", want, got)
			}
		}
		if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
			t.Fatal("diagnostics changed Outbox")
		}
	}
	check("attempts_exhausted")
	record(events[2], now+1, "sensitive_remote_code")
	check("unclassified")
	for _, code := range []string{"pair_not_active", "service_handoff_unconfirmed", "retry_after_expiry", "attempts_exhausted", "approval_disabled", "approval_resolved", "http_429"} {
		if doctorDeliveryError(code) != code {
			t.Errorf("safe producer reason missing: %s", code)
		}
	}
}

func TestDoctorJobWorkerStageIsHistoricalAndOfflineSkipsDirectoryLookup(t *testing.T) {
	s, p := quickTestPair(t)
	q := quickTestStart(p, 1, "private command")
	now := time.Now().Unix()
	event := digest("0")
	seedOutbox(t, s, p, 1, "apns_accepted", now)
	_, err := updateDelivery(t.Context(), s, p.PairID, event, func(d *delivery) error {
		d.Kind = quickCommandCompleted
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	job := quickJobRecord{quickJobReply: quickJobReply{quickResultIdentity: q.quickResultIdentity, State: "stopped", OccurredAt: now},
		Owner: os.Getuid(), PairID: p.PairID, EventID: event, Delivery: "outbox_saved"}
	if err := job.save(s); err != nil {
		t.Fatal(err)
	}
	before := providerSnapshot(t, s.Dir)
	r := runProviderReport(t, s, newAPI(), doctorOptions{Offline: true})
	j := providerFinding(t, r, "job.records", "")
	if j.Counts["worker_delivery_snapshots"] != 1 || j.Counts["notification_pending"] != 0 || j.Reason != "job_delivery_history" {
		t.Fatal(j)
	}
	d := providerFinding(t, r, "delivery.outbox", "")
	if d.Counts["apns_accepted"] != 1 || d.Details["device_display"] != "unobserved" {
		t.Fatal(d)
	}
	shell := providerFinding(t, r, "job.shell", "")
	if shell.Status != "skipped" || shell.Reason != "offline" || len(shell.Details) != 0 {
		t.Fatal(shell)
	}
	for _, f := range r.Features {
		if (f.ID == "job.foreground_backend" || f.ID == "job.worker_backend") && f.Status != "unknown" {
			t.Fatal(f)
		}
	}
	other := runProviderReport(t, s, newAPI(), doctorOptions{Offline: true, Pair: "another01"})
	if providerFinding(t, other, "job.records", "").Counts["worker_delivery_snapshots"] != 0 {
		t.Fatal("borrowed another pair's worker stage")
	}
	if !reflect.DeepEqual(before, providerSnapshot(t, s.Dir)) {
		t.Fatal("read-only job inspection changed disk state")
	}
}
