# Codex conversation append over trusted SSH

A verified completion notification from a default shared Codex daemon can receive one ordinary message for its original main thread. Other input and newer notifications can arrive first. The queue uses the conversation context at execution time. An idle thread can start work immediately, a busy thread can wait, and an unloaded persistent thread can wait for manual restoration. Interrupted work can pause native consumption even after restoration. Questions, approvals and terminal navigation retain their own identities and authorization.

Install the compatible CLI first, update the App, then explicitly enable append in server notification settings. Discovery requires both reply protocol 2 and mode `thread_append`. Embedded operation, older notices without a verified append source, and a replaced or unsupported daemon leave the action unavailable. Disable the local scope before rolling back. Preserve the private claim store and App ledger through upgrades and rollback so a previous unknown decision remains query-only.

## Delivery identity and limits

A trusted Codex notify callback verifies the immediate daemon parent, native peer and process birth, original home, root TUI thread and completed turn. The host identifies the source by installation, canonical configuration home, exact thread and exact turn. It freezes the daemon incarnation as the storage identity and the native completion time as the original seven-day eligibility clock. Before fan-out delivery becomes eligible, it binds the Pair, origin, event and sealed envelope digest to this source. A changed runtime cannot overwrite the original slot. Runtime details and source storage stay on the paired host.

The local writer lock serializes all audience claims. A private atomic record stores the winning decision, client message ID and SHA-256 digest before a single native mutation. It contains no plaintext message. ACK loss, cancellation, process death or disk failure after that boundary leaves a query responsibility. The host retains a claim for at least 90 days. Registration collects expired metadata into permanent digest-only replay fences. Limits are 2,048 resident sources, 32,768 compacted fences and 8 MiB of host metadata. Capacity exhaustion disables new eligible sources where necessary and preserves prior records. The App retains its existing body-free 90-day responsibility ledger.

## Discovery, authorization and configuration

Use `status --installation-json --read-only` over the verified SSH path. A compatible CLI advertises `reply_protocols:[1,2]` and `reply_modes:["thread_append"]`. This capability uses the existing cloud `codex/followupMessage` grant and an independent, explicitly enabled local Pair scope. Authorizing async follow-up alone grants no append access.

The SSH commands use one big-endian uint32 length followed by strict UTF-8 JSON and EOF, with a 128 KiB frame limit. Operate on the current authenticated Pair. `reply configuration --stdin-frame` takes `v:2,mode:"thread_append",pair_id`. The response echoes those fields and the installation ID, with `activations` and `revisions` for the Codex append scope and `async_enabled` for the existing local async scope. The v1 command keeps its own Agent map.

For `reply configure`, include the same fields as v1 configure with `v:2,mode:"thread_append",agent:"codex",activation_revision:1`. Enabling requires a current cloud grant of that generation. The local CAS checks `expected_revision` and advances the configuration revision on success. Disable invalidates outstanding append leases. A subsequent explicit enable permits fresh inspect of a still-eligible old source.

## Signed operations

Use `reply inspect|send|status --stdin-frame` on an independent SSH exec. Fields shared by all operations are `v:2,mode:"thread_append",agent:"codex",api_url,installation_id,pair_id,event_id,envelope_digest,nonce,expires_at,proof`. Use the original notification envelope digest defined in [reply v1](reply-v1.md). The nonce and expiry bind this invocation to its operation for at most 60 seconds.

Inspect an original event without sending a thread ID. An eligible reply supplies `source_id,lease,activation_revision,policy_generation,expires_at,opportunity_expires_at` and the last observed `thread_state`. The handle binds the immutable source and its original sealed audience copy. A current grant, local revision and exact same live runtime must still verify at send. An inspect refresh leaves the original source deadline intact.

Send adds `action:"send",source_id,lease,activation_revision,policy_generation,decision_id,client_message_id,response_digest,text`. The response digest is lowercase SHA-256 over the original UTF-8 bytes of the single literal message, with a maximum of 16 KiB. Preserve spaces, Unicode and newlines. The App persists the exact decision before the first SSH frame byte. A confirmed pre-write failure allows continuation of the same frozen decision. Once transmission may have begun, use status exclusively. Status repeats the claimed tuple with a fresh nonce and expiry and omits text. An active winning Pair can query after grant or source expiry. A retired Pair loses read authority.

Derive the 32-byte HMAC key with HKDF-SHA256, the Pair encryption key, UTF-8 Pair ID salt and info `mobterm/reply/2`. Each field is a big-endian uint32 byte count followed by UTF-8 bytes. Sign these ordered fields with HMAC-SHA256 and encode the result as standard Base64. Use empty strings for absent strings, decimal ASCII for integers (zero when absent) and lowercase booleans. The purpose is `mobterm/reply/2`.

```text
purpose, "2", mode, operation, api_url, installation_id, pair_id, event_id,
envelope_digest, nonce, source_id, lease, agent, action, response_digest,
decision_id, client_message_id, activation_revision, policy_generation, expires_at
```

Configure has its own tuple.

```text
purpose, "2", mode, "configure", api_url, installation_id, pair_id, agent,
enabled, activation_revision, policy_generation, expected_revision, nonce, expires_at
```

The [wire fixture](../testdata/reply-v2.json) supplies the full message/configuration examples and proofs for all four signed operations. Reply v1 and v2, interaction and approval purposes remain distinct.

## Matching native evidence

| State | Evidence |
| --- | --- |
| `eligible` | Recent read-only thread and queue checks plus current authorization |
| `other_decision` | Another claimant owns this source |
| `delivery_unknown` | Durable dispatch boundary, missing ACK, unknown native error or missing current evidence |
| `not_dispatched` | Confirmed pre-write refusal or a recognized native pre-enqueue rejection |
| `queued` | Exact add ACK, or earlier exact queue observation with `current_state_unconfirmed` |
| `waiting` | Read-only queue lookup found the same queue/client ID and exact original text |
| `admitted` | The exact thread contains a native UserMessage with matching client ID and original bytes |
| `content_changed` | The matching queue item contains externally changed input |
| `unavailable` | Source, identity, active authorization, version or capability could not be verified |

Receipts echo the notification identity, mode and operation nonce. The winning receipt includes the decision ID, client ID, digest, activation revision, generation and lease. `ack_confirmed` records a verified add ACK, and `queue_id` records a positively observed item. A failed current lookup preserves prior evidence and can return `current_state_unconfirmed`. A queue disappearance supplies no proof of admission or completion.

The CLI uses metadata-only thread/read and same-thread queue/list discovery. Status examines at most four pages each of queue/list (100 records/page) and thread/items/list (16 records/page), within a three-second search budget, a 2 MiB per-frame cap, a 4 MiB connection read budget and the command deadline. Query responses expose only the matching decision’s evidence. No endpoint, native history or reply body is sent to the Worker or APNs. Daemon restarts preserve the local receipt and preclude further observation through the replacement instance. The helper never starts or resumes a thread, steers a turn, injects terminal keys or retries an add.
