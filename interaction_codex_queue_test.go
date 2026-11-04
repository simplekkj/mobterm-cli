package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func codexTestRPC(t *testing.T, serve func(map[string]json.RawMessage) (any, bool)) *codexRPC {
	t.Helper()
	dir, err := os.MkdirTemp("", "mc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		var key string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "Sec-WebSocket-Key:") {
				key = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Key:"))
			}
			if line == "\r\n" {
				break
			}
		}
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		for {
			var h [2]byte
			if _, err = io.ReadFull(r, h[:]); err != nil {
				return
			}
			if h[0] != 0x81 || h[1]&0x80 == 0 {
				t.Error("invalid/unmasked client frame")
				return
			}
			n := uint64(h[1] & 127)
			if n == 126 {
				var b [2]byte
				io.ReadFull(r, b[:])
				n = uint64(binary.BigEndian.Uint16(b[:]))
			} else if n == 127 {
				var b [8]byte
				io.ReadFull(r, b[:])
				n = binary.BigEndian.Uint64(b[:])
			}
			if n > interactionFrameLimit {
				t.Error("unbounded frame")
				return
			}
			var mask [4]byte
			if _, err = io.ReadFull(r, mask[:]); err != nil {
				return
			}
			raw := make([]byte, int(n))
			if _, err = io.ReadFull(r, raw); err != nil {
				return
			}
			for i := range raw {
				raw[i] ^= mask[i%4]
			}
			var request map[string]json.RawMessage
			if err = json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			result, reply := serve(request)
			if !reply {
				return
			}
			if request["id"] == nil {
				continue
			}
			body, _ := json.Marshal(map[string]any{"id": request["id"], "result": result})
			header := []byte{0x81}
			if len(body) < 126 {
				header = append(header, byte(len(body)))
			} else {
				header = append(header, 126, byte(len(body)>>8), byte(len(body)))
			}
			conn.Write(append(header, body...))
		}
	}()
	conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); <-done })
	pid, uid, err := codexSocketPeer(conn)
	if err != nil || pid != os.Getpid() || uid != os.Geteuid() {
		t.Fatalf("peer %d/%d: %v", pid, uid, err)
	}
	client, err := codexHandshake(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	client.peerPID = pid
	client.peerBirth, err = processIdentity(pid)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func codexTestHook() []byte {
	return []byte(`{"session_id":"session","agent_id":"thread","turn_id":"turn","tool_use_id":"call","transcript_path":"/synthetic/transcript.jsonl","hook_event_name":"PostToolUse","tool_name":"request_user_input_async","tool_input":{"questions":[{"title":"Choose one","options":["A","B"]},{"title":"Details"}]},"tool_response":"{\"accepted\":true}"}`)
}
func TestCodexAsyncFinalContract(t *testing.T) {
	raw := codexTestHook()
	_, s, err := codexFollowupSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "followupMessage" || s.TTL != 300*time.Second || !strings.Contains(s.Message, "Details") || !strings.Contains(s.Message, "- B") || len(s.Fields) != 1 || s.Fields[0].ID != "text" {
		t.Fatalf("incomplete contract: %+v", s)
	}
	for _, change := range [][2]string{{"PostToolUse", "PreToolUse"}, {"request_user_input_async", "request_user_input"}, {`\"accepted\":true`, `\"accepted\":false`}, {`"transcript_path":"/synthetic/transcript.jsonl"`, `"transcript_path":"relative"`}, {`"title":"Details"`, `"title":"Details","unknown":true`}, {`"title":"Details"`, `"title":"Details","title":"Duplicate"`}} {
		t.Run(change[1], func(t *testing.T) {
			_, _, err := codexFollowupSpec([]byte(strings.Replace(string(raw), change[0], change[1], 1)))
			if err == nil {
				t.Fatal("unsupported native payload accepted")
			}
		})
	}
}
func TestCodexExistingThreadMapping(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-session", "wrong-path", "not-loaded", "no-direct", "unknown-direct", "duplicate", "ambiguous", "paged", "wrong-agent", "owner-changed"} {
		t.Run(mode, func(t *testing.T) {
			client := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				var method string
				json.Unmarshal(req["method"], &method)
				switch method {
				case "thread/loaded/list":
					ids := []string{"thread"}
					if mode == "duplicate" {
						ids = append(ids, "thread")
					}
					if mode == "ambiguous" {
						ids = append(ids, "thread2")
					}
					var cursor any
					if mode == "paged" {
						cursor = "next"
					}
					return map[string]any{"data": ids, "nextCursor": cursor}, true
				case "thread/read":
					var params struct {
						ThreadID     string `json:"threadId"`
						IncludeTurns bool   `json:"includeTurns"`
					}
					json.Unmarshal(req["params"], &params)
					if params.IncludeTurns {
						t.Error("must not request transcript")
					}
					session, path, status := "session", "/synthetic/transcript.jsonl", "active"
					var direct any = true
					switch mode {
					case "wrong-session":
						session = "another"
					case "wrong-path":
						path = "/another"
					case "not-loaded":
						status = "notLoaded"
					case "no-direct":
						direct = false
					case "unknown-direct":
						direct = nil
					}
					return map[string]any{"thread": map[string]any{"id": params.ThreadID, "sessionId": session, "path": path, "status": map[string]string{"type": status}, "canAcceptDirectInput": direct}}, true
				default:
					t.Errorf("unexpected native method %s", method)
					return nil, false
				}
			})
			p, _, _ := codexFollowupSpec(codexTestHook())
			if mode == "ambiguous" {
				p.AgentID = ""
			}
			if mode == "wrong-agent" {
				p.AgentID = "another"
			}
			if mode == "owner-changed" {
				client.peerBirth = "stale"
			}
			thread, err := client.thread(context.Background(), p, "")
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mapping %s: %+v %v", mode, thread, err)
			}
		})
	}
}
func TestCodexQueueSingleMutationAndReceipt(t *testing.T) {
	for _, mode := range []string{"ack", "lost-ack", "wrong-receipt"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			client := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				calls.Add(1)
				var method string
				json.Unmarshal(req["method"], &method)
				if method != "thread/queue/add" {
					t.Errorf("unexpected mutation %s", method)
				}
				var params struct {
					ThreadID string `json:"threadId"`
					ClientID string `json:"clientUserMessageId"`
					Input    []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"input"`
				}
				if json.Unmarshal(req["params"], &params) != nil || params.ThreadID != "thread" || params.ClientID != "decision" || len(params.Input) != 1 || params.Input[0].Text != "literal $(do not execute)\n你好" {
					t.Error("queue input changed")
				}
				id := "decision"
				if mode == "wrong-receipt" {
					id = "other"
				}
				return map[string]any{"queuedSubmission": map[string]string{"id": "queued", "clientUserMessageId": id}}, mode != "lost-ack"
			})
			err := client.queueFollowup(context.Background(), "thread", "decision", "literal $(do not execute)\n你好")
			if (err == nil) != (mode == "ack") {
				t.Fatalf("receipt %s: %v", mode, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("mutation retried %d times", calls.Load())
			}
		})
	}
}
func TestCodexGuardedRendezvous(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "c-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	parent := filepath.Join(home, "app-server-control")
	if err = os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	parent, _ = filepath.EvalSymlinks(parent)
	directory := filepath.Join(root, fmt.Sprintf("codex-daemon-%d", os.Geteuid()))
	os.Mkdir(directory, 0700)
	advertised := filepath.Join(parent, "app-server-control.sock")
	physical := filepath.Join(directory, digest(advertised))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: physical, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	os.Chmod(physical, 0600)
	os.Symlink(physical, advertised)
	got, err := codexVerifiedSocketAt(home, root)
	if err != nil || got != physical {
		t.Fatalf("valid rendezvous %s %v", got, err)
	}
	os.Chmod(directory, 0755)
	if _, err = codexVerifiedSocketAt(home, root); err == nil {
		t.Fatal("nonprivate directory accepted")
	}
	os.Chmod(directory, 0700)
	os.Chmod(physical, 0666)
	if _, err = codexVerifiedSocketAt(home, root); err == nil {
		t.Fatal("nonprivate socket accepted")
	}
	os.Chmod(physical, 0600)
	os.Remove(advertised)
	os.Symlink("/wrong/socket", advertised)
	if _, err = codexVerifiedSocketAt(home, root); err == nil {
		t.Fatal("wrong alias accepted")
	}
	if !codexIsAncestor(os.Getpid()) || !codexIsAncestor(os.Getppid()) || codexIsAncestor(-1) {
		t.Fatal("process ancestry mismatch")
	}
}
