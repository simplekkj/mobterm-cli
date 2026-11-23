# Read-only workspace discovery over SSH

## Capability and directory queries

`capabilities --json` reports `v:1`, binary `version` and `commit`, Unix
`server_time`, `operations`, `execution_backends`, numeric `limits`, and the
existing `installation_id` when initialized. The probe reads local state without
creating a directory, lock or identity. Invalid private state fails the probe.
The execution backend list is empty in this version. Unrecognized
commands on older CLIs fail before user command submission. The client may use
its previous SSH discovery path on a trusted connection.

`discovery OP --stdin-frame` reads a four-byte big-endian JSON length (1–16384),
then one UTF-8 JSON object and EOF. The request carries `v:1` and the absolute
`executable` path already resolved by the App. The CLI invokes that exact
program with argument arrays and bounds output. Results include `v:1`, the
requested `operation` and the client version banner. Output JSON including
newline is limited to 4 MiB.

| Operation | Arguments | Result |
| --- | --- | --- |
| `tmux.discovery` | optional absolute `socket` | `panes` array. The single read-only list-panes command returns session/window/pane IDs and names, active flags, indexes, history size (lines) and `server_identity` (PID, server start and session creation). Client and running server must meet the existing tmux 3.2 minimum. Up to 64 sessions and 4096 panes per session. Missing server returns an empty list. Permission, version, malformed data or mixed identity fails. |
| `herdr.list` | explicit `namespace` | Validated `sessions` with name and running flag. |
| `herdr.probe` | explicit `namespace` | Same directory and that namespace's `status`, including running server version, protocol and compatibility. One failed query fails the probe. |
| `herdr.snapshot` | explicit `namespace` | Native typed `snapshot` plus `server_identity` derived from the verified namespace socket before and after the read. Pane/terminal identities stay in the snapshot, which the App validates before use. |

Herdr queries unset inherited HERDR pane/socket/session settings and retain the
existing minimum 0.9.1. Discovery performs no workspace mutation. It does not
attach a client, choose a window, create a session or send terminal input.
