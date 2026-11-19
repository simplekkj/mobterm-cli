// Exercise the shipped modules against controlled in-process native host APIs.
// Actual official host loading is checked separately by test-native-adapters-host.py.
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {EventEmitter} from "node:events";
import {PassThrough, Writable} from "node:stream";

const asset = name => readFile(new URL("../adapters/" + name, import.meta.url), "utf8");
const minimum = (await asset("shared/minimum.js")).replace("__MOBTERM_MINIMUMS_JSON__", await asset("minimums.json"));
const reply = await asset("shared/reply.js");
const turn = () => new Promise(resolve => setImmediate(resolve));
const answer = {v:1, source_id:"source-original", decision_id:"decision-original", owner_token:"owner-private",
  text:"  中文\n🧪 \" \\ $(echo x) /cmd {{template}}\n"};
function cli() {
  const calls = [], receipts = [], waiters = [];
  let beforeAuthorize = () => {};
  function spawn(_exe,args) {
    const child = new EventEmitter();
    child.stdout = new PassThrough();
    child.kill = () => { child.emit("close", 1); return true; };
    const finish = value => { if (value !== undefined) child.stdout.write(JSON.stringify(value));child.emit("close",0); };
    child.stdin = new Writable({write(bytes,_enc,done) {
      const framed = args.includes("--stdin-frame");
      if (framed) {assert.equal(bytes.readUInt32BE(),bytes.length - 4);bytes=bytes.subarray(4);}
      const input = JSON.parse(bytes);
      calls.push({args,input});
      if (args[0] !== "reply") queueMicrotask(() => finish());
      else if (args[1] === "wait") waiters.push({input,answer:(decision=answer)=>finish(decision)});
      else if (args[1] === "authorize") queueMicrotask(() => {beforeAuthorize();finish({v:1,allowed:true});});
      else if (args[1] === "receipt") {receipts.push(input);queueMicrotask(() => finish({ok:true}));}
      done();
    }});
    return child;
  }
  return {spawn,calls,receipts,waiters,setBeforeAuthorize:f=>{beforeAuthorize=f;}};
}

async function adapter(file, dependencies) {
  globalThis.__replyFixture = dependencies;
  let code = (await asset(file)).replace("__MOBTERM_MINIMUM_JS__",()=>minimum).replace("__MOBTERM_REPLY_JS__",()=>reply)
    .replaceAll("__MOBTERM_CLI_JSON__", JSON.stringify("/fixture/mobterm-cli")).replaceAll("__MOBTERM_STORE_JSON__",JSON.stringify("/fixture/store"))
    .replace('import { spawn } from "node:child_process";', 'const {spawn}=globalThis.__replyFixture;')
    .replace('import { VERSION } from "@earendil-works/pi-coding-agent";', 'const {VERSION}=globalThis.__replyFixture;')
    .replace('import { Plugin } from "@opencode/plugin/tui";','const Plugin={define:value=>value};')
    .replace('import { createEffect, createRoot } from "solid-js";','const createEffect=f=>f(), createRoot=f=>f(()=>{});');
  return import("data:text/javascript;base64," + Buffer.from(code).toString("base64") + "#" + Math.random());
}

export async function testNativeReply() {
  for (const change of ["unknown-pending","question-before","compact-before","send","limit","busy","switch","tree","question","late-authorize"]) {
    const fixture = cli(), handlers=new Map(), submitted=[];
    const originalText=answer.text;
    if (change==="limit") answer.text="\u0001".repeat(16*1024);
    const plugin = await adapter("pi/mobterm-jumpto.ts",{spawn:fixture.spawn,VERSION:"1.5.3-rc.1"});
    const state={mode:"tui",sessionManager:{getSessionId:()=>"S"}, isIdle:()=>true,hasPendingMessages:()=>false};
    plugin.default({on:(name,f)=>handlers.set(name,f),sendUserMessage:(...args)=>submitted.push(args)});
    const emit=(name,data={})=>handlers.get(name)(data,state);
    emit("session_start");emit("agent_start");
    if(change==="question-before")emit("ui_prompt_start",{kind:"custom",reason:"ui_prompt"});
    if(change==="compact-before")emit("session_before_compact");
    if(change==="unknown-pending")state.hasPendingMessages=()=>undefined;
    emit("agent_settled",{aborted:false});
    if(["question-before","compact-before","unknown-pending"].includes(change)){
      await turn();
      assert.ok(fixture.calls.some(call=>call.input.event==="agent_settled"),"existing settled notification survives "+change);
      assert.equal(fixture.waiters.length,0,"no reply during "+change);
      emit("session_shutdown");continue;
    }
    assert.equal(fixture.waiters.length,1);
    emit("agent_settled",{aborted:false});assert.equal(fixture.waiters.length,1);
    if(change==="busy") state.hasPendingMessages=()=>true;
    if(change==="switch")emit("session_before_switch");
    if(change==="tree")emit("session_before_tree");
    if(change==="question")emit("ui_prompt_start",{kind:"custom",reason:"ui_prompt"});
    if(change==="late-authorize") fixture.setBeforeAuthorize(()=>emit("input"));
    fixture.waiters[0].answer();await turn();
    assert.deepEqual(submitted,["send","limit"].includes(change)?[[answer.text,{expandPromptTemplates:false}]]:[]);
    if (["send","limit"].includes(change)) assert.equal(fixture.receipts[0].state,"delivery_unknown");
    emit("session_shutdown"); answer.text=originalText;
  }

  for (const change of ["invalid-active","late-execution","global-before","permission-before","send","permission","new-input","new-execution","dispose","late-route"]) {
    const fixture=cli(),handlers=new Map(),submissions=[];
    const plugin=await adapter("opencode/tui.js",{spawn:fixture.spawn});
    let route="S",pending=[],seq=0;
    const location={directory:"/project",workspaceID:"ws"};
    const session={id:"S",location,time:{}};
    const api={get:async()=>session,active:async()=>change==="invalid-active"?[]:{},inbox:{list:async()=>[]},form:{list:async()=>[]},
      prompt:async r=>{submissions.push(r);return {id:r.id,sessionID:r.sessionID,type:"user",payload:{text:r.text},delivery:r.delivery};}};
    const memory={instance:"old",seen:[]};
    const dispose=plugin.default.setup({app:{version:"3.0.0-beta.1+build",channel:"latest"},storage:{memory:()=>[memory,f=>f(memory)]},
      ui:{router:{current:()=>({type:"session",sessionID:route})},toast:{show:()=>{}}},
      data:{on:(name,f)=>{handlers.set(name,f);return ()=>handlers.delete(name);},session:{get:()=>session,root:id=>id,family:id=>[id],
        permission:{list:()=>pending},form:{list:id=>id==="global" && change==="global-before"?[{id:"G"}]:[]}}},
      client:{session:api,permission:{list:async()=>pending}}});
    const emit=(type,data={},sequence)=>handlers.get(type)?.({type,id:"event-"+(++seq),durable:{aggregateID:"S",version:1,seq:sequence??seq},location,data:{sessionID:"S",...data}});
    if(change==="late-execution"){
      emit("session.execution.started",{},10);
      emit("session.execution.started",{},3);
      emit("session.execution.failed",{},4);
      emit("session.execution.succeeded",{},5);
      assert.equal(fixture.waiters.length,0,"earlier execution cannot gain a new waiter");
      emit("session.execution.succeeded",{},11);
      assert.equal(fixture.waiters.length,1,"older events cannot close the newer execution");
      emit("session.execution.started",{},8);
      fixture.waiters[0].answer();await turn();
      assert.equal(submissions.length,1,"replayed start cannot cancel the newer source");
      dispose();continue;
    }
    emit("session.execution.started");
    if(change==="permission-before")pending=[{id:"P",sessionID:"S"}];
    emit("session.execution.succeeded");
    if(change==="global-before" || change==="permission-before"){
      assert.equal(fixture.waiters.length,0,"no reply during "+change);
      dispose();continue;
    }
    assert.equal(fixture.waiters.length,1);
    if(change==="permission")pending=[{id:"P",sessionID:"S"}];
    if(change==="new-input")emit("session.inbox.enqueued",{inboxID:"I"});
    if(change==="new-execution")emit("session.execution.started");
    if(change==="dispose")dispose();
    if(change==="late-route")fixture.setBeforeAuthorize(()=>{route="other";});
    fixture.waiters[0].answer();await turn();
    assert.equal(submissions.length,change==="send"?1:0,change);
    if(change==="send"){
      assert.deepEqual(submissions[0],{id:"msg_mobterm_source-original",sessionID:"S",text:answer.text,delivery:"queue"});
      assert.equal(fixture.receipts[0].state,"native_accepted");
      emit("session.inbox.delivered",{inboxID:"wrong"});assert.equal(fixture.receipts.length,1);
      emit("session.inbox.delivered",{inboxID:submissions[0].id});await turn();
      assert.deepEqual(fixture.receipts.map(r=>r.state),["native_accepted","native_consumed"]);
      emit("session.execution.started");emit("session.execution.succeeded");
      fixture.waiters[1].answer({...answer,source_id:"another-source",decision_id:"decisionoriginal"});
      await turn();
      assert.equal(submissions[1].text,submissions[0].text);
      assert.notEqual(submissions[1].id,submissions[0].id,"independent sources never share a native ID");
    }
    dispose();
  }

  for (const change of ["send","drop","clear","stop","new-prompt","child","reload","missing-native-fields"]) {
    const hooks=new Map(), receipts=[],sent=[],waiters=[];
    const mod=await adapter("claude/register.js",{});
    mod.register((name,f)=>hooks.set(name,f));
    let session="S",prompt="P";
    const $={clock:{now:async()=>Date.now()},session:{id:async()=>session,info:async()=>({promptId:prompt}),
      version:async()=>({version:"2.1.328"}),surfaces:async()=>["terminal"]},
      process:{hookEnv:async()=>({CLAUDE_CODE_SESSION_ID:session}),spawn:req=>{
        let done;const result=new Promise(r=>{done=r;});
        let output;const data=new Promise(r=>{output=r;});
        const stream=(async function*(){const val=await data;yield {stream:"stdout",text:JSON.stringify(val)};done({code:0});})();
        stream.result=result;waiters.push({request:JSON.parse(req.input),answer:()=>output(answer)});return stream;
      },run:async (cmd,init)=>{
        const buf=Buffer.from(init.stdin);assert.equal(buf.readUInt32BE(), buf.length-4);
        if(cmd[2]==="receipt")receipts.push(JSON.parse(buf.subarray(4)));
        return {exitCode:0,stdout:'{"allowed":true}'};
      }},prompt:{submit:async arg=>{sent.push(arg);return change==="drop"?{drop:"stopped"}:{text:arg.text};}}};
    const emit=(name,data={})=>hooks.get(name)($,data,x=>x);
    await emit("session.start",{surface:"terminal",isInteractive:true});
    if(change==="missing-native-fields")await emit("turn.complete",{reason:"answer"});
    await emit("turn.start",{turnId:"T"});
    if(change==="missing-native-fields"){
      await emit("turn.complete",{turnId:"T",reason:"answer"});await turn();
      assert.equal(waiters.length,0);continue;
    }
    await emit("turn.complete",{turnId:"T",reason:"answer",isAborted:false,...(change==="child"?{agentId:"child"}:{})});await turn();
    if(change==="child"){assert.equal(waiters.length,0);continue;}
    assert.equal(waiters.length,1);assert.equal(waiters[0].request.event_id,"P");
    if(change==="clear"){await emit("session.end");session="new";}
    if(change==="stop")await emit("session.stop");
    if(change==="new-prompt"){await emit("prompt.submit");prompt="new";}
    if(change==="reload")await emit("session.start",{surface:"terminal",isInteractive:true});
    waiters[0].answer();await turn();await turn();
    assert.deepEqual(sent,["send","drop"].includes(change)?[{text:answer.text,asUser:true}]:[],change);
    if(change==="send"||change==="drop")assert.equal(receipts[0].state,change==="drop"?"native_rejected":"native_accepted");
  }
  console.log("PASS source-bound native reply contract (Pi, OpenCode V2, Claude Mod)");
}
