#!/usr/bin/env python3
"""Exercise the installed adapter in a real Pi or OpenCode V2 TUI using an offline synthetic model.
Usage: python3 scripts/test-plugin-native-reply.py pi|opencode /path/to/agent /path/to/mobterm-cli
The helper supplies one synthetic already-authorized decision. Separate wire
tests cover SSH, grant validation and CAS. No credentials or paid model needed.
"""
import fcntl
import http.server
import json
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

agent = sys.argv[1]
assert agent in ("pi", "opencode")
binary, cli = [pathlib.Path(p).resolve() for p in sys.argv[2:]]
text = "  回复 🧪\n{{keep}} /literal  "
seed = "mobterm seed"
next_prompt = "mobterm input during first turn"
calls = []
release_first_response = threading.Event()


class Provider(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        users = [msg["content"] for msg in request["messages"] if msg["role"] == "user"]
        users = [msg if isinstance(msg, str) else "".join(part.get("text", "") for part in msg) for msg in users]
        calls.append(users)
        if agent == "pi" and len(calls) == 1:
            if not release_first_response.wait(timeout=45):
                self.send_error(408)
                return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for delta, finish in [({"role": "assistant", "content": "fixture completion"}, None), ({}, "stop")]:
            event = {"id": "fixture", "model": "fixture", "created": 1, "object": "chat.completion.chunk",
                     "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
            self.wfile.write(("data: "+json.dumps(event)+"\n\n").encode())
        self.wfile.write(b"data: [DONE]\n\n")


with tempfile.TemporaryDirectory(prefix="mobterm-"+agent+"-reply-") as tmp:
    root = pathlib.Path(tmp)
    home, store, config, bindir = [root / name for name in ["home", "store", "agent", "bin"]]
    for path in [home, store, config, bindir]:
        path.mkdir(mode=0o700)
    (bindir / agent).symlink_to(binary)
    env = {"HOME": str(home), "PATH": str(bindir)+os.pathsep+os.environ["PATH"],
           "TERM": "xterm-256color", "MOBTERM_CONFIG_DIR": str(store),
           "PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "PI_CODING_AGENT_DIR": str(config),
           "NO_PROXY": "localhost,127.0.0.1", "XDG_CONFIG_HOME": str(root / "config"),
           "XDG_DATA_HOME": str(root/"data"), "XDG_STATE_HOME": str(root/"state"),
           "XDG_CACHE_HOME":str(root/"cache"), "OPENCODE_CONFIG_DIR":str(config),
           "OPENCODE_DISABLE_AUTOUPDATE":"1", "MOBTERM_FIXTURE_KEY":"local-placeholder"}
    subprocess.run([cli, "hooks", "install", "--agent", agent, "--json"], cwd=root, env=env, check=True, capture_output=True)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    endpoint = f"http://127.0.0.1:{server.server_port}/v1"
    if agent=="pi":
        (config / "models.json").write_text(json.dumps({"providers":{"fixture":{
            "baseUrl":endpoint,"api":"openai-completions","apiKey":"fixture-local","models":[{"id":"fixture"}]}}}))
        adapter, = (config/"extensions").glob("*.ts")
        argv = [binary,"--provider","fixture","--model","fixture",seed]
    else:
        (config/"opencode.json").write_text(json.dumps({"model":"fixture/fixture","update":"disable",
            "providers":{"fixture":{"package":"@opencode/ai/providers/openai-compatible","env":["MOBTERM_FIXTURE_KEY"],
                "settings":{"baseURL":endpoint},"models":{"fixture":{"name":"Fixture","capabilities":{"tools": True},
                "limit":{"context":32000,"output":1024}}}}}}))
        adapter=store/"native/opencode/tui.js"
        argv=[binary,"--standalone","--prompt",seed]
    helper = root / "decision-fixture"
    helper.write_text(f"""#!{sys.executable}
import sys,json,pathlib,os
root=pathlib.Path({str(root)!r})
args=sys.argv[1:]
if {agent!r}=="pi" and args[0]=="notify" and os.getenv("MOBTERM_NATIVE_COMPLETION")=="1":
    print("mobterm/message-body/1",flush=True)
raw=sys.stdin.buffer.read()
if args[:2]==["reply","wait"]:
    source=json.loads(raw)
    session=source["session_id"]
    if {agent!r}=="pi":
        with (root/"waits").open("a") as out: print(source["run_id"],file=out)
    try:
        with (root/"first-source").open("x") as out: out.write(session)
    except FileExistsError: sys.exit(0)
    print(json.dumps({{"v":1,"source_id":"source","decision_id":"decision","owner_token":"owner","text":{text!r}}}))
elif args[0]=="notify" and {agent!r}=="pi":
    event=json.loads(raw)
    if event["event"]=="agent_settled":
        with (root/"settled").open("a") as out: print(event["run_id"],file=out)
elif args[:2]==["reply","authorize"]:
    assert int.from_bytes(raw[:4],"big")==len(raw)-4
    print('{{"v":1,"allowed":true}}')
elif args[:2]==["reply","receipt"]:
    assert int.from_bytes(raw[:4],"big")==len(raw)-4
    with (root/"receipt").open("a") as f:print(json.loads(raw[4:])["state"],file=f)
    print('{{"v":1,"ok":true}}')
""")
    helper.chmod(0o700)
    rendered = adapter.read_text()
    assert rendered.count("const executable = "+json.dumps(str(cli))+";") == 1
    adapter.write_text(rendered.replace("const executable = "+json.dumps(str(cli))+";",
                                        "const executable = "+json.dumps(str(helper))+";"))
    if agent == "pi":
        witness = config / "extensions" / "witness.ts"
        witness.write_text("""import {appendFileSync} from 'node:fs';
export default function(pi) {
  let releaseInput;
  pi.on("input", async e => {
    if(e.text===NEXT_INPUT) {
      await new Promise(resolve => {
        releaseInput=resolve;
        appendFileSync(ENTERED,"input");
      });
    }
  });
  pi.on("agent_settled", () => {
    if(releaseInput) { setImmediate(releaseInput); releaseInput=undefined; }
  });
  pi.on("message_start", (e,ctx) => {
    if(e.message?.role==="user") appendFileSync(FILENAME,JSON.stringify({
        session:ctx.sessionManager.getSessionId(),mode:ctx.mode,
        matches:e.message.content.some(p=>p.type==="text" && p.text===EXPECTED)})+"\\n");
  });
}""".replace("FILENAME", json.dumps(str(root / "witness"))).replace("EXPECTED",json.dumps(text))
            .replace("NEXT_INPUT",json.dumps(next_prompt)).replace("ENTERED",json.dumps(str(root/"entered"))))
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
    process = subprocess.Popen(argv, cwd=root, env=env,
                               stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    try:
        deadline = time.monotonic()+90
        entered_next = False
        while time.monotonic() < deadline and process.poll() is None:
            if agent == "pi" and calls and not entered_next:
                os.write(master,next_prompt.encode())
                time.sleep(.2)
                os.write(master,b"\r")
                entered_next = True
            if (root/"entered").exists():
                release_first_response.set()
            states=(root/"receipt").read_text().splitlines() if (root/"receipt").exists() else []
            notifications_ready = agent!="pi" or ((root/"settled").exists() and len((root/"settled").read_text().splitlines())>=2)
            if notifications_ready and ("delivery_unknown" if agent=="pi" else "native_consumed") in states and any(text in history for history in calls):
                break
            if select.select([master], [], [], 0.2)[0]:
                try: os.read(master, 65536)
                except OSError: break
        assert (root/"first-source").exists(), f"{agent} never registered source; provider saw {len(calls)} calls"
        states=(root/"receipt").read_text().splitlines() if (root/"receipt").exists() else []
        assert any(seed in history and text in history for history in calls), f"original provider history mismatch; receipt states {states}"
        if agent=="pi":
            assert states==["delivery_unknown"]
            assert any(history==[seed,next_prompt,text] for history in calls)
            settled=(root/"settled").read_text().splitlines()
            waits=(root/"waits").read_text().splitlines()
            assert len(settled)>=2 and settled[0] not in waits and waits[0]==settled[1]
            messages=[json.loads(line) for line in (root/"witness").read_text().splitlines()]
            assert len(messages)==3 and all(row["mode"]=="tui" and row["session"]==(root/"first-source").read_text() for row in messages)
            assert [row["matches"] for row in messages]==[False,False,True]
        else:
            assert "native_consumed" in states and "native_accepted" in states
        print(f"PASS original {agent} TUI consumed unmodified ordinary reply; same session included both user messages in loopback synthetic provider input; receipt states: {states}")
    finally:
        release_first_response.set()
        if process.poll() is None: os.killpg(process.pid, signal.SIGTERM)
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired: os.killpg(process.pid, signal.SIGKILL); process.wait()
        os.close(master)
        server.shutdown()
        server.server_close()
