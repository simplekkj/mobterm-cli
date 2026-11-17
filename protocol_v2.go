package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// V2 uses its own authenticated domain; the v1 wire contract is unchanged.
const maxV2Ciphertext = 2400

type RunTool struct {
	InvocationID string `json:"invocation_id"`
	Category     string `json:"category"`
	Status       string `json:"status"`
}
type ApprovalHint struct {
	RequestID string `json:"request_id"`
	Digest    string `json:"digest"`
	ExpiresAt int64  `json:"expires_at"`
}
type RunPayload struct {
	Summary         string           `json:"summary,omitempty"`
	Interaction     *InteractionHint `json:"interaction,omitempty"`
	ActiveToolCount int              `json:"active_tool_count"`
	ToolsTruncated  bool             `json:"tools_truncated"`
	V               int              `json:"v"`
	Locator         Locator          `json:"locator"`
	Agent           string           `json:"agent"`
	SessionID       string           `json:"session_id"`
	RunID           string           `json:"run_id"`
	StreamID        string           `json:"stream_id"`
	Revision        int64            `json:"revision"`
	State           string           `json:"state"`
	OccurredAt      int64            `json:"occurred_at"`
	Tools           []RunTool        `json:"tools"`
	Approval        *ApprovalHint    `json:"approval,omitempty"`
}

func (e Envelope) aadV2() []byte {
	domain := "reminder"
	if e.Kind == "state" {
		domain = "snapshot"
	}
	return []byte(fmt.Sprintf("mobterm/2\n%s\n%s\n%s\n%s\n%s\n%d\n%d\n%d", domain, e.PairID, e.EventID, e.Kind, e.StreamID, e.Revision, e.CapabilityGeneration, e.ExpiresAt))
}
func sealRun(key []byte, e Envelope, p RunPayload, nonce []byte) (Envelope, error) {
	if e.V != 2 || p.V != 2 || e.StreamID != p.StreamID || e.Revision != p.Revision || e.Revision <= 0 || e.CapabilityGeneration <= 0 || !opaqueID.MatchString(e.StreamID) {
		return e, errors.New("invalid_v2_identity")
	}
	if err := p.Locator.validate(); err != nil {
		return e, err
	}
	if len(key) != 32 {
		return e, errors.New("invalid_key")
	}
	if nonce == nil {
		nonce = make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return e, err
		}
	}
	if len(nonce) != 12 {
		return e, errors.New("invalid_nonce")
	}
	p.Summary = cleanMessageSummary(p.Summary)
	block, err := aes.NewCipher(key)
	if err != nil {
		return e, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return e, err
	}
	for {
		raw, err := json.Marshal(p)
		if err != nil {
			return e, err
		}
		if len(raw)+g.Overhead() <= maxV2Ciphertext {
			e.Nonce = base64.StdEncoding.EncodeToString(nonce)
			e.Ciphertext = base64.StdEncoding.EncodeToString(g.Seal(nil, nonce, raw, e.aad()))
			if e.Kind == "state" || notificationPayloadSize(e) <= 4096 {
				return e, nil
			}
			nonce = make([]byte, 12)
			if _, err = rand.Read(nonce); err != nil {
				return e, err
			}
		}
		if p.Summary != "" {
			p.Summary = truncateMessageSummary(p.Summary, len([]rune(p.Summary))/2, 512)
			continue
		}
		if len(p.Tools) > 0 {
			p.Tools = p.Tools[:len(p.Tools)-1]
			p.ToolsTruncated = true
			continue
		}
		return e, errors.New("v2_payload_too_large")
	}
}
