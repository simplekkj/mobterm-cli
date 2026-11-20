package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCodexCompletionMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "child", "background", "not-loaded", "different-thread", "later-turn", "in-progress"} {
		t.Run(mode, func(t *testing.T) {
			client := codexTestRPC(t, func(req map[string]json.RawMessage) (any, bool) {
				var method string
				var params map[string]any
				if json.Unmarshal(req["method"], &method) != nil || json.Unmarshal(req["params"], &params) != nil || params["threadId"] != "thread" {
					t.Error("missing exact thread identity")
				}
				switch method {
				case "thread/read":
					if params["includeTurns"] != false {
						t.Error("unexpected transcript request")
					}
					thread := map[string]any{"id": "thread", "threadSource": "user", "parentThreadId": nil, "originator": "codex-tui", "ephemeral": false, "status": map[string]any{"type": "idle"}}
					switch mode {
					case "child":
						thread["parentThreadId"] = "parent"
					case "background":
						thread["originator"] = "codex-exec"
					case "not-loaded":
						thread["status"] = map[string]string{"type": "notLoaded"}
					case "different-thread":
						thread["id"] = "sibling"
					}
					return map[string]any{"thread": thread}, true
				case "thread/turns/list":
					if params["itemsView"] != "notLoaded" || params["sortDirection"] != "desc" || params["limit"] != float64(1) {
						t.Error("requested unnecessary history")
					}
					turn := map[string]string{"id": "turn", "status": "completed"}
					if mode == "later-turn" {
						turn["id"] = "other"
					}
					if mode == "in-progress" {
						turn["status"] = "inProgress"
					}
					return map[string]any{"data": []any{turn}}, true
				default:
					t.Error("unexpected native method", method)
					return nil, false
				}
			})
			err := client.confirmCompletion(context.Background(), "thread", "turn")
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mode %s: %v", mode, err)
			}
		})
	}
}

func TestUnboundCodexPayload(t *testing.T) {
	l := fixtureLocator()
	p := payloadForRun(StoredRun{Agent: "codex", Session: "thread", Run: "turn", State: "turn_completed", Revision: 1, CodexRuntime: &codexRuntimeSource{Installation: l.InstallationID, Home: "/private/native-home", PeerPID: 123}}, randomID())
	p.Source = &noticeSource{Session: randomID(), RunOrder: 1}
	wire, _ := json.Marshal(p)
	var fields map[string]any
	if json.Unmarshal(wire, &fields) != nil || fields["installation_id"] != l.InstallationID || fields["locator"] != nil || fields["codex_runtime"] != nil {
		t.Fatal("unbound identity leaked or omitted", string(wire))
	}
	e := Envelope{V: 2, PairID: randomID(), EventID: randomID(), Kind: "turn_completed", StreamID: p.StreamID, Revision: 1, CapabilityGeneration: 1}
	key := make([]byte, 32)
	if _, err := sealRun(key, e, p, nil); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RunPayload){func(v *RunPayload) { v.Locator = l }, func(v *RunPayload) { v.Source = nil }, func(v *RunPayload) { v.InstallationID = "" }, func(v *RunPayload) { v.Agent = "pi" }, func(v *RunPayload) { v.Approval = &ApprovalHint{} }} {
		bad := p
		change(&bad)
		if _, err := sealRun(key, e, bad, nil); err == nil {
			t.Fatal("invalid unbound completion accepted")
		}
	}
}
