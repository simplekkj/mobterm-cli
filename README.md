# mobterm-cli

A short-lived remote notification helper for Mobterm. It reports verified Agent
events from tmux or Herdr, encrypts the terminal locator for the paired phone,
and exits. It does not run a daemon, listen on a port, read transcripts, upload
terminal content, approve tools, or run remote commands on behalf of the API.

Linux and macOS are supported on amd64 and arm64. This repository contains only
the independent Go CLI and public protocol; it does not license other Mobterm
repositories. See [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md).

## Install

Build with the toolchain pinned in go.mod:

```sh
go build -trimpath -o mobterm-cli .
install -d -m 700 "$HOME/.local/bin"
install -m 755 mobterm-cli "$HOME/.local/bin/mobterm-cli"
mobterm-cli version
```

Ensure `mobterm-cli` is on the remote non-interactive SSH PATH as well as your
interactive PATH. It must also remain at the absolute path recorded by installed
Agent hooks. Run installation for the same OS user that runs the Agent.

Future stable tags publish these assets to this repository's GitHub Releases:

| System | Architecture | Asset |
| --- | --- | --- |
| Linux | Intel/AMD 64-bit | `mobterm-cli_vX.Y.Z_linux_amd64.tar.gz` |
| Linux | ARM 64-bit | `mobterm-cli_vX.Y.Z_linux_arm64.tar.gz` |
| macOS | Intel | `mobterm-cli_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS | Apple Silicon | `mobterm-cli_vX.Y.Z_darwin_arm64.tar.gz` |

Download the matching archive and `checksums.txt` from the **same explicit tag**
at [GitHub Releases](https://github.com/simplekkj/mobterm-cli/releases).
No initial release is implied by these instructions. Verify with
`sha256sum --check --ignore-missing checksums.txt` on Linux or
`shasum -a 256 --check --ignore-missing checksums.txt` on macOS, then extract the
verified archive and install its `mobterm-cli` executable with mode 0755.
macOS binaries are unsigned and not notarized; Gatekeeper approval is not claimed.

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

Uninstall only removes this CLI's hook additions, retaining later user edits:

```sh
mobterm-cli hooks uninstall --agent codex --json
mobterm-cli hooks uninstall --agent claude --json
```

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
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
go test -race ./...
python3 scripts/package.py --version v0.0.0 --commit FULL_40_CHARACTER_COMMIT_SHA
```

The package script builds all four targets with `CGO_ENABLED=0`, records the
source SHA/platform/version, normalizes tar/gzip metadata, and writes SHA-256
checksums. CI performs native Linux/macOS tests and cross-builds; only stable tag
pushes can reach the separately privileged Release job. Existing releases are
never overwritten. Packaging locally does not publish or tag anything.

See [protocol v1](docs/protocol-v1.md), [security](docs/security.md), and the
honest scope of [validation](docs/validation.md). Secrets, transcripts, caches,
and build outputs must never be committed.
