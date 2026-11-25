package main

import (
	"fmt"
	"io"
	"strings"
)

// Human output selects displayable fields from the same receipts as JSON.
// An Agent probe, a hook configuration and a delivery receipt each describe
// a different observation.
type humanResult struct {
	out  io.Writer
	caps terminalCaps
}

func (r humanResult) line(role, text string) {
	fmt.Fprintln(r.out, r.caps.style(role, wrapCells(text, r.caps.Width)))
}

func writeHumanResult(w io.Writer, caps terminalCaps, args []string, value any) {
	object, ok := value.(map[string]any)
	if !ok || object == nil || len(args) == 0 {
		return
	}
	r := humanResult{w, caps}
	switch args[0] {
	case "status":
		r.status(object)
	case "hooks":
		r.hooks(object)
	case "test":
		r.line("title", "Test notification")
		if list, multiple := object["results"].([]map[string]any); multiple {
			if len(list) == 0 {
				r.line("warning", "No delivery receipt.")
			}
			for _, item := range list {
				r.delivery(item)
			}
		} else {
			r.delivery(object)
		}
	}
}

func (r humanResult) status(result map[string]any) {
	r.line("title", "Pairing")
	if pairs, ok := result["pairs"].([]map[string]any); ok {
		if len(pairs) == 0 {
			r.line("warning", "No saved pairs. Pair this host in MobTerm.")
		}
		for _, pair := range pairs {
			r.pair(pair)
		}
	} else if _, specific := result["pair_id"]; specific {
		r.pair(result)
	} else {
		r.line("warning", "Pairing status unavailable.")
	}

	if health, ok := result["health"].(map[string]agentHealth); ok {
		fmt.Fprintln(r.out)
		r.line("title", "Agent integrations")
		for _, name := range []string{"codex", "claude", "opencode", "pi"} {
			if h, found := health[name]; found {
				role := ""
				config := strings.ReplaceAll(h.ConfigurationState, "_", " ")
				status := name + "  Hooks on disk: " + config
				switch h.State {
				case "not_installed":
					status += " (Agent not found)"
				case "unsupported":
					status += " (unsupported Agent version)"
				case "unknown":
					status += " (Agent status unknown)"
				}
				if h.State != "complete" && h.State != "not_installed" && h.State != "not_configured" {
					role = "warning"
				}
				r.line(role, status)
			}
		}
	}
	if box, ok := result["outbox"].(map[string]any); ok {
		fmt.Fprintln(r.out)
		r.line("title", "Notification outbox")
		if _, failed := box["error"]; failed {
			r.line("warning", "Outbox status unavailable.")
		} else {
			r.line("", fmt.Sprintf("%d pending, %d handed off, %d terminated", box["pending"], box["handed_off"], box["terminated"]))
		}
	}
}

func pairLabel(result map[string]any) string {
	if id, ok := result["pair_id"].(string); ok && opaqueID.MatchString(id) {
		return "Pair " + id
	}
	return "Pair"
}

func (r humanResult) pair(pair map[string]any) {
	label := pairLabel(pair)
	role, summary := "warning", "API status unconfirmed"
	switch pair["status"] {
	case "active":
		if pair["failure"] == nil {
			role, summary = "success", "active at API"
		}
	case "unpaired":
		summary = "no local pairing"
	case "revoked":
		summary = "revoked at API"
	case "expired":
		summary = "expired at API"
	case "api_unavailable":
		summary = "API unavailable"
	}
	if local, ok := pair["local_status"].(string); ok && local != "" {
		summary += " (local: " + strings.ReplaceAll(safeCode(local), "_", " ") + ")"
	}
	r.line(role, label+"  "+summary)
	if pair["action"] == "reconcile_or_abandon_pending" || pair["action"] == "reconcile_credentials" {
		r.line("hint", "hint: Check the saved pairing with mobterm-cli pairs reconcile --help.")
	}
}

func (r humanResult) hooks(result map[string]any) {
	name, _ := result["agent"].(string)
	if _, known := agentCompatibilities[name]; !known {
		name = "Agent"
	}
	if result["ok"] != true {
		r.line("error", name+" hook configuration did not complete.")
		return
	}
	if result["action"] == "uninstall" {
		r.line("success", name+" hook configuration removed.")
		return
	}
	r.line("success", name+" hooks configured on disk.")
	switch result["activation"] {
	case "native_review_required":
		r.line("hint", "hint: Restart Codex and review the native hooks in /hooks.")
	case "restart_required":
		r.line("hint", "hint: Restart "+name+" to load the hooks.")
	}
}

func (r humanResult) delivery(item map[string]any) {
	role, status := "warning", "No confirmed delivery state."
	switch item["state"] {
	case "accepted":
		role, status = "success", "Accepted by API. APNs acceptance unconfirmed."
	case "apns_accepted":
		role, status = "success", "Accepted by APNs. Check the phone for display."
	case "pending":
		status = "Pending in the local outbox."
	case "not_saved":
		role, status = "error", "Could not save to the local outbox."
	case "permanent_failed":
		role, status = "error", "Notification failed permanently."
	case "revoked":
		role, status = "error", "Pairing revoked."
	case "disabled":
		status = "Notifications disabled."
	}
	r.line(role, pairLabel(item)+"  "+status)
	if code, ok := item["error"].(string); ok && code != "" {
		r.line("warning", "Reason: "+strings.ReplaceAll(safeCode(code), "_", " "))
	}
}
