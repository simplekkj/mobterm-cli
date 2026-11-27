# Contributing to mobterm-cli

Build from the repository root with the Go toolchain in [go.mod](go.mod).
Use [Build and verify](README.md#build-and-verify) for platform requirements
and the [verification guide](docs/validation.md) to select the tests for your
change. Use an isolated configuration directory for experiments.

For changes to public commands or wire messages, describe the behavior for
both current and older clients. Include the affected protocol vectors, docs
and regression tests. The [engineering guidelines](AGENTS.md) cover local
state, compatibility and security. In a contribution, describe what you
changed, the commands you ran and any platform-specific checks still pending.
Use synthetic examples for configuration, keys and event payloads.
