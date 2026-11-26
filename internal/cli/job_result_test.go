package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type quickResultVector struct {
	Key     string             `json:"key"`
	AAD     string             `json:"aad"`
	Receipt quickResultReceipt `json:"receipt"`
	Payload quickResultPlain   `json:"payload"`
}

func loadQuickResultVector(t *testing.T) quickResultVector {
	t.Helper()
	raw, err := os.ReadFile(testdataDir + "/quick-command-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector quickResultVector
	if err := interactionJSON(raw, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

func TestQuickResultVectorAndAuthentication(t *testing.T) {
	vector := loadQuickResultVector(t)
	id := vector.Receipt.quickResultIdentity
	key, err := decode64(vector.Key, 32)
	if err != nil || string(id.aad()) != vector.AAD || !vector.Payload.valid() {
		t.Fatal("invalid shared fixture or AAD")
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	fresh, err := sealQuickResult(key, id, vector.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Nonce == vector.Receipt.Nonce {
		t.Fatal("fresh result reused the fixture nonce")
	}
	for _, r := range []quickResultReceipt{vector.Receipt, fresh} {
		nonce, err := decode64(r.Nonce, 12)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := base64.StdEncoding.Strict().DecodeString(r.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := gcm.Open(nil, nonce, sealed, id.aad())
		if err != nil {
			t.Fatal(err)
		}
		var payload quickResultPlain
		if interactionJSON(raw, &payload) != nil || !reflect.DeepEqual(payload, vector.Payload) || r.OccurredAt != payload.OccurredAt {
			t.Fatal("decrypted fixture fields disagree")
		}
		for _, change := range []func(*quickResultIdentity){
			func(v *quickResultIdentity) { v.InstallationID += "_other" },
			func(v *quickResultIdentity) { v.JobID = id.CommandID },
			func(v *quickResultIdentity) { v.CommandID = id.JobID },
			func(v *quickResultIdentity) { v.RequestDigest = strings.Repeat("0", 64) },
		} {
			other := id
			change(&other)
			if _, err := gcm.Open(nil, nonce, sealed, other.aad()); err == nil {
				t.Fatal("changed run authenticated")
			}
		}
		sealed[len(sealed)-1] ^= 1
		if _, err := gcm.Open(nil, nonce, sealed, id.aad()); err == nil {
			t.Fatal("modified result authenticated")
		}
	}
}

func TestQuickResultTailAndState(t *testing.T) {
	v := loadQuickResultVector(t)
	key := make([]byte, 32)
	p := v.Payload
	p.Output = bytes.Repeat([]byte{0xff}, 4096)
	p.StderrMask = bytes.Repeat([]byte{0xaa}, 512)
	if _, err := sealQuickResult(key, v.Receipt.quickResultIdentity, p); err != nil {
		t.Fatal("largest permitted alternating-stream tail:", err)
	}
	p.Output, p.StderrMask = []byte{}, []byte{}
	if _, err := sealQuickResult(key, v.Receipt.quickResultIdentity, p); err != nil {
		t.Fatal("empty output:", err)
	}
	code, signal := 0, 9
	for _, change := range []func(*quickResultPlain){
		func(p *quickResultPlain) { p.State = "running" },
		func(p *quickResultPlain) { p.State = "succeeded" },
		func(p *quickResultPlain) { p.ExitCode = &code },
		func(p *quickResultPlain) { p.State = "signaled"; p.Signal = &signal },
		func(p *quickResultPlain) { p.StartedAt = p.OccurredAt + 1 },
		func(p *quickResultPlain) { p.Output = make([]byte, 4097); p.StderrMask = make([]byte, 513) },
		func(p *quickResultPlain) { p.StderrMask = []byte{0xff} },
		func(p *quickResultPlain) { p.StderrMask = nil },
	} {
		p := v.Payload
		change(&p)
		if _, err := sealQuickResult(key, v.Receipt.quickResultIdentity, p); err == nil {
			t.Fatal("invalid result sealed")
		}
	}
}

func TestQuickResultReadOnlyCommand(t *testing.T) {
	v := loadQuickResultVector(t)
	dir := filepath.Join(t.TempDir(), "private")
	t.Setenv("MOBTERM_CONFIG_DIR", dir)
	s := Store{dir}
	id := v.Receipt.quickResultIdentity
	request := func(in []byte, want string) []byte {
		t.Helper()
		var out, diag bytes.Buffer
		code := run([]string{"job", "output", "--stdin-frame"}, bytes.NewReader(in), &out, &diag)
		if want == "" {
			if code != 0 || diag.Len() != 0 {
				t.Fatalf("read failed: %d %s", code, diag.String())
			}
		} else {
			var failure struct {
				V     int    `json:"v"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if code != 1 || interactionJSON(out.Bytes(), &failure) != nil || failure.V != 1 || failure.OK || failure.Error != want {
				t.Fatalf("expected %s, got %s", want, out.Bytes())
			}
			if diag.String() != "mobterm-cli: "+want+"\n" {
				t.Fatal("diagnostic exposed more than the failure code")
			}
		}
		return out.Bytes()
	}
	request(framed(id), "result_unavailable")
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("query created state")
	}
	if err := ensureDir(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := s.writeJSON("installation.json", storedInstallation{1, id.InstallationID}); err != nil {
		t.Fatal(err)
	}
	request(framed(id), "result_unavailable")
	request(append(framed(id), '!'), "invalid_request")
	rawRequest, _ := json.Marshal(id)
	conflicting := bytes.Replace(rawRequest, []byte(`"v":1`), []byte(`"V":2,"v":1`), 1)
	request(framed(json.RawMessage(conflicting)), "invalid_request")
	request([]byte{0, 0, 64, 1}, "invalid_request")
	other := id
	other.InstallationID += "_other"
	request(framed(other), "identity_mismatch")
	other = id
	other.JobID = "../other-job"
	request(framed(other), "invalid_request")

	now := time.Now()
	p := v.Payload
	p.StartedAt, p.OccurredAt = now.Unix()-2, now.Unix()-1
	key, _ := decode64(v.Key, 32)
	r, err := sealQuickResult(key, id, p)
	if err != nil {
		t.Fatal(err)
	}
	name := "quick-result-" + id.JobID + ".json"
	if err := s.writeJSON(name, r); err != nil {
		t.Fatal(err)
	}
	var got quickResultReceipt
	if json.Unmarshal(request(framed(id), ""), &got) != nil || got != r {
		t.Fatal("read did not return the exact sealed receipt")
	}
	other = id
	other.RequestDigest = strings.Repeat("0", 64)
	request(framed(other), "identity_mismatch")
	expiry := time.Unix(r.OccurredAt, 0).Add(quickResultLifetime)
	peek := func(at time.Time) error {
		_, err := readQuickResult(context.Background(), s, bytes.NewReader(framed(id)), func() time.Time { return at })
		return err
	}
	if err := peek(expiry.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := peek(expiry); err == nil || err.Error() != "result_expired" {
		t.Fatal("expiry boundary ignored")
	}
	path := filepath.Join(dir, name)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	request(framed(id), "invalid_result")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "installation.json"), path); err != nil {
		t.Fatal(err)
	}
	request(framed(id), "invalid_result")
}
