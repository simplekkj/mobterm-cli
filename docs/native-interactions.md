# Native interaction adapters (interaction wire v1)

Each action negotiates its native schema and checks its running host. Versions
use SemVer precedence against the following minimum releases. Build metadata
does not affect eligibility. A newer major or prerelease still needs the actual
action API, host mode and source binding.

| Agent | Structured interactions | Ordinary reply to the current source |
| --- | --- | --- |
| Codex | 0.166.0 | Terminal fallback in shared and profile/embedded mode. A loaded thread has no continuous originating TUI incarnation. |
| Claude Code | 2.1.316 classic Hooks | 2.1.326, current interactive Mod and verified completed source |
| OpenCode V2 | 2.2.5 TUI plugin | 2.2.5, current root session, execution and location |
| Pi | 1.4.0 extension-owned gate | 1.5.1, idle extension in the verified session and settled run |

For an ordinary message follow [reply wire v1](reply-v1.md). A real host event
registers one source. After its initial helper exits, an authorized inspection
can wake the current host to create a fresh short lease. A user-restored session can acquire a new unclaimed waiter after the adapter proves the same native storage, session and completed turn. Claude uses one Mod timer,
OpenCode watches the private store during plugin setup/cleanup, and Pi watches
from session start to shutdown. The source notification and completion
remain the existing event, independently of body-cache success.

`hooks install --agent claude` prepares the classic Hooks and, on a compatible
version, a fingerprint-owned plugin under the MobTerm state directory at
`native/claude-reply`. Load this directory using Claude's own plugin mechanism
(e.g. `claude --plugin-dir /absolute/path/to/native/claude-reply` in a new
interactive terminal). Installation leaves active sessions and organization
settings alone. `health.claude.reply_adapter=installed_load_unverified` describes
the files. Only a live source-bound opportunity proves that the Mod loaded for
this message. It captures the classic prompt ID at main turn start, requires
the same native turn at completion, and calls `$.prompt.submit({text,asUser:true})`.
After `/clear` or an in-process resume, the Mod cancels the old opportunity.
The next root turn in a different session can open its own opportunity after
the Mod verifies the terminal surface and matches the session to its hook environment.
Classic Question and Elicitation check the executable image of the running Claude ancestor. A PATH upgrade alone cannot enable its native schema. A drop is a native rejection. The Mod never repeats a dropped submission.

OpenCode specifies a stable input ID and `delivery:"queue"` on the original
host's `session.prompt`. Its matching admission result and
`session.inbox.delivered` event carry distinct receipt states. Pi uses
`pi.sendUserMessage(text,{expandPromptTemplates:false})`; the API has no
caller-supplied correlation ID, so returning from it leaves delivery unknown.
Pi marks the current run ineligible as soon as it observes new input, even
before a reply helper exists.
That run can still emit its completion notification. The next real `agent_start`
begins a new run with its own eligibility.
All three paths invalidate their captured source on input, native requests
or owner change. Native final checks and submission have a race with other
inputs on hosts that lack an expected-source precondition.

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
  nor repairs a daemon endpoint. It keeps that same socket, verifies the negotiated
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
- OpenCode approval uses the running server's version and process identity from
  `client.server.info()`. The TUI version still gates the plugin. The managed
  host registration verifies the server's relationship to the local TUI and
  open database. On stable servers 2.2.5 through 2.3.0, reject affects all
  pending permissions in the session. On 2.3.1 and 2.3.2 it affects the
  session's pending requests with the same `source.messageID`; a request
  without `source` affects every other pending request without a source in
  that session. Missing process proof, unknown server semantics and malformed
  sources leave the original permission available locally. Independently
  supported allow-once and release remain available.
- Before waiting and again before dispatch the plugin reads the original
  permission and takes the applicable batch from the authoritative list. A
  changed original request, target set, server, route, user input or grant
  cancels the held decision. Other steps can continue independently. The
  native API receives one `decision: "reject"` and can also reject concurrent
  arrivals within the same scope at execution time. Legacy approval v3
  continues to offer only allow-once. See [request compatibility](interaction-v1.md#opencode-approval-request-contents).
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
- OpenCode reject implementations for [2.3.0](https://github.com/anomalyco/opencode/blob/a6b15d33daf6ab7d35569f6d711fdb630b9f04e7/packages/core/src/permission.ts), [2.3.1](https://github.com/anomalyco/opencode/blob/c769de83007e3584b5b395176c9fcde2d3231e88/packages/core/src/permission.ts) and [2.3.2](https://github.com/anomalyco/opencode/blob/c0ad18b402eb2827fc1ee71c171b1177b3fc02d1/packages/core/src/permission.ts), with the [permission source](https://github.com/anomalyco/opencode/blob/c0ad18b402eb2827fc1ee71c171b1177b3fc02d1/packages/schema/src/permission.ts) and [server identity](https://github.com/anomalyco/opencode/blob/c0ad18b402eb2827fc1ee71c171b1177b3fc02d1/packages/protocol/src/groups/server.ts).
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
