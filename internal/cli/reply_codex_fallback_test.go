package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The public thread/read reply omits live TUI ownership, subscriber count and
// incarnation. Keep those facts in the fixture to model distinct native states
// that look identical to a notification consumer.
type fallbackThread struct {
	id, session, incarnation, owner string
}
type fallbackDaemon struct {
	mu          sync.Mutex
	loaded      []fallbackThread
	methods     []string
	connections atomic.Int32
	transcript  string
}

func (d *fallbackDaemon) response(method string, params json.RawMessage) any {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.methods = append(d.methods, method)
	switch method {
	case "initialize":
		return map[string]any{"userAgent": "codex/0.170.0"}
	case "thread/loaded/list":
		var ids []string
		for _, thread := range d.loaded {
			ids = append(ids, thread.id)
		}
		return map[string]any{"data": ids, "nextCursor": nil}
	case "thread/read":
		var p struct {
			ID string `json:"threadId"`
		}
		_ = json.Unmarshal(params, &p)
		for _, thread := range d.loaded {
			if thread.id == p.ID {
				return map[string]any{"thread": map[string]any{"id": thread.id, "sessionId": thread.session, "path": d.transcript, "status": map[string]any{"type": "idle"}, "canAcceptDirectInput": true}}
			}
		}
	}
	return map[string]any{}
}

func (d *fallbackDaemon) serve(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
	if rw.Flush() != nil {
		return
	}
	for {
		body, err := fallbackRPCFrame(rw.Reader)
		if err != nil {
			return
		}
		var q struct {
			Method string
			ID     int
			Params json.RawMessage
		}
		if json.Unmarshal(body, &q) != nil {
			return
		}
		result := d.response(q.Method, q.Params)
		if q.ID == 0 {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"id": q.ID, "result": result})
		header := []byte{0x81, byte(len(raw))}
		if len(raw) >= 126 {
			header = []byte{0x81, 126, byte(len(raw) >> 8), byte(len(raw))}
		}
		if _, err = conn.Write(append(header, raw...)); err != nil {
			return
		}
	}
}
func fallbackRPCFrame(in *bufio.Reader) ([]byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(in, h[:]); err != nil {
		return nil, err
	}
	if h[0] != 0x81 || h[1]&0x80 == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(h[1] & 127)
	switch n {
	case 127:
		return nil, io.ErrUnexpectedEOF
	case 126:
		var size uint16
		if err := binary.Read(in, binary.BigEndian, &size); err != nil {
			return nil, err
		}
		n = int(size)
	}
	var mask [4]byte
	if _, err := io.ReadFull(in, mask[:]); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(in, b); err != nil {
		return nil, err
	}
	for i := range b {
		b[i] ^= mask[i%4]
	}
	return b, nil
}

// Serve on the real guarded default discovery path for an isolated CODEX_HOME.
// Only the unique socket associated with this test's home is created/removed.
func listenFallbackDaemon(t *testing.T, home string, d *fallbackDaemon) {
	t.Helper()
	parent := filepath.Join(home, "app-server-control")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, fmt.Sprintf("codex-daemon-%d", os.Geteuid()))
	if err = os.Mkdir(dir, 0700); err == nil {
		t.Cleanup(func() { _ = os.Remove(dir) }) // Only removes our directory when empty.
	} else if !os.IsExist(err) {
		t.Fatal(err)
	}
	stat, err := os.Lstat(dir)
	if err != nil || !stat.IsDir() || stat.Mode().Perm() != 0700 {
		t.Fatal("invalid fixture socket directory", err)
	}
	advertised := filepath.Join(parent, "app-server-control.sock")
	physical := filepath.Join(dir, digest(advertised))
	l, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: physical})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err = os.Chmod(physical, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(physical, advertised); err != nil {
		t.Fatal(err)
	}
	server := http.Server{Handler: http.HandlerFunc(d.serve), ConnState: func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			d.connections.Add(1)
		}
	}}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
}

func TestReplyCodexOrdinaryCannotSelectLogicalThread(t *testing.T) {
	const thread = "codex-root-thread"
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	daemon := &fallbackDaemon{transcript: writeCodexMetadata(t, home, thread, nil)}
	listenFallbackDaemon(t, home, daemon)
	fx := newReplyFixture(t)
	t.Setenv("MOBTERM_CONFIG_DIR", fx.s.Dir)
	p := fx.pairs[0]
	for _, mode := range []string{"idle-unsubscribed-before-unload", "unload-reload-same-id", "tree-shared-session", "embedded-profile-with-default-daemon"} {
		t.Run(mode, func(t *testing.T) {
			original := fallbackThread{thread, "tree-session", "original", "original-tui"}
			daemon.mu.Lock()
			daemon.loaded = []fallbackThread{original}
			daemon.mu.Unlock()
			// Both shared and embedded launches produce the same legacy notification
			// fields. There is no launch flag, owner handle or runtime epoch on wire.
			raw, _ := json.Marshal(map[string]any{"type": "agent-turn-complete", "thread-id": thread, "turn-id": mode, "client": "codex-tui"})
			ctx := context.Background()
			a, input, err := parseActivity(ctx, fx.s, "codex", raw)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := admitCodexActivity(ctx, fx.s, fx.l, &a, input); err != nil || !ok {
				t.Fatal("invalid original fixture", err)
			}
			binding := replyIntentBinding(a, fx.l, p, "")
			d, err := enqueueBoundDelivery(ctx, fx.s, p, a.Reminder, a.Occurrence, fx.l, "codex", "", nil, nil, binding)
			if err != nil {
				t.Fatal(err)
			}
			daemon.mu.Lock()
			switch mode {
			case "idle-unsubscribed-before-unload":
				daemon.loaded[0].owner = ""
			case "unload-reload-same-id":
				daemon.loaded = nil
				daemon.loaded = append(daemon.loaded, fallbackThread{thread, original.session, "reloaded", "other-tui"})
			case "tree-shared-session":
				daemon.loaded = append(daemon.loaded, fallbackThread{"separate-child", original.session, "child", "child-tui"})
			case "embedded-profile-with-default-daemon":
				// The original --profile TUI owns an embedded runtime. The default
				// daemon concurrently hosts an identically named logical thread.
				daemon.loaded = []fallbackThread{{thread, original.session, "default-daemon", "default-tui"}}
			}
			daemon.mu.Unlock()
			// Positive control through real discovery/handshake. A loaded exact ID
			// with direct input is available for each scenario.
			client, err := codexConnect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.thread(ctx, codexAsyncHook{SessionID: original.session, AgentID: thread, TranscriptPath: daemon.transcript}, thread)
			_ = client.conn.Close()
			if err != nil {
				t.Fatal("fixture did not provide eligible logical thread", err)
			}
			before := daemon.connections.Load()
			daemon.mu.Lock()
			methods := slices.Clone(daemon.methods)
			daemon.mu.Unlock()
			f := replyFrame{bodyIdentity: bodyIdentity{1, p.APIURL, p.InstallationID, p.PairID, d.EventID, d.Reply.Envelope, randomID()}, Expires: time.Now().Unix() + 30}
			for _, op := range []string{"inspect", "send"} {
				if op == "send" {
					f = replySendFrame(f, replyRecord{SourceID: binding.SourceID, Cursor: binding.Cursor, Agent: "codex"}, "literal reply")
				}
				key, _ := decode64(p.EncryptionKey, 32)
				f.Proof, _ = replyMAC(key, op, f)
				var out, diag bytes.Buffer
				exit := run([]string{"reply", op, "--stdin-frame"}, bytes.NewReader(interactionFrameBytes(f)), &out, &diag)
				var got replyResult
				if exit != 0 || json.Unmarshal(out.Bytes(), &got) != nil || got.State != "unavailable" || got.Reason != "ordinary_reply_unavailable" {
					t.Fatal(op, "did not use stable terminal fallback", exit, diag.String())
				}
			}
			daemon.mu.Lock()
			after := slices.Clone(daemon.methods)
			daemon.mu.Unlock()
			if daemon.connections.Load() != before || !slices.Equal(after, methods) {
				t.Fatal("ordinary reply accessed native daemon", after)
			}
			if len(methods) < 3 || !slices.Contains(methods, "thread/loaded/list") || !slices.Contains(methods, "thread/read") {
				t.Fatal("positive control did not query daemon")
			}
		})
	}
}
