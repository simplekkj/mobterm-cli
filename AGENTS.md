# mobterm-cli engineering guidelines

## Commands and local state

- Keep commands short-lived. The CLI reports verified Agent events and supports opt-in native interactions over trusted SSH. Do not add a daemon, listener, terminal content collection, or API-driven remote execution.
- The root [main.go](main.go) passes build information to [internal/cli/main.go](internal/cli/main.go). The `internal/cli` package handles protocol and requests in `protocol*.go` and `api*.go`, events and configuration in `events.go` and `hooks.go`, and run state and bounded dispatch in `activity*.go`. Keep native approval in `approval*.go` and native interaction in `interaction*.go`. Use `target.go` for target verification, `store.go` for state, and `upgrade.go` for upgrades.
- Check actual Agent versions and event schemas against [hook compatibility](docs/compatibility.md) before extending support. Preserve existing notifiers, unrelated settings, and later user edits. Leave unknown configuration shapes intact.
- Accept pairing secrets only through bounded stdin frames. Preserve private permissions, locking, and atomic writes. If verification fails, stop without guessing a pane or creating a terminal. Keep credentials, keys, and hook payloads out of logs and Git. Keep build artifacts out of Git.

## Code and Verification

- Use the toolchain pinned in `go.mod` and [.github/workflows/ci.yml](.github/workflows/ci.yml). Retain `go.sum`. For Go changes run the following checks from the root. Build with `go build -trimpath -o mobterm-cli .` and exercise the changed command. Use isolated temporary configuration in tests.

  ```sh
  test -z "$(gofmt -l .)"
  go test ./...
  go vet ./...
  go test -race ./...
  ```

- CI runs native Linux and macOS jobs. For tmux interoperability, use a real tmux fixture. CI sets `MOBTERM_REQUIRE_TMUX=1` so a missing dependency fails the check. Record any skipped live checks in the verification result.
- For packaging or upgrade changes, run `python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"`. Verify the Linux/macOS archives for both amd64 and arm64 in `dist/`. The [build and verification guide](README.md#build-and-verify) includes the offline installer test. Publishing a stable version tag triggers the release workflow and requires separate release authorization.
- Update affected docs. For documentation-only edits verify facts, links and anchors, and run `git diff --check`. Use the [verification guide](docs/validation.md) for independent test commands.

## Public protocols and compatibility

This repository owns [v1 pairing and targets](docs/protocol-v1.md), [v2 state/inbox/badge and legacy approval](docs/protocol-v2.md), and [interaction wire v1](docs/interaction-v1.md). Use [native interactions](docs/native-interactions.md) for action support, [compatibility](docs/compatibility.md) for notification samples, [security](docs/security.md) for trust boundaries, and the [README](README.md#notification-api-request-deadlines) for command deadlines.

- For changes to SSH stdin frames, CLI JSON, REST authorization or state (including `token_generation`), envelopes/AAD, target identities, or APNs fields, trace the affected writer and reader against the published contracts. Check old/new combinations. Document the supported versions and upgrade order for breaking changes.
- Keep pairing, notifications, revocation and navigation working. Update affected code, docs and regression tests together. For encryption changes check the [public protocol vectors](testdata/) and exercise the notification and target-verification paths.
- Report which delivery stages you observed. Distinguish API acceptance, APNs acceptance, phone display, and navigation. Identify any unavailable client or environment when reporting compatibility or delivery.
- Keep per-Agent/kind grants disabled by default. Bind cancellation and late results to the original request, target, and owner. Notification envelope versions, approval command wire v3, and interaction wire v1 evolve independently. Preserve HMAC purposes and fixture names, including `mobterm/approval/2` and `approval-v2.json`.
