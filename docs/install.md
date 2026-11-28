# Install mobterm-cli

Install on the Linux or macOS host where the Agent runs. Use the same OS account
for installation, the Agent and the saved SSH connection in Mobterm.

## Install a release

The [installer](../install.sh) runs with POSIX sh, curl, tar, sha256sum or
shasum, and standard Unix utilities. No Go toolchain, jq or sudo is required.
To install the latest published stable release:

```sh
curl -fsSL https://raw.githubusercontent.com/mobterm/mobterm-cli/main/install.sh | sh
```

To review the script first, download it to an empty working directory and
inspect that copy before executing it:

```sh
curl -fsSL -o install.sh https://raw.githubusercontent.com/mobterm/mobterm-cli/main/install.sh
less install.sh
```

Run `sh install.sh` for the latest stable binary. To choose a specific binary,
replace `vX.Y.Z` with an existing stable tag from [Releases](https://github.com/mobterm/mobterm-cli/releases):

```sh
sh install.sh --version vX.Y.Z
```

Read back the installed version and platform from the destination:

```sh
"$HOME/.local/bin/mobterm-cli" version
```

The script from `main` selects a published release and installs its binary
with mode 0755. The installed binary's own upgrade behavior depends on that
release. macOS release binaries are unsigned and not notarized.
See [Release source verification](security.md#release-source-verification)
for the trust boundary and what to check after a failed or interrupted install.

### PATH and Agent hooks

Keep the executable at the absolute path recorded by the Agent hooks.
For the current shell, run:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

A non-interactive SSH session may load different startup files.
Configure its PATH separately or use the absolute executable path in that
environment. Installation leaves shell and SSH configuration, pairing state
and Agent settings untouched. Continue with [pairing and Agent setup](usage.md).

## Manual installation

Download the archive for your host and `checksums.txt` from **the same stable tag**
in [Releases](https://github.com/mobterm/mobterm-cli/releases).

| Host | Archive |
| --- | --- |
| Linux, Intel/AMD 64-bit | `mobterm-cli_vX.Y.Z_linux_amd64.tar.gz` |
| Linux, ARM 64-bit | `mobterm-cli_vX.Y.Z_linux_arm64.tar.gz` |
| macOS, Intel | `mobterm-cli_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS, Apple Silicon | `mobterm-cli_vX.Y.Z_darwin_arm64.tar.gz` |

Put only those two files in an empty download directory. From that directory,
run `sha256sum --check --ignore-missing checksums.txt` on Linux or
`shasum -a 256 --check --ignore-missing checksums.txt` on macOS. Confirm the
chosen archive reports `OK` before extracting it. Install its `mobterm-cli`
executable with mode 0755 in a user-owned directory and read back its version.

## Build from source

From the repository root with the toolchain in [go.mod](../go.mod):

```sh
go build -trimpath -o mobterm-cli .
install -d -m 700 "$HOME/.local/bin"
install -m 755 mobterm-cli "$HOME/.local/bin/mobterm-cli"
"$HOME/.local/bin/mobterm-cli" version
```

See [Verifying mobterm-cli](validation.md) for independent build and installer checks.

## Upgrade

Replace `vX.Y.Z` with an existing stable tag. Use a regular executable in a directory your account can write to.

```sh
mobterm-cli upgrade --version vX.Y.Z
```

Plain upgrade verifies the platform archive and atomically replaces the running
CLI's executable. A download, checksum, extraction or pre-replacement write
failure leaves the old executable in place. Upgrade executes no remote
scripts and preserves pairing, configuration and hooks.
For progress and JSON output see [Terminal output](cli-reference.md#terminal-output).

If an older CLI rejects the release URL after a repository transfer, use the
current installer with an explicit tag, then verify the version at the absolute
path. An older App with a bundled installer needs a separate App update.

## Updating existing Agent integrations

On a CLI supporting integration migration, opt in to updating existing owned
components along with the binary:

```sh
mobterm-cli upgrade --version vX.Y.Z --with-integrations
```

The CLI runs the new executable at its verified absolute path, checking its
version and migration capability before updating integrations. If the old CLI
predates this flag, select a published stable tag with migration support and
use the actual installation path for both commands:

```sh
"$HOME/.local/bin/mobterm-cli" upgrade --version vX.Y.Z && \
  "$HOME/.local/bin/mobterm-cli" hooks reconcile
```

After replacing the binary manually or updating an Agent, run
`mobterm-cli hooks reconcile --check` to preview changes. It reads the
invoking account's effective Agent configuration without initializing an
installation, repairing pending writes or contacting the notification API.
Use `mobterm-cli hooks reconcile` to apply safe updates. Components without
ownership records or journals are skipped. Review missing, disabled, edited
or ambiguous owned components. Reconciliation preserves third-party settings
and existing Codex notify chains. New integrations and trust grants require
separate user action.

Claude hooks and its reply adapter have separate results and respect the
account's hooks-disabled settings. Pi migration honors personal extension
filters, including exact overrides and common exclusion globs. An unknown
filter leaves the adapter and ownership record untouched.

Follow the Agent's restart or reload instructions and review changed Codex
native hooks in `/hooks`. Disk checks cannot confirm runtime loading or phone
delivery. After interruption, run the check again. Leave an incompatible
journal in place and use the original CLI to inspect or finish its operation.
Before downgrading, check pending operations and adapter revisions for
compatibility. SSH clients can inspect the [migration receipt](protocol-v1.md#integration-reconciliation-over-ssh).

## Uninstall

Remove each installed integration while its recorded CLI executable is still
available. Choose the commands for your Agents:

```sh
mobterm-cli hooks uninstall --agent codex --json
mobterm-cli hooks uninstall --agent claude --json
mobterm-cli hooks uninstall --agent opencode --json
mobterm-cli hooks uninstall --agent pi --json
```

These commands remove CLI-owned additions and retain later user edits.
If a hook was edited, review it and keep the executable until all installed
integrations have been removed successfully. Then, for the default installation:

```sh
rm "$HOME/.local/bin/mobterm-cli"
```

Pair credentials remain in the private Mobterm directory. Revoke each Pair
separately in the App. Each paired device has its own credentials and
encryption key. The CLI cannot create a new receiver or revive a revoked Pair.
