# Mobterm CLI documentation

## Using the CLI

| I want to… | Guide |
| --- | --- |
| Install on a remote host, upgrade, or remove the CLI | [Installation](install.md) |
| Pair a connection, activate an Agent, or diagnose a missing notification | [Usage and troubleshooting](usage.md) |
| Find a command or interpret its output and timeouts | [CLI reference](cli-reference.md) |
| Check an Agent version, session mode, or navigation requirement | [Agent compatibility](compatibility.md) |
| Check which approvals, questions and replies an Agent supports | [Native interaction support](native-interactions.md) |
| Understand what leaves the host or what a paired phone may access | [Security and privacy](security.md) |

## Implementing a protocol client

Use the contract for the operation you need. Notification envelopes, native
interactions and replies negotiate their versions independently.

| Operation | Contract |
| --- | --- |
| Pair an installation, send an event, inspect a target or reconcile hooks | [Setup, notifications and targets v1](protocol-v1.md) |
| Receive state, inbox and badge updates or use legacy native approval | [Notification protocol v2](protocol-v2.md) |
| Receive an asynchronous Quick Command completion | [Notification envelope v3](notification-v3.md) |
| Submit a Quick Command or read its result | [Job control](quick-command-job-v1.md) and [result receipt](quick-command-result-v1.md) |
| Discover workspaces over SSH | [Workspace discovery](workspace-discovery.md) |
| Answer a native request | [Interaction wire v1](interaction-v1.md) |
| Reply to a running Agent or append to a verified Codex thread | [Ordinary replies v1](reply-v1.md) and [Codex thread append v2](reply-v2.md) |
| Request a diagnostic or a scoped repair | [Doctor v1](doctor-v1.md) |

## Developing and verifying

Follow the [contributor guide](../CONTRIBUTING.md) and
[engineering guidelines](../AGENTS.md). The [verification guide](validation.md)
includes the native CI matrix, installer checks and Agent lifecycle commands.
[Release notes](https://github.com/mobterm/mobterm-cli/releases) describe published versions.
