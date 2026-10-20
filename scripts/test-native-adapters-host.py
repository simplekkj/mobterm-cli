#!/usr/bin/env python3
"""Load CLI-generated integrations in isolated official hosts. No model/API keys.
Usage: script AGENT AGENT_BINARY MOBTERM_CLI EXPECTED_VERSION [PI_PACKAGE_ROOT]
OpenCode runs its actual TUI in a PTY; Pi uses its published discovery/TS loader.
"""
import json, os, pathlib, pty, select, shutil, signal, subprocess, sys, tempfile, time
agent, binary, cli, version = sys.argv[1:5]
binary, cli = str(pathlib.Path(binary).resolve()), str(pathlib.Path(cli).resolve())
with tempfile.TemporaryDirectory(prefix='mobterm-native-host-') as tmp:
    root = pathlib.Path(tmp)
    for name in ['home','cwd','config','data','cache','state','tmp','store','bin','pi']:
        (root/name).mkdir(mode=0o700)
    (root/'bin'/agent).symlink_to(binary)
    env = {k:v for k,v in os.environ.items() if not k.startswith(('OPENCODE_','PI_','OTEL_')) and
           not any(token in k for token in ['API_KEY','TOKEN','SECRET','PASSWORD','CREDENTIAL'])}
    env.update(HOME=str(root/'home'), PATH=str(root/'bin')+os.pathsep+env.get('PATH',''),
        XDG_CONFIG_HOME=str(root/'config'),XDG_DATA_HOME=str(root/'data'),
        XDG_CACHE_HOME=str(root/'cache'),XDG_STATE_HOME=str(root/'state'),TMPDIR=str(root/'tmp'),
        MOBTERM_CONFIG_DIR=str(root/'store'),PI_CODING_AGENT_DIR=str(root/'pi'),
        PI_OFFLINE='1',PI_SKIP_VERSION_CHECK='1',OPENCODE_CONFIG_DIR=str(root/'config'/'opencode'),
        OPENCODE_DISABLE_AUTOUPDATE='1',NO_COLOR='1',TERM='xterm-256color')
    # Exercise the real installer's path detection and generated config, including JSONC.
    if agent == 'opencode':
        config=root/'config'/'opencode';config.mkdir()
        (config/'cli.json').write_text('// existing user setting\n{"plugins":[],}\n')
    receipt=subprocess.run([cli,'hooks','install','--agent',agent,'--json'],env=env,cwd=root/'cwd',capture_output=True,text=True,timeout=20)
    if receipt.returncode or not json.loads(receipt.stdout).get('ok'):
        raise RuntimeError('isolated installation failed: '+receipt.stdout+' '+receipt.stderr)
    if agent == 'pi':
        package=pathlib.Path(sys.argv[5]).resolve()
        check=root/'load.mjs'
        check.write_text('''import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const api=await import(pathToFileURL(process.argv[2]+'/dist/index.js'));
assert.equal(api.VERSION,process.argv[3]);
const result=await api.discoverAndLoadExtensions([],process.cwd(),process.env.PI_CODING_AGENT_DIR);
assert.deepEqual(result.errors,[]);
assert.equal(result.extensions.length,1);
for(const event of ['session_start','session_shutdown','agent_start','agent_settled'])
  assert.equal(result.extensions[0].handlers.get(event).length,1);
assert.equal(result.extensions[0].handlers.has('ui_prompt_start'),false);
console.log('PASS Pi official discovery/import/registration '+api.VERSION);
''')
        subprocess.run(['node',str(check),str(package),version],env=env,cwd=root/'cwd',check=True,timeout=60)
        assets=list((root/'pi'/'extensions').glob('*.ts'))
        assert len(assets)==1,assets
        asset=assets[0];original=root/'actual-extension.ts';asset.rename(original)
        marker=root/'pi-loaded.json'
        asset.write_text('import extension from '+json.dumps(str(original))+''';
import {writeFileSync} from 'node:fs';
import {VERSION} from '@earendil-works/pi-coding-agent';
export default function(pi) {
 let listeners=0;
 extension({...pi,on(...args){listeners++;return pi.on(...args)}});
 pi.on('session_start',(_event,ctx)=>writeFileSync('''+json.dumps(str(marker))+''',JSON.stringify({version:VERSION,mode:ctx.mode,listeners})));
}
''')
        master,slave=pty.openpty()
        proc=subprocess.Popen([binary],env=env,cwd=root/'cwd',stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
        os.close(slave);deadline=time.monotonic()+60;tail=b''
        try:
            while time.monotonic()<deadline and not marker.exists() and proc.poll() is None:
                if select.select([master],[],[],0.25)[0]:
                    try:tail=(tail+os.read(master,65536))[-16384:]
                    except OSError:break
            if not marker.exists():raise RuntimeError('Pi TUI did not load extension: '+repr(tail))
            result=json.loads(marker.read_text())
            assert result=={'version':version,'mode':'tui','listeners':4},result
            print('PASS Pi actual TUI session_start',result)
        finally:
            if proc.poll() is None:os.killpg(proc.pid,signal.SIGTERM)
            try:proc.wait(timeout=5)
            except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait()
            os.close(master)
    else:
        # Instrument only the temporary copy: native setup still runs unchanged and
        # must register all nine listeners. A second TUI launch proves restart loading.
        assets=list((root/'store').rglob('tui.js'))
        assert len(assets)==1, assets
        asset=assets[0];original=asset.with_name('original.js');asset.rename(original)
        marker=root/'loaded.json'
        asset.write_text('''import plugin from './original.js';
import { writeFileSync } from 'node:fs';
export default {...plugin,setup(context){
 let listeners=0;
 const wrapped={...context,data:{...context.data,on(...args){listeners++;return context.data.on(...args)}}};
 const cleanup=plugin.setup(wrapped);
 writeFileSync('''+json.dumps(str(marker))+''',JSON.stringify({version:context.app.version,channel:context.app.channel,listeners}));
 return cleanup;
}};
''')
        for attempt in range(2):
            marker.unlink(missing_ok=True)
            master,slave=pty.openpty()
            proc=subprocess.Popen([binary],env=env,cwd=root/'cwd',stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
            os.close(slave);deadline=time.monotonic()+45;tail=b''
            try:
                while time.monotonic()<deadline and not marker.exists() and proc.poll() is None:
                    if select.select([master],[],[],0.25)[0]:
                        try:tail=(tail+os.read(master,65536))[-16384:]
                        except OSError:break
                if not marker.exists(): raise RuntimeError('official TUI did not load plugin; isolated output: '+repr(tail))
                result=json.loads(marker.read_text())
                assert result['version']==version and result['listeners']==9,result
                print('PASS OpenCode official TUI setup/restart',result)
            finally:
                if proc.poll() is None:os.killpg(proc.pid,signal.SIGTERM)
                try:proc.wait(timeout=5)
                except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait()
                os.close(master)
