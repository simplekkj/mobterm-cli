package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

type helpTopic struct {
	name, summary string
	// Each line is an alternative invocation. Flags in the usage select the
	// matching parameter descriptions, including flags shared between commands.
	usage, example string
	human          bool
	children       []helpTopic
}

type helpGroup struct {
	title  string
	topics []helpTopic
}

func framedHelp(name, summary string) helpTopic {
	return helpTopic{name: name, summary: summary, usage: "--stdin-frame"}
}

func replyHelp(name, summary string) helpTopic {
	return helpTopic{name: name, summary: summary, usage: "--stdin-frame [--json]"}
}

var helpGroups = []helpGroup{
	{"Common commands", []helpTopic{
		{name: "doctor", summary: "Diagnose this installation and review scoped repairs.", usage: "[--scope MODULE] [--agent NAME] [--pair ID] [--offline] [--deep] [--json | --report]\n[--scope MODULE] [--agent NAME] --fix\n--fix --stdin-frame\n--protocol", human: true, example: "doctor --scope network"},
		{name: "status", summary: "Show pairing and Agent integration status.", usage: "[--pair ID] [--json]\n--installation-json [--read-only]\n--agents-json", human: true, example: "status --agents-json"},
		{name: "hooks", summary: "Manage Agent integrations.", children: []helpTopic{
			{name: "install", summary: "Write the hook configuration for one Agent.", usage: "--agent NAME [--json]", human: true, example: "hooks install --agent codex"},
			{name: "reconcile", summary: "Check or update existing owned Agent integrations.", usage: "[--check] [--json]", human: true, example: "hooks reconcile --check"},
			{name: "uninstall", summary: "Remove Mobterm's hook configuration for one Agent.", usage: "--agent NAME [--json]", human: true, example: "hooks uninstall --agent codex"},
		}},
		{name: "test", summary: "Send a test notification to active pairs.", usage: "[--pair ID] [--json]", human: true, example: "test --pair PAIR_ID"},
		{name: "upgrade", summary: "Install a specific stable CLI release.", usage: "--version TAG [--with-integrations] [--json] [--progress=MODE]", human: true, example: "upgrade --version v1.2.3"},
		{name: "version", summary: "Print the version, commit and platform.", example: "version"},
		{name: "help", summary: "Show help for a command or subcommand.", usage: "[command [subcommand]]", example: "help hooks install"},
	}},
	{"Maintenance", []helpTopic{
		{name: "outbox", summary: "Inspect or retry queued notifications.", children: []helpTopic{
			{name: "status", summary: "Show the local delivery queue.", usage: "[--pair ID] [--json]", example: "outbox status --json"},
			{name: "flush", summary: "Retry pending deliveries.", usage: "[--pair ID] [--json]", example: "outbox flush --pair PAIR_ID"},
			{name: "refresh", summary: "Refresh delivery receipts.", usage: "[--pair ID] [--json]", example: "outbox refresh --pair PAIR_ID"},
		}},
		{name: "pairs", summary: "Reconcile or clean up pairing records.", children: []helpTopic{
			{name: "reconcile", summary: "Check saved pairs against the service.", usage: "[--pair ID] [--json]", example: "pairs reconcile --pair PAIR_ID"},
			{name: "abandon", summary: "Remove a pending pairing attempt from this host.", usage: "--pair ID [--json]", example: "pairs abandon --pair PENDING_PAIR_ID"},
			{name: "retire", summary: "Remove the pair identified by a trusted App frame.", usage: "--stdin-frame --json"},
		}},
	}},
	{"App protocol", []helpTopic{
		{name: "capabilities", summary: "Report supported protocols and operations.", usage: "--json", example: "capabilities --json"},
		framedHelp("setup", "Pair this installation using the App's setup frame."),
		{name: "job", summary: "Manage Quick Command jobs.", children: []helpTopic{
			framedHelp("submit", "Submit a background job."),
			framedHelp("status", "Read a job's state."),
			framedHelp("output", "Read a job's result."),
			framedHelp("cancel", "Cancel a background job."),
			framedHelp("foreground", "Run a foreground job over SSH."),
			{name: "worker", summary: "Internal worker started by the CLI."},
		}},
		{name: "discovery", summary: "Discover tmux and Herdr workspaces.", children: []helpTopic{
			framedHelp("tmux.discovery", "Discover tmux sessions, windows and panes."),
			framedHelp("herdr.list", "List Herdr workspaces."),
			framedHelp("herdr.probe", "Probe a Herdr session."),
			framedHelp("herdr.snapshot", "Read a Herdr workspace snapshot."),
		}},
		{name: "message", summary: "Read a stored notification body.", children: []helpTopic{
			framedHelp("read", "Read the message selected by the request frame."),
		}},
		{name: "target", summary: "Check or resolve a navigation target.", children: []helpTopic{
			framedHelp("verify", "Verify a target's identity."),
			{name: "resolve", summary: "Resolve a navigation target.", usage: "--stdin-frame [--read-only]"},
		}},
		{name: "reply", summary: "Inspect and submit replies.", children: []helpTopic{
			replyHelp("inspect", "Inspect a reply opportunity."),
			replyHelp("send", "Submit a reply."),
			replyHelp("status", "Check reply state."),
			replyHelp("latest", "Find the latest reply opportunity."),
			replyHelp("configure", "Update reply settings."),
			replyHelp("configuration", "Read reply settings."),
			replyHelp("navigation-observe", "Record a navigation observation."),
			replyHelp("navigation-confirm", "Confirm a navigation binding."),
			replyHelp("host", "Register a native reply host."),
			replyHelp("rebind", "Rebind a reply to a native host."),
			replyHelp("reopen", "Reopen a reply opportunity."),
			replyHelp("authorize", "Authorize a native reply."),
			replyHelp("receipt", "Record a native reply receipt."),
			{name: "wait", summary: "Wait in an installed Agent hook.", usage: "--source NAME"},
		}},
		{name: "approval", summary: "Manage native approval requests.", children: []helpTopic{
			framedHelp("configure", "Save approval settings."),
			framedHelp("inspect", "Inspect an approval request."),
			framedHelp("decide", "Submit an approval decision."),
			framedHelp("release", "Release an approval request."),
			{name: "wait", summary: "Wait in an installed Agent hook.", usage: "--source NAME"},
		}},
		{name: "interaction", summary: "Manage native interactions.", children: []helpTopic{
			framedHelp("configure", "Save interaction settings."),
			framedHelp("inspect", "Inspect a pending interaction."),
			framedHelp("respond", "Submit an interaction response."),
			framedHelp("status", "Read the interaction state."),
			framedHelp("release", "Release the interaction."),
			framedHelp("authorize", "Authorize a native action."),
			framedHelp("authorize-approval", "Authorize a native approval."),
			framedHelp("receipt", "Record a native interaction receipt."),
			{name: "wait", summary: "Wait in an installed Agent hook.", usage: "--source NAME"},
			{name: "codex-followup", summary: "Handle an installed Codex follow-up hook."},
		}},
	}},
	{"Agent callbacks (automatic)", []helpTopic{
		{name: "notify", summary: "Report an Agent completion event.", usage: "--source NAME [--mobterm-chain] [payload]"},
		{name: "observe", summary: "Observe an Agent hook event.", usage: "--source NAME"},
		{name: "activity", summary: "Dispatch saved Agent activity.", children: []helpTopic{
			{name: "flush", summary: "Flush saved activity."},
			{name: "dispatch", summary: "Dispatch pending activity from the CLI worker."},
		}},
	}},
}

type helpRow struct{ label, description string }

var helpOptions = []helpRow{
	{"--scope MODULE", "Select all, core, storage, network, pair, agent, workspace, delivery, interaction or job."},
	{"--deep", "Include additional bounded, read-only runtime evidence."},
	{"--offline", "Inspect locally without network requests or running Agent version probes."},
	{"--report", "Write a shareable report containing only redacted status counts."},
	{"--fix", "Choose one repair and confirm it in a terminal, or supply a selected App frame."},
	{"--protocol", "Read the Doctor protocol version without accessing configuration."},
	{"--agent NAME", "Select codex, claude, opencode or pi."},
	{"--version TAG", "Select a stable release tag in vX.Y.Z form."},
	{"--pair ID", "Select the pair by its ID."},
	{"--json", "Write the machine-readable result."},
	{"--installation-json", "Print installation identity and protocol versions."},
	{"--agents-json", "Print local Agent discovery results."},
	{"--check", "Show possible integration updates without writing."},
	{"--with-integrations", "Verify the new CLI, then update existing owned integrations."},
	{"--read-only", "Use existing state without initializing it."},
	{"--stdin-frame", "Read the request as a length-prefixed frame from standard input."},
	{"--source NAME", "Identify the Agent that invoked the hook."},
	{"--mobterm-chain", "Run the saved Codex notifier after handling the event."},
	{"--progress=MODE", "Use auto, plain (stage lines) or never."},
}

var helpDisplay = []helpRow{
	{"--color=MODE", "Use auto, always or never."},
	{"--plain", "Use plain text for human output."},
}

type helpLayout struct {
	out  io.Writer
	caps terminalCaps
}

// Wrap unstyled text before adding indentation or styling each display line.
func (h helpLayout) line(indent int, role, text string) {
	indent = min(indent, max(0, h.caps.Width-1))
	prefix := strings.Repeat(" ", indent)
	for _, line := range strings.Split(wrapCells(text, max(1, h.caps.Width-indent)), "\n") {
		if role != "" {
			line = h.caps.style(role, line)
		}
		fmt.Fprintln(h.out, prefix+line)
	}
}

func (h helpLayout) heading(title string) {
	fmt.Fprintln(h.out)
	h.line(0, "title", title)
}

func (h helpLayout) rows(title string, rows []helpRow) {
	if len(rows) == 0 {
		return
	}
	h.heading(title)
	longest := 0
	for _, row := range rows {
		longest = max(longest, cellWidth(row.label))
	}
	column := longest + 4
	for _, row := range rows {
		if h.caps.Width < max(52, column+24) {
			h.line(2, "", row.label)
			h.line(4, "", row.description)
			continue
		}
		prefix := "  " + row.label + strings.Repeat(" ", longest-cellWidth(row.label)+2)
		for _, line := range strings.Split(wrapCells(row.description, h.caps.Width-column), "\n") {
			fmt.Fprintln(h.out, prefix+line)
			prefix = strings.Repeat(" ", column)
		}
	}
}

func (h helpLayout) commands(title string, topics []helpTopic) {
	rows := make([]helpRow, 0, len(topics))
	for _, t := range topics {
		rows = append(rows, helpRow{t.name, t.summary})
	}
	h.rows(title, rows)
}

func findHelp(topics []helpTopic, path []string) *helpTopic {
	for i := range topics {
		t := &topics[i]
		if t.name == path[0] {
			if len(path) == 1 {
				return t
			}
			return findHelp(t.children, path[1:])
		}
	}
	return nil
}

func renderHelp(out io.Writer, caps terminalCaps, path []string) bool {
	h := helpLayout{out, caps}
	if len(path) == 0 {
		h.line(0, "title", "Mobterm CLI")
		h.line(0, "", "Connect terminal Agents to Mobterm.")
		h.heading("Usage")
		h.line(2, "", "mobterm-cli <command> [options]")
		h.commands(helpGroups[0].title, helpGroups[0].topics)
		h.heading("Examples")
		h.line(2, "", "mobterm-cli status")
		h.line(2, "", "mobterm-cli help hooks install")
		h.line(2, "", "mobterm-cli <command> --help")
		for _, group := range helpGroups[1:] {
			h.commands(group.title, group.topics)
		}
		h.rows("Help options", append([]helpRow{{"-h, --help", "Show help for the selected command."}}, helpDisplay...))
		return true
	}
	for _, group := range helpGroups {
		if t := findHelp(group.topics, path); t != nil {
			h.topic(t, strings.Join(path, " "))
			return true
		}
	}
	return false
}

func (h helpLayout) topic(t *helpTopic, path string) {
	command := "mobterm-cli " + path
	h.line(0, "title", command)
	h.line(0, "", t.summary)
	h.heading("Usage")
	if len(t.children) > 0 {
		h.line(2, "", command+" <subcommand> [options]")
		h.commands("Subcommands", t.children)
		h.heading("Example")
		h.line(2, "", command+" "+t.children[0].name+" --help")
		return
	}
	for _, variant := range strings.Split(t.usage, "\n") {
		h.line(2, "", strings.TrimSpace(command+" "+variant))
	}
	parameters := strings.Fields(strings.NewReplacer("[", "", "]", "").Replace(t.usage))
	options := []helpRow{}
	for _, option := range helpOptions {
		if slices.Contains(parameters, strings.Fields(option.label)[0]) {
			options = append(options, option)
		}
	}
	h.rows("Options", options)
	if t.human || path == "help" {
		h.rows("Display", helpDisplay)
	}
	if t.example != "" {
		h.heading("Example")
		h.line(2, "", "mobterm-cli "+t.example)
	} else if slices.Contains(parameters, "--stdin-frame") {
		h.heading("Input")
		h.line(2, "", "Provide the request frame over the App's SSH channel.")
	}
	if path == "upgrade" {
		h.heading("Release tags")
		url := "https://github.com/" + releaseRepo + "/releases"
		// Soft wrapping preserves the address when copied from a narrow pane.
		fmt.Fprintln(h.out, "  "+h.caps.link(url))
	}
}
