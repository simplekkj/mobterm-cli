package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestMessageSummaryGolden(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			nonce := make([]byte, 12)
			for i := range nonce {
				nonce[i] = byte(64 + version*16 + i)
			}
			e := Envelope{V: version, PairID: "pair_fixture_000000000000000001", EventID: "event_summary_fixture_00000001", Kind: "turn_completed", ExpiresAt: 2000000000}
			l := fixtureLocator()
			summary := "已修复会话归属。Tests passed; awaiting review. 🔒"
			var err error
			if version == 1 {
				e, err = sealAgentSummary(key, e, l, nonce, "codex", summary)
			} else {
				e.StreamID = "stream_summary_fixture_0000001"
				e.Revision = 7
				e.CapabilityGeneration = 1
				p := RunPayload{V: 2, Locator: l, Agent: "codex", SessionID: "session_fixture", RunID: "run_fixture", StreamID: e.StreamID, Revision: e.Revision, State: "turn_completed", OccurredAt: 1999999900, Tools: []RunTool{}, Summary: summary}
				e, err = sealRun(key, e, p, nonce)
			}
			if err != nil {
				t.Fatal(err)
			}
			block, _ := aes.NewCipher(key)
			g, _ := cipher.NewGCM(block)
			encrypted, _ := base64.StdEncoding.DecodeString(e.Ciphertext)
			plain, err := g.Open(nil, nonce, encrypted, e.aad())
			if err != nil {
				t.Fatal(err)
			}
			f := golden{version, base64.StdEncoding.EncodeToString(key), string(plain), string(e.aad()), e}
			raw, _ := json.MarshalIndent(f, "", "  ")
			path := fmt.Sprintf("testdata/protocol-v%d-summary.json", version)
			if os.Getenv("UPDATE_SUMMARY_GOLDEN") == "1" {
				if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(bytes.TrimSpace(expected), raw) {
				t.Fatal("summary golden mismatch", err)
			}
			for _, mutation := range []func(*Envelope){func(e *Envelope) { e.Kind = "needs_input" }, func(e *Envelope) { e.EventID = "other_event_fixture_00000001" }, func(e *Envelope) { e.PairID = "other_pair_fixture_000000001" }} {
				altered := e
				mutation(&altered)
				if _, err = g.Open(nil, nonce, encrypted, altered.aad()); err == nil {
					t.Fatal("unauthenticated summary")
				}
			}
		})
	}
}
