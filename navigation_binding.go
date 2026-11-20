package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The originating interactive host identifies the source of a fresh navigation probe.
// The observed process and stable terminal are captured independently of reply
// activation. Persisting this record alone grants no authority to navigate.
type navigationOwner struct {
	Agent   string `json:"agent"`
	PID     int    `json:"pid"`
	Birth   string `json:"birth"`
	Target  string `json:"target"`
	Storage string `json:"storage,omitempty"`
}

func noticeNavigationOwner(a Activity, l Locator) *navigationOwner {
	if a.Reminder == "" || l.OccupantID == "" || a.ChildWait != nil || a.NativeStorage != "" && a.storageID == "" {
		return nil
	}
	switch a.Agent {
	case "claude", "opencode", "pi":
	default:
		return nil
	}
	pid := replyParent(a.Agent)
	birth, err := processIdentity(pid)
	if err != nil {
		return nil
	}
	return &navigationOwner{a.Agent, pid, birth, nativeTargetIdentity("", l), a.storageID}
}

type navigationChallenge struct {
	Nonce string `json:"nonce"`
	PID   int    `json:"pid"`
	Until int64  `json:"until"`
}

type navigationObservation struct {
	Nonce   string  `json:"nonce"`
	Agent   string  `json:"agent"`
	Logical string  `json:"logical"`
	Birth   string  `json:"birth"`
	Locator Locator `json:"locator"`
	Storage string  `json:"storage,omitempty"`
}

const navigationRequestFile = "navigation-request.json"
const navigationObservationFile = "navigation-observation.json"
const navigationConfirmedFile = "navigation-confirmed.json"

var errNavigationBinding = errors.New("navigation_binding_unconfirmed")

func navigationRead(s Store, name string, out any) error {
	raw, mode, err := secureReadWithMode(filepath.Join(s.Dir, name), 4096)
	if err != nil {
		return err
	}
	if mode.Perm()&0077 != 0 || interactionJSON(raw, out) != nil {
		return errNavigationBinding
	}
	return nil
}

func navigationForeground(ctx context.Context, pid int) bool {
	// Read only numeric process groups. A still-live, backgrounded TUI cannot
	// attest the session currently displayed in a reused shell.
	out, err := commandOutput(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "pgid=", "-o", "tpgid=")
	if err != nil {
		return false
	}
	f := strings.Fields(string(out))
	// Group membership alone could describe a sibling in a pipeline or a
	// non-job-control shell. Only the foreground job leader can attest.
	return pid > 1 && len(f) == 2 && f[0] == strconv.Itoa(pid) && f[0] == f[1]
}

func navigationTarget(ctx context.Context, s Store, pid int) (Locator, error) {
	id, err := s.existingInstallation()
	if err != nil {
		return Locator{}, err
	}
	l, err := captureTarget(ctx, id)
	if err != nil {
		return Locator{}, err
	}
	var panePID int
	switch l.Backend {
	case "tmux":
		var again Locator
		again, panePID, err = captureTmuxProof(ctx, id, l.Namespace, l.PaneID)
		if err != nil || again != l {
			return Locator{}, errNavigationBinding
		}
	case "herdr":
		socket, e := herdrSocket(l.Namespace)
		if e != nil {
			return Locator{}, e
		}
		panePID, err = herdrShellPID(ctx, socket, l.PaneID)
		if err != nil {
			return Locator{}, err
		}
		again, e := queryHerdrTarget(ctx, id, l.Namespace, l.PaneID, l.TerminalID, l.ServerIdentity)
		if e != nil || again != l {
			return Locator{}, errNavigationBinding
		}
	default:
		return Locator{}, errNavigationBinding
	}
	hostSID, e := unix.Getsid(pid)
	paneSID, err := unix.Getsid(panePID)
	if e != nil || err != nil || hostSID != paneSID || !codexIsAncestor(panePID) || !navigationForeground(ctx, pid) {
		return Locator{}, errNavigationBinding
	}
	return l, nil
}

// This local adapter operation reads the current session twice, on opposite
// sides of the terminal sampling. Both calls must come directly from the
// recorded TUI, even when a classic notification was launched below a shell.
func observeNavigation(ctx context.Context, s Store, op string, in io.Reader) (any, error) {
	var f struct {
		V         int    `json:"v"`
		Agent     string `json:"agent"`
		Session   string `json:"session_id"`
		Storage   string `json:"storage,omitempty"`
		ServerPID int    `json:"server_pid,omitempty"`
		Nonce     string `json:"nonce"`
	}
	if err := readStrictFrame(ctx, in, &f, 8192); err != nil {
		return nil, err
	}
	if f.V != 1 || !opaqueID.MatchString(f.Nonce) || f.Session == "" || len(f.Session) > 256 || (f.Agent != "claude" && f.Agent != "pi" && f.Agent != "opencode") {
		return nil, errNavigationBinding
	}
	pid := os.Getppid()
	var ch navigationChallenge
	if navigationRead(s, navigationRequestFile, &ch) != nil || ch.Nonce != f.Nonce || ch.PID != pid || ch.Until <= time.Now().Unix() {
		return nil, errNavigationBinding
	}
	logical, err := logicalSession(f.Agent, f.Session)
	if err != nil {
		return nil, err
	}
	birth, err := processIdentity(pid)
	if err != nil {
		return nil, err
	}
	storage := nativeStorageKey(f.Agent, f.Storage)
	logical = sessionWithStorage(logical, storage)
	if storage != "" && f.Agent == "opencode" && !localOpenCodeDatabase(ctx, pid, f.ServerPID, f.Storage) {
		return nil, errNavigationBinding
	}
	var target Locator
	if op == "navigation-observe" {
		target, err = navigationTarget(ctx, s, pid)
		if err != nil {
			return nil, err
		}
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var current navigationChallenge
	if navigationRead(s, navigationRequestFile, &current) != nil || current != ch || current.Until <= time.Now().Unix() || pid != os.Getppid() {
		return nil, errNavigationBinding
	}
	if op == "navigation-observe" {
		err = s.writeJSON(navigationObservationFile, navigationObservation{ch.Nonce, f.Agent, logical, birth, target, storage})
	} else {
		var observation navigationObservation
		if navigationRead(s, navigationObservationFile, &observation) != nil || observation.Nonce != ch.Nonce || observation.Agent != f.Agent || observation.Birth != birth || observation.Logical != logical || observation.Storage != storage || !navigationForeground(ctx, pid) {
			return nil, errNavigationBinding
		}
		err = s.writeJSON(navigationConfirmedFile, ch.Nonce)
	}
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func navigationCandidate(s Store, p Pair, d delivery) (navigationOwner, error) {
	if d.Navigation == nil {
		return navigationOwner{}, errNavigationBinding
	}
	owner := *d.Navigation
	if owner.Storage == "" {
		return owner, nil
	}
	host, err := currentNativeHost(s, owner.Storage, func(h nativeHost) bool {
		return h.Agent == owner.Agent && audienceSession(p, h.Logical) == d.VisibleSession
	})
	if err != nil {
		return navigationOwner{}, err
	}
	owner.PID, owner.Birth = host.PID, host.Birth
	return owner, nil
}

func confirmNavigation(ctx context.Context, s Store, p Pair, d delivery, until int64) (Locator, error) {
	owner, err := navigationCandidate(s, p, d)
	if err != nil {
		return Locator{}, err
	}
	if birth, err := processIdentity(owner.PID); err != nil || birth != owner.Birth || !navigationForeground(ctx, owner.PID) {
		return Locator{}, errNavigationBinding
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := checkReplyWakeStore(s); err != nil {
		return Locator{}, err
	}
	// A single bounded slot also limits concurrent combinations of Pair/host.
	unlock, err := lockFileWait(ctx, filepath.Join(s.Dir, ".navigation-lock"), true)
	if err != nil {
		return Locator{}, err
	}
	defer unlock()
	ch := navigationChallenge{randomID(), owner.PID, min(until, time.Now().Unix()+5)}
	releaseStore, err := s.lock(ctx)
	if err != nil {
		return Locator{}, err
	}
	err = s.writeJSON(navigationRequestFile, ch)
	releaseStore()
	if err != nil {
		return Locator{}, err
	}
	defer func() {
		for _, name := range []string{navigationRequestFile, navigationObservationFile, navigationConfirmedFile} {
			_ = os.Remove(filepath.Join(s.Dir, name))
		}
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Locator{}, errNavigationBinding
		case <-ticker.C:
			var nonce string
			if navigationRead(s, navigationConfirmedFile, &nonce) != nil || nonce != ch.Nonce {
				continue
			}
			var observation navigationObservation
			err = navigationRead(s, navigationObservationFile, &observation)
			birth, e := processIdentity(owner.PID)
			if err != nil || e != nil || birth != owner.Birth || ch.Until <= time.Now().Unix() || observation.Nonce != nonce || observation.Agent != owner.Agent || observation.Birth != birth ||
				audienceSession(p, observation.Logical) != d.VisibleSession || observation.Locator.validate() != nil || observation.Locator.InstallationID != p.InstallationID ||
				(owner.Storage == "" && nativeTargetIdentity("", observation.Locator) != owner.Target || owner.Storage != "" && observation.Storage != owner.Storage) || !navigationForeground(ctx, owner.PID) {
				return Locator{}, errNavigationBinding
			}
			if current, e := navigationCandidate(s, p, d); e != nil || current != owner {
				return Locator{}, errNavigationBinding
			}

			return observation.Locator, nil
		}
	}
}
