// Controlled native-event contract tests. These are not real-host loading evidence.
import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const temp = await mkdtemp(join(tmpdir(), 'mobterm-native-contract-'));
const output = join(temp, 'events.jsonl');
const recorder = join(temp, 'notify');
await writeFile(recorder, `#!${process.execPath}\nconst fs=require('node:fs');let b='';process.stdin.on('data',x=>b+=x);process.stdin.on('end',()=>fs.appendFileSync(${JSON.stringify(output)},b+'\\n'));`);
await chmod(recorder, 0o700);
const pause = () => new Promise(resolve => setTimeout(resolve, 2000));
const events = async () => { await pause(); try { return (await readFile(output, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse); } catch { return []; } };
let effects = [];
let diagnostics = 0;
globalThis.__mobtermFixture = { Plugin: { define: x => x }, createEffect: f => { effects.push(f); f(); } };
async function load(source, version = '1.3.0', executable = recorder) {
  let text = await readFile(new URL(`../adapters/${source === 'pi' ? 'pi/mobterm-jumpto.ts' : 'opencode/tui.js'}`, import.meta.url), 'utf8');
  text = text.replaceAll('__MOBTERM_CLI_JSON__', JSON.stringify(executable)).replaceAll('__MOBTERM_STORE_JSON__', JSON.stringify(temp))
    .replace('import { Plugin } from "@opencode/plugin/tui";', 'const {Plugin} = globalThis.__mobtermFixture;')
    .replace('import { createEffect } from "solid-js";', 'const {createEffect} = globalThis.__mobtermFixture;')
    .replace('import { VERSION } from "@earendil-works/pi-coding-agent";', `const VERSION = ${JSON.stringify(version)};`);
  return (await import(`data:text/javascript;base64,${Buffer.from(text).toString('base64')}#${Math.random()}`)).default;
}
function host(memory = { instance: 'test-instance', seen: [] }) {
  const handlers = new Map(), sessions = new Map();
  for (const [id, parentID] of [['root'], ['child', 'root'], ['other'], ['orphan', 'missing']])
    sessions.set(id, { id, parentID, location: { directory: '/fixture' } });
  let selected = 'root';
  const pending = new Map();
  const context = {
    app: {version:'2.1.0',channel:'latest'}, storage:{memory:()=>[memory, f=>f(memory)]},
    ui:{router:{current:()=>({type:'session',sessionID:selected})},toast:{show:()=>{diagnostics++;}}},
    data:{on:(type,f)=>{let list=handlers.get(type)??[];list.push(f);handlers.set(type,list);return()=>handlers.set(type,list.filter(x=>x!==f));},session:{
      get:id=>sessions.get(id), root:id=>id==='child'?'root':id,
      family:id=>id==='root'?['root','child']:[id],
      permission:{list:id=>pending.get(id)??[]},form:{list:id=>pending.get(id)??[]},
    }},
  };
  return {context,memory,pending,emit:(type,id,data)=>{for(const f of handlers.get(type)??[])f({type,id,data});},
    route:id=>{selected=id;for(const f of effects)f();},handlers};
}
try {
  const plugin = await load('opencode');
  const h=host(), cleanup=plugin.setup(h.context);
  h.emit('session.execution.started','evt_start',{sessionID:'root'});
  h.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  h.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  assert.equal((await events()).length,1,'root success once');
  for(const sessionID of ['child','other','global','unknown','orphan']) h.emit('session.execution.succeeded','evt_'+sessionID,{sessionID});
  h.emit('session.execution.failed','evt_failed',{sessionID:'root'});
  h.emit('session.execution.succeeded','evt_after_failure',{sessionID:'root'});
  assert.equal((await events()).length,1,'unknown/child/other/failed excluded');
  h.emit('session.execution.started','evt_again',{sessionID:'root'});
  h.pending.set('child',[{id:'req_child',sessionID:'child'}]);
  h.emit('permission.asked','evt_permission',{id:'req_child',sessionID:'child'});
  assert.equal((await events()).length,2,'child permission belongs visible root');
  h.emit('permission.asked','evt_absent',{id:'absent',sessionID:'root'});
  h.pending.set('root',[{id:'form_1',sessionID:'root'}]);
  h.emit('form.created','evt_form',{form:{id:'form_1',sessionID:'root'}});
  h.emit('form.replied','evt_reply',{id:'form_1',sessionID:'root'});
  assert.equal((await events()).length,2,'answered before queue starts excluded');
  h.pending.set('root',[{id:'form_2',sessionID:'root'}]);
  h.emit('form.created','evt_form_2',{form:{id:'form_2',sessionID:'root'}});
  assert.equal((await events()).length,3,'pending form delivered');
  h.emit('session.execution.succeeded','evt_late',{sessionID:'root'});
  h.route('other');h.route('root');
  assert.equal((await events()).length,3,'switch away and back invalidates queued work');
  cleanup();effects=[];
  const again=host(h.memory), cleanAgain=plugin.setup(again.context);
  again.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  assert.equal((await events()).length,3,'memory survives reload');
  const second=host({instance:'second-tui',seen:[]}), cleanSecond=plugin.setup(second.context);
  second.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  assert.equal((await events()).length,4,'same root another TUI remains independent');
  cleanAgain();cleanSecond();effects=[];
  const unsupported=host();unsupported.context.app.version='1.99.0';plugin.setup(unsupported.context);
  assert.equal(unsupported.handlers.size,0);
  unsupported.context.app.version='2.1.0';unsupported.context.app.channel='dev';plugin.setup(unsupported.context);
  assert.equal(unsupported.handlers.size,0);
  unsupported.context.app.channel='latest';unsupported.context.app.version='2.1.0-beta';plugin.setup(unsupported.context);
  assert.equal(unsupported.handlers.size,0,'prerelease runtime rejected');
  const malformed=host(),cleanMalformed=plugin.setup(malformed.context);
  malformed.emit('session.execution.succeeded','x'.repeat(8192),{sessionID:'root'});
  malformed.emit('unknown.native.event','evt_unknown',{sessionID:'root'});
  cleanMalformed();
  assert.equal((await events()).length,4,'oversize identity and unknown event ignored');
  const missingPlugin=await load('opencode','1.3.0',join(temp,'missing-cli'));
  const missing=host(),cleanMissing=missingPlugin.setup(missing.context);
  missing.emit('session.execution.succeeded','evt_missing_one',{sessionID:'root'});
  missing.emit('session.execution.succeeded','evt_missing_two',{sessionID:'root'});
  assert.equal((await events()).length,4,'missing CLI never interrupts handlers or emits data');
  assert.equal(diagnostics,1,'spawn failures report one generic diagnostic');
  cleanMissing();
  diagnostics=0;
  const overflow=host({instance:'overflow',seen:[]}), cleanOverflow=plugin.setup(overflow.context);
  for(let index=0;index<40;index++) overflow.emit('session.execution.succeeded','evt_overflow_'+index,{sessionID:'root'});
  cleanOverflow();
  assert.equal(diagnostics,1,'queue overflow emits one generic diagnostic');
  assert.equal((await events()).length,4,'cleanup drops queued work');
  const extension=await load('pi');const handlers=new Map();extension({on:(name,f)=>handlers.set(name,f)});
  let session='pi-session';const ctx={mode:'tui',sessionManager:{getSessionId:()=>session},ui:{notify:()=>{}}};
  const emit=(name,event={})=>handlers.get(name)?.(event,ctx);
  emit('session_start');emit('agent_settled',{aborted:false});
  emit('agent_start');emit('agent_settled',{aborted:true});emit('agent_settled');
  assert.equal((await events()).length,4,'Pi reload half-round and aborted/missing ignored');
  emit('agent_start');emit('agent_start');emit('agent_settled',{aborted:false});emit('agent_settled',{aborted:false});
  assert.equal((await events()).length,5,'Pi final settled once');
  emit('agent_start');session='different';emit('agent_settled',{aborted:false});
  emit('session_start');emit('agent_start');emit('agent_settled',{aborted:false});emit('session_shutdown');
  assert.equal((await events()).length,5,'session switch/shutdown invalidate');
  for(const mode of ['rpc','print','json']) {ctx.mode=mode;emit('session_start');emit('agent_start');emit('agent_settled',{aborted:false});}
  assert.equal((await events()).length,5,'non TUI excluded');
  assert.equal(handlers.has('ui_prompt_start'),false,'no Pi needs_input approximation');
  for(const value of await events()) assert.deepEqual(Object.keys(value).sort(),(value.event==='agent_settled'?['aborted']:[]).concat(['adapter_revision','runtime_version','event','session_id','event_id','instance_id']).sort());
  console.log('PASS native adapter contracts: ownership, pending, lifecycle, reload, identity, Pi settled/mode, bridge metadata');
} finally { delete globalThis.__mobtermFixture; await rm(temp,{recursive:true,force:true}); }
