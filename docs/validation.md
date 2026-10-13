# Implementation validation — 2026-10-13

Environment: macOS, Intel amd64, Go 1.27.1; local Codex 0.162.1, Claude Code
2.1.282, tmux available. These results apply to the initial implementation source
in this delivery, before its first commit. The delivery report records final SHAs.

## Executed checks

| Check | Result |
| --- | --- |
| `test -z "$(gofmt -l .)"` | PASS |
| `go test -count=1 ./...` | PASS, 21 tests |
| `go vet ./...` | PASS |
| `go test -race -count=1 ./...` | PASS, including concurrent installation/state access |
| `actionlint .github/workflows/ci.yml .github/workflows/release.yml` | PASS |
| `python3 scripts/package.py --version v0.0.0 --commit 0000000000000000000000000000000000000000` | PASS, four CGO-disabled archives |
| `shasum -a 256 -c checksums.txt` inside `dist` | PASS, all four archives |
| Repeat the identical package command and compare `checksums.txt` | PASS, all four SHA-256 values identical |
| Extract `darwin_amd64`, run `version` and `status --installation-json` with isolated state | PASS, native execution and durable random installation identity |

`v0.0.0` and the all-zero commit above are explicit local packaging fixtures,
not a release tag or a claim about source provenance. Actual releases require
their tagged full source SHA. No tag or Release was created during verification.

The first in-progress packaging attempt overlapped an import change and failed
before producing a complete set. It was rerun after source freeze, then repeated
to establish reproducibility; the complete final set and checksums passed.

Tests cover AES-GCM cross-language golden bytes and mutation/wrong-key/nonce/AAD/
version/expiry rejection; framed setup/installation mismatch/idempotent claim and
local readback; 0600 storage, symlink refusal and concurrent identity creation;
TOML comments/multiline notify argv composition and original notifier exit codes;
Claude preservation of unrelated hooks and later user edits; stable encrypted
retry bodies and separate turns; native-event filtering; silent hook failures;
real tmux inactive-pane lookup, rename and deleted-pane rejection; Herdr namespace,
socket replacement and occupant membership; release checksum/path validation and
upgrade failure retaining the old executable.

## Actual upstream event/runtime observations

A local tmux session on a dedicated test socket ran installed Codex 0.162.1 with
`codex -c notify=[fixture-collector] exec --ephemeral --skip-git-repo-check` and a
minimal request to reply without tools. The process exited 0. Its actual native
notify payload contained `type:agent-turn-complete`, a nonempty `thread-id`, and a
nonempty `turn-id`. The collector retained only those three allowlisted fields;
input, reply, and transcript content were discarded. No user Agent configuration
was modified. These observed fields match the adapter fixtures. This validates
the actual source signal, separately from synthetic API and cryptography tests;
it is not an APNs/phone delivery test.

Two real pinned Herdr release binaries ran with separate local HOME/XDG directories
and a synthetic workspace. CLI `target verify --stdin-frame` connected to the real
socket using `session.snapshot`, validated its actual snapshot and socket identity,
and returned `status:valid`. After the same fixture server stopped, verification
failed as required. Fixture servers were stopped; no user server was touched.

| Herdr | macOS amd64 binary SHA-256 | Result |
| --- | --- | --- |
| 0.9.1 | `053be0639935fe54ab5efbdb46651054e4f6a753a5b43153c88bd6912bce1e94` | live socket PASS, stopped instance rejected |
| 0.9.3 | `db62d548ff3e832b087a96b1894a08d26be3905f1830309cd556783f215d4054` | live socket PASS, stopped instance rejected |

The 0.9.1 release asset digest was also checked against GitHub's release metadata.
Both versions' fixed source restore paths allocate fresh opaque terminal IDs.

## Limits of this evidence

- Claude real interactive permission/elicitation/idle notifications were not
  triggered. Its exact installed version, official semantics and narrow fixtures
  were checked. No claim of a live Claude-to-phone result is made.
- Linux amd64/arm64 and macOS arm64 were cross-built here, not executed on those
  architectures. CI is configured for native Linux/macOS tests, but a workflow
  run and future tag Release are separate evidence.
- No cloud API was deployed, no live Workers-to-APNs connection was exercised,
  and no real device alert/lock-screen/cold-start/Jump to was observed here.
- API integration in CLI tests uses local TLS/HTTP fixtures and simulated response
  stages. It proves client request/retry behavior, not service availability.
- macOS signing, notarization, Gatekeeper acceptance and actual network upgrade
  from a published Release were not performed. Local package/upgrader validations
  and failure preservation passed.

See [compatibility](compatibility.md) for event delays, unsupported combinations,
source references and the Claude occurrence-ID limitation.

## Round 2 v2.0 — JT-R2 repair validation

The checks below ran on 2026-10-13 against the JT-R2 source changes. Native human
waits now use a fresh invocation identity independent of completion associations;
the existing 512-envelope bound and expiry policy are unchanged.

The regression was reproduced before the implementation change with
`go test -count=1 -run 'TestClaudeWaitWithoutCompletionAssociation|TestClaudeWaitNotifyEncryptedRetries' ./...`
(FAIL, exit 1). Both `permission_prompt` and `elicitation_dialog` returned no event
for missing/expired cycles; their full notify tests sent zero HTTP requests instead
of four. Valid-cycle parse tests also exposed that waits changed the completion
association timestamp/state. This was the expected pre-fix failure, not an
unresolved validation failure.

| Executed check | Result |
| --- | --- |
| `go test -count=1 -run 'TestClaudeWait\|TestClaudeSemantics\|TestCodexStableIdentityAndIgnoreApproval' ./...` | PASS after repair |
| `test -z "$(gofmt -l .)"` | PASS |
| `go test -count=1 ./...` | PASS, 25 top-level tests |
| `go vet ./...` | PASS |
| `go test -race -count=1 ./...` | PASS |
| `python3 scripts/package.py --version v0.0.0 --commit 0000000000000000000000000000000000000000 --output .cache/jt-r2-package` | PASS, all four CGO-disabled Linux/macOS amd64/arm64 archives |
| Repeat the identical package command and compare `checksums.txt` | PASS, all four SHA-256 values identical |
| `shasum -a 256 -c checksums.txt` in `.cache/jt-r2-package` | PASS, all four archives |
| Python archive/metadata inspection; extract native binary and execute `version` and `status --installation-json` twice with isolated `MOBTERM_CONFIG_DIR` | PASS, archive layout/mode/version/fixture commit correct; native darwin/amd64 executable and stable installation identity |
| `go test -count=1 -run 'TestUpgradeFailurePreservesOldBinary\|TestReleaseVerification' -v ./...` | PASS, checksum/path rejection and old-executable preservation |

The new regression matrix covers both human-wait event types with missing,
expired (>86400 seconds) and valid completion associations at both parsing and
the actual CLI `run` → `notify` → AES-GCM → controlled HTTP boundary. Every case
sends two separate native invocations; each receives a simulated HTTP 503 followed
by success. Retries have byte-identical bodies (event ID, nonce and ciphertext),
while the two waits have distinct IDs, nonces and ciphertext. Decryption verifies
the exact terminal locator. Raw session/message/tool/transcript fields and pairing
secrets are absent from request bodies and hook output; raw hook fields are also
absent from the cycle/envelope cache.

Additional checks prove waits do not create or refresh completion associations,
missing/expired cycles do not produce `idle_prompt` completion, unsupported versions
and missing session/target or ambiguous terminal contexts remain blocked, and
the cache refuses a 513th live event while allowing existing retries and expiry
cleanup. Existing Stop/SubagentStop/unknown-notification and Codex
PermissionRequest filters remain covered.

The version/terminal command outputs and HTTP service in these regressions are
controlled fixtures. This is not evidence of real interactive Claude events,
APNs delivery, phone Jump to, or a live service result. The all-zero commit and
`v0.0.0` identify local packaging fixtures; no tag/release/deployment was created.
Non-native architectures were built and inspected, not executed. Upgrade failure
contracts were tested locally; an upgrade from a published release was not run.
