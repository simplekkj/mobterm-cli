package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func quickTestStart(p Pair, n int, command string) quickStart {
	q := quickStart{quickResultIdentity: quickResultIdentity{V: 1, InstallationID: p.InstallationID, JobID: fmt.Sprintf("11111111-1111-4111-8111-%012d", n), CommandID: "22222222-2222-4222-8222-222222222222"},
		PairID: p.PairID, EventID: randomID(), CommandName: "回归 Shell", CommandB64: base64.StdEncoding.EncodeToString([]byte(command)), ResultKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
		TimeoutSeconds: 10, SubmitUntil: time.Now().Unix() + 120}
	q.RequestDigest = q.digest()
	return q
}
func quickTestPair(t *testing.T) (Store, Pair) {
	t.Helper()
	s := testStore(t)
	t.Setenv("MOBTERM_CONFIG_DIR", s.Dir)
	id, err := s.installation()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	t.Cleanup(srv.Close)
	p := testPair(id, srv.URL)
	if err := s.savePair(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return s, p
}

func waitQuickJob(t *testing.T, s Store, q quickStart) quickJobRecord {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		r, err := getQuickJob(s, q.quickResultIdentity)
		if err == nil && !quickRunning(r.State) && r.Delivery != "pending" {
			return r
		}
	}
	r, err := getQuickJob(s, q.quickResultIdentity)
	t.Fatalf("worker did not finish job %s, last state %s, stage %s, worker alive %t, child alive %t, read error %v", q.JobID, r.State, r.Delivery, sameQuickProcess(r.Worker, r.WorkerBirth), sameQuickProcess(r.Child, r.ChildBirth), err)
	return quickJobRecord{}
}

func TestQuickWorkerRunsOriginalCommand(t *testing.T) {
	s, p := quickTestPair(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	std := "输出-unique"
	q := quickTestStart(p, 1, "printf 'x\\n' >> product; sleep 0.5; printf '"+std+"'; printf 'tag-err' >&2; exit 7")
	if r, err := handoffQuickJob(t.Context(), exe, q); err != nil || r.State != "accepted" {
		t.Fatal("start", r.State, err)
	}
	if r, err := handoffQuickJob(t.Context(), exe, q); err != nil || r.State != "accepted" {
		t.Fatal("dedup", r.State, err)
	}
	// A client that lost the ACK retains this job identity. A mismatched
	// request or key cannot consume it.
	for _, alter := range []func(*quickStart){
		func(q *quickStart) { q.CommandName = "changed"; q.RequestDigest = q.digest() },
		func(q *quickStart) { q.ResultKey = base64.StdEncoding.EncodeToString(make([]byte, 32)) },
	} {
		other := q
		alter(&other)
		if _, err := handoffQuickJob(t.Context(), exe, other); err == nil || err.Error() != "identity_mismatch" {
			t.Fatal("changed request", err)
		}
	}
	r := waitQuickJob(t, s, q)
	if r.State != "failed" || r.ExitCode == nil || *r.ExitCode != 7 || r.Worker != 0 || r.Child != 0 || r.Delivery != "policy_unavailable" {
		t.Fatal("wrong terminal status", r.quickJobReply, r.Delivery)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "product")); string(b) != "x\n" {
		t.Fatal("command executed more than once")
	}
	out, err := readQuickResult(t.Context(), s, bytes.NewReader(interactionFrameBytes(q.quickResultIdentity)), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	plain := openQuickTestResult(t, q, out)
	var stdout, stderr []byte
	for i, b := range plain.Output {
		if plain.StderrMask[i/8]&(1<<uint(i%8)) != 0 {
			stderr = append(stderr, b)
		} else {
			stdout = append(stdout, b)
		}
	}
	if plain.State != "failed" || string(stdout) != std || string(stderr) != "tag-err" {
		t.Fatal("unexpected actual output")
	}
	files, _ := os.ReadDir(s.Dir)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Dir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{q.CommandB64, q.ResultKey, std, "tag-err"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatal("plaintext retained")
			}
		}
	}
}

func TestQuickSubmitDisconnectLosesOnlyAck(t *testing.T) {
	s, p := quickTestPair(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	command := exec.Command(exe, "job", "submit", "--stdin-frame")
	command.Dir = dir
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var prepared struct {
		V            int    `json:"v"`
		State        string `json:"state"`
		Installation string `json:"installation_id"`
		Challenge    string `json:"challenge"`
		Expires      int64  `json:"submit_until"`
	}
	line, err := bufio.NewReader(output).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &prepared) != nil || prepared.V != 1 || prepared.State != "prepared" || prepared.Installation != p.InstallationID {
		t.Fatal("prepare", err)
	}
	q := quickTestStart(p, 30, "sleep .5; printf once >> product")
	q.Challenge = prepared.Challenge
	q.SubmitUntil = prepared.Expires
	// Drop the acknowledgement path before sending. The control process can
	// lose its stdout without cancelling an admitted worker or its command.
	output.Close()
	if _, err := input.Write(interactionFrameBytes(q)); err != nil {
		t.Fatal(err)
	}
	input.Close()
	_ = command.Wait()
	if r := waitQuickJob(t, s, q); r.State != "succeeded" {
		t.Fatal("lost ACK changed terminal", r.State)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "product")); err != nil || string(b) != "once" {
		t.Fatal("lost ACK changed execution", err)
	}
}

func openQuickTestResult(t *testing.T, q quickStart, r quickResultReceipt) quickResultPlain {
	t.Helper()
	key, _ := decode64(q.ResultKey, 32)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce, _ := decode64(r.Nonce, 12)
	sealed, _ := base64.StdEncoding.DecodeString(r.Ciphertext)
	b, err := gcm.Open(nil, nonce, sealed, q.aad())
	if err != nil {
		t.Fatal(err)
	}
	var p quickResultPlain
	if interactionJSON(b, &p) != nil || !p.valid() {
		t.Fatal("invalid encrypted terminal output")
	}
	return p
}

func TestQuickWorkerBudgetsAndStop(t *testing.T) {
	s, p := quickTestPair(t)
	exe, _ := os.Executable()
	for n, tc := range []struct {
		command, state string
		timeLimit      int
		stop           bool
	}{
		{"exec sleep 30", "timed_out", 1, false}, {"trap '' TERM; sleep 30", "stopped", 30, true}, {"kill -TERM $$", "signaled", 10, false},
		{"head -c 1200000 /dev/zero; head -c 1200000 /dev/zero >&2; printf end", "succeeded", 10, false},
	} {
		q := quickTestStart(p, n+2, tc.command)
		q.TimeoutSeconds = tc.timeLimit
		q.RequestDigest = q.digest()
		if reply, err := handoffQuickJob(t.Context(), exe, q); err != nil || reply.State != "accepted" {
			t.Fatal("handoff", err, reply.State)
		}
		if tc.stop {
			reply, err := quickStatus(t.Context(), s, q.quickResultIdentity, true)
			if err != nil || reply.State != "running" {
				t.Fatal(reply, err)
			}
		}
		r := waitQuickJob(t, s, q)
		if r.State != tc.state {
			t.Fatal("wrong terminal status", r.State, tc.state)
		}
		if tc.state == "succeeded" {
			receipt, err := readQuickResult(t.Context(), s, bytes.NewReader(interactionFrameBytes(q.quickResultIdentity)), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			p := openQuickTestResult(t, q, receipt)
			if !p.Truncated || len(p.Output) != 4096 {
				t.Fatal("unbounded or unmarked output")
			}
		}
	}
}

func TestQuickStartRejections(t *testing.T) {
	s, p := quickTestPair(t)
	exe, _ := os.Executable()
	q := quickTestStart(p, 10, "true")
	q.SubmitUntil = time.Now().Unix() - 1
	reply, err := handoffQuickJob(t.Context(), exe, q)
	if err != nil || reply.State != "not_started" || reply.Error != "expired" {
		t.Fatal(reply, err)
	}
	large := ":" + strings.Repeat(" ", quickCommandLimit-1)
	q = quickTestStart(p, 11, large)
	// Kernel argv limits depend on page/stack configuration. Compare the
	// producer against the actual account shell on this host.
	shell, err := quickAccountShell(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	probeErr := exec.Command(shell, "-c", large).Run()
	reply, err = handoffQuickJob(t.Context(), exe, q)
	switch {
	case errors.Is(probeErr, syscall.E2BIG):
		if err != nil || reply.State != "not_started" || reply.Error != "argument_too_large" {
			t.Fatal("actual system argv limit", reply, err)
		}
		if _, err := readQuickResult(t.Context(), s, bytes.NewReader(interactionFrameBytes(q.quickResultIdentity)), time.Now); err == nil {
			t.Fatal("unstarted command has an output receipt")
		}
	case probeErr == nil:
		if err != nil || reply.State != "accepted" || waitQuickJob(t, s, q).State != "succeeded" {
			t.Fatal("kernel accepted the full command", err)
		}
	default:
		t.Fatal("shell baseline", probeErr)
	}
	q.CommandB64 = base64.StdEncoding.EncodeToString([]byte{'a', 0, 'b'})
	q.RequestDigest = q.digest()
	if q.validate() == nil {
		t.Fatal("embedded NUL")
	}
	raw := interactionFrameBytes(map[string]any{"v": 1, "command_b64": "YQ=="})
	if readQuickFrame(t.Context(), bytes.NewReader(append(raw, raw...)), &map[string]any{}) == nil {
		t.Fatal("trailing frame accepted")
	}
}

func TestQuickSupportedShellSemantics(t *testing.T) {
	for _, name := range []string{"bash", "zsh", "sh", "dash"} {
		t.Run(name, func(t *testing.T) {
			shell, err := exec.LookPath(name)
			if err != nil {
				t.Skip("shell not installed")
			}
			startup := t.TempDir()
			t.Setenv("HOME", startup)
			t.Setenv("ZDOTDIR", startup)
			t.Setenv("BASH_ENV", "")
			if err := os.WriteFile(filepath.Join(startup, ".zshenv"), []byte("export QC_SHELL_FIXTURE=from-zshenv\n"), 0600); err != nil {
				t.Fatal(err)
			}
			command := "v=\"space ' quote\"\nprintf '%s\n' \"$v\"; printf '%s\n' \"$QC_SHELL_FIXTURE\"; cat <<'END'\n你好 $HOME\nEND\nexec /bin/sh -c 'exit 23'"
			raw := exec.Command(shell, "-c", command)
			raw.Args[0] = filepath.Base(shell)
			expected, rawErr := raw.CombinedOutput()
			if rawErr == nil || raw.ProcessState.ExitCode() != 23 {
				t.Fatal("raw shell baseline", rawErr)
			}
			c, err := startQuickChild(shell, command)
			if err != nil {
				t.Fatal(err)
			}
			r := quickJobRecord{Child: c.process.Pid}
			r.ChildBirth, _ = processIdentity(r.Child)
			result, err := waitQuickChild(t.Context(), Store{t.TempDir()}, r, c, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			b, _, _ := c.tail.snapshot()
			if !bytes.Equal(b, expected) || result.ExitCode == nil || *result.ExitCode != 23 {
				t.Fatal(result.State, string(b))
			}
		})
	}
}

func TestQuickExplicitBashStartup(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	startup := filepath.Join(t.TempDir(), "startup")
	if err := os.WriteFile(startup, []byte("export QC_STARTUP_COUNT=$((QC_STARTUP_COUNT + 1))\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", startup)
	t.Setenv("QC_STARTUP_COUNT", "0")
	raw, err := exec.Command(shell, "-c", "printf '%s' \"$QC_STARTUP_COUNT\"").Output()
	if err != nil || string(raw) != "1" {
		t.Fatal("raw bash startup", err)
	}
	nested := exec.Command(shell, "-c", "printf '%s' \"$QC_STARTUP_COUNT\"")
	nested.Env = append(os.Environ(), "QC_STARTUP_COUNT=1")
	got, err := nested.Output()
	if err != nil || string(got) != "2" {
		t.Fatal("nested bash startup", err)
	}
	if _, err := quickSupportedShell(shell); err == nil {
		t.Fatal("repeated explicit BASH_ENV startup accepted")
	}
}

func TestQuickForegroundEntrypoint(t *testing.T) {
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "job", "foreground", "--stdin-frame")
	cmd.Stdin = bytes.NewReader(interactionFrameBytes(map[string]any{"v": 1, "command_b64": base64.StdEncoding.EncodeToString([]byte("printf good; printf bad >&2; exit 7"))}))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatal("foreground transport", err)
	}
	decoder := json.NewDecoder(&stdout)
	var e quickForegroundEvent
	if err := decoder.Decode(&e); err != nil || e.V != 1 || e.State != "accepted" {
		t.Fatal("missing exec acknowledgement")
	}
	streams := map[string]string{}
	for {
		var event quickForegroundEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal("missing foreground terminal", err)
		}
		if event.State == "stdout" || event.State == "stderr" {
			if len(event.Data) > 16<<10 {
				t.Fatal("unbounded foreground chunk")
			}
			streams[event.State] += string(event.Data)
			continue
		}
		if event.State != "failed" || event.ExitCode == nil || *event.ExitCode != 7 {
			t.Fatal("incorrect foreground terminal status")
		}
		break
	}
	if streams["stdout"] != "good" || streams["stderr"] != "bad" {
		t.Fatal("incorrect foreground output")
	}

}

func TestQuickJobStorageRetention(t *testing.T) {
	s, p := quickTestPair(t)
	now := time.Now().Unix()
	var oldest, second quickStart
	for i := 0; i < 8; i++ {
		q := quickTestStart(p, i+1, "true")
		if i == 0 {
			oldest = q
		}
		if i == 1 {
			second = q
		}
		r := quickJobRecord{quickJobReply: quickJobReply{quickResultIdentity: q.quickResultIdentity, State: "running"}, Owner: os.Getuid(), Deadline: now + 1}
		if err := r.save(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := makeQuickJobRoom(s, p.InstallationID, now); err == nil || err.Error() != "job_limit" {
		t.Fatal("eight active jobs")
	}
	r, _ := getQuickJob(s, oldest.quickResultIdentity)
	r.State = "succeeded"
	r.OccurredAt = now - int64(quickResultLifetime/time.Second)
	r.SubmitUntil = r.OccurredAt - 1
	if err := r.save(s); err != nil {
		t.Fatal(err)
	}
	if _, err := quickStatus(t.Context(), s, oldest.quickResultIdentity, false); err == nil || err.Error() != "job_expired" {
		t.Fatal("expired status remained readable", err)
	}
	if err := makeQuickJobRoom(s, p.InstallationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := getQuickJob(s, oldest.quickResultIdentity); !os.IsNotExist(err) {
		t.Fatal("expired dedup was retained")
	}
	// Capacity pressure on output must leave independent, newer dedup entries.
	for i := 0; i < 500; i++ {
		q := quickTestStart(p, i+1, "true")
		stamp := struct {
			OccurredAt int64  `json:"occurred_at"`
			Pad        string `json:"pad"`
		}{now - 1000 + int64(i), strings.Repeat("x", 8500)}
		raw, _ := json.Marshal(stamp)
		if err := os.WriteFile(filepath.Join(s.Dir, "quick-result-"+q.JobID+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	q := quickTestStart(p, 1001, "true")
	v := loadQuickResultVector(t).Receipt
	v.quickResultIdentity = q.quickResultIdentity
	v.OccurredAt = now
	if err := saveQuickReceipt(s, v, now); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(s.Dir)
	size, count := 0, 0
	for _, f := range files {
		if quickStoreName(f.Name(), "quick-result-") {
			i, _ := f.Info()
			size += int(i.Size())
			count++
		}
	}
	if size > 4<<20 || count > 500 {
		t.Fatal("result capacity", size, count)
	}
	if _, err := getQuickJob(s, second.quickResultIdentity); err != nil {
		t.Fatal("result pressure deleted dedup")
	}
}
