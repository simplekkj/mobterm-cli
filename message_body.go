package main

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const bodyPurpose = "mobterm/message-body/1"
const bodyLimit = 512 << 10
const bodyTTL = int64(7 * 86400)
const bodyFileLimit = 500
const bodyTotalLimit = 64 << 20

type assistantBody struct {
	Text     string
	TooLarge bool
}

func newAssistantBody(text string) assistantBody {
	if !utf8.ValidString(text) {
		return assistantBody{}
	}
	if len(text) > bodyLimit {
		return assistantBody{TooLarge: true}
	}
	clean := cleanTerminalText(text)
	// Some hosts mix a leading tagged reasoning block into a text field.
	// Never archive ambiguous private content as an attributed final answer.
	for {
		leading := strings.TrimLeft(clean, " \t\n\r")
		tag := ""
		for _, name := range []string{"think", "thinking", "analysis", "reasoning"} {
			if strings.HasPrefix(leading, "<"+name+">") {
				tag = name
				break
			}
		}
		if tag == "" {
			break
		}
		_, rest, ok := strings.Cut(leading, "</"+tag+">")
		if !ok {
			return assistantBody{}
		}
		clean = strings.TrimLeft(rest, "\r\n")
	}
	return assistantBody{Text: clean}
}

type bodyRef struct {
	Source  string `json:"source,omitempty"`
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Created int64  `json:"created"`
	Bytes   int    `json:"bytes,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

type bodyBinding struct {
	Ref            bodyRef `json:"ref"`
	EnvelopeDigest string  `json:"envelope_digest"`
}

func envelopeBodyDigest(e Envelope) string {
	return digest(string(e.aad()) + "\n" + e.Nonce + "\n" + e.Ciphertext)
}

func bindBody(ref *bodyRef, e Envelope) *bodyBinding {
	if ref == nil || e.Kind != "turn_completed" {
		return nil
	}
	return &bodyBinding{*ref, envelopeBodyDigest(e)}
}

func (s Store) bodyDir() string { return filepath.Join(s.Dir, "message-bodies-v1") }

var bodyNamePattern = regexp.MustCompile(`^([0-9]{10,12})\.([a-f0-9]{64})\.([a-f0-9]{64})\.([a-zA-Z0-9_-]{43})$`)
var bodyHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func bodyName(ref bodyRef, p Pair) string {
	return fmt.Sprintf("%d.%s.%s.%s", ref.Created, digest(p.InstallationID), digest(strings.TrimSuffix(p.APIURL, "/")+"\n"+p.PairID), ref.ID)
}

func checkBodyDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe_message_body_directory")
	}
	return nil
}

// Store.lock protects writers, including eviction and pair retirement. Filenames
// carry the original creation time, so a restart never extends retention.
func (s Store) pruneBodies(installation, retiredPair string, now int64, addCount, addBytes int) error {
	dir := s.bodyDir()
	if err := checkBodyDir(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type file struct {
		name    string
		created int64
		bytes   int
	}
	var keep []file
	total := 0
	for _, entry := range entries {
		name := entry.Name()
		// Interrupted atomicWrite leaves an uncommitted private temporary file.
		if strings.HasPrefix(name, ".mobterm-write-") {
			if err = os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
			continue
		}
		match := bodyNamePattern.FindStringSubmatch(name)
		if match == nil {
			return errors.New("invalid_message_body_directory")
		}
		created, _ := strconv.ParseInt(match[1], 10, 64)
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("unsafe_message_body_file")
		}
		if created+bodyTTL <= now || (installation != "" && match[2] != digest(installation)) || match[3] == retiredPair {
			if err = os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
			continue
		}
		keep = append(keep, file{name, created, int(info.Size())})
		total += int(info.Size())
	}
	sort.Slice(keep, func(i, j int) bool {
		if keep[i].created != keep[j].created {
			return keep[i].created < keep[j].created
		}
		return keep[i].name < keep[j].name
	})
	for len(keep)+addCount > bodyFileLimit || total+addBytes > bodyTotalLimit {
		if err = os.Remove(filepath.Join(dir, keep[0].name)); err != nil {
			return err
		}
		total -= keep[0].bytes
		keep = keep[1:]
	}
	return nil
}

// A failed optional archive never suppresses the base notification. Only the
// reference committed with the first durable intent/delivery can be looked up.
func (s Store) freezeBody(p Pair, b assistantBody, now int64) *bodyRef {
	r := &bodyRef{Created: now, Status: "missing"}
	n := len(b.Text)
	if b.TooLarge || n > bodyLimit {
		r.Status = "too_large"
		return r
	}
	if n == 0 {
		return r
	}
	r.Status = "save_failed"
	if ensureDir(s.bodyDir(), true) != nil || s.pruneBodies(p.InstallationID, "", now, 1, n) != nil {
		return r
	}
	r.ID = randomID()
	if err := atomicWrite(filepath.Join(s.bodyDir(), bodyName(*r, p)), []byte(b.Text), 0600); err != nil {
		r.ID = ""
		return r
	}
	r.Status = "available"
	r.Bytes = n
	r.Digest = digest(b.Text)
	return r
}

type bodyIdentity struct {
	V            int    `json:"v"`
	Origin       string `json:"api_url"`
	Installation string `json:"installation_id"`
	Pair         string `json:"pair_id"`
	Event        string `json:"event_id"`
	Envelope     string `json:"envelope_digest"`
	Nonce        string `json:"nonce"`
}
type bodyRequest struct {
	bodyIdentity
	Expires int64  `json:"expires_at"`
	Proof   string `json:"proof"`
}
type bodyResponse struct {
	bodyIdentity
	Status  string `json:"status"`
	Bytes   int    `json:"byte_count,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Base64  string `json:"body_base64,omitempty"`
	Expires int64  `json:"expires_at,omitempty"`
}

func bodyMAC(key []byte, f bodyRequest) (string, error) {
	derived, err := hkdf.Key(sha256.New, key, []byte(f.Pair), bodyPurpose, 32)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, derived)
	fmt.Fprintf(m, "%s\n%s\n%s\n%s\n%s\n%s\n%s\n%d", bodyPurpose, f.Origin, f.Installation, f.Pair, f.Event, f.Envelope, f.Nonce, f.Expires)
	return base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}

// A reader joins the existing writer lock without creating the store or a lock
// file. Holding it through the read linearizes against revoke and eviction.
func (s Store) lockBodyRead(ctx context.Context) (func(), error) {
	if err := checkBodyDir(s.Dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func readMessageBody(ctx context.Context, s Store, in io.Reader) (bodyResponse, error) {
	var f bodyRequest
	if err := readFrameContext(ctx, in, &f); err != nil {
		return bodyResponse{}, err
	}
	res := bodyResponse{bodyIdentity: f.bodyIdentity, Status: "denied"}
	now := time.Now().Unix()
	identity := RetirePacket{V: f.V, APIURL: f.Origin, PairID: f.Pair, InstallationID: f.Installation}
	if identity.validate() != nil || strings.ContainsAny(f.Origin, "\r\n") || strings.HasSuffix(f.Origin, "/") || !opaqueID.MatchString(f.Event) || !bodyHashPattern.MatchString(f.Envelope) || !opaqueID.MatchString(f.Nonce) || f.Expires <= now || f.Expires > now+60 {
		return bodyResponse{}, errors.New("invalid_message_body_request")
	}
	unlock, err := s.lockBodyRead(ctx)
	if err != nil {
		res.Status = "unavailable"
		return res, nil
	}
	defer unlock()
	id, err := s.existingInstallation()
	if err != nil {
		res.Status = "unavailable"
		return res, nil
	}
	if id != f.Installation {
		return res, nil
	}
	pairs, err := s.pairs()
	if err != nil {
		res.Status = "unavailable"
		return res, nil
	}
	p, ok := pairs[f.Pair]
	if !ok || p.PairID != f.Pair || p.Status != "active" || p.InstallationID != id || strings.TrimSuffix(p.APIURL, "/") != f.Origin {
		return res, nil
	}
	var retired []retiredPair
	if err = s.readJSON("pair-history.json", &retired); err != nil && !errors.Is(err, os.ErrNotExist) {
		res.Status = "unavailable"
		return res, nil
	}
	for _, marker := range retired {
		if marker.PairRef == digest(f.Pair) {
			return res, nil
		}
	}
	key, err := decode64(p.EncryptionKey, 32)
	if err != nil {
		return res, nil
	}
	want, err := bodyMAC(key, f)
	if err != nil || !hmac.Equal([]byte(f.Proof), []byte(want)) || f.Expires <= time.Now().Unix() {
		return res, nil
	}
	k := deliveryKey(f.Pair, f.Event)
	if err = checkBodyDir(filepath.Join(s.Dir, "outbox-v1")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			res.Status = "missing"
		} else {
			res.Status = "unavailable"
		}
		return res, nil
	}
	sh, err := s.loadShard(k)
	if err != nil {
		res.Status = "unavailable"
		return res, nil
	}
	d, ok := sh.Records[k]
	if !ok || d.Body == nil {
		res.Status = "missing"
		return res, nil
	}
	if d.Body.EnvelopeDigest != f.Envelope {
		return res, nil
	}
	r := d.Body.Ref
	if r.Created+bodyTTL <= time.Now().Unix() {
		res.Status = "missing"
		return res, nil
	}
	switch r.Status {
	case "too_large":
		res.Status = "too_large"
		return res, nil
	case "missing", "save_failed":
		res.Status = "missing"
		return res, nil
	case "available":
	default:
		res.Status = "unavailable"
		return res, nil
	}
	if !opaqueID.MatchString(r.ID) || !bodyHashPattern.MatchString(r.Digest) || r.Bytes <= 0 || r.Bytes > bodyLimit {
		res.Status = "unavailable"
		return res, nil
	}
	err = checkBodyDir(s.bodyDir())
	var raw []byte
	var mode os.FileMode
	if err == nil {
		raw, mode, err = secureReadWithMode(filepath.Join(s.bodyDir(), bodyName(r, p)), bodyLimit)
	}
	if errors.Is(err, os.ErrNotExist) {
		res.Status = "missing"
		return res, nil
	}
	if err != nil || mode.Perm()&0077 != 0 || len(raw) != r.Bytes || !utf8.Valid(raw) || digest(string(raw)) != r.Digest || ctx.Err() != nil {
		res.Status = "unavailable"
		return res, nil
	}
	res.Status = "available"
	res.Base64 = base64.StdEncoding.EncodeToString(raw)
	res.Digest = r.Digest
	res.Bytes = r.Bytes
	res.Expires = r.Created + bodyTTL
	return res, nil
}
