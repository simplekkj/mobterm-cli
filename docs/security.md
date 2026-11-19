# Security and privacy

Each installation has a random local ID. Each paired phone has an independent
32-byte sending credential and AES-256 key. Credentials and keys are local mode
0600 files in a mode 0700 directory. The API stores credential hashes and cannot
decrypt target locators or message summaries. The CLI does not contain global service secrets.

Only routing metadata (opaque IDs, event kind, expiry, ciphertext length) reaches
the API/APNs. Encryption does not hide timing, kind, or traffic volume. Server,
username and terminal coordinates remain in the encrypted/local parts. Native
Agent payloads may include responses, prompts, tool arguments and transcript
paths; ordinary observations decode explicit lifecycle fields. Completion extracts
only the current attributed assistant text, cleans it to at most 120 Unicode
scalars / 512 UTF-8 bytes, and encrypts that bounded excerpt end to end. It does
not collect full conversations, user prompts, reasoning, raw tool output or
terminal screens. Codex attribution may read only the canonical first session
metadata record under the resolved Codex home; it never reads subsequent
conversation records or opens an arbitrary hook transcript path. This bounded
read-only source header is distinct from conversation collection. The opt-in [v2 approval bridge](protocol-v2.md) additionally keeps
a bounded full native operation snapshot locally for the short-lived waiter;
only explicit SSH inspect reads it. It is never included in API/APNs, and terminal
request transitions scrub it. If a process is killed, the next local approval
cleanup scrubs expired snapshots; no background cleanup daemon is promised.

The native Codex notify interface itself puts its JSON input in argv. The CLI
cannot change that upstream contract and preserves it when composing a user's
existing notifier. Mobterm pairing secrets never use argv or environment variables.

Setup and target verification run over an independently authenticated, non-PTY
SSH channel. They never write to a foreground terminal. Store/config writes use
temporary files, fsync and rename; symlink files and unsafe state directories
are refused. Advisory locks serialize CLI writers. Agent files are compared
immediately before replacement; a conflicting concurrent edit fails explicitly.

No global service credential can authorize a command. The service cannot invoke
this CLI or read SSH keys. Notifications do not grant host trust, bypass an
authentication prompt, grant input permission or take over a Herdr controller.
The phone rechecks its local pair and connection binding before opening a target.

Hook errors are quiet on stdout, sanitized on stderr, and return zero. An existing
Codex notifier is invoked with its original argv/input and its own output/exit
status preserved. The [current command-budget reference](../README.md#notification-api-request-deadlines)
covers network, local discovery, observation, approval, interaction and upgrade
commands; local-only commands do not all share a ten-second limit. Shorter parent
deadlines and cancellation win. A disabled permission Hook durably records its
observation and may launch the bounded `activity dispatch` child without waiting
for HTTP: 12 seconds total, including at most 5 seconds of network draining.
It has no listener, scheduled idle wakeup or recursive child creation. Installed
Agent-managed Hook timeouts may cancel a CLI earlier; upgrading the binary does
not change installed Agent configuration or adapter deadlines.
Completion, test and explicit flush commands allow at most two POST attempts per
event within the command budget, sharing that limit across fresh sending and
recovery. Jittered backoff respects Retry-After and cancellation and waits outside
store locks. Short observation callers remain single-attempt. Later events or
explicit flush can recover up to 32 due deliveries, with at most four requests at once.
A delivery reserves at most 12 POST attempts across calls. Durable owner leases
and exact-envelope reuse protect concurrent hook/flush operations. Per-event
service handoff ends local POST recovery; receipt refresh only performs GETs. Interactive test/status expose failures instead of
reporting a phone receipt. The original user notifier remains under the user's
own timeout/exit semantics.

Pairing classifies typed network timeouts and response-body cancellation/timeouts
using the existing fixed `timeout`/`cancelled` codes, with cancellation first.
Underlying causes are kept only internally: displayed response-read errors remain
`invalid API response`, and transport errors remain `API unavailable`. Raw error
text, URLs, credentials and response bodies are never added to failure metadata.
Ordinary body-read errors, invalid JSON and oversized responses keep their previous
safe behavior; they are not relabeled as connection failures.

Failed API requests emit allowlisted network diagnostics to stderr; setting
`MOBTERM_NETWORK_DIAGNOSTICS=1` includes successful requests. Only fixed method,
phase/outcome, timings, connection reuse, protocol, HTTP status and a strictly
validated UUID request ID are emitted. No URLs, addresses, credentials, hook
payloads, arbitrary headers or raw server/network errors are retained. There is
no CLI diagnostic file and no change to stdout JSON. HTTP error responses retain
status and Retry-After even when their body is unreadable, using a fixed fallback
code instead of response content.

Upgrades trust the public project's GitHub release account and HTTPS release
assets, not arbitrary mirrors or script URLs. SHA-256 verifies integrity against
that same release. This is not an independent publisher signature. A compromised
publisher account remains a supply-chain risk. macOS signing/notarization is not
claimed.

New tmux occupant proofs hash OS boot identity, pane PID and exact kernel process
birth; no command line, environment or terminal content is collected. Resolve is
restricted to the original tmux session, or Herdr namespace/socket incarnation.
Missing/ambiguous/replaced identity never falls back to names or guessed panes.
Optional Agent metadata stays inside authenticated ciphertext and outside target
identity comparisons. Existing public envelope/AAD and credential separation
remain unchanged. Local retired-pair summaries contain only digests, status and
time, and never sending tokens or encryption keys.

V2 state indexes additionally expose an opaque random Pair-scoped stream ID and
monotonic revision. Finite encrypted state/reminder retention and per-device
read/badge bookkeeping are described in protocol-v2. SSH approval actions require
a purpose-separated HKDF/HMAC proof, the exact full-operation digest, live waiter,
strong target and current Pair eligibility at submission and consumption. This
does not isolate an attacker already controlling the same SSH account or make
cloud revocation and native consumption atomic. A submitted decision is not proof
of successful native execution. In the legacy approval channel, forms, Pi UI
prompts and unverifiable inputs retain terminal handling; separately enabled
interaction kinds have the exact support limits below. Neither approval nor
release writes keys into a terminal.

## Remote interaction decisions

Interaction wire v1 uses a separate HKDF/HMAC purpose domain and length-prefixed
fields, binding the exact request and answer bytes, pairing, kind/action,
revision, generation, expiry and decision ID. It never accepts an App-selected
native endpoint or command. Requests are globally unique per native continuation
on the host; separate phones compete on the same durable CAS.

`dispatching` is persisted before native I/O. An uncertain delivery cannot be
retried, released or reopened as pending. The policy is rechecked immediately
before dispatch, but cannot be an atomic transaction with the remote Agent;
revocation cannot recall an already delivered decision. Native completion and
actual tool execution remain separate evidence. The private store strips full
request/answer content after settlement and retains minimal anti-replay records
until the native runtime has exited and the replay window has elapsed.

Questions, schemas and labels are untrusted display data. Unsupported constraints,
ambiguous Claude question text/labels and unsafe numeric precision fail closed.
No URL is fetched automatically. Pi's opt-in gate is an additional extension gate;
allow returns control to the host's remaining trusted extensions rather than
bypassing them. OpenCode native deny affects the session's pending permission
batch and requires the new explicit scope; legacy approval offers only once.
See [the protocol and bounded lifecycle](interaction-v1.md).

## Message summaries and preview consent

A summary is an Agent output excerpt, not MobTerm's verification of claims such
as “tests passed”. Authenticated event/state enums determine status: completion
means this response ended, not that the overall task succeeded. Summaries do not
participate in occurrence/event IDs, Locator hashes, SSH targets, approval or
interaction digests, policy, or request selection. The existing candidate and frozen event excerpt remain bounded. Full replies are
separate private files; outbox metadata retains only the local archive reference,
size, content digest and exact envelope binding; logs and additional APNs/userInfo/spool fields contain
no plaintext summary. Same-event re-entry and retries reuse the original envelope.

App records show the original authenticated event's summary independently of a
later run snapshot. System notification summary preview requires its own local,
default-off permission; an existing Agent/connection-name preview preference is
not consent to response text. The extension also checks the current pair,
installation, keys, validity, system previews and publication revision. Turning
summary preview off retracts delivered detailed notifications which actually
contained a summary, respecting revision races; it does not delete App history,
unread records, generic reminders or change remote notification/action grants.
System lock-screen policy retains final control over subsequent presentation.

## Local historical message bodies

An attributed original reply stays on the CLI installation in a private 0700
directory and 0600 file. Neither the cloud envelope nor APNs receives full
text. The local bounds are 512 KiB of UTF-8 per reply, 500 files and 64 MiB
of reply bytes across the installation, including unpublished candidates.
The original creation time limits reads to seven days. Related writes and
explicit outbox maintenance prune expired entries and then evict oldest
entries (filename tie-break). Idle machines can retain expired files until
maintenance. Pair retirement removes its candidates/files and denies future
reads even if physical cleanup fails. Installation replacement fences the old
identity. The read command opens the existing lock and files only; it does
not initialize, prune, contact the API or update a run, terminal, request or
read receipt. The App and CLI each enforce locally known binding/revocation:
a cloud-only revocation has a propagation window until either learns it.
A historical read does not grant an approval, answer or navigation right.
