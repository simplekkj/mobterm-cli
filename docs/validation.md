# Verifying mobterm-cli

Run these checks from the repository root. Install the Go toolchain pinned
in [go.mod](../go.mod), tmux, Python 3.12 (as `python3`) and Node.js 22.
The test fixtures provide their own
configuration, events and protocol peers.

```sh
git diff --check
test -z "$(gofmt -l .)"
go_timeout=10m
if [ "$(uname -sm)" = "Darwin x86_64" ]; then
  go_timeout=45m
fi
MOBTERM_REQUIRE_TMUX=1 go test -timeout="$go_timeout" -count=1 ./...
go vet ./...
MOBTERM_REQUIRE_TMUX=1 go test -race -timeout="$go_timeout" -count=1 ./...
go build -trimpath -o mobterm-cli .
./mobterm-cli version
./mobterm-cli --help
sh -n install.sh
python3 tests/install_test.py
```

## Release packages and installer

```sh
revision=$(git rev-parse HEAD)
python3 scripts/package.py --version v0.0.0 --commit "$revision"
python3 tests/install_test.py --dist dist --commit "$revision"
```

The offline installer suite uses local archives and simulated download
responses. With `--dist` it checks the four Linux/macOS amd64/arm64 packages
and executes the installed native binary. CI also runs the Go, tmux and
installer checks natively on the eight [documented runners](../README.md#build-and-verify).

## Agent compatibility

Use the [platform-specific lifecycle commands](../README.md#build-and-verify)
for pinned Agent installation, macOS signature and execution checks, and
Linux loopback HTTPS. The [CI workflow](../.github/workflows/ci.yml) gives the
exact Linux asdf probe, minimum/current host samples, macOS configuration sample
and official OpenCode/Pi host loading commands. To run the adapter contracts:

```sh
python3 tests/select_agent_samples_test.py
python3 tests/darwin_codex_test.py
npm ci --prefix scripts/native-adapter-tests --ignore-scripts --no-audit --no-fund
node scripts/test-native-adapters.mjs
node scripts/test-native-reply.mjs
```

For the real Codex TUI and shared daemon, supply the verified standalone
Codex 0.171.0 Darwin x86_64 executable in `CODEX_BINARY`. The harness
uses an isolated home, local synthetic services and its own terminal server.
Run each command with the built CLI:

```sh
python3 scripts/test-codex-shared-daemon.py tmux --cli ./mobterm-cli --codex "$CODEX_BINARY"
python3 scripts/test-codex-shared-daemon.py tmux --cli ./mobterm-cli --codex "$CODEX_BINARY" --first-registration
python3 scripts/test-codex-shared-daemon.py tmux --cli ./mobterm-cli --codex "$CODEX_BINARY" --no-daemon
python3 scripts/test-codex-shared-daemon.py tmux --cli ./mobterm-cli --codex "$CODEX_BINARY" --queue-lifecycle
```

For Herdr, replace `tmux` with `herdr` and pass `--herdr "$HERDR_BINARY"`.
For native replies, run [the Claude test](../scripts/test-claude-native-reply.py)
or [the Pi/OpenCode test](../scripts/test-plugin-native-reply.py) using the
invocation in each script and a supported native executable. The
[hook measurement tool](../scripts/measure-codex-hooks.py) documents its
fixture and real-tmux scenarios in `--help`.

Report the revision, OS/architecture, Agent and terminal versions, commands,
and observed results. Separate fixture checks and real host loading from API
acceptance, APNs acceptance, phone display and navigation. Record any skipped
or unavailable platform.

## Check staged files and commits

Install [Gitleaks v8.30.1](https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1)
and verify the archive against that release's checksums. With the binary on
your PATH, scan the staged files before committing. Before pushing, scan the
commits from the previous remote tip through the local tip.

```sh
python3 scripts/check-public-boundary.py
python3 scripts/check-public-boundary.py --base "$BASE_SHA" --head "$HEAD_SHA"
```

Use full commit IDs for both revisions. The public CI repeats the check before
building installer packages. Review the affected file locally when a rule
reports a finding.
