package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const quickResultTailLimit = 4096
const quickResultPlainLimit = 8192
const quickResultLifetime = 7 * 24 * time.Hour

type quickResultIdentity struct {
	V              int    `json:"v"`
	InstallationID string `json:"installation_id"`
	JobID          string `json:"job_id"`
	CommandID      string `json:"command_id"`
	RequestDigest  string `json:"request_digest"`
}

func (i quickResultIdentity) valid() bool {
	return i.V == 1 && opaqueID.MatchString(i.InstallationID) &&
		notificationUUID.MatchString(i.JobID) && i.JobID == strings.ToLower(i.JobID) &&
		notificationUUID.MatchString(i.CommandID) && i.CommandID == strings.ToLower(i.CommandID) &&
		bodyHashPattern.MatchString(i.RequestDigest)
}

func (i quickResultIdentity) aad() []byte {
	return []byte(strings.Join([]string{"mobterm/quick-command-result/1", i.InstallationID, i.JobID, i.CommandID, i.RequestDigest}, "\n"))
}

// The byte mask preserves ordering and per-byte stream ownership even when
// stdout and stderr alternate 4096 times. Invalid UTF-8 is ordinary output.
type quickResultPlain struct {
	State      string `json:"state"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Signal     *int   `json:"signal,omitempty"`
	StartedAt  int64  `json:"started_at"`
	OccurredAt int64  `json:"occurred_at"`
	Truncated  bool   `json:"truncated"`
	Output     []byte `json:"output"`
	StderrMask []byte `json:"stderr_mask"`
}

func (p quickResultPlain) valid() bool {
	if p.StartedAt <= 0 || p.OccurredAt < p.StartedAt ||
		p.Output == nil || p.StderrMask == nil || len(p.Output) > quickResultTailLimit ||
		len(p.StderrMask) != (len(p.Output)+7)/8 {
		return false
	}
	if bits := len(p.Output) % 8; bits != 0 && p.StderrMask[len(p.StderrMask)-1]>>bits != 0 {
		return false
	}
	if p.ExitCode != nil && (*p.ExitCode < 0 || *p.ExitCode > 255 || p.Signal != nil) ||
		p.Signal != nil && (*p.Signal <= 0 || *p.Signal > 127) {
		return false
	}
	switch p.State {
	case "succeeded":
		return p.ExitCode != nil && *p.ExitCode == 0
	case "failed":
		return p.ExitCode != nil && *p.ExitCode != 0
	case "signaled":
		return p.Signal != nil
	case "timed_out", "stopped":
		return true
	default:
		return false
	}
}

type quickResultReceipt struct {
	quickResultIdentity
	OccurredAt int64  `json:"occurred_at"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func sealQuickResult(key []byte, id quickResultIdentity, p quickResultPlain) (quickResultReceipt, error) {
	if len(key) != 32 || !id.valid() || !p.valid() {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > quickResultPlainLimit {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return quickResultReceipt{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return quickResultReceipt{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return quickResultReceipt{}, err
	}
	sealed := gcm.Seal(nil, nonce, raw, id.aad())
	return quickResultReceipt{id, p.OccurredAt, base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(sealed)}, nil
}

// encoding/json also accepts case-insensitive aliases of struct fields. Match
// the wire spelling so repeated identities cannot enter under different keys.
func decodeQuickResultObject(raw []byte, dst any) error {
	var fields map[string]json.RawMessage
	if err := interactionJSON(raw, &fields); err != nil || fields == nil {
		return errors.New("invalid_result")
	}
	for key := range fields {
		if key != strings.ToLower(key) {
			return errors.New("invalid_result")
		}
	}
	return interactionJSON(raw, dst)
}

// This reader never creates the store or locks. An atomic record replacement
// presents one complete snapshot. Keep all errors free of local paths and data.
func readQuickResult(ctx context.Context, s Store, in io.Reader, clock func() time.Time) (quickResultReceipt, error) {
	var frame json.RawMessage
	var q quickResultIdentity
	if readStrictFrame(ctx, in, &frame, maxFrame) != nil || decodeQuickResultObject(frame, &q) != nil || !q.valid() {
		return quickResultReceipt{}, errors.New("invalid_request")
	}
	id, err := s.existingInstallation()
	if err != nil {
		return quickResultReceipt{}, errors.New("result_unavailable")
	}
	if id != q.InstallationID {
		return quickResultReceipt{}, errors.New("identity_mismatch")
	}
	path := filepath.Join(s.Dir, "quick-result-"+q.JobID+".json")
	raw, mode, err := secureReadWithMode(path, maxFrame)
	if errors.Is(err, os.ErrNotExist) {
		return quickResultReceipt{}, errors.New("result_unavailable")
	}
	var receipt quickResultReceipt
	if err != nil || mode&0077 != 0 || decodeQuickResultObject(raw, &receipt) != nil {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	if receipt.quickResultIdentity != q {
		return quickResultReceipt{}, errors.New("identity_mismatch")
	}
	now := clock()
	if receipt.OccurredAt <= 0 || receipt.OccurredAt > now.Unix() {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	if receipt.OccurredAt <= now.Add(-quickResultLifetime).Unix() {
		return quickResultReceipt{}, errors.New("result_expired")
	}
	if _, err := decode64(receipt.Nonce, 12); err != nil {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	c, err := base64.StdEncoding.Strict().DecodeString(receipt.Ciphertext)
	if err != nil || len(c) <= 16 || len(c) > quickResultPlainLimit+16 || base64.StdEncoding.EncodeToString(c) != receipt.Ciphertext {
		return quickResultReceipt{}, errors.New("invalid_result")
	}
	return receipt, nil
}
