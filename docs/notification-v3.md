# QuickCommand completion notification v3

The [one-shot job worker](quick-command-job-v1.md) commits its encrypted result and terminal fact before checking the submitting Pair's current completion policy. It seals one event and hands its durable outbox record to the regular delivery path. The worker's delivery budget is 35 seconds. Policy denial or an unavailable policy at completion cannot authorize a later re-seal. A frozen, unacknowledged outbox entry retains its identity for a subsequent explicit flush within its TTL.

## Pair policy and identity

The sender needs the submitting job's completion-notification intent and exact
Pair, event, installation, job and command identities. At termination it must
read the original Pair's current `GET /v2/pairs/:id/capabilities` receipt.
The v3 sealer validates its version, Pair, installation, and expiry before using the active Pair key. Eligibility requires
`notifications_enabled`, `enabled`, `inbox`, `quick_command_v3` and
`quick_command_completed`. Both QC generations must be positive JSON-safe
integers. Missing fields or a failed read leave permission unconfirmed.

## Authenticated payload

Version 3 takes `kind=quick_command_completed`, `pair_id`, `event_id`,
`expires_at` (Unix seconds), `quick_command_capability_generation`,
`quick_command_policy_generation`, `nonce` and `ciphertext`. The 32-byte Pair
key seals UTF-8 JSON using AES-256-GCM with a fresh 12-byte random nonce. Use
padded standard Base64 for the nonce and ciphertext followed by the 16-byte
tag. The decoded ciphertext including tag fits 2400 bytes. The containing
APNs payload fits 4096 bytes, including the localized alert and badge.

AAD consists of the following UTF-8 fields joined with LF. There is no final LF.

```text
mobterm/3
quick_command_completed
<pair_id>
<event_id>
<quick_command_capability_generation>
<quick_command_policy_generation>
<expires_at>
```

The plaintext contains `v=1`, `installation_id`, UUIDs `job_id` and
`command_id`, `command_name`, `state`, optional `exit_code` and `signal`, and
`occurred_at` (Unix seconds). Use a single-line display name of at most 120
Unicode scalars and 512 UTF-8 bytes. The App retains the full configuration
name locally and freezes a separate display name in the job binding. It trims
outer whitespace, takes a prefix ending at a complete grapheme, and uses
`QuickCommand` if the resulting name is empty or contains disallowed text.
The CLI validates the frozen value as received. `succeeded` requires exit zero, `failed`
a nonzero exit and `signaled` a signal. A confirmed timeout or stop uses
`timed_out` or `stopped`. All five states allow at most one of exit code
and signal, including stop and timeout. The CLI sets expiry to one hour after completion, within the API's 86400-second limit.
The cross-language public vector is [protocol-v3.json](../testdata/protocol-v3.json).

## Admission and redelivery

Persist the frozen envelope before submitting it to
`POST /v2/pairs/:id/events` with the exact Pair's sending credential. Preserve
the generations, event ID, expiry, nonce and ciphertext on every attempt.
The Worker rechecks policy and capability generations, tracks the exact
dismissed event, and reports durable ownership in its event receipt. Accept a
matching `event_id` and `outbox_handoff:true` in an accepted, sending or
retryable state, including on a non-200 response. Terminal receipts settle
the delivery. An interrupted request leaves the original outbox record for
an eligible retry. Older Workers and older receiving Apps require keeping
QuickCommand notifications off. Agent notifications retain their v1/v2 routes.
