package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type quickCommandVector struct {
	Key       string
	Plaintext string
	AAD       string
	Envelope  Envelope
}

func quickCommandFixture(t *testing.T) (quickCommandVector, QuickCommandCompletion) {
	t.Helper()
	var f quickCommandVector
	b, err := os.ReadFile(testdataDir + "/protocol-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var c QuickCommandCompletion
	if err := json.Unmarshal([]byte(f.Plaintext), &c); err != nil {
		t.Fatal(err)
	}
	return f, c
}

func quickCommandTestPolicy(c QuickCommandCompletion, pairID string) quickCommandPolicy {
	return quickCommandPolicy{V: 2, PairID: pairID, InstallationID: c.InstallationID, Enabled: true, Inbox: true,
		NotificationsEnabled: true, Supported: true, Completed: true, CapabilityGeneration: 1,
		PolicyGeneration: 1, ExpiresAt: c.OccurredAt + 60}
}

func TestQuickCommandV3Golden(t *testing.T) {
	f, c := quickCommandFixture(t)
	pair := testPair(c.InstallationID, "https://fixture.invalid")
	pair.EncryptionKey = f.Key
	nonce, err := decode64(f.Envelope.Nonce, 12)
	if err != nil {
		t.Fatal(err)
	}
	policy := quickCommandTestPolicy(c, pair.PairID)
	seal := func(policy quickCommandPolicy, value QuickCommandCompletion) (Envelope, error) {
		return sealQuickCommand(pair, f.Envelope.PairID, f.Envelope.EventID, policy, value, c.OccurredAt, nonce)
	}
	got, err := seal(policy, c)
	if err != nil || got != f.Envelope || string(got.aad()) != f.AAD {
		t.Fatalf("golden mismatch: %v", err)
	}
	for _, alter := range []func(*quickCommandPolicy){
		func(p *quickCommandPolicy) { p.Enabled = false },
		func(p *quickCommandPolicy) { p.Inbox = false },
		func(p *quickCommandPolicy) { p.NotificationsEnabled = false },
		func(p *quickCommandPolicy) { p.Supported = false },
		func(p *quickCommandPolicy) { p.Completed = false },
		func(p *quickCommandPolicy) { p.PairID = "pair-other-0000000000001" },
		func(p *quickCommandPolicy) { p.InstallationID = "installation-other-00001" },
		func(p *quickCommandPolicy) { p.ExpiresAt = c.OccurredAt },
		func(p *quickCommandPolicy) { p.ExpiresAt = c.OccurredAt + 62 },
		func(p *quickCommandPolicy) { p.CapabilityGeneration = 1 << 53 },
		func(p *quickCommandPolicy) { p.PolicyGeneration = 0 },
	} {
		copy := policy
		alter(&copy)
		if _, err := seal(copy, c); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
	for _, alter := range []func(*QuickCommandCompletion){
		func(c *QuickCommandCompletion) { c.OccurredAt -= 3600 },
		func(c *QuickCommandCompletion) { c.State = "running" },
		func(c *QuickCommandCompletion) { c.State = "succeeded" },
		func(c *QuickCommandCompletion) { c.CommandName = "hidden\u2029line" },
		func(c *QuickCommandCompletion) { c.JobID = "not-a-job" },
	} {
		value := c
		alter(&value)
		if _, err := seal(policy, value); err == nil {
			t.Fatal("accepted invalid completion")
		}
	}
	if _, err := sealQuickCommand(pair, "", f.Envelope.EventID, policy, c, c.OccurredAt, nonce); err == nil {
		t.Fatal("missing original notification intent")
	}
}

func TestQuickCommandDisplayNameBoundariesAndTerminalEvidence(t *testing.T) {
	_, completion := quickCommandFixture(t)
	for _, entry := range []struct {
		name  string
		valid bool
	}{
		{strings.Repeat("a", 120), true}, {strings.Repeat("a", 121), false},
		{strings.Repeat("🙂", 120), true}, {strings.Repeat("🙂", 129), false},
		{"编译 👩‍👩‍👧‍👦", true}, {"QuickCommand", true}, {" foo", false}, {"foo\u200b", false},
		{"foo\u202e", false}, {"foo\x1b[0m", false}, {"a\tb", false},
	} {
		completion.CommandName = entry.name
		if completion.valid() != entry.valid {
			t.Errorf("name %q: expected valid=%v", entry.name, entry.valid)
		}
	}
	completion.CommandName = "QuickCommand"
	exit, signal := 7, 15
	for _, state := range []string{"timed_out", "stopped"} {
		completion.State = state
		for _, fields := range []struct {
			exit, signal *int
			valid        bool
		}{{nil, nil, true}, {&exit, nil, true}, {nil, &signal, true}, {&exit, &signal, false}} {
			completion.ExitCode, completion.Signal = fields.exit, fields.signal
			result := quickResultPlain{State: state, ExitCode: fields.exit, Signal: fields.signal,
				StartedAt: 1, OccurredAt: 2, Output: []byte{}, StderrMask: []byte{}}
			if completion.valid() != fields.valid || result.valid() != fields.valid {
				t.Errorf("%s: inconsistent termination evidence", state)
			}
		}
	}
}

// The persisted queue owns an immutable envelope. A lost response leaves it due
// for replay and a matching non-200 durable handoff settles it once.
func TestQuickCommandFrozenOutbox(t *testing.T) {
	f, c := quickCommandFixture(t)
	s := testStore(t)
	ctx := context.Background()
	var calls atomic.Int32
	var original Envelope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v2/pairs/"+f.Envelope.PairID+"/events" {
			t.Error("unexpected delivery route")
		}
		var received Envelope
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil || received != original {
			t.Error("changed frozen envelope")
		}
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close() // Service received the envelope, but the sender lost its receipt.
			return
		}
		w.WriteHeader(429)
		printJSON(w, map[string]any{"event_id": received.EventID, "state": "retryable_failed", "outbox_handoff": true})
	}))
	defer server.Close()
	p := testPair(c.InstallationID, server.URL)
	p.EncryptionKey = f.Key
	if err := s.savePair(ctx, p); err != nil {
		t.Fatal(err)
	}
	c.OccurredAt = time.Now().Unix()
	base := f.Envelope
	policy := quickCommandTestPolicy(c, p.PairID)
	d, err := s.queueQuickJobCompletion(ctx, p, base.EventID, policy, c)
	if err != nil || d.Envelope == nil {
		t.Fatal("failed to freeze the authorized completion", err)
	}
	original = *d.Envelope
	again, err := s.queueQuickJobCompletion(ctx, p, base.EventID, policy, c)
	if err != nil || again.Envelope == nil || *again.Envelope != original {
		t.Fatal("same completion was resealed", err)
	}
	other := c
	other.CommandName = "Another job"
	if _, err := s.queueQuickJobCompletion(ctx, p, base.EventID, policy, other); err == nil {
		t.Fatal("event ID was reused for a different completion")
	}
	policy.Completed = false
	if _, err := s.queueQuickJobCompletion(ctx, p, randomID(), policy, c); err == nil {
		t.Fatal("queued completion with disabled policy")
	}
	api := API{server.Client()}
	if _, err = deliverOne(ctx, s, api, d); err == nil {
		t.Fatal("lost handoff was accepted")
	}
	_, err = updateDelivery(ctx, s, p.PairID, base.EventID, func(d *delivery) error {
		if d.State != "pending" || d.Envelope == nil || *d.Envelope != original {
			t.Fatal("lost frozen responsibility")
		}
		d.Next = 0
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = flushOutbox(ctx, s, api, p.PairID, false); err != nil {
		t.Fatal(err)
	}
	records, err := outboxRecords(ctx, s, p.PairID, false)
	if err != nil || len(records) != 1 || records[0].State != "handed_off" || records[0].Envelope != nil || calls.Load() != 2 {
		t.Fatal("missing durable handoff", err)
	}
	_, err = updateDelivery(ctx, s, p.PairID, base.EventID, func(d *delivery) error {
		next := original
		next.V = 4
		d.State = "pending"
		d.Envelope = &next
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = deliverOne(ctx, s, api, d); err == nil || calls.Load() != 2 {
		t.Fatal("dispatched unsupported wire")
	}
}
