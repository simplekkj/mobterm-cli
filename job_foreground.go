package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type quickForegroundEvent struct {
	V        int    `json:"v"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	Data     []byte `json:"data,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Signal   *int   `json:"signal,omitempty"`
}

type foregroundOutput struct {
	mu      sync.Mutex
	encoder *json.Encoder
	cancel  context.CancelFunc
	err     error
}

func (o *foregroundOutput) write(event quickForegroundEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err == nil {
		event.V = 1
		o.err = o.encoder.Encode(event)
		if o.err != nil {
			o.cancel()
		}
	}
	return o.err
}

type foregroundStream struct {
	output *foregroundOutput
	name   string
}

func (w foregroundStream) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), 16<<10)
		if err := w.output.write(quickForegroundEvent{State: w.name, Data: p[:n]}); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// The SSH command itself stays in the foreground. The first event separates
// a successful native exec from a pre-exec error. User bytes cannot be
// confused with metadata because they travel in separate base64 data frames.
func foregroundQuickJob(base context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := signal.NotifyContext(base, syscall.SIGHUP, syscall.SIGPIPE)
	defer cancel()
	output := &foregroundOutput{encoder: json.NewEncoder(out), cancel: cancel}
	input, done := context.WithTimeout(ctx, 20*time.Second)
	var q struct {
		V          int    `json:"v"`
		CommandB64 string `json:"command_b64"`
	}
	err := readQuickFrame(input, in, &q)
	done()
	var command, shell string
	if err == nil && q.V != 1 {
		err = errors.New("invalid_request")
	}
	if err == nil {
		command, err = quickScript(q.CommandB64)
	}
	if err == nil {
		shell, err = quickAccountShell(ctx)
	}
	var child *quickChild
	if err == nil {
		child, err = launchQuickChild(shell, command)
	}
	if err != nil {
		return output.write(quickForegroundEvent{State: "not_started", Error: safeCode(err.Error())})
	}
	r := quickJobRecord{quickJobReply: quickJobReply{StartedAt: time.Now().Unix()}, Child: child.process.Pid}
	r.ChildBirth, _ = processIdentity(r.Child)
	_ = output.write(quickForegroundEvent{State: "accepted"})
	child.capture(foregroundStream{output, "stdout"}, foregroundStream{output, "stderr"})
	result, err := waitQuickChild(ctx, Store{}, r, child, 7*24*time.Hour)
	if err != nil {
		return errors.New("foreground_exit_unknown")
	}
	state := "succeeded"
	if result.Signal != nil {
		state = "signaled"
	} else if result.ExitCode != nil && *result.ExitCode != 0 {
		state = "failed"
	}
	return output.write(quickForegroundEvent{State: state, ExitCode: result.ExitCode, Signal: result.Signal})
}
