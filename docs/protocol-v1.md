# Mobterm notification protocol v1

This is the public, implementation-independent wire contract. The normative
cross-language fixture is [protocol-v1.json](../testdata/protocol-v1.json).

## Pair setup over SSH

`mobterm-cli status --installation-json` returns one JSON line containing `v:1`,
`installation_id` (32 random bytes encoded base64url without padding), `version`
and `commit`. The identity is atomically created once, independent of hostname,
IP or display name.

`mobterm-cli setup --stdin-frame` reads four big-endian bytes containing a positive
length at most 16384, followed by exactly that many UTF-8 JSON bytes:

```json
{"v":1,"api_url":"https://api.example.invalid","pair_id":"opaque_pair_identifier","installation_id":"opaque_installation_identifier","send_token":"BASE64URL_32_RANDOM_BYTES","encryption_key":"BASE64_32_RANDOM_BYTES"}
```

The sample domain is deliberately unusable. `api_url` is an HTTPS origin, without
credentials, query, fragment, or path. Secret packets are accepted only on stdin.
The CLI validates the version/installation, saves with mode 0600, claims using
`claim_id = pair_id`, then persists active status and reads the saved value back.
Success is an unframed JSON line:

```json
{"v":1,"pair_id":"opaque_pair_identifier","installation_id":"opaque_installation_identifier","status":"active","local_saved":true}
```

Failure is nonzero with a JSON error and sanitized stderr. Pending local
state is retained for an idempotent retry after an ambiguous network failure;
it does not prove pairing succeeded. The App separately confirms API state and
`status --json --pair ID`. Cancellation never removes unrelated pairs. A caller
can abandon/revoke only its own pending attempt via device management.

Pairing failures may add an optional `failure` object to the same JSON result:

```json
{"v":1,"ok":false,"error":"operation_failed","failure":{"stage":"claim","code":"rate_limited","http_status":429}}
```

`stage` is one of `configuration`, `installation`, `setup_frame`, `local_save`,
`claim`, `pair_status`, `local_readback`. `code` is one of `invalid_frame`,
`installation_changed`, `local_config_failed`, `local_save_failed`,
`api_unavailable`, `timeout`, `api_rejected`, `rate_limited`, `pair_revoked`,
`pair_expired`, `pair_status_failed`, `cancelled`, `unknown`. These are fixed
classifications, never raw exception text, stderr, URLs or response bodies.
Optional `http_status` is an integer from 100 through 599, present only when an
HTTP response supplied it. An unavailable connection does not establish whether
DNS, TLS or another network component failed. `timeout` requires context
or transport deadline evidence; arbitrary error text is never interpreted.

This metadata also covers configuration/installation failures before setup and
single-pair status failures. Existing status results keep their pair, local-save,
status and diagnostic fields; only one JSON object is emitted. Existing `v`,
`ok`, `error` and successful response meanings remain unchanged. Old consumers
ignore the optional object; new consumers reject unknown/malformed metadata and
fall back to the locally known operation stage. No metadata makes a nonzero exit
successful. No new minimum CLI version or API deployment is required.

For `status --json --pair ID`, the CLI reads the necessary local pair and queries
its API state **before** Agent probes or outbox scans. A successful result still
requires a local pair with the same installation identity and an API response
with the same pair/installation and `status:active`. Missing local state,
inactive/mismatched/malformed API state and API errors fail with nonzero exit.
The unchanged `capabilities`, `hooks`, `health` and `outbox` fields are then filled
within the remaining ten-second command budget. Probe timeout or outbox failure
is reported in those diagnostics and cannot replace an already confirmed core
result or change its exit status. Explicit cancellation still fails. This does
not promise fast diagnostics or turn a real network failure into success.
Full status and installation-only/agents-only retain their existing roles.

`status --json` adds Agent compatibility diagnostics under `capabilities.codex`
and `capabilities.claude`, plus `capabilities.opencode` and `capabilities.pi`: `minimum_version`, `supported_range`, and (on refusal)
`version_rejection`. `supported` is the shared stable/minimum policy decision,
not proof of native-event or phone delivery. `version` is a validation reference,
identified by `version_role: "validation_reference"`; it is not a version gate.
`actual_version` is bounded to 100 bytes (empty for failed/oversized probes), and
`installed` retains its successful-probe meaning. These optional diagnostics do
not change v1 installation/pair fields or their types. Installation-only status
does not probe Agents. Consumers may ignore added fields; no App/API upgrade,
data migration or credential/config format change is required. An older CLI still
reads the same state and restores its old exact Agent gate on rollback.

New sources also report `managed_hooks:true`, `adapter_revision:1`, and
`validation_reference`. Management support is independent of `supported`:
uninstall works without a supported runtime. Existing `hooks` values remain
booleans describing disk ownership, never live activation. Hook receipts retain
`v/agent/action/ok`; new-source successful install adds
`activation:"restart_required"`. An old App can consume new-source v1 events
without knowing the source. A new App facing an old CLI refuses only unsupported
new management actions and asks for a CLI update. Public envelope fields, AAD,
locator types, policies and token generations are unchanged.

## Read-only Agent discovery

`mobterm-cli status --agents-json` returns a single JSON line with required
`v:1`, `scope:"agents"`, `discovery_version:1`, build `version`/`commit`, and
`capabilities`, `hooks`, `health` objects containing all four known Agent keys.
The cross-client response fixture is
[agents-discovery-v1.json](../testdata/agents-discovery-v1.json).

This mode branches before installation initialization. It creates no Mobterm
state directory, installation ID, lock or pair; reads no pairs or outbox;
performs no notification API requests, sends, migrations or hook repairs.
Existing hook ownership/configuration is only read. Agent version commands use
one isolated disposable directory per probe, cleaned afterward, with the existing
five-second per-Agent / ten-second command budgets and bounded output. This
isolates real configuration; it does not claim arbitrary third-party executables
have no behavior of their own. Agent detection works before pairing.

`--agents-json` is exclusive with `--json`, `--installation-json`, `--pair`
(including empty or false-valued flags) and repeated mode flags. Invalid
arguments fail before probing or initializing state. Original `status --json`
and `status --installation-json` retain their installation/pair contracts.

Each capability adds the required typed `probe_status`:

| Value | Meaning |
| --- | --- |
| `ok` | Version command completed; `supported` still applies the existing stable/minimum policy |
| `not_found` | No Agent entry found in this user's current selection context |
| `failed` | Present entry could not run, including permissions, Node/asdf selection or Pi package failures |
| `timeout` | Per-Agent probe budget expired |
| `cancelled` | Probe context was cancelled |

`installed` retains its prior successful-probe meaning and is not an absence
signal by itself. Existing version rejection codes remain compatible; use
`probe_status`, not their strings, to distinguish absent/failed/cancelled probes.
Unknown output, prereleases and old versions produce `ok` with `supported:false`.
Only `ok`, `installed:true`, `supported:true` and the applicable integration
capabilities permit configuration. OpenCode/Pi additionally require
`managed_hooks:true` and supported `adapter_revision`; Codex/Claude do not.

`health.configuration_state`, `issues`, `restart_required` and `loaded` remain
independent of runtime availability. `state` can be `not_installed` for absence,
`unsupported` for a completed unsupported probe, or `unknown` for probe failures;
these never replace `configuration_state`. `loaded` remains `"unknown"`.
`hooks` reports ownership facts, not live loading or successful delivery.
Removal remains available without a compatible runtime and enforces ownership.

One Agent failure/timeout does not discard the other results. An overall command
cancellation/deadline returns a nonzero generic error instead of a discovery
snapshot. Callers must validate exit status, scope/version and required structure;
missing fields or unknown enum values do not mean no Agent was found.

An old CLI rejects this unknown flag. A new App must retain existing pairings and
show discovery unavailable/failed, never automatically fall back to full status,
setup or an install command. Explicit advanced diagnostics/maintenance may still
use the original full status, but must not promote it into a discovery snapshot.
An old App can ignore the added status fields on a new CLI. There is no migration,
API change, release or remote upgrade implied by this source change.

## Encrypted events

```json
{"v":1,"pair_id":"opaque_pair_identifier","event_id":"opaque_event_identifier","kind":"turn_completed","expires_at":2000000000,"nonce":"BASE64_12_RANDOM_BYTES","ciphertext":"BASE64_CIPHERTEXT_AND_16_BYTE_TAG"}
```

`pair_id` and `event_id` are 16–128 ASCII characters in `[A-Za-z0-9_-]`. `kind` is
`turn_completed`, `needs_input`, or `test`. Expiry is integer Unix seconds;
servers allow at most 24 hours in the future, while this CLI emits one hour.
Standard padded canonical base64 is used for the nonce and ciphertext. The
decoded ciphertext, **including the GCM tag**, is 16–2800 bytes. The API additionally
rejects any complete APNs payload over **4096 UTF-8 bytes**; it never truncates IDs.

Use AES-256-GCM with a 32-byte pair key and a fresh random 12-byte nonce. Append the
16-byte authentication tag to the ciphertext. AAD is these exact UTF-8 bytes,
with LF delimiters and **no trailing LF**:

```text
mobterm/1
PAIR_ID
EVENT_ID
KIND
EXPIRES_AT_DECIMAL
```

The plaintext is UTF-8 JSON, with locator fields:

```json
{"v":1,"installation_id":"opaque_installation_identifier","backend":"tmux","namespace":"/tmp/tmux-1000/default","server_identity":"1234:1700000000:1700000001","workspace_id":"$1","view_id":"@2","pane_id":"%3"}
```

tmux `namespace` is the exact `socket_path`; server identity is
`pid:start_time:session_created` from that pane's session. IDs are session/window/pane,
never names or indices. Ambiguous linked session membership is rejected.

Herdr uses `backend:herdr`, the real session namespace, opaque `terminal_id`, and
`occupant_id` equal to the exact terminal ID. `workspace_id`, `view_id`, and `pane_id`
come from the same structured snapshot. `server_identity` is
`socket-v1:` followed by lowercase SHA-256 of ASCII `dev:inode:ctime_seconds:ctime_nanoseconds`
from the namespace socket's `lstat`. It is checked before and after the query.
Pinned Herdr restores allocate new terminal IDs; IDs are never derived from panes.

Tests have `backend:test`, the installation ID, and empty namespace/server/workspace/
view/pane fields. Terminal and occupant fields are absent. Test events must never
initiate terminal navigation.

`mobterm-cli target verify --stdin-frame` accepts a locator in the same frame
format, verifies the current installation and exact live target, and responds
`{"v":1,"status":"valid","locator":{...}}` on success. Unknown, replaced, stopped,
ambiguous, and stale targets return nonzero; no server or terminal is created.
The App checks again after any provisional controller open.

## REST resources and stages

All requests use JSON and `Authorization: Bearer TOKEN`. Sending tokens are
base64url without padding encoding 32 random bytes. They authorize only their
own pair; installation/pair IDs alone grant no authority. Requests are at most
8192 bytes. Redirects are refused by the CLI.

| Request | Body / result |
| --- | --- |
| `POST /v1/pairs/{pair}/claim` | `{installation_id,claim_id}`; returns active pair |
| `GET /v1/pairs/{pair}` | `{pair_id,installation_id,status,policy,expires_at}` |
| `POST /v1/pairs/{pair}/events` | encrypted envelope; `{event_id,state,retry_after?,error?}` |
| `GET /v1/pairs/{pair}/events/{event}` | limited status of that pair's event |

Claim retries use the original pair ID as claim ID. The identical accepted claim
is idempotent; another claim ID after use conflicts, expired pending claims fail.
Pair states: `pending`, `active`, `revoked`, `expired`. Policy contains booleans
`turn_completed` and `needs_input`; `test` bypasses those two event policy switches.

Event states are `accepted`, `sending`, `apns_accepted`, `retryable_failed`,
`permanent_failed`, and `revoked`. HTTP 200 `apns_accepted` confirms only APNs
acceptance. HTTP 202 is processing; 429/503 can be retried with the **same envelope**
and Retry-After; 401/403/409/410/422 require an appropriate caller action.
An API success never means delivered, displayed, read, or successfully jumped.

Device management uses a separate App-generated random 32-byte bearer. IDs and
APNs tokens are not authorization credentials. A sender cannot call the management
routes below. Management requests use the same ID grammar and JSON conventions:

| Request | Body / observable contract |
| --- | --- |
| `POST /v1/devices` | `{device_id,apns_token,environment}` with the new management bearer; an owned registration retry retains the existing token |
| `GET /v1/devices/{device}` | `{device_id,environment,token_generation,sendable}`; sendable is service configuration/known token validity, not a delivery receipt |
| `PUT /v1/devices/{device}/token` | `{apns_token,environment,token_generation}`; atomically updates only the expected generation, then increments it, including explicit same-token registration |
| `POST /v1/devices/{device}/pairs` | `{pair_id,installation_id,send_token_hash,policy}`; pending claim expires after 600 seconds; same binding/credential retry returns current state |
| `DELETE /v1/pairs/{pair}` | Returns `{pair_id,status:"revoked"}` for this owner's pair |
| `PATCH /v1/pairs/{pair}/policy` | `{turn_completed:boolean,needs_input:boolean}`; disables subsequent authorization for disabled event types |

Token PUT requires a nonnegative safe integer `token_generation` from the device
POST/GET response (new devices start at 1). A stale generation returns HTTP 409
`token_generation_conflict` without changing the token; a missing or invalid
generation returns HTTP 400 `invalid_token_generation`. After conflict, reread
device state and use the latest locally owned token, checking ownership after
each await before writing or publishing readiness. All registration callers must
share this ownership, including pairing and token refresh. Cancellation alone
does not prevent an already sent request from executing on the server. Owned
POST retries do not overwrite the existing token. App and API must both support
this required conditional-write field; no database migration is needed.

`send_token_hash` is lowercase SHA-256 hex of the **UTF-8 bearer string**, not of
its base64-decoded bytes. The device management bearer can also read its own pair
and event states and submit an encrypted test event through the event route. It
does not grant ownership of another device's pairs. A claimed active pair is not
expired by its former pending-claim deadline.

`environment` is exactly `sandbox` or `production`, matching the service's APNs
configuration; provider topic and host cannot be selected by a sender. Actual APNs
payload shape is `{aps:{alert:{"loc-key":KEY},sound:"default"},mobterm:ENVELOPE}`;
localization keys are `agentNotificationAlert` and `agentNotificationTestAlert`.
They contain generic copy without terminal/project details. Neither management
nor event APIs accept SSH credentials, executable commands, plaintext targets,
decryption keys, or arbitrary webhook destinations.

## Retry identity

Codex identity is the hash of source + thread + turn. Claude native idle completion
requires the unexpired random cycle created at `UserPromptSubmit`. Human-wait
notifications are independent of that association and never create or refresh it.
They have no upstream occurrence/tool ID: each actual invocation gets a fresh
random identity scoped by session digest, avoiding loss of separate parallel or
repeated waits without another persistent occurrence store. HTTP retries within
that invocation retain the identity and encrypted envelope; separately duplicated
upstream calls cannot be distinguished and can duplicate alerts. This limitation
is not hidden behind a time-window heuristic.

For OpenCode/Pi only, identity hashes source + native event + session + event/round
ID + TUI instance, then the locally captured verified locator. OpenCode uses its
native event ID and per-TUI in-memory instance across hot reload. Pi uses a fresh
random association per observed run and never fabricates completion after reload.
Two TUI instances showing the same root can notify their own exact terminals.
The local adapter stdin contains only revision, host runtime version, event,
session/event/instance IDs and Pi aborted=false (maximum 4096 bytes); unknown
fields or invalid metadata are refused. It is not a public REST schema.

The local retry cache contains ciphertext and a locator digest, never hook content.
Same pair/source identity keeps nonce, expiry and ciphertext identical across
re-entry. Expired entries are rejected and retained as tombstones for another day.
The bounded cache holds 512 entries; saturation fails visibly to stderr while
allowing the Agent to continue. No daemon or scheduled retry is started. Cross-system
delivery is not exactly-once.

## Request protection and daily admission budgets

All callers, including scripts, use the same authorization and resource limits.
Public routing/body validation and native Cloudflare limiters precede D1 access.
Native limits (IP 300/minute, fixed service key 5000/minute, credential per route
family 120/minute) are node-local and eventually consistent, not precise global
quotas. Only Cloudflare's trusted connecting IP is used; shared NAT users share
that coarse limit. A credential is not proof of an official App installation.

Persistent request buckets are charged only after ownership/sending authentication
(or the new-registration exception). Sending and management credential buckets
are separate. Existing per-minute/hour and capacity restrictions remain in place.
New-device registration first checks existing ownership, then fixed global budgets,
then IP admission and capacity; arbitrary new registration is therefore not free.

Conservative configurable defaults, per UTC day:

| Admission | Default | HTTP 429 error |
| --- | ---: | --- |
| New device, service-wide | 200 | `registration_daily_limit` |
| New pair, per device | 40 | `pair_creation_daily_limit` |
| Claim, per verified pair | 20 | `claim_daily_limit` |
| Notification processing, per pair | 500 | `notification_pair_daily_limit` |
| Notification processing, per device (including tests) | 1000 | `notification_device_daily_limit` |
| Notification processing, service-wide | 5000 | `notification_global_daily_limit` |

Notification budgets count **valid processing admission attempts**, not unique
messages, delivery receipts or billable units. After authorization, active/policy,
envelope and payload validation, existing identical terminal events, live leases
and retries that are not due return their current state without a daily charge.
Different content under the same ID remains HTTP 409. Other eligible requests
charge global → device → pair daily buckets once each, before insertion, lease,
attempt increment or APNs. Each is an atomic D1 conditional counter; these layers
are not one all-or-nothing transaction. Earlier charges remain consumed when a
later bucket rejects, a competing lease wins, or service work fails. Each admitted
request can attempt APNs at most once; the five-attempt event ceiling remains.
Owned registration, pair creation and completed-claim retries skip their new
admission budgets, while ordinary request protection still applies.

Errors use the existing `{ "error": "CODE" }` envelope. Daily budget rejection
includes positive integer `Retry-After` seconds to the next UTC day (1–86400).
`rate_limited` retains the persistent bucket's remaining window. Native
`preflight_rate_limited` returns 429 with a conservative 60 seconds, not an exact
global recovery time. Missing/failing native bindings return 503
`rate_limit_unavailable`; invalid budget configuration returns 503
`budget_configuration_invalid`; both return 60 seconds. Existing APNs 429/503
state/error responses retain their meaning. Daily sending exhaustion does not
consume the management credential budget or prohibit policy disable/revoke,
device reads or token updates; those retain separate ordinary abuse protection.
Global exhaustion temporarily affects every user's new notifications.

The CLI preserves one initial POST plus at most one retry, a four-second HTTP
timeout and the command's ten-second context. A next-day Retry-After exits promptly
without a second POST; it never rotates event ID/nonce/ciphertext to evade a quota.
The 512-entry replay cache is not a daemon, spool or scheduled retry queue. App
clients do not automatically poll or re-pair on 429. Old App versions show a generic
failure with the new API; old CLI versions already exit within their command
budget. New clients retain generic fallback for older API errors. No encryption,
AAD, target, APNs payload or credential authority changes require simultaneous
upgrades. API/APNs acceptance still does not establish phone delivery/navigation.

## Reliability and strong target additions (round 1 v1.0, 2026-10-21)

These additions preserve the v1 public envelope and exact AES-GCM AAD above.
The plaintext remains a **top-level locator**, with optional `agent` restricted to
`codex`, `claude`, `opencode`, `pi`. Missing agent means “Agent”. Display metadata
is not a target identity field. Before `target verify` or `target resolve`, callers
must encode only the locator field whitelist; never forward the decrypted agent
field to the strict stdin decoder. The original golden vector is unchanged;
[protocol-v1-extended.json](../testdata/protocol-v1-extended.json) adds agent and
strong occupant fields using the same key/nonce/AAD convention.

### SSH diagnostics and recovery

`status --json [--pair ID]` adds `health`, `outbox` and
`capabilities.target:{resolve:true,tmux_occupant:"tmux-proc-v1"}`. Each
`health.<agent>` has `state`, `configuration_state`, `issues` (at most 12 fixed
`code/event/capability` objects), `restart_required`, and `loaded:"unknown"`.
States are `not_installed`, `unsupported`, `not_configured`, `complete`, `partial`,
`modified`, `conflict`, `unknown`. Version and disk state are independent.
`complete` proves disk entry/conditions only, never that an Agent loaded them or
produced a real event. Old `hooks` remains its existing boolean ownership signal.
Tests of notification delivery do not alter hook health.

Codex parses root TOML notify argv, executable and preserved notifier chain;
Claude checks both UserPromptSubmit and Notification commands, type, timeout 10,
and the exact `^(permission_prompt|elicitation_dialog|idle_prompt)$` matcher.
Missing owned entries may be restored by `hooks install`; moved, replaced,
duplicated or edited entries are refused. OpenCode/Pi check effective roots,
transaction/ownership, revision 1, rendered asset hash, CLI executable and the
OpenCode plugin reference. Their installation still requires an Agent restart;
no loaded-state inference is made. Unknown configuration is preserved.

`outbox` includes `pending`, `handed_off`, `terminated`, `next_attempt_at`, at most
8 `recent_errors`, and `waiting_for:"next_agent_event_or_explicit_flush"`.
An independent `outbox.error` means diagnostics could not safely read the store.
Status and `outbox status --json [--pair ID]` never send, migrate, or repair event
state. An interrupted transaction reports `outbox_recovery_required` until a
mutating operation resumes it. Pair status adds `local_status` and an optional
action independently of remote `status`.

- `outbox flush --json [--pair ID]`: user-triggered bounded send recovery.
- `outbox refresh --json [--pair ID]`: at most 32 receipt GETs, never a POST.
- `pairs reconcile --json [--pair ID]`: authenticate original credentials and
  persist active status or retire explicitly revoked/expired records.
- `pairs abandon --pair ID --json`: only a pending/pending_review attempt; removes
  local token/key and keeps a bounded redacted summary. This is **not** remote
  revocation or evidence that remote capacity has been freed.

Local active/review records and pending/pending_review records have separate
32-entry limits. Only active records send. Unknown authentication/404 retains
credentials for review; a transient active-pair network failure never deletes or
rebinds it. A lost claim reply is reconciled using the same authenticated pair GET
and installation. Confirmed inactive records and explicitly abandoned attempts
leave no local token/key, with at most 128 digest-only historical summaries.
Retirement does not affect another device. `unauthorized` suspends this pair;
policy denial terminates only that delivery. No automatic re-pair loop exists.

### Durable delivery ownership

Every eligible pair is independently persisted **before the first network send**.
A failed save is `not_saved`, never claimed queued. The command remains bounded to
10 seconds, each HTTP to 4 seconds. New events run before old due work. This
implementation uses one immediate POST per hook delivery (within the maximum of
two); subsequent invocations/explicit flush retry persisted work. Flush selects
at most 32 records with at most 4 concurrent requests and pair round-robin.
There is no daemon, cron installation, port, detached child, or idle wake-up.

Each delivery retains its original event ID, nonce, ciphertext, first creation
and one-hour expiry. Across calls at most 12 POST attempts are reserved durably.
Retry delay starts at 15 seconds, doubles to 5 minutes. A valid longer Retry-After
is respected. Integer overflow/excessive numeric headers saturate to one day
(which exceeds the original TTL), HTTP dates are supported, invalid headers use
the local delay. A retry at/after expiry terminates; expiry is never extended.

Only a structurally valid matching `event_id` response with
`outbox_handoff:true` transfers responsibility. Explicit terminal states win
before handoff. For `accepted/sending/retryable_failed`, handoff applies even
with HTTP 202/429/503: the receipt is retained and further local POSTs stop.
Without handoff, even 202 leaves local responsibility. Bad JSON, wrong event ID,
or a global health capability cannot imply receipt. Terminal/handed-off records
release large ciphertext; retry metadata and deduplication remain.

Receipt refresh uses the authorized existing single-event GET. A missing record
means `service_record_unavailable`; only after the known original expiry + 24h
may the CLI say `service_retention_may_have_elapsed`. Neither means delivered,
and neither causes a new event or re-POST. API/Cron recovery remains independent
of the CLI after handoff. APNs acceptance, local receipt and user viewing are
separate stages; no phone delivery or exactly-once guarantee follows.

### Versioned local storage and migration

`outbox-v1/` uses 256 digest-prefix shards, each bounded to 2 MiB; this does not
increase Store.readJSON's 2 MiB limit for other configuration. A dedicated bounded
index (32 MiB) maintains exact pair/global counts. A bounded transaction journal
(36 MiB) atomically repairs interrupted shard/index commits. Every read/modify
uses the existing cross-process private lock and 0600 atomic writes. Network IO
never holds that lock; persisted lease owner and 15-second expiry prevent hook /
flush competition. Late results must still own the lease.

Pending limits are 512 per pair, 16384 per installation, 80 MiB serialized
envelopes. Deduplication/results last seven days, at most 4096 per pair and
131072 installation-wide. No valid pending record or protected tombstone is
evicted to admit another event. Fixed capacity errors ask for retry after expiry,
reconciliation, or an explicit flush. Source identity is an irreversible digest;
raw hook input, terminal output, directories, prompts and conversations are not
persisted. Seven days at 500 events/day for 32 devices fits the metadata budget.

Legacy events.json migrates idempotently to `legacy_unknown`, never an automatic
send queue. Its existing identity/envelope/expiry is preserved; no new expiry or occupant
proof is synthesized. At most 512 legacy unknown envelopes remain under the
same byte budget for their seven-day deduplication window. They are not known
terminal deliveries and are never dispatched. Authentication-only inspection
recognizes legacy native source hashes even after a location move or expiry;
it does not authorize navigation or extend expiry. The obsolete events.json is
removed only after the replacement store commits. Corruption is an error, never an
empty replacement database. A rollback must not replay the new outbox through an
old CLI. Seven-day source replay protection requires a recognizable upstream
identity. Claude human-wait callbacks have no authoritative occurrence ID and
cannot be recognized as arbitrary raw replays; unrelated waits are not merged
by timing. Nothing promises permanent recognition of arbitrary callbacks beyond
the retained source-identity window.

### Strong identity and explicit movement

New tmux locators include `occupant_id:"tmux-proc-v1:<64 lowercase hex>"`.
SHA-256 input is UTF-8 `os + "\n" + lowercased_boot_id + "\n" + decimal_pid +
"\n" + birth`. Linux uses boot_id and /proc/PID/stat starttime ticks; Darwin
uses kern.bootsessionuuid and kern.proc.pid starttime `seconds:microseconds`
via fixed golang.org/x/sys v0.46.0. PID/name/second-resolution ps alone never
proves occupancy. Queries bracket process birth reads with matching pane,
pane_pid, full server identity and location snapshots. Missing/denied/reused
process identity fails without fallback.

`target verify --stdin-frame` remains strict about position. New
`target resolve --stdin-frame` returns `{v:1,status:"valid",locator:{...},
tmux_pane_pid:123}` for tmux; PID is absent for Herdr and is only an SSH control
proof. Resolve allows window changes **within the same tmux session** and keeps
installation, namespace, full `pid:start_time:session_created`, session, pane and
occupant identical. Cross-session changes are rejected. Respawn-pane retaining a
pane ID produces a new birth proof and is rejected. New clients must validate
before/after final conditional navigation and compare the resolved pane_pid in
the final tmux selection command.

Herdr resolve checks the same installation, namespace and socket incarnation
before/after one structured snapshot, then uniquely finds terminal_id==occupant_id
across workspace/tab/pane. Duplicate terminal IDs or pane slots, restart, restore
with new terminal ID and namespace change fail. It does not consult shared focus.
Old tmux locators without a strong occupant are available as history/manual-open
only in the new App; no live proof can be added retroactively. Old Herdr locators
with valid existing socket/terminal/occupant proof can resolve. The new guarantee
requires both CLI and App: an old App retains only its original navigation checks.

### API, APNs and rollout compatibility

The API conditionally persists the original envelope before announcing per-event
handoff. Old NULL-envelope rows do not claim handoff or auto-recover; a same-hash,
nonterminal retry may fill the original envelope without resetting state/attempts
or expiry. Existing once-per-minute Cron resumes accepted/due/released-lease work;
HTTP and Cron share leases, quota admission and final authorization. Terminal
metadata remains until original expiry + 24h; recoverable ciphertext is cleared.
New API response fields do not require a new envelope or access permission.

APNs keeps original generic/test loc-keys and encrypted envelope, adds a safe
`title:"MobTerm"`, `mutable-content:1`, and a per-pair/event 64-hex collapse ID: SHA-256 of UTF-8
`mobterm-apns-v1\nPAIR_ID\nEVENT_ID` with no trailing newline.
Different events never collapse by target. The final UTF-8 payload still must fit
4096 bytes. An old App ignores extension flags; an old API still provides generic
notifications but cannot guarantee NSE spool/preview invocation. New NSE detail
is opt-in and device-decrypted; Agent and separately permitted local connection
name are the only preview context. A processing-time locked key, missing group,
timeout or error falls back to original safe content.

Release order is separately authorized: additive migration/API first, then App,
then CLI. API rollback must stop new handoff and drain/explicitly terminate
outstanding responsibility, or retain the recovery implementation. Do not roll
back responsibility into a service that cannot recover it. No production release,
remote migration, Apple group creation or APNs operation is authorized by this
local implementation or by source commit/push.

Target commands on failure return nonzero with
`{v:1,ok:false,error:CODE}`. Codes are `target_upgrade_required` (old tmux proof),
`installation_replaced`, `target_invalid` (definite missing/replaced/ambiguous
identity), and `target_query_unavailable` (query, permission or unavailable birth
proof). Temporary inability to query does not prove the terminal was replaced.
