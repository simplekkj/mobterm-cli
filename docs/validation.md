# Validation records

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
