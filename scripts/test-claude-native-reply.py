#!/usr/bin/env python3
"""Real Claude Mod/classic Hook rendezvous and prompt submission, local synthetic provider.
Usage: python3 scripts/test-claude-native-reply.py /path/to/claude /path/to/mobterm-cli
The helper supplies a single already-authorized test decision. The original TUI,
Mod event dispatcher and Claude prompt API are real. No account or paid model.
"""
import fcntl
import http.server
import json
import os
import pathlib
import pty
import select
import shlex
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

binary, cli = [pathlib.Path(p).resolve() for p in sys.argv[1:]]
seed = "mobterm fixture first turn"
reply = "Claude fixture reply 中文 🧪\n{{literal}}"
native_users = []


class Provider(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        self.send_response(200)
        if "count_tokens" in self.path:
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"input_tokens":10}')
            return
        users = []
        for msg in request.get("messages", []):
            if msg["role"] != "user":
                continue
            parts = msg["content"]
            users.extend([parts] if isinstance(parts, str) else [p.get("text", "") for p in parts])
        native_users.append([u for u in users if u in (seed, reply)])
        message = {"id": "msg_fixture_"+str(len(native_users)), "model": request.get("model", "fixture"),
                   "type": "message", "role": "assistant", "content": [],
                   "usage": {"input_tokens": 10, "output_tokens": 1}, "stop_reason": None}
        if not request.get("stream"):
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({**message, "content": [{"type": "text", "text": "Synthetic response"}],
                                         "stop_reason": "end_turn"}).encode())
            return
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for event, data in [
            ("message_start", {"message": message}),
            ("content_block_start", {"index": 0, "content_block": {"type":"text", "text":""}}),
            ("content_block_delta", {"index":0, "delta":{"type":"text_delta","text":"Synthetic response"}}),
            ("content_block_stop", {"index":0}),
            ("message_delta", {"delta":{"stop_reason":"end_turn","stop_sequence":None},"usage":{"output_tokens":3}}),
            ("message_stop", {})]:
            self.wfile.write(("event: "+event+"\ndata: "+json.dumps({"type":event,**data})+"\n\n").encode())


with tempfile.TemporaryDirectory(prefix="mobterm-claude-reply-") as tmp:
    root=pathlib.Path(tmp).resolve()
    home, config, store, bindir = [root/name for name in ("home","config","store","bin")]
    for path in [home,config,store,bindir]:
        path.mkdir(mode=0o700)
    (bindir/"claude").symlink_to(binary)
    server=http.server.ThreadingHTTPServer(("127.0.0.1",0),Provider)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    env={"HOME":str(home),"PATH":str(bindir)+os.pathsep+os.environ["PATH"],"TERM":"xterm-256color",
         "CLAUDE_CONFIG_DIR":str(config),"MOBTERM_CONFIG_DIR":str(store),"DISABLE_AUTOUPDATER":"1",
         "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":"1","ANTHROPIC_API_KEY":"offline-fixture",
         "ANTHROPIC_BASE_URL":f"http://127.0.0.1:{server.server_port}","NO_PROXY":"127.0.0.1"}
    subprocess.run([cli,"hooks","install","--agent","claude","--json"],cwd=root,env=env,check=True,capture_output=True)
    plugin=store/"native/claude-reply"
    subprocess.run([binary,"plugin","validate",plugin],cwd=root,env=env,check=True,capture_output=True)
    (config/".claude.json").write_text(json.dumps({"hasCompletedOnboarding":True,"theme":"dark",
        "projects":{str(root):{"hasTrustDialogAccepted":True}},"customApiKeyResponses":{"approved":["offline-fixture"]}}))
    helper=root/"decision-fixture"
    helper.write_text(f"""#!{sys.executable}
import json,os,pathlib,sys
root=pathlib.Path({str(root)!r});args=sys.argv[1:];data=sys.stdin.buffer.read()
owner={{'parent':os.getppid(),'host':os.getenv('CLAUDE_PID'),'session':os.getenv('CLAUDE_CODE_SESSION_ID')}}
if args[0]=='classic':
    event=json.loads(data);owner['prompt']=event.get('prompt_id');owner['event']=event['hook_event_name']
    with (root/'classic').open('a') as f:print(json.dumps(owner),file=f)
elif args[0]=='startup':
    (root/'startup').write_text(json.dumps(owner))
elif args[:2]==['reply','wait']:
    source=json.loads(data);owner['prompt']=source['event_id'];owner['runtime']=source['runtime_version']
    assert source['event_id']==source['run_id'] and source['session_id']==owner['session']
    try:
        with (root/'wait').open('x') as f:print(json.dumps(owner),file=f)
    except FileExistsError:sys.exit(0)
    print(json.dumps({{'v':1,'source_id':'source','owner_token':'token','decision_id':'decision','text':{reply!r}}}))
elif args[:2]==['reply','authorize']:
    assert int.from_bytes(data[:4],'big')==len(data)-4
    print('{{"allowed":true}}')
elif args[:2]==['reply','receipt']:
    assert int.from_bytes(data[:4],'big')==len(data)-4
    (root/'receipt').write_text(json.loads(data[4:])['state']);print('{{"ok":true}}')
""")
    helper.chmod(0o700)
    settings=json.loads((config/"settings.json").read_text())
    for event in ["SessionStart","UserPromptSubmit","Stop"]:
        settings["hooks"].setdefault(event,[]).append({"hooks":[{"type":"command","command":shlex.quote(str(helper))+" classic"}]})
    (config/"settings.json").write_text(json.dumps(settings))
    asset=plugin/"hooks/register.js"
    js=asset.read_text().replace("const executable = "+json.dumps(str(cli))+";","const executable = "+json.dumps(str(helper))+";")
    assert js!=asset.read_text()
    # Observe activation on this temporary installed copy, without replacing any original handler.
    js=js.replace('on("session.start", ($, e, next) => {','on("session.start", async ($, e, next) => {')
    js=js.replace('instance = crypto.randomUUID();\n    return next(e);','''instance = crypto.randomUUID();
    const env = await $.process.hookEnv();
    if(interactive) await $.process.run([executable,"startup"],{env});
    return next(e);''',1)
    asset.write_text(js)
    master,slave=pty.openpty()
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack("HHHH",40,120,0,0))
    proc=subprocess.Popen([binary,"--plugin-dir",plugin,"--model","sonnet"],cwd=root,env=env,
        stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
    os.close(slave)
    try:
        deadline=time.monotonic()+120;startup=None;submitted=False;screen=b"";key_at=time.monotonic()+4
        while time.monotonic()<deadline and proc.poll() is None:
            if select.select([master],[],[],.2)[0]:
                try:screen=(screen+os.read(master,65536))[-16000:]
                except OSError:break
            if (root/"startup").exists():
                if startup is None:startup=time.monotonic()
                if not submitted and time.monotonic()>startup+3:
                    os.write(master,seed.encode());time.sleep(.3);os.write(master,b"\r");submitted=True
            elif time.monotonic()>key_at:
                key_at=time.monotonic()+4
                if b"trust" in screen.lower() and b"folder" in screen.lower():
                    os.write(master,b"\x1b[B");time.sleep(.2)
                os.write(master,b"\r")
            if (root/"receipt").exists() and any(reply in history for history in native_users):break
        assert (root/"startup").exists(),"Claude Mod did not load in isolated TUI"
        classic=[json.loads(row) for row in (root/"classic").read_text().splitlines()]
        assert (root/"wait").exists(),f"Claude produced no reply opportunity; classic events: {[r['event'] for r in classic]}; provider calls: {len(native_users)}"
        source=json.loads((root/"wait").read_text())
        assert source["host"]==str(proc.pid) and source["parent"]==proc.pid
        assert any(r["event"]=="UserPromptSubmit" and r["prompt"]==source["prompt"] and r["session"]==source["session"]
                   and r["host"]==source["host"] for r in classic)
        assert (root/"receipt").read_text()=="native_accepted"
        assert any(seed in history and reply in history for history in native_users),"original provider history missing correlated user input"
        print("PASS Claude",source["runtime"],"official TUI Mod/classic prompt and owner rendezvous; original native session started a second turn with exact reply; loopback synthetic provider and decision fixture.")
    finally:
        if proc.poll() is None:os.killpg(proc.pid,signal.SIGTERM)
        try:proc.wait(timeout=5)
        except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait()
        os.close(master);server.shutdown();server.server_close()
