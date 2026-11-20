# Ordinary replies over trusted SSH (reply wire v1)

The App queries the exact notification copy through the paired host. A reply is a new literal message to the original running main Agent. A Question, form, elicitation or approval retains its own request, schema, deadline and response path. Terminal navigation and message-body lookup have independent identities and lifetimes.

## Discovery and local activation

`status --installation-json --read-only` advertises `reply_protocols:[1]`. An absent version leaves notifications, Jump to and existing native interactions usable. Native support also requires the correct loaded adapter and source. Codex ordinary replies return `ordinary_reply_unavailable`, including shared and embedded/profile modes. A loaded logical thread does not identify the original TUI incarnation.

After explicit user confirmation and an authenticated API policy update, invoke `reply configure --stdin-frame` with `v:1,api_url,installation_id,pair_id,agent,enabled,activation_revision:1,policy_generation,expected_revision,nonce,expires_at,proof`. Activation checks the active Pair, exact API generation and enabled per-Agent `followupMessage` grant. On success the CLI echoes the fields, installation ID, incremented `configuration_revision`, `configuration_saved:true` and `loaded:"unknown"`. Query `reply configuration --stdin-frame` with `v:1,pair_id` for the locally saved per-Agent generation map in `activations` and local CAS revisions in `revisions` (a missing Agent has revision zero). Confirm and sign that revision. Each successful configure increments it, including disable, preventing an old signed enable from undoing a later disable in the same cloud generation. A saved activation is distinct from an adapter currently handling a specific source.

A pre-upgrade interaction opt-in, including Codex async follow-up, contains no ordinary-reply activation. Installation does not enable it. Changing the API policy generation requires new confirmation, even when the effective boolean remains true. The existing local interaction disable also removes the matching ordinary activation. Each finite opportunity captures the participating local activations. Turning the feature off and on cannot restore an old opportunity. Install a compatible CLI first, update the App, then explicitly enable and load the adapter. Rollback preserves private receipts and replay tombstones.

## Operations and proof

Use `reply inspect|send|status --stdin-frame` over a dedicated, verified SSH exec channel. Supply one big-endian uint32 byte length followed by strict UTF-8 JSON and EOF. The reply frame limit is 128 KiB. The CLI returns a single JSON object. Duplicate keys, invalid Unicode and trailing data are rejected. No response text, key, helper token or native endpoint belongs in the command line, environment, logs, Worker or APNs. The request operation is determined by the command.

All three operations take `v:1,api_url,installation_id,pair_id,event_id,envelope_digest,nonce,expires_at,proof`. Use the canonical HTTPS origin without a trailing slash. The envelope digest matches message-body wire v1, the lowercase SHA-256 of AAD + LF + nonce-base64 + LF + ciphertext-base64. A fresh nonce and a proof expiring within 60 seconds bind this particular operation. A completed send or status lookup also binds `source_id,cursor,agent,action:"send",response_digest,decision_id,policy_generation`. Only send carries `text`. Retain its exact UTF-8 bytes, including surrounding whitespace and newlines. All-whitespace input and more than 16 KiB are rejected. The content digest is lowercase hex SHA-256 over the original UTF-8 bytes, without normalization.

Derive 32 bytes with HKDF-SHA256 using the Pair encryption key, Pair ID UTF-8 salt and `mobterm/reply/1` info. Compute HMAC-SHA256 over the following strings in order, each prefixed with its four-byte big-endian UTF-8 byte length. Use an empty string for omitted strings, decimal 0 for omitted policy generation, and standard padded base64 for the proof.

```text
mobterm/reply/1
operation
api_url
installation_id
pair_id
event_id
envelope_digest
nonce
source_id
cursor
agent
action
response_digest
decision_id
policy_generation
expires_at
```

For configure, use the same derivation and length encoding with the following fields in order: purpose, `configure`, api_url, installation_id, pair_id, agent, enabled (literal `true` or `false`), activation_revision, policy_generation, expected_revision, nonce, expires_at. Numbers use decimal encoding. A signed positive activation must match the cloud grant; an authenticated local disable can close an opportunity even when the cloud is unreachable.

The shared example exercises quotes, spaces, a newline and a multi-scalar emoji. [Go/Swift reply v1 vector](../testdata/reply-v1.json)

For Claude, OpenCode and Pi, an authorized inspect can request a new short lease from the same still-running adapter. An eligible response echoes the original delivery identity and nonce and provides `state:"eligible",source_id,cursor,agent,policy_generation,expires_at,opportunity_expires_at`. The lease is bounded by the current API grant and active host lease. Inspect again before send. Typical unavailability reasons include `source_binding_missing`, `ordinary_reply_unavailable`, `native_adapter_unavailable`, `source_changed`, `native_session_unavailable`, `reply_channel_unavailable`, `opportunity_expired`, `target_unconfirmed`, `activation_required`, `activation_changed` and `reply_disabled`. Missing or oversized body content does not affect eligibility.

## Dispatch and evidence

The CLI binds each Pair's immutable outbox event and envelope digest to a shared host source. For Claude, OpenCode and Pi, each source binds the installation, Agent storage/configuration namespace, exact native session, run, adapter instance, completion occurrence and parent process birth. The source registration, approval and native interaction records retain their original logical session digest for subsequent SSH checks. A new input in another pane of this session advances the same input cursor. A separate configuration or storage namespace with the same native ID has its own cursor. Claude classic and Mod rendezvous on the captured prompt ID and cycle. Heartbeats and tool progress retain the source cursor. A new turn/input or native request supersedes it. The original adapter additionally checks its live epoch, pending requests, location and source just before mutation.

The shipped Claude, OpenCode and Pi adapters register the event once. Their initial 15-second helper exits with a private source/owner handle if idle. The adapter keeps that handle only while its own source, session and activation remain current. Later, including after 30 minutes, an authorized inspect places a five-second marker in the private store. The marker filename contains the source digest and its contents contain only the expiry. A Claude Mod timer or an OpenCode/Pi filesystem watcher with bounded rescan checks the marker. The host checks its own source and launches a short CLI to reopen a 55-second lease. The CLI verifies parent PID and birth, registration token, unclaimed source, current run cursor, and consumes the marker under the writer lock. A frozen decision cannot obtain another lease. A missing or exited host produces an unavailable result.

The store admits up to eight active ordinary waiters and 1,024 source records. Registration preserves records with a live parent or within at least 24 hours of expiry. Store writes use the private directory, no-follow regular files, writer lock and atomic fsynced replacement. Exhaustion refuses new responsibilities. The native host does not repeat its source notification or re-register when an idle lease expires.

Under this same writer lock the send freezes its decision, persists `decision_recorded`, and then the original waiter independently checks the grant and source, removes the stored text and records `dispatching` before returning bytes to the adapter. Each owner has one final authorization. A different Pair sees `other_decision`, without the winning content, decision ID or digest. Reusing a decision ID with changed content or another source is rejected. An interrupted response after possible I/O remains owned by the original decision. Do not retry a native mutation. The original active participant may sign a new status query for its frozen tuple after native exit, expiry or action disable. Pair revocation ends access.

| Wire state | Evidence |
| --- | --- |
| decision_recorded | Exact decision accepted into the private local store. |
| dispatching | Durable claim before helper output. Check status only. |
| not_dispatched | The original waiter stopped before dispatch, or its bound adapter explicitly reported that no native call was made. |
| native_rejected | Explicit rejection from the original native interface. |
| native_accepted | That interface accepted the message. |
| native_consumed | A native input identifier correlated this decision with consumption. |
| delivery_unknown | The invocation may have started, but its outcome is unknown. |
| other_decision | Another operation owns this source. |

A reply state never proves model task completion, API/APNs delivery or phone display. Pi's void return has no correlated acceptance evidence. An unavailable or expired opportunity can coexist with a valid Jump to. Upstreams without an atomic expected-source/idle precondition retain a race between the last real check and their native call. The CLI cannot serialize unrelated terminal input or impose exactly-once execution on the Agent.

The `latest` operation uses the inspect identity fields and its own operation name in the proof. It checks the named Pair, event, and envelope digest against the local per-audience visible head over trusted SSH. A positive `state: current` response includes the live `locator` and an `expires_at` lease of at most five seconds. A local four-second challenge asks the originating interactive Claude, OpenCode or Pi adapter to read its current native session on either side of terminal sampling. The host checks the recorded process birth, foreground process-group leadership, controlling session, pane ancestry, installation and strong terminal identity. The comparison uses the event's Pair-scoped logical session. The caller must recheck the original Pair and event before selecting the terminal. Moving between tabs or windows can change the returned view metadata within the same strong identity.

An absent adapter, a reused shell, a switched session or a different strong terminal returns `unavailable` without a locator. Legacy notices without this provenance and a default Codex daemon without an independent live display binding also fail closed. This read-only operation survives the reply-helper lifetime while the original foreground host can prove the session. A live navigation receipt samples the host at a point in time. The Agent or multiplexer can still change after the last sample.
