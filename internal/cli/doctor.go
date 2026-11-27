package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/simplekkj/mobterm-cli/internal/doctor"
	"golang.org/x/term"
)

func runDoctor(args []string, display displayOptions, in io.Reader, out, diagnostic io.Writer) int {
	if slices.Equal(args, []string{"--protocol"}) {
		printJSON(out, map[string]any{"v": 1, "doctor_protocols": []int{1}})
		return 0
	}
	var o doctorOptions
	var machine, fix, frame, report bool
	f, err := flags("doctor", args, func(f *flag.FlagSet) {
		f.StringVar(&o.Scope, "scope", "all", "")
		f.StringVar(&o.Agent, "agent", "", "")
		f.StringVar(&o.Pair, "pair", "", "")
		f.BoolVar(&o.Offline, "offline", false, "")
		f.BoolVar(&o.Deep, "deep", false, "")
		f.BoolVar(&machine, "json", false, "")
		f.BoolVar(&fix, "fix", false, "")
		f.BoolVar(&frame, "stdin-frame", false, "")
		f.BoolVar(&report, "report", false, "")
	})
	if err != nil || f.NArg() != 0 || !slices.Contains([]string{"all", "core", "storage", "network", "pair", "agent", "workspace", "delivery", "interaction", "job"}, o.Scope) ||
		o.Agent != "" && !interactionAgent(o.Agent) || o.Pair != "" && !opaqueID.MatchString(o.Pair) ||
		machine && report || frame && (!fix || f.NFlag() != 2) || fix && (machine || report || o.Offline) {
		fmt.Fprintln(diagnostic, "mobterm-cli: invalid Doctor request. See mobterm-cli doctor --help.")
		return 1
	}
	caps := detectTerminal(out, display)
	if fix && !frame {
		input, ok := in.(*os.File)
		if !ok || !caps.TTY || !term.IsTerminal(int(input.Fd())) {
			fmt.Fprintln(diagnostic, "mobterm-cli: select and confirm a repair in an interactive terminal")
			return 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	budget := 30 * time.Second
	if o.Deep {
		budget = 120 * time.Second
	}
	run, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	s, storeErr := defaultStore()
	exe, _ := os.Executable()
	if frame {
		var req doctorRepairRequest
		if err := readStrictFrame(run, in, &req, 4096); err != nil || !req.valid() {
			if errors.Is(ctx.Err(), context.Canceled) {
				return 130
			}
			fmt.Fprintln(diagnostic, "mobterm-cli: invalid Doctor repair frame")
			return 1
		}
		receipt := doctorRepair(run, s, exe, req)
		printJSON(out, receipt)
		if errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		return doctorRepairExit(receipt)
	}
	var checks []doctor.Check
	if storeErr != nil {
		checks = []doctor.Check{
			{ID: "core.environment", Scope: "host", Run: doctorEnvironment},
			{ID: "storage.directory", Scope: "host", Run: func(context.Context) doctor.Result {
				return doctor.Result{Status: "fail", Reason: "store_path_unavailable", Evidence: "process"}
			}},
		}
		if o.Scope != "all" && o.Scope != "core" && o.Scope != "storage" {
			checks = append(checks, doctor.Check{ID: o.Scope + ".availability", Scope: "host", Depends: []string{doctor.Key("storage.directory", "host")}, Run: func(context.Context) doctor.Result {
				return doctor.Result{Status: "unknown", Reason: "store_path_unavailable", Evidence: "process"}
			}})
		}
	} else {
		checks = append(doctorProviders(s, newAPI(), o), doctorAgentChecks(s, exe, o)...)
	}
	target, _ := doctorTarget(s, exe)
	r, err := doctor.Run(run, checks, o.Scope, target)
	if err != nil {
		fmt.Fprintln(diagnostic, "mobterm-cli: invalid Doctor checks")
		return 1
	}
	if storeErr != nil {
		r.Complete = false
		r.Overall = "fail"
	}
	switch {
	case report:
		printJSON(out, doctorShare(r))
	case machine:
		raw, err := json.Marshal(r)
		if err != nil || len(raw)+1 > doctor.MaxReportBytes {
			fmt.Fprintln(diagnostic, "mobterm-cli: Doctor response exceeds limit")
			return 1
		}
		if _, err = out.Write(append(raw, '\n')); err != nil {
			return 1
		}
	default:
		showDoctor(humanResult{out, caps}, r)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	if fix && len(r.Actions) > 0 {
		return selectDoctorRepair(ctx, s, exe, in, humanResult{out, caps}, r)
	}
	if !r.Complete || r.Overall != "pass" {
		return 2
	}
	return 0
}

// Only counts indexed by fixed protocol vocabulary cross the share boundary. Scope,
// target, plan tokens, result details, raw identifiers and paths stay local.
func doctorShare(r doctor.Report) map[string]any {
	counts := map[string]int{}
	causes := map[string]int{}
	for _, c := range r.Checks {
		counts[c.Status]++
		switch c.Reason {
		case "dns_failed", "tcp_failed", "tls_failed", "proxy_failed",
			"request_timed_out", "health_http_failure", "health_receipt_invalid", "private_directory_required",
			"installation_identity_invalid", "installation_mismatch", "pair_auth_unconfirmed", "pair_revoked",
			"pair_expired", "pair_reconcile_needed", "ownership_conflict", "pending_conflict",
			"worker_identity_unconfirmed", "outbox_transaction_pending", "outbox_expired_pending", "delivery_failure_recorded",
			"interaction_result_unknown", "check_timeout":
			causes[c.Reason]++
		}
	}
	features := map[string]int{}
	for _, f := range r.Features {
		features[f.Status]++
	}
	return map[string]any{"v": 1, "overall": r.Overall, "complete": r.Complete, "checks_by_status": counts, "features_by_status": features, "causes": causes}
}

func showDoctor(h humanResult, r doctor.Report) {
	h.line("title", "Mobterm Doctor · "+r.Overall)
	if !r.Complete {
		h.line("warning", "Partial report. Some checks could not finish.")
	}
	for _, c := range r.Checks {
		if c.Status == "fail" || c.Status == "warn" {
			label := c.ID + " [" + c.Scope + "]: " + strings.ReplaceAll(c.Reason, "_", " ")
			if len(c.Features) > 0 {
				label += ". Affects " + strings.Join(c.Features, ", ")
			}
			h.line("warning", label)
		}
	}
	last := ""
	for _, c := range r.Checks {
		group, _, _ := strings.Cut(c.ID, ".")
		if group != last {
			h.line("title", group)
			last = group
		}
		h.line("", fmt.Sprintf("  [%s] %s (%s): %s", c.Status, c.ID, c.Scope, strings.ReplaceAll(c.Reason, "_", " ")))
		if len(c.BlockedBy) > 0 {
			h.line("", "    Waiting on "+strings.Join(c.BlockedBy, ", "))
		}
		var fields []string
		for k, v := range c.Counts {
			fields = append(fields, fmt.Sprintf("%s=%d", k, v))
		}
		for k, v := range c.Details {
			fields = append(fields, k+"="+terminalText(v))
		}
		sort.Strings(fields)
		if len(fields) > 0 {
			h.line("", "    "+strings.Join(fields, ", "))
		}
		if next := doctorNextStep(c); next != "" {
			h.line("hint", "    "+next)
		}
	}
	if len(r.Features) > 0 {
		h.line("title", "Feature evidence")
		for _, f := range r.Features {
			h.line("", fmt.Sprintf("  [%s] %s (%s)", strings.ReplaceAll(f.Status, "_", " "), f.ID, f.Scope))
		}
	}
	if len(r.Actions) > 0 {
		h.line("hint", "Run mobterm-cli doctor --fix to review and confirm one owned integration update.")
	}
}

func doctorNextStep(c doctor.Finding) string {
	switch c.Reason {
	case "pair_reconcile_needed", "pair_pending":
		return "Review the selected pairing in Mobterm, then reconcile that pair ID if its state needs to be synchronized."
	case "pair_revoked", "pair_expired", "installation_mismatch":
		return "Review this server and installation in Mobterm. Pair again explicitly if access is still wanted."
	case "outbox_pending", "expired_pending", "transaction_pending", "outbox_transaction_pending":
		return "Inspect outbox status and the original event's expiry and receipt. Retry only an eligible pending delivery."
	case "worker_identity_unconfirmed", "job_result_unavailable":
		return "Inspect the original Quick Command job and its stored result. The worker's outcome is unconfirmed."
	case "interaction_result_unknown":
		return "Check the original Agent interaction on the server before taking another action."
	case "select_pair_for_remaining":
		return "Run Doctor with --pair PAIR_ID to inspect a specific remaining pair."
	case "ownership_conflict", "pending_conflict":
		return "Review the existing Agent settings and Mobterm ownership record before changing either."
	case "native_review_required":
		return "Review the installed hooks and trust choices in Codex /hooks."
	case "unsupported":
		return "Check the Agent compatibility requirements for this integration."
	case "version_unconfirmed":
		return "Run an online Doctor check to verify the Agent version."
	case "private_directory_required", "store_path_unavailable":
		return "Check your effective HOME, MOBTERM_CONFIG_DIR, owner and private permissions."
	}
	if c.Status == "warn" || c.Status == "fail" {
		group, _, _ := strings.Cut(c.ID, ".")
		switch group {
		case "network":
			return "Check this server's DNS, proxy, TLS trust and connectivity to its configured API."
		case "pair":
			return "Review this specific pairing in Mobterm's notification settings."
		case "delivery":
			return "Review the saved delivery and its receipt with mobterm-cli outbox status."
		case "job":
			return "Inspect the original Quick Command job and its result before taking further action."
		case "interaction":
			return "Review the affected Agent's grants and active binding before submitting a new interaction."
		}
	}
	return ""
}

func selectDoctorRepair(ctx context.Context, s Store, exe string, in io.Reader, h humanResult, r doctor.Report) int {
	for i, a := range r.Actions {
		h.line("", fmt.Sprintf("%d. Update Mobterm-owned configuration for %s", i+1, a.Scope))
	}
	h.line("hint", "Choose one number or press Return to cancel.")
	reader := bufio.NewReaderSize(in, 256)
	read := func() (string, bool) {
		type answer struct {
			value string
			ok    bool
		}
		done := make(chan answer, 1)
		go func() {
			b, prefix, err := reader.ReadLine()
			done <- answer{strings.TrimSpace(string(b)), !prefix && err == nil}
		}()
		select {
		case <-ctx.Done():
			return "", false
		case a := <-done:
			return a.value, a.ok
		}
	}
	choice, ok := read()
	if !ok || choice == "" {
		return 130
	}
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 || index > len(r.Actions) {
		h.line("error", "Invalid action selection.")
		return 1
	}
	a := r.Actions[index-1]
	h.line("warning", "Update only the verified, Mobterm-owned "+a.Scope+" configuration. You may need to restart the Agent or review its trust settings. Type yes to apply.")
	confirm, ok := read()
	if !ok || confirm != "yes" {
		return 130
	}
	run, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	receipt := doctorRepair(run, s, exe, doctorRepairRequest{V: 1, ID: a.ID, Scope: a.Scope, Target: r.Target, Plan: a.Plan})
	h.line("", "Repair "+receipt.Scope+": "+strings.ReplaceAll(receipt.Status, "_", " "))
	switch receipt.Status {
	case "manual_required":
		h.line("hint", "Verified current on disk. Restart the Agent and review its native trust settings.")
	case "stale":
		h.line("hint", "The plan changed or expired. Run Doctor again.")
	case "unknown", "applied":
		h.line("hint", "Run Doctor again to inspect remaining changes before any further repair.")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	return doctorRepairExit(receipt)
}

func doctorRepairExit(r doctorRepairReceipt) int {
	switch r.Status {
	case "manual_required", "verified", "applied":
		return 0
	}
	return 2
}
