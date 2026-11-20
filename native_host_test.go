package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisteredHostRebindsOnlyUnclaimedMatchingTurn(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	storage := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(storage, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "restored.jsonl")
	if err := os.Link(storage, alias); err != nil {
		t.Fatal(err)
	}
	fx := newReplyFixtureWithStorage(t, storage)
	s := fx.s
	ctx := context.Background()
	config, err := nativeConfigPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	cli, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = changeNativeHooks(ctx, s, "pi", "install", config, cli, false); err != nil {
		t.Fatal(err)
	}
	var install nativeHookRecord
	if err = s.readJSON(nativeRecordName("pi"), &install); err != nil {
		t.Fatal(err)
	}
	r := fx.record
	r.ParentBirth = "original-host-exited"
	r.State = "expired"
	r.Expires = time.Now().Add(-30 * time.Minute).Unix()
	if err = saveReply(s, r); err != nil {
		t.Fatal(err)
	}
	f := nativeHostFrame{V: 1, Agent: r.Agent, Session: r.Session, Storage: alias, Turn: r.NativeTurn}
	register := func() (any, error) { return registerNativeHost(ctx, s, bytes.NewReader(interactionFrameBytes(f))) }
	if _, err = register(); err == nil {
		t.Fatal("uncredentialed caller enrolled a host")
	}
	f.Token = install.HostToken
	out, err := register()
	if err != nil || out.(map[string]string)["source_id"] != r.SourceID {
		t.Fatal("restored host did not find exact unclaimed source", err)
	}
	hosts, err := loadNativeHosts(s)
	if err != nil || len(hosts) != 1 {
		t.Fatal("missing host", err)
	}
	host := hosts[0]
	changed := host
	changed.PID = os.Getpid()
	changed.Birth, _ = processIdentity(changed.PID)
	if err = s.writeJSON(nativeHostsFile, []nativeHost{host, changed}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.writeJSON(wakeFile(r.SourceID), now.Unix()+5); err != nil {
		t.Fatal(err)
	}
	try := func(h nativeHost, turn string) (replyRecord, error) {
		v := r
		err := rebindReplyLocked(s, &v, h, turn, now)
		return v, err
	}
	if _, err = try(host, r.NativeTurn); err == nil {
		t.Fatal("selected one of two distinct live hosts")
	}
	if err = s.writeJSON(nativeHostsFile, hosts); err != nil {
		t.Fatal(err)
	}
	if _, err = try(host, "later-entry"); err == nil {
		t.Fatal("changed turn reopened original reply")
	}
	other := host
	other.Storage = digest("different file")
	if _, err = try(other, r.NativeTurn); err == nil {
		t.Fatal("different storage reopened original reply")
	}
	record, err := try(host, r.NativeTurn)
	if err != nil {
		t.Fatal(err)
	}
	if record.OwnerToken == r.OwnerToken || record.SourceID != r.SourceID || record.Locator != r.Locator || record.ParentBirth != host.Birth || record.ParentPID != host.PID || record.State != "pending" {
		t.Fatal("incorrect transfer of source or current owner")
	}
	frame := replySendFrame(fx.frames[0], record, "frozen text")
	record.State = "dispatching"
	record.Frame = &frame
	if err = saveReply(s, record); err != nil {
		t.Fatal(err)
	}
	out, err = register()
	if err != nil || out.(map[string]string)["source_id"] != "" {
		t.Fatal("frozen source offered to restored adapter", err)
	}
	r = record
	if _, err = try(host, r.NativeTurn); err == nil {
		t.Fatal("rebound a frozen decision")
	}
	// A previous foreground session may deliver its registration after /new
	// clears the same still-live PID. Preserve the newer empty activation.
	original := f
	f.Session, f.Storage, f.Turn, f.Epoch = "", "", "", 4
	if _, err = register(); err != nil {
		t.Fatal(err)
	}
	f = original
	f.Epoch = 3
	if _, err = register(); err == nil {
		t.Fatal("late registration revived a cleared session")
	}
	if nativeReplyOwnerCurrent(s, record) {
		t.Fatal("cleared activation retained ownership")
	}
	f.Epoch = 5
	if _, err = register(); err != nil || !nativeReplyOwnerCurrent(s, record) {
		t.Fatal("newer activation could not register", err)
	}
	if err = changeNativeHooks(ctx, s, "pi", "uninstall", config, cli, false); err != nil {
		t.Fatal(err)
	}
	if nativeReplyOwnerCurrent(s, record) {
		t.Fatal("uninstalled adapter retained migrated ownership")
	}
}
