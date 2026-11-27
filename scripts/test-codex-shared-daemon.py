#!/usr/bin/env python3
"""Reproduce shared Codex daemon ownership and queue races with synthetic local input.

The named Codex package must be the signed standalone Darwin x86_64 release
for Codex 0.171.0. Each invocation owns a fresh private fixture.
"""

import argparse
import atexit
import base64
import hashlib
import json
import os
import queue
import shlex
import shutil
import socket
import struct
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from contextlib import ExitStack


sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tests"))
from codex_daemon_fixture import CodexFixture, Client, ids, log, prepare_runner, until
from darwin_codex import identity

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("mux", choices=("tmux", "herdr"))
parser.add_argument("--cli", type=Path, required=True)
parser.add_argument("--codex", type=Path, required=True)
parser.add_argument("--herdr", type=Path)
mode = parser.add_mutually_exclusive_group()
mode.add_argument("--first-registration", action="store_true", help="Hold the first notify until the next turn reaches the local inbox")
mode.add_argument("--no-daemon", action="store_true", help="Probe an embedded TUI with a separate stdio app-server")
mode.add_argument("--queue-lifecycle", action="store_true", help="Probe cold resume and interruption in a disposable native app-server")
args = parser.parse_args()
kind = args.mux
if kind == "herdr" and args.herdr is None:
    parser.error("--herdr is required with herdr")

prepare_runner()
stack = ExitStack()
atexit.register(stack.close)
fixture = CodexFixture(stack, kind, args.cli, args.codex, args.herdr, queue_lifecycle=args.queue_lifecycle)
root, profile, work, store = fixture.root, fixture.profile, fixture.work, fixture.store
cli, native, initial = fixture.cli, fixture.native, fixture.initial
env, wait_model, release = fixture.env, fixture.wait_model, fixture.release
enter, notice, visible = fixture.enter, fixture.notice, fixture.visible
tm = fixture.tm


class Capture(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def result(self, data):
        body = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.result(
            dict(
                v=2,
                pair_id=pair,
                installation_id=installation,
                enabled=True,
                inbox=True,
                states=True,
                capability_generation=1,
                policy_generation=1,
                expires_at=int(time.time()) + 50,
                policy=dict(turn_completed=True, needs_input=True, abnormal_end=True),
                approvals=dict(codex=False, claude=False, pi=False, opencode=False),
            )
        )

    def do_POST(self):
        data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.result(dict(v=2, event_id=data["event_id"], state="accepted", outbox_handoff=True))
        (root / (data["event_id"] + ".delivery")).write_text(json.dumps(data))


capture = ThreadingHTTPServer(("127.0.0.1", 0), Capture)
threading.Thread(target=capture.serve_forever, daemon=True).start()
stack.callback(capture.server_close)
stack.callback(capture.shutdown)
installation = "I" * 32
pair = "P" * 32
sender = base64.urlsafe_b64encode(os.urandom(32)).decode().rstrip("=")
origin = "http://127.0.0.1:" + str(capture.server_port)
sk = fixture.write_pair(installation, pair, sender, origin)
fixture.configure_notify()
gate = None
gate_path = root / "first-notify.sock"
if args.first_registration:
    gate = socket.socket(socket.AF_UNIX)
    gate.bind(str(gate_path))
    gate.listen(1)
    gate.settimeout(20)
    stack.callback(gate.close)


class StdioClient:
    """An independent native app-server over the same isolated persistent store."""

    def __init__(self, home):
        assert identity(native) == initial
        self.proc = subprocess.Popen(
            [native, "app-server", "--listen", "stdio://"],
            env=dict(env, CODEX_HOME=str(home)), cwd=work,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
        )
        self.responses = queue.Queue()
        self.completions = queue.Queue()
        self.requests = queue.Queue()
        self.n = 0
        threading.Thread(target=self.read_responses, daemon=True).start()
        self.rpc("initialize", {
            "clientInfo": {"name": "mobterm_embedded_probe", "version": "1"},
            "capabilities": {"experimentalApi": True},
        })
        self.send({"method": "initialized"})

    def read_responses(self):
        for line in self.proc.stdout:
            response = json.loads(line)
            if "id" in response and "method" in response:
                self.requests.put(response)
            elif "id" in response:
                self.responses.put(response)
            elif response.get("method") == "turn/completed":
                self.completions.put(response["params"])

    def send(self, value):
        self.proc.stdin.write(json.dumps(value) + "\n")
        self.proc.stdin.flush()

    def rpc(self, method, params):
        self.n += 1
        self.send({"id": self.n, "method": method, "params": params})
        response = self.responses.get(timeout=10)
        assert response["id"] == self.n
        if "error" in response:
            raise ValueError(method + " " + str(response["error"]["code"]))
        return response["result"]

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=4)
        except subprocess.TimeoutExpired:
            self.proc.terminate()
            self.proc.wait(timeout=4)


def check(c, d):
    thread, turn = ids(d)
    t = c.rpc("thread/read", {"threadId": thread, "includeTurns": False})["thread"]
    turns = c.rpc("thread/turns/list", {"threadId": thread, "limit": 1})["data"]
    q = c.rpc("thread/queue/list", {"threadId": thread})["data"]
    return (
        thread in c.rpc("thread/loaded/list", {})["data"]
        and not t.get("parentThreadId")
        and t["canAcceptDirectInput"]
        and t["status"]["type"] == "idle"
        and turns[0]["id"] == turn
        and turns[0]["status"] == "completed"
        and not q
    )


def add(c, d, prompt):
    token = str(uuid.uuid4())
    v = c.rpc(
        "thread/queue/add",
        {
            "threadId": d["thread-id"],
            "clientUserMessageId": token,
            "input": [{"type": "text", "text": prompt, "text_elements": []}],
        },
    )["queuedSubmission"]
    assert v["clientUserMessageId"] == token and v["input"][0]["text"] == prompt
    return bool(v["id"])


def embedded_probe(pane, first):
    field = "tmux" if kind == "tmux" else "herdr"
    assert first[field] == pane
    thread, turn = ids(first)
    # The isolated native notify passed through the CLI's canonical root check.
    # Authenticate and decode the actual v1 envelope generated for this Pair.
    occurrence = hashlib.sha256(("codex\x00" + thread + "\x00" + turn).encode()).hexdigest()
    event = hashlib.sha256((occurrence + "\x00" + pair).encode()).hexdigest()
    packet = root / (event + ".delivery")
    until(packet.is_file)
    envelope = json.loads(packet.read_text())
    crypto = """
        const crypto = require('node:crypto');
        const {key, envelope: e} = JSON.parse(require('node:fs').readFileSync(0));
        const data = Buffer.from(e.ciphertext, 'base64');
        const decipher = crypto.createDecipheriv('aes-256-gcm', Buffer.from(key, 'base64'), Buffer.from(e.nonce, 'base64'));
        decipher.setAAD(Buffer.from(['mobterm/1',e.pair_id,e.event_id,e.kind,e.expires_at].join('\\n')));
        decipher.setAuthTag(data.subarray(-16));
        process.stdout.write(Buffer.concat([decipher.update(data.subarray(0,-16)), decipher.final()]));
    """
    locator = json.loads(subprocess.check_output(
        [shutil.which("node"), "-e", crypto], env=env, cwd=work,
        input=json.dumps({"key": sk, "envelope": envelope}).encode(), timeout=6,
    ))
    assert locator.pop("agent") == "codex"
    locator.pop("summary", None)
    assert locator["backend"] == kind and locator["pane_id"] == pane
    body = json.dumps(locator).encode()
    def target():
        result = subprocess.run(
            [cli, "target", "resolve", "--stdin-frame", "--read-only"],
            input=struct.pack("!I", len(body)) + body,
            env=dict(env, MOBTERM_CONFIG_DIR=str(store)), cwd=work, capture_output=True, timeout=10,
        )
        return json.loads(result.stdout)
    assert target()["status"] == "valid"
    log(embedded_notice=True, native_thread_turn=True, initial_locator_verified=True, pane=pane)

    other = root / "unrelated-profile"
    other.mkdir()
    wrong = StdioClient(other)
    try:
        try:
            wrong.rpc("thread/read", {"threadId": thread, "includeTurns": False})
        except ValueError:
            log(other_profile_read_refused=True)
        else:
            raise AssertionError("unrelated profile resolved the fixture thread")
    finally:
        wrong.close()

    client = StdioClient(profile)
    try:
        def head():
            return client.rpc("thread/turns/list", {"threadId": thread, "limit": 1})["data"][0]
        latest = head()
        assert latest["id"] == turn and latest["status"] == "completed"
        loaded = thread in client.rpc("thread/loaded/list", {})["data"]
        log(embedded_completed_turn_matches=True, separate_stdio_reports_loaded=loaded,
            verified_live_gate=check(client, first))
        assert add(client, first, "mobile-probe-embedded-follow")
        follow = notice("mobile-probe-embedded-follow")
        assert follow["thread-id"] == thread and follow["turn-id"] != turn
        until(lambda: follow["last-assistant-message"] in visible(pane))
        assert head()["id"] == follow["turn-id"]
        enter(pane, "mobile-probe-race-terminal")
        assert wait_model.wait(20)
        # The model barrier proves that terminal input has started a new turn.
        # The independent stdio queue still accepts an add based on the old head.
        racing = head()
        log(stdio_observed_head_status=racing["status"], stdio_observed_new_turn=racing["id"] != follow["turn-id"])
        assert add(client, follow, "mobile-probe-embedded-stale")
        release.set()
        late = notice("mobile-probe-embedded-stale")
        assert late["thread-id"] == thread and late["turn-id"] != follow["turn-id"]
        log(embedded_own_tui_consumed=True, stale_queue_accepted_and_consumed=True)
        enter(pane, "/new")
        time.sleep(0.65)
        enter(pane, "mobile-probe-after-switch")
        changed = notice("mobile-probe-after-switch")
        assert changed["thread-id"] != thread and changed[field] == pane
        log(new_thread_same_pane=True, original_target_still_resolves=target().get("status") == "valid")
        if kind == "tmux":
            tm("respawn-pane", "-k", "-t", pane, "/bin/sh")
        else:
            enter(pane, "/quit")
            shell_returned = root / "shell-returned"
            time.sleep(0.4)
            enter(pane, "touch " + shlex.quote(str(shell_returned)))
            until(shell_returned.exists)
        retained = client.rpc("thread/read", {"threadId": thread, "includeTurns": False})["thread"]
        assert retained["id"] == thread
        log(
            embedded_tui_exited=True,
            stored_thread_readable=True,
            original_target_still_resolves_after_exit=target().get("status") == "valid",
        )
    finally:
        release.set()
        client.close()


def queue_lifecycle():
    # Only this experiment acts as the user starting, interrupting and
    # explicitly resuming disposable threads. Production append never does so.
    client = StdioClient(profile)

    def start_thread():
        return client.rpc("thread/start", {"cwd": str(work), "modelProvider": "localprobe"})["thread"]["id"]

    def start_turn(thread, text):
        return client.rpc(
            "turn/start",
            {
                "threadId": thread,
                "input": [{"type": "text", "text": text, "text_elements": []}],
            },
        )["turn"]["id"]

    def completed(thread, status):
        event = client.completions.get(timeout=25)
        assert event["threadId"] == thread and event["turn"]["status"] == status

    def queued(thread):
        return [
            r["clientUserMessageId"]
            for r in client.rpc("thread/queue/list", {"threadId": thread, "limit": 100})["data"]
        ]

    def enqueue(thread, token):
        row = client.rpc(
            "thread/queue/add",
            {
                "threadId": thread,
                "clientUserMessageId": token,
                "input": [{"type": "text", "text": token, "text_elements": []}],
            },
        )["queuedSubmission"]
        assert row["clientUserMessageId"] == token and row["input"][0]["text"] == token and row["id"]

    def admitted(thread, token):
        items = client.rpc(
            "thread/items/list",
            {"threadId": thread, "sortDirection": "desc", "limit": 16},
        )["data"]
        return any(
            r["item"].get("type") == "userMessage"
            and r["item"].get("clientId") == token
            and r["item"]["content"][0].get("text") == token
            for r in items
        )

    try:
        for marker, expected_method, flag in (
            ("probe-approval", "item/commandExecution/requestApproval", "waitingOnApproval"),
            ("probe-question", "item/tool/requestUserInput", "waitingOnUserInput"),
        ):
            target = client.rpc(
                "thread/start",
                {
                    "cwd": str(work),
                    "modelProvider": "localprobe",
                    "approvalPolicy": "on-request",
                    "sandbox": "read-only",
                },
            )["thread"]["id"]
            params = {"threadId": target, "input": [{"type": "text", "text": marker, "text_elements": []}]}
            if marker == "probe-question":
                params["collaborationMode"] = {
                    "mode": "plan",
                    "settings": {"model": "gpt-5.1", "reasoning_effort": None, "developer_instructions": None},
                }
            turn_id = client.rpc("turn/start", params)["turn"]["id"]
            pending = client.requests.get(timeout=18)
            assert (
                pending["method"] == expected_method
                and pending["params"]["threadId"] == target
                and pending["params"]["turnId"] == turn_id
            ), pending["method"]
            enqueue(target, "同意")
            assert queued(target) == ["同意"]
            status = client.rpc("thread/read", {"threadId": target, "includeTurns": False})["thread"]["status"]
            assert flag in status["activeFlags"], status["type"]
            log(native_request=pending["method"], append_queued_request_still_pending=True)
            # End the synthetic pending request as the test operator, separately
            # from queue/add. Approval is denied; no command is run for the test.
            answer = (
                {"decision": "decline"}
                if marker == "probe-approval"
                else {"answers": {"choice": {"answers": ["First"]}}}
            )
            client.send({"id": pending["id"], "result": answer})
            completed(target, "completed")
            until(lambda: admitted(target, "同意"))
            completed(target, "completed")
        ordinary = start_thread()
        start_turn(ordinary, "mobile-probe-lifecycle")
        completed(ordinary, "completed")
        interrupted = start_thread()
        turn = start_turn(interrupted, "mobile-probe-race-terminal")
        assert wait_model.wait(12)
        enqueue(interrupted, "mobile-probe-before-interrupt")
        client.rpc("turn/interrupt", {"threadId": interrupted, "turnId": turn})
        completed(interrupted, "interrupted")
        release.set()
        enqueue(interrupted, "mobile-probe-after-interrupt")
        expected = ["mobile-probe-before-interrupt", "mobile-probe-after-interrupt"]
        client.rpc("thread/resume", {"threadId": interrupted})
        assert queued(interrupted) == expected
        client.close()
        client = StdioClient(profile)
        assert ordinary not in client.rpc("thread/loaded/list", {})["data"]
        enqueue(ordinary, "mobile-probe-cold-resume")
        assert queued(ordinary) == ["mobile-probe-cold-resume"]
        assert (
            client.rpc("thread/read", {"threadId": ordinary, "includeTurns": False})["thread"]["status"]["type"]
            == "notLoaded"
        )
        assert ordinary not in client.rpc("thread/loaded/list", {})["data"]
        client.rpc("thread/resume", {"threadId": ordinary})
        until(lambda: admitted(ordinary, "mobile-probe-cold-resume"))
        assert queued(ordinary) == []
        client.rpc("thread/resume", {"threadId": interrupted})
        enqueue(interrupted, "mobile-probe-cold-interrupted")
        time.sleep(0.5)
        assert queued(interrupted) == expected + ["mobile-probe-cold-interrupted"]
        # Capacity and archival exercise upstream rejections with the same
        # disposable threads. Nothing here invokes the product dispatcher.
        for i in range(100 - len(queued(interrupted))):
            enqueue(interrupted, "mobile-probe-limit-" + str(i))
        try:
            enqueue(interrupted, "mobile-probe-over-capacity")
        except ValueError:
            assert len(queued(interrupted)) == 100
        else:
            raise AssertionError("native queue exceeded its documented capacity")
        client.rpc("thread/archive", {"threadId": ordinary})
        try:
            enqueue(ordinary, "mobile-probe-archived")
        except ValueError:
            pass
        else:
            raise AssertionError("native queue accepted an archived thread")
        client.rpc("thread/delete", {"threadId": ordinary})
        try:
            enqueue(ordinary, "mobile-probe-deleted")
        except ValueError:
            pass
        else:
            raise AssertionError("native queue accepted a deleted thread")
        log(capacity_100_verified=True, archived_refused=True, deleted_refused=True)
        log(
            unloaded_add_stays_unloaded=True,
            manual_cold_resume_admission=True,
            busy_queued=True,
            interrupted_normal_resume_pending=True,
            interrupted_cold_resume_pending=True,
        )
    finally:
        release.set()
        client.close()


c = None
try:
    assert identity(native) == initial
    if args.queue_lifecycle:
        queue_lifecycle()
        sys.exit(0)
    a, first = fixture.first_pane(no_daemon=args.no_daemon)
    if args.first_registration:
        with gate.accept()[0] as paused:
            enter(a, "mobile-probe-next")
            second = notice("mobile-probe-next")
            assert second["thread-id"] == first["thread-id"] and second["turn-id"] != first["turn-id"]
            until(lambda: list(root.glob("*.delivery")))
            paused.sendall(b"1")
        diag = root / (first["turn-id"] + ".diag")
        until(lambda: diag.exists() and "codex_completion_turn_unconfirmed" in diag.read_text())
        db = json.loads((store / "runs-v2.json").read_text())
        [run] = db["runs"].values()
        [head] = db["visible"].values()
        event = hashlib.sha256((run["first_occurrence"] + "\x00" + pair).encode()).hexdigest()
        assert run["run"] == second["turn-id"] and run["codex_runtime"]["peer_pid"] == second["ppid"]
        assert head["event"] == event and head["run_order"] == 1
        assert [f.stem for f in root.glob("*.delivery")] == [event]
        log(b_first=True, stale_first_callback_rejected=True, durable_b_head=True, encrypted_events=1)
        sys.exit(0)
    if args.no_daemon:
        embedded_probe(a, first)
        sys.exit(0)
    b, second = fixture.second_pane(a, first)
    field = "tmux" if kind == "tmux" else "herdr"
    c = Client(profile)
    for i, (p, other, d) in enumerate([(a, b, first), (b, a, second)]):
        # The local mock responds instantly. Native status can settle just after notify.
        until(lambda: check(c, d))
        prompt = "mobile-probe-follow-" + str(i)
        assert add(c, d, prompt)
        done = notice(prompt)
        assert done["thread-id"] == d["thread-id"] and done["turn-id"] != d["turn-id"]
        msg = done["last-assistant-message"]
        until(lambda: msg in visible(p))
        assert msg not in visible(other)
        log(
            pane=p,
            completed_turn_matches_notify=True,
            loaded_main=True,
            queue_ack=True,
            own_tui=True,
            other_tui=False,
        )
        if i == 1:
            current = done
    assert check(c, current)  # last check in the native dispatch lane
    enter(b, "mobile-probe-race-terminal")
    assert wait_model.wait(20)
    # An independent observer, not the simulated sender, reads the new active turn.
    observer = Client(profile)
    rt = observer.rpc("thread/turns/list", {"threadId": current["thread-id"], "limit": 1})["data"][0]
    log(interposed_turn=rt["status"], turn_changed=rt["id"] != current["turn-id"])
    assert rt["id"] != current["turn-id"]
    accepted = add(c, current, "mobile-probe-stale-after-check")
    v = observer.rpc("thread/queue/list", {"threadId": current["thread-id"]})["data"]
    log(
        stale_accepted=accepted,
        queued_while_interposed=any(q["input"][0]["text"] == "mobile-probe-stale-after-check" for q in v),
    )
    release.set()
    late = notice("mobile-probe-stale-after-check")
    before = c.mutations
    if check(c, current):
        add(c, current, "unexpected-duplicate")
    assert c.mutations == before and late["thread-id"] == current["thread-id"]
    log(
        stale_consumed_same_thread=True,
        observed_new_input_guard=True,
        guard_native_writes=c.mutations - before,
    )
    observer.close()
    c.close()
    c = None
    enter(b, "/new")
    time.sleep(0.65)
    enter(b, "mobile-probe-after-switch")
    changed = notice("mobile-probe-after-switch")
    log(
        same_pane_switch=changed["thread-id"] != second["thread-id"],
        callback_still_points_to=changed[field],
        same_pid_daemon=changed["ppid"] == first["ppid"],
    )
    # Replace the test TUI with an inert shell in the same pane. Keep the other TUI attached.
    if kind == "tmux":
        tm("respawn-pane", "-k", "-t", b, "/bin/sh")
    else:
        enter(b, "/quit")
        shell_returned = root / "shell-returned"
        # A shell can execute this fixed command only after the test TUI exits.
        time.sleep(0.4)
        enter(b, "touch " + shlex.quote(str(shell_returned)))
        until(shell_returned.exists)
    time.sleep(0.6)
    c = Client(profile)
    log(
        daemon_reachable_after_exit=True,
        first_thread_loaded=first["thread-id"] in c.rpc("thread/loaded/list", {})["data"],
        new_thread_loaded_after_exit=changed["thread-id"] in c.rpc("thread/loaded/list", {})["data"],
    )

    # Both first user notices must have reached the paired local API. Other
    # fixture events may be deliberately superseded during the race.
    def admitted(d):
        records = json.loads((store / "runs-v2.json").read_text())["runs"].values()
        record = next((r for r in records if r["run"] == d["turn-id"]), None)
        if not record:
            return False
        source = record.get("codex_runtime") or {}
        event = hashlib.sha256((record["first_occurrence"] + "\x00" + pair).encode()).hexdigest()
        return (
            record.get("codex_root_verified")
            and source.get("peer_pid") == d["ppid"]
            and source.get("installation_id") == installation
            and (root / (event + ".delivery")).is_file()
        )

    assert admitted(first) and admitted(second)
    log(two_authenticated_completions=True, encrypted_events=len(list(root.glob("*.delivery"))))
    print(
        "CLI notify diagnostics",
        sorted(set(f.read_text().strip() for f in root.glob("*.diag") if f.stat().st_size)),
        flush=True,
    )
finally:
    release.set()
    if c:
        c.close()
    stack.close()
