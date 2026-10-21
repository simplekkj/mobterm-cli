package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestExtendedProtocolFixture(t *testing.T) {
	var f struct {
		FixtureVersion int      `json:"fixture_version"`
		Key            string   `json:"key"`
		Plaintext      string   `json:"plaintext"`
		AAD            string   `json:"aad"`
		Envelope       Envelope `json:"envelope"`
	}
	b, e := os.ReadFile("testdata/protocol-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(b, &f)
	var l Locator
	_ = json.Unmarshal([]byte(f.Plaintext), &l)
	l.OccupantID = "tmux-proc-v1:" + strings.Repeat("a", 64)
	key, _ := base64.StdEncoding.DecodeString(f.Key)
	nonce, _ := base64.StdEncoding.DecodeString(f.Envelope.Nonce)
	f.Envelope, e = sealAgent(key, f.Envelope, l, nonce, "codex")
	if e != nil {
		t.Fatal(e)
	}
	plain, _ := json.Marshal(struct {
		Locator
		Agent string `json:"agent"`
	}{l, "codex"})
	f.Plaintext = string(plain)
	expected, err := os.ReadFile("testdata/protocol-v1-extended.json")
	if err != nil {
		t.Fatal(err)
	}
	var want any
	var actual any
	_ = json.Unmarshal(expected, &want)
	b, _ = json.Marshal(f)
	_ = json.Unmarshal(b, &actual)
	if !reflect.DeepEqual(want, actual) {
		t.Fatal("extended AES-GCM vector changed")
	}
}
