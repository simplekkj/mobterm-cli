# Native QuickCommand jobs over SSH

An explicit user activation opens one authenticated SSH control session. The
App freezes the command, original target and optional jump, owner, name, job ID,
command ID and notification destination. Async execution requires an eligible
notification registration and exact paired installation. A foreground command
remains available independently of notification setup.

## Preparation and submission

The read-only capabilities command advertises `quick_command_native_v1` in
`execution_backends` when the real UID's account database identifies an
executable bash, zsh, sh or dash login shell. The CLI uses a two-second,
8 KiB bounded lookup via macOS Directory Services or Linux getent/NSS.
A Bash session explicitly setting `BASH_ENV` would re-run its supplied startup script inside the SSH account shell. This backend declines that case. Each accepted shell uses its own `-c` startup rules. Commands relying on side effects from the number of SSH or shell initializations should use ordinary foreground SSH. An unsupported shell or older CLI can use the ordinary foreground SSH path.
Install a Worker supporting v3 completion and classified inbox recovery,
then an eligible CLI and App before enabling asynchronous execution.

Run `mobterm-cli job submit --stdin-frame` without a PTY. Its first stdout
line is a JSON object with `v=1`, `state=prepared`, `installation_id`,
`server_time`, `submit_until` (Unix seconds, 120 seconds later) and a random
opaque `challenge` bound to that invocation. Check installation against the
original active Pair. The App must persist its complete run binding and
device-only 32-byte result key **before** sending the following request on the
same stdin. The control request has a 20-second deadline independent of the
user's runtime budget.

Send one four-byte unsigned big-endian byte count, one UTF-8 JSON object of
that length, and EOF. This dedicated frame is limited to 2 MiB. Pairing and
existing control protocols keep their original limits. The object contains:

| Field | Wire value |
| --- | --- |
| `v`, `installation_id`, `job_id`, `command_id`, `request_digest` | Original [result identity](quick-command-result-v1.md). |
| `pair_id`, `event_id` | Frozen original Pair and unique event. |
| `command_name` | Frozen single-line [notification display name](notification-v3.md). |
| `command_b64` | Standard padded Base64 of the original nonempty UTF-8 command, 1 MiB maximum and no NUL. |
| `result_key` | Standard padded Base64 of the per-job 32 random bytes. |
| `timeout_seconds` | Integer 1 through 604800. |
| `challenge`, `submit_until` | Exact values from this preparation. |

The request digest is lower-case hex SHA-256 over the following UTF-8 fields
joined with LF and no final newline:

```text
mobterm/quick-command-start/1
installation_id
job_id
command_id
pair_id
event_id
base64(UTF8(command_name))
decimal(timeout_seconds)
command_b64
```

The CLI launches its own executable in a separate session with isolated stdio
and two private inherited descriptors for the request and readiness response.
Under a short store transaction the worker reserves the ID, owner and request
and key fingerprints, starts one account shell with a single original command
operand, observes its process identity and persists running state. The parent's
second stdout line contains the full result identity and `state=accepted`.
The parent closes its descriptors and exits immediately. A proved pre-exec
failure instead returns that identity, `state=not_started` and an `error`,
including `argument_too_large` for the operating system's actual argv limit.
An unconfirmed handoff or identity conflict leaves execution uncertain. A client
that lost the ACK retains its original responsibility until an authenticated
completion or explicit original-job recovery.

Submitting an existing ID cannot rerun it. A changed request or key conflicts.
For an absent record, the worker rejects expired submissions. The installation
admits at most eight concurrent jobs; the App maintains one active command.
The worker inherits the SSH account's working directory and environment,
retains its original UID, and starts a dedicated process group without a PTY.
The submitted noninteractive shell has /dev/null stdin. SSH disconnect leaves
the worker running. Host shutdown, an administrator kill, OOM, and operating
systems that purge the entire login cgroup can terminate the worker.

## Completion and recovery

The worker drains stdout and stderr and retains the last 4096 combined bytes
in memory with stream ownership and truncation. It maps the actual wait
status to `succeeded`, `failed`, `signaled`, `timed_out` or `stopped`,
then commits [result-v1](quick-command-result-v1.md), the original terminal
identity and notification stage. It releases this job's execution resources
and leaves user-created files in place. The original Pair's completion
policy is checked at termination before a [v3 notification](notification-v3.md)
is frozen for that Pair. An unavailable completion channel leaves the actual
result intact.

`job status --stdin-frame` and `job cancel --stdin-frame` take the same
identity as `job output --stdin-frame`. Both return a bounded reply with
the identity, `state`, `started_at`, and, for terminal results,
`occurred_at` plus any observed `exit_code` or `signal`. An unknown job or
different installation returns an error. A terminal status outside its seven-day retention returns `job_expired`. A dead worker without a confirmed
termination reports `unknown`. Status reads leave retention unchanged.
Cancel records intent against the original worker. Only its verified child
process group receives TERM and, after a bounded grace period, KILL. The
worker leaves its child unreaped across escalation to keep the group ID pinned.
Following the final signal it allows two seconds for the kernel to produce a
wait status. An uninterruptible process leaves the job unconfirmed. A cancel
response can still show running. Only a persisted terminal result
confirms termination. Normal App waiting uses completion events and the
existing HTTP inbox, with SSH reads reserved for explicit recovery or output.

Encrypted receipts expire after seven days and are limited to 500 records and
4 MiB total. Admission maintains a separate seven-day dedup store capped at
4096 records and 1 MiB. Admission can decline new work at capacity while an
existing ID is protected. An evicted output does not free a live dedup
obligation. Results and dedup records are collected on writes; discovery and
read-only operations never extend retention.

## Foreground

The native backend also advertises `quick_command_foreground_protocols: [1]`.
`job foreground --stdin-frame` takes only `v=1` and `command_b64`, using the
same dedicated bounded request and account shell. Its stdout carries one
JSON object per line, each with `v=1` and a `state` field. The first event is
`accepted` after a real native exec or `not_started` with an `error` code.
The latter includes a true operating-system `argument_too_large` failure.
After acceptance, `stdout` and `stderr` events carry one standard padded
Base64 `data` field containing 1–16384 bytes. A terminal `succeeded`,
`failed` or `signaled` event carries the real `exit_code` or `signal`.
The CLI exits zero after writing a complete protocol response. The user
command's status comes from the terminal frame.

No background job, notification gate or output spool is involved.
Clients choose the backend before sending the command. Disconnecting the
UI cancels local tracking; a transport or framing failure cannot authorize
a second execution.
