# mobterm-cli

A short-lived remote notification helper for Mobterm. It reports verified Agent
events from tmux or Herdr, encrypts the terminal locator for the paired phone,
and exits. Explicit opt-in enables bounded native approval and interaction
bridges over trusted SSH, with per-Agent/version limits. It does not run a daemon,
listen on a port, read transcripts, upload terminal content, inject terminal keys,
or run remote commands on behalf of the API.

Linux and macOS are supported on amd64 and arm64. This repository contains only
the independent Go CLI and public protocol; it does not license other Mobterm
repositories. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md).

## Documentation map

- **Use and development:** [Install](#install), [Pair and use](#pair-and-use),
  [Upgrade](#upgrade), [Build and verify](#build-and-verify), and the
  [current command budgets and diagnostics](#notification-api-request-deadlines).
- [Read-only capabilities and workspace discovery](docs/workspace-discovery.md) over an authenticated SSH connection.
- Start a [native QuickCommand job](docs/quick-command-job-v1.md) from a trusted SSH session. The short submission returns after the one-shot worker accepts the command. The worker keeps running after disconnect and sends completion to the original Pair.
- [Read an encrypted QuickCommand result receipt](docs/quick-command-result-v1.md) for its original job and compare its terminal state with the [completion notification](docs/notification-v3.md).
- **Protocol and operation:** [v1 setup, notifications and targets](docs/protocol-v1.md),
  [v2 state/inbox/badge and legacy approval wire v3](docs/protocol-v2.md),
  [QuickCommand completion notification v3](docs/notification-v3.md),
  [independent interaction wire v1](docs/interaction-v1.md),
  [native interaction support](docs/native-interactions.md), [Codex conversation append](docs/reply-v2.md),
  [notification compatibility and fixed samples](docs/compatibility.md), and
  [security](docs/security.md). These are separate contracts, not a system-wide
  version upgrade; v1 AAD and compatibility remain supported.
- **Engineering constraints:** [AGENTS.md](AGENTS.md).
- **History and provenance:** [dated validation records](docs/validation.md),
  [Codex P1 measurement evidence](https://github.com/simplekkj/mobterm-cli/blob/67ba0a446c1e524b9d686838878b9aa43905f72a/docs/evidence/codex-p1-latency/README.md),
  [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY.md). Historical PASS
  results do not establish current runtime, release or phone delivery status.

## Install

Install the latest published stable Release (no Go, jq or sudo required):

```sh
curl -fsSL https://raw.githubusercontent.com/simplekkj/mobterm-cli/main/install.sh | sh
```

Or pin an **existing** stable Release, replacing `vX.Y.Z` with its exact tag:

```sh
curl -fsSL https://raw.githubusercontent.com/simplekkj/mobterm-cli/main/install.sh | sh -s -- --version vX.Y.Z
```

The script detects Linux/macOS and amd64/arm64, downloads the matching archive
and `checksums.txt` from the same tag, verifies SHA-256 and safe archive entries,
then installs only the binary to `$HOME/.local/bin/mobterm-cli` with mode 0755.
It uses POSIX sh, curl, tar, and sha256sum or shasum (plus standard Unix tools).
An existing regular CLI file is replaced atomically; repeat installation is safe.
Any failure before the atomic replacement preserves the old binary and its mode.
Failures and caught HUP/INT/TERM signals clean this run's downloads and staging
file, then remove only installation directories created by this run that remain
empty. Existing directories and their permissions are preserved. Cleanup failures
produce warnings for manual inspection; nonempty directories are retained.
Successful replacement commits the binary even if later cleanup or output fails.
SIGKILL, power loss and concurrent replacement of installation paths cannot be
fully recovered automatically; inspect reported leftovers without deleting
unrecognized files. Symlink or non-directory installation directories and
symlink/non-regular binary targets are refused.
HTTPS and the same-source checksum establish transfer
integrity, not an independent digital signature. macOS binaries are unsigned
and not notarized; Gatekeeper approval is not claimed.

**A published Release is required.** These instructions do not imply that the
first Release has been published; without one, installation fails explicitly.
The default latest selection excludes drafts and prereleases and must resolve
to a stable `vX.Y.Z` tag in this repository.

Run installation as the OS user that runs the Agent. Check the installed version:

```sh
"$HOME/.local/bin/mobterm-cli" version
```

Ensure `mobterm-cli` is on the remote non-interactive SSH PATH as well as your
interactive PATH, or use the absolute path above. For the current shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

An interactive shell's startup file may not be read by non-interactive SSH;
configure that environment's PATH separately. The installer only prints a PATH
hint and never edits shell startup files or SSH configuration. Keep the binary
at the absolute path recorded by Agent hooks; reinstalling or upgrading at that
same path preserves their executable reference.

The script does not pair, install/refresh hooks, or modify `~/.config/mobterm`,
`CODEX_HOME` or `CLAUDE_CONFIG_DIR`. Continue configuration in the corresponding
saved Mobterm App connection as described below. For later explicit upgrades,
use `mobterm-cli upgrade --version vX.Y.Z` (see [Upgrade](#upgrade)).

For manual installation, stable tags produce these assets at
[GitHub Releases](https://github.com/simplekkj/mobterm-cli/releases):

| System | Architecture | Asset |
| --- | --- | --- |
| Linux | Intel/AMD 64-bit | `mobterm-cli_vX.Y.Z_linux_amd64.tar.gz` |
| Linux | ARM 64-bit | `mobterm-cli_vX.Y.Z_linux_arm64.tar.gz` |
| macOS | Intel | `mobterm-cli_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS | Apple Silicon | `mobterm-cli_vX.Y.Z_darwin_arm64.tar.gz` |

Download the matching archive and `checksums.txt` from the **same explicit tag**.
In an otherwise empty download directory, run `sha256sum --check --ignore-missing
checksums.txt` on Linux or `shasum -a 256 --check --ignore-missing checksums.txt` on
macOS and confirm the selected archive reports `OK`. Extract the verified archive,
then install its `mobterm-cli` executable with mode 0755 into a user-owned directory.

For advanced source installation, build with the toolchain pinned in go.mod:

```sh
go build -trimpath -o mobterm-cli .
install -d -m 700 "$HOME/.local/bin"
install -m 755 mobterm-cli "$HOME/.local/bin/mobterm-cli"
"$HOME/.local/bin/mobterm-cli" version
```

## Pair and use

Install the CLI on a connection already saved in Mobterm, then use that
connection's Agent Notifications screen to pair. An available compatible API
and APNs provider must be configured by the service operator; this source tree
does not imply a deployed service or invent a service URL.

Pairing uses an authenticated, host-verified SSH byte stream. The App queries
`status --installation-json`, creates a pending pair, and supplies a bounded
stdin frame to `setup --stdin-frame`. Never paste the packet into shell argv,
environment variables, a URL, terminal input, or a log. A packet contains secrets.

Install hooks after checking the [version compatibility](docs/compatibility.md):
Codex >=0.100.0, Claude Code >=2.0.36, OpenCode >=2.0.0, or Pi coding-agent
(`@earendil-works/pi-coding-agent`) >=1.1.0, compared using SemVer precedence. `status --json` reports
the detected version, minimum and refusal reason. Its `supported` flag means the
version policy passes; it does not establish live notification delivery. Each active host must still provide the required event schema. For ordinary message
replies, see the [per-action minimum versions and host loading instructions](docs/native-interactions.md).

Before pairing, `status --agents-json` can discover the four Agents without
creating Mobterm state or contacting the notification API. It distinguishes a
missing entry from a failed check and reports runtime and configuration separately.
See the [discovery contract](docs/protocol-v1.md#read-only-agent-discovery).

```sh
mobterm-cli status --agents-json
mobterm-cli hooks install --agent codex --json
mobterm-cli hooks install --agent claude --json
mobterm-cli hooks install --agent opencode --json
mobterm-cli hooks install --agent pi --json
mobterm-cli status --json
mobterm-cli test --pair PAIR_ID --json
```

Restart Agent sessions after hook installation. Codex retains native final-turn
`notify` as its single completion source; verified 0.166.0+ installations also add
owned native observation and PermissionRequest hooks, requiring native `/hooks`
review before activation. Claude adds tool and PermissionRequest observation;
completion is delayed `idle_prompt` associated with the current prompt cycle and
its Stop, never Stop alone. Existing user hooks and decisions are preserved. Herdr uses these
same native Agent hooks, with an explicit structured terminal query. No second
Herdr event forwarder is installed.

Codex native hooks follow the existing representation in the selected `CODEX_HOME`:

| Existing native event definitions | Installation target |
| --- | --- |
| Only `hooks.json` | Merge into `hooks.json` |
| Only inline TOML | Keep inline hooks in `config.toml` |
| Neither | Create native definitions in `hooks.json` |
| Both | Move only proven Mobterm entries to the user's existing representation; refuse if user events remain in both |

`notify` stays in `config.toml` as the single completion source. `hooks.state`
trust/enable settings stay untouched in TOML and do not count as inline events.
Old Mobterm inline installations remain supported; when user JSON hooks coexist,
`status --agents-json` reports `partial`, enabling the App's repair action. Run
`hooks install --agent codex --json` (or repair in the App) to migrate them.
The same repair state upgrades legacy notify-only installs on Codex >=0.166.0.
No discovery/status command performs a migration. Restart Codex and review the
result in `/hooks`; moving a hook source does not grant trust.

For Codex versions with a shared app server, start a terminal-owned TUI in the
desired Herdr or tmux pane with `codex --no-daemon` (or
`codex --no-daemon resume`). A shared server's hooks cannot identify which
attached TUI owns the turn. Mobterm requires the hook host to belong to the
captured pane before recording state, a reminder or an action.
`status --json` reports the latest refused Codex completion in the Codex hook
health field `last_terminal_owner_refusal_at` (Unix seconds), when available.
Hook installation, trust review and the user's chained notifier keep their
existing behavior. See [Codex terminal ownership](docs/compatibility.md#codex-terminal-ownership).

Migration journals cover both files and ownership. The old source is removed
before the new source is written, so interrupted migrations can temporarily omit
Mobterm native handlers until the same operation is resumed, without duplicating
them. Changes to either file during recovery cause a conflict instead of an
overwrite. Retain the journal and finish the pending action with this CLI.
Uninstall removes only recorded Mobterm groups; existing JSON values and later
user groups are retained. JSON formatting may change, and an empty `hooks` object
may remain after uninstall. Do not manually move managed entries between files.
CLIs predating JSON hook ownership cannot manage these installations; uninstall
with the new CLI before downgrading. Pairing and notification wire formats are unchanged.

OpenCode uses a TUI-only plugin: only successful completion of the currently
visible root session and verified pending permission/form requests in its family
form completion/wait reminders. Revision 2 also records native start, tool,
failure and interruption observations; background roots, child completion and
non-TUI frontends remain excluded. Pi records native start/tool/UI-prompt
intervals and final `agent_settled`; only explicit aborted=true is interruption.
Pi prompt observation does not provide a native approval handle. Neither source
reads prompts, responses or transcripts. Ordinary state updates do not push or
increase unread; abnormal-end reminders are opt-in.

The [v2 protocol](docs/protocol-v2.md) negotiates state/inbox support before
sending. New App notification taps open details; terminal navigation is explicit.
API stores finite ciphertext history and supplies an absolute badge. Mobile
approval is separately opt-in per Pair/Agent, default off, and uses trusted SSH
inspect plus an authenticated one-time decision. It never injects terminal keys,
changes tool input, or grants permanent/session permissions. Read, approve and
terminal navigation are separate actions. Native trust/loading, server admission,
APNs acceptance and actual device display are separate evidence.

Successful new-source installation returns `activation: restart_required`: files
are configured, but the Agent must restart/reload. `hooks` booleans describe
owned files/references matching on disk, not whether a running Agent loaded them.
OpenCode uses `OPENCODE_CONFIG_DIR` or `(XDG_CONFIG_HOME or ~/.config)/opencode/cli.json`
(JSONC preserved); Pi uses `PI_CODING_AGENT_DIR` (including leading `~`) or
`~/.pi/agent/extensions`. No Agent/runtime is installed or upgraded. Uninstall
uses recorded paths and works even after the Agent has been removed/downgraded.
Changed roots or edited/foreign assets are refused instead of silently overwritten.
Before rolling back to an older CLI, uninstall these new integrations with the
new CLI if possible; old CLIs cannot manage them, but pairing and old hooks remain
readable. The App updater installs the latest official Release, so unreleased
source commits do not make these integrations available through that updater.

`test` sends a clearly marked, encrypted test event without a terminal locator;
it is usable over non-PTY SSH and does not jump to an arbitrary pane. Responses
distinguish API processing from `apns_accepted`. Neither proves phone delivery.
`status --json --pair PAIR_ID` reports a fresh API state and durable local presence;
unreachable API, revoked pair, unpaired, and missing hooks are different states.
The phone remains the authority on notification permission and actual display.

Structured `health` diagnoses each Agent's required events, conditions, executable,
owned assets and configuration root; `complete` means disk configuration only.
A test notification does not prove Agent activation. Re-run `hooks install` to
restore missing owned entries; conflicts need review and native adapters need an
Agent restart. Existing user edits and unrelated notifiers remain intact.
Interrupted Codex/Claude writes retain a private operation journal. The optional
`health.<agent>.recovery_action` tells the App to continue install, finish uninstall,
or uninstall before a fresh install; a real conflict remains unchanged. Retry the
indicated operation using the same configuration root. Status/discovery stays
read-only, and `hooks:false` alone does not mean there is no unfinished operation.

Business events go only to active pairs. Pending attempts have their own 32-entry
limit, separate from 32 active pairs. Original credentials can be reconciled after
a lost claim response; unknown authentication is retained for review.

```sh
mobterm-cli pairs reconcile --pair PAIR_ID --json
mobterm-cli pairs abandon --pair PENDING_PAIR_ID --json
mobterm-cli outbox status --pair PAIR_ID --json
mobterm-cli outbox flush --pair PAIR_ID --json
mobterm-cli outbox refresh --pair PAIR_ID --json
```

Abandon removes only the named pending attempt's local secrets; it does not revoke
a remote active pair. Confirmed revoked/expired records release local capacity.
After cloud cancellation, the App retains a separate remote-cleanup obligation and
uses a trusted SSH channel to call `pairs retire --stdin-frame --json` for the exact
origin, Pair and installation. This removes local pending/active credentials and
prevents late setup from recreating them; it is not independent CLI proof of API
revocation. Lost receipts are retryable. Old CLIs leave cleanup pending until the
user updates or maintains the connection. See the [frame/receipt contract](docs/protocol-v1.md#ssh-diagnostics-and-recovery).
Retirement markers contain only digests/reason/time and are bounded at 4096
identities; the CLI refuses new retirement identities at that boundary rather
than evicting markers that still prevent delayed operations from reviving an ID.
Events are durably saved for all active pairs before sending. Completion, test and
explicit flush commands can retry once within their remaining budget. Work still
pending waits for another Agent event or explicit flush; an idle host does not wake itself.
Once the service explicitly takes responsibility, refresh queries its receipt
without posting the event again. Status is read-only. The original one-hour TTL,
nonce/ciphertext and event ID never change on retry. Terminal results and source
deduplication are retained for seven days under hard budgets. A full queue reports
a fixed error rather than evicting a valid pending event.

New tmux notifications prove the process instance using OS boot identity and
precise process start time, detecting `respawn-pane` even when pane IDs are reused.
`target resolve --stdin-frame` can find the same pane in another window of the
same session; `target verify --stdin-frame` still requires the exact position.
Herdr can resolve a unique terminal across workspace/tab/pane within its original
namespace/socket incarnation. Old tmux messages need manual connection opening;
strong automatic navigation requires both an updated App and CLI.

Configuration is under `~/.config/mobterm` (0700), with credential/state files
0600. `MOBTERM_CONFIG_DIR` may select another private directory; it carries a path,
never a credential. Codex respects `CODEX_HOME`; Claude respects
`CLAUDE_CONFIG_DIR`. Existing TOML comments, unrelated settings, and existing
notify argv are preserved. Codex/Claude JSON is reformatted but preserves unrelated
values/hooks. Unsupported config shapes and edited owned entries fail without
overwriting the user's configuration. Concurrent CLI writers use advisory locks.
Claude ownership records track the complete entries inserted by Mobterm. A
pre-existing identical command remains user-owned; later edits to a managed
command, timeout, matcher or entry metadata cause a conflict instead of removal.
Legacy Claude records without entry ownership require manual hook review;
installation and removal leave that configuration unchanged rather than guess
which matching commands belong to Mobterm.
To migrate such a record, review Claude settings and remove only entries you
can confirm were added by Mobterm, then move `hooks-claude.json` from the active
private Mobterm config directory into a private backup. Reinstalling hooks will
create entry ownership metadata. Retain pairing and installation identity files.

Uninstall only removes this CLI's hook additions, retaining later user edits:

```sh
mobterm-cli hooks uninstall --agent codex --json
mobterm-cli hooks uninstall --agent claude --json
mobterm-cli hooks uninstall --agent opencode --json
mobterm-cli hooks uninstall --agent pi --json
```

After all installed hook removals succeed, remove the installed executable if desired:

```sh
rm "$HOME/.local/bin/mobterm-cli"
```

If removal reports an edited hook, keep the executable until you have reviewed
that entry. Pair credentials remain in the private Mobterm directory; revoke
pairs in the App separately.

Revoke a pair in the App. The CLI cannot create a new receiver or revive a revoked
pair. Multiple devices have separate pair credentials and encryption keys.
Uninstalling hooks does not revoke pairs or delete credentials.

## Terminal output

Run `mobterm-cli --help` for grouped commands and examples. Use
`mobterm-cli help upgrade` or `mobterm-cli hooks install --help` for the
options at a specific command. Help reads the output terminal size and wraps
wide text and long paths to fit.

When stdout is a terminal, `status`, `hooks install`, `hooks uninstall`, `hooks reconcile`,
`test` and `upgrade` show human-readable results. Redirecting stdout or
passing `--json` selects the existing JSON format. For a test notification,
the result distinguishes local queuing, API acceptance and APNs acceptance.
Phone display requires observation on the device.

- Append `--color=auto|always|never` to help or a human command (choose one
  value). In auto mode, nonempty `NO_COLOR` (including `0`) disables color,
  then nonzero `CLICOLOR_FORCE` can force it, then `CLICOLOR=0` disables it.
  Otherwise color follows the output descriptor and terminal environment.
- `--plain` takes precedence over color settings and turns off decorative
  symbols, dynamic updates and hyperlinks. It preserves the selected result
  format. `NO_COLOR` alone preserves layout and live progress.
- Width, UTF-8 locale, color and refresh capability are evaluated separately.
  Monochrome ANSI terminals can still refresh progress. Unrecognized cursor
  capabilities use static stages. Terminals soft-wrap the continuous release
  URL so it can be copied even in a narrow pane. Help adds clickable links on
  recognized direct terminals such as iTerm2, WezTerm and Ghostty.
- Machine protocols, stdin frames, Agent callbacks and the single-line
  `version` probe keep their established formats, including with forced color.

## Upgrade

```sh
mobterm-cli upgrade --version vX.Y.Z
```

On an interactive run, progress goes to stderr and the installed version and
path go to stdout. `--progress=auto` draws a single line for slower updates.
Known response lengths show actual bytes and a percentage. Unknown lengths show
bytes and elapsed time. CI and redirected stderr use static stages. Select
`--progress=plain` for stage lines or `--progress=never` to suppress them.
`upgrade --json` and redirected stdout suppress progress on both streams.

Use an existing, explicit stable version. Upgrade accepts only this repository's
GitHub release assets, checks the four-platform manifest, selected asset name and
SHA-256, then atomically replaces the current regular executable. Download,
checksum, extraction, or pre-rename write failure keeps the old executable.
No remote scripts execute, and pair/config/hook files are unchanged. The binary's
directory must be writable; use a user-owned installation to avoid elevated access.

### Updating existing Agent integrations

Once running a version that supports migration, opt in with:

```sh
mobterm-cli upgrade --version vX.Y.Z --with-integrations
```

This runs the newly installed executable at its verified absolute path. It
checks the version and migration capability before updating any integrations.
The plain upgrade above keeps its binary-only behavior. On a CLI that predates
this flag, use the two commands below. Replace the example version with a
**published stable release containing migration support** and use your actual
installation path both times:

```sh
"$HOME/.local/bin/mobterm-cli" upgrade --version vX.Y.Z && \
  "$HOME/.local/bin/mobterm-cli" hooks reconcile
```

After a manual binary replacement or an Agent upgrade, run
`hooks reconcile --check` to inspect existing integrations. Run
`hooks reconcile` to apply safe differences. Both operate in the invoking
account's effective Agent configuration directories. A check never creates
installation identity, repairs pending writes or contacts the notification API.
No record or journal means skipped. A missing, disabled, edited or ambiguous
owned component needs review. Reconciliation keeps third-party settings and
the Codex notify chain. It never installs a new Agent integration or grants
trust. Claude hooks and its reply adapter have separate results. Both respect
the account's hooks-disabled settings. Pi migration checks the personal
`extensions` settings, including exact overrides and common exclusion globs.
A filter the CLI cannot interpret leaves the adapter and record untouched.

Restart or reload the affected Agent as indicated; review changed native
Codex hooks in `/hooks`. Disk state alone cannot confirm runtime loading or
notification delivery. If interrupted, run the check before continuing. Keep
an incompatible pending journal in place and use the original CLI to inspect
or finish its operation before retrying. Inspect pending operations and the
adapter revision before downgrading; an older binary may not understand them.

#### Migration receipts over SSH

`capabilities --json` advertises top-level `"hooks_reconcile": 1`.
For version 1, compare `hooks_reconcile_path` to the verified executable path before writing.
No version-1 declaration means leave migration to Agent management.
Existing `status --agents-json` values are unchanged.

`hooks reconcile [--check] --json` returns at most 32 KiB. Version 1 includes
`v`, `operation` (check or reconcile), `cli_version`, `cli_path`, `overall`,
`requires_action`, and `agents`. Each Agent has an `agent`, a `status`, and
`components` (Codex hooks, Claude hooks and adapter, OpenCode adapter, Pi
adapter). Each component carries its `component`, `status`, `reason` and
`activation`. Reasons are codes and contain no hook payloads.

| Component status | Reason | Interpretation |
| --- | --- | --- |
| current | current | Owned configuration already matches |
| available | template_changed | Check found a safe update or compatible unfinished transaction |
| updated | updated | Reconciliation completed the on-disk change |
| skipped | unmanaged / missing_agent / probe_failed / unsupported | No ownership evidence, or the Agent is unavailable or unsupported |
| attention | ownership_conflict / disabled / unsupported / pending_conflict / write_failed | Review this component before continuing |

An Agent can report `partial` when one component is verified and another
requires attention. The `activation` code is `unknown`, `restart_required`
or `native_review_required`. A check's activation describes what would follow
the change. A current Codex native configuration retains the `/hooks` review
indication on a fresh check because trust and loading are observed in the TUI.
`requires_action` covers remaining configuration work, separately
from activation. Exit 0 has a complete result, including a check with available
updates. Exit 2 carries a full attention result. Exit 1 means the operation
could not supply a migration receipt. Parse the bounded receipt even on exit 2.

For a combined upgrade, JSON records binary replacement (`upgraded`), version
readback (`verified`), `integration_state` and an optional `integrations`
receipt. Exit 2 preserves the fact of replacement even if readback failed,
migration is unsupported or the integration result is unconfirmed. Recheck
the reported installation before continuing; do not automatically replay writes.

## Build and verify

The root [main.go](main.go) starts the CLI. Command dispatch, implementation,
and Go tests live in [internal/cli](internal/cli/).

```sh
sh -n install.sh
python3 tests/install_test.py
test -z "$(gofmt -l .)"
go build -trimpath -o mobterm-cli .
./mobterm-cli version
go test ./...
go vet ./...
go test -race ./...
python3 scripts/package.py --version v0.0.0 --commit FULL_40_CHARACTER_COMMIT_SHA
python3 tests/install_test.py --dist dist --commit FULL_40_CHARACTER_COMMIT_SHA
```

The package script builds all four targets with `CGO_ENABLED=0`, records the
source SHA/platform/version, normalizes tar/gzip metadata, and writes SHA-256
checksums. Branch pushes and pull requests run eight native CI jobs covering
all four targets on two OS versions each:

| Target | Native GitHub-hosted Runner |
| --- | --- |
| Linux amd64 | `ubuntu-22.04`, `ubuntu-24.04` |
| Linux arm64 | `ubuntu-22.04-arm`, `ubuntu-24.04-arm` |
| macOS amd64 | `macos-15-intel`, `macos-26-intel` |
| macOS arm64 | `macos-15`, `macos-26` |

Each job checks the OS/CPU and Go host/target architecture, rejects Rosetta,
installs/checks tmux, and runs uncached, verbose Go tests and race checks with
`MOBTERM_REQUIRE_TMUX=1`: a missing tmux fails instead of skipping its live test.
On Intel macOS, both Go test commands allow 45 minutes per package.
Each job builds and executes the native binary. One Ubuntu job cross-builds all
four test packages and uploads them with their checksums. Each native job downloads
the same artifact for offline installer regression and its native smoke test. This matrix
does not establish compatibility with other Linux distributions or OS versions.
Release tag pushes reuse this CI from the same commit. All eight native
jobs must succeed before the Ubuntu release build cross-compiles the four assets;
only that successful build can reach the separately privileged Release job.
Only stable `vX.Y.Z` tags in the official repository can publish, and existing
releases are never overwritten. Packaging locally does not publish or tag anything.
macOS release binaries remain unsigned and not notarized. CI success does not
prove a Release was published, Gatekeeper accepted an installation, or a phone
displayed a notification or navigated successfully.
Installer tests use isolated HOME/PATH, simulated GitHub responses and local
archives without network access. With `--dist`, they additionally install and
check all four real packages and run the native installed binary's `version`;
this is not a download test against a public Release.

The eight native CI jobs use historical Codex 0.162.1 / Claude Code 2.1.282.
Linux also runs the real minimum and current endpoint suites. The macOS Intel
`macos-config` job uses Codex 0.171.0 with minimum Claude Code 2.0.36,
OpenCode 2.0.0 and Pi 1.1.0. Each job selects its exact versions from
`testdata/compatibility-samples.json` before npm installation. Go fixtures
exercise the Codex notification floor of 0.100.0 on both systems.

With Node.js 22 and Python 3.12 available, run this from the repository root.
It selects the Linux minimum lifecycle suite or the macOS real configuration
suite, including native configuration loading before and after hook removal.
Linux also exercises the loopback HTTPS setup.

```sh
(
  set -eu
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  mkdir -p "$work/home"
  go build -trimpath -o "$work/mobterm-cli" .
  case "$(uname -s)" in
    Darwin) sample=macos-config; set -- --config-only --agent-prefix "$work/agents" ;;
    Linux)  sample=minimum; set -- ;;
    *) exit 1 ;;
  esac
  python3.12 tests/select_agent_samples.py "$sample" > "$work/versions.env"
  . "$work/versions.env"
  HOME="$work/home" npm install --global --prefix "$work/agents" --no-audit --no-fund \
    "@openai/codex@$CODEX_TEST_VERSION" "@anthropic-ai/claude-code@$CLAUDE_TEST_VERSION"
  PATH="$work/agents/bin:$PATH" python3.12 tests/linux_lifecycle_test.py \
    --binary "$work/mobterm-cli" "$@" --expect-codex-version "$CODEX_TEST_VERSION" \
    --expect-claude-version "$CLAUDE_TEST_VERSION" -v
)
```

Use `sample=current` for the other Linux endpoint, or `sample=historical` on
either platform. On macOS the selector refuses Codex packages older than 0.119.0.
The runner checks the selected prefix, package and native executable, including
its signature and macOS execution policy, before any version probe. A refusal
ends the run. For a stale or mismatched PATH/package, run the block with a fresh
prefix and the fixed `macos-config` sample. If system policy rejects that
sample or cannot assess it, record the diagnostic and use a host where the
same official sample passes its system checks. Keep system protections enabled.

These checks create disposable HOME/config directories, disable Claude updates
and nonessential traffic, and use no Agent login credentials or real model/APNs
requests. Linux setup requests use a loopback HTTPS fixture. Native runtime
notification events and phone delivery/navigation remain separate verification.

Secrets, transcripts, caches, and build outputs must never be committed.
See the [documentation map](#documentation-map) for contracts and evidence scope.

### Notification API request deadlines

The command budgets below come from `commandTimeout` in
[internal/cli/main.go](internal/cli/main.go) and from `api.go`, `compatibility.go`,
and `activity_dispatch.go` in the same package. Protocol-specific waiter lifetimes
remain in [approval](docs/protocol-v2.md#native-approval-over-ssh) and
[native interaction](docs/native-interactions.md) contracts.

| Command or phase | Maximum budget |
| --- | --- |
| `setup`, `notify`, `test`, full/pair `status`, `outbox flush/refresh`, `pairs reconcile` | 125 seconds total, including retries/readback and persistence |
| One notification API request | 60 seconds across DNS, connection, TLS, headers and body; TLS negotiation is included, not added |
| Agent version probe | 20 seconds |
| `hooks reconcile` | 60 seconds total; parallel candidate probes up to 20 seconds, then up to 8 seconds per Agent for local planning, locking and writing |
| `hooks install`, `status --agents-json` | 25 seconds including probe and cleanup |
| `status --installation-json`, hook removal and other default local commands | 10 seconds |
| `observe` | 2 seconds |
| `activity dispatch` | 12 seconds total; at most 5 seconds for network draining |
| Other `activity` commands | 5 seconds |
| `approval` commands | 125 seconds; native permission request lifetime is at most 120 seconds within the configured 130-second host Hook deadline |
| `interaction` commands | 335 seconds; supported native question/helper lifetimes have a separate 300-second maximum |
| `upgrade` | 2 minutes for download and replacement; combined upgrade additionally allows 10 seconds for readback and capability detection, and 62 seconds for migration and process cleanup |

The independent API transport preserves certificate verification, proxy rules,
HTTP/2 and redirect refusal. Shorter parent deadlines or cancellation win; API
requests reserve the final second of the parent context for persistence (one
fifth of remaining time when less than five seconds remain). Installed native Hook
budgets can stop a command earlier; replacing the CLI binary does not rewrite them.
Completion reminders and explicit outbox flushes inherit the network command
budget for capability negotiation and sending. Ordinary Codex tool observations
persist locally and may launch the bounded dispatcher; it is not a daemon or an
idle retry scheduler. Its lifecycle is described in
[compatibility](docs/compatibility.md#codex-p1-approval-and-observation-changes-2026-11-06).
The App's generic SSH notification wait is 75 seconds and its setup outer wait is
90 seconds (`AgentNotificationsSSH.swift` in the App repository). These caller
limits are distinct from the CLI's 125 seconds and may end waiting earlier.

Pairing reports typed network timeouts (including TLS handshake timeouts) as the
existing `timeout` failure; explicit cancellation takes priority. Response-body
timeout/cancellation evidence is preserved without displaying raw network errors.
A generic `api_unavailable` result alone cannot identify the failed network phase.

Failed API requests now write a safe diagnostic line to stderr, prefixed with
`mobterm-cli network: `. To include successful requests during troubleshooting:

```sh
MOBTERM_NETWORK_DIAGNOSTICS=1 mobterm-cli test --pair PAIR_ID --json
MOBTERM_NETWORK_DIAGNOSTICS=1 mobterm-cli outbox flush --pair PAIR_ID --json
```

Records distinguish DNS, TCP, TLS, request write, response headers and body phases,
and include available timings, connection reuse, HTTP protocol/status and a valid
UUID request ID. `first_byte_ms` measures elapsed time from request start, not an
additional phase duration. No URLs, addresses, credentials, payloads or raw error
text are logged. The CLI does not retain a diagnostic file, and stdout JSON is
unchanged. A completed request write does not prove API acceptance or phone display.

Retryable completion delivery and capability failures get at most one additional
attempt during the command, with a randomized delay starting at 15–18.75 seconds.
Retries reuse the original event and ciphertext, respect longer `Retry-After`
values, and require at least five seconds left after the wait. Cancellation ends
the wait. Short observation hooks keep their existing budgets; there is no
background retry after the command exits.

For a field report, record the App build, API origin, attempt time, and the absolute
CLI path plus version/commit observed by the App's installation step. Setup and
readback reuse that path; an interactive shell's `which`, a different installed
binary, or the latest published release does not establish which binary ran.
Pairing does not automatically upgrade the CLI. `status --installation-json` can
initialize installation state, so it is not a side-effect-free version probe.
