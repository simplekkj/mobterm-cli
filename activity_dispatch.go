package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const activityDispatchBudget = 12 * time.Second

// receiveActivity acknowledges only durable local reception. In particular a
// full store must not turn a tool Hook into a synchronous network flush.
func receiveActivity(ctx context.Context, s Store, l Locator, a Activity) error {
	_, _, _, err := applyActivity(ctx, s, l, a)
	launchErr := startActivityFlush(s)
	if err != nil {
		return err
	}
	return launchErr
}

// The inherited pending lock admits at most one child waiting to send. Once
// that child owns the active lock, it releases pending BEFORE reading work.
// Thus a producer either joins that child's upcoming read or starts its one
// successor. There is no check-empty/exit lost wakeup and no child spawns a child.
// Kernel locks disappear on crash; the next Hook or explicit flush can recover.
func startActivityFlush(s Store) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return launchActivityFlush(s, executable)
}

func launchActivityFlush(s Store, executable string) error {
	if err := ensureDir(s.Dir, true); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".activity-pending-lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil
		}
		return err
	}
	cmd := exec.Command(executable, "activity", "dispatch")
	cmd.Env = append(os.Environ(), "MOBTERM_CONFIG_DIR="+s.Dir)
	cmd.ExtraFiles = []*os.File{f}
	// No inherited Hook protocol streams, terminal, or stdin. The child remains
	// short lived even after its launching Hook process has exited.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func dispatchActivity(ctx context.Context, s Store, api API, pending *os.File) error {
	if pending == nil {
		return errors.New("activity_dispatch_lock_missing")
	}
	defer pending.Close()
	fi, err := pending.Stat()
	pathInfo, pathErr := os.Lstat(filepath.Join(s.Dir, ".activity-pending-lock"))
	if err != nil || pathErr != nil || !fi.Mode().IsRegular() || !os.SameFile(fi, pathInfo) {
		return errors.New("activity_dispatch_lock_invalid")
	}
	unlock, err := lockFile(ctx, filepath.Join(s.Dir, ".activity-active-lock"))
	if err != nil {
		return err
	}
	defer unlock()
	// Coalesce a burst while retaining the pending slot. This also bounds
	// successful short-network launches instead of spawning for every tool.
	select {
	case <-time.After(250 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	if err = pending.Close(); err != nil {
		return err
	}
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return drainActivity(flushCtx, s, api)
}

func drainActivity(ctx context.Context, s Store, api API) error {
	// A pass preserves the existing per-pair scheduler and outbox retry rules.
	// Continue only while durable intents change; throttled/offline work remains
	// for a later Hook or explicit outbox flush, without a background retry loop.
	for pass := 0; pass < 8; pass++ {
		before, err := activityIntentFingerprint(ctx, s)
		if err != nil {
			return err
		}
		if err = flushRunSnapshotsWithBudget(ctx, s, api, 5*time.Second); err != nil {
			return err
		}
		after, err := activityIntentFingerprint(ctx, s)
		if err != nil || before == after {
			return err
		}
	}
	return nil
}

func activityIntentFingerprint(ctx context.Context, s Store) (string, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(db.Intents)
	return digest(string(raw)), err
}
