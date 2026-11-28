# Using Mobterm CLI with the App

## Pair a saved connection

[Install the CLI](install.md) on the remote host under the same account that
runs your Agent. In the Mobterm App, open the saved SSH connection's **Agent
Notifications** screen and pair it. The service operator must have configured
a compatible notification API and APNs provider.

The App checks the installation and sends the secret setup packet directly to
the CLI over an authenticated, host-verified SSH connection. Keep the packet
out of command lines, environment variables, URLs, terminals and logs. Each
phone pairs separately. Use the App to revoke a Pair when you want to stop its
access. Removing an Agent integration leaves the Pair in place.

## Activate your Agent

Run these commands as the Agent user. Discovery works before pairing, leaves
Mobterm state untouched and makes no notification API request:

```sh
mobterm-cli status --agents-json
```

Check the detected executable, version and configuration separately. A failed
probe needs investigation even if the executable exists. Choose your Agent in
the App or install its integration on the host. For example, for Codex:

```sh
mobterm-cli hooks install --agent codex --json
mobterm-cli status --json
```

The other Agent names are `claude`, `opencode` and `pi`. Installation configures
an existing Agent. Restart or reload the Agent after installation, and review
Codex native hooks in `/hooks` when prompted. Herdr uses the same Agent hooks as
tmux. For Claude ordinary replies, also load the managed Mod in the interactive
host as described in [native interactions](native-interactions.md). Check the
[compatibility guide](compatibility.md) for the supported event and host modes.

Use the same configuration roots in the SSH session and the running Agent.
Codex and Claude respect `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. OpenCode uses
`cli.json` in `OPENCODE_CONFIG_DIR` or the `opencode` directory under
`XDG_CONFIG_HOME` (default `~/.config`). Pi loads extensions under
`PI_CODING_AGENT_DIR` (default `~/.pi/agent`). Mobterm keeps its own private
state under `~/.config/mobterm` unless you set `MOBTERM_CONFIG_DIR` to a
different private directory.

### Check delivery

For a test directed to one phone, replace `PAIR_ID` with its active Pair ID:

```sh
mobterm-cli status --json --pair PAIR_ID
mobterm-cli test --pair PAIR_ID --json
```

Check the API result, then the APNs acceptance field and the actual notice on
the phone. A test event can be sent over non-PTY SSH and carries no navigation
target. To check Agent activation, complete a turn in the supported Agent
session and check for its notice. To check navigation, open the notice details
and explicitly select the terminal action when available. The CLI's
`health` and `hooks` fields describe files on disk. The device reports its own
notification permissions and display.

## Use a notification

A notice can include an encrypted excerpt of the current assistant response.
On supported sources, the App can retrieve the original response from the
paired host through trusted SSH. Full responses stay out of the cloud
notification. See [security and privacy](security.md) for what the CLI reads,
retains and encrypts. With v2, ordinary state changes do not send a push or
increase unread counts. Abnormal-end reminders are opt-in.

- **Open the terminal.** Navigation requires proof of the current terminal.
  A supported tmux pane may move within the same session. A respawned pane
  has a different occupant. Herdr can locate a unique terminal within the
  same namespace and socket incarnation. Open older tmux notices manually
  if they lack a strong identity. See [target resolution](protocol-v1.md#strong-identity-and-explicit-movement).
- **Answer or approve.** Enable only the actions you need for this Pair and
  Agent. A supported native request and trusted SSH inspection are required.
  Reading a message or opening its terminal grants no permission. Available
  actions and host loading requirements are listed in [native interactions](native-interactions.md).
- **Reply to a completion.** On supported Claude, OpenCode and Pi hosts the
  current source must qualify for a [native reply](reply-v1.md). With a
  verified default Codex shared daemon, a completion can reach the inbox
  without an identified pane. Appending to the original thread separately
  requires reply protocol 2, a verified queue, the cloud follow-up grant and
  explicit local append authorization. Embedded `codex --no-daemon` is an
  optional pane-owned mode without this append capability. See
  [Codex terminal ownership](compatibility.md#codex-terminal-ownership)
  and [conversation append](reply-v2.md).

## Quick Commands and workspaces

Start a Quick Command from the App over trusted SSH. A foreground run works
independently of notification setup. To run asynchronously, use a supported
account shell and compatible App, CLI and service with an eligible notification
registration for that exact paired installation. The one-shot worker captures
bounded output from the command itself. The completion notice and the
[encrypted result](quick-command-result-v1.md) have separate roles.
See [job submission and recovery](quick-command-job-v1.md).

The App can list tmux sessions and Herdr workspaces through the CLI over SSH.
[Workspace discovery](workspace-discovery.md) reads the available structure
without attaching a client or creating a session.

## Troubleshooting

Run `mobterm-cli doctor` in the affected account and environment, even before
pairing. Its default examination is read-only and can contact services and
probe Agent versions. A Doctor-capable App also offers these checks from the
Terminal connection menu over trusted SSH.

```sh
mobterm-cli doctor --offline
mobterm-cli doctor --scope network --pair PAIR_ID
mobterm-cli doctor --scope agent --agent codex --deep
```

Use `--offline` to skip network and Agent version probes. Use `--report` for
a shareable summary and keep `--json` reports local. Interactive
`mobterm-cli doctor --fix` asks you to select and confirm one offered repair
to an existing owned component. Read the result and follow any restart or
trust instructions. After interruption, diagnose again before taking action.
See the [Doctor contract](doctor-v1.md).

### Recover Agent configuration

Read the Agent's `health` entry in `status --json`. For a missing owned hook,
use the App repair action or rerun `hooks install --agent NAME` with the same
configuration root. For a pending operation, follow its `recovery_action`:
continue installation, finish removal, uninstall before reinstalling, or review
a conflict. Keep the private journal. A false `hooks` flag can coexist with an
unfinished operation. Changed or foreign entries need manual review.

- **Codex.** A partial installation can indicate a legacy notify-only setup
  or managed events split between configuration files. Repair with the current
  CLI, restart Codex and review trust. Keep native definitions in the
  [managed representation](compatibility.md#negotiated-state-and-approval-2026-10-31).
  During migration a handler may be temporarily absent. Resume the pending
  operation before manually changing either file. Remove the integration with
  the current CLI before downgrading to one that predates JSON hook ownership.
  For missing completions,
  check the effective root `notify`, `last_terminal_owner_refusal_at` when
  available, and the outbox.
- **Claude.** Legacy ownership records can require manual review. Remove only
  entries you can confirm Mobterm added, then move `hooks-claude.json` from
  the active private Mobterm configuration directory to a private backup.
  Retain installation identity and Pair files. Reinstall the integration to
  record ownership. Leave user hooks and entries of uncertain origin intact.
- **OpenCode and Pi.** Check the effective root, owned assets and running TUI.
  A configured plugin or extension takes effect on reload. Before downgrading
  to a CLI that cannot manage the recorded integration, remove it with the
  current CLI. See [updating existing integrations](install.md#updating-existing-agent-integrations)
  and [uninstall](install.md#uninstall).

### Recover pairing or queued events

After a lost pairing response, reconcile the original credentials with
`mobterm-cli pairs reconcile --pair PAIR_ID --json`. An unknown authentication
result retains the record for review. If you intend to discard a named pending
attempt, use `mobterm-cli pairs abandon --pair PENDING_PAIR_ID --json`.
This discards its local secrets. Revoke an active Pair in the App. After
confirmed cloud cancellation, reconnect over trusted SSH so the App can
finish remote-host cleanup. Older CLIs may need an update. See
[Pair recovery and capacity limits](protocol-v1.md#ssh-diagnostics-and-recovery).

| When | Command |
| --- | --- |
| Inspect the local queue without sending | `mobterm-cli outbox status --pair PAIR_ID --json` |
| Retry pending sends during the current command | `mobterm-cli outbox flush --pair PAIR_ID --json` |
| Look up receipts after handoff to the service | `mobterm-cli outbox refresh --pair PAIR_ID --json` |

Pending events wait for another Agent event or an explicit flush on an idle
host. Retries preserve the event, ciphertext and original expiry. A full
queue reports an error and keeps valid pending events. See
[durable delivery ownership](protocol-v1.md#durable-delivery-ownership) for
retry, retention and handoff rules.

### Diagnose a network failure

Failed notification API requests emit a redacted `mobterm-cli network:` line
on stderr. To include successful requests, enable diagnostics for a deliberate
test or flush:

```sh
MOBTERM_NETWORK_DIAGNOSTICS=1 mobterm-cli test --pair PAIR_ID --json
MOBTERM_NETWORK_DIAGNOSTICS=1 mobterm-cli outbox flush --pair PAIR_ID --json
```

Inspect the reported DNS, TCP, TLS, write, header or body phase and available
timings, connection reuse, HTTP protocol/status and a validated request ID
when available. Pairing reports a network timeout (including a TLS handshake
timeout) as `timeout`; explicit cancellation takes priority.
`first_byte_ms` measures from request start. A completed write alone
cannot confirm API acceptance. `api_unavailable` alone cannot identify a
failed phase. Diagnostics omit endpoints, credentials, payloads and raw
errors. The CLI retains no diagnostic log. See the
[request deadlines](cli-reference.md#notification-api-request-deadlines).

When investigating a specific attempt locally, record its time, the App
build, API origin and the absolute CLI path, version and commit seen by the
App's installation step. Recheck that executable with `version`. Pairing
uses the selected binary without automatically upgrading it.
`status --installation-json` can initialize installation state.
