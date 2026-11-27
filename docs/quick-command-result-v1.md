# QuickCommand result receipt v1

An authenticated SSH client can read a previously sealed receipt with
`mobterm-cli job output --stdin-frame`. Discover the reader through
`quick_command_result_protocols: [1]` in `capabilities --json`. An omitted
field means that the client has no evidence of this capability. Execution
backends are advertised separately. The [one-shot native worker](quick-command-job-v1.md) seals a receipt from the real shell wait status and the tail of its two output streams before enqueueing a completion notification.

## Read request

Send one unsigned four-byte big-endian length, one UTF-8 JSON object of that
length (1–16384 bytes) and EOF. The fields are:

| Field | Value |
| --- | --- |
| `v` | 1 |
| `installation_id` | The original CLI installation ID. |
| `job_id`, `command_id` | The original job and configuration UUIDs, lowercase with hyphens. |
| `request_digest` | The original frozen request's SHA-256 digest, 64 lowercase hex characters. |

The reader validates the current installation and loads one mode-private regular
file named `quick-result-<job_id>.json` inside the existing private CLI store.
It rejects symlinks, a different command or digest, and records whose completion
time is in the future. At `occurred_at + 604800` seconds the receipt expires.
Repeated reads leave the data and expiry unchanged.

On success, stdout contains one JSON object with the request fields plus integer
`occurred_at` (Unix seconds), `nonce`, and `ciphertext`. The 12-byte nonce and
the ciphertext including the 16-byte tag use standard padded Base64. Errors emit
a bounded v1 object with `ok: false` and exit status 1. Codes are
`invalid_request`, `identity_mismatch`, `result_unavailable`,
`result_expired`, and `invalid_result`. Missing and evicted records are both
unavailable. The client must retain the original lookup identity when retrying.

## Authentication and stream provenance

The producer seals the payload with AES-256-GCM and a fresh random nonce per
seal, using the original job's independent result key. A reader needs no result
key, pairing credential or network access to the notification service. The key
is excluded from the stored receipt, URL, request and response.

AAD is UTF-8 with these five lines and no trailing newline:

```text
mobterm/quick-command-result/1
<installation_id>
<job_id>
<command_id>
<request_digest>
```

The authenticated plaintext is one JSON object up to 8192 bytes. Its
`started_at` and `occurred_at` are Unix seconds. The client checks the
decrypted completion time against the outer field and the expected job and
terminal state. Both `exit_code` (0–255) and `signal` (1–127) are optional,
mutually exclusive integers. `succeeded` requires exit zero, `failed` requires
a nonzero exit, and `signaled` requires a signal. `timed_out` and `stopped`
can include the observed exit or signal. The v3 completion payload uses the
same state, exit and signal rules as this result.

`output` is standard padded Base64 containing the retained bytes in observation
order, at most 4096 combined for stdout and stderr. `stderr_mask` encodes
ceil(byte count / 8) bytes in the same format. Bit `i % 8` of mask byte
`i / 8` is one when output byte `i` came from stderr. Unused upper bits of the
last mask byte are zero. An empty output uses empty strings for both fields.
`truncated` records whether preceding output was discarded. Binary and partial
UTF-8 data remain intact on the wire.

The [cross-language fixture](../testdata/quick-command-result-v1.json)
contains public synthetic key material, expected AAD, receipt and decoded
payload. It exercises alternating streams, binary bytes, truncation and a
nonzero exit. See the [verification guide](validation.md) to run the fixture tests.
