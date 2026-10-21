package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Populate supported production shapes efficiently, preserving all normal
// transaction/budget checks. Used for seven-day and hard-capacity fixtures.
func seedOutbox(t *testing.T, s Store, p Pair, n int, state string, created int64) {
	t.Helper()
	unlock, err := s.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = s.migrateOutbox(); err != nil {
		t.Fatal(err)
	}
	shards := map[string]outboxShard{}
	originals := map[string]outboxShard{}
	for i := 0; i < n; i++ {
		event := digest(fmt.Sprint(i))
		key := deliveryKey(p.PairID, event)
		sh, ok := shards[key[:2]]
		if !ok {
			sh, err = s.loadShard(key)
			if err != nil {
				t.Fatal(err)
			}
			originals[key[:2]] = cloneShard(sh)
		}
		d := delivery{PairID: p.PairID, EventID: event, Kind: "needs_input", LocatorHash: digest("source"), Created: created, Expires: created + 3600, State: state}
		if state == "pending" {
			e := Envelope{V: 1, PairID: p.PairID, EventID: event, Kind: d.Kind, ExpiresAt: d.Expires, Nonce: "AAAAAAAAAAAAAAAA", Ciphertext: "AAAAAAAAAAAAAAAAAAAAAA=="}
			d.Envelope = &e
		}
		sh.Records[key] = d
		shards[key[:2]] = sh
	}
	for prefix, sh := range shards {
		idx, e := s.outboxLoad(true)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.saveShard(idx, prefix, originals[prefix], sh); e != nil {
			t.Fatal(e)
		}
	}
}
func TestOutboxHandoffAndLegacyAPIMatrix(t *testing.T) {
	for _, test := range []struct {
		name    string
		code    int
		handoff bool
		state   string
		valid   bool
		want    string
	}{
		{"old202", 202, false, "accepted", true, "pending"}, {"handoff202", 202, true, "accepted", true, "handed_off"}, {"handoff429", 429, true, "retryable_failed", true, "handed_off"}, {"handoff503", 503, true, "sending", true, "handed_off"}, {"terminalFirst", 200, true, "apns_accepted", true, "apns_accepted"}, {"wrongEvent", 503, true, "accepted", false, "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var e Envelope
				_ = json.NewDecoder(r.Body).Decode(&e)
				event := e.EventID
				if !test.valid {
					event = "wrong"
				}
				w.WriteHeader(test.code)
				printJSON(w, map[string]any{"event_id": event, "state": test.state, "outbox_handoff": test.handoff})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			_ = s.savePair(context.Background(), p)
			l := fixtureLocator()
			l.InstallationID = id
			d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "codex")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = deliverOne(context.Background(), s, API{server.Client()}, d)
			records, err := outboxRecords(context.Background(), s, "", false)
			if err != nil || len(records) != 1 || records[0].State != test.want {
				t.Fatal(records, err)
			}
			if test.want != "pending" && records[0].Envelope != nil {
				t.Fatal("terminal/handoff ciphertext retained")
			}
			_, _ = flushOutbox(context.Background(), s, API{server.Client()}, "", false)
			if calls.Load() != 1 {
				t.Fatal("resent handed-off or not-due record")
			}
			duplicate, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "codex")
			if err != nil || duplicate.EventID != d.EventID || duplicate.State != test.want {
				t.Fatal("duplicate resurrected", err)
			}
		})
	}
}
func TestOutboxRecoveryEnvelopeConcurrencyAndReceipts(t *testing.T) {
	if dir := os.Getenv("MOBTERM_FLUSH_CHILD"); dir != "" {
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_, err := flushOutbox(ctx, Store{dir}, newAPI(), "", false)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	s := testStore(t)
	id, _ := s.installation()
	var mu sync.Mutex
	var bodies [][]byte
	ready := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.WriteHeader(404)
			printJSON(w, map[string]string{"error": "event_not_found"})
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		var e Envelope
		_ = json.Unmarshal(b, &e)
		if !ready.Load() {
			w.WriteHeader(503)
			printJSON(w, map[string]string{"error": "api_unavailable"})
			return
		}
		printJSON(w, map[string]any{"event_id": e.EventID, "state": "accepted", "outbox_handoff": true})
	}))
	defer server.Close()
	p := testPair(id, server.URL)
	_ = s.savePair(context.Background(), p)
	l := fixtureLocator()
	l.InstallationID = id
	d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "codex")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = deliverOne(context.Background(), s, API{server.Client()}, d)
	_, err = updateDelivery(context.Background(), s, p.PairID, d.EventID, func(d *delivery) error { d.Next = 0; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ready.Store(true)
	children := []*exec.Cmd{}
	for i := 0; i < 2; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxRecoveryEnvelopeConcurrencyAndReceipts$")
		cmd.Env = append(os.Environ(), "MOBTERM_FLUSH_CHILD="+s.Dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = flushOutbox(context.Background(), Store{s.Dir}, API{server.Client()}, "", false)
		}()
	}
	wg.Wait()
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatal("cross-process flush failed", err)
		}
	}
	mu.Lock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("concurrent flush changed/repeated envelope", len(bodies))
	}
	mu.Unlock()
	_, _ = flushOutbox(context.Background(), s, API{server.Client()}, "", true)
	records, err := outboxRecords(context.Background(), s, "", false)
	if err != nil || records[0].State != "handed_off" || records[0].Error != "service_record_unavailable" {
		t.Fatal("404 changed recovery ownership", records, err)
	}
	_, _ = flushOutbox(context.Background(), s, API{server.Client()}, "", false)
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatal("404 reposted original receipt")
	}
}
func TestOutboxMigrationJournalAndReadOnly(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	l := fixtureLocator()
	l.InstallationID = id
	key, _ := decode64(p.EncryptionKey, 32)
	e, _ := seal(key, Envelope{V: 1, PairID: p.PairID, EventID: digest("source\x00" + p.PairID), Kind: "turn_completed", ExpiresAt: time.Now().Add(time.Hour).Unix()}, l, nil)
	plain, _ := json.Marshal(l)
	if err := s.writeJSON("events.json", map[string]cachedEnvelope{p.PairID + ":" + e.EventID: {e, digest(string(plain))}}); err != nil {
		t.Fatal(err)
	}
	if _, err := outboxSummary(context.Background(), s, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "outbox-v1")); !os.IsNotExist(err) {
		t.Fatal("status migrated state")
	}
	records, err := outboxRecords(context.Background(), s, "", true)
	if err != nil || len(records) != 1 || records[0].State != "legacy_unknown" || *records[0].Envelope != e {
		t.Fatal("legacy cache lost or queued", records, err)
	}
	keyID := deliveryKey(p.PairID, e.EventID)
	sh, _ := s.loadShard(keyID)
	idx, _ := s.outboxLoad(false)
	if err = s.outboxWrite("transaction.json", outboxTransaction{1, keyID[:2], sh, idx}, 36*1024*1024); err != nil {
		t.Fatal(err)
	}
	if _, err = outboxSummary(context.Background(), s, ""); err == nil {
		t.Fatal("read-only ignored incomplete transaction")
	}
	records, err = outboxRecords(context.Background(), Store{s.Dir}, "", true)
	if err != nil || len(records) != 1 || *records[0].Envelope != e {
		t.Fatal("interrupted migration changed envelope", err)
	}
}
func TestOutboxLongRetryAfterAndAttemptBound(t *testing.T) {
	for _, attempts := range []int{0, 11} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "999999999999999999999999999999")
				w.WriteHeader(429)
				printJSON(w, map[string]string{"error": "notification_pair_daily_limit"})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			_ = s.savePair(context.Background(), p)
			l := fixtureLocator()
			l.InstallationID = id
			d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = updateDelivery(context.Background(), s, p.PairID, d.EventID, func(d *delivery) error { d.Posts = attempts; return nil })
			if err != nil {
				t.Fatal(err)
			}
			_, _ = deliverOne(context.Background(), s, API{server.Client()}, d)
			records, _ := outboxRecords(context.Background(), s, "", false)
			if records[0].State != "permanent_failed" || records[0].Envelope != nil || records[0].Posts != attempts+1 {
				t.Fatal(records)
			}
		})
	}
}
func TestOutboxSevenDaysReopen(t *testing.T) {
	if dir := os.Getenv("MOBTERM_OUTBOX_REOPEN"); dir != "" {
		records, err := outboxRecords(context.Background(), Store{dir}, "", false)
		if err != nil || len(records) != 32*3500 {
			t.Fatal("new process reread", len(records), err)
		}
		return
	}
	s := testStore(t)
	id, _ := s.installation()
	now := time.Now().Unix()
	if err := s.migrateOutbox(); err != nil {
		t.Fatal(err)
	}
	shards := map[string]outboxShard{}
	for i := 0; i < 32; i++ {
		p := testPair(id, "https://api.invalid")
		p.PairID = fmt.Sprintf("pair_capacity_%020d", i)
		for j := 0; j < 3500; j++ {
			event := digest(fmt.Sprint(j))
			key := deliveryKey(p.PairID, event)
			sh, ok := shards[key[:2]]
			if !ok {
				sh = outboxShard{V: 1, Records: map[string]delivery{}}
			}
			sh.Records[key] = delivery{PairID: p.PairID, EventID: event, Kind: "turn_completed", LocatorHash: digest("source"), Created: now - 6*86400, Expires: now - 6*86400 + 3600, State: "apns_accepted"}
			shards[key[:2]] = sh
		}
	}
	for key, sh := range shards {
		idx, err := s.outboxLoad(true)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.saveShard(idx, key, outboxShard{V: 1, Records: map[string]delivery{}}, sh); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := s.outboxLoad(false)
	if err != nil || idx.Total.Records != 112000 || idx.Total.Bytes != 0 {
		t.Fatal("seven day budget", idx.Total, err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxSevenDaysReopen$")
	cmd.Env = append(os.Environ(), "MOBTERM_OUTBOX_REOPEN="+s.Dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("new process reread", err, string(out))
	}
}

func TestNotifyPersistsAllDevicesBeforeNetwork(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			records, err := outboxRecords(context.Background(), s, "", false)
			if err != nil || len(records) != 2 {
				t.Error("network began before complete durable fanout", len(records), err)
			}
		}
		var e Envelope
		_ = json.NewDecoder(r.Body).Decode(&e)
		printJSON(w, map[string]any{"event_id": e.EventID, "state": "apns_accepted"})
	}))
	defer server.Close()
	for i := 0; i < 3; i++ {
		p := testPair(id, server.URL)
		p.PairID = fmt.Sprintf("pair_fanout_%020d", i)
		if i == 2 {
			p.Status = "pending"
		}
		if err := s.savePair(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	result, err := notify(context.Background(), s, API{server.Client()}, "", nil, "", true)
	if err != nil || len(result) != 2 || calls.Load() != 2 {
		t.Fatal("active fanout failed", len(result), calls.Load(), err)
	}
}
func TestOutboxCorruptionNeverResets(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	l := fixtureLocator()
	l.InstallationID = id
	d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "outbox-v1", deliveryKey(p.PairID, d.EventID)[:2]+".json")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = outboxSummary(context.Background(), s, ""); err == nil {
		t.Fatal("missing shard silently reset")
	}
	if _, err = enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, ""); err == nil {
		t.Fatal("missing shard recreated event")
	}
}

func TestLegacyNativeReplayDoesNotGainNewIdentityAfterMoveOrExpiry(t *testing.T) {
	s := testStore(t)
	id, _ := s.installation()
	p := testPair(id, "https://api.invalid")
	old := fixtureLocator()
	old.InstallationID = id
	rawIdentity := digest("native-source-occurrence")
	plain, _ := json.Marshal(old)
	legacyIdentity := digest(rawIdentity + "\x00" + string(plain))
	key, _ := decode64(p.EncryptionKey, 32)
	e, err := seal(key, Envelope{V: 1, PairID: p.PairID, EventID: digest(legacyIdentity + "\x00" + p.PairID), Kind: "turn_completed", ExpiresAt: time.Now().Unix() - 3600}, old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.writeJSON("events.json", map[string]cachedEnvelope{p.PairID + ":" + e.EventID: {e, digest(string(plain))}}); err != nil {
		t.Fatal(err)
	}
	moved := old
	moved.OccupantID = "tmux-proc-v1:" + strings.Repeat("a", 64)
	moved.ViewID = "@9"
	d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", nativeTargetIdentity(rawIdentity, moved), moved, "opencode", rawIdentity)
	if err != nil || d.State != "legacy_unknown" || d.EventID != e.EventID || d.Expires != e.ExpiresAt || d.Envelope == nil || *d.Envelope != e {
		t.Fatal("legacy source resurrected", d.State, err)
	}
	if _, err = os.Stat(filepath.Join(s.Dir, "events.json")); !os.IsNotExist(err) {
		t.Fatal("obsolete cache retained after committed migration")
	}
	records, err := outboxRecords(context.Background(), s, "", true)
	if err != nil || len(records) != 1 || records[0].State != "legacy_unknown" {
		t.Fatal("legacy queued", err)
	}
}

func TestOutboxConfirmedPairRetirement(t *testing.T) {
	for _, reason := range []string{"revoked", "expired"} {
		t.Run(reason, func(t *testing.T) {
			s := testStore(t)
			id, _ := s.installation()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(410)
				printJSON(w, map[string]string{"error": "pair_" + reason})
			}))
			defer server.Close()
			p := testPair(id, server.URL)
			if err := s.savePair(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			l := fixtureLocator()
			l.InstallationID = id
			d, err := enqueueDelivery(context.Background(), s, p, "turn_completed", "source", l, "codex")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = deliverOne(context.Background(), s, API{server.Client()}, d)
			pairs, err := s.pairs()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := pairs[p.PairID]; ok {
				t.Fatal("confirmed retired credentials remain")
			}
			var history []retiredPair
			if err := s.readJSON("pair-history.json", &history); err != nil || len(history) != 1 || history[0].Status != reason {
				t.Fatal("retirement reason lost", err)
			}
			records, err := outboxRecords(context.Background(), s, "", false)
			if err != nil || len(records) != 1 || records[0].State != "permanent_failed" || records[0].Envelope != nil {
				t.Fatal("retired event retained for delivery", err)
			}
		})
	}
}
