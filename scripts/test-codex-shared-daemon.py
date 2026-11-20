#!/usr/bin/env python3
"""Reproduce shared Codex daemon ownership and queue races with synthetic local input.

The named Codex package must be the signed standalone Darwin x86_64 release
from the validation record. Each invocation owns a fresh private fixture.
"""

import argparse
import base64
import hashlib
import json
import os
import queue
import shlex
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

os.umask(0o077)
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("mux", choices=("tmux", "herdr"))
parser.add_argument("--cli", type=Path, required=True)
parser.add_argument("--codex", type=Path, required=True)
parser.add_argument("--herdr", type=Path)
mode = parser.add_mutually_exclusive_group()
mode.add_argument("--first-registration", action="store_true", help="Hold the first notify until the next turn reaches the local inbox")
mode.add_argument("--no-daemon", action="store_true", help="Probe an embedded TUI with a separate stdio app-server")
args = parser.parse_args()
kind = args.mux
if kind == "herdr" and args.herdr is None:
    parser.error("--herdr is required with herdr")
cli = args.cli.resolve(strict=True)
native = args.codex.resolve(strict=True)
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tests"))
from darwin_codex import assess_native, digest, identity

fixture = tempfile.TemporaryDirectory(prefix="mt-d01-", dir="/tmp")
base = Path(fixture.name).resolve()
# Keep all counters and synthetic artifacts together. The fixture has no access
# to the user's model credentials or real multiplexer sockets.
root = base / kind
root.mkdir()
profile = root / "profile"
profile.mkdir()
work = root / "work"
work.mkdir()
initial = identity(native)
assess_native(native)
assert identity(native) == initial
expected = "0cc2c99ee61041ecde0c848f85861ea9cb6e45c5c136fd66383a7eb2198e307c"
if (
    digest(native) != expected
    or json.loads((native.parent.parent / "codex-package.json").read_text())["version"] != "0.171.0"
):
    sys.exit("Expected the pinned signed Codex 0.171.0 standalone Darwin x86_64 binary.")
env = dict(
    HOME=str(root),
    CODEX_HOME=str(profile),
    PATH=os.pathsep.join(
        [
            str(native.parent),
            str(Path(shutil.which("tmux") or "/usr/bin/tmux").parent),
            "/usr/bin",
            "/bin",
            "/usr/sbin",
            "/sbin",
        ]
    ),
    TERM="xterm-256color",
    NO_PROXY="127.0.0.1,localhost",
    OTEL_SDK_DISABLED="true",
    SHELL="/bin/sh",
    XDG_CONFIG_HOME=str(root / "config"),
    HTTP_PROXY="http://127.0.0.1:9",
    HTTPS_PROXY="http://127.0.0.1:9",
)
assert identity(native) == initial
print("native", subprocess.check_output([native, "--version"], env=env, text=True).strip(), flush=True)
wait_model = threading.Event()
release = threading.Event()
counter = 0


class Mock(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        global counter
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        counter += 1
        i = counter
        # This injected barrier is solely a local model callback. No production user input.
        if "mobile-probe-race-terminal" in json.dumps(body) and not wait_model.is_set():
            wait_model.set()
            release.wait(18)
        rid = "resp_" + str(i)
        msg = "fixture-response-" + str(i)
        events = [
            {"type": "response.created", "response": {"id": rid}},
            {
                "type": "response.output_item.done",
                "item": {
                    "type": "message",
                    "role": "assistant",
                    "id": "msg_" + str(i),
                    "content": [{"type": "output_text", "text": msg}],
                },
            },
            {
                "type": "response.completed",
                "response": {"id": rid, "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}},
            },
        ]
        data = "".join("event: " + e["type"] + "\ndata: " + json.dumps(e) + "\n\n" for e in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


server = ThreadingHTTPServer(("127.0.0.1", 0), Mock)
threading.Thread(target=server.serve_forever, daemon=True).start()


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
store = root / "store"
store.mkdir()
installation = "I" * 32
pair = "P" * 32
sk = base64.b64encode(os.urandom(32)).decode()
(store / "installation.json").write_text(json.dumps(dict(v=1, installation_id=installation)))
(store / "pairs.json").write_text(
    json.dumps(
        {
            pair: dict(
                v=1,
                pair_id=pair,
                installation_id=installation,
                status="active",
                send_token=base64.urlsafe_b64encode(os.urandom(32)).decode().rstrip("="),
                encryption_key=sk,
                api_url="http://127.0.0.1:" + str(capture.server_port),
            )
        }
    )
)
gate = None
gate_path = root / "first-notify.sock"
if args.first_registration:
    gate = socket.socket(socket.AF_UNIX)
    gate.bind(str(gate_path))
    gate.listen(1)
    gate.settimeout(20)
helper = root / "notify.py"
helper.write_text("""import os
import sys
import json
from pathlib import Path

root, cli, raw = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
payload = json.loads(raw)
record = {k: payload.get(k) for k in ("thread-id", "turn-id", "input-messages", "last-assistant-message")}
record.update(tmux=os.getenv("TMUX_PANE"), herdr=os.getenv("HERDR_PANE_ID"), ppid=os.getppid())
out = root / (payload["turn-id"] + ".new")
out.write_text(json.dumps(record))
out.rename(out.with_suffix(".seen"))
gate_path = root / "first-notify.sock"
if gate_path.exists() and (record["input-messages"] or []) == ["mobile-probe-first"]:
    import socket
    with socket.socket(socket.AF_UNIX) as gate:
        gate.settimeout(55)
        gate.connect(str(gate_path))
        if gate.recv(1) != b"1":
            sys.exit("missing test release")
os.environ["MOBTERM_CONFIG_DIR"] = str(root / "store")
with out.with_suffix(".diag").open("w") as output:
    os.dup2(output.fileno(), 1)
    os.dup2(output.fileno(), 2)
os.execv(cli, [cli, "notify", "--source", "codex", raw])
""")
notify = json.dumps([sys.executable, str(helper), str(root), str(cli)])
(profile / "config.toml").write_text(f'''notify = {notify}
model = "gpt-5.1"
model_provider = "localprobe"
approval_policy = "never"
sandbox_mode = "read-only"
check_for_update_on_startup = false
[analytics]
enabled = false
[model_providers.localprobe]
name = "localprobe"
base_url = "http://127.0.0.1:{server.server_port}/v1"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
[projects."{work}"]
trust_level = "trusted"
''')
herdr = str(args.herdr.resolve(strict=True)) if args.herdr else None


def cmd(*args, check=True):
    r = subprocess.run(args, env=env, cwd=work, capture_output=True, text=True, timeout=8)
    if check and r.returncode:
        raise RuntimeError(str(args[1:]) + ": " + r.stderr[-500:])
    return r.stdout.strip()


def tm(*a, **kw):
    return cmd("tmux", "-f", "/dev/null", "-S", str(root / "tmux.sock"), *a, **kw)


def hd(*a, **kw):
    return cmd(herdr, "--session", "proof", *a, **kw)


def snap():
    sock = root / "config/herdr/sessions/proof/herdr.sock"
    with socket.socket(socket.AF_UNIX) as c:
        c.settimeout(3)
        c.connect(str(sock))
        c.sendall(b'{"id":"probe","method":"session.snapshot","params":{}}\n')
        return json.loads(c.makefile("rb").readline())["result"]["snapshot"]


def visible(p):
    if kind == "tmux":
        return tm("capture-pane", "-p", "-t", p)
    return hd("pane", "read", "--source", "visible", "--raw", p)


def key(p, k):
    if kind == "tmux":
        tm("send-keys", "-t", p, k)
    else:
        hd("pane", "send-keys", p, k)


def enter(p, s):
    if kind == "tmux":
        tm("send-keys", "-t", p, "-l", s)
    else:
        hd("pane", "send-text", p, s)
    time.sleep(0.18)
    key(p, "Enter")


def until(fn, timeout=55):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        val = fn()
        if val:
            return val
        time.sleep(0.12)
    raise TimeoutError(str(fn))


def seen(token):
    ds = [json.loads(f.read_text()) for f in root.glob("*.seen")]
    return next((d for d in ds if token in (d["input-messages"] or [])), None)


def notice(token):
    return until(lambda: seen(token))


def ready(p):
    until(lambda: "gpt-5.1" in visible(p), 70)
    time.sleep(0.3)


class Client:
    def __init__(self):
        physical = (profile / "app-server-control/app-server-control.sock").resolve(strict=True)
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(6)
        self.sock.connect(str(physical))
        self.f = self.sock.makefile("rb")
        self.n = 0
        self.mutations = 0
        nonce = base64.b64encode(os.urandom(16)).decode()
        self.sock.sendall(
            (
                "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "
                + nonce
                + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
            ).encode()
        )
        assert b"101" in self.f.readline()
        for _ in range(32):
            line = self.f.readline(8192)
            if line == b"\r\n":
                break
            if not line:
                raise EOFError("incomplete websocket upgrade")
        else:
            raise ValueError("oversized websocket upgrade")
        self.rpc(
            "initialize",
            {
                "clientInfo": {"name": "mobterm_probe", "version": "1"},
                "capabilities": {"experimentalApi": True},
            },
        )
        self.send({"method": "initialized"})

    def send(self, obj):
        data = json.dumps(obj).encode()
        key = os.urandom(4)
        n = len(data)
        header = bytes([129, 128 + n]) if n < 126 else bytes([129, 254]) + struct.pack("!H", n)
        masked = bytes(byte ^ key[i % 4] for i, byte in enumerate(data))
        self.sock.sendall(header + key + masked)

    def rpc(self, m, p):
        if m == "thread/queue/add":
            self.mutations += 1
        self.n += 1
        n = self.n
        self.send({"id": n, "method": m, "params": p})
        while True:
            h = self.f.read(2)
            ln = h[1] & 127
            if ln == 126:
                ln = struct.unpack("!H", self.f.read(2))[0]
            elif ln == 127:
                ln = struct.unpack("!Q", self.f.read(8))[0]
            if ln > 1 << 20:
                raise ValueError("oversized fixture RPC frame")
            raw = self.f.read(ln)
            if h[0] & 15 != 1:
                continue
            d = json.loads(raw)
            if d.get("id") == n:
                if "error" in d:
                    raise RuntimeError(str(d["error"]))
                return d["result"]

    def close(self):
        self.f.close()
        self.sock.close()


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
            if "id" in response:
                self.responses.put(response)

    def send(self, value):
        self.proc.stdin.write(json.dumps(value) + "\n")
        self.proc.stdin.flush()

    def rpc(self, method, params):
        self.n += 1
        self.send({"id": self.n, "method": method, "params": params})
        response = self.responses.get(timeout=10)
        assert response["id"] == self.n
        if "error" in response:
            raise ValueError(str(response["error"]["code"]))
        return response["result"]

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=4)
        except subprocess.TimeoutExpired:
            self.proc.terminate()
            self.proc.wait(timeout=4)


def log(**kw):
    print(json.dumps(kw, ensure_ascii=False), flush=True)


def ids(d):
    return d["thread-id"], d["turn-id"]


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
        log(embedded_tui_exited=True, stored_thread_readable=True,
            original_target_still_resolves_after_exit=target().get("status") == "valid")
    finally:
        release.set()
        client.close()


mux = None
c = None
try:
    assert identity(native) == initial
    launch = "codex --no-daemon" if args.no_daemon else "codex"
    if kind == "tmux":
        log(version=tm("-V"))
        tm("new-session", "-d", "-s", "probe", "-x", "150", "-y", "55", "-c", str(work), launch)
        a = tm("display-message", "-p", "-t", "probe", "#{pane_id}")
        ready(a)
    else:
        mux = subprocess.Popen(
            [herdr, "--session", "proof", "server"],
            cwd=work,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=(root / "herdr.log").open("w"),
            stderr=subprocess.STDOUT,
        )
        until(lambda: (root / "config/herdr/sessions/proof/herdr.sock").is_socket())
        hd("workspace", "create", "--cwd", str(work), "--label", "fixture")
        s = snap()
        log(version=s["version"], protocol=s["protocol"])
        a = s["panes"][-1]["pane_id"]
        enter(a, "export PATH=" + shlex.quote(env["PATH"]))
        enter(a, launch)
        ready(a)
    enter(a, "mobile-probe-first")
    first = notice("mobile-probe-first")
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
    if kind == "tmux":
        b = tm("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", a, "-c", str(work), "codex")
    else:
        hd("pane", "split", "--pane", a, "--direction", "right", "--cwd", str(work))
        b = next(p["pane_id"] for p in snap()["panes"] if p["pane_id"] != a)
        enter(b, "export PATH=" + shlex.quote(env["PATH"]))
        enter(b, "codex")
    ready(b)
    enter(b, "mobile-probe-second")
    second = notice("mobile-probe-second")
    field = "tmux" if kind == "tmux" else "herdr"
    log(
        panes=[a, b],
        notify_env=[first[field], second[field]],
        shared_daemon=first["ppid"] == second["ppid"],
        threads_distinct=ids(first)[0] != ids(second)[0],
    )
    c = Client()
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
    observer = Client()
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
    c = Client()
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
    if gate:
        gate.close()
    if c:
        c.close()
    if kind == "tmux":
        tm("kill-server", check=False)
    elif mux:
        hd("server", "stop", check=False)
        mux.wait(timeout=6)
    if not args.no_daemon:
        subprocess.run(
            [native, "app-server", "daemon", "stop"],
            cwd=work, env=env, timeout=15, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
    # The standalone updater can outlive "daemon stop". Verify this fixture's
    # exact managed executable and command before stopping only that process.
    f = profile / "app-server-daemon/daemon-updater.pid"
    if f.is_file():
        pid = json.loads(f.read_text())["pid"]
        argv = subprocess.run(
            ["/bin/ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True
        ).stdout.strip()
        if argv.startswith(str(profile / "packages/app-server-daemon/releases") + "/") and argv.endswith(
            "/bin/codex app-server daemon pid-update-loop"
        ):
            os.kill(pid, signal.SIGTERM)
    server.shutdown()
    capture.shutdown()
    fixture.cleanup()
