# mobterm-cli

The remote companion CLI for Mobterm. Install it on the host where you run
your coding Agent to get completion notifications in the Mobterm App. When
the CLI can verify the original terminal, you can jump back to it from your phone.

- **Follow an Agent session.** Receive notifications from Codex, Claude Code,
  OpenCode or Pi. Native approvals, questions and message replies require
  explicit authorization and depend on the Agent, version and session mode.
- **Run a Quick Command.** Start an eligible command over SSH and disconnect
  while it runs. The App can notify you on completion and retrieve its result.
- **Find a workspace.** Discover tmux and Herdr workspaces over your saved SSH
  connection. Use Doctor to check the CLI installation and Agent integration.

The CLI runs on **Linux and macOS, on amd64 and arm64**. This repository
contains the MIT-licensed Go CLI and its public protocols. The Mobterm App
and notification service are separate components. The CLI needs no resident
daemon or listening port on the host.

<a name="install"></a>

## Installation

On the remote host, sign in as the same OS user that Mobterm connects to over
SSH and that runs the Agent. Install the latest stable release and check it:

```sh
curl -fsSL https://raw.githubusercontent.com/mobterm/mobterm-cli/main/install.sh | sh
"$HOME/.local/bin/mobterm-cli" version
```

The installer runs without sudo. macOS release binaries are unsigned and
not notarized. For script inspection, checksums, a specific release or SSH
PATH setup, see the [installation guide](docs/install.md).

<a name="pair-and-use"></a>

## Getting started

1. In the Mobterm App, open the remote host's saved connection. Open **Agent
   Notifications** and pair the installation. The App supplies the pairing
   secret over host-verified SSH. Keep it out of shell commands and logs.
2. Configure the integration for the Agent you use. Restart its session and
   complete any native hook trust review it requests.
3. Send a test notification from the paired connection. Check that it appears
   on your phone. To try **Jump to**, complete a real Agent turn in a supported
   terminal session with a verifiable target.

A verified Codex shared-daemon completion can reach the inbox even when it has
no terminal to jump to. Replying to the original thread requires a separate
grant and local activation. See [Agent compatibility](docs/compatibility.md)
and [Codex replies](docs/reply-v2.md).

Mobterm encrypts a bounded response excerpt for the phone. When available,
the full reply stays on the host for authorized reading over SSH. Quick
Command output also stays out of notification payloads.
[Security and privacy](docs/security.md) describes the data and trust boundaries.

## Useful commands

Run these on the remote host with the CLI on your PATH. All three work before
pairing.

```sh
mobterm-cli --help
mobterm-cli version
mobterm-cli doctor
```

Doctor can contact the network and probe installed Agents. Use
`mobterm-cli doctor --offline` for local inspection without those probes.

<a name="documentation-map"></a>

## Documentation

Start with the [full documentation index](docs/README.md). If you're reading
an extracted release archive, [browse the documentation online](https://github.com/mobterm/mobterm-cli/tree/main/docs).
That link tracks main. Select the tag printed by `mobterm-cli version` for the
documentation shipped in the corresponding source tree.

- [Pair, configure and use the CLI](docs/usage.md)
- <a name="diagnose-this-installation"></a>[Diagnose an installation](docs/usage.md#troubleshooting)
- <a name="upgrade"></a>[Upgrade the CLI](docs/install.md#upgrade) or <a name="updating-existing-agent-integrations"></a>[update existing Agent integrations](docs/install.md#updating-existing-agent-integrations)
- <a name="terminal-output"></a>[Terminal and JSON output](docs/cli-reference.md#terminal-output)
- <a name="notification-api-request-deadlines"></a>[Notification API request deadlines](docs/cli-reference.md#notification-api-request-deadlines)
- <a name="migration-receipts-over-ssh"></a>[Integration reconciliation receipts over SSH](docs/protocol-v1.md#integration-reconciliation-over-ssh)

## Contributing and license

<a name="build-and-verify"></a>
See the [build and verification guide](docs/validation.md) to reproduce the
checks and the [contributor guide](CONTRIBUTING.md) to propose a change.

[Releases](https://github.com/mobterm/mobterm-cli/releases) ·
[MIT license](LICENSE) · [Third-party notices](THIRD_PARTY.md)
