# Notification protocol v2

Round 1 v1.0, 2026-10-31. This contract adds encrypted run state, recoverable
reminders and an opt-in native approval bridge. [Protocol v1](protocol-v1.md)
and its golden vectors are unchanged. Native target stdin frames still contain
only the v1 locator whitelist. The API cannot decrypt or execute approvals.

## Negotiation and identities

The management bearer explicitly enables the device with
`PUT /v2/devices/:device/capabilities` and
`{v:2,enabled:true,capability_generation:<expected>}`. The generation advances on
each successful change. Before constructing a new envelope, the Pair sender reads
`GET /v2/pairs/:pair/capabilities`: `v`, exact `pair_id`, `installation_id`,
`enabled`, `states`, `inbox`, `capability_generation`, `policy_generation`,
`expires_at` (60-second receipt), `policy:{turn_completed,needs_input,abnormal_end}`
and `approvals:{codex,claude,opencode,pi:false}`. Missing/malformed receipts or
network failure are unavailable, not permission to downgrade. An explicit old-API
404 or a complete disabled receipt permits v1 selection before admission. API
admission/delivery checks the current D1 capability generation again.

`PATCH /v2/pairs/:pair/policy` is management-only and requires all policy and
approval fields plus the expected `policy_generation`. Abnormal end reminders
and mobile approval default off. Enabling one Agent's approval does not enable
generic needs-input reminders or another Agent. Since Agent identity is encrypted,
the API permits a v2 needs-input envelope when generic needs-input or any Agent's
approval is enabled; the trusted CLI enforces the exact per-Agent distinction.
Ordinary waits still obey generic needs-input. A disallowed reminder becomes a
state snapshot before encryption. v1 needs-input remains governed only by its
original policy.

A run has an explicit native turn identity when available. Claude uses a durable
session-scoped random prompt cycle; a new prompt invalidates the prior cycle's
Stop/idle association. OpenCode/Pi adapter instances and run identities are scoped
to the loaded TUI, not claimed to survive process restart. Per-Pair stream IDs are
cryptographically random and stored only locally. Locator identity includes
installation, backend, namespace/incarnation, workspace and strong occupant.

Envelope event IDs are stable SHA-256 hex of `source_occurrence + NUL + pair_id`.
Codex completion uses the same notify-derived occurrence as v1. Native sources
include source/event/session/event ID/instance and the existing normalized strong
target identity. Retrying an accepted/unknown v2 delivery never creates a second
v1 format. Stored envelope ID, nonce, ciphertext, kind and expiry are immutable;
incompatible generations fail closed. Deliberate downgrade requires disabling
capabilities and draining/terminating in-flight v2 deliveries first.

## Envelope and authenticated plaintext

The exact outer fields are:

```json
{"v":2,"pair_id":"opaque","event_id":"opaque","kind":"needs_input",
 "stream_id":"opaque","revision":1,"capability_generation":1,
 "expires_at":2000000000,"nonce":"standard-base64-12-bytes",
 "ciphertext":"standard-base64-ciphertext-and-tag"}
```

IDs use `[A-Za-z0-9_-]{16,128}`. Revision and capability generation are positive
integers below JavaScript's maximum safe integer. Expiry is Unix seconds, after
admission and at most 24 hours ahead; CLI normally uses one hour. Kind is `state`,
`turn_completed`, `needs_input`, `failed`, `interrupted` or `test`. State uses
`PUT /v2/pairs/:pair/snapshots/:stream`; reminders use
`POST /v2/pairs/:pair/events`. Nonce is 12 fresh random bytes; AES-256-GCM uses the
existing Pair key and appends its 16-byte tag. Ciphertext including tag is at most
2400 bytes; this alone is not an APNs-size guarantee. Both the whole wire payload
and Foundation's conservative slash-escaped reserialization must fit 4096 bytes.
Reject oversize data; never truncate ciphertext.

AAD is the following UTF-8 sequence, LF-separated, **without** a trailing LF:

```text
mobterm/2
DOMAIN
PAIR_ID
EVENT_ID
KIND
STREAM_ID
REVISION
CAPABILITY_GENERATION
EXPIRES_AT
```

DOMAIN is `snapshot` for `state`, otherwise `reminder`. Numbers are base-ten
integers. All indexing/conditional-write metadata is authenticated. Independent
purposes get independent nonces. A reminder itself supplies the recoverable
latest snapshot, so the CLI does not send another snapshot at that revision.

Plaintext is `{v:2,locator,agent,session_id,run_id,stream_id,revision,state,
occurred_at,tools,active_tool_count,tools_truncated,approval?}`. `locator` is the
existing strong locator. Agent is codex/claude/opencode/pi. State is starting,
running, tool_pending, tool_running, waiting_input, waiting_approval,
turn_completed, failed or interrupted. `occurred_at` is Unix seconds. Session,
run and tool identities are bounded to 256 bytes by adapters. `tools` has at most
32 entries `{invocation_id,category,status}`; category is shell/file/search/web/
mcp/other, status pending/running/succeeded/failed/interrupted/returned. A local
active set prevents one completed tool clearing other invocations. Completed
summaries are evicted as needed, and late tool-start reports cannot resurrect a
finished invocation. Wire summaries may omit rows to fit the size budget:
`active_tool_count` remains the true observed active count, and `tools_truncated`
indicates omitted rows, not lost terminal authority. Unknown/over-capacity
observations do not fabricate completion. Freshness is independent of state.

Optional `approval` contains only `request_id`, SHA-256 `digest` and `expires_at`.
It is stripped for Pair audiences lacking current approval eligibility. Full
commands, parameters, paths, reasons, prompts and transcripts never enter Worker
or APNs through this hint. `test` never contributes business unread.

The local run store atomically persists state plus bounded per-Pair sending
intents before networking. State intents coalesce; low-frequency reminders have
separate identities and use the existing durable outbox/lease mechanism after
format selection. At most 128 retained runs and 512 unencoded intents are kept;
file writes retain the existing 2 MiB/private-directory/atomic-write guard.
State requests obey a separate local throttle and server Retry-After. A later
event or explicit `activity flush`/unfiltered `outbox flush` resumes pending work.
No idle daemon, listening port or automatic future wakeup is created.

## Inbox, ACK, legacy migration and badge

Management-only device endpoints are `GET /v2/devices/:device/snapshots`,
`GET /v2/devices/:device/inbox?cursor=0&limit=50`, exact
`GET /v2/devices/:device/inbox/:pair/:event`, and exact snapshot
`GET /v2/devices/:device/snapshots/:pair/:stream`. Snapshot storage is 24 hours,
128 runs/device. Inbox storage is independent of delivery outbox: seven days,
500 ciphertexts/device, at most 4 MiB envelope JSON. Pruning prefers read entries,
then oldest unread; it updates count/revision transactionally. APNs failure does
not read a retained reminder. Reading beyond delivery expiry does not extend
navigation or approval authority.

An inbox page carries a fixed `watermark`, `next_cursor` and ledger receipt.
Continue with that watermark, then retrieve later revisions. The receipt contains
device/environment, `enabled`, `capability_generation`, `cutover_at`,
`inbox_revision`, `minimum_cursor`, `unread_count`, `legacy_offset` and
`offset_revision`. A stale cursor receives `full_sync_required`; a cursor=0 full
snapshot removes absent server-managed records without resurrecting tombstones.

`POST /v2/devices/:device/inbox/ack` accepts
`{request_id,events:[{pair_id,event_id,disposition}]}` (1–100 exact identities).
Disposition is read/dismissed/rejected/retention; repetition of a request ID must
have identical contents. Temporary missing keys or disk errors are not permanent
rejection. ACK/removal tombstones survive eight days plus cleanup lag. Local ACK
intent and local read/delete state must persist in one transaction before UI and
badge update. Reads, deletes and eviction do not decide an approval.

`PUT /v2/devices/:device/legacy` accepts expected `offset_revision` plus up to 500
exact local unread `{pair_id,event_id}` identities. The service excludes IDs
already admitted, and matching future admission removes the legacy identity
atomically. Legacy content is not uploaded. Device badge equals server unread
plus confirmed legacy offset; each actual APNs attempt computes the latest
absolute value (including zero). Test/state never add unread. App foreground
reconciliation spans origin/device/environment scopes. Multi-origin background
delivery, APNs reordering and offline reads can temporarily show an old or
single-origin count; instantaneous lock-screen consistency is not promised.

## Native approval over SSH

Default-off local per-Agent opt-in accompanies remote policy. After an explicit
App change, `approval configure --stdin-frame` accepts
`{v:2,pair_id,agent,enabled,policy_generation}` and verifies the current remote
receipt before enabling. Local opt-in survives unrelated Pair policy updates;
every new request binds the current global policy generation. Configuration does
not prove the running Agent loaded or trusted a Hook. Codex may require `/hooks`
review; CLI never edits trust or feature flags.

Codex/Claude `approval wait --source AGENT` is a synchronous native PermissionRequest
Hook with a configured 130-second host timeout and an absolute request budget of
at most 120 seconds, including admission and delivery. Unsupported/oversize
requests, disabled policy, capacity exhaustion, timeout and cancellation return
no decision so native terminal flow resumes. Disabled hooks persist observations
and start at most a bounded five-second short-lived `activity flush` child, then
return immediately; they never block the native prompt on HTTP. Observation hooks
use a two-second command budget (Codex Interrupt host budget is three seconds).

OpenCode keeps one finite child per verified pending request, at most eight, with
a separate control path. After a decision, it re-queries the native pending
request, verifies exact contents/location/root and deadline, then calls official
`context.client.permission.reply` with `once` or `reject`. Route changes,
resolution, cleanup or deadline cancel children. Pi UI prompt events are observed
only and never offer native approval. Form/free-input/unknown tools use terminal.

`approval inspect|decide|release --stdin-frame` uses the existing four-byte
big-endian length-prefixed JSON frame, maximum 16384 bytes. Inspect accepts
`{v:2,pair_id,request_id}` and returns request/digest/expiry/generation/state/agent,
`operation_json` and `allowed_decisions:["allow_once","deny"]`. Inspect actively
checks the exact locator, live waiting process and current remote eligibility.
Only supported native operation structures up to 6144 bytes are accepted;
canonical full operation output is at most 8192 bytes. No truncation can enable
approval. Current Codex/Claude supported structures are Bash.command,
Codex apply_patch.command, Write.file_path/content, Edit.file_path/old_string/
new_string. OpenCode uses the complete native PermissionRequest snapshot.

`operation_json` is exact UTF-8 JSON with sorted object keys recursively, native
JSON numeric tokens preserved, Go JSON escaping (including HTML characters), and
no insignificant whitespace. It binds agent, session_id, run_id, request_id,
native_request_id, full locator, tool_name, complete tool_input,
allowed_decisions, created_at and expires_at. App verifies SHA-256 of the supplied
bytes; it must not hash a Foundation reserialization. This is not a terminal
transcript. Unknown or truncated inputs cannot be approved.

Decide/release accept `{v:2,pair_id,request_id,digest,decision_id,action,
policy_generation,expires_at,proof}`. Action is allow_once/deny/release. Derive
32 bytes using HKDF-SHA256: IKM = Pair encryption key, salt = UTF-8 pair_id,
info = UTF-8 `mobterm/approval/2`. Proof is standard Base64 HMAC-SHA256 over these
UTF-8 LF-separated fields with no trailing LF:

```text
mobterm/approval/2
PAIR_ID
REQUEST_ID
DIGEST
DECISION_ID
ACTION
POLICY_GENERATION
EXPIRES_AT
```

Private 0700/0600 state, no-follow file reads, cross-process lock and atomic
writes implement pending → decision_recorded → consumed/expired/cancelled/released.
One native request is shared across eligible Pair aliases; known native request
IDs deduplicate installation/agent/session/run/tool identity. Without a native
ID the identity is scoped to one random waiter, not falsely deduplicated by time.
Same decision ID/action is idempotent; another decision conflicts. Inspect/read
never decides. Release is no-decision, not deny. Disable of the last local
eligible Pair releases the waiter. Consumption rechecks target, live process,
deadline and current authenticated eligibility; an ambiguous consumed allow is
never replayed. API revocation and native consumption are not a cross-system
atomic transaction and cannot recall a decision already handed to the host.

Full operation JSON is scrubbed at terminal transitions; expired records are
scrubbed on next cleanup and receipts retained up to one hour after expiry for
idempotency. At most eight live requests and bounded receipt files are admitted.
SSH account control remains the trust boundary; a Pair ID alone is not proof,
and the HMAC does not isolate an attacker controlling that same remote account.
`decision_recorded` means submitted, `consumed` means handed to the native
consumer, not that the tool executed successfully. Never allow session-wide
permission, rewrite tool input, inject terminal keys or install permanent rules.

## Errors, vectors and rollout

Envelope/shape errors are 400; unauthenticated is 401; wrong Pair is 403;
capability/policy generation and same-revision content mismatches are 409;
revoked/expired Pair is 410; whole payload overflow is 413; independent quota is
429 with Retry-After. Server delivery receipts keep v1 state meanings. CLI SSH
approval failures are structured errors, distinct from the silent native Hook
output contract. Generation conflicts never justify an unproven alternate-format
replay.

Fixtures: `testdata/protocol-v2.json` authenticates all v2 indexing metadata;
`testdata/approval-v2.json` includes canonical operation bytes, digest and proof.
The API and App copy these exact fixtures. Existing v1 vectors remain untouched.
Future authorized rollout: compatible API/migration, App, CLI. New App with old
API retains local history/details/badge correction without cloud recovery;
old CLI stays v1; old adapter revisions remain accepted with limited capabilities.
No deployment, remote migration, real APNs, server hook installation or real
device testing is implied by local checks.
