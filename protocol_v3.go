package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const quickCommandCompleted = "quick_command_completed"

var notificationUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// QuickCommandCompletion is the entire encrypted notification payload. Output
// and SSH/result credentials are carried by a separate result protocol.
type QuickCommandCompletion struct {
	V              int    `json:"v"`
	InstallationID string `json:"installation_id"`
	JobID          string `json:"job_id"`
	CommandID      string `json:"command_id"`
	CommandName    string `json:"command_name"`
	State          string `json:"state"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	Signal         *int   `json:"signal,omitempty"`
	OccurredAt     int64  `json:"occurred_at"`
}

func (c QuickCommandCompletion) valid() bool {
	if c.V != 1 || !opaqueID.MatchString(c.InstallationID) || !notificationUUID.MatchString(c.JobID) ||
		!notificationUUID.MatchString(c.CommandID) || c.OccurredAt <= 0 || !validQuickCommandName(c.CommandName) {
		return false
	}
	return validQuickCommandTermination(c.State, c.ExitCode, c.Signal)
}

func validQuickCommandName(name string) bool {
	return name != "" && len(name) <= 512 && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 120 &&
		strings.TrimFunc(name, quickCommandNameSpace) == name && cleanTerminalText(name) == name &&
		!strings.ContainsAny(name, "\t\r\n\u2028\u2029")
}

// Foundation's whitespacesAndNewlines also trims U+200B at the two ends.
func quickCommandNameSpace(r rune) bool { return unicode.IsSpace(r) || r == '\u200b' }

func validQuickCommandTermination(state string, exitCode, signal *int) bool {
	if exitCode != nil && (*exitCode < 0 || *exitCode > 255 || signal != nil) || signal != nil && (*signal < 1 || *signal > 127) {
		return false
	}
	switch state {
	case "succeeded":
		return exitCode != nil && *exitCode == 0
	case "failed":
		return exitCode != nil && *exitCode != 0
	case "signaled":
		return signal != nil
	case "timed_out", "stopped":
		return true
	default:
		return false
	}
}

// The sender obtains this short-lived receipt from the exact Pair's v2
// capabilities endpoint. Absent fields on an older API leave the gates closed.
type quickCommandPolicy struct {
	V                    int    `json:"v"`
	PairID               string `json:"pair_id"`
	InstallationID       string `json:"installation_id"`
	Enabled              bool   `json:"enabled"`
	Inbox                bool   `json:"inbox"`
	NotificationsEnabled bool   `json:"notifications_enabled"`
	Supported            bool   `json:"quick_command_v3"`
	Completed            bool   `json:"quick_command_completed"`
	CapabilityGeneration int64  `json:"quick_command_capability_generation"`
	PolicyGeneration     int64  `json:"quick_command_policy_generation"`
	ExpiresAt            int64  `json:"expires_at"`
}

// pairID and eventID come from the original job's optional notification intent.
// Derive both generations from one authenticated receipt and the key from the
// same active Pair. Retrying an existing outbox entry never calls this sealer.
func sealQuickCommand(pair Pair, pairID, eventID string, policy quickCommandPolicy, c QuickCommandCompletion, now int64, nonce []byte) (Envelope, error) {
	const maxGeneration = 1<<53 - 1
	if !opaqueID.MatchString(pairID) || !opaqueID.MatchString(eventID) || pair.Status != "active" ||
		pair.PairID != pairID || pair.InstallationID != c.InstallationID || policy.V != 2 ||
		policy.PairID != pairID || policy.InstallationID != c.InstallationID ||
		policy.ExpiresAt <= now || policy.ExpiresAt-now > 61 ||
		!policy.Enabled || !policy.Inbox || !policy.NotificationsEnabled || !policy.Supported || !policy.Completed ||
		policy.CapabilityGeneration < 1 || policy.CapabilityGeneration > maxGeneration ||
		policy.PolicyGeneration < 1 || policy.PolicyGeneration > maxGeneration {
		return Envelope{}, errors.New("quick_command_notification_unavailable")
	}
	if !c.valid() || c.OccurredAt > now || now-c.OccurredAt >= 3600 {
		return Envelope{}, errors.New("invalid_quick_command_completion")
	}
	e := Envelope{V: 3, Kind: quickCommandCompleted, PairID: pairID, EventID: eventID, ExpiresAt: c.OccurredAt + 3600,
		QuickCapabilityGeneration: policy.CapabilityGeneration, QuickPolicyGeneration: policy.PolicyGeneration}
	key, err := decode64(pair.EncryptionKey, 32)
	if err != nil {
		return Envelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, err
	}
	if nonce == nil {
		nonce = make([]byte, gcm.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return Envelope{}, err
		}
	}
	if len(nonce) != gcm.NonceSize() {
		return Envelope{}, errors.New("invalid_nonce")
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return Envelope{}, err
	}
	if len(plain)+gcm.Overhead() > 2400 {
		return Envelope{}, errors.New("quick_command_payload_too_large")
	}
	e.Nonce = base64.StdEncoding.EncodeToString(nonce)
	e.Ciphertext = base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, e.aad()))
	if notificationPayloadSize(e) > 4096 {
		return Envelope{}, errors.New("quick_command_payload_too_large")
	}
	return e, nil
}
