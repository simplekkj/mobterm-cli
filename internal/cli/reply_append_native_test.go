package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestAppendNativeACKRequiresOriginalLiteral(t *testing.T) {
	const message = "  追加\n🙂 "
	for _, tc := range []struct{ name, want string }{{"matched", "queued"}, {"changed", "delivery_unknown"}, {"wrong-client", "delivery_unknown"}, {"no-queue-id", "delivery_unknown"}, {"bad-queue-id", "delivery_unknown"}, {"lost", "delivery_unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				var p struct {
					ThreadID, ClientUserMessageID string
					Input                         []appendTextInput
				}
				if string(req["method"]) != `"thread/queue/add"` || json.Unmarshal(req["params"], &p) != nil || p.ThreadID != "original" || p.ClientUserMessageID != "client" || !matchesAppendText(p.Input, digest(message)) {
					t.Error("wrong native operation or body")
				}
				q := appendQueueItem{ID: "queue-id-fixture-0001", ClientID: p.ClientUserMessageID, Input: p.Input}
				switch tc.name {
				case "changed":
					q.Input[0].Text += "x"
				case "wrong-client":
					q.ClientID = "other"
				case "bad-queue-id":
					q.ID = " "
				case "no-queue-id":
					q.ID = ""
				}
				return map[string]any{"queuedSubmission": q}, tc.name != "lost"
			})
			state, id, _ := c.appendOnce(context.Background(), "original", "client", message)
			if state != tc.want || (id == "queue-id-fixture-0001") != (state == "queued") || c.next != 1 {
				t.Errorf("unexpected receipt %s/%s calls=%d", state, id, c.next)
			}
		})
	}
}
func TestAppendNativeKnownRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("disconnected"), ""}, {&codexRejection{-32603, "queue cannot contain more than 100 submissions"}, ""},
		{&codexRejection{-32602, "queue cannot contain more than 100 submissions"}, "codex_queue_full"},
		{&codexRejection{-32601, "method not found"}, "codex_queue_unavailable"},
		{&codexRejection{-32602, "thread not found: other"}, ""}, {&codexRejection{-32602, "thread not found: original"}, "codex_thread_unavailable"},
	} {
		if got := appendRefusal(tc.err, "original"); got != tc.want {
			t.Errorf("refusal %q, want %q", got, tc.want)
		}
	}
}
func TestAppendEvidencePagesAndBound(t *testing.T) {
	for _, tc := range []struct{ mode, state string }{{"queue", "waiting"}, {"edit", "content_changed"}, {"history", "admitted"}, {"gone", ""}, {"collision", ""}, {"wrong-queue", ""}, {"budget", ""}} {
		t.Run(tc.mode, func(t *testing.T) {
			c := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				var p struct {
					ThreadID, Cursor, SortDirection string
					Limit                           int
				}
				if json.Unmarshal(req["params"], &p) != nil || p.ThreadID != "original" {
					t.Error("wrong target")
				}
				var cursor any
				data := []any{}
				switch string(req["method"]) {
				case `"thread/queue/list"`:
					if p.Limit != 100 {
						t.Error("queue page size")
					}
					if tc.mode == "budget" {
						cursor = p.Cursor + "n"
					} else if p.Cursor == "" {
						cursor = "second"
					} else {
						q := appendQueueItem{ID: "queue-id-fixture-0001", ClientID: "client", Input: []appendTextInput{{Type: "text", Text: "original text"}}}
						switch tc.mode {
						case "queue":
							data = append(data, q)
						case "edit":
							q.Input[0].Text = "replacement"
							data = append(data, q)
						case "collision":
							data = append(data, q)
							q.ID = "different"
							data = append(data, q)
						case "wrong-queue":
							q.ClientID = "other"
							data = append(data, q)
						}
					}
				case `"thread/items/list"`:
					if p.Limit != 16 || p.SortDirection != "desc" {
						t.Error("unbounded history")
					}
					if tc.mode == "budget" {
						cursor = p.Cursor + "n"
					} else if tc.mode == "history" {
						data = append(data, map[string]any{"item": map[string]any{"type": "userMessage", "clientId": "client", "content": []appendTextInput{{Type: "text", Text: "original text"}}}})
					}
				default:
					t.Error("unexpected mutation")
				}
				return map[string]any{"data": data, "nextCursor": cursor}, true
			})
			state, _ := c.appendEvidence(context.Background(), "original", "client", "queue-id-fixture-0001", digest("original text"))
			if state != tc.state || tc.mode == "budget" && c.next != 8 {
				t.Errorf("state=%s rpc=%d", state, c.next)
			}
		})
	}
}

func TestAppendThreadEligibilityIndependentOfTurnAndTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, state, flag, latest, want string
		ephemeral, child, noDirect      bool
	}{
		{name: "idle", state: "idle", want: "idle"},
		{name: "busy", state: "active", want: "active"},
		{name: "approval", state: "active", flag: "waitingOnApproval", want: "active"},
		{name: "question", state: "active", flag: "waitingOnUserInput", want: "active"},
		{name: "unloaded", state: "notLoaded", noDirect: true, want: "notLoaded"},
		{name: "interrupted", state: "idle", latest: "interrupted", want: "interrupted"},
		{name: "ephemeral", state: "idle", ephemeral: true},
		{name: "subagent", state: "idle", child: true},
		{name: "cannot accept direct", state: "active", noDirect: true},
		{name: "unsupported status", state: "systemError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				switch string(req["method"]) {
				case `"thread/read"`:
					var parent any
					if tc.child {
						parent = "another"
					}
					return map[string]any{"thread": map[string]any{"id": "original", "threadSource": "user", "originator": "codex-tui", "parentThreadId": parent, "ephemeral": tc.ephemeral, "canAcceptDirectInput": !tc.noDirect, "status": map[string]any{"type": tc.state, "activeFlags": []string{tc.flag}}}}, true
				case `"thread/queue/list"`:
					return map[string]any{"data": []any{}, "nextCursor": nil}, true
				case `"thread/turns/list"`:
					return map[string]any{"data": []any{map[string]string{"id": "newer-turn", "status": tc.latest}}}, true
				default:
					t.Errorf("unexpected native method %s", req["method"])
					return nil, false
				}
			})
			got, err := c.appendThreadState(context.Background(), "original", true)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Errorf("state %q, err %v", got, err)
			}
		})
	}
}
