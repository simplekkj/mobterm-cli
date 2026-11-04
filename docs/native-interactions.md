# Native interaction adapters (interaction wire v1)

Checked against official release sources on 2026-11-03 (Asia/Shanghai). New
interactive actions use an explicit version allowlist; old notification minimums
are unchanged. Official package registry dist-tags were checked separately from
cached release listings: Codex 0.166.0, Claude Code 2.1.317, OpenCode V2 2.2.5,
Pi 1.5.0. Pi 1.4.0 is also supported. Claude 2.1.316 and 2.1.317 share the checked
classic Hook contract. An unknown newer runtime does not inherit authorization.

## Original TUI paths

- Codex/Claude permission Hooks retain approval v3. Codex supports the canonical
  `mcp__server__tool` name and the complete original argument object, as well as
  Bash/apply_patch. Permission sets and special connection verification are not
  flattened into allow-once. The native Hook has no tool-use ID for Codex
  permission requests; the live held invocation and process birth bind it.
- Codex 0.166.0 request_user_input_async offers a subsequent new user message.
  Only an accepted PostToolUse result and its final complete questions create the
  follow-up opportunity. A default-off, finite helper waits at most 300 seconds;
  the original Hook waits only for bounded owner/thread discovery. It reads the
  existing official CODEX_HOME default Unix rendezvous, verifies its exact alias,
  private directory/socket modes and UID, and verifies kernel peer PID, UID,
  process birth and ancestry while the original Hook is alive. It neither creates
  nor repairs a daemon endpoint. It keeps that same socket, verifies the pinned
  server version, and uses loaded/list plus metadata-only thread/read to require
  one live direct-input-capable thread matching session_id, optional agent_id and
  exact transcript_path. It never reads the transcript file or resumes a thread.
  Unavailable, ambiguous, paginated or unsupported discovery falls back to the
  terminal. Before queue/add it rechecks the captured thread, owner, target,
  deadline and local/remote policy. A durable dispatch claim permits only one
  queue/add; missing or invalid acknowledgment is delivery_unknown, with no
  reconnect, retry or inferred failure from queue/list. clientUserMessageId is
  correlation only. A matching queue receipt proves response_written, not native
  consumption or which concurrent message won. Synchronous questions remain
  unavailable. Custom socket locations are not guessed.
- Claude's single managed PreToolUse dispatcher handles AskUserQuestion and
  observes other tools. It preserves the entire original input and replaces only
  `answers`, keyed by original question text. Duplicate question text is
  unavailable. Typed multi-select is mapped to the documented classic Hook comma
  string only for comma-free option labels; arbitrary multi-select Other and
  labels containing commas are unavailable. SDK array examples do not establish
  the classic TUI consumption contract. Single-choice Other text is passed as the
  user's actual string. Elicitation form uses action/content; URL mode returns to
  native authentication. Direct Elicitation answers have no consumer ACK.
- Claude Question and Elicitation have a 300-second local maximum and a managed
  Hook timeout of 330 seconds. Installation migrates only owned commands, preserves
  user-added commands, and keeps ownership checks. Already-running Agent sessions
  must load the configuration through their own reload/restart procedure.
- OpenCode 2.2.5 uses the existing TUI plugin client. Session/Form ID, location,
  immutable schema and pending state are checked before a single reply. Field
  types include string, number, integer, boolean, multiselect and external steps;
  optional/missing, false, zero and empty values remain distinct. `when` conditions
  are ANDed and multiselect conditions test membership. Native string limits use
  UTF-16 units. Pattern/unknown constraints and hidden input requirements fall
  back instead of being discarded; displayed defaults are never auto-submitted.
  External fields require an explicit true acknowledgment, not an OAuth success
  claim. Expiring the mobile lease never invokes form.cancel.
- OpenCode `permission.reply` uses `decision: once/reject`. Reject explicitly
  covers all permissions still pending in the same session at execution time;
  the plugin fetches the authoritative native pending list before waiting and
  again immediately before reject, refusing changed known scope. Both new and
  legacy paths revalidate current authorization before the native reply. The native
  interface cannot atomically reject a supplied list, so an arrival during the
  final native call remains part of the disclosed scope. Legacy approval v3 now
  exposes only allow_once for OpenCode; batch deny requires the new interaction
  scope. Old strict clients safely fall back to their terminal. Disabled new
  interaction approval retains the existing allow-once authority, not a second
  parallel waiter.
- Pi adds its own tool_call gate only after explicit local and remote opt-in.
  Built-in bash/read/write/edit/grep/find/ls expose their complete input and cwd.
  Allow returns undefined so later extension gates still decide. Denial, invalid
  identity, failure and timeout block the current enabled invocation. A release
  uses this extension's own bounded confirm and the captured AbortSignal;
  confirmations are serialized, still consuming the original deadline. Session
  shutdown/reload/fork invalidates captured epochs and settles waiters independently
  of child-close delivery. Native nested IDs may contain slashes. The gate cannot
  prevent other trusted extensions from changing event.input after it returns.

## Evidence and unsupported contracts

The following are deliberately unavailable, with original terminal fallback:

- Codex synchronous request_user_input, including isBlocking=false: the observed
  PreToolUse event is not a pending-response handle. `thread/resume` has a cold
  recovery branch, and the public protocol has no atomic attach-only operation.
- Claude whole-question `response` and ExitPlanMode: classic docs describe answer
  replacement and expose the full plan, but do not establish this adapter's exact
  response/permission-mode combination. Plan text is shown read-only from the
  original tool_input; arbitrary planFilePath is not dereferenced. The SDK's
  indefinitely pending callback and -p defer semantics are not ordinary TUI Hook
  capabilities. No extra -p process, inbox reverse-engineering or Mod is used.
- OpenCode global MCP form has no original session owner and is not reassigned to
  the selected session. Pi third-party UI events lack the resolver, full fields
  and native request identity. No global ctx.ui patch or terminal input injection
  is used. RPC and App Server owner modes remain separate architecture choices.

Hook stdout/HTTP success records delivery only. Claude same-call PostToolUse
answers can confirm the answer digest. OpenCode built-in question confirmation
requires the exact session, assistant message and tool ID and actual
metadata.answers from session.tool.success. form.replied/GET cache are not a
consumer ACK; concurrent native replies can publish a value different from the
Deferred value. Equal actual answers prove the value, not which device won.

## Primary sources

- [Codex 0.166.0 release](https://github.com/openai/codex/releases/tag/rust-v0.166.0)
  and [official Hook reference](https://developers.openai.com/codex/hooks).
- [Codex permission payload](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/core/src/tools/approvals.rs),
  [Hook schema](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/hooks/src/schema.rs),
  [queue endpoint](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/app-server/src/request_processors/thread_queue_processor.rs),
  [socket safety](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/app-server-transport/src/transport/unix_socket.rs),
  [async tool result](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/core/src/tools/handlers/request_user_input_async.rs),
  [thread metadata](https://github.com/openai/codex/blob/6ff1e1ca3b30fc8b914720681fcf5fe62e95f856/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs).
- [Claude classic Hooks](https://code.claude.com/docs/en/hooks),
  [SDK input contract](https://code.claude.com/docs/en/agent-sdk/user-input),
  [2.1.317 delta](https://github.com/anthropics/claude-code/releases/tag/v2.1.317).
  The core engine is closed; these are public contract checks, not a claim of
  complete engine source review or a successful model-driven TUI answer.
- [OpenCode Form schema](https://github.com/anomalyco/opencode/blob/9bbb8ce18c80f099e0e448775f59d965ead40a21/packages/schema/src/form.ts),
  [validation/Deferred lifecycle](https://github.com/anomalyco/opencode/blob/9bbb8ce18c80f099e0e448775f59d965ead40a21/packages/core/src/form.ts),
  [client decision and reply](https://github.com/anomalyco/opencode/blob/9bbb8ce18c80f099e0e448775f59d965ead40a21/packages/client/src/promise/generated/client.ts),
  [Question consumer](https://github.com/anomalyco/opencode/blob/9bbb8ce18c80f099e0e448775f59d965ead40a21/packages/core/src/tool/plugin/question.ts),
  [exact result event](https://github.com/anomalyco/opencode/blob/9bbb8ce18c80f099e0e448775f59d965ead40a21/packages/core/src/session/runner/publish-llm-event.ts).
- [Pi 1.5.0 release](https://github.com/earendil-works/pi/releases/tag/v1.5.0),
  [extension types](https://github.com/earendil-works/pi/blob/v1.5.0/packages/coding-agent/src/core/extensions/types.ts),
  [extension runner](https://github.com/earendil-works/pi/blob/v1.5.0/packages/coding-agent/src/core/extensions/runner.ts),
  [agent loop](https://github.com/earendil-works/pi/blob/v1.5.0/packages/agent/src/agent-loop.ts).
  The runner and agent-loop files were byte-identical between 1.4.0 and 1.5.0;
  types and AgentSession deltas did not change this gate's callback, signal or
  session-shutdown contract.

## Local verification scope

The controlled Node harness exercises form false/zero/custom/multiselect values,
precise question consumer receipts, lifecycle cancellation, Pi disabled/enabled
and explicit local release, OpenCode legacy revocation and authoritative batch
scope changes. It also reproduces the shared Go/Swift request/response digests,
UTF-8 length-prefixed signature input and HKDF/HMAC proof from
`testdata/interaction-v1.json`. These are E1 contracts, not a real model session.

Controlled Go Unix socket tests cover Codex peer credentials, guarded alias and
permissions, final async payloads, unique thread mapping and rejection, and one
queue mutation with matching, missing or mismatched receipts. These are E1
protocol tests; no actual Codex daemon, model turn or phone interaction was used.

Official OpenCode 2.2.5 and Pi 1.5.0 were installed in a disposable directory and
started with isolated configuration and no model request. OpenCode loaded the
plugin through its official setup/restart lifecycle; Pi discovered/imported the
extension and loaded its nine listeners in the real TUI. This is E2 loading
proof only. No E3 real-model question/approval or E4 phone/network/background
interaction is claimed. One initial OpenCode probe timed out during parallel
host load and passed unchanged on retry. The existing user configuration and
sessions were not used.
