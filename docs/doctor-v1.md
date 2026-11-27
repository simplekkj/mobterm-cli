# Doctor protocol v1

The CLI serves this protocol over a non-PTY SSH exec or directly to a user.
Existing setup, status, Agent, job and notification protocols are independent.
A new CLI can serve an old App without migration. Install a Doctor-capable CLI
to enable the new App menu's remote checks. Upgrading the App alone does not
change the server or trigger a CLI upgrade.

## Discovery and read-only diagnosis

Check for integer 1 in `capabilities --json` field `doctor_protocols`. When
capability discovery fails (for example, a corrupt installation identity), use
the standalone `doctor --protocol` probe. It returns
`{"v":1,"doctor_protocols":[1]}` without reading the store. Absence on an older
CLI means unsupported. A nonzero legacy error is never a Doctor diagnosis.

Run the resolved executable with fixed arguments `doctor --json`. The server
returns a single UTF-8 JSON object, at most 128 KiB including the terminating
newline. Exit 0 indicates a completed healthy check, 2 indicates a valid
report requiring attention (including partial or unknown), 1 indicates a
request or execution failure, and 130 indicates cancellation. A killed process,
missing exit status or interrupted SSH stream cannot confirm completion. Do not
interpret stderr, human output or terminal buffers as receipts.

See the [wire fixture](../testdata/doctor-v1.json). Reports carry:

- `v`, `scope`, `target` (64 lowercase hex), `checked_at` (Unix seconds),
  `complete` and `overall` (`pass`, `warn`, `fail` or `unknown`).
- Up to 256 `checks`, sorted by check ID and target scope. Each has `id`,
  `scope`, `status`, `reason`, `evidence`, and `features` (IDs). Check states
  are pass, warn, fail, blocked, skipped and unknown. Optional `blocked_by`
  lists root check keys in the form `id/scope`. Fixed reason codes refer to
  evidence at the given layer. Missing runtime evidence is independent of
  matching files on disk. `duration_ms` covers execution. Optional `counts`
  and `details` hold up to 12 fields each. Their keys are safe codes, counts
  are nonnegative integers, and string details (including paths) are for local
  display only, limited to 512 UTF-8 bytes without controls or bidi overrides.
- Up to 256 `features` with `id`, `scope` and `status` (ready, degraded,
  unavailable, not_configured, disabled or unknown). A ready feature describes
  that capability at the evidenced layer.
- Up to 64 `actions` with `id`, `scope` and opaque `plan` (64 lowercase hex).
  Only known action/target combinations may be selected. A report with no
  planned writes has an empty actions array.

Code strings match `[A-Za-z0-9._-]{1,96}`. A consumer bounds the complete
receipt, nesting (16 levels), arrays and values before using them. Treat
unknown or malformed receipts as unverified. Never interpret remote strings
as commands, links, or executable paths. The App independently verifies its
own connection, account, CLI executable and operation owner on each run.

The default budget is 30 s (deep 120 s), with at most six active checks. Each
has its own deadline. Modules select transitive prerequisites, evaluate shared
observations once, propagate root causes within the affected dependency graph,
and preserve independent facts when one check fails or times out.
`--offline` makes no API requests and skips Agent version commands and system
account-directory lookups. Job records can still be inspected.
Deep interaction checks inspect scoped reply, approval and append records. A lost
acknowledgment remains unconfirmed. Worker notification stages are historical;
the delivery module reports the current Outbox snapshot. Its recent error is
from the newest-created event with a recorded error, using a deterministic tie
break. Unknown error text is replaced with a fixed category.
A remote non-PTY process cannot establish which TUI is foreground or whether a
phone displayed a notification. No check submits a job, interacts with an
Agent session, sends a test notification or initializes a missing installation.

## Selected recovery

After displaying the plan and receiving confirmation for one component, send
`doctor --fix --stdin-frame`. Stdin is a four-byte unsigned big-endian byte
length (1–4096), followed by one strict UTF-8 JSON object, then EOF:

```json
{"v":1,"id":"agent.reconcile","scope":"codex.hooks","target":"<report target>","plan":"<selected plan>"}
```

Allowed component scopes are codex.hooks, claude.hooks, claude.adapter,
opencode.adapter and pi.adapter. The action repairs an existing, verified
Mobterm-owned component. It cannot install a missing integration, change trust,
enable disabled hooks, or write a different component. The token covers
installation/account, CLI identity, component, Agent compatibility, selected
plan and file generations. Tokens expire within ten minutes. The CLI compares
the plan again under the component's normal locks, applies the existing
transaction, and independently reads back its disk state. Interactive
`doctor --fix` requires both a selection and confirmation in a TTY.

A recovery reply contains only `v`, `scope` and `status`. Exit 0 allows
`verified`, `manual_required` (current on disk, Agent restart or trust review
required), or `applied` (one journal completed, more inspection needed).
Exit 2 allows `stale`, `failed` or `unknown`. A lost reply may follow a write.
Issue a fresh read-only diagnostic before deciding whether further recovery
is appropriate. Never replay or broaden a selected write. Declared actions
cannot send notifications, retry jobs, replay approvals or remove pairings.

## Sharing

The normal wire report is local to the trusted SSH operation. It contains a
target identifier, scoped observations and may include private paths. Only an
explicit `doctor --report` or App share creates a shareable summary. The CLI
allowlists the overall state, completeness, counts by check/feature state, and counts for fixed support reason codes such as DNS or TLS failure.
It emits no targets, addresses, filenames, identifiers, payloads, raw errors,
commands, environment, credentials or unsanitized remote text. No upload runs.
