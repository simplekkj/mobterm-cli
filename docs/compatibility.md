# Agent and terminal compatibility

Checked on 2026-10-16. Install, notify and status share one local stable-version
policy; they do not query a registry at runtime. Stable versions at or above the
minimum can use the existing adapter, with no mechanical upper limit. Every event
must still satisfy the field, semantic, target and pairing checks. A future version
passing the gate is **not** a claim that its event contract has been tested.

| Agent | Minimum stable | Historical regression | Current reference (2026-10-16) |
| --- | --- | --- | --- |
| Codex | 0.100.0 | 0.162.1 | 0.163.0 |
| Claude Code | 2.0.36 | 2.1.282 | 2.1.301 |

The pinned test samples live in `testdata/compatibility-samples.json`. Go tests
check minimum/current against the production descriptor in `compatibility.go` and
this document; CI reads that manifest. Change these together when intentionally
changing the minimum or recording a new validation reference. No CLI release is
needed merely to unlock a higher stable Agent version using the same contract.

Only whole official output forms `codex-cli X.Y.Z` and `X.Y.Z (Claude Code)` are
recognized. Outer whitespace, an optional `v` immediately before the number and
SemVer build metadata (`+build.7`) are accepted. Components are unsigned 64-bit
integers with no leading zeros. Build metadata has nonempty ASCII alphanumeric /
hyphen dot-separated identifiers and does not affect comparison. Prereleases
(alpha/beta/rc/nightly/dev or any `-suffix`), overflow, partial versions, banners,
multiple versions and unknown forms fail safely. Probe output is bounded by the
2 MiB command limit and by 100 bytes in status. Each Agent probe has the same
five-second budget, bounded by the calling context, with bounded pipe cleanup.
Status probes the two independent Agents concurrently to retain time for pair
readback within the command's ten-second context; deadline failures are reported
as `probe_timeout` without exposing command output.
This accommodates older JavaScript CLI startup (Claude 2.0.36 measured 1.4–1.5
seconds on macOS Intel). Version probing resolves the normal PATH executable but
uses a disposable HOME, cwd, CODEX_HOME, CLAUDE_CONFIG_DIR and XDG_CONFIG_HOME,
with updates/nonessential traffic disabled. Claude 2.0.36's version bootstrap
otherwise migrates `~/.claude.json`; probe isolation prevents that native side
effect on user configuration. No probe state/cache is retained. Install refuses
before configuration or ownership writes. Uninstall never requires a present or compatible Agent.

| Source | Native event | Mapping / limitation |
| --- | --- | --- |
| Codex >=0.100.0 stable | `notify: agent-turn-complete` | `turn_completed`; requires opaque `thread-id` and `turn-id` |
| Codex | `PermissionRequest`, `Stop`, TUI approval alerts | Not forwarded: not proof of final completion or unresolved human wait |
| Claude Code >=2.0.36 stable | `UserPromptSubmit` then `Notification: idle_prompt` | `turn_completed`; requires a live prompt association and native delayed idle |
| Claude Code >=2.0.36 stable | `Notification: permission_prompt`, `elicitation_dialog` | `needs_input`; independent of completion associations |
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
