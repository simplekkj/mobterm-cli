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
| `PUT /v1/devices/{device}/token` | `{apns_token,environment}`; advances token generation, including explicit same-token registration |
| `POST /v1/devices/{device}/pairs` | `{pair_id,installation_id,send_token_hash,policy}`; pending claim expires after 600 seconds; same binding/credential retry returns current state |
| `DELETE /v1/pairs/{pair}` | Returns `{pair_id,status:"revoked"}` for this owner's pair |
| `PATCH /v1/pairs/{pair}/policy` | `{turn_completed:boolean,needs_input:boolean}`; disables subsequent authorization for disabled event types |

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
uses the minimal random cycle created at `UserPromptSubmit`. Input notifications
have no upstream occurrence/tool ID: each actual invocation gets a persisted
per-cycle sequence, avoiding loss of separate parallel or repeated waits. HTTP
retries within that invocation retain it; separately duplicated upstream calls
cannot be distinguished and can duplicate alerts. This limitation is not hidden
behind a time-window heuristic.

The local retry cache contains ciphertext and a locator digest, never hook content.
Same pair/source identity keeps nonce, expiry and ciphertext identical across
re-entry. Expired entries are rejected and retained as tombstones for another day.
The bounded cache holds 512 entries; saturation fails visibly to stderr while
allowing the Agent to continue. No daemon or scheduled retry is started. Cross-system
delivery is not exactly-once.
