// Controlled native-event contract tests. These are not real-host loading evidence.
import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile, chmod, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
// Explicit client runtime: Node's default solid-js export is SSR (no-op effects).
import { createEffect, createRoot, createSignal } from './native-adapter-tests/node_modules/solid-js/dist/solid.js';
const temp = await mkdtemp(join(tmpdir(), 'mobterm-native-contract-'));
const output = join(temp, 'events.jsonl');
const recorder = join(temp, 'notify');
await writeFile(recorder, `#!${process.execPath}\nconst fs=require('node:fs');let b='';process.stdin.on('data',x=>b+=x);process.stdin.on('end',()=>fs.appendFileSync(${JSON.stringify(output)},b+'\\n'));`);
await chmod(recorder, 0o700);
const pause = () => new Promise(resolve => setTimeout(resolve, 2000));
const allEvents = async () => { await pause(); try { return (await readFile(output, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse); } catch { return []; } };
// Preserve the earlier reminder ownership assertions while states use their own channel.
const events = async () => (await allEvents()).filter(v => ["session.execution.succeeded","permission.asked","form.created"].includes(v.event) || v.event === "agent_settled" && v.aborted === false);
const waitFor = async (condition, message, budget = 5000) => {
  const deadline=Date.now()+budget;
  while(!await condition()) {
    assert.ok(Date.now()<deadline,message);
    await new Promise(resolve=>setTimeout(resolve,25));
  }
};
let diagnostics = 0;
globalThis.__mobtermFixture = { Plugin: { define: x => x }, createEffect, createRoot };
async function load(source, version = '1.3.0', executable = recorder) {
  let text = await readFile(new URL(`../adapters/${source === 'pi' ? 'pi/mobterm-jumpto.ts' : 'opencode/tui.js'}`, import.meta.url), 'utf8');
  text = text.replaceAll('__MOBTERM_CLI_JSON__', JSON.stringify(executable)).replaceAll('__MOBTERM_STORE_JSON__', JSON.stringify(temp))
    .replace('import { Plugin } from "@opencode/plugin/tui";', 'const {Plugin} = globalThis.__mobtermFixture;')
    .replace('import { createEffect, createRoot } from "solid-js";', 'const {createEffect, createRoot} = globalThis.__mobtermFixture;')
    .replace('import { VERSION } from "@earendil-works/pi-coding-agent";', `const VERSION = ${JSON.stringify(version)};`);
  return (await import(`data:text/javascript;base64,${Buffer.from(text).toString('base64')}#${Math.random()}`)).default;
}
function host(memory = { instance: 'test-instance', seen: [] }, router = createSignal('root')) {
  const handlers = new Map(), sessions = new Map();
  for (const [id, parentID] of [['root'], ['child', 'root'], ['other'], ['orphan', 'missing']])
    sessions.set(id, { id, parentID, location: { directory: '/fixture' } });
  let reads = 0;
  const [selected, setSelected] = router;
  const pending = new Map();
  const context = {
    app: {version:'2.1.0',channel:'latest'}, storage:{memory:()=>[memory, f=>f(memory)]},
    ui:{router:{current:()=>{reads++;return {type:'session',sessionID:selected()};}},toast:{show:()=>{diagnostics++;}}},
    data:{on:(type,f)=>{let list=handlers.get(type)??[];list.push(f);handlers.set(type,list);return()=>handlers.set(type,list.filter(x=>x!==f));},session:{
      get:id=>sessions.get(id), root:id=>id==='child'?'root':id,
      family:id=>id==='root'?['root','child']:[id],
      permission:{list:id=>pending.get(id)??[]},form:{list:id=>pending.get(id)??[]},
    }},
  };
  return {context,memory,pending,emit:(type,id,data)=>{for(const f of handlers.get(type)??[])f({type,id,data});},
    route:setSelected,router,reads:()=>reads,handlers};
}
try {
  const plugin = await load('opencode');
  // Same live router and TUI memory across activations; the fixture never removes observers.
  const sharedRouter=createSignal('root'), sharedMemory={instance:'reload-tui',seen:[]};
  const retired=[];
  for(let cycle=0;cycle<5;cycle++) {
    const current=host(sharedMemory,sharedRouter), dispose=plugin.setup(current.context);
    assert.equal(current.reads(),1,'real client effect runs at setup');
    current.route('other');
    assert.equal(current.reads(),2,'real signal reruns active observer');
    for(const [old,reads] of retired) assert.equal(old.reads(),reads,'retired activation never reads shared router');
    dispose();dispose();
    assert.equal([...current.handlers.values()].flat().length,0,'all native listeners removed');
    retired.push([current,current.reads()]);
    current.route('root');
    assert.equal(current.reads(),2,'cleanup removes reactive subscription');
  }
  const partial=host(sharedMemory,sharedRouter), originalOn=partial.context.data.on;
  let registrations=0;
  partial.context.data.on=(...args)=>{if(++registrations===4)throw Error('fixture setup failure');return originalOn(...args);};
  assert.doesNotThrow(()=>plugin.setup(partial.context));
  assert.equal(registrations,4,'exercise partial listener registration');
  const partialReads=partial.reads();
  partial.route('other');
  assert.equal(partial.reads(),partialReads,'failed setup disposes route effect');
  assert.equal([...partial.handlers.values()].flat().length,0,'failed setup releases earlier listeners');
  const failedRoute=host();let failedReads=0;
  failedRoute.context.ui.router.current=()=>{failedReads++;sharedRouter[0]();throw Error('fixture route failure');};
  assert.doesNotThrow(()=>plugin.setup(failedRoute.context));
  sharedRouter[1]('root');
  assert.equal(failedReads,1,'failure during initial effect releases its subscription');
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
  cleanup();
  const again=host(h.memory,h.router), cleanAgain=plugin.setup(again.context);
  again.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  assert.equal((await events()).length,3,'memory survives reload');
  const second=host({instance:'second-tui',seen:[]}), cleanSecond=plugin.setup(second.context);
  second.emit('session.execution.succeeded','evt_root',{sessionID:'root'});
  assert.equal((await events()).length,4,'same root another TUI remains independent');
  cleanAgain();cleanSecond();
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
  // Real child processes: intentional cancellation is silent and cannot consume
  // the diagnostic reserved for a later genuine failure in the same activation.
  const childMode=join(temp,'child-mode'), childReady=join(temp,'child-ready');
  const controlled=join(temp,'controlled-notify');
  await writeFile(controlled, `#!${process.execPath}\nconst fs=require('node:fs');process.stdin.resume();process.stdin.on('end',()=>{const mode=fs.readFileSync(${JSON.stringify(childMode)},'utf8');fs.writeFileSync(${JSON.stringify(childReady)},String(process.pid));if(mode==='fail')process.exit(1);setInterval(()=>{},1000);});`);
  await chmod(controlled,0o700);
  const readyPID=async()=>{try{return Number(await readFile(childReady,'utf8'));}catch{return 0;}};
  const exited=pid=>{try{process.kill(pid,0);return false;}catch{return true;}};
  for(const source of ['opencode','pi']) {
    diagnostics=0;
    await writeFile(childMode,'hold');await rm(childReady,{force:true});
    const adapter=await load(source,'1.3.0',controlled);
    let send,cancel,stop;
    if(source==='opencode') {
      const active=host();stop=adapter.setup(active.context);
      send=id=>active.emit('session.execution.succeeded',id,{sessionID:'root'});
      cancel=()=>{active.route('other');active.route('root');};
    } else {
      const callbacks=new Map();adapter({on:(name,f)=>callbacks.set(name,f)});
      const context={mode:'tui',sessionManager:{getSessionId:()=> 'pi-cancel'},ui:{notify:()=>{diagnostics++;}}};
      const emit=(name,event={})=>callbacks.get(name)(event,context);
      emit('session_start');
      send=()=>{emit('agent_start');emit('agent_settled',{aborted:false});};
      cancel=()=>emit('session_start');stop=()=>emit('session_shutdown');
    }
    send('cancel-active');await waitFor(readyPID,source+' child starts');
    const pid=await readyPID();cancel();await waitFor(()=>exited(pid),source+' cancelled child exits');
    await new Promise(resolve=>setTimeout(resolve,100));
    assert.equal(diagnostics,0,source+' active cancellation has no failure diagnostic');
    await writeFile(childMode,'fail');await rm(childReady,{force:true});
    send('real-failure');await waitFor(()=>diagnostics===1,source+' later nonzero exit still warns');
    send('another-failure');await pause();
    assert.equal(diagnostics,1,source+' failures stay bounded');stop();
  }
  diagnostics=0;
  await writeFile(childMode,'hold');await rm(childReady,{force:true});
  const timeoutPlugin=await load('opencode','1.3.0',controlled), timeoutHost=host();
  const stopTimeout=timeoutPlugin.setup(timeoutHost.context);
  timeoutHost.emit('session.execution.succeeded','timeout',{sessionID:'root'});
  await waitFor(readyPID,'timeout child starts');const timeoutPID=await readyPID();
  await waitFor(()=>diagnostics===1,'real ten-second timeout warns',12000);
  await waitFor(()=>exited(timeoutPID),'timed out child exits');
  assert.equal(diagnostics,1,'timeout and subsequent close warn once');stopTimeout();
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
  assert.equal(handlers.has('ui_prompt_start'),true,'Pi exposes the native observation interval');
  for(const value of await events()) {
    assert.equal(value.adapter_revision,2);
    assert.equal(typeof value.run_id,'string');
    for(const key of Object.keys(value)) assert.ok(['aborted','adapter_revision','runtime_version','event','session_id','run_id','event_id','instance_id','hook_event_name','tool_name','tool_use_id','tool_input'].includes(key));
  }
  ctx.mode='tui';session='state-session';emit('session_start');emit('agent_start');
  emit('tool_execution_start',{toolCallId:'tool-one',toolName:'bash',args:{secret:'not uploaded'}});
  emit('tool_execution_start',{toolCallId:'tool-two',toolName:'read',args:{secret:'not uploaded'}});
  emit('tool_execution_end',{toolCallId:'tool-one',toolName:'bash',isError:true,result:{secret:'not uploaded'}});
  emit('ui_prompt_start',{reason:'ui_prompt',kind:'confirm',title:'not uploaded'});
  emit('ui_prompt_end',{reason:'ui_prompt',kind:'confirm'});
  emit('agent_settled',{aborted:true});
  const states=(await allEvents()).filter(e=>e.session_id==='state-session');
  assert.ok(states.some(e=>e.event==='agent_start'));
  assert.equal(states.filter(e=>e.event==='tool_execution_start').length,2);
  assert.ok(states.some(e=>e.tool_status==='failed'));
  assert.ok(states.some(e=>e.event==='ui_prompt_start'));
  assert.ok(states.some(e=>e.event==='agent_settled'&&e.aborted===true));
  assert.equal(JSON.stringify(states).includes('not uploaded'),false);
  emit('session_shutdown');
  // Exact OpenCode native approval handoff, distinct from the observation queue.
  const decider=join(temp,'decider');
  await writeFile(decider, `#!${process.execPath}\nlet b='';process.stdin.on('data',x=>b+=x);process.stdin.on('end',()=>{const p=JSON.parse(b);if(p.tool_input?.action==='bash')process.stdout.write(JSON.stringify({expires_at:Math.floor(Date.now()/1000)+30,hookSpecificOutput:{decision:{behavior:'allow'}}}));});`);
  await chmod(decider,0o700);
  const approvalPlugin=await load('opencode','1.3.0',decider);
  const approving=host();let nativeReplies=[];
  const request={id:'request-approval',sessionID:'root',action:'bash',resources:['echo hello'],metadata:{command:'echo hello'}};
  approving.pending.set('root',[request]);
  approving.context.client={permission:{get:async()=>({...request}),reply:async value=>nativeReplies.push(value)}};
  const disposeApproval=approvalPlugin.setup(approving.context);
  approving.emit('permission.asked','approval-event',request);
  await waitFor(()=>nativeReplies.length===1,'native permission reply not delivered');
  assert.deepEqual(nativeReplies[0],{sessionID:'root',requestID:request.id,reply:'once'});
  approving.emit('permission.asked','approval-event',request);
  await pause();assert.equal(nativeReplies.length,1,'same occurrence not replayed');
  approving.context.client.permission.get=async()=>({...request,resources:['changed command']});
  approving.emit('permission.asked','changed-approval-event',request);
  await pause();assert.equal(nativeReplies.length,1,'changed native request must not be approved');
  disposeApproval();
  console.log('PASS native adapter contracts: ownership, pending, lifecycle, reload, identity, Pi settled/mode, bridge metadata');
} finally { delete globalThis.__mobtermFixture; await rm(temp,{recursive:true,force:true}); }
