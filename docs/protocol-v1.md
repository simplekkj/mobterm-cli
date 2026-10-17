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

Failure is nonzero with a generic JSON error and sanitized stderr. Pending local
state is retained for an idempotent retry after an ambiguous network failure;
it does not prove pairing succeeded. The App separately confirms API state and
`status --json --pair ID`. Cancellation never removes unrelated pairs. A caller
can abandon/revoke only its own pending attempt via device management.

`status --json` adds Agent compatibility diagnostics under `capabilities.codex`
and `capabilities.claude`: `minimum_version`, `supported_range`, and (on refusal)
`version_rejection`. `supported` is the shared stable/minimum policy decision,
not proof of native-event or phone delivery. `version` is a validation reference,
identified by `version_role: "validation_reference"`; it is not a version gate.
`actual_version` is bounded to 100 bytes (empty for failed/oversized probes), and
`installed` retains its successful-probe meaning. These optional diagnostics do
not change v1 installation/pair fields or their types. Installation-only status
does not probe Agents. Consumers may ignore added fields; no App/API upgrade,
data migration or credential/config format change is required. An older CLI still
reads the same state and restores its old exact Agent gate on rollback.

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
