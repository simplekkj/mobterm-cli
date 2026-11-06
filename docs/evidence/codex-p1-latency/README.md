# Codex P1 subprocess latency evidence

The initial implementation failed to durably receive 10/512 concurrent and 2/64 historical/multipair observations before returning; `after-initial` preserves that failed result and its source manifest. It is not the final acceptance result. `after-identity` then measured the installation identity read optimization: target queries completed for all samples, but 8 concurrent and 1 history/multipair observation still failed durable receipt; these failed results are likewise retained. `after-reception-priority` records a subsequent lock-priority attempt with 9 concurrent and 2 history/multipair failures; it is also excluded from final acceptance. The subsequent `after-queued-lock` focused rerun preserved all 576 observations with zero foreground HTTP attempts; its final full-matrix rerun is recorded separately as `after`. The receiver-priority gate was removed from the final code.

The CSV files are raw measurements (milliseconds, no removed outliers); matching JSON files contain nearest-rank P50/P95/P99, source/build fingerprints, environment and counters. `scripts/measure-codex-hooks.py` invokes the actual CLI executable for every sample, through the installed command (`observe --source codex`, or `approval wait --source codex`). It never invokes a model or reads user configuration.

The baseline executable was built with `go build -trimpath` from a `git archive` of `a1cae43c2ce260e27bffbbb3a2ed6cb51013b5dc`. The after executable uses the same toolchain/build flags. The source identifier and executable SHA256 are recorded in each JSON file. `baseline-sources.json` and `after-sources.json` list SHA256 for every root Go source (including tests and initially untracked files), go.mod and go.sum; the after source identifier includes the full manifest digest. This is a shared development host, not a dedicated benchmark machine. Each formal `baseline` and `after` measurement window ran with repository builds/tests paused. Debugging, rebuilds and focused regression checks occurred between windows; the final matrix used the same host, toolchain, build flags and fixtures. An earlier exploratory run overlapped orphaned test children; its `exploratory-contaminated` files are retained for traceability and excluded from all comparisons. The exploratory run also used the original larger concurrency/history sample counts. Both comparison runs use the same fixtures, timed invocation function and sample counts. The harness later gained only an optional `--scenarios` selector to repeat failing scenarios; each run records its actual harness fingerprint.

Each ordinary scenario runs eight unrecorded Pre/Post pairs for warmup, then records the wall clock from before subprocess launch until foreground completion, including startup, target validation, lock acquisition, storage and any synchronous network. Pre/Post alternate for each invocation so that an unbounded set of unfinished tools does not artificially exhaust storage. The concurrent scenario has eight lanes sharing one configuration root, with a distinct turn per lane. It still preserves Pre then Post within a lane. The immediate read after each tool invocation verifies the event occurrence exists in the durable run ledger; this read is outside the measured time. A CLI exit code of zero alone is not treated as successful receipt, because hooks intentionally fail open.

No Codex process is launched; payloads follow the repository's 0.166.0 Hook schema baseline. The CLI binaries are `dev` builds, identified by their source manifests and binary SHA256 rather than a release version.

Most scenarios use a counted local Unix-socket Herdr protocol fixture (0.9.3/protocol 22), not the Herdr application. `real-tmux` instead starts and destroys an isolated real tmux server on its own temporary socket. Targets are verified by the unchanged production code. All API traffic goes to a private loopback HTTP receiver with synthetic pairs; successful event receipts are controlled `accepted`/`outbox_handoff` responses, not real API or APNs acceptance.

`normal-sequential` contains 1,000 Pre and 1,000 Post samples; `normal-concurrent8` contains 256 of each (32 Pre/Post pairs per lane). `cold-cache` removes the capability-cache file before each invocation; the production state-send schedule is retained, so this does not force a network call on every invocation. `slow-api` does the same with a 300 ms delay per receiver request. `offline-api` uses an immediately refused local TCP port, not a DNS/TLS black hole. `history-multipair` measures 32 Pre/Post pairs and uses three pairs and 16 prior interrupted runs with retained delivery history. Its exact starting run/file counts are in JSON; the legacy `history_fixture.outbox_files` counter counts all JSON files in the temporary store, not only outbox shards. `real-tmux` has 100 samples per event. The other five installed Hook types have 32 samples each and no warmup: UserPromptSubmit, PermissionRequest, Interrupt, SessionStart and SessionEnd. The latter two are currently ignored by the activity parser, so their timings intentionally include no target or persistence. PermissionRequest is measured with phone approval disabled; **no artificial or human waiting time is represented as approval-processing performance**.

The `synchronous_http` CSV column counts foreground `network_request` diagnostics, including failed connection attempts. Children do not inherit the foreground diagnostic pipe. It therefore separates foreground attempts from `total_receiver_http` (requests accepted by the local receiver during the recorded calls, including dispatcher traffic). Herdr target-query counts come from the fixture. Worker process counts are sampled every 50 ms with `ps`, matching `activity flush` and `activity dispatch` at the task-specific executable path; this gives an observed maximum, not a mathematical proof of the concurrency bound. Code-level/behavioral worker tests provide that bound. Polling overhead is included equally in both runs. Receiver overlap is also recorded.

These measurements include lock and disk costs in wall-clock time, but do not separately instrument lock-wait duration, fsync count/latency or the real tmux query count. Those JSON values are null rather than invented. The script does not establish real Codex trust activation, live native approval aggregation, real Herdr operation, phone/APNs delivery or Jump to UI behavior. Small slow/offline/other-Hook samples do not establish stable production tail latency. Request lifecycle, lost-wakeup, crash recovery, retry-envelope integrity and policy revocation require the focused Go regression tests, not the performance receiver.

Reproduction (use isolated baseline and after executables):

```sh
python3 scripts/measure-codex-hooks.py --binary /absolute/path/to/mobterm-cli \
  --source EXACT_SOURCE_IDENTIFIER --label baseline \
  --output docs/evidence/codex-p1-latency --samples 1000
```

The temporary stores contain synthetic test credentials only and are removed after each scenario. No Hook inputs, command contents, encryption keys, tokens or notification ciphertext are written to the evidence artifacts.

## Final comparison

The final run contains 3,080 CLI calls. All 2,920 ordinary tool observations were durable when the foreground returned, had zero foreground HTTP attempts, exited zero and emitted no stdout. The observed maximum was 2 dispatcher processes (one active plus one pending) and 1 concurrent receiver request. The clean baseline lost 22 of 512 concurrent observations; the final full run lost none. These are measured fixture results, not a guarantee for arbitrary load.

All values below are milliseconds; each triple is P50 / P95 / P99. Counts are per Hook and include failed receipt attempts in baseline percentiles.

| Scenario / Hook | N | Baseline P50 / P95 / P99 | Final P50 / P95 / P99 | Foreground HTTP attempts, baseline → final | Durable receipt failures, baseline → final |
| --- | ---: | --- | --- | ---: | ---: |
| normal-sequential / PostToolUse | 1000 | 84.59 / 260.87 / 285.18 | 64.62 / 90.61 / 99.77 | 54 → 0 | 0 → 0 |
| normal-sequential / PreToolUse | 1000 | 84.73 / 178.12 / 278.60 | 64.56 / 90.65 / 100.27 | 47 → 0 | 0 → 0 |
| normal-concurrent8 / PostToolUse | 256 | 416.13 / 1903.07 / 2016.65 | 374.50 / 420.04 / 501.70 | 11 → 0 | 7 → 0 |
| normal-concurrent8 / PreToolUse | 256 | 393.71 / 2014.39 / 2024.19 | 371.36 / 412.59 / 422.87 | 9 → 0 | 15 → 0 |
| cold-cache / PostToolUse | 40 | 81.03 / 103.66 / 314.48 | 63.19 / 90.44 / 95.85 | 4 → 0 | 0 → 0 |
| cold-cache / PreToolUse | 40 | 81.37 / 89.78 / 314.71 | 63.26 / 90.23 / 99.93 | 4 → 0 | 0 → 0 |
| slow-api / PostToolUse | 16 | 81.17 / 91.93 / 91.93 | 60.66 / 94.92 / 94.92 | 0 → 0 | 0 → 0 |
| slow-api / PreToolUse | 16 | 82.30 / 915.50 / 915.50 | 61.77 / 94.96 / 94.96 | 4 → 0 | 0 → 0 |
| offline-api / PostToolUse | 16 | 81.86 / 127.62 / 127.62 | 60.17 / 73.55 / 73.55 | 16 → 0 | 0 → 0 |
| offline-api / PreToolUse | 16 | 83.56 / 91.27 / 91.27 | 61.64 / 69.95 / 69.95 | 16 → 0 | 0 → 0 |
| history-multipair / PostToolUse | 32 | 92.87 / 98.95 / 643.71 | 91.09 / 198.20 / 199.99 | 3 → 0 | 0 → 0 |
| history-multipair / PreToolUse | 32 | 92.84 / 647.73 / 1095.50 | 92.54 / 183.87 / 225.43 | 13 → 0 | 0 → 0 |
| real-tmux / PostToolUse | 100 | 104.53 / 281.99 / 290.63 | 85.53 / 97.47 / 110.69 | 6 → 0 | 0 → 0 |
| real-tmux / PreToolUse | 100 | 104.62 / 286.04 / 288.91 | 84.04 / 94.50 / 109.99 | 6 → 0 | 0 → 0 |
| installed-UserPromptSubmit / UserPromptSubmit | 32 | 83.84 / 401.47 / 405.04 | 82.31 / 401.36 / 440.68 | 3 → 3 | not sampled |
| installed-PermissionRequest / PermissionRequest | 32 | 583.39 / 1013.23 / 1100.98 | 65.83 / 102.02 / 103.00 | 0 → 0 | not sampled |
| installed-Interrupt / Interrupt | 32 | 589.04 / 642.04 / 714.84 | 587.01 / 599.06 / 828.01 | 33 → 33 | not sampled |
| installed-SessionStart / SessionStart | 32 | 13.14 / 15.64 / 15.87 | 12.88 / 14.73 / 15.79 | 0 → 0 | not sampled |
| installed-SessionEnd / SessionEnd | 32 | 12.98 / 16.72 / 17.96 | 12.75 / 15.85 / 16.65 | 0 → 0 | not sampled |

UserPromptSubmit and Interrupt still use their existing synchronous business paths. Their small-sample tails are not consistently lower; this change does not claim to optimize every Hook. SessionStart/End remain parser no-ops. The PermissionRequest row measures disabled mobile approval only.

The loopback receiver saw 70 HTTP requests during the final 2,000 sequential tool calls, entirely from dispatchers; the baseline foreground made 101 requests. Both made 2,000 counted Herdr target queries. In the eight-lane case the final receiver saw 1 request while all 512 calls were active, with 512 target queries; dispatchers can drain after the measured foreground interval. Do not interpret this as dropped business reminders or total lifetime network traffic.

Before commit, CSV line endings were normalized from the Python default CRLF to
LF and the exporter now explicitly writes LF. Every parsed sample cell was
compared before/after and remained identical. Stored harness fingerprints name
the actual measurement-time script, before this output-format-only adjustment.
