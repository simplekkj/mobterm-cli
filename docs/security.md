# Security and privacy

Each installation has a random local ID. Each paired phone has an independent
32-byte sending credential and AES-256 key. Credentials and keys are local mode
0600 files in a mode 0700 directory. The API stores credential hashes and cannot
decrypt target locators. The CLI does not contain global service secrets.

Only routing metadata (opaque IDs, event kind, expiry, ciphertext length) reaches
the API/APNs. Encryption does not hide timing, kind, or traffic volume. Server,
username and terminal coordinates remain in the encrypted/local parts. Native
Agent payloads may include responses, prompts, tool arguments and transcript
paths; ordinary observations decode only explicit lifecycle fields and never open
transcript files. The opt-in [v2 approval bridge](protocol-v2.md) additionally keeps
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
