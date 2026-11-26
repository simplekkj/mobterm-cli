package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var errCodexPaneOwner = errors.New("codex_terminal_owner_unconfirmed")

// The process-role reader is replaced only by in-process native callback
// fixtures, which have no Codex host. Kernel session ownership still runs.
var terminalCodexHost = codexTerminalHost

func codexPaneOwner(ctx context.Context, panePID int, nativeCommand bool) bool {
	paneSession, err := unix.Getsid(panePID)
	if err != nil || panePID <= 0 || !codexIsAncestor(panePID) {
		return false
	}
	hookSession, err := unix.Getsid(0)
	if err != nil {
		return false
	}
	if hookSession == paneSession {
		return terminalCodexHost(ctx, os.Getppid(), panePID)
	}
	if !nativeCommand || !codexIsAncestor(hookSession) {
		return false
	}
	// Codex launches native shell hooks with ProcessMode::NewSession. Check
	// the launching host just outside this one boundary. Walking further
	// would admit a detached daemon started from the first still-live pane.
	host, err := codexProcessParent(hookSession)
	if err != nil || host <= 1 {
		return false
	}
	hostSession, err := unix.Getsid(host)
	return err == nil && hostSession == paneSession && terminalCodexHost(ctx, host, panePID)
}

type codexOrigin uint8

const (
	codexOriginUnknown codexOrigin = iota
	codexOriginRoot
	codexOriginNonRoot
)
const (
	codexMetadataEntries    = 4096
	codexMetadataCandidates = 8
	codexMetadataLineBytes  = 64 * 1024
	codexMetadataTotalBytes = 256 * 1024
	codexMetadataDuration   = 150 * time.Millisecond
)

// Only the canonical first metadata record is inspected. No transcript path
// supplied by a hook is opened and no message records are read.
func codexCompletionOrigin(ctx context.Context, thread string) codexOrigin {
	if thread == "" || len(thread) > 256 || strings.ContainsAny(thread, "/\\\x00") {
		return codexOriginUnknown
	}
	config, err := configPath("codex")
	if err != nil {
		return codexOriginUnknown
	}
	home, err := filepath.Abs(filepath.Dir(config))
	if err != nil {
		return codexOriginUnknown
	}
	fd, err := openCodexRoot(home)
	if err != nil {
		return codexOriginUnknown
	}
	root := os.NewFile(uintptr(fd), "codex-metadata-root")
	defer root.Close()
	deadline := time.Now().Add(codexMetadataDuration)
	entries, candidates, total := 0, 0, 0
	origin := codexOriginUnknown
	fingerprint := ""
	failed := false
	check := func() bool { return !failed && ctx.Err() == nil && time.Now().Before(deadline) }
	var walk func(*os.File, int, bool)
	walk = func(dir *os.File, depth int, archived bool) {
		for check() {
			list, readErr := dir.ReadDir(64)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				failed = true
				return
			}
			for _, entry := range list {
				entries++
				if entries > codexMetadataEntries || !check() {
					failed = true
					return
				}
				name := entry.Name()
				candidate := codexRolloutForThread(name, thread)
				if candidate && depth != 0 && depth != 3 {
					failed = true
					return
				}
				// Supported layouts are sessions/YYYY/MM/DD, legacy sessions/, and
				// archived_sessions/. Unrecognized directories are never traversed.
				directory := !archived && depth < 3 && metadataDatePart(name, depth)
				if !candidate && !directory {
					continue
				}
				if entry.Type()&os.ModeSymlink != 0 {
					failed = true
					return
				}
				flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
				if directory {
					flags |= unix.O_DIRECTORY
				}
				childFD, openErr := unix.Openat(int(dir.Fd()), name, flags, 0)
				if openErr != nil {
					failed = true
					return
				}
				child := os.NewFile(uintptr(childFD), "codex-metadata")
				info, statErr := child.Stat()
				if statErr != nil {
					child.Close()
					failed = true
					return
				}
				if directory {
					walk(child, depth+1, archived)
				} else {
					candidates++
					if candidates > codexMetadataCandidates || !info.Mode().IsRegular() {
						child.Close()
						failed = true
						return
					}
					// Reading one byte at a time deliberately avoids buffering bytes from
					// the next record (which may contain a user prompt or tool output).
					line := make([]byte, 0, 1024)
					var one [1]byte
					for check() && len(line) < codexMetadataLineBytes && total < codexMetadataTotalBytes {
						n, e := child.Read(one[:])
						total += n
						if n == 1 {
							if one[0] == '\n' {
								break
							}
							line = append(line, one[0])
						}
						if e != nil {
							if !errors.Is(e, io.EOF) {
								failed = true
							}
							break
						}
					}
					if !check() || len(line) >= codexMetadataLineBytes || total >= codexMetadataTotalBytes {
						failed = true
					}
					found, identity := parseCodexMetadata(line, thread)
					if found == codexOriginUnknown || fingerprint != "" && (identity != fingerprint || found != origin) {
						failed = true
					}
					origin, fingerprint = found, identity
				}
				child.Close()
				if failed {
					return
				}
			}
			if errors.Is(readErr, io.EOF) {
				return
			}
		}
		failed = true
	}
	for _, name := range []string{"sessions", "archived_sessions"} {
		if !check() {
			return codexOriginUnknown
		}
		subFD, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) {
			continue
		}
		if e != nil {
			return codexOriginUnknown
		}
		sub := os.NewFile(uintptr(subFD), "codex-metadata-directory")
		walk(sub, 0, name == "archived_sessions")
		sub.Close()
	}
	if failed || !check() {
		return codexOriginUnknown
	}
	return origin
}

var codexRolloutUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func codexRolloutForThread(name, thread string) bool {
	const layout = "2006-01-02T15-04-05"
	core, ok := strings.CutPrefix(name, "rollout-")
	if !ok {
		return false
	}
	core, ok = strings.CutSuffix(core, ".jsonl")
	if !ok || len(core) <= len(layout) || core[len(layout)] != '-' {
		return false
	}
	// A revert adds a distinct rollout ID after the stable thread ID.
	// Only the thread component selects candidates for metadata verification.
	threadID, rolloutID, reverted := strings.Cut(core[len(layout)+1:], "_")
	if threadID != thread || reverted && !(codexRolloutUUID.MatchString(threadID) && codexRolloutUUID.MatchString(rolloutID)) {
		return false
	}
	_, err := time.Parse(layout, core[:len(layout)])
	return err == nil
}

func metadataDatePart(name string, depth int) bool {
	length := 2
	if depth == 0 {
		length = 4
	}
	if len(name) != length {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	if depth == 1 && (name < "01" || name > "12") || depth == 2 && (name < "01" || name > "31") {
		return false
	}
	return true
}
func parseCodexMetadata(line []byte, thread string) (codexOrigin, string) {
	// Duplicate keys are ambiguous even when encoding/json would choose the
	// last occurrence. Validate the bounded header before decoding its whitelist.
	if !uniqueJSONKeys(line) {
		return codexOriginUnknown, ""
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string          `json:"id"`
			SessionID    string          `json:"session_id"`
			Source       json.RawMessage `json:"source"`
			Parent       json.RawMessage `json:"parent_thread_id"`
			ThreadSource json.RawMessage `json:"thread_source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" || record.Payload.ID != thread {
		return codexOriginUnknown, ""
	}
	p := record.Payload
	if p.SessionID != "" && p.SessionID != thread {
		return codexOriginUnknown, ""
	}
	var source string
	root, nonRoot := false, false
	if json.Unmarshal(p.Source, &source) == nil {
		switch source {
		case "cli", "vscode", "exec", "mcp":
			root = true
		default:
			return codexOriginUnknown, ""
		}
	} else {
		var obj map[string]json.RawMessage
		if json.Unmarshal(p.Source, &obj) != nil || len(obj) != 1 {
			return codexOriginUnknown, ""
		}
		if _, ok := obj["subagent"]; ok {
			nonRoot = true
		}
		if _, ok := obj["internal"]; ok {
			nonRoot = true
		}
		if !nonRoot {
			return codexOriginUnknown, ""
		}
	}
	if len(p.Parent) > 0 && string(p.Parent) != "null" {
		var parent string
		if json.Unmarshal(p.Parent, &parent) != nil || parent == "" || root {
			return codexOriginUnknown, ""
		}
		nonRoot = true
	}
	if len(p.ThreadSource) > 0 && string(p.ThreadSource) != "null" {
		var ts string
		if json.Unmarshal(p.ThreadSource, &ts) != nil || ts == "" {
			return codexOriginUnknown, ""
		}
		if ts == "user" {
			if nonRoot {
				return codexOriginUnknown, ""
			}
		} else {
			if root {
				return codexOriginUnknown, ""
			}
			nonRoot = true
		}
	}
	encoded, _ := json.Marshal(p)
	if nonRoot {
		return codexOriginNonRoot, string(encoded)
	}
	if root {
		return codexOriginRoot, string(encoded)
	}
	return codexOriginUnknown, ""
}
func uniqueJSONKeys(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		tok, e := d.Token()
		if e != nil {
			return false
		}
		delimiter, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				name, ok := key.(string)
				if e != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, e = d.Token()
		return e == nil
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return errors.Is(err, io.EOF)
}

func codexOriginError(origin codexOrigin) error {
	if origin == codexOriginNonRoot {
		return nil
	}
	return errors.New("codex_completion_origin_unknown")
}

// Root proofs are attached only to a verified target and the exact native
// session/turn. Pre-upgrade StoredRuns do not carry this field.
func codexKnownRoot(ctx context.Context, s Store, l Locator, a Activity) (bool, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	db, err := loadRuns(s)
	if err != nil {
		return false, err
	}
	for _, r := range db.Runs {
		if r.CodexRoot && r.Agent == "codex" && r.Session == a.Session && r.Instance == a.Instance && r.Run == a.Run && r.Locator == l {
			return true, nil
		}
	}
	return false, nil
}

// Open each component relative to a pinned directory descriptor so neither a
// configured-root ancestor nor a session-directory rename can redirect reads.
func openCodexRoot(home string) (int, error) {
	if !filepath.IsAbs(home) || len(home) > 4096 {
		return -1, errors.New("invalid_codex_root")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(filepath.Clean(home), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
	}
	return fd, nil
}

func admitCodexActivity(ctx context.Context, s Store, l Locator, a *Activity, input activityInput) (bool, error) {
	if input.AgentID != "" || a.Reminder == "needs_input" || a.ResolveRequest || input.HookEventName == "PermissionRequest" || input.ToolName == "request_user_input_async" {
		return true, nil
	}
	if input.HookEventName != "UserPromptSubmit" && input.HookEventName != "NotifyComplete" && input.HookEventName != "Interrupt" {
		return true, nil
	}
	known, err := codexKnownRoot(ctx, s, l, *a)
	if err != nil {
		return false, err
	}
	if known {
		a.CodexRoot = true
		return true, nil
	}
	if input.HookEventName == "UserPromptSubmit" || input.HookEventName == "NotifyComplete" || input.HookEventName == "Interrupt" {
		origin := codexCompletionOrigin(ctx, a.Session)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if origin != codexOriginRoot {
			return false, codexOriginError(origin)
		}
		a.CodexRoot = true
	}
	// Ordinary observations without a start remain untrusted; they never scan
	// metadata or turn an old isolated StoredRun into root provenance.
	return true, nil
}
