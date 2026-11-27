"""Run-owned real Codex processes, loopback model and disposable multiplexer."""
import base64
import json
import os
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
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from darwin_codex import assess_native, digest, identity


def prepare_runner():
    os.umask(0o077)
    signal.signal(signal.SIGINT, signal.default_int_handler)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))


def start_model(stack, queue_lifecycle):
    wait_model = threading.Event()
    release = threading.Event()
    counter = 0
    issued_requests = set()

    class Mock(BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def do_GET(self):
            self.send_response(404)
            self.end_headers()

        def do_POST(self):
            nonlocal counter
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            counter += 1
            i = counter
            # This injected barrier is solely a local model callback. No production user input.
            held_turn = "mobile-probe-race-terminal" in json.dumps(body) and not wait_model.is_set()
            if held_turn:
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
            if queue_lifecycle:
                for marker, name, params in (
                    (
                        "probe-approval",
                        "exec_command",
                        {
                            "cmd": "true",
                            "sandbox_permissions": "require_escalated",
                            "justification": "Synthetic approval?",
                        },
                    ),
                    (
                        "probe-question",
                        "request_user_input",
                        {
                            "questions": [
                                {
                                    "id": "choice",
                                    "header": "Fixture",
                                    "question": "Choose one.",
                                    "options": [
                                        {"label": "First", "description": "First choice"},
                                        {"label": "Second", "description": "Second choice"},
                                    ],
                                }
                            ]
                        },
                    ),
                ):
                    if marker in json.dumps(body["input"]) and marker not in issued_requests:
                        issued_requests.add(marker)
                        events[1]["item"] = {
                            "type": "function_call",
                            "id": "item_" + str(i),
                            "call_id": "call_" + str(i),
                            "name": name,
                            "arguments": json.dumps(params),
                        }
            data = "".join("event: " + e["type"] + "\ndata: " + json.dumps(e) + "\n\n" for e in events).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            try:
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError):
                if not (queue_lifecycle and held_turn and release.is_set()):
                    raise

    server = ThreadingHTTPServer(("127.0.0.1", 0), Mock)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    stack.callback(server.server_close)
    stack.callback(server.shutdown)
    return server.server_port, wait_model, release


def until(fn, timeout=55):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        val = fn()
        if val:
            return val
        time.sleep(0.12)
    raise TimeoutError(str(fn))


def log(**kw):
    print(json.dumps(kw, ensure_ascii=False), flush=True)


def ids(d):
    return d["thread-id"], d["turn-id"]


class Client:
    def __init__(self, profile):
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


class CodexFixture:
    def __init__(self, stack, kind, cli, native, herdr=None, *, queue_lifecycle=False):
        self.kind = kind
        self.cli = cli.resolve(strict=True)
        self.native = native.resolve(strict=True)
        self.herdr = herdr.resolve(strict=True) if herdr else None
        self.mux = None
        self.stop_daemon = False
        base = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="mt-codex-", dir="/tmp"))).resolve()
        root = self.root = base / kind
        root.mkdir()
        profile = self.profile = root / "profile"
        profile.mkdir()
        self.work = root / "work"
        self.work.mkdir()
        self.store = root / "store"
        self.store.mkdir()
        native = self.native
        initial = self.initial = identity(native)
        assess_native(native)
        assert identity(native) == initial
        expected = "0cc2c99ee61041ecde0c848f85861ea9cb6e45c5c136fd66383a7eb2198e307c"
        if (
            digest(native) != expected
            or json.loads((native.parent.parent / "codex-package.json").read_text())["version"] != "0.171.0"
        ):
            sys.exit("Expected the pinned signed Codex 0.171.0 standalone Darwin x86_64 binary.")
        env = self.env = dict(
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
        self.model_port, self.wait_model, self.release = start_model(stack, queue_lifecycle)
        stack.callback(self.close)

    def write_pair(self, installation, pair, sender, origin):
        sk = base64.b64encode(os.urandom(32)).decode()
        (self.store / "installation.json").write_text(json.dumps(dict(v=1, installation_id=installation)))
        (self.store / "pairs.json").write_text(
            json.dumps(
                {
                    pair: dict(
                        v=1,
                        pair_id=pair,
                        installation_id=installation,
                        status="active",
                        send_token=sender,
                        encryption_key=sk,
                        api_url=origin,
                    )
                }
            )
        )
        return sk

    def configure_notify(self):
        root, cli, profile, work = self.root, self.cli, self.profile, self.work
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
base_url = "http://127.0.0.1:{self.model_port}/v1"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
[projects."{work}"]
trust_level = "trusted"
''')

    def cmd(self, *args, check=True):
        r = subprocess.run(args, env=self.env, cwd=self.work, capture_output=True, text=True, timeout=8)
        if check and r.returncode:
            raise RuntimeError(str(args[1:]) + ": " + r.stderr[-500:])
        return r.stdout.strip()

    def tm(self, *a, **kw):
        return self.cmd("tmux", "-f", "/dev/null", "-S", str(self.root / "tmux.sock"), *a, **kw)

    def hd(self, *a, **kw):
        return self.cmd(self.herdr, "--session", "proof", *a, **kw)

    def snap(self):
        sock = self.root / "config/herdr/sessions/proof/herdr.sock"
        with socket.socket(socket.AF_UNIX) as c:
            c.settimeout(3)
            c.connect(str(sock))
            c.sendall(b'{"id":"probe","method":"session.snapshot","params":{}}\n')
            return json.loads(c.makefile("rb").readline())["result"]["snapshot"]

    def visible(self, p):
        if self.kind == "tmux":
            return self.tm("capture-pane", "-p", "-t", p)
        return self.hd("pane", "read", "--source", "visible", "--raw", p)

    def key(self, p, k):
        if self.kind == "tmux":
            self.tm("send-keys", "-t", p, k)
        else:
            self.hd("pane", "send-keys", p, k)

    def enter(self, p, s):
        if self.kind == "tmux":
            self.tm("send-keys", "-t", p, "-l", s)
        else:
            self.hd("pane", "send-text", p, s)
        time.sleep(0.18)
        self.key(p, "Enter")

    def seen(self, token):
        ds = [json.loads(f.read_text()) for f in self.root.glob("*.seen")]
        return next((d for d in ds if token in (d["input-messages"] or [])), None)

    def notice(self, token):
        return until(lambda: self.seen(token))

    def ready(self, p):
        until(lambda: "gpt-5.1" in self.visible(p), 70)
        time.sleep(0.3)

    def first_pane(self, *, no_daemon=False):
        assert identity(self.native) == self.initial
        self.stop_daemon = not no_daemon
        launch = "codex --no-daemon" if no_daemon else "codex"
        if self.kind == "tmux":
            log(version=self.tm("-V"))
            self.tm("new-session", "-d", "-s", "probe", "-x", "150", "-y", "55", "-c", str(self.work), launch)
            a = self.tm("display-message", "-p", "-t", "probe", "#{pane_id}")
            self.ready(a)
        else:
            self.mux = subprocess.Popen(
                [self.herdr, "--session", "proof", "server"],
                cwd=self.work,
                env=self.env,
                stdin=subprocess.DEVNULL,
                stdout=(self.root / "herdr.log").open("w"),
                stderr=subprocess.STDOUT,
            )
            until(lambda: (self.root / "config/herdr/sessions/proof/herdr.sock").is_socket())
            self.hd("workspace", "create", "--cwd", str(self.work), "--label", "fixture")
            s = self.snap()
            log(version=s["version"], protocol=s["protocol"])
            a = s["panes"][-1]["pane_id"]
            self.enter(a, "export PATH=" + shlex.quote(self.env["PATH"]))
            self.enter(a, launch)
            self.ready(a)
        self.enter(a, "mobile-probe-first")
        first = self.notice("mobile-probe-first")
        return a, first

    def second_pane(self, a, first):
        if self.kind == "tmux":
            b = self.tm("split-window", "-d", "-P", "-F", "#{pane_id}", "-t", a, "-c", str(self.work), "codex")
        else:
            self.hd("pane", "split", "--pane", a, "--direction", "right", "--cwd", str(self.work))
            b = next(p["pane_id"] for p in self.snap()["panes"] if p["pane_id"] != a)
            self.enter(b, "export PATH=" + shlex.quote(self.env["PATH"]))
            self.enter(b, "codex")
        self.ready(b)
        self.enter(b, "mobile-probe-second")
        second = self.notice("mobile-probe-second")
        field = "tmux" if self.kind == "tmux" else "herdr"
        log(
            panes=[a, b],
            notify_env=[first[field], second[field]],
            shared_daemon=first["ppid"] == second["ppid"],
            threads_distinct=ids(first)[0] != ids(second)[0],
        )
        return b, second

    def close(self):
        self.release.set()
        if self.kind == "tmux":
            self.tm("kill-server", check=False)
        elif self.mux:
            self.hd("server", "stop", check=False)
            self.mux.wait(timeout=6)
        if self.stop_daemon:
            subprocess.run(
                [self.native, "app-server", "daemon", "stop"],
                cwd=self.work, env=self.env, timeout=15, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
        # The standalone updater can outlive "daemon stop". Verify this fixture's
        # exact managed executable and command before stopping only that process.
        f = self.profile / "app-server-daemon/daemon-updater.pid"
        if f.is_file():
            pid = json.loads(f.read_text())["pid"]
            argv = subprocess.run(
                ["/bin/ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True
            ).stdout.strip()
            if argv.startswith(str(self.profile / "packages/app-server-daemon/releases") + "/") and argv.endswith(
                "/bin/codex app-server daemon pid-update-loop"
            ):
                os.kill(pid, signal.SIGTERM)
