package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// This deliberately small WebSocket client only speaks the version-pinned local
// Codex protocol: masked outbound text, uncompressed final inbound text/control.
// It opens no listener, follows no redirects and never retries a native mutation.
type codexRPC struct {
	conn      *net.UnixConn
	reader    *bufio.Reader
	next      int
	peerPID   int
	peerBirth string
}

func codexHandshake(ctx context.Context, c *net.UnixConn) (*codexRPC, error) {
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	nonce := base64.StdEncoding.EncodeToString(key)
	if _, err := fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", nonce); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(c, 4096)
	var header []byte
	for len(header) < 8192 {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		header = append(header, b)
		if strings.HasSuffix(string(header), "\r\n\r\n") {
			break
		}
	}
	lines := strings.Split(string(header), "\r\n")
	if len(header) >= 8192 || len(lines) < 3 || lines[0] != "HTTP/1.1 101 Switching Protocols" {
		return nil, errors.New("codex_websocket_handshake")
	}
	headers := map[string]string{}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		k = strings.ToLower(k)
		if !ok || headers[k] != "" {
			return nil, errors.New("codex_websocket_handshake")
		}
		headers[k] = strings.TrimSpace(v)
	}
	sum := sha1.Sum([]byte(nonce + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if !strings.EqualFold(headers["upgrade"], "websocket") || !strings.EqualFold(headers["connection"], "upgrade") || headers["sec-websocket-accept"] != base64.StdEncoding.EncodeToString(sum[:]) || headers["sec-websocket-extensions"] != "" || headers["sec-websocket-protocol"] != "" {
		return nil, errors.New("codex_websocket_handshake")
	}
	return &codexRPC{conn: c, reader: reader}, nil
}
func (c *codexRPC) write(op byte, payload []byte) error {
	if len(payload) > interactionFrameLimit {
		return errors.New("codex_rpc_frame_too_large")
	}
	header := []byte{0x80 | op}
	n := len(payload)
	if n < 126 {
		header = append(header, 0x80|byte(n))
	} else if n <= 65535 {
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	} else {
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	for i, b := range payload {
		header = append(header, b^mask[i%4])
	}
	for len(header) > 0 {
		n, err := c.conn.Write(header)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		header = header[n:]
	}
	return nil
}
func (c *codexRPC) read() ([]byte, error) {
	for skipped := 0; skipped < 32; skipped++ {
		var h [2]byte
		if _, err := io.ReadFull(c.reader, h[:]); err != nil {
			return nil, err
		}
		if h[0]&0xf0 != 0x80 || h[1]&0x80 != 0 {
			return nil, errors.New("codex_rpc_unsupported_frame")
		}
		n := uint64(h[1] & 127)
		if n == 126 {
			var b [2]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return nil, e
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		} else if n == 127 {
			var b [8]byte
			if _, e := io.ReadFull(c.reader, b[:]); e != nil {
				return nil, e
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		op := h[0] & 15
		if n > interactionFrameLimit || (op >= 8 && n > 125) {
			return nil, errors.New("codex_rpc_frame_too_large")
		}
		payload := make([]byte, int(n))
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return nil, err
		}
		switch op {
		case 1:
			return payload, nil
		case 8:
			return nil, io.EOF
		case 9:
			if err := c.write(10, payload); err != nil {
				return nil, err
			}
		case 10:
		default:
			return nil, errors.New("codex_rpc_unsupported_frame")
		}
	}
	return nil, errors.New("codex_rpc_frame_limit")
}
func (c *codexRPC) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.conn.SetDeadline(deadline)
	c.next++
	id := c.next
	raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err = c.write(1, raw); err != nil {
		return nil, err
	}
	for ignored := 0; ignored < 64; ignored++ {
		raw, err = c.read()
		if err != nil {
			return nil, err
		}
		var object map[string]json.RawMessage
		if interactionJSON(raw, &object) != nil {
			return nil, errors.New("codex_rpc_invalid_response")
		}
		if _, isNotice := object["id"]; !isNotice {
			continue
		}
		var got int
		if json.Unmarshal(object["id"], &got) != nil || got != id {
			return nil, errors.New("codex_rpc_identity_mismatch")
		}
		if _, present := object["method"]; present {
			return nil, errors.New("codex_rpc_unexpected_request")
		}
		if _, present := object["error"]; present {
			return nil, errors.New("codex_rpc_rejected")
		}
		result, ok := object["result"]
		if !ok {
			return nil, errors.New("codex_rpc_invalid_response")
		}
		return result, nil
	}
	return nil, errors.New("codex_rpc_notification_limit")
}
