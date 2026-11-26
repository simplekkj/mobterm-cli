package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func (c *codexRPC) checkAppendOwner(ctx context.Context, source codexRuntimeSource) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	physical, err := codexVerifiedSocket(source.Home)
	if err != nil || physical != source.Socket || source.Home != c.home || !filepath.IsAbs(source.Home) {
		return errors.New("codex_daemon_socket_untrusted")
	}
	birth, err := processIdentity(source.PeerPID)
	if err != nil || birth != c.peerBirth || birth != source.PeerBirth || source.PeerPID != c.peerPID ||
		source.UID != os.Geteuid() || !codexDaemonImage(ctx, c.peerPID) {
		return errors.New("codex_daemon_owner_changed")
	}
	return nil
}

func openAppendNative(ctx context.Context, source codexRuntimeSource, thread string, sending bool) (*codexRPC, string, error) {
	if !codexRolloutUUID.MatchString(thread) {
		return nil, "", errors.New("codex_thread_identity_unverified")
	}
	client, err := dialCodex(ctx, source.Home, &source)
	if err != nil {
		return nil, "", err
	}
	state, err := client.appendThreadState(ctx, thread, sending)
	if err == nil {
		err = client.checkAppendOwner(ctx, source)
	}
	if err != nil {
		client.conn.Close()
		return nil, "", err
	}
	return client, state, nil
}

func (c *codexRPC) appendThreadState(ctx context.Context, thread string, sending bool) (string, error) {
	result, err := c.request(ctx, "thread/read", map[string]any{"threadId": thread, "includeTurns": false})
	if err != nil {
		return "", errors.New("codex_thread_unavailable")
	}
	var read struct {
		Thread struct {
			ID                   string
			ThreadSource         string
			Originator           string
			ParentThreadID       json.RawMessage
			Ephemeral            *bool
			CanAcceptDirectInput *bool
			Status               struct{ Type string }
		}
	}
	t := &read.Thread
	if json.Unmarshal(result, &read) != nil || t.ID != thread || t.ThreadSource != "user" ||
		t.Originator != "codex-tui" || string(t.ParentThreadID) != "null" || t.Ephemeral == nil || *t.Ephemeral {
		return "", errors.New("codex_thread_identity_unverified")
	}
	switch t.Status.Type {
	case "notLoaded", "idle", "active":
	default:
		return "", errors.New("codex_thread_unavailable")
	}
	if sending && t.Status.Type != "notLoaded" && (t.CanAcceptDirectInput == nil || !*t.CanAcceptDirectInput) {
		return "", errors.New("codex_thread_unavailable")
	}
	// This read-only operation checks support, including the native check for
	// an archived/deleted unloaded thread. The add transaction enforces capacity.
	if _, err := c.appendPage(ctx, "thread/queue/list", thread, "", 1); err != nil {
		if reason := appendRefusal(err, thread); reason != "" {
			return "", errors.New(reason)
		}
		return "", errors.New("codex_queue_unavailable")
	}
	state := t.Status.Type
	if state == "idle" || state == "notLoaded" {
		raw, e := c.request(ctx, "thread/turns/list", map[string]any{"threadId": thread, "itemsView": "notLoaded", "sortDirection": "desc", "limit": 1})
		var turns struct{ Data []struct{ Status string } }
		if e == nil && json.Unmarshal(raw, &turns) == nil && len(turns.Data) == 1 && turns.Data[0].Status == "interrupted" {
			state = "interrupted"
		}
	}
	return state, nil
}

// Only messages verified against the upstream pre-enqueue paths are definitive.
func appendRefusal(err error, thread string) string {
	if errors.Is(err, errCodexNoRequestBytes) {
		return "codex_daemon_unreachable"
	}
	var r *codexRejection
	if !errors.As(err, &r) {
		return ""
	}
	if r.Code == -32601 {
		return "codex_queue_unavailable"
	}
	if r.Code != -32600 && r.Code != -32602 {
		return ""
	}
	switch r.Message {
	case "queue cannot contain more than 100 submissions":
		return "codex_queue_full"
	case "user message queue is unavailable":
		return "codex_queue_unavailable"
	case "thread not found: " + thread, "ephemeral thread does not support queued submissions: " + thread,
		"session " + thread + " is archived. Run `codex unarchive " + thread + "` to unarchive it first.":
		return "codex_thread_unavailable"
	}
	return ""
}

type appendTextInput struct {
	Type     string            `json:"type"`
	Text     string            `json:"text"`
	Elements []json.RawMessage `json:"text_elements"`
}

func matchesAppendText(input []appendTextInput, originalDigest string) bool {
	return len(input) == 1 && input[0].Type == "text" && len(input[0].Elements) == 0 && digest(input[0].Text) == originalDigest
}

type appendQueueItem struct {
	ID       string            `json:"id"`
	ClientID string            `json:"clientUserMessageId"`
	Input    []appendTextInput `json:"input"`
}

func (c *codexRPC) appendOnce(ctx context.Context, thread, clientID, text string) (state, queueID, reason string) {
	if ctx.Err() != nil {
		return "not_dispatched", "", "codex_dispatch_expired"
	}
	input := []appendTextInput{{Type: "text", Text: text, Elements: []json.RawMessage{}}}
	raw, err := c.request(ctx, "thread/queue/add", map[string]any{"threadId": thread, "clientUserMessageId": clientID, "input": input})
	if err != nil {
		if reason := appendRefusal(err, thread); reason != "" {
			return "not_dispatched", "", reason
		}
		return "delivery_unknown", "", ""
	}
	var ack struct{ QueuedSubmission appendQueueItem }
	if json.Unmarshal(raw, &ack) != nil || !opaqueID.MatchString(ack.QueuedSubmission.ID) || ack.QueuedSubmission.ClientID != clientID || !matchesAppendText(ack.QueuedSubmission.Input, digest(text)) {
		return "delivery_unknown", "", ""
	}
	return "queued", ack.QueuedSubmission.ID, ""
}

type appendPageResult struct {
	Data   []json.RawMessage
	Cursor string
}

func (c *codexRPC) appendPage(ctx context.Context, method, thread, cursor string, limit int) (appendPageResult, error) {
	params := map[string]any{"threadId": thread, "limit": limit}
	if cursor != "" {
		params["cursor"] = cursor
	}
	if method == "thread/items/list" {
		params["sortDirection"] = "desc"
	}
	raw, err := c.request(ctx, method, params)
	var page appendPageResult
	if err != nil {
		return page, err
	}
	var wire struct {
		Data       []json.RawMessage
		NextCursor json.RawMessage
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Data == nil || len(wire.Data) > limit || len(wire.NextCursor) == 0 ||
		(string(wire.NextCursor) != "null" && (json.Unmarshal(wire.NextCursor, &page.Cursor) != nil || !nativeInteractionID(page.Cursor))) {
		return page, errors.New("codex_rpc_invalid_response")
	}
	page.Data = wire.Data
	return page, nil
}

func (c *codexRPC) appendEvidence(ctx context.Context, thread, clientID, queueID, textDigest string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	state, foundID := "", ""
	for _, method := range []string{"thread/queue/list", "thread/items/list"} {
		cursor := ""
		seen := map[string]bool{}
		limit := 100
		if method == "thread/items/list" {
			limit = 16
		}
		for n := 0; n < 4; n++ {
			page, err := c.appendPage(ctx, method, thread, cursor, limit)
			if err != nil {
				break
			}
			for _, entry := range page.Data {
				if method == "thread/queue/list" {
					var item appendQueueItem
					if json.Unmarshal(entry, &item) != nil {
						return "", ""
					}
					if item.ClientID != clientID {
						if queueID != "" && queueID == item.ID {
							return "", ""
						}
						continue
					}
					if !opaqueID.MatchString(item.ID) || (queueID != "" && queueID != item.ID) || (foundID != "" && foundID != item.ID) {
						return "", ""
					}
					next := "waiting"
					if !matchesAppendText(item.Input, textDigest) {
						next = "content_changed"
					}
					if state != "" && state != next {
						return "", ""
					}
					state, foundID = next, item.ID
				} else {
					var history struct {
						Item struct {
							Type, ClientID string
							Content        []appendTextInput
						}
					}
					if json.Unmarshal(entry, &history) != nil {
						return "", ""
					}
					if history.Item.Type != "userMessage" || history.Item.ClientID != clientID {
						continue
					}
					if state == "content_changed" || !matchesAppendText(history.Item.Content, textDigest) {
						return "", ""
					}
					state = "admitted"
				}
			}
			if page.Cursor == "" || seen[page.Cursor] {
				break
			}
			cursor = page.Cursor
			seen[cursor] = true
		}
	}
	return state, foundID
}
