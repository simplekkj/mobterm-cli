package main

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Each adapter publishes its current native session from its own host. Entries
// select a challenge recipient. They never authorize a navigation or a write.
type nativeHost struct {
	ServerPID     int    `json:"server_pid,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	ServerBirth   string `json:"server_birth,omitempty"`
	Epoch         uint64 `json:"epoch,omitempty,string"`
	Agent         string `json:"agent"`
	Logical       string `json:"logical"`
	Storage       string `json:"storage"`
	PID           int    `json:"pid"`
	Birth         string `json:"birth"`
	Credential    string `json:"credential"`
}

type nativeHostFrame struct {
	ServerVersion string `json:"server_version,omitempty"`
	Epoch         uint64 `json:"epoch,omitempty,string"`
	ServerPID     int    `json:"server_pid,omitempty"`
	Token         string `json:"host_token"`
	V             int    `json:"v"`
	Agent         string `json:"agent"`
	Session       string `json:"session_id"`
	Storage       string `json:"storage"`
	Turn          string `json:"turn,omitempty"`
	Source        string `json:"source_id,omitempty"`
}

const nativeHostsFile = "native-hosts.json"

func validHostTurn(v string) bool {
	return v != "" && len(v) <= 256 && strings.IndexFunc(v, unicode.IsControl) < 0
}

func nativeStorageKey(agent, value string) string {
	if (agent != "pi" && agent != "claude" && agent != "opencode") || !filepath.IsAbs(value) || filepath.Clean(value) != value || len(value) > 4096 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	info, err := os.Stat(value)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return ""
	}
	return digest(fmt.Sprintf("%s\x00%d:%d", agent, stat.Dev, stat.Ino))
}

func loadNativeHosts(s Store) ([]nativeHost, error) {
	var hosts []nativeHost
	err := s.readJSON(nativeHostsFile, &hosts)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return hosts, err
}

func currentNativeHost(s Store, storage string, match func(nativeHost) bool) (nativeHost, error) {
	hosts, err := loadNativeHosts(s)
	if err != nil {
		return nativeHost{}, err
	}
	if len(hosts) > 256 {
		return nativeHost{}, errors.New("native_host_capacity")
	}
	var found nativeHost
	for _, h := range hosts {
		if h.Storage != storage || !match(h) {
			continue
		}
		if birth, err := processIdentity(h.PID); err != nil || birth != h.Birth {
			continue
		}
		if found.PID != 0 {
			return nativeHost{}, errNavigationBinding
		}
		found = h
	}
	var installation nativeHookRecord
	if found.PID == 0 || s.readJSON(nativeRecordName(found.Agent), &installation) != nil || installation.Pending != "" ||
		installation.Revision != nativeAdapterRevision || !opaqueID.MatchString(installation.HostToken) || digest(installation.HostToken) != found.Credential {
		return found, errNavigationBinding
	}
	return found, nil
}

func nativeReplyOwnerCurrent(s Store, r replyRecord) bool {
	h, err := currentNativeHost(s, r.NativeStorage, func(h nativeHost) bool { return h.Agent == r.Agent && h.Logical == r.LogicalSession })
	return err == nil && h.PID == r.ParentPID && h.Birth == r.ParentBirth
}

func readHostFrame(ctx context.Context, s Store, in io.Reader) (nativeHostFrame, nativeHost, error) {
	var f nativeHostFrame
	if err := readStrictFrame(ctx, in, &f, 8192); err != nil {
		return f, nativeHost{}, err
	}
	h := nativeHost{Agent: f.Agent, PID: os.Getppid(), Epoch: f.Epoch}
	if f.V != 1 || (h.Agent != "pi" && h.Agent != "claude" && h.Agent != "opencode") || (f.Session != "" && !validHostTurn(f.Session)) {
		return f, h, errNavigationBinding
	}
	var record nativeHookRecord
	if s.readJSON(nativeRecordName(f.Agent), &record) != nil || record.Pending != "" || record.Revision != nativeAdapterRevision || !opaqueID.MatchString(f.Token) || !hmac.Equal([]byte(f.Token), []byte(record.HostToken)) || !nativeHooksInstalled(s, f.Agent) {
		return f, h, errNavigationBinding
	}
	h.Credential = digest(f.Token)
	var err error
	if h.Birth, err = processIdentity(h.PID); err != nil {
		return f, h, err
	}
	if f.Session != "" {
		if f.Agent == "opencode" {
			if !localOpenCodeDatabase(ctx, h.PID, f.ServerPID, f.Storage) {
				return f, h, errNavigationBinding
			}
			if openCodeScopeVersion(f.ServerVersion) != "" {
				h.ServerPID, h.ServerVersion = f.ServerPID, f.ServerVersion
				h.ServerBirth, _ = processIdentity(f.ServerPID)
			}
		}
		h.Storage = nativeStorageKey(f.Agent, f.Storage)
		if h.Storage == "" {
			return f, h, errNavigationBinding
		}
		var base string
		base, err = logicalSession(f.Agent, f.Session)
		h.Logical = sessionWithStorage(base, h.Storage)
	}
	return f, h, err
}

func registerNativeHost(ctx context.Context, s Store, in io.Reader) (any, error) {
	f, h, err := readHostFrame(ctx, s, in)
	if err != nil {
		return nil, err
	}
	if err = checkReplyWakeStore(s); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	hosts, err := loadNativeHosts(s)
	if err != nil {
		return nil, err
	}
	live := hosts[:0]
	for _, old := range hosts {
		if old.PID == h.PID {
			// A delayed registration cannot restore the session the user just left.
			if old.Birth == h.Birth && old.Epoch != 0 && (old.Epoch > h.Epoch || old.Epoch == h.Epoch && h != old) {
				return nil, errors.New("native_activation_changed")
			}
			continue
		}
		if birth, e := processIdentity(old.PID); e == nil && birth == old.Birth {
			live = append(live, old)
		}
	}
	// Record every live registrant, even when navigation/reply queries have
	// reached their bounded scan limit. Omitting a host could hide ambiguity.
	if h.Logical != "" || h.Epoch != 0 {
		live = append(live, h)
	}
	if err = s.writeJSON(nativeHostsFile, live); err != nil {
		return nil, err
	}
	result := map[string]string{}
	if h.Logical == "" || !validHostTurn(f.Turn) {
		return result, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "reply-source-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "reply-source-"), ".json")
		r, e := readReply(s, id)
		if e != nil {
			return nil, e
		}
		if r.Frame != nil || !r.Renewable || r.NativeStorage != h.Storage || r.LogicalSession != h.Logical || r.NativeTurn != f.Turn || replyCursorReason(s, r) != "" {
			continue
		}
		if result["source_id"] != "" {
			return nil, errors.New("ambiguous_reply_source")
		}
		result["source_id"] = id
	}
	return result, nil
}

func rebindReply(ctx context.Context, s Store, api API, in io.Reader) (any, error) {
	f, h, err := readHostFrame(ctx, s, in)
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	r, err := readReply(s, f.Source)
	if err == nil {
		err = rebindReplyLocked(s, &r, h, f.Turn, time.Now())
	}
	unlock()
	if err != nil {
		return nil, err
	}
	return waitForReplyDecision(ctx, s, api, r)
}

func rebindReplyLocked(s Store, r *replyRecord, h nativeHost, turn string, now time.Time) error {
	if !validHostTurn(turn) || r.Frame != nil || !r.Renewable || r.NativeTurn != turn || r.Agent != h.Agent || r.NativeStorage == "" || r.NativeStorage != h.Storage || r.LogicalSession != h.Logical || replyCursorReason(s, *r) != "" {
		return errors.New("reply_owner_mismatch")
	}
	if r.State != "expired" && (r.State != "pending" || r.Expires > now.Unix() && r.alive(true)) {
		return errors.New("reply_claim_conflict")
	}
	r.ParentPID, r.ParentBirth = h.PID, h.Birth
	if !nativeReplyOwnerCurrent(s, *r) {
		return errors.New("reply_owner_mismatch")
	}
	r.PID, r.Birth = 0, ""
	r.OwnerToken = randomID()
	// The source, original terminal, audience tokens and immutable cursor stay
	// tied to the original notification. Only the unclaimed local waiter moves.
	return claimReplyWakeLocked(s, r, now)
}

// Resolve both ends of a currently established loopback connection. A PID
// returned by a remote API has meaning only inside that server's OS.
func localOpenCodeDatabase(ctx context.Context, host, server int, path string) bool {
	if server <= 1 || host <= 1 || host == server || !nativeOpenDatabase(ctx, server, path) {
		return false
	}
	if parent, err := codexProcessParent(server); err == nil && parent == host {
		return true
	}
	return nativeLoopbackConnection(ctx, host, server)
}
