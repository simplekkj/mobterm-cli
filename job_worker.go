package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Retain only the final combined tail. Each stream is drained independently
// and carries one bit of provenance per byte in the final serialisation.
type quickOutputTail struct {
	mu           sync.Mutex
	data         [quickResultTailLimit]byte
	stream       [quickResultTailLimit]bool
	offset, used int
	truncated    bool
}
type quickStreamWriter struct {
	tail   *quickOutputTail
	stderr bool
}

func (w quickStreamWriter) Write(p []byte) (int, error) {
	t := w.tail
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if len(p) > len(t.data) {
		p = p[len(p)-len(t.data):]
		t.truncated = true
	}
	for _, b := range p {
		t.data[t.offset] = b
		t.stream[t.offset] = w.stderr
		t.offset = (t.offset + 1) % len(t.data)
		if t.used < len(t.data) {
			t.used++
		} else {
			t.truncated = true
		}
	}
	return n, nil
}
func (t *quickOutputTail) snapshot() ([]byte, []byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, mask := make([]byte, t.used), make([]byte, (t.used+7)/8)
	for i := range b {
		idx := (t.offset + len(t.data) - t.used + i) % len(t.data)
		b[i] = t.data[idx]
		if t.stream[idx] {
			mask[i/8] |= 1 << uint(i%8)
		}
	}
	return b, mask, t.truncated
}

type quickChild struct {
	process     *os.Process
	out, stderr *os.File
	drained     chan struct{}
	tail        quickOutputTail
}

func startQuickChild(shell, command string) (*quickChild, error) {
	c, err := launchQuickChild(shell, command)
	if err == nil {
		c.capture(quickStreamWriter{&c.tail, false}, quickStreamWriter{&c.tail, true})
	}
	return c, err
}

func launchQuickChild(shell, command string) (*quickChild, error) {
	rd, out, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer out.Close()
	re, stderr, err := os.Pipe()
	if err != nil {
		rd.Close()
		return nil, err
	}
	defer stderr.Close()
	input, err := os.Open(os.DevNull)
	if err != nil {
		rd.Close()
		re.Close()
		return nil, err
	}
	defer input.Close()
	p, err := os.StartProcess(shell, []string{filepath.Base(shell), "-c", command}, &os.ProcAttr{Env: os.Environ(), Files: []*os.File{input, out, stderr}, Sys: &syscall.SysProcAttr{Setpgid: true}})
	if err != nil {
		rd.Close()
		re.Close()
		return nil, quickSpawnError(err)
	}
	return &quickChild{process: p, out: rd, stderr: re, drained: make(chan struct{})}, nil
}

func (c *quickChild) capture(stdout, stderr io.Writer) {
	var wg sync.WaitGroup
	for i, f := range []*os.File{c.out, c.stderr} {
		output := []io.Writer{stdout, stderr}[i]
		wg.Go(func() { _, _ = io.Copy(output, f) })
	}
	go func() { wg.Wait(); close(c.drained) }()
}

func (c *quickChild) drain() {
	select {
	case <-c.drained:
	case <-time.After(250 * time.Millisecond):
	}
	c.out.Close()
	c.stderr.Close()
	<-c.drained
}

func quickWorker(s Store, api API) error {
	request, ready := os.NewFile(3, "job-packet"), os.NewFile(4, "job-ready")
	defer request.Close()
	defer ready.Close()
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	for _, f := range []*os.File{request, ready} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return errors.New("invalid_worker_pipe")
		}
	}
	_ = request.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, err := io.ReadAll(io.LimitReader(request, quickJobFrameLimit+1))
	request.Close()
	var q quickStart
	if err != nil || len(raw) > quickJobFrameLimit || interactionJSON(raw, &q) != nil || q.validate() != nil {
		return errors.New("invalid_request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	r, child, state, code := admitQuickJob(ctx, s, q)
	cancel()
	_ = json.NewEncoder(ready).Encode(quickJobReply{quickResultIdentity: q.quickResultIdentity, State: state, Error: code})
	ready.Close()
	if child == nil {
		return nil
	}
	key, _ := decode64(q.ResultKey, 32)
	// Only bounded metadata and the in-memory result key survive acceptance.
	// Do not retain the original command or wire packet during execution.
	raw = nil
	q.CommandB64 = ""
	q.ResultKey = ""
	result, err := waitQuickChild(context.Background(), s, r, child, time.Duration(q.TimeoutSeconds)*time.Second)
	if err != nil {
		return err
	}
	result.Output, result.StderrMask, result.Truncated = child.tail.snapshot()
	receipt, err := sealQuickResult(key, q.quickResultIdentity, result)
	clear(key)
	if err != nil {
		return err
	}
	c := QuickCommandCompletion{1, r.InstallationID, r.JobID, r.CommandID, r.CommandName, result.State, result.ExitCode, result.Signal, result.OccurredAt}
	finish, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	unlock, err := s.lock(finish)
	if err != nil {
		return err
	}
	current, err := getQuickJob(s, q.quickResultIdentity)
	if err == nil && current.WorkerBirth == r.WorkerBirth && current.Worker == os.Getpid() {
		err = saveQuickReceipt(s, receipt, time.Now().Unix())
		if err == nil {
			current.State = result.State
			current.ExitCode = result.ExitCode
			current.Signal = result.Signal
			current.OccurredAt = result.OccurredAt
			current.Child = 0
			current.ChildBirth = ""
			current.Worker = 0
			current.WorkerBirth = ""
			current.Stop = false
			current.Delivery = "pending"
			err = current.save(s)
		}
	} else {
		err = errors.New("identity_mismatch")
	}
	unlock()
	if err != nil {
		return err
	}
	notify, stop := context.WithTimeout(context.Background(), networkCommandTimeout)
	defer stop()
	stage := deliverQuickJobCompletion(notify, s, api, r.PairID, r.EventID, c)
	update, end := context.WithTimeout(context.Background(), 3*time.Second)
	defer end()
	unlock, err = s.lock(update)
	if err != nil {
		return err
	}
	defer unlock()
	current, err = getQuickJob(s, q.quickResultIdentity)
	if err != nil || current.Delivery != "pending" {
		return errors.New("identity_mismatch")
	}
	current.Delivery = safeCode(stage)
	return current.save(s)
}

// Reserve the identity durably before starting the shell. The global lock
// covers just the transaction and exec acknowledgement, never the execution.
func admitQuickJob(ctx context.Context, s Store, q quickStart) (quickJobRecord, *quickChild, string, string) {
	shell, shellErr := quickAccountShell(ctx)
	birth, birthErr := processIdentity(os.Getpid())
	command, _ := quickScript(q.CommandB64)
	unlock, err := s.lock(ctx)
	if err != nil {
		return quickJobRecord{}, nil, "error", "job_store_failed"
	}
	defer unlock()
	r, err := getQuickJob(s, q.quickResultIdentity)
	if err == nil {
		if r.KeyDigest != digest(q.ResultKey) {
			return r, nil, "error", "identity_mismatch"
		}
		if r.State == "not_started" {
			return r, nil, r.State, r.Error
		}
		if r.State == "running" && sameQuickProcess(r.Worker, r.WorkerBirth) ||
			r.OccurredAt > 0 && validQuickCommandTermination(r.State, r.ExitCode, r.Signal) {
			return r, nil, "accepted", ""
		}
		return r, nil, "error", "handoff_unknown"
	}
	if !errors.Is(err, os.ErrNotExist) {
		return r, nil, "error", "identity_mismatch"
	}
	if shellErr != nil {
		return r, nil, "not_started", "unsupported_shell"
	}
	if birthErr != nil {
		return r, nil, "not_started", "worker_unavailable"
	}
	now := time.Now().Unix()
	if now >= q.SubmitUntil || q.SubmitUntil > now+120 {
		return r, nil, "not_started", "expired"
	}
	pairs, err := s.pairs()
	p, ok := pairs[q.PairID]
	if err != nil || !ok || p.Status != "active" || p.InstallationID != q.InstallationID || p.PairID != q.PairID {
		return r, nil, "not_started", "notification_unavailable"
	}
	if err := makeQuickJobRoom(s, q.InstallationID, now); err != nil {
		return r, nil, "not_started", safeCode(err.Error())
	}
	r = quickJobRecord{quickJobReply: quickJobReply{quickResultIdentity: q.quickResultIdentity, State: "accepted", StartedAt: now}, Owner: os.Getuid(),
		PairID: q.PairID, EventID: q.EventID, CommandName: q.CommandName,
		KeyDigest: digest(q.ResultKey), Worker: os.Getpid(), WorkerBirth: birth, SubmitUntil: q.SubmitUntil, Deadline: now + int64(q.TimeoutSeconds)}
	if err := r.save(s); err != nil {
		return r, nil, "error", "job_store_failed"
	}
	child, err := startQuickChild(shell, command)
	if err != nil {
		r.State = "not_started"
		r.StartedAt = 0
		r.Error = safeCode(err.Error())
		r.OccurredAt = time.Now().Unix()
		r.Worker = 0
		r.WorkerBirth = ""
		if err := r.save(s); err != nil {
			return r, nil, "error", "job_store_failed"
		}
		return r, nil, r.State, r.Error
	}
	r.Child = child.process.Pid
	r.ChildBirth, err = processIdentity(r.Child)
	r.State = "running"
	if saveErr := r.save(s); err != nil || saveErr != nil {
		return r, child, "error", "handoff_unknown"
	}
	return r, child, "accepted", ""
}

// The worker is the sole waiter. During TERM grace it deliberately leaves even
// a zombie child unreaped so its PID/process group cannot be recycled before
// the final group KILL. Remote cancellation only records intent for this worker.
func waitQuickChild(ctx context.Context, s Store, r quickJobRecord, c *quickChild, budget time.Duration) (quickResultPlain, error) {
	defer c.drain()
	defer c.process.Release()
	exitNotice := make(chan os.Signal, 1)
	signal.Notify(exitNotice, syscall.SIGCHLD)
	defer signal.Stop(exitNotice)
	limit := time.NewTimer(budget)
	defer limit.Stop()
	checkStop := time.NewTicker(time.Second)
	defer checkStop.Stop()
	var status syscall.WaitStatus
	reason := ""
	for {
		pid, err := syscall.Wait4(r.Child, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return quickResultPlain{}, err
		}
		if pid == r.Child {
			break
		}
		if reason != "" {
			group, err := syscall.Getpgid(r.Child)
			if err != nil || group != r.Child || !sameQuickProcess(r.Child, r.ChildBirth) {
				// Exit can race the timeout. A waitable direct child belongs to
				// this worker even on systems that hide zombie birth records.
				if pid, waitErr := syscall.Wait4(r.Child, &status, syscall.WNOHANG, nil); waitErr == nil && pid == r.Child {
					reason = ""
					break
				}
				return quickResultPlain{}, errors.New("child_identity_changed")
			}
			if err := syscall.Kill(-group, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				return quickResultPlain{}, err
			}
			// The verified group ID remains reserved by our unreaped child
			// throughout this grace period, including when TERM makes it a zombie.
			time.Sleep(500 * time.Millisecond)
			if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				// Darwin can return EPERM for a group whose last member is
				// already a zombie. Accept only the actual collected exit.
				if pid, waitErr := syscall.Wait4(r.Child, &status, syscall.WNOHANG, nil); waitErr == nil && pid == r.Child {
					break
				}
				return quickResultPlain{}, err
			}
			// A task stuck in a kernel wait may survive an issued KILL for
			// an unbounded time. Leave its obligation unknown in that case.
			reapLimit := time.Now().Add(2 * time.Second)
			for {
				pid, waitErr := syscall.Wait4(r.Child, &status, syscall.WNOHANG, nil)
				if waitErr == nil && pid == r.Child {
					break
				}
				if waitErr != nil && !errors.Is(waitErr, syscall.EINTR) {
					return quickResultPlain{}, waitErr
				}
				if time.Now().After(reapLimit) {
					return quickResultPlain{}, errors.New("termination_unconfirmed")
				}
				time.Sleep(20 * time.Millisecond)
			}
			break
		}
		select {
		case <-exitNotice:
		case <-ctx.Done():
			reason = "stopped"
		case <-limit.C:
			reason = "timed_out"
		case <-checkStop.C:
			if r.JobID == "" {
				continue
			}
			latest, err := getQuickJob(s, r.quickResultIdentity)
			if err == nil && latest.Stop && latest.Owner == r.Owner && latest.Worker == os.Getpid() &&
				latest.WorkerBirth == r.WorkerBirth && latest.Child == r.Child && latest.ChildBirth == r.ChildBirth {
				reason = "stopped"
			}
		}
	}
	result := quickResultPlain{StartedAt: r.StartedAt, OccurredAt: max(time.Now().Unix(), r.StartedAt), State: reason}
	if status.Exited() {
		code := status.ExitStatus()
		result.ExitCode = &code
		if reason == "" {
			if code == 0 {
				result.State = "succeeded"
			} else {
				result.State = "failed"
			}
		}
	} else if status.Signaled() {
		sig := int(status.Signal())
		result.Signal = &sig
		if reason == "" {
			result.State = "signaled"
		}
	} else {
		return quickResultPlain{}, errors.New("unknown_exit")
	}
	return result, nil
}
