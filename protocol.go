package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxFrame = 16384
const maxCiphertext = 2800 // Includes the 16-byte GCM tag. API separately checks the full 4096-byte APNs payload.
var opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var strongTmuxOccupant = regexp.MustCompile(`^tmux-proc-v1:[a-f0-9]{64}$`)
var simpleID = regexp.MustCompile(`^[A-Za-z0-9_:.%-]{1,128}$`)

type SetupPacket struct {
	V              int    `json:"v"`
	APIURL         string `json:"api_url"`
	PairID         string `json:"pair_id"`
	InstallationID string `json:"installation_id"`
	SendToken      string `json:"send_token"`
	EncryptionKey  string `json:"encryption_key"`
}
type Locator struct {
	V              int    `json:"v"`
	InstallationID string `json:"installation_id"`
	Backend        string `json:"backend"`
	Namespace      string `json:"namespace"`
	ServerIdentity string `json:"server_identity"`
	WorkspaceID    string `json:"workspace_id"`
	ViewID         string `json:"view_id"`
	PaneID         string `json:"pane_id"`
	TerminalID     string `json:"terminal_id,omitempty"`
	OccupantID     string `json:"occupant_id,omitempty"`
}
type Envelope struct {
	V          int    `json:"v"`
	PairID     string `json:"pair_id"`
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	ExpiresAt  int64  `json:"expires_at"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func (e Envelope) aad() []byte {
	return []byte(fmt.Sprintf("mobterm/1\n%s\n%s\n%s\n%d", e.PairID, e.EventID, e.Kind, e.ExpiresAt))
}
func decode64(s string, n int) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != n || base64.StdEncoding.EncodeToString(b) != s {
		return nil, errors.New("invalid base64 size")
	}
	return b, nil
}
func (p SetupPacket) validate() error {
	if p.V != 1 || !opaqueID.MatchString(p.PairID) || !opaqueID.MatchString(p.InstallationID) {
		return errors.New("invalid setup identity or version")
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(p.SendToken)
	if err != nil || len(token) != 32 {
		return errors.New("invalid send credential")
	}
	if _, err = decode64(p.EncryptionKey, 32); err != nil {
		return errors.New("invalid encryption key")
	}
	u, err := url.Parse(p.APIURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("api_url must be an HTTPS origin")
	}
	return nil
}
func (l Locator) validate() error {
	if l.V != 1 || !opaqueID.MatchString(l.InstallationID) {
		return errors.New("invalid locator identity")
	}
	if l.Backend == "test" {
		if l.Namespace+l.ServerIdentity+l.WorkspaceID+l.ViewID+l.PaneID+l.TerminalID+l.OccupantID != "" {
			return errors.New("test must have no target")
		}
		return nil
	}
	if (l.Backend != "tmux" && l.Backend != "herdr") || len(l.Namespace) > 512 || strings.ContainsAny(l.Namespace, "\x00\r\n\t") || l.Namespace == "" || len(l.ServerIdentity) > 200 || l.ServerIdentity == "" {
		return errors.New("invalid locator backend or namespace")
	}
	if l.Backend == "tmux" {
		if l.OccupantID != "" && !strongTmuxOccupant.MatchString(l.OccupantID) {
			return errors.New("invalid tmux occupant")
		}
		if !tmuxSession.MatchString(l.WorkspaceID) || !tmuxWindow.MatchString(l.ViewID) || !tmuxPane.MatchString(l.PaneID) {
			return errors.New("invalid tmux target ID")
		}
	} else {
		for _, id := range []string{l.WorkspaceID, l.ViewID, l.PaneID} {
			if !simpleID.MatchString(id) {
				return errors.New("invalid target ID")
			}
		}
	}
	if l.Backend == "herdr" && (!simpleID.MatchString(l.TerminalID) || l.OccupantID != l.TerminalID) {
		return errors.New("invalid terminal identity")
	}
	return nil
}
func (e Envelope) validate(now time.Time) error {
	if e.V != 1 || !opaqueID.MatchString(e.PairID) || !opaqueID.MatchString(e.EventID) || (e.Kind != "turn_completed" && e.Kind != "needs_input" && e.Kind != "test") || e.ExpiresAt <= now.Unix() || e.ExpiresAt > now.Add(24*time.Hour).Unix() {
		return errors.New("invalid or expired envelope")
	}
	if _, err := decode64(e.Nonce, 12); err != nil {
		return err
	}
	b, err := base64.StdEncoding.Strict().DecodeString(e.Ciphertext)
	if err != nil || len(b) < 16 || len(b) > maxCiphertext {
		return errors.New("invalid ciphertext")
	}
	return nil
}
func seal(key []byte, e Envelope, l Locator, nonce []byte) (Envelope, error) {
	return sealAgent(key, e, l, nonce, "")
}
func sealAgent(key []byte, e Envelope, l Locator, nonce []byte, agent string) (Envelope, error) {
	if len(key) != 32 {
		return e, errors.New("AES-256 key required")
	}
	if err := l.validate(); err != nil {
		return e, err
	}
	if nonce == nil {
		nonce = make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return e, err
		}
	}
	if len(nonce) != 12 {
		return e, errors.New("invalid nonce")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return e, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return e, err
	}
	if agent != "" && agent != "codex" && agent != "claude" && agent != "opencode" && agent != "pi" {
		return e, errors.New("invalid agent")
	}
	plain, err := json.Marshal(struct {
		Locator
		Agent string `json:"agent,omitempty"`
	}{l, agent})
	if err != nil {
		return e, err
	}
	e.Nonce = base64.StdEncoding.EncodeToString(nonce)
	e.Ciphertext = base64.StdEncoding.EncodeToString(g.Seal(nil, nonce, plain, e.aad()))
	if len(plain)+g.Overhead() > maxCiphertext {
		return e, errors.New("encrypted target exceeds APNs budget")
	}
	return e, nil
}
func openEnvelope(key []byte, e Envelope, now time.Time) (Locator, error) {
	var l Locator
	if err := e.validate(now); err != nil {
		return l, err
	}
	return authenticateEnvelope(key, e)
}

// Authentication-only decoding is used exclusively for legacy deduplication,
// never navigation or dispatch; the stored original expiry remains unchanged.
func authenticateEnvelope(key []byte, e Envelope) (Locator, error) {
	var l Locator
	if _, err := decode64(e.Nonce, 12); err != nil {
		return l, err
	}
	c, err := base64.StdEncoding.Strict().DecodeString(e.Ciphertext)
	if err != nil || len(c) < 16 || len(c) > maxCiphertext {
		return l, errors.New("invalid ciphertext")
	}
	if len(key) != 32 {
		return l, errors.New("invalid key")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return l, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return l, err
	}
	n, _ := decode64(e.Nonce, 12)
	p, err := g.Open(nil, n, c, e.aad())
	if err != nil {
		return l, errors.New("authentication failed")
	}
	if err = json.Unmarshal(p, &l); err != nil {
		return l, err
	}
	return l, l.validate()
}
func readFrame(r io.Reader, v any) error {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return errors.New("missing frame header")
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxFrame {
		return errors.New("invalid frame length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return errors.New("truncated frame")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid frame JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("multiple frame values")
	}
	return nil
}

func readFrameContext(ctx context.Context, r io.Reader, v any) error {
	finished := make(chan error, 1)
	go func() { finished <- readFrame(r, v) }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		return errors.New("frame input timed out or cancelled")
	}
}
func intText(s string) bool { _, err := strconv.ParseUint(s, 10, 64); return err == nil }
