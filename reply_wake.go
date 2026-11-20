package main

import (
	"context"
	"crypto/hmac"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Both the requester and the owning adapter address the same private Store.
func checkReplyWakeStore(s Store) error {
	info, err := os.Lstat(s.Dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || int(stat.Uid) != os.Geteuid() {
		return errors.New("unsafe_reply_store")
	}
	return nil
}

// Only this opaque filename is observed by the original host. Its content is
// a short expiry. Grants, decision text and the owner token stay off the marker.
func wakeFile(id string) string { return "reply-wake-" + id + ".json" }

func wakeReply(ctx context.Context, s Store, r replyRecord, until int64) (replyRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return r, err
	}
	if err = checkReplyWakeStore(s); err != nil {
		unlock()
		return r, err
	}
	current, err := readReply(s, r.SourceID)
	if err == nil {
		switch {
		case current.Frame != nil:
			err = errors.New("reply_decision_conflict")
		case !current.Renewable || !current.alive(false) && current.NativeTurn == "":
			err = errors.New("native_session_unavailable")
		case replyCursorReason(s, current) != "":
			err = errors.New("source_changed")
		case current.State == "pending" && current.Expires > time.Now().Unix() && current.alive(true):
			unlock()
			return current, nil
		default:
			expires := min(time.Now().Unix()+5, until)
			if expires <= time.Now().Unix() {
				err = errors.New("reply_policy_changed")
			} else {
				err = s.writeJSON(wakeFile(r.SourceID), expires)
			}
		}
	}
	unlock()
	if err != nil {
		return r, err
	}
	tick := time.NewTicker(60 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return r, errors.New("native_session_unavailable")
		case <-tick.C:
			current, err = readReply(s, r.SourceID)
			if err != nil {
				return r, err
			}
			if current.State == "pending" && current.Expires > time.Now().Unix() && current.alive(true) {
				return current, nil
			}
			if current.Frame != nil {
				return r, errors.New("reply_decision_conflict")
			}
		}
	}
}

func reopenReply(ctx context.Context, s Store, api API, in io.Reader) (any, error) {
	var f struct {
		V      int    `json:"v"`
		Source string `json:"source_id"`
		Owner  string `json:"owner_token"`
	}
	if err := readInteractionFrame(ctx, in, &f); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	r, err := readReply(s, f.Source)
	if err == nil {
		err = renewReplyLocked(s, &r, f.V, f.Owner, time.Now())
	}
	unlock()
	if err != nil {
		return nil, err
	}
	return waitForReplyDecision(ctx, s, api, r)
}

func renewReplyLocked(s Store, r *replyRecord, version int, owner string, now time.Time) error {
	if version != 1 || !r.Renewable || r.Frame != nil || r.ParentPID != replyParent(r.Agent) || !r.alive(false) ||
		!hmac.Equal([]byte(r.OwnerToken), []byte(owner)) || replyCursorReason(s, *r) != "" {
		return errors.New("reply_owner_mismatch")
	}
	return claimReplyWakeLocked(s, r, now)
}

func claimReplyWakeLocked(s Store, r *replyRecord, now time.Time) error {
	if r.State != "expired" && (r.State != "pending" || r.Expires > now.Unix() && r.alive(true)) {
		return errors.New("reply_claim_conflict")
	}
	if err := checkReplyWakeStore(s); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, wakeFile(r.SourceID))
	raw, mode, err := secureReadWithMode(path, 32)
	var expires int64
	if err != nil || mode.Perm()&0077 != 0 || interactionJSON(raw, &expires) != nil || expires <= now.Unix() || expires > now.Unix()+5 {
		return errors.New("reply_wake_unavailable")
	}
	if err = replyCapacityLocked(s, nil); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	r.PID = os.Getpid()
	if r.Birth, err = processIdentity(r.PID); err != nil {
		return err
	}
	r.State = "pending"
	r.Expires = now.Unix() + 55
	return saveReply(s, *r)
}
