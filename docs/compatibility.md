# Verified event and terminal compatibility

Checked on 2026-10-13. Installation and notification execution require the Agent
versions below; unknown versions fail safely and `status` reports their actual
version and support state. New versions need schema/semantic verification, not
an optimistic version range.

| Source | Version | Event | Mapping / limitation |
| --- | --- | --- | --- |
| Codex | 0.162.1 | `notify: agent-turn-complete` | `turn_completed`; requires both `thread-id` and `turn-id` |
| Codex | 0.162.1 | `PermissionRequest`, `Stop`, TUI approval alerts | Not forwarded: not proof of a completed turn or unresolved human wait |
| Claude Code | 2.1.282 | `Notification: idle_prompt` | `turn_completed`; native delayed completion after a recorded user prompt |
| Claude Code | 2.1.282 | `Notification: permission_prompt`, `elicitation_dialog` | `needs_input`; native human-wait notifications |
| Claude Code | 2.1.282 | All other Notification/Stop/SubagentStop events | Ignored |
| Herdr | 0.9.1, 0.9.3, protocol 22 | Native Agent hooks inside the terminal | Same event mapping as above, plus explicit snapshot/socket verification |
| tmux | Structured `list-panes` format support | Exact inherited socket + pane | Stable session/window/pane and `pid:start_time:session_created`; linked membership ambiguity rejected |

`turn_completed` means response/turn ended. It says nothing about task success,
tests passing or deployment. No natural-language analysis is used.

Claude's native idle notification normally follows completion by about 60 seconds;
typing or background agents can defer/suppress it. Permission/form notifications
have Claude's native approximately six-second inactivity gate. Mobterm adds no
sleep or detector. This keeps a Stop hook that continues the agent from producing
an early completion. URL elicitation and background agent-view notifications are
not enabled because their target/event ownership has not been established here.

`UserPromptSubmit` stores only a random cycle and timestamp keyed by a session
digest. Native human-wait hooks lack an occurrence ID: each invocation receives a
monotonic per-cycle sequence. This preserves two separate waits even within one
tool, or across parallel tools. HTTP retries reuse the resulting event envelope;
duplicate upstream invocations can duplicate alerts. Claude real interactive
human-wait/idle delivery has not been exercised in this implementation environment;
mapping is covered by narrow lifecycle fixtures and official version-era semantics.

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
