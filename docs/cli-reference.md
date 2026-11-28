# CLI reference

Run `mobterm-cli --help` for the commands available in your installation.
Use `mobterm-cli help upgrade` or `mobterm-cli hooks install --help` for a
command's options. A compatible App discovers machine operations through the
CLI's advertised capabilities.

| Task | Commands and further reading |
| --- | --- |
| Identify the installed build | `version` prints the version, revision and platform. |
| Inspect configuration and delivery | `status`, `doctor`, `test` and `outbox`. See [usage and diagnosis](usage.md). |
| Manage an Agent integration or binary | `hooks` and `upgrade`. See [installation](install.md). |
| Inspect or recover pairings | `status` and `pairs`. See [pairing and recovery](usage.md). |
| Implement an App command or Agent callback | Use the relevant [protocol contract](README.md#implementing-a-protocol-client) for frames, receipts and exit status. |

## Terminal output

Help wraps text to fit the terminal width.

When stdout is a terminal, `status`, `hooks install`, `hooks uninstall`, `hooks reconcile`,
`test` and `upgrade` show human-readable results. Redirecting stdout or
passing `--json` selects the existing JSON format. For a test notification,
the result distinguishes local queuing, API acceptance and APNs acceptance.
Phone display requires observation on the device.

- Append `--color=auto|always|never` to help or a human command (choose one
  value). In auto mode, nonempty `NO_COLOR` (including `0`) disables color,
  then nonzero `CLICOLOR_FORCE` can force it, then `CLICOLOR=0` disables it.
  Otherwise color follows the output descriptor and terminal environment.
- `--plain` takes precedence over color settings and turns off decorative
  symbols, dynamic updates and hyperlinks. It preserves the selected result
  format. `NO_COLOR` alone preserves layout and live progress.
- Width, UTF-8 locale, color and refresh capability are evaluated separately.
  Monochrome ANSI terminals can still refresh progress. Unrecognized cursor
  capabilities use static stages. Terminals soft-wrap the continuous release
  URL so it can be copied even in a narrow pane. Help adds clickable links on
  recognized direct terminals such as iTerm2, WezTerm and Ghostty.
- Machine protocols, stdin frames, Agent callbacks and the single-line
  `version` probe keep their established formats, including with forced color.

### Upgrade progress

On an interactive run, progress goes to stderr and the installed version and
path go to stdout. `--progress=auto` draws a single line for slower updates.
Known response lengths show actual bytes and a percentage. Unknown lengths show
bytes and elapsed time. CI and redirected stderr use static stages. Select
`--progress=plain` for stage lines or `--progress=never` to suppress them.
`upgrade --json` and redirected stdout suppress progress on both streams.

## Notification API request deadlines

The CLI enforces the following budgets in
[command dispatch](../internal/cli/main.go), the [API transport](../internal/cli/api.go)
and the [activity dispatcher](../internal/cli/activity_dispatch.go).
See the [approval](protocol-v2.md#native-approval-over-ssh),
[interaction](native-interactions.md) and [Doctor](doctor-v1.md) contracts
for their independent lifetimes.

| Command or phase | Maximum budget |
| --- | --- |
| `setup`, `notify`, `test`, full/pair `status`, `outbox flush/refresh`, `pairs reconcile` | 125 seconds total, including retries/readback and persistence |
| One notification API request | 60 seconds across DNS, connection, TLS, headers and body; TLS negotiation is included, not added |
| Agent version probe | 20 seconds |
| `hooks reconcile` | 60 seconds total; parallel candidate probes up to 20 seconds, then up to 8 seconds per Agent for local planning, locking and writing |
| `hooks install`, `status --agents-json` | 25 seconds including probe and cleanup |
| `status --installation-json`, hook removal and other default local commands | 10 seconds |
| `observe` | 2 seconds |
| `activity dispatch` | 12 seconds total; at most 5 seconds for network draining |
| Other `activity` commands | 5 seconds |
| `approval` commands | 125 seconds; native permission request lifetime is at most 120 seconds within the configured 130-second host Hook deadline |
| `interaction` commands | 335 seconds; supported native question/helper lifetimes have a separate 300-second maximum |
| `upgrade` | 2 minutes for download and replacement; combined upgrade additionally allows 10 seconds for readback and capability detection, and 62 seconds for migration and process cleanup |

The API transport verifies certificates, respects proxy rules, supports HTTP/2
and refuses redirects. A shorter parent deadline or cancellation takes precedence.
Each request reserves the final second for persistence, or one fifth of the
remaining time if less than five seconds remain. Installed hooks can impose a
shorter timeout. A binary upgrade leaves those settings in place.

Completion reminders and explicit outbox flushes share the network budget
between capability negotiation and delivery. A retryable completion or capability
failure allows one additional attempt using the original event and ciphertext
where applicable. The randomized wait starts at 15–18.75 seconds, honors
longer `Retry-After` values and requires at least five seconds left after the wait.
Cancellation ends it. Ordinary Codex observations can save activity and launch a
[bounded dispatcher](compatibility.md#codex-approval-and-observation-dispatch).
There is no idle retry scheduler.

The App's generic SSH notification wait is 75 seconds and its outer setup
wait is 90 seconds. Either can end before the CLI's network budget expires.
For phase-level evidence see [troubleshooting](usage.md#troubleshooting).
