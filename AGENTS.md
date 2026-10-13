# mobterm-cli Engineering Guidelines

## Scope and Boundaries

- Read the target code and relevant docs first. Make the smallest complete change and reuse existing implementations. Avoid overengineering, unrelated refactoring, feature expansion, and changes to unrelated dependencies, configuration, or user data.
- Keep this a short-lived notification CLI: no daemon, listening ports, terminal content collection, or API-driven remote execution.
- Hook compatibility and event semantics are defined in `docs/compatibility.md`; verify actual versions and event schemas before extending support. Preserve existing notifiers, unrelated settings, and later user edits; do not overwrite unknown configuration shapes.
- Accept pairing secrets only through bounded stdin frames. Preserve private permissions, locking, and atomic writes in `store.go`. Failed target verification must not guess a pane or create a terminal. Never log or commit credentials, keys, hook payloads, or build artifacts.

## Code and Verification

- The root contains one Go `main` package: `main.go` dispatches commands; `protocol.go` / `api.go` handle the protocol and requests; `events.go` / `hooks.go` handle events and configuration; `target.go` verifies targets; `store.go` manages state; `upgrade.go` handles upgrades.
- Use the toolchain in `go.mod` and `.github/workflows/ci.yml`, currently Go 1.27.1; retain `go.sum`. Local build: `go build -trimpath -o mobterm-cli .`.
- For Go changes, run these CI checks from the repository root; CI runs on Linux/macOS. For documentation-only changes, verify facts, references, and `git diff --check`.

  ```sh
  test -z "$(gofmt -l .)"
  go test ./...
  go vet ./...
  go test -race ./...
  ```

- For packaging or upgrade changes, also run `python3 scripts/package.py --version v0.0.0 --commit "$(git rev-parse HEAD)"` and verify all four Linux/macOS and amd64/arm64 targets in `dist/`. Stable version tags trigger `.github/workflows/release.yml`; authorization to commit and push does not authorize a release.
- Use isolated temporary configuration in tests. Skips caused by missing real dependencies such as tmux do not prove interoperability. Update relevant docs for the change; see `docs/validation.md` for verification scope.

## Cross-Repository Protocol (App / CLI / API)

- The repositories are `../mobterm` (App), `../mobterm-cli`, and `../mobterm-api`. The public contract is defined in `../mobterm-cli/docs/protocol-v1.md`.
- Changes to SSH stdin frames/CLI JSON, REST authorization and states (including `token_generation`), event envelopes/AAD, target identities, or APNs fields require checking producers and consumers across all three repositories, including mixed old/new versions. For breaking changes, specify supported versions and upgrade order; a change in one repository must not break pairing, notifications, revocation, or navigation.
- Update affected implementations, docs, and regression tests together. For encryption contracts, check CLI `testdata/protocol-v1.json`, API `tests/fixtures/protocol-v1.json`, and the embedded vector in App `Tests/Core/AgentNotificationTests.swift`; run affected notification and navigation tests. Follow each repository's verification entry points. Report gaps when repositories or environments are unavailable; tests in one repository alone do not establish cross-client compatibility.
- Distinguish API acceptance, APNs acceptance, phone display, and successful navigation. Local tests do not prove real push delivery.
