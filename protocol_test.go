package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func fixtureLocator() Locator {
	return Locator{V: 1, InstallationID: "install_fixture_0000000000000001", Backend: "tmux", Namespace: "/tmp/tmux-1000/default", ServerIdentity: "1234:1700000000:1700000001", WorkspaceID: "$1", ViewID: "@2", PaneID: "%3"}
}

type golden struct {
	Version   int      `json:"fixture_version"`
	Key       string   `json:"key"`
	Plaintext string   `json:"plaintext"`
	AAD       string   `json:"aad"`
	Envelope  Envelope `json:"envelope"`
}

func TestGolden(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	nonce := make([]byte, 12)
	for i := range nonce {
		nonce[i] = byte(i + 32)
	}
	l := fixtureLocator()
	e, err := seal(key, Envelope{V: 1, PairID: "pair_fixture_000000000000000001", EventID: "event_fixture_00000000000000001", Kind: "turn_completed", ExpiresAt: 2000000000}, l, nonce)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(l)
	expected := golden{1, base64.StdEncoding.EncodeToString(key), string(plain), string(e.aad()), e}
	b, _ := json.MarshalIndent(expected, "", "  ")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err = os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile("testdata/protocol-v1.json", append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := os.ReadFile("testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(fixture), b) {
		t.Fatal("golden fixture mismatch")
	}
	now := time.Unix(e.ExpiresAt-60, 0)
	got, err := openEnvelope(key, e, now)
	if err != nil || got != l {
		t.Fatalf("roundtrip: %v", err)
	}
	mutations := []func(*Envelope){func(e *Envelope) { e.V = 2 }, func(e *Envelope) { e.PairID = "other_pair_fixture000000000000" }, func(e *Envelope) { e.EventID = "other_event_fixture0000000000" }, func(e *Envelope) { e.Kind = "needs_input" }, func(e *Envelope) { e.ExpiresAt++ }, func(e *Envelope) { e.Nonce = base64.StdEncoding.EncodeToString(make([]byte, 12)) }, func(e *Envelope) { e.Nonce = "bad" }, func(e *Envelope) { e.Ciphertext = "A" + e.Ciphertext[1:] }}
	for i, mutate := range mutations {
		x := e
		mutate(&x)
		if _, err := openEnvelope(key, x, now); err == nil {
			t.Fatalf("mutation %d accepted", i)
		}
	}
	bad := append([]byte{}, key...)
	bad[0]++
	if _, err = openEnvelope(bad, e, now); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err = openEnvelope(key, e, time.Unix(e.ExpiresAt, 0)); err == nil {
		t.Fatal("expired event accepted")
	}
}
func framed(v any) []byte {
	b, _ := json.Marshal(v)
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	return append(out, b...)
}
func TestFrames(t *testing.T) {
	for _, b := range [][]byte{nil, {0, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 0, 2, '{'}} {
		if readFrame(bytes.NewReader(b), new(SetupPacket)) == nil {
			t.Fatal("bad frame accepted")
		}
	}
	if readFrame(bytes.NewReader(framed(map[string]any{"unknown": "secret"})), new(SetupPacket)) == nil {
		t.Fatal("unknown frame field accepted")
	}
}
