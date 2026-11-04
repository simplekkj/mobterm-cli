# Remote interaction wire v1

This contract is separate from approval wire v3 and notification envelopes v1/v2.
The API is a ciphertext relay. Complete requests and responses travel only over
an authenticated, independently owned SSH exec channel. No terminal input is
injected and no daemon, port or session owner is created.

## Commands and budgets

`mobterm-cli interaction inspect|respond|status|release|configure --stdin-frame`
uses one four-byte big-endian length followed by exactly that many UTF-8 JSON
bytes and EOF. Responses are one newline-terminated JSON value. Each direction
is limited to 65,536 bytes; the old approval/control limit stays 16,384 bytes.
`request_json` is limited to 24,576 UTF-8 bytes, `response_json` to 8,192;
final nested encoding must also fit. Duplicate keys at every depth, unknown
frame/field constraints, trailing bytes, invalid UTF-8 and depth above 32 reject
the entire input. There are at most 64 fields, 256 options and eight live waiters.

Inspect/status input: `{v:1,pair_id,request_id}`. Inspection returns
`v,pair_id,request_id,digest,kind,agent,expires_at,policy_generation,
request_revision,state` and, while pending, `request_json,supported_actions`.
Only the deciding pairing receives its own `decision_id,action,response_digest`
and evidence; another original participating pairing sees the necessary state.
An unavailable observation may return complete read-only `request_json`, empty
`supported_actions`, and an `unavailable_reason`. It cannot become actionable.

The immutable request contains agent/version/adapter, session/run/turn/native ID,
runtime and PID/birth identities, locator, created/expiry timestamps, complete
native input, tool/cwd/scope, fields and supported actions. Never recompute its
digest by decoding and re-encoding: SHA-256 covers the exact `request_json`
UTF-8 bytes. Native IDs are separate from randomly generated wire IDs.

Response input adds `digest,kind,action,response_json,response_digest,
decision_id,policy_generation,request_revision,expires_at,proof`.
`response_json` is `{ "answers": { "fieldID": <typed value> } }`, or `{}`
for decisions without an answer. The bytes and decision ID are frozen at the
first explicit submission. A same-ID/different-body request is a conflict.
Release uses action `release` and competes with dispatch under the same lock.

`configure` accepts `{v:1,pair_id,agent,kind,enabled,policy_generation}`. Enabling
requires a fresh remote grant and records local explicit opt-in. Old approvals
cannot authorize a new kind. Disabling prevents dispatch and cancels an existing
decision from that pairing; other original participating pairings retain their
independent permissions and the original pairing can still query its receipt.

## Authentication vector

Derive 32 bytes with HKDF-SHA256, IKM the existing pairing encryption key, salt
the UTF-8 pair ID, info `mobterm/interaction/1`. HMAC-SHA256 covers these ordered
UTF-8 fields, each prefixed with its own unsigned 32-bit big-endian byte length:

1. `mobterm/interaction/1`
2. pair ID
3. request ID
4. request digest (lowercase hex SHA-256)
5. kind
6. action
7. response digest (lowercase hex SHA-256 of exact response bytes)
8. decision ID
9. decimal policy generation
10. decimal request revision
11. decimal expiry Unix seconds

Proof uses standard base64. `testdata/interaction-v1.json` is independently
computed in Python and verified by Go and Swift, including Unicode and commas.
Request and response identities cannot be substituted by current UI selection.

## Typed fields

Kinds: `approval`, `question`, `form`, `elicitation`, `planApproval`,
`followupMessage`. Actions are kind-specific: `allow_once/deny`, `answer/cancel`,
`accept/decline/cancel`, `send_message`; release is separate from native cancel.

Fields: `id,type,label,description,required,options:[{id,label}],custom,
min_length,max_length,minimum,maximum,min_items,max_items,format,length_unit,when`.
Types are `string,number,integer,boolean,multiselect,external`. External requires
explicit `true`; it does not prove OAuth or an external operation completed.
Absent optional values are omitted; false, zero, empty text/array and null are
distinct. Null is unsupported. Numeric input uses no exponent, at most six decimal
places, absolute value at most 9,007,199,254,740,991, and must survive decimal to
native binary64 to decimal roundtrip unchanged. Unsupported precision fails.
Length defaults to Unicode scalar count; `length_unit:utf16` preserves OpenCode's
JavaScript constraints. Formats: email, absolute URI, ISO date, RFC3339 datetime.
Unknown or unsupported native schema constraints reject the whole request.

`when` is an AND list of `{field_id,op:eq|neq,value}` referring only to preceding
fields. Scalar equality compares typed values; a multiselect condition checks
membership. An absent/inactive predecessor makes the condition inactive.
Inactive fields must not be submitted. Both clients and native adapters validate.

## Durable phases and evidence

`pending -> decision_recorded -> dispatching -> response_written`, optionally
`native_confirmed` with specific native consumption evidence. Other outcomes are
`released,expired,native_cancelled,superseded,delivery_unknown,unavailable`.
No phase alone asserts tool execution or Agent continuation. An ordinary Hook
stdout write or HTTP success stops at response_written. Loss of a live waiter
while dispatching is displayed as delivery_unknown.

A single host-wide record/CAS covers all authorized pairings. Dispatching is
persisted **before** returning the response to the held native callback. A crash
after that persistence, whether before or after actual I/O, never permits resend.
Retries return the old receipt. Release cannot reopen dispatching. Native IDs
are deduplicated within session/run/runtime/parent birth; absent native IDs mean
distinct held invocations, never deduplication by identical text.

The private 0700 store/0600 atomic records reuse existing locks and no-follow
reads. Sensitive request/response bytes are removed at settlement or expiry.
Replay tombstones remain for at least 24 hours after lease expiry and until the
owning native parent incarnation is gone. At capacity, new actionable requests
are refused; live tombstones are never evicted. Status authorizes the current
pair remotely but does not require a still-running target process.

Plugin receipt is a fixed stdin-frame command bound to an unguessable waiter
owner token and the original parent PID/birth. It cannot be submitted by App.
Only exact question answer consumption evidence can assert native_confirmed;
OpenCode form cache/events and HTTP 204 do not establish the winning answer.

App retains the original decision ID, action, response digest, request binding and
exact frame in memory. Authentication, connection and stream-open failures leave
it provably unsent: an explicit Continue sends that same frame. Immediately before
stdin send it becomes possibly sent; cancellation, loss of ACK, a pending query
or reopening details never permits a new decision. The App does not retry a
possibly sent frame. Direct and queried receipts use the same full tuple check.
A missing or different tuple shows global handling only. Even a matching
`question_answer_match` proves answer equality, not which device won or execution.

Optional form fields have an explicit Clear Answer action, distinct from entering
empty text/arrays, false or zero. Clearing removes the answer and transient input;
conditions are recomputed and inactive answers remain excluded.

If raw OpenCode input fits but the normalized request or nested response envelope
exceeds its budget, the helper alone publishes a reason-bearing unavailable
observation before creating any actionable record. It has no actions, waiter or
owner token and cannot authorize dispatch. With the current opt-in interaction
grant it retains a needs_input reminder even when general needs_input is off.
Raw-body overflow remains adapter-owned. Arbitrary child failures, including
failures after dispatch, do not trigger a second reminder or native reply.
Claude Elicitation retains parseActivity's prompt-cycle run and occurrence;
parser failures do not fall back to a reconstructed, unbound Activity.


## Cloud policy and compatibility

GET `/v2/pairs/{id}/interaction-capabilities` accepts existing authorized
management or pair send identity. PATCH `/v2/pairs/{id}/interaction-policy` is
management-only, with `{v:1,policy_generation,interactions}`. Matrix keys are
exactly four agents `codex,claude,opencode,pi`, each containing all six kind keys
with booleans. Every value defaults false. Both endpoints return
`{v:1,pair_id,installation_id,enabled,interaction_protocols:[1],policy_generation,
capability_generation,interactions,expires_at}` with a 60-second grant.
The existing global policy generation remains the only authorization clock.

The encrypted reminder adds optional `interaction:{v:1,request_id,digest,kind,
expires_at,unavailable_reason?}`; it never includes full questions or answers.
Each reminder freezes its own hint instead of selecting the run's newest request.
Ciphertext/APNs limits and AAD stay unchanged. Old clients ignore the optional
hint and retain Jump to; new clients against an old API/CLI keep old functionality
and cannot enable new actions. Old policy writes preserve the new matrix but
advance shared generation. Read-only unsupported hints never bypass the ordinary
needs_input notification policy. Revocation/disable invalidates not-yet-delivered
actions; already delivered actions cannot be recalled.

Upgrade design: compatible API migration/code, then App/CLI, then explicit user
opt-in/reload through the native agent. Rollback first disables new permissions,
preserving receipts and tombstones. This document does not authorize deployment.

A status reply may omit request_json even while pending. It cannot be used to
render or sign a fresh answer; the user must explicitly inspect. The local-only
`status --installation-json` declares `interaction_protocols:[1]` so new App
operations can distinguish an old CLI without guessing from a generic error.
Plugins use the fixed owner-only `interaction authorize --stdin-frame` recheck
after their final awaited native GET and before native reply. It cannot allocate
another dispatch, and a later policy change still cannot atomically recall native
I/O. All unavailable reasons are informational and confer no interaction grant.

The legacy OpenCode adapter uses owner-bound `interaction authorize-approval`
with `{v:1,request_id,digest}` after refreshing the native request. It checks the
original parent incarnation, consumed allow-once receipt, fresh legacy grant,
expiry and target without granting another send. New batch denial refreshes the
native permission list before final authorization; an observed list change
requires a new user decision rather than silently widening known scope.
