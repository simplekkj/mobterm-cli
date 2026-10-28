# Security and privacy

Each installation has a random local ID. Each paired phone has an independent
32-byte sending credential and AES-256 key. Credentials and keys are local mode
0600 files in a mode 0700 directory. The API stores credential hashes and cannot
decrypt target locators. The CLI does not contain global service secrets.

Only routing metadata (opaque IDs, event kind, expiry, ciphertext length) reaches
the API/APNs. Encryption does not hide timing, kind, or traffic volume. Server,
username and terminal coordinates remain in the encrypted/local parts. Native
Agent payloads may include responses, prompts, tool arguments and transcript
paths; only explicit lifecycle fields are decoded, and transcript files are never
opened. No such content is persisted or sent.

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
timeouts. Local-only commands retain their ten-second budget; upgrade retains two
minutes. Existing Agent-managed hook timeouts can cancel the CLI earlier; upgrading
the binary does not change installed Agent configuration or adapter deadlines. Each new delivery makes one immediate POST; later events or explicit
flush can recover up to 32 due deliveries, with at most four requests at once.
A delivery reserves at most 12 POST attempts across calls. Durable owner leases
and exact-envelope reuse protect concurrent hook/flush operations. Per-event
service handoff ends local POST recovery; receipt refresh only performs GETs. Interactive test/status expose failures instead of
reporting a phone receipt. The original user notifier remains under the user's
own timeout/exit semantics.

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
