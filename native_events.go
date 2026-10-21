package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

// This is a local adapter protocol, never a public notification envelope.
// Runtime version comes from the loading host, not another executable on PATH.
type nativeEvent struct {
	AdapterRevision int    `json:"adapter_revision"`
	RuntimeVersion  string `json:"runtime_version"`
	Event           string `json:"event"`
	SessionID       string `json:"session_id"`
	EventID         string `json:"event_id"`
	InstanceID      string `json:"instance_id"`
	Aborted         *bool  `json:"aborted,omitempty"`
}

func parseNativeEvent(source string, payload []byte) (string, string, error) {
	invalid := errors.New("invalid native adapter metadata")
	if len(payload) > 4096 {
		return "", "", invalid
	}
	var p nativeEvent
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || p.AdapterRevision != 1 {
		return "", "", invalid
	}
	policy, ok := agentCompatibilities[source]
	if !ok || strings.HasPrefix(p.RuntimeVersion, "v") || strings.TrimSpace(p.RuntimeVersion) != p.RuntimeVersion || policy.evaluate(policy.prefix+p.RuntimeVersion+policy.suffix) != "" {
		return "", "", invalid
	}
	for _, value := range []string{p.SessionID, p.EventID, p.InstanceID} {
		if len(value) == 0 || len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", "", invalid
		}
	}
	kind := ""
	switch source {
	case "opencode":
		if p.Aborted != nil {
			return "", "", invalid
		}
		switch p.Event {
		case "session.execution.succeeded":
			kind = "turn_completed"
		case "permission.asked", "form.created":
			kind = "needs_input"
		default:
			return "", "", nil
		}
	case "pi":
		if p.Event != "agent_settled" {
			return "", "", nil
		}
		if p.Aborted == nil {
			return "", "", invalid
		}
		if *p.Aborted {
			return "", "", nil
		}
		kind = "turn_completed"
	}
	return kind, digest(source + "\x00" + p.Event + "\x00" + p.SessionID + "\x00" + p.EventID + "\x00" + p.InstanceID), nil
}

func nativeTargetIdentity(identity string, locator Locator) string {
	// Movement is location metadata, not a new upstream occurrence.
	locator.ViewID = ""
	if locator.Backend == "herdr" {
		locator.WorkspaceID = ""
		locator.PaneID = ""
	}
	target, _ := json.Marshal(locator)
	return digest(identity + "\x00" + string(target))
}
