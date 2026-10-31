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
status preserved. Mobterm's API commands have a 65-second overall command budget and 30-second HTTP
timeouts, including a 30-second TLS handshake limit within that same request
budget on an independent transport. Default proxy rules, certificate verification
and redirect refusal remain unchanged. Shorter parent deadlines and cancellation
still win, with the final parent-context second reserved for persistence.
Local-only commands retain their ten-second budget; upgrade retains two
minutes. v2 observation uses a two-second budget; permission wait has an absolute
120-second request budget within a configured 130-second host deadline. A disabled
permission hook returns after durable observation and may launch one five-second
short-lived drain child; it does not wait for HTTP. Existing Agent-managed hook timeouts can cancel the CLI earlier; upgrading
the binary does not change installed Agent configuration or adapter deadlines. Each new delivery makes one immediate POST; later events or explicit
flush can recover up to 32 due deliveries, with at most four requests at once.
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
of successful native execution; forms, Pi prompts and unverifiable inputs retain
terminal handling. Neither approval nor release writes keys into a terminal.
