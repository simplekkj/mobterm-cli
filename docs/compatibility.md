# Agent and terminal compatibility

## Reading scope

The table below records fixed notification regression samples, originally checked
on 2026-10-16 and extended for OpenCode/Pi on 2026-10-20. “Current reference” is a
reproducible sample label, not the newest upstream release or an action allowlist.
For current v2 observation and legacy approval behavior, see
[negotiated state and approval](#negotiated-state-and-approval-2026-10-31); for
new request/answer actions, use the per-action minimum versions and runtime schemas in
[native interactions](native-interactions.md). Codex completion remains native
`notify`; the hooks-only G0 requirement remains blocked as recorded below.

## Notification version policy and fixed samples

Install, notify and status share one local SemVer
policy without a registry query. Versions at or above the
minimum can use the existing adapter, with no mechanical upper limit. Every event
must still satisfy the field, semantic, target and pairing checks. A future version
passing the gate is **not** a claim that its event contract has been tested.

| Agent | Minimum stable | Historical regression | Current reference (2026-10-16) |
| --- | --- | --- | --- |
| Codex | 0.100.0 | 0.162.1 | 0.163.0 |
| Claude Code | 2.0.36 | 2.1.282 | 2.1.301 |
| OpenCode | 2.0.0 | — | 2.1.0 (2026-10-20) |
| Pi coding-agent | 1.1.0 | — | 1.3.0 (2026-10-20) |

The pinned test samples live in `testdata/compatibility-samples.json`. Go tests
check minimum/current against the production descriptor in `compatibility.go` and
this document; CI reads that manifest. Change these together when intentionally
changing the minimum or recording a new validation reference. No CLI release is
needed merely to unlock a higher stable Agent version using the same contract.

Only whole official output forms `codex-cli X.Y.Z`, `X.Y.Z (Claude Code)`,
`opencode vX.Y.Z`, and Pi plain `X.Y.Z` are
recognized. Outer whitespace, an optional `v` immediately before the number and
SemVer build metadata (`+build.7`) are accepted. Numeric components have no leading zeros. Build metadata does
not affect comparison. A prerelease of the minimum sorts below that release;
a prerelease of a higher core version passes the version floor. Invalid forms
such as `1.2.3-01`, empty identifiers, banners, or multiple versions fail.
Probe output is bounded by the
2 MiB command limit and by 100 bytes in status. Each Agent probe has the same
20-second budget, bounded by the calling context, with bounded pipe cleanup.
`hooks install` and `status --agents-json` allow 25 seconds for the probe and
local cleanup; hook removal and installation-identity lookup retain ten seconds.
Status probes the four independent Agents concurrently to retain time for pair
readback within the full status command's 125-second context; deadline failures are reported
as `probe_timeout` without exposing command output.
`status --agents-json` uses the same runtime selection and isolated probes without
initializing installation identity, reading pairs/outbox or calling the notification
API. Each Agent reports `probe_status: ok | not_found | failed | timeout | cancelled`;
only the initial Agent entry lookup can report `not_found`. An existing launcher
with missing Node, failed asdf selection, invalid Pi package selection or permission
failure reports `failed`. Configuration health survives missing/unsupported runtimes.
The legacy `installed` and rejection-code semantics remain compatible; consumers
must use the new typed field to distinguish these outcomes. See the
[discovery contract](protocol-v1.md#read-only-agent-discovery).
This accommodates older JavaScript CLI startup (Claude 2.0.36 measured 1.4–1.5
seconds on macOS Intel). Version probing first fixes the effective launcher and
runtime in the caller's original HOME/cwd/PATH. Generated asdf shims are recognized
by their configured data-directory location and full script shape (asdf 0.20.2
format), not just a command name. Bounded `asdf which` and
`asdf env <command> /usr/bin/printenv PATH` queries resolve the executable and
execution PATH before isolation; they do not run the Agent or its pre-exec hook.
Only PATH is captured, not the full environment. This resolves the standard Node
plugin without selection callbacks. Other plugins, custom list-bin-paths,
exec-path, exec-env callbacks and shim templates are refused before querying asdf,
so arbitrary plugin code cannot run in the real HOME during resolution. Custom
pre-exec behavior and arbitrary wrapper scripts are not verified by this support.
No shell rc, login shell or wrapper eval
is used. See [asdf commands](https://asdf-vm.com/manage/commands.html) and the fixed
[implementation](https://github.com/asdf-vm/asdf/blob/cb72590e9f9691b6e651aecfe4b0c9334adff41b/internal/cli/cli.go).

For official npm `#!/usr/bin/env node` entries, including npm symlinks, the selected
Node is also resolved explicitly, using asdf's execution PATH when the Agent is a
shim. A competing global installation is never a fallback for failed resolution.
Relative PATH directories are fixed before cwd changes. Direct native binaries
keep their ordinary PATH selection. The entire resolution plus single Agent
version execution shares the 20-second deadline and 2 MiB output budget;
selection is local to each child, without changing the parent's environment/cwd,
installing versions or retaining a persistent resolution cache.

The actual Agent uses a disposable HOME, cwd, CODEX_HOME, CLAUDE_CONFIG_DIR and
XDG_CONFIG_HOME, with updates/nonessential traffic disabled. No authentication or
project files are copied into it. Claude 2.0.36's version bootstrap
otherwise migrates `~/.claude.json`; probe isolation prevents that native side
effect on user configuration. No probe state/cache is retained. Install refuses
before configuration or ownership writes. Uninstall never requires a present or compatible Agent.

### Legacy v1 event mapping

This table describes the original v1 mapping only. Later v2 observations and
interaction waiters below add distinct behavior; a PermissionRequest observation
alone still does not prove either completion or an actionable human wait.

| Source | Native event | Mapping / limitation |
| --- | --- | --- |
| Codex >=0.100.0 | `notify: agent-turn-complete` | `turn_completed`; requires stable thread/turn identity and positive root provenance below |
| Codex | `PermissionRequest`, `Stop`, TUI approval alerts | Not forwarded: not proof of final completion or unresolved human wait |
| Claude Code >=2.0.36 | `UserPromptSubmit` then `Notification: idle_prompt` | `turn_completed`; requires a live prompt association and native delayed idle |
| Claude Code >=2.0.36 | `Notification: permission_prompt`, `elicitation_dialog` | `needs_input`; independent of completion associations |
| Claude Code | Other Notification/Stop/SubagentStop events | Ignored |
| Herdr | 0.9.1, 0.9.3, protocol 22 | Same native Agent events plus snapshot/socket verification; terminal gate unchanged |
| tmux | Structured `list-panes` support | Exact socket/server/session/window/pane; linked membership ambiguity rejected |

## Minimum-version evidence

### Codex: resume-safe native notify identity

The field boundary alone is insufficient: 0.46.0 lacks `thread-id`; 0.47.0 has
thread/turn fields but reuses process-local submission counters after restart.
The adjacent rejected version 0.99.0 still initializes `next_id` to zero,
`submit` increments it, resume preserves `conversation_id`, and the native final
notification uses that submission ID. A different turn after resuming the same
thread can therefore collide with a prior cached Mobterm event.

Official commit
[`641d5268fa581bbdff401e5e8e8cc346976b92fd`](https://github.com/openai/codex/commit/641d5268fa581bbdff401e5e8e8cc346976b92fd)
replaces that counter with UUID v7 per live submission. `git tag --contains` with
numeric stable-tag sorting establishes **rust-v0.100.0** as the first containing
stable tag. Tag names and commit IDs are distinct:

- [0.99.0 / `ec9f76ce4f854c7d4f3c78c9b1bacbe128df286e`](https://github.com/openai/codex/blob/ec9f76ce4f854c7d4f3c78c9b1bacbe128df286e/codex-rs/core/src/codex.rs):
  `Codex::spawn/submit`, `Session::new` and `InitialHistory::Resumed` show the old boundary.
- [0.100.0 / `8272f9a71ee7364d8de992418103628f8a17eb6f`](https://github.com/openai/codex/blob/8272f9a71ee7364d8de992418103628f8a17eb6f/codex-rs/core/src/codex.rs):
  `submit` uses a fresh UUID; resumed history preserves the thread, not the ID of
  a new submission. At final sampling (`!needs_follow_up`), `run_turn` dispatches
  `AfterAgent` using that same `turn_context.sub_id`.
- [0.100.0 native serializer/command](https://github.com/openai/codex/blob/8272f9a71ee7364d8de992418103628f8a17eb6f/codex-rs/hooks/src/user_notification.rs)
  maps AfterAgent to `agent-turn-complete` with `thread-id` / `turn-id`, then
  appends exactly one JSON argv to the configured `notify` argv. The configuration
  maps `config.notify` into `HooksConfig.legacy_notify_argv`.
- [0.163.0 / `83086188ae0614bd882818ff1aa23f3194962012`](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/session/turn.rs)
  calls `run_legacy_after_agent_hook` after final sampling/Stop continuation checks.
  [hook_runtime.rs](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/core/src/hook_runtime.rs)
  sends the thread and `turn_context.sub_id` to
  [legacy_notify.rs](https://github.com/openai/codex/blob/83086188ae0614bd882818ff1aa23f3194962012/codex-rs/hooks/src/legacy_notify.rs).
  `SessionIo` in `core/src/session/mod.rs` uses `new_submission_id()` (UUID v7)
  for new input; recovery can preserve an existing turn ID for the same turn.

This is evidence for the actual native notify chain, not an app-server type.
Mobterm keeps IDs opaque; it neither enforces UUID spelling nor fabricates IDs,
reads transcripts, resets caches or adds a time window. Reentry of the same source
thread/turn retains the encrypted envelope; a new turn after resume has a new
source identity. Tests model that boundary; they are not real upstream resume runs.

### Claude Code: versioned package dispatch, not changelog inference

Official npm packages were downloaded with their registry SHA-512 integrity
verified. The full executable source in `package/cli.js` was inspected through
construction, matching, command dispatch, idle scheduling, human-wait UI and
prompt submission. The changelog's 2.0.37 matcher announcement is later than the
actual package boundary. Current documentation alone does not establish old behavior.

| Official package | Tarball SHA-256 | cli.js SHA-256 |
| --- | --- | --- |
| [2.0.35](https://registry.npmjs.org/@anthropic-ai/claude-code/-/claude-code-2.0.35.tgz) | `5936d45befa5d19c9f0f939333dd1efa5288580182609eef0e60c15cdd13b4ec` | `527e5613e9f98ec45b31b93787ba8c3c575beeaba917f3e9e858e262bca7ff24` |
| [2.0.36](https://registry.npmjs.org/@anthropic-ai/claude-code/-/claude-code-2.0.36.tgz) | `42095aacc8e39d8b7d5c0162fb44d873a1cc39430681269bac492c004cfd0e13` | `d92ccfd43b894d8328b94f74011ad4a8a9efd94066650fc5725e1c10322ff869` |

Locations below are zero-based character offsets in that exact 2.0.36 `cli.js`
(UTF-8 decoded); names are minifier-local, not a supported API:

- `ER` (9933189) supplies the current `session_id`; `Mp1` (9948282) constructs
  `hook_event_name: Notification` and copies the caller's `notificationType` to
  `notification_type`. In 2.0.35, `Kp1` (9937104) constructs only message/title:
  it cannot satisfy this adapter and is the adjacent rejected version.
- `cB0` (9939906) selects Notification matchers using the input's
  `notification_type`; `pB0` (9945690) serializes it and calls `dB0` / `V2I`
  (9937142 / 9937330), which spawns the configured command and writes JSON stdin.
  This checks actual control/data flow rather than the mere presence of strings.
- `Ye1` (9948677) adds `UserPromptSubmit` using the same `ER` session identity;
  the prompt flow iterates it before the model query (9089223).
- The idle UI sends `idle_prompt` when no active response/background work requires
  attention and the native inactivity threshold has elapsed (9206611); the default
  threshold is 60000 ms. The permission UI passes `permission_prompt` (8006310)
  and the MCP form UI passes `elicitation_dialog` (8009907) to `qoA` (7694990).
  Its one-shot inactivity polling uses 6000 ms and calls `Tr` → `Mp1`.

An isolated probe evaluated the unmodified extracted `ER`, `Mp1`, `cB0` functions
with controlled session/settings/process boundaries. Only the three configured
notification types reached a child process; its allowlisted JSON had the same
session and Notification event name. This is controlled function evidence,
**not** an authenticated interactive Claude lifecycle observation. Samples retained
only `session_id`, `hook_event_name`, `notification_type`; messages and transcripts
were discarded. The allowlisted contract is:

```json
{"session_id":"controlled-session","hook_event_name":"Notification","notification_type":"permission_prompt"}
```

All three supported types already exist at this minimum; no completion or human
wait is fabricated to emulate a missing native capability. Later optional types,
including URL elicitation and background agent-view notifications, remain outside
the adapter. A future type is ignored, never inferred from message text.

## Verification scope

Official latest stable release pages were rechecked on 2026-10-16:
[Codex 0.163.0](https://github.com/openai/codex/releases/tag/rust-v0.163.0),
[Claude Code 2.1.301](https://github.com/anthropics/claude-code/releases/tag/v2.1.301).
They are pinned references, not a production ceiling. No claim is made that every
intermediate release was executed. Source/package evidence, synthetic encrypted
regressions, real configuration loading, native runtime events and actual phone
delivery are distinct evidence levels; see the appended task record in
[validation.md](validation.md) for commands and actual results.

The local macOS Intel machine terminated both official Codex 0.100.0 npm and
GitHub tar executables with signal 9 before version/config loading. This is a real
failed execution, not a passing configuration test. No system security setting
was changed. Linux endpoint CI is required to establish its real configuration
path; the local failure and final CI result are recorded separately in validation.

## Event semantics and terminal boundaries

`turn_completed` means response/turn ended. It says nothing about task success,
tests passing or deployment. No natural-language analysis is used.

Claude's native idle notification normally follows completion by about 60 seconds;
typing or background agents can defer/suppress it. Permission/form notifications
have Claude's native approximately six-second inactivity gate. Mobterm adds no
sleep or detector. This keeps a Stop hook that continues the agent from producing
an early completion. URL elicitation and background agent-view notifications are
not enabled because their target/event ownership has not been established here.

`UserPromptSubmit` stores only a random cycle and timestamp keyed by a session
digest. Completion associations expire after 86400 seconds; absent or expired
associations suppress `idle_prompt` completion. Native human-wait notifications
remain eligible without an association, including long-running or resumed sessions.
They still require a supported version, valid session identity and an unambiguous
verified terminal target; they neither create nor refresh a completion association.

Native human-wait hooks lack an occurrence ID: each invocation receives a fresh
random identity, scoped by session digest, independent of prompt cycles. This
preserves separate waits even within one tool or across parallel tools without
another persistent occurrence store. The existing encrypted retry cache remains
bounded to 512 envelopes: a full cache rejects new events until expiry permits
cleanup, while cached retries remain usable. The one-hour envelope TTL and
one-day expired-cache retention are unchanged. HTTP retries within that invocation
reuse its exact event ID, nonce and ciphertext. Duplicate upstream invocations can
duplicate alerts; cross-invocation deduplication is not promised for Claude waits.

Claude real interactive human-wait/idle delivery has not been exercised in this
implementation environment. Fixtures cover both wait types with missing, expired
and valid cycles through the actual CLI notify path, encryption and controlled
HTTP retries; Agent-version/terminal command outputs are synthetic in these tests.

Herdr's effective `done`/`blocked` display status alone is not an authoritative
Agent lifecycle signal. In the pinned socket schema `PaneAgentStatusChangedEvent`
lacks an event identity and authority/provenance sufficient for this use. Therefore
Mobterm installs one native source per Agent and no Herdr forwarding plugin. This
also prevents direct-hook plus plugin double reporting. Herdr names/status text,
screen text, OSC/BEL and process quietness are never notification sources.

Herdr continuity is explicit: the current namespace socket's filesystem identity
and the opaque terminal occupant must both match. In both supported versions,
restore creates a new `TerminalId` rather than preserving a pane's prior terminal
identity. A restart, restore, socket recreation or terminal replacement invalidates
the old notification, even if public pane/workspace IDs are reused. Arbitrary
`HERDR_SOCKET_PATH` overrides that do not map to a real saved namespace are rejected.
Simultaneous inherited tmux and Herdr contexts are rejected as ambiguous.

Primary sources:

- [Codex notifications](https://developers.openai.com/codex/config-advanced#notifications)
  and [Hook decisions](https://developers.openai.com/codex/hooks), checked against
  an actual 0.162.1 `codex exec` notify from an isolated local tmux session.
- [Claude Hook reference](https://code.claude.com/docs/en/hooks#notification), with
  local installed version `2.1.282`. The narrow Notification meanings and native
  timing are documented; Stop cannot independently establish final completion.
- Herdr 0.9.1 at [`065ef9d6a531c49fb8bee7e818ef837065b21ee9`](https://github.com/herdrdev/herdr/tree/065ef9d6a531c49fb8bee7e818ef837065b21ee9):
  [TerminalId](https://github.com/herdrdev/herdr/blob/065ef9d6a531c49fb8bee7e818ef837065b21ee9/src/terminal/id.rs),
  [restore allocations](https://github.com/herdrdev/herdr/blob/065ef9d6a531c49fb8bee7e818ef837065b21ee9/src/persist/restore.rs).
- Herdr 0.9.3 at [`7b116c05bfda646af39d2524c54e70c751f57ee8`](https://github.com/herdrdev/herdr/tree/7b116c05bfda646af39d2524c54e70c751f57ee8):
  [TerminalId](https://github.com/herdrdev/herdr/blob/7b116c05bfda646af39d2524c54e70c751f57ee8/src/terminal/id.rs),
  [restore allocations](https://github.com/herdrdev/herdr/blob/7b116c05bfda646af39d2524c54e70c751f57ee8/src/persist/restore.rs),
  [socket contract](https://github.com/herdrdev/herdr/blob/7b116c05bfda646af39d2524c54e70c751f57ee8/docs/next/website/src/content/docs/socket-api.mdx),
  [event schema](https://github.com/herdrdev/herdr/blob/7b116c05bfda646af39d2524c54e70c751f57ee8/src/api/schema/events.rs).

Both pinned Herdr release binaries were exercised against real isolated local
servers: raw `session.snapshot` target verification succeeded and stopped-server
verification failed. This proves the actual socket request/response contract,
not an iPhone/SSH/APNs or controller-control integration.

## Codex terminal ownership

When the Codex TUI supports a shared app server, launch it explicitly in the
intended pane with `codex --no-daemon`. For example, use
`codex --no-daemon resume` when resuming. Use the usual launcher for versions
without the daemon option. Mobterm leaves launch commands and global settings
under the operator's control. On Codex 0.170.0, specifying a profile also
changes daemon selection, so `-p` is a separate configuration case.

Codex's [0.170.0 legacy completion callback](https://github.com/openai/codex/blob/29eb79e9866b3262a777fdf8195c1b9e63684c91/codex-rs/hooks/src/legacy_notify.rs)
carries a thread and turn but no per-client pane identity. Reconnecting another
TUI to a shared server can leave its hooks with the first pane's environment,
even while that pane and its original occupant remain live. The existing
snapshot, socket and occupant checks still run. For Codex, Mobterm also obtains
the pane's shell process from tmux or Herdr, verifies its kernel ancestry, and
compares its process session with the hook host's. For a native command hook,
Codex can [start the managed shell in a new session](https://github.com/openai/codex/blob/29eb79e9866b3262a777fdf8195c1b9e63684c91/codex-rs/hooks/src/engine/command_runner.rs);
only that one launch boundary is crossed. Legacy notify must already belong to
the pane's process session. The actual dispatch host must be a terminal Codex
process. Kernel process ancestry and birth plus a bounded command-role check
also reject a foreground `codex app-server --listen …` serving a different TUI.
Unreadable or ambiguous host evidence suppresses the event. No argument values,
prompts or environment dump are recorded. Herdr process information is bracketed by matching
snapshots on the same socket generation and occupant. No process command line is
saved. A detached host cannot borrow its ancestor TUI's target.

An unconfirmed owner suppresses publication and mobile action registration.
The original notifier still runs. Codex discards notify stderr, so Mobterm
retains one local timestamp, exposed as `last_terminal_owner_refusal_at` in
Codex hook health. It records a previous refusal and has no effect on the
configuration or trust state. Check it alongside `outbox status --json` and
the effective root `notify` when troubleshooting. With shared mode, an
unattributable event is never queued for later delivery to another terminal.
Other agents continue to use their existing verified terminal and source rules.

## OpenCode / Pi TUI adapters (2026-10-20)

OpenCode 2.0.0 (`63f7ceecbed2d7d9a627518d935dd963b9d4ac9f`) and 2.1.0
(`60c68d145a14127ce633e26aa5fdfb882a7baa6d`) provide the formal
[`@opencode/plugin/tui` context](https://github.com/anomalyco/opencode/blob/63f7ceecbed2d7d9a627518d935dd963b9d4ac9f/packages/plugin/src/tui/context.ts).
The global `cli.json` plugin reference loads only its `./tui` export. The generated
package has no server entrypoint or floating dependency. Existing JSONC comments,
trailing commas, plugin ordering and unrelated settings remain intact.

Only `session.execution.succeeded` from the current visible root is completion.
A readable parent chain and the host root/family must agree. Child completion,
unknown/global owners and background roots are ignored. Revision 2 separately
records explicit failed/interrupted lifecycle events; they never become success. `permission.asked` / `form.created` require the exact request to remain
in the official pending list in the same root family and location. Unavailable
pending data is skipped, not inferred from text or a timer. Reply/cancel events,
route changes and lifecycle generations invalidate queued work. No internal tab
is automatically selected. Two actual TUI instances showing the same root may
notify their own terminals; native event IDs and bounded per-TUI memory survive
hot reload without becoming cross-TUI ownership.
Each activation owns a disposable Solid root for its route observer. Cleanup
and partial setup failure release that root and all registered native listeners;
TUI memory identity and deduplication remain available to the next activation.

Optional completion `summary` reads only the current root/run's precise
`session.step.started` → `session.step.ended.assistantMessageID` through
`context.data.session.message.get(sessionID, messageID)`. The returned ID and
assistant type must agree. It never lists or synchronizes session history.
Only bounded `content` text is retained; reasoning/tool content is excluded.
A new step, tool observation, execution, route change or reload invalidates the
candidate. A missing/changed cache or a half-observed step yields status only.
These message contracts were checked against the fixed 2.1.0 client/context above;
completion still comes exclusively from `session.execution.succeeded`.

Pi means the official `@earendil-works/pi-coding-agent` package in
[earendil-works/pi](https://github.com/earendil-works/pi). Minimum 1.1.0
(`abe508e1b89912adde45528136c3221eb69acdd7`) adds explicit settled cancellation;
current reference 1.3.0 (`71023aec1546d6d677a65669c5665c0927ce0dd0`).
The extension uses host-resolved `VERSION`, `ctx.mode === "tui"`,
`sessionManager.getSessionId()`, observed `agent_start` and final `agent_settled`.
Only explicit `aborted:false` completes. Retry/compaction/continuation intermediate
end events never complete. `message_start/end` only manage the current round's
last assistant text candidate; tools, a new assistant/user message, a new round,
and reset clear the old candidate. Nontext-only assistants clear it as well.
Only non-aborted final settled may attach that candidate; it is not copied to an
interrupted notification. Session switch/reload/shutdown invalidates pending
associations; a half-turn after reload produces no historical notification.
Revision 2 observes native `ui_prompt_start/end` intervals as waiting input;
these events have no general native reply handle, so legacy mobile approval is
unavailable for them. The separate opt-in Pi tool gate is described in
[native interactions](native-interactions.md). `hasUI` alone is insufficient because
RPC can have UI. Fork/resume parent history is not treated as a background agent.
A non-aborted settled can follow a final error: the message means a turn ended,
not that the user's task succeeded.

OpenCode/Pi version probes resolve launcher/Node/asdf in the original environment
first, then isolate HOME/cwd/XDG config/data/cache/state and TMPDIR. OpenCode
variables and OTEL overrides are removed in the child; updates are disabled.
Pi gets an isolated `PI_CODING_AGENT_DIR`, offline/version-check flags and no
`PI_MANAGED_INSTALL_ROOT`; a nonempty `PI_PACKAGE_DIR` is resolved against the
original HOME/cwd before isolation. Pi's tilde, relative, absolute and file-URL
forms select the same package, including literal spaces in ordinary paths.
An invalid override fails the probe without trying a different installation. Actual install
paths use the caller environment, never this probe directory. Pi's output must
be plain `X.Y.Z`, OpenCode's `opencode vX.Y.Z`; neither permits an extra `v` or
unrecognized runtime suffix. SemVer build metadata and valid prereleases follow the minimum-version rules above.

The bridge uses a recorded absolute executable, argv and bounded stdin, one child
plus at most eight queued jobs, a ten-second deadline and lifecycle cleanup.
Errors show at most one generic local diagnostic per extension lifetime, with
stdout/stderr suppressed. New-source CLI failures return nonzero to this child
bridge. Intentional route/session cancellation is silent and does not consume
the diagnostic for a subsequent spawn, exit or timeout failure. The
Codex/Claude notifier exit behavior is unchanged. No user model/API
credentials, complete messages or original events enter the adapter payload.
The optional `summary` is a plain-text excerpt limited to 120 Unicode scalars and
512 UTF-8 bytes, with grapheme-safe clipping, control/ANSI/OSC removal and
code/JSON-only fallback. It is produced in memory, sent over bounded stdin and
then protected by the CLI's existing private storage and end-to-end encryption;
no terminal screen or full transcript is collected. A bridge payload that exceeds
4096 bytes first drops this optional summary. Missing native text stays absent.
Generated assets and exact config references have local fingerprint ownership;
interrupted transactions retain a retryable journal and never restore an entire
user config as rollback. Edited/unowned files and symlinks are refused. Uninstall
uses the saved root and remains available without the Agent on PATH.
Summary assets use installation record revision 4 (wire adapter revision remains
2). Upgrade the CLI and its owned assets together using the controlled installer,
then reload/restart according to the host's rules. Old strict CLI decoders may
reject the added key; new CLI accepts old assets without summaries. An installed
fingerprint proves disk contents, not that a running Agent loaded them. Existing
pairing normally remains valid. This implementation does not update real user
Agent configuration or restart active sessions automatically.

## Reliability additions — 2026-10-21

See the normative [v1 reliability additions](protocol-v1.md#reliability-and-strong-target-additions-round-1-v10-2026-10-21).
No Agent version minima, adapter revision 1, upstream event protocol or notifier
exit semantics changed. Disk health now checks each actual entry/condition and
is distinct from loaded/restarted state and test-notification delivery. Safe
repair restores provably missing owned entries/assets; user edits and uncertain
ownership require review, never wholesale config replacement.

| Combination | Behavior |
| --- | --- |
| New CLI + old API | 202 without per-event handoff retains local pending ownership; next event/user flush can retry original envelope. |
| Old CLI + new API | Original v1 envelope may be durably handed off and recovered by Cron. |
| New App + old CLI / old tmux notification | Generic/history/manual connection remains; no automatic strong tmux Jump or precise foreground suppression. |
| Old App + new CLI | Reads top-level v1 fields; original navigation guarantees only, not new respawn protection/movement. |
| New App + old API | Foreground/tap import works; missing mutable-content cannot promise NSE preview/spool execution. |
| Old App + new API | Original generic/test loc-keys remain; added extension flag is ignored. |
| Old CLI cache | legacy_unknown history/deduplication only; no bulk replay, replacement expiry or synthesized occupant. |

New tmux captures require a readable exact OS process birth identity. Linux and
Darwin amd64/arm64 are supported without cgo; process query failure safely rejects
capture/resolve instead of weakening identity. Moves are supported within one
tmux session; Herdr terminal moves are supported within one namespace/socket
incarnation. Strong tmux validation needs App and CLI together.

Pending work remains short-lived/conditionally triggered. The original v1 path
used one immediate POST per hook delivery. Current completion/test/explicit flush
may retry within the [current command budget](../README.md#notification-api-request-deadlines);
short observation paths retain their own limits. Future flushes use persisted,
bounded attempts and exact envelopes. An idle host without another event/user retry will not wake itself.
Once service handoff is known, only bounded GET refreshes are allowed locally.
Seven-day tombstones protect recognizable source identities; Claude human waits
lack upstream occurrence IDs, and arbitrary raw callback replay beyond a retained
identity cannot be claimed permanently exactly once. No fuzzy time-window merging
of independent human waits is introduced.

## Classic Hook recovery permissions

Codex/Claude v1 journals retain the original file mode. Before replacing a
configuration, recovery reads content and permissions from the same no-follow
regular-file handle and rechecks both after ownership persistence. A changed
mode (including 0644 to 0600) is a conflict: configuration, ownership and journal
remain intact for diagnosis. If the configuration already matches the committed
after-state, recovery finishes ownership/journal cleanup without rewriting the
file or changing its current permissions. Read-only health never resumes writes;
OpenCode/Pi recovery and the journal format remain unchanged.


## Negotiated state and approval (2026-10-31)

[Protocol v2](protocol-v2.md) retains the legacy completion gates above while
adding separately validated native behavior. Codex 0.166.0 is the validation
baseline for UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest,
Interrupt and SessionStart/End; it is not asserted to be the first supporting
release. `notify` remains the sole completion source. PreToolUse is pending,
not proof of execution; tool returns are not proof of task success. Owned native
events use the user's existing JSON or inline TOML representation (JSON for new
configurations), preserving unrelated config and notify argv. Native trust and
disabled hooks settings are never changed; health reports review/restart needs.

Codex 0.166.0's
[discovery implementation](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/hooks/src/engine/discovery.rs)
warns only when both sources contain native event groups; `hooks.state` alone is
not an inline event source. Migration strips only entries proven by the private
ownership record, selects the remaining user representation, and journals both
files. User events present in both sources are a conflict requiring user review;
Mobterm does not move them or silently choose which to discard. An existing
Mobterm-only TOML installation stays TOML until another user representation
requires migration. Pending multi-file operations use journal version 2; version
1 single-file journals remain recoverable. Old CLIs reject version 2 journals
and do not understand JSON native ownership; complete recovery/removal with the
new CLI before downgrading.

Claude 2.1.301 is the current fixed schema reference for tool observations and
PermissionRequest. Stop records a cycle boundary without completing the run;
only idle for that still-current stopped cycle completes it. A late prior-cycle
idle after the next prompt is ignored. Generic permission/elicitation callbacks
without request identities are not heuristically merged with different requests.
OpenCode 2.0.0/2.1.0 fixed public client types provide execution lifecycle,
`session.tool.input.started/called/success/failed`, exact pending PermissionRequest
and `context.client.permission.get/reply` (`once`/`reject`). Pi 1.3.0 fixed types
provide toolCallId/isError and UI prompt observation. Neither tool failures nor
Pi non-aborted settlement proves the user's task succeeded.

Adapter revision 1 input remains accepted by the original v1 path. Revision 2
requires explicit run/instance/occurrence metadata and keeps active tool identity.
Host-loaded version, installed assets, restart, native trust, App/API capability
and per-Agent approval opt-in are distinct facts. No test below installs hooks in
real user config or authorizes a model request. See current validation for actual
local fixture and host execution evidence, rather than assuming version probing
proves an event or approval was consumed.

Official sources read this round: [Codex Hooks](https://learn.chatgpt.com/docs/hooks),
[Claude Hooks](https://code.claude.com/docs/en/hooks),
[OpenCode fixed client types](https://github.com/anomalyco/opencode/blob/63f7ceecbed2d7d9a627518d935dd963b9d4ac9f/packages/client/src/promise/generated/types.ts),
[Pi fixed extension types](https://github.com/earendil-works/pi/blob/35dfa757378bd7a8570670e99c2687bdd1039a06/packages/coding-agent/src/core/extensions/types.ts).

OpenCode revision-2 request observations now include optional `request_id` and
forward `permission.replied`, `form.replied`, and `form.cancelled` to the exact
observed session/run. The CLI retains a bounded pending-request set alongside
active tools, removes only the resolved request, and emits no reminder for
resolution. Child-family ownership, location and route/lifecycle generations
remain checked; route switches and cleanup invalidate pending callbacks. Older
revision-2 producers without request IDs remain accepted with their previous
limited observation semantics. Install the matching CLI with the updated adapter;
a downgrade requires restoring its owned adapter as well.

### Interaction wire v1 adapters

New request/answer actions use separate default-off per-Agent/type permissions,
not the legacy mobile approval switch. Runtime minimums and checked sample versions, native
reply contracts, conditional limitations, schema subsets, lifecycle boundaries
and primary source links are recorded in [native interactions](native-interactions.md).
Notifications keep their existing minimum versions. An installed integration is
not proof that a running TUI has loaded it or that a real model consumed an answer.

### Codex P1 approval and observation changes (2026-11-06)

Codex `PermissionRequest` is a pre-decision observation. It now contributes only
ordinary `tool_pending` state until an explicitly enabled MobTerm approval Hook
has validated its input, Pair, live policy/capability and target and durably saved
its actual waiter. Only that waiter creates `waiting_approval` / `needs_input`
with a request-bound hint. This confirms a MobTerm Hook waiting for a response;
it does not prove the native Codex UI is waiting, override another Hook's deny,
or change Codex's own aggregation. Disabled/unsupported observations perform no
synchronous capability lookup and create no actionable/high-priority request.

Approval resolution removes only the matching request and recomputes remaining
waits/tools. Requests without a native ID retain distinct generated identities.
Consumed, released, cancelled, expired and invalid waiters cannot be revived by
late observations. New approval intents/outbox records carry local provenance so
senders can suppress a stale request before sending; an existing ciphertext is
never re-sealed under the same event ID. Already accepted API/APNs notifications
cannot be recalled. Unknown legacy provenance is not grounds to delete history.
The existing default-off authorization, native reply, live decision/consumption
checks, target proof, CAS and 120-second response window remain in force.

Ordinary Codex `PreToolUse` / `PostToolUse` now synchronously validate the target
and durably apply/coalesce state, then request a bounded `activity dispatch`
child. They do not perform capability, snapshot, event or outbox HTTP, including
when admission fails for capacity. Admission failure retains older obligations
and is not a successful enqueue. `request_user_input`,
`request_user_input_async`, PermissionRequest and other interaction paths retain
their dedicated handling; successful async-question PostToolUse registration is
not discarded. Hook JSON/TOML definitions and native synchronous semantics are
unchanged; no blanket `async=true` or tool filtering is introduced.

A configuration root admits one active dispatcher and at most one pending
successor through inherited kernel file locks. A pending child batches arrivals
for 250ms, releases its admission slot only after obtaining the active lock and
before reading durable work, and uses at most five seconds for network draining
within a twelve-second total lifetime. It never launches another child. Crashes
release the locks; durable intents/outbox survive failed launches, exhausted
budgets and network failures for a later Hook or explicit `outbox flush`.
There is no daemon, listener or scheduled future retry. Existing per-Pair
fairness, rate limits, retry deadlines, immutable envelopes, terminal latch and
returned-tool ordering remain authoritative. Existing installation identity is
securely read from its atomic file without queuing behind the run-store writer;
initial creation still locks and reads again before generating an identity.
Contended run-store writers join the kernel lock queue instead of periodically
polling while newer writers reacquire it. The caller's deadline is unchanged;
an expired waiter can only release/close a subsequently acquired lock, never
perform a late state write. No second activity queue is introduced.
This is not an identity/target cache. Target validation is not cached or
weakened. [Measurement evidence](evidence/codex-p1-latency/README.md) separates controlled
fixtures from real terminal/device evidence.

G0 remains blocked: Stop hooks can request continuation of the same turn before
legacy notify runs. This round adds no Stop completion mapping and performs no
P0 configuration migration. `notify` and its old reader/third-party chain remain
in place. Completing these two independent P1 changes does not complete the
hooks-only requirement or authorize a completion-source switch.

## Root attribution and response excerpts — 2026-11-17

The notification version gate above is unchanged; passing it is not proof of
support for every metadata format. This round checks Codex 0.100.0 session metadata
and the fixed 0.169.0 source contract (commit
`d32063f96c2dff1dc96af839b3952abb6acfeda4`). Codex native `notify` remains the sole
completion source; Stop is not promoted and the deferred hooks-only migration is
not resumed. A hidden title thread is a supported explanation of the reported
incident, not a proven identity of the unretained original callback.

Both legacy notify and rich observation gate MobTerm completion before creating a
run, snapshot, reminder or outbox. Explicit child `agent_id` prevents root prompt,
Stop and tool-state contamination; legitimate separately authorized questions and
approvals keep their native delivery path with distinct child identity. Absence
of `agent_id`, a SessionStart reason, a prior run, tool count, cwd, pane, text or
elapsed time is never positive root proof. Existing pre-upgrade runs remain
untrusted. Rich root proof is stored only after source validation and bound to
Agent/session/turn/instance and the captured Locator; ordinary Pre/Post observations
perform no metadata scan and remain local/durable.

Without exact persisted proof, the CLI resolves `CODEX_HOME` (or the normal
`~/.codex` root) and reads only the first canonical `session_meta` record. Supported
layouts are `sessions/YYYY/MM/DD`, legacy `sessions/`, and `archived_sessions/`.
`rollout-TIMESTAMP-THREAD_ID.jsonl` identifies an ordinary candidate. After
a revert, Codex uses `rollout-TIMESTAMP-THREAD_ID_ROLLOUT_ID.jsonl`. The
notification thread must match the thread component and the ID in the first
metadata record. Explicit source `cli`, `vscode`, `exec` or `mcp` with missing or `user`
thread_source and no parent is root evidence. Explicit subagent/internal sources
are non-root; source/parent/thread_source contradictions, absent/unknown sources,
wrong ID, conflicting candidates and malformed/duplicate-key headers are unknown.
A user's `forked_from_id` is history, not proof of a child agent.

The fallback has a 150 ms total scan budget, 4096 directory-entry budget, eight
candidate limit, 64 KiB maximum first record and 256 KiB total header bytes.
Directory and file opens are pinned with `openat`/`O_NOFOLLOW`; symbolic links in
the configured root path or scanned subtree, nonregular candidates and unknown
layouts cannot provide proof. Use a canonical nonsymlink Codex home. Arbitrary
`transcript_path` values are not opened. No bytes after the first record are read;
there is no transcript scan, poll, new listener or fixed wait. Missing metadata,
permissions or exhausted budgets produce the fixed redacted
`codex_completion_origin_unknown` reason, not an infinite retry or a claim that
this was definitely a background thread. A legacy-only legitimate ephemeral
thread with no rollout or trusted lifecycle therefore cannot notify completion.
The original third-party Codex notify argv/JSON/output/exit remains unchanged.

| Agent/path | Completed response and excerpt support | Remaining boundary |
| --- | --- | --- |
| Codex rich and legacy | Proven root; current notify `last-assistant-message` | Unknown/ephemeral/no metadata fails closed; old sender lacks this fix |
| Claude rich | Same root prompt cycle, Stop then idle; Stop `last_assistant_message` candidate | Missing optional field gives status only; continued tools/new prompt invalidate candidate |
| Claude legacy | Existing root prompt cycle then native idle, no artificial short-turn filtering | Original legacy installation has no Stop capture, so status only |
| OpenCode 2.1.0 | Existing visible-root/run/route guards, exact step message cache | Missing cache/text gives status only; official isolated loading is not model execution |
| Pi 1.3.0 (also isolated load 1.5.0) | TUI current round final non-aborted settled and assistant text | Other modes, reload half-round, unknown text or aborted runs do not inherit excerpts |

Claude `agent_type` alone is not a child flag: user `--agent` roots remain valid.
For a Claude child question, Pre and Post identify the same wait by the root
and child sessions, native tool name and invocation ID, parent process identity,
and captured target. A response receipt and a matching native answer are
separate evidence. A tool result resolves only its own wait, including a
question answered locally or a read-only plan. A child Stop can close that
child's outstanding waits. When a callback is missing, normal flushes retire
expired waits and preserve pending delivery. None of these paths sends a
root completion or changes the root Stop excerpt.

When both the recorded prompt and a subsequent event provide native `prompt_id`,
conflicting IDs reject delayed old Stop/idle. Missing optional IDs retain the
existing prompt-cycle and (rich path) Stop gate;
unknown historical event formats cannot be claimed as universally validated.
No evidence in this task establishes that Claude/OpenCode/Pi reproduced the original
Codex incident. Missing safe, request-bound short natural-language titles on waits
continues to show only the authenticated waiting status, without serializing tool
inputs, approval parameters or full native forms.

The cross-version encrypted-summary and controlled asset activation matrix is in
[protocol v1](protocol-v1.md#optional-encrypted-message-summary) and the adapter
section above. Real model rounds, API/APNs delivery, lock-screen behavior and phone
navigation are separate validation layers; this repository's local fixtures and
isolated official-host loading do not prove them.

## Full reply capture and mixed versions

Install the capable CLI before refreshing managed integrations. Codex's existing
root notify supplies its current `last-assistant-message`; preserve chained
notifier arguments, streams and exit status. For Claude, a managed root Stop
hook must be active: legacy notification-only hooks have no full-text source.
Use normal ownership-checked hook installation to enable the supported Stop
layout and reload the native Agent. Stop captures the original prompt cycle,
strong terminal identity and active recipients; the matching idle occurrence
checks them again before publication. Tools, a new prompt or a replacement
terminal cannot publish the old candidate.

Reinstall the OpenCode TUI plugin and Pi extension through the normal managed
hook installer and reload each host after changing the CLI. User-edited assets
retain the existing conflict handling. The OpenCode step must identify the
current root assistant message in its official cache. Pi correlates assistant
message start/end within the same session and round. It requires the native
assistant's normal `stopReason: stop` plus non-aborted settlement. Length-limited,
interrupted and unconfirmed native output supplies at most a summary. Only text parts travel, in original order. A body-only handshake
sets `MOBTERM_NATIVE_COMPLETION=1` on the one notification child. The capable
child writes `mobterm/message-body/1\n` before reading its single stdin packet.
An old or rolled-back executable receives the original packet shape after a
bounded handshake timeout. This per-child capability check tolerates replacement
between notifications. Existing adapters with a new CLI still deliver summaries.
Optional oversized/failed extensions degrade before that single send.
The base activity budget remains 4096 bytes, with up to eight queued events,
one notification process and the existing child deadline. Only completion adds
bounded base64 assistant text or the explicit over-limit marker. The whole
local packet stays within 1 MiB. No change to remote grants, envelope or APNs.
