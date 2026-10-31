# Validation records

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
