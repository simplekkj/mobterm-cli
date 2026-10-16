# mobterm-cli

A short-lived remote notification helper for Mobterm. It reports verified Agent
events from tmux or Herdr, encrypts the terminal locator for the paired phone,
and exits. It does not run a daemon, listen on a port, read transcripts, upload
terminal content, approve tools, or run remote commands on behalf of the API.

Linux and macOS are supported on amd64 and arm64. This repository contains only
the independent Go CLI and public protocol; it does not license other Mobterm
repositories. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md).

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

Install hooks only after checking the [version compatibility](docs/compatibility.md):

```sh
mobterm-cli hooks install --agent codex --json
mobterm-cli hooks install --agent claude --json
mobterm-cli status --json
mobterm-cli test --pair PAIR_ID --json
```

Restart Agent sessions after hook installation. Codex uses its native final-turn
`notify`. Claude uses `UserPromptSubmit` and a narrow `Notification` matcher;
completion is the native delayed `idle_prompt`, never `Stop`. Hook installation
does not change a user's Stop hooks or tool approval decisions. Herdr uses these
same native Agent hooks, with an explicit structured terminal query. No second
Herdr event forwarder is installed.

`test` sends a clearly marked, encrypted test event without a terminal locator;
it is usable over non-PTY SSH and does not jump to an arbitrary pane. Responses
distinguish API processing from `apns_accepted`. Neither proves phone delivery.
`status --json --pair PAIR_ID` reports a fresh API state and durable local presence;
unreachable API, revoked pair, unpaired, and missing hooks are different states.
The phone remains the authority on notification permission and actual display.

Configuration is under `~/.config/mobterm` (0700), with credential/state files
0600. `MOBTERM_CONFIG_DIR` may select another private directory; it carries a path,
never a credential. Codex respects `CODEX_HOME`; Claude respects
`CLAUDE_CONFIG_DIR`. Existing TOML comments, unrelated settings, and existing
notify argv are preserved. Claude JSON is reformatted but preserves unrelated
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
```

After both hook removals succeed, remove the installed executable if desired:

```sh
rm "$HOME/.local/bin/mobterm-cli"
```

If removal reports an edited hook, keep the executable until you have reviewed
that entry. Pair credentials remain in the private Mobterm directory; revoke
pairs in the App separately.

Revoke a pair in the App. The CLI cannot create a new receiver or revive a revoked
pair. Multiple devices have separate pair credentials and encryption keys.
Uninstalling hooks does not revoke pairs or delete credentials.

## Upgrade

```sh
mobterm-cli upgrade --version vX.Y.Z
```

Use an existing, explicit stable version. Upgrade accepts only this repository's
GitHub release assets, checks the four-platform manifest, selected asset name and
SHA-256, then atomically replaces the current regular executable. Download,
checksum, extraction, or pre-rename write failure keeps the old executable.
No remote scripts execute, and pair/config/hook files are unchanged. The binary's
directory must be writable; use a user-owned installation to avoid elevated access.

## Build and verify

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
Jobs also build and execute the native binary and cross-build all four packages
for offline installer regression and native installation smoke tests. This matrix
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

Every CI job installs real Codex 0.162.1 and Claude Code 2.1.282 into an isolated
npm prefix, using Node.js 22 and Python 3.12. The test runner rejects unexpected
Agent versions. Linux runs all 11 black-box lifecycle checks; macOS runs the 10
hook/configuration checks with `--config-only`. Both include native Agent
configuration loading before and after hook/executable removal. The Linux HTTPS
fixture relies on `SSL_CERT_FILE` and is excluded from the macOS suite, without
changing system certificate trust.

Locally, with Python 3.11+, OpenSSL and those exact real Agent versions on PATH,
run the black-box checks against a built executable:

```sh
python3 tests/linux_lifecycle_test.py --binary ./mobterm-cli -v
# macOS: run the hook/configuration suite
python3 tests/linux_lifecycle_test.py --binary ./mobterm-cli --config-only -v
```

These checks create disposable HOME/config directories, disable Claude updates
and nonessential traffic, and use no Agent login credentials or real model/APNs
requests. Linux setup requests use a loopback HTTPS fixture. Native runtime
notification events and phone delivery/navigation remain separate verification.

See [protocol v1](docs/protocol-v1.md), [security](docs/security.md), and the
honest scope of [validation](docs/validation.md). Secrets, transcripts, caches,
and build outputs must never be committed.
