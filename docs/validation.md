# Validation records

This is a historical evidence ledger, not a statement that the current HEAD has
passed every check. Records were both prepended and appended; file order does
not establish recency. Preserve each record’s dates, source identifiers, failures,
skips and environment limitations. Local artifact paths describe the original
run and may no longer exist. Current contracts are routed from the
[README documentation map](../README.md#documentation-map).

Topic entry points: [2026-11-09 sender cleanup](#2026-11-09--codex-hooks-round-2-v12-r1-sender-cleanup),
[2026-11-06 Codex P1](#codex-hooks-第-1-轮-v10-p1-delivery-2026-11-06),
[2026-11-04 interaction repairs](#2026-11-04--remote-interactions-review-repairs-r4r5-wf-10),
[interaction implementation](#remote-interaction-wire-v1--implementation-validation),
and [fixed compatibility samples](#mobterm-cli-compatibility--round-1-v10--2026-10-16).
The earlier records below retain their original scope; they are not rerun by
documentation maintenance.

## 2026-11-23 — QuickCommand result receipt v1

On Intel macOS 15.8.1 with Go 1.27.1, the result vector authenticated and
decoded with its synthetic AES-256 key. The Go tests verified every AAD identity,
a changed tag, interleaved binary streams, empty output, the 4096-byte limit,
invalid terminal metadata, the exact seven-day cutoff, file permission and
symlink refusal, strict framing and read-only behavior on a missing store.
The capability test checked result protocol v1 with an empty execution backend
list. All four focused tests passed.

A trimmed CLI binary read a synthetic result over loopback OpenSSH using a
pinned host key, a temporary client key and a forced `job output` command.
Two separate SSH invocations returned the same encrypted receipt byte fields.
A third request with the wrong digest returned `identity_mismatch`. Sizes and
modification times in the isolated result store remained unchanged. The
fixture removed its server, private keys and store at exit. This exercised
the SSH reader with a prewritten receipt. A real job producer, client
submission, startup disconnect survival and APNs are outside this run.

The final sources passed formatting, vet, a trimmed Intel macOS build and a
CGO-disabled linux/amd64 cross-build. There was no running local Docker daemon
for a native Linux execution check. An initial ordinary suite run encountered
the existing asdf five-second launcher preflight timeouts during simulator
startup. The final ordinary suite passed in 642.796 seconds, including the asdf
groups. The full race run passed in 783.357 seconds. Each final run recorded
902 passing tests/subtests and 6 skipped opt-in native cases/subprocess
entries. Both required real tmux and supplied the pinned Herdr 0.9.3 fixture.

## 2026-11-23 — Read-only capabilities and workspace discovery

The CLI work started from clean `main` at
`e91bbd5505c2510fbd60bb005ff3b3914bd159ac`. This change advertises only
`tmux.discovery`, `herdr.list`, `herdr.probe` and `herdr.snapshot`.
The binary reported an empty execution backend list and reading a missing
private store left the filesystem untouched.

On Intel macOS 15.8.1 and Go 1.27.1, `TestLiveTmuxDirectory` exercised real
tmux 3.7c against a dedicated socket, with the server initially absent and
then running. It checked directory topology, server identity and input framing.
`TestLiveHerdrDirectory` ran the App's previously verified v0.9.3 x86_64 Herdr
fixture (SHA-256 `db62d548ff3e832b087a96b1894a08d26be3905f1830309cd556783f215d4054`).
An explicit namespace retained its socket incarnation and pane terminal IDs with
unrelated inherited HERDR variables. `TestWorkspaceCapabilitiesNoInitialization`
also checked absent, initialized and malformed stores. All three passed on the
isolated read-only source. The tests use temporary homes, sockets and server
processes. To exercise Herdr, supply its absolute path in
`MOBTERM_DISCOVERY_TEST_HERDR`. To require tmux, set `MOBTERM_REQUIRE_TMUX=1`.
The targeted ordinary and race runs both passed all three tests. The ordinary
full suite also exposed the existing asdf launch-fixture five-second setup
timeout in two test groups under host contention. Re-running those two groups
on the same source passed all 96 tests/subtests in 138.717 seconds. No assertion
or timeout was relaxed. The full race suite reported the same two launcher
setup timeouts, with 893 passing and six opt-in skips; the two failing leaf
cases passed again with the race detector in isolated runs (7.126 and 5.764
seconds, respectively). The three new discovery tests
passed in both full runs. The CLI also passed gofmt, vet, a trimmed macOS build
and a CGO-disabled Linux amd64 cross-build. This CLI verification does not
exercise iOS rendering, production APIs or APNs.

## 2026-11-23 — Codex notification conversation append, round 1 v1.0

The CLI started from clean `main` at `9ba9255e88c3435bc096c4d114754bb0a6ade9ae`. The Go protocol vector covers inspect, send, status and configure and uses the same expected proofs as the Swift tests. A storage fixture covers same-event retry, independent homes, changed daemon birth, seven-day expiry, replay after collection and malformed/unknown stores. A synthetic two-Pair TLS grant fixture exercises new completion outbox fan-out, old-grant denial, independent local activation, invalidated old lease, concurrent claim and restart readback of retained ACK evidence. These fixtures do not exercise an iPhone or APNs.

A locally built Intel macOS CLI reported reply protocols 1 and 2 and mode `thread_append` at `status --installation-json --read-only`. Framed `reply configuration` for active fixture pairing returned the original v1 map and the v2 default-off Codex scope with async disabled. The [delivery report](https://app.notion.com/p/421cede2082381468c9dd4eb85c6514d) records the exact commits, final checks, native runtime coverage and outstanding environment boundaries.

On macOS 15.8.1 Intel, real tmux 3.7c and Herdr 0.9.5 (protocol 22) each hosted two default shared Codex 0.171.0 TUIs. Two notifications per multiplexer reached the local Worker. After a later turn in the first thread and /new in the second pane, the iOS 26.3.1 App authenticated both envelopes and obtained matched queue ACKs through trusted SSH. The original threads contained the corresponding client IDs and literal messages in all four cases. One tmux target was unloaded when the App queried; it still held the exact queue item and consumed it only after the test operator explicitly resumed it. The local worker used application grant logic and local D1 with intercepted APNs outbound traffic.

The native lifecycle fixture runs the pinned, signed 0.171.0 executable against a temporary store and loopback model. A cold unloaded thread stayed unloaded after add. Explicit resume admitted the original client ID and literal input. Normal and cold resume of the Interrupted thread both left its queued items pending. The local model produced an actual command approval and a plan-mode Question. Queueing the text “同意” preserved each pending native request. The fixture then separately denied the approval or answered the Question and observed admission. The full queue contained 100 submissions and refused one more. Archived and deleted threads both refused add.

```sh
python3 scripts/test-codex-shared-daemon.py tmux --queue-lifecycle --codex "$CODEX" --cli ./mobterm-cli
```

For the cross-repository product test, build this CLI under its final name and build the sibling API with its locked dependencies and `npm run build`. The script starts an ephemeral loopback SSH server, TLS origin, migrated local D1/Worker, and two no-argument TUIs in one profile. The harness intercepts outbound APNs. Use a fresh disposable simulator because the test trusts a run-owned TLS root and uses Keychain. Run one multiplexer at a time.

```sh
mkdir -p ../mobterm/build/codex-thread-append-r1
manifest=../mobterm/build/codex-thread-append-r1/live.json
python3 scripts/test-codex-shared-daemon.py tmux --app-bridge "$manifest" --local-api ../mobterm-api --codex "$CODEX" --cli ./mobterm-cli
```

After the fixture prints READY, import the `tls_root` from the private manifest using `xcrun simctl keychain "$SIM" add-root-cert "$CERT"`. Run the App's `mobterm` scheme with `-only-testing:MobtermTests/SSHCommandStreamTests/testCodexThreadAppendFromTwoAuthenticatedDeliveriesOverSSH` on that simulator. On success, touch `live.done` beside the manifest and wait for both native checks. The fixture explicitly resumes an unloaded synthetic thread only after observing its pending queue item. It matches each native user message by thread, client ID and original bytes. Repeat with `herdr --herdr "$HERDR"` instead of `tmux`. Interrupt the fixture on test failure and delete only the disposable simulator after preserving the xcresult. Keep the manifest private until the fixture cleans it up.

### Go integration and adjacent hosts

On Intel macOS (Darwin 24.6.0), with Go 1.27.1 and tmux 3.7c, both full suites required tmux using `MOBTERM_REQUIRE_TMUX=1`. A preliminary default-timeout run stopped after 600.952 seconds while an existing physical-session test had been running for three seconds. It recorded no preceding assertion failure. The integration runs used the established 45-minute package timeout and ran serially.

| Command | Result |
| --- | --- |
| `go test -timeout 45m -json -count=1 ./...` | 892 tests/subtests passed, six skipped, zero failures, 583.522 s |
| `go test -race -timeout 45m -json -count=1 ./...` | 892 passed, six skipped, zero failures or race reports, 756.385 s |
| `go test -timeout 8m -count=1 -run '^Test(Append\|ExpiredAppend)' .` and the same with `-race` | Final source, 33 passed in each run, zero skips/failures, 3.291 s and 4.760 s |
| `go vet ./...`, `test -z "$(gofmt -l .)"`, `git diff --check`, `go build -trimpath -o mobterm-cli .` | Passed on final Go source |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath` with temporary output | Cross-build passed, ELF x86-64. Linux native execution was outside this run. |

The full suites started before the final narrow changes aligning queue-ID validation with Swift and checking the current remote Pair before disclosing an expired source. Final-source targeted runs cover those paths, malformed and lost ACKs, the four shared wire proofs, capacity, and both local Codex scopes. The two-Pair fixture explicitly enables the legacy async scope for one Pair and leaves it off for the other. Both start with append disabled. Closing append preserves async, revokes the old lease and leaves a claimed receipt queryable.

The six routine skips were five opt-in real-host scenarios and the navigation event child process. A final-source targeted run supplied isolated test binaries for Claude Code 2.1.330, OpenCode 2.3.2 and Pi 1.6.0, the final trimmed CLI, real tmux, temporary agent configurations and local fixture models. All five scenarios passed in 96.952 seconds, with zero skips or failures. Pi, Claude and OpenCode ordinary replies after session restoration passed in 5.18, 70.50 and 8.32 seconds respectively. The OpenCode permission-scope test passed in 5.54 seconds. Pi terminal navigation passed in 7.03 seconds and invoked the event helper as a child process. These runs exercised the original protocols independently of Codex append.

## 2026-10-31 — Agent activity and native approval, round 1 v1.0

[Implementation and acceptance](https://app.notion.com/p/40acede2082381daa4a8f505dff3b649).
CLI analysis/start: `81f506c9505ec11e55f2590906c1bddf26b1488d`, clean `main`.
App start: `88362dc3bc712e98a58302cf4ad4c42f1b3e410d`; API start:
`0b0c582bb64956753efc3a401b77d6cdf6e13852`. No baseline drift.

This delivery adds negotiated v2 encrypted run snapshots and reminder intents,
independent per-Agent native approval, four native state adapters, bounded
local state and approval storage, and exact shared encryption/approval vectors.
Legacy v1 kinds and AAD remain unchanged. Completion, a tool return, a decision
recorded locally, and native execution success are distinct states.

Environment: macOS 15.8.1 x86_64, Go 1.27.1 darwin/amd64, Node 22.22.2.
Go tests use isolated temporary stores/config, synthetic native input and loopback
HTTP; the official host smoke tests below also use isolated HOME/XDG/agent roots
and omit model credentials. No real user hook or trust setting was changed.

Focused implementation checks cover AEAD metadata mutation, exact canonical
operation digest/HMAC, unsupported/malformed capability receipts, crash-safe
activity retry and single handoff, active parallel tools, 40 sequential tools,
duplicate starts and old terminal updates, Claude late idle after a new cycle,
unsupported/oversize native requests, cross-Pair competing decisions, proof
forgery, last local opt-out release, full operation scrubbing, and actual local
waiter consumption followed by refusal to replay the same native request.
The approval fixture intentionally has generic needs_input=false and Codex
approval=true; local opt-in survives unrelated global policy generation changes.

- Focused race suite: PASS 17.829s (`TestActivity|TestMalformedCapabilities|TestToolAggregation|TestApproval|TestNativeWait|TestClaudeLateIdle|TestCodexNative|TestStructuredCodex`).
- Final compatibility/discovery/Claude ownership regressions: PASS 20.295s.
  Moved/edited/duplicated commands retain the original error and byte-unchanged
  assertions. Repeating install from a different binary retains proven owned
  command paths. Each compatibility sample has a separate config/journal and
  seeds its version's current capability set before testing idempotence.
- `node scripts/test-native-adapters.mjs`: PASS, including OpenCode exact native
  pending re-query, one reply, changed-request refusal, root/family ownership,
  separate approval child control, Pi UI waits and aborted/non-aborted settlement.
  Before final handoff the coordinator also ran the required pinned install,
  `npm ci --prefix scripts/native-adapter-tests --ignore-scripts --no-audit --no-fund`,
  using isolated cache `/tmp/mobterm-round1-adapter-npm-cache`: PASS. Its first
  subsequent script run failed `root success once` (0 rather than 1): the fixture
  read events after a fixed two-second sleep while state and completion used
  successive real child processes. The final fixture tracks real notification
  child close events and bridge queue advancement before reading; independent
  pending approval children do not block event observations. A deliberate 2.2s
  first recorder delay now exercises this regression. Exact counts, cancellation,
  approval checks and the actual production ten-second timeout assertion remain.
  `node --check scripts/test-native-adapters.mjs` and the full script after the
  pinned install both PASS; log `/tmp/mobterm-round1-adapter-after-ci-final.log`.
  This follow-up changes only the test fixture and this evidence record; the
  already validated Go code, adapters and production budgets are unchanged.
- `scripts/test-native-adapters-host.py`: PASS with official OpenCode 2.1.0 TUI
  setup and restart (13 listeners) and official Pi 1.3.0 discovery/import plus
  actual TUI session_start (8 listeners). Generated adapters were loaded by the
  official hosts; no paid/authenticated model request was sent.
- `test -z "$(gofmt -l .)"`: PASS.
- `go test -json ./...`: PASS 463.189s, 132 top-level tests / 555 including subtests; zero failures or skips.
- `go vet ./...`: PASS.
- `go test -race -json ./...`: completed 493.576s, 547 test/subtest outcomes; no race reports. The command failed only on eight pre-existing asdf launcher preflights (eleven failures including their parents), each killed by the unchanged five-second bound while Simulator UI tests ran concurrently. Exact affected contexts rerun on the same frozen code after the Simulator workload stopped: `go test -race -json -run '^TestAsdfCompatibilityEntrypoints$/^(agent-project-explicit|node-home-explicit)$' ./...`: PASS 78.186s, 67 test/subtest outcomes, zero failures/skips or race reports. This covers every failure and all nested cases prevented by its preflight; the original full-race command is not reported as passing.
- `git diff --check`: PASS, including this record.

The first full run failed at 465.877s: Claude's new owned command validation
incorrectly compared the current caller path with the original owned path;
version refusal diagnostics drifted; compatibility tests assumed a future-version
native upgrade could not change config and reused config across failed cases;
and the discovery vector needed its new declared fields. A moved Claude notify
could also be mistaken for a legitimate new Stop command. Production checks now
retain the exact owned executable and validate command/event pairs; existing
moved-user-edit byte preservation assertions were not weakened. The discovery
vector is synchronized across repositories. An intermediate focused run had a
five-second discovery probe timeout under concurrent old full-race load; that
obsolete race run was stopped. The final ordinary and race commands ran serially within the CLI on frozen
code; Simulator tests in the App overlapped the initial race run. The affected
race contexts were then retried after the Simulator workload stopped, preserving
all budgets and assertions. Only this evidence record was added afterward. Historical failed runs are
not counted as passing evidence.

Native capability and evidence matrix:

| Agent | State source and capability | Native mobile approval | Evidence this round |
| --- | --- | --- | --- |
| Codex | 0.166.0 native Hook reference; notify remains sole completion; PreToolUse is pending, post-hook is returned, Interrupt is interrupted | Default off; verified PermissionRequest allow/deny/no-decision; host trust must be reviewed, never auto-granted | Fixed official schema, isolated native fixtures and installer ownership/disabled-setting tests; no real model turn |
| Claude Code | 2.1.301 fixed native Hook schema; prompt/tool events and current stopped-cycle idle | Default off; supported complete PermissionRequest tool structures only | Native schema fixtures, cycle ordering and atomic install/repair tests; no real model turn |
| OpenCode | Revision 2 TUI root execution/tool lifecycle and permission/form waits | Default off; complete native pending request re-queried before once/reject | Official 2.1.0 TUI loading/restart plus deterministic client reply fixtures; no real native permission from a model |
| Pi | Revision 2 start/tool/UI prompt/settled events; aborted is interrupted, settled is turn end only | Unsupported; terminal required | Official 1.3.0 TUI loading plus native event fixtures; no real model turn |

Not executed: real servers, remote CLI installation/upgrade, real user hooks,
real native approval against an authenticated model, device/production API or
APNs requests, iPhone display/badge/tap/terminal navigation, deployment, release
or tag. Official-host loading proves integration loading, not an end-to-end
model turn or phone approval. Codex trust/load, remote policy and receiving App
capability remain separately reported prerequisites. API revocation cannot
atomically recall a decision already handed to the native host. This is a first
implementation awaiting the one separately initiated static Review, not a Review
pass or field acceptance.

Local logs: `/tmp/mobterm-round1-cli-final-test.jsonl`,
`/tmp/mobterm-round1-cli-final-race.jsonl`,
`/tmp/mobterm-round1-cli-race-asdf-final.jsonl`,
`/tmp/mobterm-round1-cli-regressions-final.log`,
`/tmp/mobterm-round1-cli-focused-race.log`,
`/tmp/mobterm-round1-native-adapter-tests.log`,
`/tmp/mobterm-round1-opencode-host.log`, `/tmp/mobterm-round1-pi-host.log`.

## 2026-10-28 — Pairing TLS budget and safe timeout classification, round 1 v1.0

[Implementation and acceptance](https://app.notion.com/p/407cede208238185a7a5fa6455f8c297).
CLI analysis/start: `6fbdeb192073219f8ffb6e5652eafea535eb4665`, clean `main`.
App analysis/start: `acfd44e97cd36264b278fbe8bf905e903fa9bff5`; API analysis/start:
`0b0c582bb64956753efc3a401b77d6cdf6e13852`. Both remain unchanged on `main`.
No baseline drift. Tests below ran on this delivery's final code and test files;
only this verification record was added after the gates completed.

The API's owned standard transport now allows a 30-second TLS handshake within
its existing 30-second whole-request budget. It preserves default proxy/dial,
certificate validation, HTTP/2, connection pooling and redirect refusal behavior.
Typed network timeouts use the existing `timeout` code, with explicit cancellation
first. Body-read cancellation/timeouts preserve a cause behind fixed safe text;
ordinary invalid responses retain their prior behavior. The 65-second command,
35-second lease, shorter parent deadline and one-second persistence reserve remain.

Environment: macOS 15.8.1, darwin/amd64, Go 1.27.1, tmux 3.7c. Tests use temporary
stores, controlled transports and loopback TLS; no real remote server was touched.
Checks ran serially from the CLI root, without concurrent build/test jobs started
by this task. `-json` only records the required full test commands' output.

- Focused API, classification, setup/recovery, pair-status, budget and lease suite:
  `go test -count=1 -v -run 'Test(API|PairingTyped|PairingRun|SetupTyped|SetupLost|SetupReadback|NetworkAndLocal|OutboxLease|ExactRetirementFences|PairStatus)' ./...`:
  PASS, 34.165s. Initial run FAIL, 34.183s: a newly added whole-stdout assertion
  also covered the legacy status HTTP fixture's existing remote `error` field.
  Restricted the added assertion to the new typed/body failure cases; all existing
  assertions remain. No production behavior or timeout was relaxed.
- Strengthened the real body test to require `apiResponseError`, proving failure
  happened during body consumption, then ran
  `go test -count=1 -run '^TestAPIRealBodyCancellationAndDeadline$' ./...`:
  PASS, 1.400s. Full checks below include this final assertion.
- `test -z "$(gofmt -l .)"`: PASS.
- `go test -json ./...`: PASS, 329.266s package time (329.931s wall),
  115 top-level tests / 517 including subtests, zero failures/skips.
- `go vet ./...`: PASS (0.584s wall).
- `go test -race -json ./...`: PASS, 420.071s wall,
  115 top-level tests / 517 including subtests, zero failures/skips and no data race.
- `git diff --check`: PASS, including the final documentation update.

`TestAPIRealDelayedTLSHandshake` delays the server's ClientHello processing in
`GetConfigForClient` for 11 seconds, before TLS completes. It keeps the transport
constructed by `newAPI`, adding only the loopback certificate to a trusted root
pool (certificate verification is not disabled). The old-behavior branch changes
only the TLS limit back to ten seconds: in the full ordinary run it timed out at
10.002653s before the HTTP handler; production configuration succeeded at
11.004425s. The old error is a real Go typed timeout, not a context deadline.
The existing five-second HTTP handler delay test remains separate.

Coverage also includes simulated 29-second success / 31-second delay capped at
30 seconds, parent deadline minus one second, real headers-then-blocked-body
cancellation/deadline, typed body errors, ordinary read errors, invalid JSON,
oversize, late cancellation after a completed read, and safe stdout/stderr/failure
metadata for the new errors. EOF, certificate failure, ordinary transport errors
and HTTP 401/429/410 retain their categories. Recovery performs one claim and one
GET, accepts only confirmed active/matching installation with durable save,
preserves the original claim error and pending responsibility when unconfirmed,
and does not acknowledge success after revocation or a fenced local save. Existing
lease, cancellation, persistence and late-claim retirement regressions passed.

The prior timeout entry below retains its real failed runs. None of those asdf,
discovery or Claude retry failures recurred in either current full suite; this is
new execution evidence, not a claim that the historical runs were green.
Logs: `/tmp/mobterm-cli-v1-focused-final.log`, `/tmp/mobterm-cli-v1-test.log`,
`/tmp/mobterm-cli-v1-vet.log`, `/tmp/mobterm-cli-v1-race.log` and the wall-time/exit
summary `/tmp/mobterm-cli-v1-checks.json` on the execution host.

App/API compatibility was inspected, not rebuilt or tested this round: existing
App timeout/unavailable messages, absent/unknown/malformed failure fallback,
16 KiB output bounds, 75/90-second waits, and reuse of the resolved absolute CLI
path remain compatible. API claim/GET routes, credentials and response contracts
are unchanged. No new fields/enums/version gate or API deployment is needed.

The supplied curl and previously recorded Worker evidence remain distinct from
these local tests. The source defect is confirmed; the incident's unique cause
is not. Actual incident App build, CLI path/version/commit and underlying failure
were not obtained. No new Worker log query, release/tag, deployment, remote
installation/upgrade, Hook reinstall, real Pair operation, production request,
real sandbox pairing, APNs display or Jump-to was performed. The delivery is
source-level remediation awaiting the user's separately initiated static review
and any separately authorized field verification.

## 2026-10-28 — Notification API timeout

Notification API requests now allow 30 seconds. Network commands allow 65 seconds,
local-only commands retain ten seconds, and upgrade retains two minutes. Durable
outbox leases last 35 seconds so another invocation cannot take over a request
still within its 30-second network budget. Existing Agent hook caller deadlines
are unchanged and can cancel earlier. App SSH wait compatibility is described in
protocol-v1.md. No API wire format, remote installation, release or deployment
changed in this work.

Verification:

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`: PASS.
- Four focused timeout/budget/lease regressions: PASS (8.698s). They include a real
  local TLS response delayed five seconds, simulated 29-second success / 30-second
  request timeout, parent-deadline persistence reserve, local/network command
  selection, and a concurrent delivery attempt after 20 seconds that cannot steal
  a still-running 29-second request. Virtual time establishes timer semantics,
  not real Internet performance.
- After refining status flag parsing, the command budget regression passes both
  normally (2.008s) and with `-race` (2.906s), including pair values named
  `agents-json` and `installation-json`.
- `go test ./...`: FAIL (467.502s), during concurrent build/test load. Failures:
  existing five-second original-launcher prerequisites in
  `TestAsdfCompatibilityEntrypoints` / `TestAsdfProbeIsolationAndFailure`, discovery
  probe timeouts in `TestAgentDiscoveryContractAndNoInitialization`,
  `TestAgentDiscoveryMixedResults`, `TestAgentDiscoveryLocalTimeoutRetainsOtherAgents`,
  and one `TestClaudeWaitNotifyEncryptedRetries/permission_prompt/missing` fixture
  with two requests instead of four. The complete Claude retry test passes when
  rerun alone without source changes (21.809s).
- `go test -race ./...`: FAIL (599.180s), only the same original-launcher prerequisite
  and discovery contract/mixed-result timeout families; no race detector report.
  No timeout, lease or Claude regression failed in that run.
- A serialized focused race rerun covering all failure families plus the four new
  regressions: FAIL (180.451s). All `TestAsdfCompatibilityEntrypoints` cases and
  Claude retries recovered; `TestAsdfProbeIsolationAndFailure` and the three
  discovery tests still exceeded their unchanged five-second fixture budgets.
  The four new regressions passed. These remaining environment/fixture failures
  are recorded as a verification gap; the full suite is not claimed green.
- Final-source timeout/budget/lease regressions alone with `-race`: PASS (7.595s).

## 2026-10-27 — CLI recovery review fixes, round 2 v1.0

[Requirement](https://app.notion.com/p/406cede2082380ca9c90e8e3254aa415) ·
[Delivery](https://app.notion.com/p/406cede208238192a5b7cd8522463008).
Review and actual starting SHA: `0c2f99f33eb379d11774f8a9c49ca5eed30bec1c`,
clean `main`, no drift. The delivery page records the final commit and exact compare;
these checks cover that commit's production and test source.

F2 adds a same-handle content/mode reader while preserving `O_NOFOLLOW`, regular-file,
size and root checks. Existing v1 journal modes are checked both on initial recovery
and after ownership persistence, immediately before config replacement. A changed
before-state mode is a conservative conflict; an after-state file is never rewritten
for ownership/journal cleanup. No protocol, journal-format, native Hook, installer,
packaging, dependency or API change.

Go 1.27.1, darwin/amd64 on macOS 15.8.1. Isolated temporary Store/configuration,
existing local TLS/Agent/tmux fixtures; no real remote server or APNs operation.

- PASS: `go test -count=1 -run '^TestClassic' ./...` (9.323s). New
  `TestClassicJournalPreservesTightenedPermissions` covers Codex/Claude × install/
  uninstall × journal/ownership-written/config-written/ownership-completed (16 cases),
  actual chmod 0644→0600, v1 journal restart, read-only health, final mode/content/inode,
  ownership/journal preservation on conflict and idempotent after-state completion.
  Existing ordinary recovery, content/ownership edits, symlink, changed roots and
  old ownership tests remain intact.
- PASS: `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`.
- PASS: `go test -count=1 -json ./...` — 105 top-level tests, 476 passing test/subtest
  events, 0 failures/skips, package 296.136s.
- FAIL / interrupted, retained: initial `go test -race -count=1 -json ./...` overlapped
  the simulator run. Six existing Asdf launcher preflight paths hit their unchanged
  five-second deadline (`original launcher context is not usable signal: killed`):
  agent-home/{entrypoints,notify,chain,installation-only,one-probe} and
  agent-project-explicit/entrypoints. Eight failure events include parent summaries;
  56 pass events were recorded. The task's process group was interrupted at 121.289s
  to stop contention. No data-race report; this was not a passing full run.
- PASS, final isolated full race: after Xcode completed, the same
  `go test -race -count=1 -json ./...` — 105 top-level tests, 476 passing test/subtest
  events, 0 failures/skips, 388.635s, no data race. All initial failed paths passed
  at the original assertions/time budgets; no timeout or project-rule changes.

Local JSON logs: `/tmp/mobterm-cli-review-r2/{test,race,race-isolated}.json`.
They are local evidence, not shared links. Packaging/installers were unchanged and
not rerun. No tag/Release, deployment, real server cleanup/update or production APNs.


## 2026-10-27: CLI recovery round 1 v1.0

Analysis and implementation both started on clean `main` at
`3eae811e57e04ec543aa7d308845599fbf27725e`; no baseline drift. This round changes
only exact local Pair retirement, classic Hook transaction recovery, fixed health
recovery actions and the shared additive API contract. Go 1.27.1, Darwin/amd64,
tmux 3.7c; tests use isolated temporary stores/configuration, local TLS and
controlled process fixtures. No user server or deployed notification API is used.

`recovery_test.go` covers A6/A7 with 32 persisted pending credentials, an unaffected
active Pair, wrong origin/installation/frame/extra-secret fields, command receipt,
repeated invocation after lost receipt, absent-before-first-save retirement,
failed local storage and successful retry, terminal marker admission at 4096
without eviction, and a real TLS claim blocked before response while exact local
retirement commits. The late claim cannot activate the deleted Pair. Existing
reconciliation and outbox retirement tests keep their original authenticated
terminal/error distinctions. Active/pending limits remain 32 each.

A8/A9 persist both Codex and Claude operation journals and reopen the Store at
journal-only, ownership-written, config-renamed and ownership-completed stages,
for both install and uninstall. Fixtures include an existing nonempty Codex notify
chain and Claude user hooks with the same matcher. They check exact preserved
content, original file permissions, completed journal removal, repeated operations,
read-only health, rejection of the opposite pending action, real user edits,
symlinks, changed roots, corrupt journals and changed ownership (including a valid-JSON journal with a damaged ownership
fragment). Overlarge duplicated ownership is rejected before the first write,
so every committed record fits the normal reader. An old Codex
record can use its exact original fragment/argv to safely finish cleanup; old
Claude records without before-state evidence remain conflicts. Native interrupted
operations and `hooks:false` recovery actions remain covered. The journal stores
one bounded replacement plus hashes/ownership (private mode 0600), enabling exact
retry without inventing an unsafe reconstruction from edited configuration.

Initial checks and repairs:

- New fixture tests initially failed because a manually prepared journal's private
  Store directory had not been created. The fixture now prepares it explicitly;
  no production permissions or recovery assertions were weakened.
- `go test -run 'TestLocalRetire|TestRetirementMarker|TestClassic|TestLegacyCodex|TestRetireCommand' ./...`:
  PASS, 7.320s.
- `go test -count=1 -run 'TestLocalRetire|TestRetirementMarker|TestClassic|TestLegacyCodex|TestRetireCommand|TestConfigLifecycle|TestNative' ./...`:
  PASS, 14.562s after the final transaction/retirement helper changes.
- Initial `go test ./...`: FAIL, 322.266s, only
  `TestAsdfCompatibilityEntrypoints/node-project/notify` failed its existing
  five-second original-launcher prerequisite while Xcode compiled concurrently.
  The changed recovery paths did not fail. In the subsequent compilation pause,
  `go test -count=1 -run '^TestAsdfCompatibilityEntrypoints$/^node-project$/^notify$' ./...`:
  PASS, 8.626s. Original budgets and assertions are unchanged.
- `go vet ./...`, `test -z "$(gofmt -l .)"`, `git diff --check`: PASS.
- `python3 tests/install_test.py`: PASS, 25 tests in 229.562s, with one explicit
  SKIP (`test_real_packages` needs `--dist`/`--commit`). The 24 executed installer
  checks include checksum/download failures, signals before/after atomic move,
  readback, destination protections and preservation of unrelated staging files.
  Installer/packaging code is unchanged; no package publication was performed.

- Initial `go test -race -json ./...`: FAIL, 411.353s, 376 passing
  tests/subtests, zero skips, no race-detector report. The only failures were 13
  unchanged original-launcher five-second prerequisites during parallel Xcode/
  simulator work: all five entrypoints/notify/chain/installation-only/one-probe
  cases in `agent-project-explicit` and `node-home-explicit`, and the first three
  in `node-project`. The 17 failure records include their four parent summaries.
  Every other executed test, including the new recovery and existing seven-day
  112000-record outbox test, passed.
- In a coordinated window without API/Xcode/simulator tests,
  `go test -race -count=1 -json -run '^TestAsdfCompatibilityEntrypoints$/^(agent-project-explicit|node-home-explicit|node-project)$/^(entrypoints|notify|chain|installation-only|one-probe)$' ./...`:
  PASS, 91.552s, 81 tests/subtests, zero failures/skips. This includes every
  original launcher failure under the unchanged five-second budget.
- Final-source recovery/Hook/native/health/discovery/Pair checks:
  `go test -count=1 -json -run 'Test(LocalRetire|RetirementMarker|Classic|LegacyCodex|RetireCommand|ExactRetirement|Native|ConfigLifecycle|Codex|Claude|Structured|AgentDiscovery|NotifyChain|Reconcile|PairReconciliation|IndependentPair|SetupLost|DeviceRevocation|UnconfirmedDevice|OutboxConfirmed)' ./...`:
  PASS, 89.706s, 203 tests/subtests, zero failures/skips. This includes the final
  ownership-digest corruption and reader-size boundaries plus persisted-marker
  recovery before credential commit.
- The same final-source selection with `-race`: FAIL, 99.558s, 202 passing
  tests/subtests and one failed existing discovery prerequisite:
  `TestAgentDiscoveryContractAndNoInitialization` could not start its OpenCode
  probe script within the unchanged five-second budget (no probe-count file was
  created). All new recovery, retirement and Hook cases passed; no data race was
  reported. Immediately running
  `go test -race -count=1 -json -run '^TestAgentDiscoveryContractAndNoInitialization$' ./...`:
  PASS, 2.989s, unchanged assertions and budgets. Thus every failed path has a
  successful focused revalidation; neither full-suite invocation nor the combined
  final race invocation is represented as a single all-green run.
- Final `go vet ./...`, gofmt check and `git diff --check`: PASS.

No real remote SSH/jump-host transition, Agent host activation, production API,
APNs acceptance, phone display/navigation, release, deployment, remote migration
or real-user cleanup is claimed. CLI API fixtures do not prove D1 retention;
actual D1 timing/cancellation tests belong to the API repository. Installation
fixtures do not prove a real-server update. Public protocol documents the planned
migration/API/CLI/App upgrade order and rollback constraints, not an executed rollout.

## 2026-10-26: pairing confirmation and bounded failure diagnostics

Work started on clean `main` at
`afa9ec59cc3e5cc95832b6f05b21fc06b4fd4959`. The tests below use the task's working
tree on Go 1.27.1, Darwin x86_64 / darwin/amd64. They use temporary private stores,
local TLS or controlled HTTP transports and isolated Agent launchers. They do not
use the user's Debian account, deployed API, APNs, or real-device credentials.

Before changing production code, the new
`TestPairStatusSurvivesAuxiliaryDeadline` failed against that base:

```text
healthy pair lost to auxiliary budget: status=api_unavailable calls=0 err=API unavailable
--- FAIL: TestPairStatusSurvivesAuxiliaryDeadline (1.61s)
```

The fixture stores an active pair, serves a healthy local TLS API, and blocks a
real Agent version process on a FIFO until a 1.5-second command deadline. There
are no sleep-based synchronization guesses. The old order spends the budget
before making any API request. With the fix the same test passes: API GET runs
once, active remains successful, the probe reports timeout and outbox retains an
error. This reproduces a conditional CLI defect, **not** the user's original
Debian failure or a claim that healthy diagnostics always exceed a time/output
limit. Four Agent probes still run concurrently, not sequentially.

Focused tests also cover unavailable/timed-out/rejected API requests, revoked or
expired pairs, missing local pairs, local/remote identity mismatch, missing
identity/status, wrong pair and inactive state. Cancellation remains failure even
when an active body was received. Real `run` calls exercise malformed frames,
default-store and installation errors, identity changes, save failures, transport
errors, unknown/malformed responses, HTTP rejection/rate limiting and single-pair
status errors. They assert one JSON response, exit 1, safe fixed metadata, no
secrets in added diagnostics/stderr, and retention of existing status fields.
Existing repeated setup, lost-claim-reply reconciliation, storage safety,
installation-only and agents-only regressions remain in the full suite.

Executed checks:

- `go test -count=1 -run '^TestPairStatus|^TestPairingRun|^TestAgentDiscovery' -v ./...`:
  PASS; focused core failures and mode boundaries.
- `go test -count=1 -json ./...`: PASS, 92 top-level tests / 397 test and subtest
  pass events, zero skips, 290.741 seconds. The final missing-identity
  classification and metadata-scope refinements also passed focused reruns;
  the race run below covers their final integrated source.
- `go test -race -count=1 -json ./...`: the first full run overlapped a heavy
  sibling Xcode build and failed only existing `TestAsdfCompatibilityEntrypoints`
  launcher preflights (`original launcher context is not usable signal: killed`
  at their five-second fixture deadline). There were 318 pass events, zero skips
  and no race-detector reports; all new pairing tests passed under race.
- With Xcode stopped, `go test -race -count=1 -json -run
  '^TestAsdfCompatibilityEntrypoints$' ./...`: PASS, all 95 test/subtest events,
  zero skips, 116.771 seconds. No fixture/production timeouts or code were changed
  for the rerun. Across the full run and this complete failed-class rerun, all
  92 top-level tests / 398 unique test/subtest cases passed under race; no failed
  case remains unresolved. The initial full command itself is recorded as FAIL,
  not retroactively presented as a clean full run.
- `go vet ./...`, `test -z "$(gofmt -l .)"`, `git diff --check`: PASS.

The first pushed source, `bb0875ef1ab846401f8a7db3f4ce86f1586f328b`, exposed an
affected Linux black-box fixture in
[CI run 40344596591](https://github.com/simplekkj/mobterm-cli/actions/runs/40344596591).
`test_setup_https_repeat_failure_and_credentials_retained_on_hook_removal`
failed at the single-pair status call with exit 1 / `pair_status_failed`. Its
HTTPS Handler returned active plus installation identity but omitted `pair_id`;
the actual API `pairState` has always returned both identities. The preceding
base's [CI run 39908988546](https://github.com/simplekkj/mobterm-cli/actions/runs/39908988546)
passed because the old CLI synthesized the requested pair ID instead of checking
the API value. This was an affected test fixture, not evidence that the real API
omits the field. The fixture now returns `pair_id` and explicitly checks pair,
installation and local-save acknowledgement. Production success criteria and
Hooks behavior are unchanged by this follow-up.

Local follow-up verification: Python compilation and `git diff --check` passed;
executing the existing Handler's POST claim and GET status over loopback HTTP
verified both matching identities and active state. This payload check is not a
Linux HTTPS lifecycle pass. The full Python HTTPS fixture cannot run on this
Darwin host because it intentionally relies on Linux `SSL_CERT_FILE` loading;
no local Docker daemon was available. The subsequent Linux CI rerun outcome is
recorded in the task delivery report.

The public protocol remains v1 with the same six-field frame, successful setup
receipt and persistence format. Optional failure metadata is additive; old App
consumers can ignore it, and new Apps must still handle old CLI failures. No
API changes, releases/tags, Debian installation, production migrations or
notification delivery were performed. Source push does not update an installed
CLI. App/SSH integration evidence is recorded with the sibling App task.

## Linux Agent lifecycle and config preservation — 2026-10-14

Final source validation ran on a disposable Linode (`108108945`) in `us-lax`:
Ubuntu 24.04.4 LTS, kernel 6.8.0-134-generic, amd64, tmux 3.4, Node 18.19.1,
npm 9.2.0 and temporary 2 GiB swap. Provisioning reused the sibling App's
protected Linode configuration loader. Credentials stayed local; SSH pinned
the host key supplied through cloud-init, and the dedicated firewall admitted
only measured client IPv4 /32 sources.

Real Codex `0.162.1` was installed with npm `@openai/codex@0.162.1`; real
Claude Code `2.1.282` used the official `https://claude.ai/install.sh` installer
with that explicit version. Both actual version checks passed. Agent login
credentials were not transferred or used.

The tested binary includes the final working-tree hook fixes on base commit
`c6961ce769aebd03eb9eed375dad174b79fe022d`, built with Go 1.27.1 and
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, version `linux-e2e`. Its SHA-256 is
`c5463bef7f73f76098c53473aaf6947006ceeb4b0722eb2ad84df5ce2a96cdd1`.
This is a source-build result; the fixes have not been published.

| Check | Result |
| --- | --- |
| Linux `tests/linux_lifecycle_test.py --binary ./mobterm-cli -v` | PASS, 11 tests, no skips |
| Go test executable cross-built with the Linux target and executed on Linode | PASS, 30 tests / 51 including subtests, no skips; real tmux checks executed |
| Linux `tests/install_test.py --dist dist --commit <base SHA>` | PASS, 14 tests, no skips; actual installed Linux package executed |
| Local formatting, Go tests, vet and race checks | PASS on final Go source |
| Four-target package build, version `v0.0.0`, base SHA above | PASS; arm64 archives built/inspected, not executed |
| Shell syntax, Python compilation and final diff whitespace | PASS |

The black-box tests use disposable HOME directories, including spaces and a
single quote, plus default/custom config paths and actual Agent executables.
They cover stable installation identity, repeated bounded stdin setup over a
loopback HTTPS fixture, malformed/wrong-identity setup, credential permissions,
repeated hook installation/removal, original Codex notifier argv/payload/exit
codes, existing Claude Stop/Notification hooks, later user settings and hooks,
pre-existing identical commands, managed-entry edits, null/malformed settings
and symlink refusal. TOML restoration is checked byte-for-byte, Claude values
semantically, and existing config mode 0640 is preserved. Hook removal retains
pairing credentials. Both native Agents load their configuration with `mcp list`
before and after hook and executable removal.

These checks fixed Claude `hooks:null` panics and command-only ownership that
could remove existing or later-edited hooks. New records retain the entries
actually inserted. Changes to managed commands, timeout, matcher or metadata
cause a conflict without overwriting config. Legacy records without ownership
metadata now reject installation/removal and require manual review; both their
configuration and record remain byte-for-byte unchanged. See README for migration.

The unchanged installer also downloaded the actual public GitHub stable Release
`v0.1.0` twice into an isolated HOME, preserving existing Agent/Mobterm/shell
configuration. Both downloads produced executable mode 0755 and SHA-256
`88d93d30f5ae390068baf0935eadc88964bd507bbe992b967616dd16a0cc7d02`;
the actual executable reported `mobterm-cli v0.1.0` and the base SHA above.
Its hook behavior is separate from the fixed source build. The package matrix
uses controlled GitHub responses and local `v0.0.0` archives that include the
working-tree changes; base-SHA metadata does not imply a clean tagged release.

Sanitized logs and source hashes are retained under ignored `.cache/linux-e2e/`.
The final instance `108108945` and dedicated firewall `206363275` were deleted;
cleanup confirmed both exact IDs absent with HTTP 404. Earlier disposable
preparation/validation instances and their dedicated firewalls were also removed.
Setup/API responses and hook inputs are controlled fixtures; any fixture
`apns_accepted` response is not live APNs evidence. Native configuration loading
was real; authenticated model turns, native runtime notification events, live
API/APNs delivery and phone navigation were not tested.

## 2026-10-14: deployed API / temporary Linux CLI

Started from `2fabb095939f78a9675bd61e1e649b46530b2ab8` on clean `main`, identical
to the analysis baseline. No Go or protocol implementation changed in this task.
Built with Go 1.27.1:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags '-X main.version=push-r1 -X main.commit=2fabb095939f78a9675bd61e1e649b46530b2ab8' \
  -o dist/mobterm-cli-push-r1-linux-amd64 .
```

The actual binary ran on a new Singapore Nanode, Ubuntu 24.04, amd64; mode 0755,
SHA-256 `22898eba28de0d7d73bf3f2095fd40f208d120855700ee07486a45039ba4e5e9`.
First-use SSH trust was explicitly confirmed; subsequent connections pinned that
key. The App's selected ECDSA host key was also checked over that trusted SSH
channel. No verification was bypassed, no private key was copied to the server.

Separate sandbox and production synthetic-token probes used independent credentials
and pairs. `setup --stdin-frame`, durable local readback and API active status
passed; encrypted `test` reached the deployed Workers and returned
`permanent_failed / apns_invalid_token` as expected for those synthetic tokens.
Credentials/keys travelled only through the bounded SSH stdin frame. Synthetic
pairs were revoked; a revoked CLI send failed with `pair_revoked`. No public
Release or version tag was created.

A later real simulator token paired through the **App UI** to
`https://api-sandbox.mobterm.app`, used this Linux CLI for test sending and received
`apns_accepted`. The sandbox D1 readback recorded one attempt and no error; this
state requires actual Apple HTTP 200. The simulator lock screen displayed the test
notification. This is not physical-iPhone or production-delivery evidence.
Repeating the actual CLI test returned `apns_accepted` again. After App UI
revocation, CLI status read back `revoked` and another test failed with
`pair_revoked`; all three task pairs were revoked.

With real tmux 3.4, `target verify --stdin-frame` matched a deliberately inactive
pane and its complete socket/server/session/window/pane identity. Killing that
test pane made the same locator fail. The temporary tmux socket was removed after
this check. The App also connected to a separate test tmux workspace using real
Mosh 1.4.0. Codex and Claude were not installed/authenticated on this disposable
server; no real Agent native lifecycle event or end-to-end business Jump to was
verified. Synthetic event fixtures are not counted as those observations.

The temporary instance (ID `108093358`) was deleted as requested at
2026-10-14 03:18:59 UTC: DELETE 200, subsequent GET 404. Its deletion
evidence, cloud resource versions, final repository SHAs and remaining physical
checks are recorded in the
[implementation handoff](https://app.notion.com/p/3f9cede2082381f5b08ce58e98e075ce).
Local sanitized evidence lives in the sibling API repository's ignored
`.wrangler/push-r1/`; simulator screenshots/xcresult live in the App's ignored
`build/push-r1/`. Documentation checks: `git diff --check`. Existing historical
unit/race results below remain distinct from this task's actual Linux execution.

## Initial implementation — 2026-10-13

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

## mobterm-cli action — Round 1 v1.0 — 2026-10-14

Analysis and starting HEAD were both
`ec665c329cf1738a39e97061b86c8328fad144a9` on `main`, with a clean working tree
and no baseline drift. This round changes the two workflow definitions and
documentation only. The local checks below ran on macOS Intel (`Darwin x86_64`)
with Go 1.27.1 and actionlint 1.7.12 against the working-tree implementation.

| Executed check | Result |
| --- | --- |
| `actionlint .github/workflows/ci.yml .github/workflows/release.yml` | PASS |
| `sh -n install.sh` | PASS |
| `test -z "$(gofmt -l .)"` | PASS |
| `go test -count=1 -v ./...` | PASS, 30 top-level tests, no skips; real tmux test executed |
| `go vet ./...` | PASS |
| `go test -race -count=1 ./...` | PASS |
| `go build -trimpath -o mobterm-cli .` and `./mobterm-cli version` | PASS, `dev unknown darwin/amd64` |
| Execute the CI native-host verification step locally with the darwin/amd64 matrix values | PASS, uname and Go host/target agree; no Rosetta |
| `python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"` | PASS, four Linux/macOS amd64/arm64 archives |
| `shasum -a 256 -c checksums.txt` in `dist` | PASS, all four archives |
| `python3 tests/install_test.py --dist dist --commit "$(git rev-parse HEAD)"` | PASS, 14 tests, no skips; all archive layouts, metadata and installation bytes checked; native installed darwin/amd64 binary executed |
| `git diff --check` | PASS |

The fixture archives carry `v0.0.0` and the starting HEAD above; they include
working-tree documentation and are not tagged release artifacts. Local execution
does not establish native Linux amd64/arm64 or macOS arm64 success. The four
GitHub-hosted jobs use `ubuntu-24.04`, `ubuntu-24.04-arm`, `macos-26-intel` and
`macos-26`; their post-push run URLs and actual results are recorded separately
in the [round 1 handoff](https://app.notion.com/p/3f9cede2082381969f50d05578c15193).

Static workflow inspection and actionlint confirm the same-commit reusable CI
dependency `test -> build -> release`, branch/PR read-only permissions, and the
retained stable-tag, exact-commit, official-repository and no-overwrite guards.
The actual tag-triggered Release path and its failure/cancellation behavior were
not exercised: no tag, Release or deployment was created in this round. Signing,
notarization, Gatekeeper acceptance, public Release installation/upgrade and
physical phone delivery/navigation were not tested here. Earlier validation
records above describe separate tasks and remain historical evidence.

## mobterm-cli compatibility — Round 1 v1.0 — 2026-10-16

Analysis and starting HEAD are both
`45abeec25f1da108326d6c227b858cfe09d4d367`, clean `main`; no baseline drift.
The App direct consumer remains at
`efab5cd86313ba24ecf4967ed09fff390a46481f`: read-only inspection confirmed the
v1 dictionary fields and 16 KiB output boundary. App/API source, credentials,
wire encryption, locator identity, packaging and release workflow are unchanged.
The task's final commit and actual post-push CI results are recorded in the
[implementation handoff](https://app.notion.com/p/3fbcede2082381be9dacc40b98b386d0).

### Minimum decisions and evidence levels

[compatibility.md](compatibility.md) retains exact upstream commit IDs, package
URLs/digests and executable source locations. Codex >=0.100.0 stable follows the
first stable tag containing UUID submission IDs, including the native notify
path and preserved thread identity on resume; 0.99.0 still resets its counter.
Claude >=2.0.36 stable follows the actual notification-type construction and
matcher/dispatch boundary; 2.0.35 lacks the required field. This is not a choice
of the historical pins or the initially suggested candidates. Latest official
stable releases were checked on 2026-10-16 (Codex 0.163.0, Claude 2.1.301), then
fixed in the test manifest. Every intermediate version was not executed.

Versioned Claude packages' registry SHA-512 integrity and SHA-256 were checked.
A controlled probe of unmodified extracted construction/matcher functions passed
only the three configured notification types to a child process and retained
only session/event/type fields. This is source plus controlled-function evidence,
not an interactive Claude event. No real model service or user credentials were
used. Synthetic resume samples in Go likewise do not constitute a live Codex
resume observation.

Two real problems were found before final local verification: Claude 2.0.36's
1.4–1.5 second JavaScript startup exceeded status's former one-second budget;
its default-home `--version` bootstrap also migrated `.claude.json` metadata.
The shared probe now has a three-second limit, disposable home/config/cwd,
disabled updates/nonessential traffic and bounded child-pipe cleanup. It resolves
the same executable on PATH. The existing null/unknown shape and byte-preservation
assertions were retained; a regression explicitly models the native migration,
checks the user's file is unchanged and confirms disposable state removal.

### Final local checks

Environment: macOS Intel `Darwin x86_64`, Go 1.27.1, Python 3.12, Node and
real tmux on PATH. All Agent package installs use task-specific temporary npm
prefixes/caches. Agent executions use temporary HOME/config/cwd and no login.

| Command / scope | Actual result |
| --- | --- |
| `test -z "$(gofmt -l .)"` | PASS |
| `MOBTERM_REQUIRE_TMUX=1 go test -count=1 -v ./...` | PASS, 40 top-level tests; real tmux test executed, no skips |
| `go vet ./...` | PASS |
| `MOBTERM_REQUIRE_TMUX=1 go test -race -count=1 ./...` | PASS |
| `go build -trimpath -o mobterm-cli .` / `./mobterm-cli version` | PASS, `dev unknown darwin/amd64` |
| Python `py_compile` with bytecode in task temporary directory | PASS |
| `actionlint .github/workflows/ci.yml .github/workflows/release.yml` | PASS |
| `git diff --check` | PASS |
| Real current Codex 0.163.0 / Claude 2.1.301 configuration suite | PASS, all 10 macOS checks, final run 12.759s |
| Real minimum Claude 2.0.36 / current Codex 0.163.0 configuration suite | PASS, all 10 macOS checks, final run 38.289s; this does not validate Codex minimum |
| Historical Codex 0.162.1 / Claude 2.1.282 full macOS suite | FAILED locally: one copied CLI invocation timed out at 20s; not recorded as a full-suite PASS |
| Historical null-hook case alone | PASS, 0.727s; real native configuration load/removal case also passed in the full runs |
| Codex 0.100.0 native macOS preflight | FAILED, official npm and GitHub tar executables terminated with signal 9 before usable version/config output |

Configuration suite command (selecting the explicit pair shown above):

```sh
PATH="/tmp/TASK_PREFIX/bin:$PATH" python3.12 tests/linux_lifecycle_test.py \
  --binary ./mobterm-cli --config-only \
  --expect-codex-version CODEX_SAMPLE --expect-claude-version CLAUDE_SAMPLE -v
```

The suites test native `mcp list` configuration loading with installed hooks and
after hook/CLI removal, repeated install/uninstall, original notifier argv/exit
code, unrelated hooks/later edits, ownership, permissions, malformed configs and
symlinks. `--version` is provenance checking, not configuration-load evidence.
MacOS excludes the Linux loopback HTTPS fixture and does not change certificate
trust. The final Linux minimum/current endpoint results and eight historical
native-platform job results must be read from actual CI, not inferred here.

The local historical timeout was investigated with an owned fixture process:
a debug suite also blocked on `status --installation-json`, which bypasses
Agent probes; SIGQUIT ended that process with no Go stack/output. The isolated
null-hook invocation returned the expected unchanged-config error normally.
This supports a host executable-startup problem; it is not evidence that the
failed full suite passed. No system security setting was changed, no timeout was
raised to conceal the failure, and no assertion was removed. The separate Codex
0.100.0 GitHub macOS tar SHA-256 is
`8a2e3b8246b373c07b4ef44acd8e3c72aaa196ab7d9a66146936157704ec106b`.
Its local launch failure remains explicit; Linux endpoint CI is needed for the
minimum's real configuration evidence.

Go regressions cover numeric stable parsing, boundaries/middle/historical/current
and synthetic higher versions, malformed/ambiguous/prerelease/overflow output,
missing/nonzero/expired-context probes, one probe per operation, configuration
isolation, refusal preserving config/ownership/credentials, version-independent
uninstall, single bounded status JSON and installation identity without probing.
Range events traverse `run` through parsing, verified fixture target, encryption
and controlled HTTP. Same-event reentry/retries retain exact ID/nonce/ciphertext;
different turns (including synthetic resume boundaries) differ. Rejected versions,
fields and targets never send; Codex's original notifier still receives exact argv
and payload and retains its exit code. Existing Claude association/wait/filter/cache,
real tmux, Herdr, crypto and upgrade tests remain intact.

CI preserves all eight native OS/CPU jobs and required tmux execution. It reads
fixed historical samples from `testdata/compatibility-samples.json`; two focused
Linux jobs read minimum/current from the same manifest and execute the full
11-case real-Agent/loopback-HTTPS suite. Production policy/reference consistency
is tested. The same-commit release dependency and permissions are retained; no
release, tag, deployment, paid resource, live API/APNs or phone test was performed.
Sanitized local logs and upstream packages are in `/tmp/mobterm-compat.RC7sS3`,
not Git. Packaging/install checks remain in each native CI job; this task did not
repeat unrelated local installer/upgrade tests outside the required Go suite.

### Post-push native minimum follow-up

The first implementation commit is
`a1d676fdef3aee7cf15044f25e6b4448606a7e34`. Its Linux minimum/current
endpoint jobs passed in run
[38879959117](https://github.com/simplekkj/mobterm-cli/actions/runs/38879959117).
Because the local Codex 0.100.0 macOS launch failure remained unresolved, one
focused native macOS Intel minimum job was added alongside those two Linux
endpoint jobs. It runs the same ten real configuration checks with
`--config-only`; no existing job or assertion was removed. This is a response
to the observed failure, not a multiplication of the full historical matrix.
`actionlint` and `git diff --check` passed for this CI/documentation follow-up.
Final native minimum and whole-run results are recorded in the handoff after
reading the actual final-commit CI result.

### Native minimum probe-budget correction

The macOS Intel minimum follow-up in
[run 38880398633](https://github.com/simplekkj/mobterm-cli/actions/runs/38880398633)
exposed an actual failure: one Claude 2.0.36 hook-install probe returned
`probe_failed`. The native configuration-load case and the other nine-case
paths passed. The failure occurred close to the three-second probe deadline;
the original generic diagnostic did not distinguish timeout from other process
failures. It is not recorded as a passing suite.

The shared probe budget is now five seconds (still capped by the command
context), with an explicit `probe_timeout` diagnostic for deadline exhaustion.
Status starts the two independent probes concurrently so their budgets do not
consume the entire ten-second context before pair readback. There is no retry,
fixed delay, persistent cache, removed assertion or lifecycle timeout change.
The prior three-second statement above describes the first implementation.

After this source change, local gofmt, all 40 top-level Go tests with required
real tmux and no skips, vet, race, native build/version, actionlint and diff
checks passed again. The expired-context test now requires the timeout reason.
Real minimum Claude 2.0.36/current Codex 0.163.0 passed all ten native macOS
configuration checks again in 35.975 seconds. Final-commit native CI must verify
the correction, including the failing minimum macOS job; its actual result is
recorded in the handoff rather than inferred here.

## Compatibility round 2: launcher selection before isolation (2026-10-17)

Analysis/start SHA: `6982f37b1333a38e24bc4d45d86a16ec77c4425e`, clean
`main`; no baseline drift, checkout/reset/pull/rebase or changes outside CLI.
COMP-R2-01 preserves the existing minimums and all event/wire contracts.

The selection-sensitive fixture first establishes that the original HOME/project
context can launch the Agent. Before the fix,
`go test -count=1 -run '^TestAsdfCompatibilityEntrypoints/agent-project-explicit/entrypoints/codex/minimum$' ./...`
failed at the supported installation decision (2.848s). The same case passed
after fixing both the executable and runtime selection (4.093s). A fixture setup
error reading the sample manifest after changing cwd was corrected before this
reproduction; it is not evidence for the production bug. The corresponding
encrypted-notify minimum case also passed after the fix (9.076s).

New regressions exercise Agent shims and npm-to-Node shims, HOME/project choice,
default/explicit asdf data, competing global commands, space/quote paths,
encrypted notify/retry/turn identity, original notifier exits, installation-only
and single version execution. Migration sentinels and selection files are checked;
status probes use separate disposable homes without changing parent HOME/cwd/PATH.
Unsupported selection callbacks are rejected before running asdf in the real HOME.

The focused real test is `python3.12 tests/asdf_probe_test.py --binary BINARY
--work-dir NEW_TEMP_DIR` (one shell line). It builds asdf from fixed source
`cb72590e9f9691b6e651aecfe4b0c9334adff41b` (v0.20.2), fixes the official
Node plugin at `779c8dc84b3bdab38c2c80622d315c2c3267f74b`, verifies official
Node archive SHA-256 against release SHASUMS256.txt, and installs Node
22.22.2 / Claude 2.1.301 and Node 22.14.0 / Claude 2.0.36. It checks all eight
default/explicit-data, HOME/project, Agent/Node-shim combinations, real `mcp list`
configuration loading, repeated hook lifecycle and configuration/state protection.
It uses no real user config, login or model service. CI adds this only to the
existing Linux minimum job; the eight native and three endpoint jobs remain.

Local environment: macOS Intel, Go 1.27.1, Python 3.12.15, Node 22.22.2,
required real tmux. Initial `go vet ./...`, native build/version, Python syntax,
actionlint and `git diff --check` passed. Expanded checks are still required for
the final commit; their final results belong in the handoff report.

Local startup failures are retained, not passing evidence: the first real-asdf
attempt timed out launching `asdf reshim` at 60s; the next timed out launching
the isolated Node/npm installation at 180s. Even independent `go version`,
`gh --version` and gofmt processes intermittently failed to start before their
deadlines with no output. The initial expanded fixture run was stopped with
SIGQUIT after its preflight child stalled; the stack was waiting in Cmd.Output.
The preflight now has a bounded context. A current real-Agent configuration
rerun passed nine of ten cases but one copied CLI hook-install process timed out
at the unchanged 20s limit. No assertion, production deadline or system security
setting was weakened. Clean final-commit CI, including the real asdf step and
minimum/current configuration runs, must succeed before closing the task.

Local evidence/packages: `/tmp/mobterm-r2.2vskUV`, outside Git. Native model
interaction/resume, live API/APNs, phone display/navigation and publishing were
not performed. Configuration and synthetic HTTP tests do not establish those.

## Miscellaneous optimizations — round 1 v1.0, R8 (2026-10-17)

Analysis and starting CLI SHA were both
`cdbfdc989d1451c8adb66f7e7f55adf8b0f33477`, clean `main`, with no drift.
This round adds `api_retry_test.go` and the daily-admission/429/503 contract in
`docs/protocol-v1.md`; CLI runtime code, encryption vectors, packaging and upgrade
behavior are unchanged. Final commits and cross-repository acceptance evidence
are recorded in the [round 1 handoff](https://app.notion.com/p/3fccede2082381019e3ae4bd36ae9876).

Local environment: macOS Intel, Go `go1.27.1 darwin/amd64`, tmux 3.7c.
The new HTTP fixture returns 429 `notification_global_daily_limit` with
`Retry-After: 86400` inside the existing ten-second command context. The CLI
returns in under one second, sends exactly one POST and preserves the supplied
event ID, nonce and ciphertext. It creates no next-day sleeper, daemon or queue.
Existing same-envelope retry/cache tests remain unchanged.

| Executed command / scope | Actual result |
| --- | --- |
| `test -z "$(gofmt -l .)"` | PASS |
| `go test ./...` | PASS, 96.048s |
| `go vet ./...` | PASS |
| `go test -race ./...` | FAIL, 146.732s; existing synthetic launcher preflight timed out in two `agent-home` cases |
| `go test -race -count=1 -v -run '^TestAsdfCompatibilityEntrypoints/agent-home' ./...` | FAIL, 29.714s; existing preflight failed in notify/chain/installation-only/one-probe |
| `go test -race -count=1 -v ./...` | FAIL, 144.382s; `TestAsdfCompatibilityEntrypoints` and `TestAsdfProbeIsolationAndFailure` failed at original-launcher preflight; no skips or data-race warnings |
| Targeted R8 race command below | PASS, three tests, no skips, 4.184s |
| `git diff --check` | PASS |

```sh
go test -race -count=1 -v \
  -run 'TestSendDailyRetryAfterExitsWithinCommandBudget|TestSendBoundedRetriesAndStage|TestRetryReusesExactEnvelopeAcrossInvocations' ./...
```

The final full race run passed the new daily-retry test and the live tmux test
(the latter took 1.07s). Its failures reported
`original launcher context is not usable signal: killed` at the existing
five-second fixture preflight, before the production Agent probe. They were
not race-detector findings. Concurrent Xcode compilation and Simulator startup
were observed, but CPU contention is an inference, not an established cause.

To distinguish these failures from R8 changes, the unmodified starting SHA above
was exported with `git archive HEAD` into a separate temporary directory. There,
`go test -race -count=1 -v -run '^TestAsdfCompatibilityEntrypoints/agent-home/installation-only$' ./...`
reproduced the identical preflight failure in 5.16s (8.246s overall, exit 1).
This establishes an unrelated baseline/environment failure; it does not turn
the required full race check into a pass. No fixture, timeout, assertion, runtime
or system security setting was changed to suppress it. The targeted R8 race
checks provide narrower evidence for the affected request/retry path.

Sibling API validation used Node 22.22.2, pinned Miniflare/workerd, real local
D1 and a controlled APNs response. Its full 42-test run passed with no skips;
final public-validation/protocol checks passed three tests and an additional
policy/APNs completion race passed one. Atomic last-admission contention,
UTC-boundary Retry-After, idempotence, management availability, migration
preservation, indexed cleanup, 40-round expiry load and D1 row costs are recorded
in the API README and handoff. These local fixtures do not establish live edge
limiting, real APNs acceptance, phone display or navigation. Old CLI runtime
behavior already supports bounded exit with the new API; no forced simultaneous
CLI release is required.

Sanitized local evidence is under `/tmp/mobterm-r1-evidence/`: `cli-test.log`,
`cli-race.log`, `cli-race-focused.log`, `cli-race-final.log`,
`cli-baseline-race.log`, `cli-r8-race.log` and `r8-report.txt`. Temporary paths
are local evidence, not committed artifacts or hosted CI results. No packaging,
release, deployment, remote D1, WAF, Secrets or live APNs operation was performed
for this round. Earlier records above describe separate tasks.

## 2026-10-20 — OpenCode/Pi, round 1 v1.0 (WF-1.0)

First implementation; delivery awaits one external static review. CLI analysis
and start SHA: `4d61af34b59ca0d090a3d12887a25d17ee9ce6a7`. App was checked at
`3f77ce3f34d6fbb7cd6b320d3c4048d6c9717061` (its prior pairing fix is retained).
API remains unchanged at `64170845c1c08b9f0ef369771fe2ff7773e256c7`.
Final commits and exact task ranges are recorded in the
[shared handoff](https://app.notion.com/p/3ffcede2082381eeaae8c70fb0befd2b).

Environment: local macOS Intel, Go 1.27.1, Node 22.22.2, real local tmux available.
Test CLI was built from this working tree with `go build -trimpath`; its default
version is `dev`, commit `unknown`, and it was used only in disposable local
HOME/config/package-prefix directories. Packaging used `v0.0.0`, never a release.
No user remote host, real model, production API/D1 or APNs credential was used.

Executed checks:

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`: PASS.
- `go test -count=1 ./...`: PASS (206.037s), including real local tmux,
  original golden vector, legacy source semantics/ownership, native metadata,
  version isolation, native asset transactions and encrypted retry/terminal scope.
- `go test -race -count=1 ./...`: PASS (146.319s). Earlier full/race attempts
  hit the unchanged five-second asdf fixture preflight timeout during very high
  local load; the final runs pass without relaxing the production or test timeout.
- `node scripts/test-native-adapters.mjs`: PASS; controlled host API fixtures
  execute generated adapter logic, not authenticated model events. Covers root /
  child / unknown / orphan ownership, pending and resolved requests, route changes,
  reload and two TUI instances, Pi settled/cancel/session/mode boundaries, bounded
  queue and cleanup. Only whitelisted metadata reaches the recorder.
- `scripts/test-native-adapters-host.py` with each actual official endpoint:
  OpenCode 2.0.0 and 2.1.0 PASS actual TUI setup + restart (nine listeners,
  `channel:latest`); Pi 1.1.0 and 1.3.0 PASS official discovery/TS imports/registration
  and actual TUI `session_start` (`mode:tui`, four listeners). A temporary wrapper
  observes registration around the unmodified generated adapter; this does not
  prove real `succeeded`, permission/form or `agent_settled` model activity.
- `python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"`:
  PASS all Linux/macOS amd64/arm64 single-binary archives and checksums. Assets
  are embedded with Go `embed`; no installed sibling source tree is required.
- `python3 tests/install_test.py --dist dist --commit "$(git rev-parse HEAD)"`:
  PASS 25 tests (229.499s), including native packaged binary version smoke test.
- API read-only: `npm run typecheck`,
  `node --test --test-timeout=120000 --test-concurrency=1 tests/protocol.test.mjs`:
  PASS (two protocol tests). Original Go/API golden vectors remain identical.
  Additionally actual new-source CLI `notify` requests to a local test receiver
  were passed to unchanged API `envelope` / `apnsPayload`, then decrypted using
  the existing `mobterm/1` AAD: both OpenCode/Pi PASS. This is local acceptance
  and cryptographic compatibility, not a real APNs or phone observation.
- CI compatibility's existing three endpoint jobs now install and load both new
  sources; native eight-runner historical Codex/Claude matrix stays unchanged.
  YAML parsed with Ruby's YAML parser. See exact final CI links in the handoff;
  local checks do not imply every hosted runner already passed.

Official host assets used:

- OpenCode 2.0.0: npm `@opencode/cli-darwin-x64-baseline@2.0.0`,
  [registry tarball](https://registry.npmjs.org/@opencode/cli-darwin-x64-baseline/-/cli-darwin-x64-baseline-2.0.0.tgz),
  registry integrity `sha512-X7+LzkxWQpwmY1wJz8OPnOVGNI9NjlYZIyiGM/WN6I4x88clrcGT4q++2U0M1frYLXuoEjlIPZ/RE9r8sAv1Vw==`.
- OpenCode 2.1.0: [official Intel archive](https://opencode.ai/files/bin/2.1.0/opencode-darwin-x64-baseline.zip),
  SHA-256 `2dbdf0d21cd9691142e582125eb2c01b7e653facb584d412d3ffc3d972679beb`,
  matched against the [fixed official tap](https://github.com/anomalyco/homebrew-tap/blob/6754df9897acc1a92eef20fcde2642eb09a9d38a/opencode-v2.rb).
- Pi 1.1.0: [official package](https://registry.npmjs.org/@earendil-works/pi-coding-agent/-/pi-coding-agent-1.1.0.tgz),
  registry integrity `sha512-SeEi/4hdcHNgA9UWlefZl7ZZpm3dzi2OoxNjDHsBJ9o298LNOtbL4DGKgitlEj6uCTccvtw6f2hlCkTPVJ2RXg==`.
- Pi 1.3.0: [official package](https://registry.npmjs.org/@earendil-works/pi-coding-agent/-/pi-coding-agent-1.3.0.tgz),
  registry integrity `sha512-V/WoCjA2hQSe3QLWIUB24cTRdtEdeLiIzkDqU5RMXV6VWuD+KGZnzVBMhglwdnQTySkehZsjt8a3g6Jd2g4nnw==`.

Pi 1.1.0's published caret dependencies initially resolved newer same-repository
packages and failed before extension loading (`MCP_TYPESCRIPT_PREAMBLE` import).
The endpoint fixture pins coding-agent/chord/pi-agent-core/pi-ai/pi-codemode/pi-mcp/
pi-tui together to 1.1.0; CI does the same for each endpoint. npm verified registry
integrity; no project/user Agent installation was changed. The tested minimum
fixture's telemetry dependency resolved 1.3.0. An initial cold OpenCode version
probe timed out locally; subsequent original-budget probe and installation/TUI
runs passed. No version-policy fallback or timeout widening was added.

Not executed: paid/authenticated model-native completion/wait rounds, real iPhone
notification display/tap, live APNs delivery, user SSH/Mosh/Herdr network end-to-end
navigation. Existing local target and crypto tests are distinct evidence. Pi
`needs_input:false` and OpenCode's visible-root-family-only scope are intentional.
The latest official CLI Release checked during delivery is
[v0.1.4](https://github.com/simplekkj/mobterm-cli/releases/tag/v0.1.4); it does not
contain this task. Commit/push does not publish a Release, and the App updater
can therefore still install a CLI without these new management capabilities.

Final focused check after adapter-only cleanup:
`go test -count=1 -run 'TestNative|TestCompatibilitySamplesMatchPolicy|TestStableAgentVersions' ./...`
PASS (5.756s). Final controlled bridge cases additionally cover missing CLI spawn,
8 KiB identity rejection and unknown/prerelease events. Source inspection resolved
an event-order concern without introducing deferred work: both OpenCode endpoints
pin `@solid-primitives/event-bus@1.1.2`; its global reducer dispatch occurs before
typed plugin handlers, so the official pending stores are updated first.

## 2026-10-21 — OpenCode/Pi, round 2 v1.1 (WF-1.0)

Review-fix baseline and actual start: `a2d7de5a9c26d06f60934bedd582427f2ff92b49`,
branch `main`, clean worktree. The original analysis SHA remains
`4d61af34b59ca0d090a3d12887a25d17ee9ce6a7`; this round changes only R1/R2 and
their verification. App R3 starts at `8f77a2967b50bd64861e76e2bbc614ed22afc2a5`.
API remains unchanged at `64170845c1c08b9f0ef369771fe2ff7773e256c7`.
Final commits, exact repair compares and closure are recorded in the
[shared handoff](https://app.notion.com/p/3ffcede2082381eeaae8c70fb0befd2b).

R1 fixes Pi package selection before probe HOME/cwd isolation. The new regression
subprocess reads the selected package.json VERSION in 24 status/install cases,
including tilde, relative/absolute/file-URL paths, literal spaces, empty/unset
overrides and invalid/low/prerelease packages. It checks isolated child config,
unchanged parent environment and unchanged files in the caller's default/configured
Pi directories and managed-install directory. The focused test passes (12.673s).

R2 gives each OpenCode activation an owned Solid root and releases it plus earlier
native listeners on cleanup or partial setup failure. Both adapters distinguish
intentional cancellation from failures without changing event/metadata contracts.

Executed native-adapter verification:

- Install the test-only pinned runtime with
  `npm ci --prefix scripts/native-adapter-tests --ignore-scripts --no-audit --no-fund`,
  then `node scripts/test-native-adapters.mjs`: PASS. The fixture and adapter share
  the explicit Solid 1.9.15 client module (both fixed OpenCode endpoints pin this
  version), never Node's no-op SSR export. Five same-router setup/cleanup cycles,
  repeated cleanup and partial setup exceptions prove old activations stop reading
  the router while new effects run. No fixture manually clears observers. Existing
  pending/route-away-and-back/reload deduplication/two-TUI/Pi event tests remain.
  Real local children prove route and Pi session cancellation stay silent, later
  nonzero exit still warns, spawn failure is bounded and the original ten-second
  timeout kills the child with one diagnostic. Controlled events are not model events.
- `scripts/test-native-adapters-host.py`: PASS OpenCode 2.0.0/2.1.0 actual TUI setup
  and restart (nine listeners each), Pi 1.1.0/1.3.0 official discovery/TS import and
  actual TUI session_start (four listeners, mode=tui). These use CLI-generated assets.
  The same four isolated official distributions listed in round 1 were reused after
  checking installed versions and registry tarball/SRI records; the 2.1.0 archive
  SHA-256 again matches the fixed official tap. This is distinct from the same-process
  Solid lifecycle test and does not prove authenticated model events.
- Actual Pi 1.1.0 and 1.3.0: ten combinations of tilde/relative/absolute/file-URL/
  space-containing package overrides pass direct --version, CLI status and
  hooks install/uninstall. Missing overrides reject installation without creating
  an extension. No user Agent, Node, configuration or global installation changed.
- Old baseline binary install -> repaired binary at the same absolute CLI path:
  PASS for both adapters, including owned-asset update, idempotence, refusal to
  overwrite a user edit, uninstall, unrelated JSONC/plugin/settings preservation.
  The local harness was corrected to use 0700 config directories and compare the
  preserved user content rather than require an optional trailing comma after uninstall.
- `scripts/package.py --version v0.0.0 --commit <HEAD>`: PASS four standalone
  Linux/macOS x amd64/arm64 archives and checksums. Existing installer
  `InstallerTests.test_real_packages` PASS (6.066s), including native package smoke.
  These are local validation packages, not a tag or Release.
- API read-only `npm run typecheck` and the existing `tests/protocol.test.mjs`:
  PASS (2 tests, no skips). CLI/API golden files are byte-identical and App's
  embedded vector matches. Public envelope, AAD and policy consumers are unchanged.

Environment remains macOS Intel, Go 1.27.1 and Node 22.22.2. An initial full Go
run hit unchanged five-second launcher/probe timeouts while a temporary simulator
was booting; Codex configuration fixture failures followed its first timeout.
An initial R1 focused run and the first upgrade-harness probe also timed out.
No production timeout or assertion was relaxed. Final serial verification:

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`: PASS.
- `go test -count=1 ./...`: PASS (107.774s).
- `go test -race -count=1 ./...`: PASS (148.548s).

No live model, production API/APNs, iPhone notification display/tap or user remote
SSH/Mosh/Herdr navigation was executed. Release remains v0.1.4 at this check and
does not include these adapters; source push is not publication through the App
updater. No Release/tag/deployment or second external review is part of this round.
Local detailed evidence is in `/tmp/mobterm-r2-host-evidence/` and
`/tmp/mobterm-r2-*.log`; this committed summary is the shareable evidence.

## 2026-10-21 — Agent Jump to reliability, round 1 v1.0

First implementation for the [shared implementation and handoff](https://app.notion.com/p/400cede20823814fb0a4f27f76ec37d7).
CLI actual start and analysis SHA both equal
`711bd59ffd21364783fb1508b818ac60abec4588`; branch `main`, initially clean.
No CLI analysis drift needed resolution. Checks below use the changed working
tree on that baseline; local package metadata uses the baseline SHA and does not
claim a clean tagged release. Final delivery commits are recorded in the handoff.

Environment: macOS 15.8.1 (24H32), Intel amd64, Go 1.27.1. Tests use isolated
configuration, loopback HTTP/HTTPS fixtures, owned temporary tmux sockets and
subprocesses; no production service or user Agent configuration is changed.
The current suite contains 80 top-level Go tests (subtests are additional).

Executed checks:

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`: PASS.
- `go test ./...`: PASS (271.717s). Follow-up changes described below were
  covered by focused race checks and the subsequent complete race runs.
- `go test -race -run 'TestReconcileLate|TestPairReconciliation|TestSetupLost|TestIndependentPair|TestTargetStructured|TestStructuredCodex|TestOutboxMigration|TestLegacyNativeReplay' ./...`:
  PASS (12.376s), including the final concurrent pair-state guard.
- Focused outbox and pair verification: PASS (56.452s); additional focused
  compatibility/health/target checks passed.
- `python3 -m py_compile tests/linux_lifecycle_test.py`: PASS. Its controlled
  active response now includes installation identity and event acknowledgement
  includes the matching event ID; the full Linux lifecycle harness was not run
  in this macOS round.

Evidence in committed tests:

- `health_test.go` exercises Codex TOML argv, original notifier, config-root
  drift and fake markers; Claude missing/moved/duplicated/modified matcher,
  timeout and command; OpenCode/Pi ownership, missing assets and safe repair.
  A marker inside a user's multiline TOML value remains byte-preserved.
- `outbox_test.go` exercises old 202 without handoff, 202/429/503 with handoff,
  mismatched acknowledgements, terminal precedence, original-envelope retry,
  twelve-attempt and Retry-After/expiry bounds, no-rePOST receipt 404, journal
  recovery, legacy migration, read-only status and missing-shard refusal.
  Eight concurrent callers and two real CLI-test subprocesses contend on the
  durable delivery lease; only the owner sends the reserved attempt.
- Seven-day capacity case: 32 devices x 500 records/day x 7 days = 112,000
  retained terminal records, written through the production sharded writer and
  index. A new process reads all 112,000; the index reports zero ciphertext
  bytes for these terminal records. This covers the promised tombstone retention
  load, separately from the 512 pending/pair and 16,384 global pending bounds.
  No pending or retained terminal record is evicted to admit a new record.
- `pairs_test.go` covers independent active/pending limits, explicit abandonment,
  lost claim reply recovery, authenticated expiry, unknown remote state, and
  blocked GET -> abandon -> delayed active/revoked reply. Snapshot comparison
  under the same lock prevents stale reconciliation from restoring credentials
  or changing the user's abandonment history. Setup and outbox auth transitions
  use the same conditional-write mechanism.
- `target_test.go` uses a real local tmux server and real Darwin boot/process
  birth data: same-session pane movement resolves, strict verify rejects the old
  location, respawn retaining pane ID rejects, cross-session movement rejects.
  An injected same-PID/birth-change race rejects; Herdr socket fixtures cover
  terminal movement and duplicate candidates. Structured errors distinguish
  old identity capability, installation replacement, definitive invalid target
  and unavailable query.
- `protocol_extended_test.go` reproduces the extended encrypted v1 fixture with
  optional agent and strong tmux occupant; the original golden fixture is
  unchanged. API and App consumer results are recorded in their own validation
  records and the shared handoff.

Initial failures and corrections: existing event fixtures still supplied the
old five-field tmux query or assumed two immediate POSTs. They now provide an
actual process PID and trigger the durable retry through explicit flush, keeping
original-envelope, independent-event and target-identity assertions. A long
`testing.T` temporary path exceeded the UNIX socket limit; real tmux tests now
use their own short temporary socket and clean up only that server. Target error
JSON initially serialized a typed nil map as null; it now emits the fixed
versioned error object and the regression passes. Legacy native callbacks are
matched to their original cached locator identity before stable move-aware
identity is used, including after expiry, preventing an upgrade from generating
a fresh event for an old callback. Missing Codex entries are restored without
stripping marker-like text from user TOML values.

The first full race run failed after 424.373s only in four existing
`TestAsdfCompatibilityEntrypoints/node-home-explicit` launcher probes at their
unchanged five-second timeout (`signal: killed`); no data race was reported.
The second complete `go test -race ./...` run failed after 382.839s only in
`agent-project-explicit/notify` and `/chain` at the same original launcher
five-second timeout; no other failures were reported and neither run reported a
data race. The failing preflight executes the original Agent launcher with
`--version` and `FIXTURE_PREFLIGHT=1` before invoking the CLI under test
(`compatibility_test.go`); it is not a failure inside the changed CLI `run()`. Complete race execution therefore did not finish entirely green in this
environment. Instead of repeating the full suite indefinitely, focused runs use
the original budgets:

- `go test -race -run 'TestAsdfCompatibilityEntrypoints/node-home-explicit|TestOutboxConfirmedPairRetirement|TestReconcileLate' ./...`:
  PASS (20.931s), including all initially failing node-home launcher cases and
  the final expired/revoked history and concurrent abandonment regressions.
- `go test -race -run '^TestAsdfCompatibilityEntrypoints$/^agent-project-explicit$/(notify|chain)$' ./...`:
  PASS (39.412s), covering both failures from the second full run.

No production timeout or assertion was relaxed.

`python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"`:
PASS; produced and verified all four Linux/macOS x amd64/arm64 standalone
archives, executable mode and metadata. `shasum -a 256 -c checksums.txt` from
`dist/`: PASS for all four archives (an initial invocation from the repository
root could not locate the checksum file's relative archive paths; rerunning in
`dist/` fixed the invocation). The existing focused installer smoke test below
passed, 1 test with four target subcases in 3.596s, including native installed
Darwin/amd64 binary execution and the expected version/base SHA. Other binaries
were installed to isolated fixture paths and inspected, not executed.

```sh
python3 - <<'PYTEST'
import importlib.util
from pathlib import Path
import subprocess
import unittest
spec = importlib.util.spec_from_file_location('install_test', 'tests/install_test.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
module.DIST = Path('dist').resolve()
module.COMMIT = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
result = unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite([module.InstallerTests('test_real_packages')]))
raise SystemExit(not result.wasSuccessful())
PYTEST
```

Not executed: Linux runtime /proc identity checks (Linux targets are
cross-compiled), arm64 execution, actual Herdr application navigation, paid or
authenticated model events, production API/APNs delivery, real iPhone display/
lock-screen preview/tap, or user SSH/Mosh end-to-end navigation. Native adapter
resources and their event schemas did not change; actual OpenCode/Pi host-loader
runs from earlier rounds are not claimed as rerun evidence for this round.
Local fixture API states do not prove APNs acceptance or phone display. No
release, tag, deployment, or production configuration change is included.

## 2026-10-23: read-only Agent discovery (round 1 v1.0)

Source work started on `main` at
`1139b3d2f1aa5a6059c2f856893df3c08158fbfb`, matching the analysis baseline.
Validation uses Go 1.27.1 on Darwin/amd64 and temporary local Agent executables,
HOME/configuration roots and state directories. No release, remote installation,
user remote configuration, live notification API or APNs operation is part of this run.

`discovery_test.go` executes the public `status --agents-json` command and checks
that an absent Mobterm store stays absent, existing identity/pair/outbox bytes
remain unchanged, notification HTTP requests stay at zero, each Agent is probed
once and disposable probe directories are removed. It tests exclusive flags,
mixed available/missing/old/failed results, unknown/prerelease versions,
permission/Node/Pi-package failures, cancellation, the real five-second per-Agent
timeout, partial-result preservation and removal of owned hooks without a runtime.
Existing asdf/isolation, compatibility, lifecycle, health, native integration and
protocol tests continue to exercise the unchanged command and ownership gates.

`testdata/agents-discovery-v1.json` was emitted by a locally built modified CLI
against four isolated supported-version scripts. The Go test compares actual
command output semantically with this same cross-client response sample. It is a
fixture, not evidence that real Agents loaded integrations or delivered messages.

An initial suite found that a new cancellation rejection string changed the old
contract; the implementation now retains `probe_failed` while exposing the typed
`probe_status:cancelled`. The focused discovery/asdf regression suite then passed
(11.117s). A subsequent full run (316.852s) and one focused retry hit the unchanged
five-second deadline before a fixture shell started. During those runs the host
was compiling the App and booting an iOS Simulator; observed load averages reached
186.92 / 155.57 / 76.89. After coordinating a quiet window, the exact failed test
passed unchanged (test 1.58s, package 2.498s). Neither the production timeout nor
assertions were weakened.

Final-source checks after the environment settled:

- `test -z "$(gofmt -l .)"`: PASS; only changed Go files were formatted.
- `go test ./...`: PASS, 271.198s.
- `go vet ./...`: PASS.
- `go test -race ./...`: PASS, exit 0; package 352.523s, wall time 404.359s
  including compilation.
  The earlier race process was interrupted by an agent-server restart before a
  final result could be confirmed, so only this unconfirmed check was rerun.
  Its output and exit/elapsed record are retained locally in ignored
  `.cache/agent-discovery-v1/race-final.log` and `race-final-result.json`.
- `git diff --check`: PASS.

Real Agent runtime activation, real SSH/jump-host selection, notification API/APNs
delivery, physical-device display and Jump-to were not tested by these local
fixtures. The discovery addition does not change event types, encryption vectors,
pair policy or API contracts. No packaging/release gate is needed for this change.

## 2026-10-27: device registration lifecycle (round 1 v1.0)

Source work started on `main` at
`8bc77d7da9234eabb14a2cade16afc748a84fd74`, matching the analysis baseline.
This CLI change updates the public lifecycle contract and compatibility tests;
CLI runtime, hooks, dependencies, encryption envelope/AAD and golden vectors are
unchanged. Test fixtures use isolated temporary local stores and loopback HTTP/TLS
servers with Go 1.27.1 on Darwin/amd64; tmux 3.7c is available.

`TestDeviceRevocationReconcilesOnlyAffectedPairs` checks both authenticated
`status:revoked` and HTTP 410 `pair_revoked` responses for two pairs belonging to
one retired device, an unaffected third pair, durable credential/key removal,
digest-only history and another invocation reopening the store. The CLI fixture
does not simulate D1 cleanup: actual sender authentication and retained Pair
terminal responses across cleanup are the API repository's local D1 responsibility.
`TestUnconfirmedDeviceRetirementRetainsPairCredentials` covers reconcile and send
with 401/403/404/503, HTTP timeout, unrecognized 410 and mismatched status/code;
these responses keep the original sender token, key, origin and installation and
do not retire another device. The existing setup regression now also verifies
`pair_revoked` during claim; existing lost-claim, late-result and outbox retirement
checks run unchanged.

Checks on the modified source above:

- `go test -count=1 -run 'TestPairReconciliationTrustAndLostClaim|TestReconcileLateResultCannotReviveAbandonedAttempt|TestSetupLostReplyAndConfirmedExpiry|TestOutboxConfirmedPairRetirement|TestDeviceRevocationReconcilesOnlyAffectedPairs|TestUnconfirmedDeviceRetirementRetainsPairCredentials' ./...`:
  PASS, 11.406s. An initial new timeout fixture stalled its HTTP server shutdown
  because it had not consumed the POST request body; the fixture was corrected
  and the suite rerun without changing production timeouts or assertions.
- `test -z "$(gofmt -l .)"`: PASS.
- `go vet ./...`: PASS.
- `go test -json ./...`: PASS, exit 0; 417 tests/subtests, zero skips or failures.
  Test process 277.356s; package command elapsed 334.262s including tool cleanup.
- `go test -race -json ./...`: first run FAIL, exit 1, 403.087s; 352
  tests/subtests passed and none skipped. Eight pre-existing launcher fixture
  prerequisites in `TestAsdfCompatibilityEntrypoints` exceeded their unchanged
  five-second timeout while the App compiled concurrently (observed host load
  40.43 / 35.84 / 18.93). Failures were `agent-home` entrypoints/notify/one-probe
  and `agent-project-explicit` entrypoints/notify/chain/installation-only/one-probe.
  All lifecycle tests passed; no race detector report occurred.
- After coordinating a window without Xcode compilation,
  `go test -race -count=1 -json -run '^TestAsdfCompatibilityEntrypoints$/^(agent-home|agent-project-explicit)$/^(entrypoints|notify|chain|installation-only|one-probe)$' ./...`:
  PASS, exit 0, 73.178s; covers all failed prerequisites and their dependent
  subcases. No production timeout or assertion was relaxed. The original full
  race run remains recorded as failed, with all its failures resolved by this
  focused rerun.
- `git diff --check`: PASS.

No real notification API, APNs delivery, iPhone display/lock-screen/navigation,
remote SSH/Mosh target, deployment, remote D1 migration/cleanup, release or tag was
executed. Local protocol fixtures do not establish production retention, APNs
acceptance or phone delivery. This change does not require packaging or publishing.

## Round 2 v1.1 Review fixes (2026-10-31)

Review/start baseline: `d39d03b15b81089bf8552ea708e36573b21abb7f`, branch `main`,
initially clean. Results below are for that baseline plus this round's task
working tree. The full race run used the final production code; the subsequent
addition was the real two-process regression, independently run with and without
race instrumentation. No commit, push, release or deployment is implied by a
local test result; the final task commit and push evidence are recorded in the
single Notion delivery page by the primary agent.

Environment: macOS/Darwin x86_64, Go 1.27.1, Node 22.22.2, npm 11.21.0.
Logs are retained at `/tmp/mobterm-round2-cli-evidence/`.

| Review item | Implementation and focused evidence |
| --- | --- |
| R1 | Native Codex/Claude cwd is validated, bound to canonical operation bytes/digest and returned only over SSH. Inspection/decision frames use v3; configure and HMAC domain remain v2. `approval_context_test.go`, the shared approval vector and existing approval CAS/default-off/native-wait tests cover different directories, relative file/patch inputs, missing/invalid/oversize context, old requests and tampering. App presentation is verified in the App repository. |
| R2 | `flushRunSnapshots` runs fresh business, due durable outbox, then coalesced state within the caller budget. `scheduleRunIntents` persists a per-phase Pair cursor. `TestRichNotifyRecoversDurableOutbox` uses the real rich notify entry: capability success followed by 503/429/lost receipt, no early retry, then automatic recovery of the identical v2 envelope on a later rich event. `TestRunIntentPairRotationSurvivesRestart` covers multiple Pairs and persistent fairness. Existing outbox lease, handoff, retry and v1/v2 tests remain enabled. |
| R3 | `saveRuns` checks the exact compact serialized bytes before atomic replacement (16 MiB); other configuration writes now match their existing 2 MiB reader. A bounded 32 MiB legacy read retry never clears an oversized source. `TestRunStorageOversizeLegacyRecoveryAndAtomicBudget` reproduces 128 x 256 real SHA-256 occurrences beyond the old reader limit, verifies readable compaction and preservation after rejected writes. `TestRunConcurrentPersistenceReadable` covers concurrent goroutines; the separate process test below covers actual processes. |
| R5 | Only completed runs without unsent intents are reclaimed; active runs are retained even beyond 24 hours. Lightweight terminal/start tombstones use the total byte budget instead of an independent historical count gate. State has 128 ordinary slots; critical obligations can use bounded overflow up to 512 runs, and 256 state intents reserve room within the 512-intent total. Capacity attempts bounded recovery before one admission retry. `TestRunRetentionDoesNotLimitDailyBusiness` executes 160 runs; `TestCriticalRunOverflowProtectsUnfinishedRuns` protects 128 active runs; `TestRetainedTerminalCapacityWithPairsKeepsCriticalDelivery` covers two-Pair v2 handoff after 128 completed runs and 4200 tombstones, with duplicate admission suppressed. |
| R6 | `cachedRunCapabilities` is bounded, credential/scope-specific, at most 60 seconds and persists capability Retry-After. Approval authorization continues using uncached `runCapabilities`. Snapshot claims and cooldowns use the shared store lock and a crash-expiring lease. `TestCapabilityCacheAndStateClaimAcrossObservers`, `TestCapabilitiesRetryAfterPersistsAcrossCalls` and the process test below cover cache sharing, current authorization bypass, one claim and backoff. The API repository tests the actual separated edge/D1 route budgets. |
| R7 | The OpenCode adapter forwards precise request IDs and resolved/cancelled events, retains route/location/root-family/lifecycle ownership, releases children and removes invalid queued requests. The CLI aggregates the remaining pending-request/tool sets and ignores unknown/duplicate/terminal resolutions. `TestResolvedRequestPreservesOtherWaitsAndTools` checks aggregation; `scripts/test-native-adapters.mjs` checks the actual child input payloads for permission/form resolution, exact session/run, child family, duplicate, route switch, terminal and cleanup cases. No resolution creates a business reminder. |

Executed checks:

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check`: PASS.
- `go test -json ./...`: FAIL, 433.319 seconds (433.329 command package elapsed),
  502 test/subtest passes, zero skips. Seven leaf prerequisites in the existing
  `TestAsdfCompatibilityEntrypoints` exceeded their unchanged five-second launcher
  preflight during concurrent host load (observed load 188/229/128):
  `agent-project-explicit/{entrypoints,notify,chain,installation-only}` and
  `node-home-explicit/{entrypoints,notify,installation-only}`. Including parents,
  there were ten failed test records plus the package failure. No task behavior
  assertion failed. This original full command remains recorded as FAIL.
- After coordinating lower host load,
  `go test -count=1 -json -run '^TestAsdfCompatibilityEntrypoints$/^(agent-project-explicit|node-home-explicit)$' ./...`:
  PASS, 67 tests/subtests, zero failures/skips, 62.343 seconds. This runs both
  complete failed contexts and their dependent matrices, without relaxing a
  preflight or assertion (`asdf-rerun.json`).
- `go test -count=1 -json -run 'TestRichNotify|TestRunStorage|TestRunRetention|TestCriticalRun|TestCapabilityCache|TestCapabilitiesRetryAfter|TestRetainedTerminalCapacity|TestRunConcurrentPersistence|TestRunIntentPairRotation|TestResolvedRequest|TestActivity|TestToolAggregation|TestRunParallel|TestApproval' ./...`:
  PASS, 44 tests/subtests, zero failures/skips, 32.509 seconds
  (`final-focused.json`).
- `go test -race -json ./...`: PASS, 571 tests/subtests, zero failures/skips or
  race reports, 460.119 seconds (`go-race.json`). This also covers every Asdf
  context and the final shared approval vector.
- `go test -count=1 -json -run '^TestActivityStorageAndTransportAcrossProcesses$' ./...`:
  PASS, 1.347 seconds. The test starts two real test-binary child processes,
  concurrently persists eight distinct observations of one run, then releases
  a flush barrier. All eight revisions/occurrences survive reload; the local
  HTTP server observes exactly one capability GET and one snapshot PUT.
- The same process test with `-race`: PASS, 4.241 seconds. Both child processes
  use the race-instrumented binary. Children have bounded contexts and explicit
  cleanup; these are actual processes, not goroutines (`process-race.json`).
- `npm ci --prefix scripts/native-adapter-tests --cache /tmp/mobterm-round2-native-cache --ignore-scripts --no-audit --no-fund`:
  PASS; lockfiles unchanged.
- `node scripts/test-native-adapters.mjs`: PASS on final adapter code
  (`native-adapters.log`). During development its strict field whitelist
  correctly rejected the newly added `request_id`; the contract assertion was
  extended alongside direct resolution payload assertions, then rerun. A first
  short-phase transport experiment exposed the prior fixed one-second HTTP
  reserve; short phases now retain a proportional commit margin, and the final
  real notify recovery regression passes.
- `go build -trimpath -o /tmp/mobterm-round2-cli-evidence/mobterm-cli .`, followed by
  `python3 scripts/test-native-adapters-host.py opencode /tmp/mobterm-round1-native-hosts/node_modules/.bin/opencode /tmp/mobterm-round2-cli-evidence/mobterm-cli 2.1.0`:
  PASS. The pinned, previously isolated official OpenCode 2.1.0 host loaded the
  newly built adapter at both setup and restart, registering 13 listeners each
  (`opencode-host.log`). The script created and removed its own temporary
  HOME/XDG/config/store, scrubbed model credentials and sent no model request.

The 16 MiB run budget and finite live queues are deliberate limits, not an
unbounded throughput guarantee. If genuine live responsibility exceeds them,
prior data is preserved and an explicit capacity error remains until bounded
recovery/expiry frees capacity. Older CLIs must not overwrite the new run file;
mobile approval v3 requires the matching App/CLI, while v1 notification/AAD
vectors and older adapter input remain compatible. No daemon or idle automatic
retry is claimed. No real Agent approval, user Hook configuration, SSH server,
APNs, physical device, remote migration, deployment, release or tag was operated.

## Codex hook representation migration (2026-10-31)

Native Codex installation now follows existing JSON/TOML event definitions,
using JSON for new configurations. Only proven Mobterm entries are migrated;
user events in both formats are preserved and reported as a conflict. Trust
state remains in TOML. Version 2 journals guard both files, ownership and modes;
the source is removed first, so an interruption requires resuming the pending
action and can temporarily omit native handlers without duplicating them.
Version 1 journal recovery remains covered by the existing regression suite.

Focused checks passed:

- `go test ./... -run 'TestCodexLayout|TestCodexNotifyOnly|TestRichNotify' -count=1`
  covers representation selection, both migration directions, unchanged repeat
  installs, original/later user data, duplicate keys, edited/duplicated ownership,
  symlinks, all journal write boundaries, conflicting concurrent edits, App
  repair discovery, native activation receipts and JSON-owned v2 completion retry.
- `go test ./... -run '^TestCodexJSONUninstall' -count=1` additionally checks that
  uninstall preserves later user groups in the same event, large integer metadata
  and user events in both representations.
- `go vet ./...`, formatting and `git diff --check` passed.

`go test ./...` passed (373.432 seconds). After tightening the file-state
recheck immediately after the first migration write,
`go test ./... -run '^TestCodexLayoutRecovery' -count=1` passed (13.049 seconds),
and `go vet ./...`, formatting and diff checks passed again.
The final full `go test -race ./...` passed (477.348 seconds), with no race report.

The installed official Codex 0.166.0 was also executed against disposable HOME,
CODEX_HOME, cwd and Mobterm store directories, with an allowlisted environment
containing no credentials. A fresh installation was read through the real
`app-server` `hooks/list` RPC: seven Mobterm hooks plus the original user
SessionStart, with no warnings/errors. For the upgrade path, v0.1.11 source was
exported using `git archive`, built in a temporary directory and installed at the
same temporary executable path as the new build. The old CLI reproduced the
mixed-representation warning with eight hooks; new discovery reported the repair
state; migration retained all eight hooks and removed the warning. A repeated
install still listed eight hooks, and uninstall left only the original user
SessionStart. TOML trust state was preserved. All temporary binaries/configs and
app-server processes were cleaned up; no model request or hook trust grant ran.

App compatibility was checked by code inspection: existing `partial` states
permit repair and bypass the `complete` early return, and unknown issue codes
are accepted. The API does not consume these local hook files or discovery
states; wire envelopes and pairing contracts are unchanged. No App build or
physical push test is claimed. No remote installation, release or deployment
was performed.

## Remote interaction wire v1 — implementation validation

Starting CLI `main` HEAD: `5ece1082187fca431ec4998fa8ea433275837346` (same as
analysis). The delivery report records final task commits; these commands ran
against that HEAD plus the task diff. The new protocol is described in
[interaction-v1.md](interaction-v1.md), and official version/adapter evidence in
[native-interactions.md](native-interactions.md). No production API, remote D1,
paid model, user Agent session or physical iPhone was used.

Focused Go interaction tests passed while integrating: 4.584s, 13.371s, 6.588s,
15.231s and 10.907s runs covered exact-byte HMAC vectors, nested duplicate JSON
keys/invalid Unicode/trailing frames, safe numeric values, absent/false/zero/empty
answers, native UTF-16 conditions, concurrent pairing CAS, idempotent replay and
body conflict, status privacy after process exit, release/disable, pre-native
crash boundaries and event-owned immutable hints. Later focused results below
supersede these intermediate runs. `go vet ./...` passed during integration.

The first complete ordinary `go test ./...` finished in 541.092s with one actual
regression: `TestStructuredClaudeHealthAndSafeRepair` still recognized only the
old managed Hook command/timeout. The production health checker now recognizes
both the existing owned record and the new 330-second dispatcher while retaining
exact ownership and tamper checks. Its focused rerun passed (1.977s); no assertion
was weakened. Native schema/Hook focused suites passed (3.167s and 16.618s).

The first full `go test -race ./...` exceeded the default ten-minute harness
limit at 602.199s and reported two existing tight command-budget/probe failures
under concurrent Xcode work (`TestRetainedTerminalCapacityWithPairsKeepsCriticalDelivery`
and `TestAsdfProbeIsolationAndFailure`); no data race was reported. Repeating the
same suite with `-timeout 20m` completed in 620.580s: those paths passed, with one
remaining five-second launcher fixture timeout in
`TestAsdfCompatibilityEntrypoints/node-project/one-probe`. The per-operation
production and test budgets were not increased. This full run is not reported as
PASS; the isolated retry and final changed-path results are recorded below.

Evidence levels: protocol/fault/concurrency tests and native adapter harnesses
are E1. Official isolated OpenCode/Pi host loading is E2; loading does not prove a
model produced a question or consumed a mobile answer. E3 real-model sessions and
E4 real-device SSH/APNs/network/background flows remain unexecuted for lack of an
authorized environment. Unknown delivery remains unknown rather than a success
claim. No daemon, terminal-input injection, release, deployment or cloud migration
was performed by these checks.

Final delivery checks on 2026-11-04 (Asia/Shanghai): ordinary
`go test -timeout 20m ./...` PASS, 416.279s. The isolated
`go test -race -timeout 3m -run '^TestAsdfCompatibilityEntrypoints$' ./...`
PASS, 156.766s, with the original probe/command budgets. `go vet ./...` and
Linux amd64 cross-build PASS. The final native focused suite passed in 20.147s;
the Node adapter harness (including the shared HKDF/HMAC vector) passed.
Rebuilt adapters loaded successfully twice in official OpenCode 2.2.5 and in
official Pi 1.5.0's real TUI (nine listeners). These remain E2 loading checks,
not model-driven or phone-to-Agent validation.

The late Codex existing-daemon queue implementation has four dedicated E1 test
groups: accepted final Hook contract, unique loaded-thread mapping and owner
changes, exactly one mutation with ACK/lost ACK/mismatched receipt, and guarded
Unix rendezvous/peer ownership. They passed in 0.856s; the first rendezvous fixture
exceeded macOS's Unix socket path limit and was corrected to use a short isolated
temporary directory. No production socket or user session was created or used.
The ordinary full suite started before these four new test groups were added;
the final changed-path race run below covers them and the final implementation.

Final changed-path `go test -race -timeout 5m -run '<interaction/native/Codex
groups>' ./...`: PASS, 11.245s, including all 15 `TestInteraction*` groups, event
hint ownership, Claude/OpenCode native schema and Hook migration, MCP/legacy
approval scope, and the four Codex queue groups. No data races were reported.
Final `go vet ./...`, `gofmt -l .` (empty) and `git diff --check`: PASS.
Reviewable sanitized output and the exact focused selector are recorded in
[delivery evidence](evidence/interaction-v1.txt). No full race PASS is claimed;
the original full-suite timing failures and the isolated successful rerun remain
part of the evidence above.

## 2026-11-04 — Remote interactions, Review repairs R4/R5 (WF-1.0)

The effective instructions are round 2 v1.0 on the [existing handoff](https://app.notion.com/p/40ccede20823814abf99d3d55f24759e).
Analysis and actual start: `c46bb626f21e04996f68283ffdd5509f7942fd4e`, clean main.
API was read-only at `adaa3db7b4f70d09de89fd34bbc585e9be136ebb`; no API
contract, dependency, allowlist, production budget or deployment changed.

R4: normalized request/envelope overflow before record creation produces one
helper-owned read-only unavailable observation. It has no actions, native waiter,
owner token or dispatch privilege. Fresh enabled interaction policy can authorize
the reminder even with general needs_input off. Tests exposed and repaired this
policy interaction; no broad retry on helper/adapter errors was added. Existing
adapter close behavior already consumes null without reply or duplicate notify;
new subprocess tests cover raw overflow, helper-owned normalized overflow,
arbitrary child failure and lost native ACK after attempted dispatch.

R5: Elicitation keeps parseActivity's run/occurrence and errors. Production
observe -> normalization -> store -> encrypted needs_input -> authenticated
response -> Hook output tests cover two prompt cycles, two requests per cycle,
unchanged terminal history, no-cycle nonempty occurrence and malformed cycle
storage. No history clearing or terminal-state reopening is used.

R1/R2 compatibility: existing same-frame CAS/replay, immutable request binding,
status and native-evidence tests remain. A new confirmed receipt test verifies
global native_confirmed is visible to both original pairs while decision/action/
response_digest/evidence are visible only to the submitting pair, never answer
body. App matches that tuple; no wire migration is necessary.

Environment: Go 1.27.1 darwin/amd64, Node v22.22.2. Final checks use the task
working tree included in this round's commit. Logs are retained in /tmp with the
mobterm-cli-r2 prefix; sanitized, committed evidence below is the portable record.
Early focused test failures are retained: the first test harness decoded variable
ciphertext with an exact-size helper and incorrectly expected a URL-only fallback
under disabled general needs_input. Corrected those fixtures; a real R4 suppressed
reminder was repaired as above. Overlapping Xcode work also exceeded fixture
notification waits; final focused production tests passed with original operation
budgets. No production timeout or effective assertion was relaxed. A failing
fixture now cancels and drains its owned waiter before temporary-store cleanup.

`node scripts/test-native-adapters.mjs`: PASS (existing pinned Solid dependency
and controlled actual subprocesses); no official host/model loading is claimed
for this round. The final focused R4/R5 suite before the extra receipt-redaction
assertions passed: `go test -v -timeout 2m -run 'TestInteraction(OpenCode|ClaudeElicitation)' ./...`, 10.923s.
Full-suite and final-check results follow below. First-round full-race failures
remain historical facts; they are not reclassified as passes by this repair.

No E3/E4 real model, user Agent session, paid provider, physical phone, remote
SSH/PIV/Mosh or APNs flow was executed. API full tests, packaging/release and
unrelated official host harnesses were not rerun because their paths did not
change. No cloud or remote persistent state was modified.

First complete ordinary check in this round: `go test -timeout 20m ./...`
**FAIL**, 452.983s. Only `TestAsdfCompatibilityEntrypoints` failed: six original
launcher five-second preflight timeouts (`node-home-explicit/installation-only`,
`node-home-explicit/one-probe`, and `node-project/{entrypoints,notify,chain,installation-only}`).
The failure is in the unchanged `versionEnvironment` launcher usability check,
before the task's interaction path; all R4/R5 and receipt tests completed without
assertion failures. This run overlapped Xcode compilation and is retained as a
failed command, not reclassified. Complete reruns below run without Xcode work;
production/test command budgets remain unchanged and the explicit suite timeout
is 20 minutes, as authorized in the handoff.

Complete ordinary rerun without Xcode: `go test -timeout 20m ./...`
**PASS**, 405.337s, exit 0, on final production/test source. This includes the
new four review-regression groups and unchanged CAS/replay/evidence contracts.
`go vet ./...` PASS, `test -z "$(gofmt -l .)"` PASS, and `git diff --check` PASS.
No individual production or fixture timeout was altered to obtain this result.

Final complete race check, no Xcode work: `go test -race -timeout 20m ./...`
**PASS**, 498.806s, exit 0, with no data race report. This is a full-suite PASS
for this round, unlike the preserved first-round full-race failures. No code,
assertion or per-operation budget changed between the serial ordinary and race
runs. Final production and tests are frozen; only these validation facts and
[portable sanitized output](evidence/interaction-r2.txt) were appended afterward.

R1–R5 task changes now have no unresolved related actual failure. API remained
read-only/clean; App's unrelated Binding/Pairing baseline failures are documented
in its own report. Under the agreed single static Review workflow, Codex will
save/read back the same-page repair report and close the original task directly;
no second ChatGPT Review is required. Exact commit/push/status receipts are in
that Notion report.

## Codex hooks 第 1 轮 v1.0 P1 delivery (2026-11-06)

Scope: P1-A approval observation/waiter lifecycle and P1-B ordinary Codex tool
Hook reception/dispatch. G0 is still a known event-contract blocker: a Stop Hook
can cause the current turn to continue before legacy notify. P0 has not been
implemented, and the overall requirement remains **待实施**, not 待验收/已完成.
No completion mapping, installed Hook template, migration, installer, third-party
notifier chain, user Codex configuration or remote Agent runtime was changed.

Opening CLI: main at `a1cae43c2ce260e27bffbbb3a2ed6cb51013b5dc`, clean. API was
read-only/clean at `adaa3db7b4f70d09de89fd34bbc585e9be136ebb`. App was read-only/clean
at `cfe565f2a18d7c423f95bad6da15dcb846f23c7e`, ahead of the analysis reference
`2f077d10ba894656f3c17353b71558f7e7f9c2d2`. Its intervening configuration UI and
installation changes do not alter NotificationActivity's state/hint contract;
that consumer and API notification-v2's independent revisioned state path were
read. App/API received no task changes, tests or deployments.

Environment: macOS 15.8.1 (24H32), Intel Core i7-8569U, x86_64, Go 1.27.1
(darwin/amd64). [Raw subprocess measurements and limitations](evidence/codex-p1-latency/README.md)
record baseline/after build fingerprints, sample counts, percentiles, synchronous
HTTP, durable receipt and sampled worker/target metrics. Counts from fixtures
are not real Codex/native approval or API/APNs delivery evidence. No real Herdr
application, phone display, Jump to UI, external API, model calls or paid runtime
was exercised. Real isolated tmux identity coverage is reported by the Go suite
and the separate real-tmux measurement scenario.

Focused dispatch tests cover durable reception and zero synchronous HTTP,
including full-store recovery; late Pre cannot regress a returned tool; a
concurrent burst admits one active and one pending worker; a final queued
business event is sent; a killed pending worker and failed executable launch
release admission without deleting stored responsibility. An initial worker
race fixture reported only one of two sends: investigation found the new short
worker inherited the old 600ms foreground phase cap. The dispatcher now passes
its explicit five-second network budget. The focused dispatch suite passed
three repeats after that repair; no business-state assertion was weakened.
Approval lifecycle coverage and complete required check results follow below.

P1-A focused checks passed (24.349s), covering passive observations, two same-text
requests without native IDs, request-level resolution alongside another approval
and question, expiry/parent identity/local permission loss, resolved intents and
already-encoded outbox, resolution during capability lookup, Pair-scoped
revocation, terminal run protection, native consumption and existing CAS.
A subsequent registration-sweep regression passed (3.233s): old expired waiters
are removed from their exact run before retention cleanup or new registration.

Measurement failures are retained, not relabelled as passes. The first baseline
was contaminated by three test processes started before the test executable's
child-command routing existed. Only those owned processes were terminated;
`exploratory-contaminated` data is excluded from performance comparisons. A clean
baseline was collected without concurrent builds/tests. It had zero sequential
receipt failures but 22/512 missing durable occurrences under eight-way load.
The first modified build (`after-initial`) still had 10/512 concurrent and 2/64
history/multiple-Pair receipt failures despite zero foreground HTTP. Five of the
concurrent calls failed before target query, identifying the existing installation
read's global write-lock acquisition as a concrete contributor. The identity
read now avoids that lock for an existing valid atomic file; initialization,
private-path checks and invalid-data rejection remain unchanged. Regression tests
for reading while a run writer holds the lock and for eight simultaneous first
initializations passed (1.152s). No operation timeout or Hook template was relaxed.
Final measurements and complete-suite results follow below.

The identity-only follow-up still had 8/512 concurrent and 1/64 history receipt
failures; all target queries now completed. A temporary receiver-priority gate
also failed (9/512 and 2/64) and was removed rather than retained as unused
complexity. The remaining store contention used fixed 20ms nonblocking polls,
allowing newer writers to reacquire repeatedly while earlier writers slept.
Contended Store.lock now joins the kernel lock wait queue with an unbuffered
ownership handoff. The original deadline remains; a cancelled waiter cannot
write state or leak the later-acquired lock. All other lock users retain their
existing behavior. Focused dispatch, concurrent storage, identity and cancelled
waiter tests passed (4.282s). The same subprocess follow-up then saved 512/512
concurrent and 64/64 history observations, with zero foreground HTTP; the final
full measurement matrix uses that frozen binary. All failed intermediate data
remains in the evidence directory; no operation timeout was increased.

The schema fixture check initially failed because the existing Codex
PermissionRequest sample still expected `waiting_approval`. The same stale
expectation surfaced in the first full ordinary run, which was stopped after
recording that confirmed failure (owned test process group only). Updated just
that Codex fixture to `tool_pending`; Claude/OpenCode assertions remain intact.
`go test ./... -run '^TestActivitySchemas$' -count=1` then passed (1.395s).
The measured Go-source manifest still exactly matches the working tree; only
this expected-result fixture and documentation changed after measurement.
A fresh full ordinary/vet/race sequence follows, with the failed initial record
preserved in [checks-initial](evidence/codex-p1-checks-initial.json).

Final full-matrix measurement: 3,080 actual CLI calls, including 2,920 ordinary
Pre/Post observations; every ordinary observation was immediately durable, emitted
no stdout and made zero foreground HTTP attempts. Concurrent 8-lane reception
was 512/512 (clean baseline 490/512); three-Pair history was 64/64; isolated real
tmux was 200/200. Sampled dispatcher maximum was two processes, receiver overlap
one request. Sequential Pre P50/P95/P99 was 64.56/90.65/100.27ms versus clean
baseline 84.73/178.12/278.60ms; Post was 64.62/90.61/99.77ms versus
84.59/260.87/285.18ms. The full table includes all seven installed Hook types;
small-sample UserPromptSubmit/Interrupt tails did not uniformly improve. Separate
lock-wait/fsync metrics and real device/native runtime evidence remain unmeasured.

Final ordinary suite: `go test -json -timeout 20m ./...` PASS, 465.984s, 742 test
items passed, no failures or skips. `go vet ./...` PASS, 0.520s. JSON output and a
20-minute suite ceiling only improve traceability; no per-operation test or Hook
budget was relaxed. Portable command results are in
[checks](evidence/codex-p1-checks.json). Full race results follow.

Final race suite: `go test -race -json -timeout 20m ./...` PASS, 571.214s, 742
items passed, no failures, skips or data race report. `test -z "$(gofmt -l .)"`
and `git diff --check` PASS. The measurement script compiled with Python and all
seven CSV rounds were independently recomputed against their JSON percentiles,
sample counts and durable-receipt counts; final ordinary stdout/HTTP/receipt
assertions passed. No further production or test-source edits followed these
successful complete checks.

A1–A7 and A11 have passing local behavior/regression evidence. A8 additionally
includes real isolated tmux tests and measurements, but real Herdr and App Jump
to UI were not exercised. A9 has unchanged wire contracts, CLI vector/compatibility
tests and read-only App/API inspection; it is not proof of real API/APNs or phone
delivery. A10 has complete per-installed-Hook timing evidence and disclosed
sample sizes, but no separately instrumented lock-wait/fsync duration/counts or
enabled human-wait timing. A12/A13 remain blocked by G0 and unimplemented. No
release, deployment, live configuration migration or completion-source change
was performed. The full requirement must remain 待实施.

The first staged diff check additionally caught CRLF line endings in newly added
CSV evidence (untracked files were not covered by earlier unstaged diff checks).
Normalized them to LF and made the exporter explicit; parsed samples are exactly
unchanged. Python compilation and the final staged diff check then passed. This
format-only change does not alter the measured or tested Go implementation.

## 2026-11-09 — Codex hooks round 2 v1.2, R1 sender cleanup

[Effective scope and repair instructions](https://app.notion.com/p/410cede208238114a929c77dbacc1edb).
CLI start/review: `67ba0a446c1e524b9d686838878b9aa43905f72a`, clean `main`.
App `cfe565f2a18d7c423f95bad6da15dcb846f23c7e` and API
`adaa3db7b4f70d09de89fd34bbc585e9be136ebb` remain read-only and unchanged.
The user deferred P0 (pure hooks and old configuration migration); final scope
is P1-A, P1-B and R1. The existing notify completion source remains in use.
G0 remains a contract limitation, but no longer blocks this adjusted scope.

The earlier local PASS does not supersede the subsequent
[macos-26-intel race-step failure](https://github.com/simplekkj/mobterm-cli/actions/runs/42195870502/job/127818867353).
That SHA's CI finished with 10 successful jobs and one failed job.
`TestInteractionHeldDispatchNeverReplays` returned after its native waiter,
while the independent `publishActivity` goroutine could still write outbox
files during TempDir removal. The log reports `directory not empty`, not a
Go `DATA RACE` report. This was not an activity-dispatch child-process failure.

`withInteractionSenders` provides optional, context-scoped ownership of the
actual sender goroutines. Registration precedes launch; completion follows
all publication work and cancellation cleanup. Test owners stop/join native
waiters first, then join senders before their HTTP server and store cleanup.
The adjacent Claude elicitation and OpenCode form fixtures use the same boundary.
Production native output, cancellation policy, five-second sender budget,
durable intents/outbox, ordinary tool reception and dispatcher are unchanged.

`TestInteractionHeldSenderCleanup` gates the actual capability transport after
durable activity reception. Both completion and cancellation cases keep the
sender blocked until teardown, while the native answer and response receipt
finish first. Teardown releases the gate and joins the whole sender, including
its final store writes, before resource destruction. The original dispatching
CAS, no replay for the same native identity, duplicate receipt, response_written
and sensitive-data scrubbing assertions run in every case. No fixed sleep,
skip, ignored cleanup error or extended production budget is used as the fix.

Local environment: macOS 15.8.1 (24H32), x86_64; Go 1.27.1 darwin/amd64;
tmux 3.7c. Checks ran on the repair working tree based on the review SHA;
the accompanying repair commit contains the same tested Go sources.

- Focused ordinary run: PASS, 4.018s. The transport completion marker was then
  tightened to cover RoundTrip return before the following race/full checks.
- `go test -race -count=20 -run '^TestInteractionHeld(DispatchNeverReplays|SenderCleanup)$' -json ./...`:
  PASS, 74.531s; original, delayed completion and delayed cancellation each ran
  20 times (80 test/subtest PASS outcomes), zero failures/skips.
- `go test -race -count=1 -run 'TestInteraction|TestCodexPassiveApproval|TestApproval|TestResolvedApproval|TestTerminalRunCannotKeepActionableApproval|TestOrdinaryCodexObservation|TestActivityDispatch' -json ./...`:
  PASS, 32.534s; 47 outcomes, zero failures/skips. Includes ordinary synchronous
  HTTP=0/durable reception and bounded dispatcher recovery regressions.
- `test -z "$(gofmt -l .)"`: PASS.
- `go test -count=1 -json -timeout 20m ./...`: PASS, 455.005s; 745 outcomes,
  zero failures/skips.
- `go vet ./...`: PASS.
- `go test -race -count=1 -json -timeout 20m ./...`: PASS, 547.811s; 745 outcomes,
  zero failures/skips or data race reports.
- `git diff --check`: PASS.

The prior performance matrix belongs to the first-round SHA. This repair does
not change the measured ordinary reception/dispatcher paths, and no new latency
measurement is claimed. No local packaging matrix, App/API full suite, real
model, production API/APNs, phone display or Jump to UI test was performed.
The existing CI matrix and exact final-SHA results are recorded in the linked
handoff after push; local results are not a claim that CI passed.

P0 已延期，纯 hooks 及旧配置迁移尚未实施；按唯一一次静态 Review 后修复的约定直接关闭。
This closing rule applies only after R1, required checks, push and final-SHA CI
verification succeed. No second ChatGPT Review or acceptance prompt is required.

## Claude child waits and Codex reverted rollouts (2026-11-18)

Tested on the repair tree based on `c9b8bf62d944b185668300ae83d49f2467f2c673`
with Go 1.27.1 darwin/amd64, macOS 15.8.1 and tmux 3.7c.

- Two child sessions used the same tool ID. A matching Post cleared the original
  wait after reopening the store. Wrong root, owner or target, duplicate and
  late callbacks left other requests intact. Coverage includes failure, cancellation,
  missing Post, expiry, capacity retry, outstanding delivery, disabled questions
  and read-only plans.
- During verification the held interaction sender exhausted the synchronous
  600 ms budget before a notification was delivered. A controlled 700 ms
  capabilities response now exercises its existing five-second asynchronous
  deadline through delivery and native confirmation.
- The earlier hyphenated fixture did not exercise Codex's reverted filename.
  Independent rich and legacy fixtures now resolve a thread whose only file
  ends in `THREAD_ID_ROLLOUT_ID.jsonl`, replay the callback, and decrypt the
  single reminder to check its original bounded excerpt.
- With `MOBTERM_REQUIRE_TMUX=1`, 72 focused tests/subtests passed (82.540 s).
  `go test -count=1 -json -timeout=30m ./...` and
  `go test -race -count=1 -json -timeout=30m ./...` each passed 809
  tests/subtests (488.007 s and 609.190 s), with zero skips. Repository gofmt,
  `go vet ./...` and `git diff --check` passed. Native events and HTTP were
  local fixtures. Real model sessions, APNs and device display were not exercised.

## Historical reply protocol and archive

Run `go test ./... -run 'TestMessageBody|TestMessageSummaryNativeNegotiationAndRetry|TestCodexOwnershipThroughDurableNotification'`
for framed proof/vector, retention, capacity, actual private files, write failure,
revocation and frozen v1/v2 delivery coverage. The native harness
`node scripts/test-native-adapters.mjs` exercises exact assistant-part selection,
OpenCode route/message ownership, Pi round/settled correlation, and the same-child
capability handshake including rollback. Use the official isolated host loader
separately to check registration. Neither harness performs live model turns or
establishes APNs delivery, phone rendering or physical credential behavior.

### First implementation, 2026-11-19

Tested the working tree based on
`aae20e523e1d3dcbab9e3d69e19ea4b2ac55145b` on macOS 15.8.1 (24H32),
Intel x86_64, Go 1.27.1, Node 22.22.2 and tmux 3.7c.

| Check | Observed result |
| --- | --- |
| `test -z "$(gofmt -l .)"`, `go vet ./...`, `git diff --check` | PASS |
| `go test -json -count=1 -timeout 20m ./...` | PASS, 501.526 s. 817 test/subtest PASS, zero failures or skips |
| `go test -race -json -count=1 -timeout 20m ./...` | PASS, 686.133 s. 817 test/subtest PASS, zero failures, skips or race reports |
| `node scripts/test-native-adapters.mjs` | PASS, including text-part attribution, bounded extension, rollback to base packet and Pi length-limited reply |
| `python3 scripts/test-native-adapters-host.py` with official OpenCode 2.1.0 and Pi 1.5.0, isolated generated assets | PASS. OpenCode TUI setup and restart registered 15 listeners each. Pi's official loader imported the extension and its TUI session registered 11 listeners |

The ordinary full suite started before a final `api.go` correction to the
classification of a successful pair-status response. The ordinary targeted
`TestPairStatus|TestRemoteRetirementDisables` check on that change passed in
5.986 s. The race suite compiled the complete final Go source.

Running the full suites concurrently initially exhausted the asdf fixture's
five-second original-launcher preflight and the default Go package timeout.
The ordinary and race preflight cases passed separately (2.939 s and 6.124 s);
the full runs above then ran serially with the original launcher/check deadlines.

The cross-version pipe check used binaries built from the starting SHA and
the implementation, empty private stores, a disposable local tmux socket,
and a synthetic Pi completion. The old executable accepted the base packet
after capability timeout, the new executable accepted its negotiated extension,
and both accepted the unchanged old adapter packet. Each accepted occurrence
was read back from local run state. The Node harness separately exercised the
shipped adapters. The host registration checks used temporary HOME/config;
they did not run a live model conversation or send real APNs notifications.


## Native source-bound replies, 2026-11-19

Verified on macOS Darwin 24.6.0, x86_64, Go 1.27.1 and Node 22.22.2.
The tested locally built CLI used the implementation working tree based on
`b21dd0e1facffcfa6373833400c1bfae84f54872`. Its SHA-256 for the three final
native delivery runs was `96ab1a4618510ddfcac164fb7b1ccef5735509450c5815c7c7e5749ac8c6af18`.
The exact Git commits for this cross-repository delivery appear in its handoff.

| Executed scope | Result |
| --- | --- |
| `node scripts/test-native-adapters.mjs` | PASS. Includes the exported adapter modules, held and late helper outcomes, grants, version gates, Pi switch/tree/busy/third-party prompt, OpenCode route/permission/new source/disposal, and Claude stop/end/change/reload/subagent results. Unicode, whitespace and full-budget input are checked. |
| `python3 scripts/test-claude-native-reply.py "$CLAUDE_BINARY" "$CLI"` | PASS with official Claude Code 2.1.328. The CLI-generated plugin passed Claude's module validator. A real isolated interactive TUI loaded the Mod. Its ordinary-reply helper and classic Hook saw the same session, host PID and prompt ID. The Mod submitted the exact new user text, produced `native_accepted`, and the local model fixture saw both prompts in that original session. |
| `python3 scripts/test-plugin-native-reply.py opencode "$OPENCODE_BINARY" "$CLI"` | PASS with official OpenCode V2 2.3.2. The real standalone TUI accepted the original plugin's explicit queued prompt, published the matching native inbox delivery, and the fixture received both user messages, byte-for-byte, in the same history. Receipt states observed: `native_accepted` and `native_consumed`. |
| `python3 scripts/test-plugin-native-reply.py pi "$PI_BINARY" "$CLI"` | PASS with official Pi 1.5.2. The original extension's `sendUserMessage` produced a second native user-message event in the same session. The local provider received the text with surrounding whitespace, Unicode and literal template unchanged. The adapter reported `delivery_unknown` because its public caller has no reliable correlation ID. |
| `python3 scripts/test-native-adapters-host.py opencode "$OPENCODE_BINARY" "$CLI" 2.3.2`, and `python3 scripts/test-native-adapters-host.py pi "$PI_BINARY" "$CLI" 1.5.2 "$PI_PACKAGE_ROOT"` | PASS in isolated directories. OpenCode loaded all 18 listeners on both real TUI launches. Pi's official discovery/import and its real TUI registered 16 listeners. An initial OpenCode probe under simultaneous builds timed out, and one JS child readiness check timed out. Both reruns passed. |
| Codex CLI `--version` | Observed 0.170.0. Ordinary replies have no dispatch path in either shared or embedded/profile mode. Legacy async acceptance, exact loaded-thread mapping and socket/queue checks use the existing controlled tests. |

Controlled native-event regressions exercised late and overlapping callbacks.
Pi preserved its completion notification across a pre-settlement compaction,
withheld the ordinary operation while a third-party UI prompt remained open
and refused a missing pending-input probe result. OpenCode refused same-location
global forms and family permissions already open at completion, rejected a
malformed active-session response, and ignored replayed durable execution
events with older sequence numbers. Claude forwarded incomplete native
completion events through the Mod hook without opening an opportunity.

The three real native delivery runs supply one already-authorized helper decision
and a local synthetic model response. They verify the production adapter's
submission and original host consumption; remote authorization, API/APNs, paid
model execution, real device delivery and background/SSH transport are separate
evidence. No installed user session, credential or configuration was exercised.

### CLI checks for the final source-bound reply implementation

Both full suites ran serially on macOS 15.8.1 x86_64, Go 1.27.1 and tmux 3.7c
with the same final Go sources, tests and embedded adapters. The repository
build command produced SHA-256
`2feec877afdee17e3174f544f782ce77777eacb4f14039c8a41061a34042da82`.
Building the same tree without `-trimpath` reproduced the binary used in the
three official host tests byte for byte.

| Executed command or scope | Observed result |
| --- | --- |
| `MOBTERM_REQUIRE_TMUX=1 go test -json -count=1 -timeout 45m ./...` | PASS, 624.759 s. All 836 tests and subtests passed with zero skips. The live tmux identity, pane movement and respawn fixtures ran. |
| `MOBTERM_REQUIRE_TMUX=1 go test -race -json -count=1 -timeout 45m ./...` | PASS, 754.271 s. All 836 tests and subtests passed with zero skips and zero race reports. |
| `test -z "$(gofmt -l .)"`, `go vet ./...`, `go build -trimpath -o mobterm-cli .`, `git diff --check` | PASS. |
| `node scripts/test-native-adapters.mjs` and `go test -count=1 -run '^TestNativeSummaryAssetUpgrade$' ./...` | PASS against the final templates, including reinstall/upgrade of generated adapters and their ownership fingerprints. |
| Final CLI executable with a temporary private store and synthetic Pair | PASS for read-only negotiation, signed local disable, stale signed-frame rejection, configuration revision readback and authenticated missing-source fallback. |

The Go/Swift wire vectors, two-Pair competing submissions, single-use native
authorization, stale source/owner changes, crash responsibility, policy
off/on replay, receipt access after outbox cleanup and private file checks ran
in both suites. The controlled native completion fixtures produced one
notification per Pair and matched its signed envelope to the same original
Claude, OpenCode or Pi source with no body. Claude ran with the Mod callback
both before and after classic Stop/idle delivery.

The Codex regression used a recording Unix daemon at the guarded default
rendezvous for an isolated configuration. Each positive control established a
loaded, directly addressable thread. In all four scenarios (unsubscribed idle,
same-ID unload/reload, shared tree session ID, embedded/profile with a separate
default daemon), CLI inspect and send returned the ordinary-reply fallback.
They opened zero additional connections and made zero native calls.

An early integration run caught obsolete expectations for the managed adapter
revision and reached Go's default ten-minute package timeout during the
seven-day outbox fixture. Superseded concurrent runs were stopped. An earlier
ordinary suite passed in 617.951 s before the last Pi/OpenCode adapter changes.
The final runs above include those changes and used a 45-minute package-level
test timeout.

### Claude conversation changes and Pi input before settlement

On macOS 15.8.1 x86_64, `node scripts/test-native-adapters.mjs` reproduced
both regressions against their original adapters at
`d4d83953fb580a95c65f85f5a1e2a734f1561b34`. Claude opened only the old
waiter across a clear. Pi opened a waiter for a round after observing new
input. With the fixes, both cases and their new-source recovery checks passed.
The controlled callbacks also exercised late authorization, source-specific
receipts, Claude resume, and Pi idle with no pending messages during preflight.

Two isolated official TUI runs exercised the assets installed by the built CLI:

- `python3 scripts/test-claude-native-reply.py "$CLAUDE_BINARY" "$CLI"`
  passed on 2.1.328. Across a real `/clear`, the same process registered
  one Mod start and distinct session, prompt and instance IDs. The new
  conversation received the exact reply and produced `native_accepted`.
- `python3 scripts/test-plugin-native-reply.py pi "$PI_BINARY" "$CLI"`
  passed on 1.5.2. A second extension held the next input while the first
  run settled. Its completion reached the notification helper. Only the
  new run opened a reply opportunity during that transition. The original
  session consumed the exact reply and the adapter reported `delivery_unknown`.

These runs used a local synthetic model and a fixture authorization decision.
An isolated reinstall from the Review binary verified both rendered asset
fingerprints, repeat installation, and preservation of locally edited assets.

## Codex TUI terminal ownership and Herdr completion entries (2026-11-20)

The comparison used Intel macOS, the official Codex 0.170.0 executable, tmux
3.7c, and the previously checksum-pinned Herdr 0.9.1 and 0.9.3 binaries
(protocol 22). All configuration roots, sockets, model Responses and API
receipts were local fixtures. No personal login or model provider was used.
The baseline CLI was built from
`184f8dce25c122a33fe9b8c7fb7bbebd39ac820f` (v0.1.17). Each TUI started
in a real multiplexer pane, after the normal hook installer merged a root
notifier and a third-party SessionStart command in its isolated configuration.

| Launch and observation | Result |
| --- | --- |
| Baseline, two ordinary Codex TUIs using one managed daemon in two live Herdr panes, same cwd | Two native completions reached the local receiver. Both persisted locators named the first pane and its occupant. |
| Updated CLI, same two-panes shared case | Two completed TUI turns; the chained notifier still ran. Zero Mobterm snapshots, deliveries and pending events. The Codex health reported a prior terminal-owner refusal while configuration remained complete and loaded remained unknown. |
| Foreground `codex app-server --listen unix://…` in one pane, `codex --remote unix://…` in another | Confirmed local response, native callback and chained notifier; zero Mobterm events, snapshots and pending events. A shared server staying inside a pane's process session did not acquire that TUI's authority. |
| `codex --no-daemon` in Herdr and in tmux | Confirmed root turn, native notify and matching persisted terminal locator. The normal callback passed both with native hook trust skipped in the isolated review UI and with those fixture hooks approved there. The original chained command remained active in both cases. |
| Historical official Codex 0.162.1 binary, explicit embedded launch in Herdr 0.9.3 | The unchanged root callback reached the local receiver, retained the chained notifier and finished with one handoff and zero pending. |
| Four sources on one actual Herdr terminal, 0.9.3/default and 0.9.1/named | The Codex TUI generated its own root callback. Claude fixture UserPromptSubmit → Stop → idle, OpenCode `session.execution.succeeded` and Pi non-aborted `agent_settled` used their managed CLI commands. Each set produced four distinct deliveries to the same verified occupant and four byte-identical 53-byte synthetic Markdown bodies, including Chinese text, emoji, a fence and indentation. Four unique local handoffs were confirmed per set. |
| Recovery when a short native hook exhausted its time budget | Two durable deliveries remained pending. After the original lease elapsed, an explicit outbox flush transferred exactly two different event IDs to the same local API origin. Final counts were four handed off, zero pending. |

The operator-facing launch and diagnostic fields are documented under
[Codex terminal ownership](compatibility.md#codex-terminal-ownership).
Kernel-session, foreground/daemon role, changed-occupant and bounded diagnostic
regressions live in `codex_terminal_test.go`. The hook measurement harness
launches its synthetic dispatch process inside its real tmux pane and answers
process-info queries on its synthetic Herdr socket. Its current wall-clock
figures include the explicitly named fixture host.

These runs exercised the actual Codex TUI and multiplexer processes with
controlled provider responses. The other three Agent completion inputs were
adapter-shaped fixtures; native adapter filtering was also tested separately.
The user’s server, production API, APNs acceptance, phone display and navigation
were outside these CLI runs.

## macOS Codex compatibility test selection (2026-11-20)

- On Intel macOS 15.8.1, the four `select_agent_samples_test.py` regressions
  checked the installation inputs. A Darwin request for the semantic minimum
  failed before emitting any install variables. Linux still selected 0.100.0.
  The six `darwin_codex_test.py` regressions exercised the lifecycle entry
  with counted targets. Old or mismatched packages, substituted files and
  wrappers, and denied or inconclusive system checks produced zero target
  invocations. Substitution after admission also stopped before execution.
  An admitted test program returning the wrong version or exit 7 failed.
- The [README configuration command](../README.md#build-and-verify) ran
  with Node 22.22.2, Python 3.12 and Go 1.27.1 in a fresh global npm prefix.
  The selected `@openai/codex-darwin-x64/vendor/x86_64-apple-darwin/bin/codex`
  matched the 0.163.0 SHA-256
  `73697f229103f463acaafd8659976caaa4892ec11e2c246c7fa97f93d523f90e`.
  Developer ID, notarization and Gatekeeper accepted the actual native target
  before its first invocation. All ten real configuration tests passed with
  Codex 0.163.0 and Claude Code 2.0.36, including native configuration loading
  after hook installation and after removal. The runner used disposable homes.
- Local Go tests passed for the sample manifest, stable and rejected versions,
  asdf launcher selection, probe isolation/failures, notifier preservation and
  encrypted event identity. Their 0.100.0 output came from controlled fixtures.
  No old-certificate macOS Codex was launched. The executable behind the
  originally reported system alert has not been identified.

## 2026-11-20 — Default Codex completion and queue race (D01/D03/D05)

The executable probe builds on a fresh checkout of this delivery. Run from the
CLI repository with Python 3.11+, the official Darwin x86_64 standalone Codex
0.171.0, and the indicated multiplexer. The script checks the selected binary's
SHA-256, notarized Developer ID and Gatekeeper assessment before the first
execution. It creates new private agent, multiplexer and pairing state for each
run. All prompts and replies are synthetic, and both model and notification
receiver listen on loopback. The script simulates human input solely in panes
it created. Production reply/navigation code never types into panes.

```sh
go build -trimpath -o mobterm-cli .
python3 scripts/test-codex-shared-daemon.py tmux --codex "$(command -v codex)" --cli ./mobterm-cli
python3 scripts/test-codex-shared-daemon.py herdr --codex "$(command -v codex)" --cli ./mobterm-cli --herdr "$(command -v herdr)"
go test -run 'TestCodexCompletionMetadata|TestUnboundCodexPayload' .
```

- Real tmux 3.7c and Herdr 0.9.5 (protocol 22), two default no-argument TUIs
  per backend, identical working directory and profile. Both tmux callbacks
  inherited `%0` with the TUIs in `%0` and `%1`. Both Herdr callbacks inherited
  `w1:p1` with the TUIs in `w1:p1` and `w1:p2`. Each pair shared a daemon PID.
  The CLI authenticated each main completion via the actual daemon socket.
  The local API received encrypted notices for both threads. The private
  source record contained the exact native thread, completed turn and peer birth.
- Independent native probe: for each pane, `thread/read`, `thread/loaded/list`,
  `thread/turns/list` and `thread/queue/list` showed the matching loaded, idle,
  direct-input main thread and completed turn with an empty queue. A single
  `thread/queue/add` addressed to that exact thread produced a matched queue
  receipt and a new turn displayed only in the original pane on both backends.
  These direct RPCs bypass App/SSH grants; this demonstrates the upstream
  mechanism, not a successful Mobterm mobile reply.
- Controlled stale-turn race on both backends: the simulated sender completed
  its final checks, then the fixture submitted a new terminal input. The local
  model held the new response at a barrier. A separate RPC connection confirmed
  a different `inProgress` turn. Sending the old instruction with the public
  add API still succeeded, appeared in the queue during that turn, and was
  actually consumed after the barrier opened. The sender that observed the
  new input in its own final check made zero native mutations. Queue/add in
  0.171.0 has no expected-turn guard. The CLI keeps ordinary Codex writes closed.
- `/new` in the second pane created a new thread while callbacks still named the
  first pane. With the other TUI open, the daemon remained reachable after
  replacing the tmux TUI with a shell or quitting the Herdr TUI. The most
  recently displayed thread was still loaded on the immediate post-exit
  sample. We found no current TUI-to-thread binding in this runtime that
  authenticates default navigation across a switch or pane reuse. No iPhone,
  APNs, deployed API or actual mobile navigation was exercised here.

## 2026-11-20 — Multiplexer release minima

The target verifier samples the tmux client's `-V` and the running server's `#{version}` on the named socket. Each structured pane sample still verifies membership and process birth. The Herdr raw socket path reads the server SemVer and required snapshot fields. Both reject unrecognized versions distinctly from a release below the floor. Validation used the current D11 working tree based on `ff3e7a1e77fdc8c632fb92bf7d4e52999083e5de`.

- `MOBTERM_REQUIRE_TMUX=1 go test -count=1 -run 'Test(MultiplexerReleaseFloors|LiveTmux|Herdr|ReadOnlyTargetServerExit|TargetStructuredFailure|RangeNotifyEncryptedIdentity)' .` passed with Go 1.27.1 and real tmux 3.7c. The live sockets covered capture, rename, move, replacement, removal and read-only resolution. Fixtures cover tmux 3.1/3.2/3.2a/3.7c/4.0 and Herdr 0.9.0/0.9.1/0.9.3/0.9.5/1.0.0, malformed versions, prereleases and protocol 23.
- Built with `go build -trimpath -o mobterm-cli .`. On macOS, a separate private HOME, XDG tree and Mobterm store hosted a checksum-verified Herdr 0.9.3 server with one synthetic workspace. `target verify --stdin-frame` and `target resolve --stdin-frame` each returned `status:valid` and the exact original locator. Future versions were exercised through fixtures.

### Embedded Codex (`--no-daemon`) and independent profiles (A3, A7–A10)

Using the same signed Codex 0.171.0, isolated loopback model/API and notification callback as the default-launch D01 probe, run each command above with `--no-daemon`. This variant also requires Node.js on PATH to authenticate and decode the synthetic AES-GCM envelope. The independently started native `app-server --listen stdio://` only exists inside the disposable probe. The shipping CLI does not start an app-server to obtain write access.

| Observation | Real tmux 3.7c | Real Herdr 0.9.5, protocol 22 |
| --- | --- | --- |
| First terminal completion | The notification for the exact thread and turn reached the paired fixture API. Its decrypted locator named `%0`; `target resolve --stdin-frame --read-only` returned valid. | Exact completion delivered; authenticated locator named `w1:p1` and resolved successfully. |
| Runtime and namespace | An independent app-server using another private CODEX_HOME could not read the thread. In the source profile it read the exact completed turn, but its loaded-thread list omitted the running TUI's thread. | Same results. |
| Queue acceptance and P6 | An independent same-profile queue/add produced a matched receipt and a new turn visible in the original TUI. While a later user turn was held inside the local model, the independent reader reported a different turn (its status was `interrupted`); the deliberately stale add still succeeded and was consumed. | Same results. |
| Terminal navigation boundary | `/new` created a different thread at the same pane; resolving the old locator still succeeded. Replacing the TUI process with a shell using `respawn-pane` made the old tmux locator fail. | `/new` produced the same mismatch. After the TUI quit back to its original pane shell, the Herdr terminal identifier still resolved. |
| Persistence after TUI exit | The old native thread's metadata remained readable via the independent app-server. | The old native thread remained readable too. |

The branch exercises `thread/read` metadata-only, exact turn/queue APIs, authenticated source decryption and actual CLI target resolution. It does not read the user's profiles, issue a restore, authorize a reply, or attach a phone. Embedded persistence and a live multiplexer target cannot establish the current displayed thread or an atomic expected-turn write. Same-tree child exclusion is covered separately by the CLI's source-origin tests; this embedded probe did not launch a real child Agent.

## 2026-11-20 — Source-bound wake after the ordinary reply helper exits (D01/D02/D06)

| Command and scope | Observed result |
| --- | --- |
| `python3 scripts/test-claude-native-reply.py "$CLAUDE_BINARY" "$CLI"` with Claude Code 2.1.328 | PASS. The official plugin validator accepted the generated Mod. A real interactive TUI changed session, prompt and generation on `/clear`. After the registration process exited, the same Mod timer observed the private marker without another user input or Mod restart. The new conversation's synthetic model input contained the exact reply. The Mod reported `native_accepted`. |
| `python3 scripts/test-plugin-native-reply.py opencode "$OPENCODE_BINARY" "$CLI"` with OpenCode V2 2.3.2 | PASS. The real TUI plugin woke after registration helper exit. A matching prompt admission and inbox delivery produced `native_accepted` and `native_consumed`. The loopback provider received both literal messages in the same history. |
| `python3 scripts/test-plugin-native-reply.py pi "$PI_BINARY" "$CLI"` with Pi 1.5.2 | PASS. Observed input during the first turn prevented its late settled event from reopening a source. The next valid source woke after its helper exited. The original TUI received the exact reply once, including whitespace, Unicode and literal template characters. The extension reported `delivery_unknown` because its public call returns no matching input ID. |
| `node scripts/test-native-adapters.mjs` | PASS. The shipped modules exercised source changes, pending requests, cleanup, late authorizations, original request ownership and delivery evidence. |
| `go test -race -count=1 -run 'TestReply(ReopenAfterHalfHour\|RegistrationHandsOffToRunningHost\|CrossPairClaimAndReceipts\|WatermarkFollowsSessionAcrossPanesAndSSHProfiles)\|TestClaudePromptCycleStaysInOriginatingNamespace' ./...` (focused runs) | PASS. The CLI registration entry point returned its private wake handle and exited. A record with a departed helper and a deadline 30 minutes in the past required a new grant and host lease. Incorrect owner and duplicate wake were rejected, and a single claim removed the literal text before dispatch. A legacy frozen decision survived outbox removal, parent exit and missing logical-scope metadata. The exact native run could not register again with a new source key. A fresh input in a different pane advanced the original session watermark. An identical native session ID in a separate tested configuration retained an independent watermark, and an SSH caller with another configuration still consulted the stored source. |

All three host scripts used isolated configurations and a loopback synthetic
model. Their decision helper supplied a synthetic authorized decision and had to
exit before the fixture wrote the wake marker. The Go tests separately exercised
the actual CLI store, grant and claim machinery with local fixtures and a
controlled expired timestamp. This separates the observed original host
lifecycle from the 30-minute expiry calculation. These checks did not observe
30 minutes of real elapsed host time, an iPhone or a deployed service.


### Live navigation on the original strong terminal

Build `go build -trimpath -o "$CLI" .` and run `PI_BINARY="$PI_BINARY" MOBTERM_NAV_CLI="$CLI" go test -count=1 -v -run '^TestLivePiNavigation$' .` with an absolute Pi executable and CLI path. The opt-in fixture uses a private tmux server, temporary home and a real Pi TUI. An independent witness submits one synthetic reminder using the native session ID and actual parent PID. A local TLS fixture authenticates the signed Pair-status lookup. The installed production adapter and CLI handle the challenge. On tmux 3.7c, both Pi 1.5.0 and 1.5.2 returned a fresh strong locator for the original notification and the new window after moving its pane within the original tmux session. After `/new` in the same pane and foreground PID, another signed request for the same Pair, event and envelope returned `navigation_binding_unconfirmed` with no locator. No model, production Pair, phone or Apple service was involved.

The installed-adapter scripts above also issue read-only navigation challenges to the real OpenCode 2.3.2 and Pi 1.5.2 TUIs. Claude 2.1.328’s real Mod confirmed two distinct native sessions in the same foreground host around `/clear`. They validate the current native session and the direct parent of both child commands with a private fixture helper. The Go fixture exercises that helper boundary with the actual CLI. The tests do not establish navigation after a move to a different strong terminal.

Final integrated checks on Go 1.27.1, darwin/amd64, with real tmux required: `MOBTERM_REQUIRE_TMUX=1 GOFLAGS=-timeout=35m go test ./...` passed in 692.235s and `MOBTERM_REQUIRE_TMUX=1 GOFLAGS=-timeout=35m go test -race ./...` passed in 878.887s. Formatting, `go vet ./...`, the trimmed macOS build and a linux/amd64 cross-build passed. The macOS binary created and re-read the same installation identity in an isolated store, and rejected an invalid framed `reply latest` request. The final native adapter contract suite also passed. The ordinary Codex product reply and navigation to a different strong terminal remain open.

## 2026-11-20 — Shared Codex completion callbacks racing on first registration (D04/A5)

`TestCodexCompletionFirstRegistrationOrder` uses two local RPC connections, the production current-thread/current-turn parser, a channel after A's successful check, the private run store and per-Pair envelope encoding. With confirmation outside registration (CLI baseline `701a7d9a4e28f073e0e4a8a6233e28566b5ec74a`), B registered while A was paused. After B then A were encoded, the persisted head was A with order 2. With the session-scoped advisory lock, B's bounded command cannot pass the paused registration. After A is released, B registers and remains the visible head even when A's envelope is encoded last. A duplicate B keeps its original order. In a separate B-first store, a late first A is rejected after reopening the store and allocates no order or intent. Capacity retries re-enter confirmation after the flush. The probe uses synthetic native RPC responses and performs no native input mutation.

The same signed Codex CLI 0.171.0 was exercised through `notify --source codex` with `scripts/test-codex-shared-daemon.py tmux --first-registration` and `herdr --first-registration`, supplying the built CLI, pinned Codex and Herdr binary as in D01. A Unix-socket barrier held A’s actual legacy callback before its first CLI invocation until B was delivered. On tmux 3.7c and Herdr 0.9.5 (protocol 22), the isolated local API received exactly B’s encrypted event. Both stores contained one run with its verified daemon peer and B’s visible head at order 1. The released A failed with `codex_completion_turn_unconfirmed`. The native callbacks and local delivery were real; APNs and an iPhone were outside this probe.

## 2026-11-20 — Running native sessions restored into another terminal (D02/D06/D10)

`TestRegisteredHostRebindsOnlyUnclaimedMatchingTurn` exercises a privately installed adapter credential, two names for one physical file, ambiguous live processes, a changed turn or storage, one successful claim of a new waiter, stale lifecycle messages, uninstallation and a frozen decision. It preserves the original source, terminal and Pair responsibility. The CLI's local registry contains the current native host PID/birth, physical storage identity and logical session. The SSH request and response formats are unchanged.

`TestPhysicalSessionHeadsPreserveCopiesAndFrozenHistory` drives Pi activity capture, source registration, per-Pair envelope encoding and an authenticated reply decision through the local store. Two files with identical contents and identical native IDs remain independently current. A later turn through a hard link in another fixture pane supersedes only the original file’s reply cursor and visible head. The test decrypts the production envelope with the Pair key, checks the exact config-only retirement digest and measures ciphertext and serialized APNs sizes. The other Pair’s suppressed reminder carries no retirement field. A reminder for one Pair retires that Pair’s older unscoped head even if an older intent is encoded afterward. The other Pair retains its visible head. A late callback with an exact configuration-only cursor and native request ID cannot authorize input after a physical cursor advances. The original frozen decision remains queryable and duplicate registration is refused. `TestClaudePromptCycleSeparatesCopiedTranscriptsBeforeFirstWrite` covers prompt callbacks received before transcript creation and separate Stop and idle callbacks on two copies. These controlled checks use fixture inputs and a local API.

On macOS with real tmux 3.7c and Pi 1.5.2, `TestLivePiNavigation` used signed latest requests on a synthetic reminder. It covered the original pane, a move between windows, `/new`, reuse, user restoration in another pane/process, distinct files with the same session ID and two live hosts of the same inode through hard links. `TestLivePiReboundReply` exercised the production CLI entry point, a local TLS relay with fixture grants, actual Pi completion, signed inspect/wake/rebind/send, native authorization, exact input in the original model history and status after host exit. Pi reported `delivery_unknown`. These tests require `PI_BINARY`, and navigation also takes the freshly built `MOBTERM_NAV_CLI`. They used a local model and did not exercise SSH transport, APNs or an iPhone.

In a separate OpenCode V2 2.3.2 probe, an isolated interactive `--standalone` TUI and an isolated managed-service TUI each loaded the installed plugin and recorded their actual host PID/birth, logical session and physical database through the production CLI. The CLI verified the database was open in the local server and the server was its child or connected through a matching established loopback socket. Linux's corresponding process/socket inspection was cross-built; these live checks used macOS.

### Native adapter lifecycle and submission (A7/A9–A11/A13/A16)

The CLI and installed adapter assets must be updated together and the managed adapter reloaded. These tests use temporary Agent configurations, loopback providers and local notification fixtures. They do not install anything on a paired user's real device or service.

- `scripts/test-claude-native-reply.py "$CLAUDE" "$CLI" --resume` exercises the installed Mod in Claude Code 2.1.328. A user-launched fresh TUI with `--resume` reported the original session and transcript path, but no current `promptId`. Reading the single last native message with `$.session.messages({last:1})` provided the same assistant row UUID on both sides of restoration. The Mod sends only the UUID as turn evidence. The test first runs `/clear` and samples navigation in both conversations, restores the second conversation in another PTY/PID, samples it again and simulates an unused first rebind lease. It wakes the restored TUI a second time with its new owner token, which submits the exact text once; the loopback model sees it in the original history. The decision/grant helper in this script is a fixture. The native result is `native_accepted`.
- `scripts/test-plugin-native-reply.py opencode "$OPENCODE" "$CLI" --resume` exercises the OpenCode 2.3.2 TUI plugin with a private standalone server, a fresh PTY and the original session ID. The original completed execution and the restored latest native idle message supply the same message ID. The decision helper is a fixture. The actual OpenCode queue acknowledges the input and emits the matching delivery; its original model history includes the literal text. Local service/database ownership is checked separately in the shipping CLI.
- `scripts/test-plugin-native-reply.py pi "$PI" "$CLI" --resume` exercises Pi 1.5.2 with the same native session file and completed assistant entry in a user-started new TUI. The witness sees the original messages and exactly one mobile input. The extension reports `delivery_unknown` because the send call supplies no correlated acknowledgement.

The opt-in Go tests in `live_rebound_reply_test.go` also run the production CLI host registration, source/recipient authentication and frozen dispatch paths with real TUIs and a loopback TLS receiver, fixture grants and synthetic model. Use the pinned Agent binaries and run individual tests with `MOBTERM_REQUIRE_TMUX=1`.

| Host and command | Observed result |
| --- | --- |
| `CLAUDE_BINARY="$CLAUDE" go test -count=1 -run '^TestLiveClaudeReboundReply$' .`, Claude Code 2.1.328 | The native Stop/idle_prompt pair produced the encrypted notification. After user restoration, the new host lacked TMUX/TMUX_PANE. Signed latest returned no locator; signed inspect and send reached the matching session. The model saw the exact follow-up and status remained native_accepted after the new process exited. |
| `OPENCODE_BINARY="$OPENCODE" go test -count=1 -run '^TestLiveOpenCodeReboundReply$' .`, OpenCode 2.3.2 | The original pane was reused. The user restored the session in a different pane with a private standalone server, and signed latest identified the new strong pane. The signed path reached the managed plugin, validated the native database and service, and the loopback model saw the exact user input in the original history. The original decision remained native_consumed after host exit. A short notify timeout can leave a durable intent or send lease; the test uses the normal outbox flush to recover. |
| `PI_BINARY="$PI" go test -count=1 -run '^TestLivePiReboundReply$' .`, Pi 1.5.1 and 1.5.2 | An actual completed Pi turn led to signed latest for the new strong pane, followed by signed inspect and send on the new host after pane reuse. Pi exposed no correlated native receipt, so status remained delivery_unknown after exit. Pi 1.5.0 was exercised for navigation only. |

An additional local navigation fixture restored a Pi 1.5.2 session that originated in tmux 3.7c into Herdr 0.9.5. The old pane had been replaced, and a fresh signed latest for the same Pair/event/envelope returned the new Herdr pane and terminal ID. This used a synthetic original notice and the production CLI and plugins. The phone path was tested separately with simulator fixtures.

## 2026-11-23 Agent TUI compatibility, round 1 v1.0

This round used macOS 15.8.1 x86_64, Go 1.27.1, Node 22.22.2, Python 3.12 and tmux 3.7c. The four fixed release tags resolved to the commits in [compatibility](compatibility.md#notification-version-policy-and-fixed-samples). The npm latest tags for these package names matched the snapshot. Each test had its own temporary HOME, work directory and Agent configuration. Native reply and notification scenarios used loopback fixtures and synthetic messages.

| Official package or release | Artifact checked this round |
| --- | --- |
| [Codex npm 0.171.0](https://registry.npmjs.org/@openai%2fcodex/0.171.0) | Verified npm SHA-512 integrity for the root tarball and both Darwin platform tarballs before hashing their contents. Launcher SHA-256 `0e0c66f50074965a40cbd12f625b0a35963baf41bbd8c201a93e32fddbe2e073`, x64 executable `0cc2c99ee61041ecde0c848f85861ea9cb6e45c5c136fd66383a7eb2198e307c`, arm64 executable `fc8d77cf7585aded2b9bc487dad29c4ee2f066123e4e76dd3b17653a8322ce42`. Only x64 executed, after Developer ID, notarization and Gatekeeper preflight. The shared/embedded fixture independently checked the signed standalone x64 binary with the same hash. |
| [Claude Code npm 2.1.330](https://registry.npmjs.org/@anthropic-ai%2fclaude-code/2.1.330) | Isolated global npm prefix, Darwin x64 executable SHA-256 `bbed99bb8a39bb3f94389c5c99d3d02cb75f3f983b5d1bc40b1b0d1efca84713`. The actual version command reported `2.1.330 (Claude Code)`. |
| [OpenCode V2 2.3.2](https://github.com/anomalyco/opencode/releases/tag/v2.3.2) | Official `opencode-darwin-x64-baseline.zip` SHA-256 `6dc5deb4554df035d27dbb3a4663d96954969a33079aeb6b01dd9de2d9092545`. The local executable matched the archived entry SHA-256 `5d188d68f808570aa1f8826c9d6bd7d9ade18e9bac8961aa3c0079f41e2850e8`. The TUI reported `2.3.2`. |
| [Pi coding-agent npm 1.6.0](https://registry.npmjs.org/@earendil-works%2fpi-coding-agent/1.6.0) | Isolated npm install with the CI peer pins. The selected `dist/bundle/cli.js` SHA-256 was `e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774`. The host's own loader reported `1.6.0`. |

The following command arguments use the absolute executable paths from those isolated packages. `$CLI` was built using `go build -trimpath`. Copying or updating the managed files does not reload an already running user TUI.

| Command and layer | Observed result |
| --- | --- |
| `go test -count=1 -run 'TestCompatibilitySamplesMatchPolicy\|TestAgentDiscoveryContractAndNoInitialization' .` and `python3.12 tests/select_agent_samples_test.py -v`, `python3.12 tests/darwin_codex_test.py -v` | PASS. Manifest/descriptor and the read-only discovery fixture agreed. Four selector tests and six guard tests passed. A guard subprocess initially exceeded its five-second timeout during simultaneous builds. The unchanged guard passed on rerun. |
| `tests/linux_lifecycle_test.py --config-only --binary "$CLI" --agent-prefix "$PREFIX" --expect-codex-version 0.171.0 --expect-claude-version 2.1.330 -v` with Python 3.12 and the npm prefix on PATH | PASS, ten tests. This is the first current sample in this suite which installs native Codex hooks inside the user's JSON file. The initial legacy fixture treated this file as always untouched. The updated fixture checks exact restoration of a third-party command, preservation of both files on conflicting edits and cleanup after the user restores the owned command. Native configuration loading, original notify chaining and unrelated Claude hooks were exercised. |
| `scripts/test-codex-shared-daemon.py tmux --cli "$CLI" --codex "$CODEX"`, repeated with `--no-daemon`; `herdr --first-registration --herdr "$HERDR" --cli "$CLI" --codex "$CODEX"` | PASS. Two tmux TUIs sent native completions from distinct threads through the same shared daemon. The local relay received five encrypted events. The interposed turn demonstrated stale native queue admission, and the product guard issued zero ordinary writes. Embedded mode verified the original strong pane and lost the locator on exit. Herdr 0.9.5 protocol 22 accepted only B when callback A was held until after B. |
| `scripts/test-native-adapters-host.py opencode "$OPENCODE" "$CLI" 2.3.2` and `scripts/test-native-adapters-host.py pi "$PI" "$CLI" 1.6.0 "$PI_PACKAGE_ROOT"` | PASS. The generated OpenCode plugin registered 18 listeners in each of two real TUI launches. Pi's published loader imported the generated extension, and a real TUI session registered 16 listeners. These loading probes terminated their own TUIs after recording registration. |
| `scripts/test-claude-native-reply.py "$CLAUDE" "$CLI"`, repeated with `--resume` | PASS in both the original and user-restored interactive Claude TUI. Plugin validation and classic/Mod callbacks succeeded. `/clear` rotated identity, the native session was checked around navigation, and the synthetic provider saw the exact reply in the intended history. |
| `scripts/test-plugin-native-reply.py opencode "$OPENCODE" "$CLI"` and `scripts/test-plugin-native-reply.py pi "$PI" "$CLI"`, each repeated with `--resume` | PASS in each original and restored TUI. OpenCode emitted `native_accepted` then `native_consumed`. Pi observed the intervening user input, final settled run and literal follow-up once in the same history. Its shipping receipt remained `delivery_unknown`. The native API exposes no correlated acknowledgement to the calling extension. |

The macOS CI configuration pair also passed all ten lifecycle tests (81.546 s) with the same signed Codex 0.171.0 and a separate npm prefix for Claude 2.0.36. The current/current configuration run took 29.412 s.

With `MOBTERM_REQUIRE_TMUX=1` and the corresponding `PI_BINARY`, `CLAUDE_BINARY` and `OPENCODE_BINARY`, `go test -timeout 14m -count=1 -v -run '^TestLive(Pi|Claude|OpenCode)ReboundReply$' .` passed all three host cases (161.188 s). Each exercised the real installed adapter, private tmux, authenticated latest/inspect/send, synthetic model and local TLS relay. Pi 1.6.0 and OpenCode 2.3.2 returned the restored strong pane. Claude 2.1.330 restored without tmux metadata, so navigation correctly had no locator. The final signed receipts were `delivery_unknown`, `native_consumed` and `native_accepted` respectively for Pi, OpenCode and Claude. Pi and Claude exercised the existing outbox flush recovery. After sharing the live fixture with the new permission test, an additional OpenCode rebound run passed in 80.42 s, including due-record flush recovery.

Four official OpenCode Darwin x64 servers ran separately on loopback with isolated HOME and XDG paths. The native permission API created six synthetic requests across two sessions. In session one, a/b shared a source message, c had a different message, and d/e had no source. Session two held f. For each server, `GET /api/info` returned the expected version and process ID.

| OpenCode server | Executable SHA-256 | Pending requests after native reject |
| --- | --- | --- |
| 2.2.5 | `5e0cf9c94ee0fd4a4635807534e9ca4bf51ffe1c83260ada3510bf60e7333173` | Reject(a) left f only. |
| 2.3.0 | `06b36d6b341e4bc248d982fd634d9075baa804383ff4a030b923888ef7a7095a` | Reject(a) left f only. |
| 2.3.1 | `235a6377bf3dbef847eb9828efb8bca1666d04b9b699cc67d50b029d6c6f2d99` | Reject(a) left c/d/e/f. Reject(d) then left c/f. |
| 2.3.2 | `5d188d68f808570aa1f8826c9d6bd7d9ade18e9bac8961aa3c0079f41e2850e8` | Reject(a) left c/d/e/f. Reject(d) then left c/f. |

`OPENCODE_BINARY="$OPENCODE" go test -count=2 -run '^TestLiveOpenCodePermissionScope$' -v .` passed both runs on 2.3.2 (9.47 s and 8.27 s). The real standalone TUI's loopback model requested two read-only files in one assistant step. The test relay received both encrypted needs-input reminders and selected the higher revision. An early fixture run timed out with one reminder pending in the normal outbox. The maintained fixture uses the same due-record flush recovery as the existing reply test. Through `interaction inspect`, the CLI returned digest-bound request v2 with the actual server version, process birth, message ID and both native request IDs. A signed deny on that request reached the native adapter. After the separate receipt writer finished, `interaction status` returned `response_written`. In the earlier diagnostic probe, a second reminder arrived between inspect and respond. The formerly current request returned `interaction_source_changed`. A status query during dispatch can temporarily return `delivery_unknown` after the waiter exits and before the adapter records the result.

The Python reply probes supplied decisions through a fixture helper. Configuration loading, real host callbacks, synthetic provider history and local encrypted relay acceptance are distinct observations. Production model accounts, production remote servers, phone display, APNs and Linux/arm64 host execution were outside these local runs.

### Final CLI checks on darwin/amd64

- `test -z "$(gofmt -l .)"`, `go vet ./...`, `go build -trimpath -o mobterm-cli .` and `git diff --check` passed. The resulting binary reported `mobterm-cli dev unknown darwin/amd64`. In an isolated HOME and configuration, `./mobterm-cli status --agents-json` performed all four real executable probes. Every probe reported `ok`, and the actual versions were Codex 0.171.0, Claude Code 2.1.330, OpenCode V2 2.3.2 and Pi 1.6.0.
- An initial ordinary run during development hit Go’s default ten-minute package timeout in `TestReplyNativeCompletionRendezvous`. Earlier in that run `TestMessageBodyClaudeTargetAndStopOwnership` lost its body candidate during concurrent simulator work. The optional Stop target probe has a 750 ms budget. With unchanged code, five focused repetitions passed in 6.324 s, and the case passed in subsequent full runs.
- For the integrated runs, `MOBTERM_REQUIRE_TMUX=1` required real tmux. `GOFLAGS=-timeout=45m` set the package timeout, and `-count=1 -json` disabled result caching and recorded each case. The first `go test ./...` with these settings took 590.103 s. Eight five-second fixture launcher preflights in `TestAsdfCompatibilityEntrypoints` timed out while the simulator restarted. The result stream recorded 816 passes, six skips and eleven failures counting the three enclosing cases. The fixture and product timeouts were unchanged.
- `go test -race -count=1 -json ./...` passed in 694.467 s with 861 passing tests/subtests, six skips and no race reports. The ordinary and race suites ran serially.
- After the race suite finished, the complete ordinary suite was rerun. `go test -count=1 -json ./...` passed in 539.218 s with 861 passing tests/subtests, six skips and zero failures. This included all eight previously timed-out preflights.
- Five opt-in native host cases and one subprocess helper skip by design in both unconfigured full suites. The three `TestLive*ReboundReply` cases and `TestLiveOpenCodePermissionScope` ran separately above. With Pi 1.6.0, the final trimmed CLI and `MOBTERM_REQUIRE_TMUX=1 PI_BINARY="$PI" MOBTERM_NAV_CLI="$CLI" go test -timeout 3m -count=1 -run '^TestLivePiNavigation$' -v .`, the navigation scenario passed in 7.77 s. It launches `TestNavigationEventSource` as a subprocess and checks pane movement, a new session, pane reuse, a rebound source and two concurrently active copies.

## 2026-11-23 — QuickCommand notification v3

On Intel macOS with Go 1.27.1, the focused command
`go test -run '^TestQuickCommand(V3Golden|FrozenOutbox)$' .` passed.
The final lost-response test also passed with `-race`. Go sealed
the public `protocol-v3.json` vector byte-for-byte. The queue tests persisted
one original Pair/event, dropped the HTTP response after reading the POST, retried the identical
envelope, accepted a matching HTTP 429 durable handoff, and refused a version
4 record before sending. The sealer rejected missing job notification intent, closed gates, mismatched
Pair/installation, stale receipts, invalid QC generations and inconsistent terminal metadata.

A separate local cross-repository run seeded an isolated outbox using the
production Go sealer and a fixture terminal result (exit 23). The fixture read
its exact Pair capabilities over HTTP from workerd, including both QC
generations, and passed that receipt to the sealer. The built CLI ran
`outbox flush --pair … --json` through a loopback bridge to the real Worker
route in Miniflare/workerd. Local D1 accepted it and retained the ciphertext
with mock APNs returning 503. A second flush made no POST. The App's production
Swift envelope and activity files, compiled on macOS, authenticated the envelope
read from the classified inbox and recovered the original job, installation,
command name and exit code. The fixture, Worker and Swift consumer used a
synthetic Pair. This run did not exercise a remote job, real APNs, an iPhone
notification, or navigation. CLI automatic completion-policy evaluation and
terminal-to-outbox creation are still pending. No Linux runtime was exercised
in this notification interop run.

## 2026-11-23 — QuickCommand terminal evidence and display-name compatibility

On darwin/amd64 with Go 1.27.1, the Go v3 codec and result-v1 codec checked
identical rules for exit code and signal for all five terminal states. The
boundary test exercises 120/121 scalars, multi-byte names, surrounding
whitespace and disallowed control characters. The public v3 fixture agrees
with the App and Worker copies; the public result-v1 fixture agrees with the
App copy. The read-only CLI command returned `result_unavailable` for an
uninitialized fixture without creating its store, and `result_expired` for
the expired result vector. This run did not launch a background command.

Formatting, vet and trimpath build passed. The initial ordinary suite reached
Go's default ten-minute package timeout on an unrelated message-body test.
The suite with the repository's 45-minute package timeout passed 903 tests
and subtests with six opt-in fixture skips; tmux 3.7c and Herdr 0.9.3 were
available for their interoperability tests.

The race suite on the same source passed the same 903 tests and subtests
with the same six skips in 760.268 seconds. No race reports were emitted.
