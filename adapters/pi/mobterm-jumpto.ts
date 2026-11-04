// @ts-nocheck -- shipped as standalone host extension; contract tested by endpoint fixtures.
// Installed by mobterm-cli. No event content is persisted or printed.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { lstatSync, readFileSync } from "node:fs";
import { join } from "node:path";
const executable = __MOBTERM_CLI_JSON__;
const store = __MOBTERM_STORE_JSON__;
const validID = (value) => typeof value === "string" && value.length > 0 && Buffer.byteLength(value) <= 1024 && !/[\u0000-\u001f\u007f]/.test(value);
const stableVersion = (value, minimum) => {
  if (typeof value !== "string") return false;
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\+[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)?$/.exec(value);
  if (!match) return false;
  const parts = match.slice(1, 4).map(Number);
  return parts.every(Number.isSafeInteger) && (parts[0] > minimum[0] ||
    (parts[0] === minimum[0] && (parts[1] > minimum[1] || (parts[1] === minimum[1] && parts[2] >= minimum[2]))));
};

// One child and at most eight queued jobs; callbacks never await remote HTTP.
function bridge(source, warn) {
  let stopped = false, active, warned = false;
  const queue = [];
  const diagnostic = () => { if (!warned && !stopped) { warned = true; try { warn(); } catch {} } };
  const drain = () => {
    if (stopped || active || !queue.length) return;
    const job = queue.shift();
    try {
      if (!job.valid()) { queueMicrotask(drain); return; }
      const body = JSON.stringify(job.payload);
      if (Buffer.byteLength(body) > 4096) { diagnostic(); queueMicrotask(drain); return; }
      const child = spawn(executable, ["notify", "--source", source], {
        env: { ...process.env, MOBTERM_CONFIG_DIR: store },
        stdio: ["pipe", "ignore", "ignore"], shell: false,
      });
      const running = { child, cancelled: false };
      active = running;
      const timer = running.timer = setTimeout(() => { diagnostic(); child.kill("SIGKILL"); }, 10000);
      let finished = false;
      const finish = (failed) => {
        if (finished) return;
        finished = true; clearTimeout(timer);
        if (failed && !running.cancelled) diagnostic();
        if (active === running) active = undefined;
        queueMicrotask(drain);
      };
      child.once("error", () => finish(true));
      child.once("close", (code) => finish(code !== 0));
      child.stdin.on("error", () => { if (!running.cancelled) diagnostic(); child.kill("SIGKILL"); });
      child.stdin.end(body);
    } catch { active = undefined; diagnostic(); queueMicrotask(drain); }
  };
  return {
    enqueue(payload, valid) {
      if (stopped) return;
      const critical = ["session.execution.succeeded","session.execution.failed","session.execution.interrupted","permission.asked","form.created","agent_settled","ui_prompt_start"].includes(payload.event);
      if (queue.length >= 8) {
        const replace=queue.findIndex(job=>!job.critical);
        if(critical&&replace>=0)queue.splice(replace,1);else{diagnostic();return;}
      }
      const job={payload,valid,critical};
      // Keep chronological events in a run; reserve capacity by dropping only
      // intermediate observations, never preempt a pending business reminder.
      queue.push(job); queueMicrotask(drain);
    },
    cancel() { queue.length = 0; if (active) { active.cancelled = true; clearTimeout(active.timer); active.child.kill("SIGKILL"); } },
    stop() { stopped = true; this.cancel(); },
  };
}
import { VERSION } from "@earendil-works/pi-coding-agent";

export default function mobtermJumpTo(pi) {
  let alive = true, epoch = 0, round, context;
  const instance = randomUUID();
  const pendingGates = new Map();
  let confirmQueue=Promise.resolve();
  const settleGates=()=>{for(const gate of pendingGates.values())gate.cancel();pendingGates.clear();};
  const receipt=(result,state)=>new Promise(resolve=>{
    if(!result?.owner_token||!result?.request_id){resolve();return;}
    try{const body=Buffer.from(JSON.stringify({v:1,request_id:result.request_id,owner_token:result.owner_token,state})),header=Buffer.alloc(4);header.writeUInt32BE(body.length);
      const child=spawn(executable,["interaction","receipt","--stdin-frame"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","ignore","ignore"],shell:false});
      const timer=setTimeout(()=>{child.kill("SIGKILL");resolve();},3000);
      child.once("close",()=>{clearTimeout(timer);resolve();});child.once("error",()=>{clearTimeout(timer);resolve();});child.stdin.on("error",()=>child.kill("SIGKILL"));child.stdin.end(Buffer.concat([header,body]));
    }catch{resolve();}
  });
    const authorize = result => new Promise(resolve=>{
      if(!result?.owner_token||!result?.request_id){resolve(false);return;}
      let child,done=false,timer;const finish=value=>{if(done)return;done=true;clearTimeout(timer);child?.kill("SIGKILL");resolve(value);};
      try {const body=Buffer.from(JSON.stringify({v:1,request_id:result.request_id,owner_token:result.owner_token})),header=Buffer.alloc(4);header.writeUInt32BE(body.length);
        child=spawn(executable,["interaction","authorize","--stdin-frame"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","pipe","ignore"],shell:false});let output="";
        timer=setTimeout(()=>finish(false),5000);child.stdout.on("data",bytes=>{output+=bytes;if(Buffer.byteLength(output)>4096)finish(false);});
        child.once("error",()=>finish(false));child.stdin.on("error",()=>finish(false));child.once("close",code=>{try{finish(code===0&&JSON.parse(output).allowed===true);}catch{finish(false);}});child.stdin.end(Buffer.concat([header,body]));
      }catch{finish(false);}
    });
  const transport = bridge("pi", () => {
    if (context?.mode === "tui") context.ui.notify("MobTerm notification unavailable. Check integration.", "warning");
  });
  const session = (ctx) => ctx.sessionManager.getSessionId();
  const reset = (ctx) => { context = ctx; epoch++; round = undefined; transport.cancel(); settleGates(); };
  pi.on("session_start", (_event, ctx) => { alive = true; reset(ctx); });
  pi.on("session_shutdown", (_event, ctx) => { alive = false; reset(ctx); });
  pi.on("agent_start", (_event, ctx) => {
    context = ctx;
    round = undefined;
    const id = session(ctx);
    if (!alive || ctx.mode !== "tui" || !stableVersion(VERSION, [1, 1, 0]) || !validID(id)) return;
    round = { session: id, id: randomUUID(), epoch, sent: false };
    send("agent_start", ctx);
  });
  const send = (name, ctx, extra = {}) => {
    const current = round;
    if (!alive || ctx.mode !== "tui" || !current || current.epoch !== epoch || session(ctx) !== current.session) return;
    transport.enqueue({ adapter_revision: 2, runtime_version: VERSION, event: name,
      session_id: current.session, run_id: current.id, event_id: randomUUID(), instance_id: instance, ...extra },
      () => alive && ctx.mode === "tui" && epoch === current.epoch && session(ctx) === current.session);
  };
  const gateEnabled=()=>{
    try {
      const path=join(store,"interaction-policy-v1.json"),info=lstatSync(path);
      if(!info.isFile()||info.isSymbolicLink()||(info.mode&0o077)!==0||info.uid!==process.getuid?.()||info.size>65536)return true;
      const policy=JSON.parse(readFileSync(path,"utf8"));
      return Object.values(policy).some(agents=>Number.isSafeInteger(agents?.pi?.approval)&&agents.pi.approval>0);
    }catch(error){return error?.code!=="ENOENT";}
  };
  pi.on("tool_call", async (event,ctx) => {
    if(!alive||ctx.mode!=="tui"||!["1.4.0","1.5.0"].includes(VERSION)||!gateEnabled())return;
    if(!["bash","read","write","edit","grep","find","ls"].includes(event.toolName))return;
    const captured={session:session(ctx),epoch,signal:ctx.signal,deadline:Date.now()+120000,round:round?.id??randomUUID(),toolCallId:event.toolCallId};
    const valid=()=>alive&&epoch===captured.epoch&&session(ctx)===captured.session&&!captured.signal?.aborted&&Date.now()<captured.deadline&&gateEnabled();
    if(!validID(captured.session)||!validID(captured.toolCallId))return {block:true,reason:"MobTerm cannot bind this tool request"};
    // Capture before the first await: the host exposes signal through a getter.
    let snapshot;
    try{snapshot=JSON.parse(JSON.stringify(event.input));}catch{return {block:true,reason:"MobTerm cannot inspect this tool input"};}
    const key=randomUUID();
    const result=await new Promise(resolve=>{
      let child,settled=false,timer;
      const finish=value=>{if(settled)return;settled=true;clearTimeout(timer);captured.signal?.removeEventListener("abort",cancel);pendingGates.delete(key);child?.kill("SIGKILL");resolve(value);};
      const cancel=()=>finish({action:"deny"});
      pendingGates.set(key,{cancel});
      if(pendingGates.size>8||!valid()){cancel();return;}
      captured.signal?.addEventListener("abort",cancel,{once:true});
      timer=setTimeout(cancel,Math.max(1,captured.deadline-Date.now()));
      const body=JSON.stringify({adapter_revision:2,runtime_version:VERSION,event:"tool_gate",session_id:captured.session,run_id:captured.round,event_id:key,instance_id:instance,request_id:captured.toolCallId,tool_use_id:captured.toolCallId,tool_name:event.toolName,tool_input:snapshot,cwd:ctx.cwd});
      if(Buffer.byteLength(body)>24576){cancel();return;}
      try{
        child=spawn(executable,["interaction","wait","--source","pi"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","pipe","ignore"],shell:false});
        let output="";child.stdout.on("data",b=>{output+=b;if(Buffer.byteLength(output)>65536)cancel();});child.once("error",cancel);child.stdin.on("error",cancel);
        child.once("close",code=>{if(code!==0){cancel();return;}try{finish(JSON.parse(output));}catch{cancel();}});child.stdin.end(body);
      }catch{cancel();}
    });
    if(result?.gate_enabled===false)return {block:true,reason:"MobTerm approval was disabled while this request was pending"};
    if(result?.action==="release") {
      const confirm=async()=>{
        if(!valid())return false;
        const own=new AbortController();const abort=()=>own.abort();pendingGates.set(key,{cancel:abort});captured.signal?.addEventListener("abort",abort,{once:true});
        const timer=setTimeout(abort,Math.max(1,captured.deadline-Date.now()));
        try {
          // This is this extension's own confirmation, never a third-party UI resolver.
          const cancelled=new Promise(resolve=>own.signal.addEventListener("abort",()=>resolve(false),{once:true}));
          return await Promise.race([ctx.ui.confirm("MobTerm tool approval",event.toolName+"\n"+JSON.stringify(snapshot),{signal:own.signal,timeout:Math.max(1,captured.deadline-Date.now())}),cancelled])===true&&valid();
        }catch{return false;}finally{clearTimeout(timer);pendingGates.delete(key);captured.signal?.removeEventListener("abort",abort);}
      };
      const allowed=confirmQueue.then(confirm,confirm);confirmQueue=allowed.then(()=>{},()=>{});
      return await allowed?undefined:{block:true,reason:"MobTerm tool approval was not granted"};
    }
    if(!valid()||result?.action!=="allow_once"){await receipt(result,"response_written");return {block:true,reason:"MobTerm tool approval denied, expired or unavailable"};}
    if(!await authorize(result)||!valid()){await receipt(result,"native_cancelled");return {block:true,reason:"MobTerm authorization expired or was revoked"};}
    await receipt(result,"response_written");
    if(!valid())return {block:true,reason:"MobTerm tool request expired"};
    // undefined deliberately permits the next extension gate to decide.
    return undefined;
  });
  pi.on("tool_execution_start", (event, ctx) => {
    if (validID(event.toolCallId) && typeof event.toolName === "string")
      send("tool_execution_start", ctx, {tool_id:event.toolCallId,tool_name:event.toolName});
  });
  pi.on("tool_execution_end", (event, ctx) => {
    if (validID(event.toolCallId) && typeof event.toolName === "string" && typeof event.isError === "boolean")
      send("tool_execution_end", ctx, {tool_id:event.toolCallId,tool_name:event.toolName,tool_status:event.isError?"failed":"succeeded"});
  });
  for (const name of ["ui_prompt_start", "ui_prompt_end"]) pi.on(name, (event,ctx) => {
    if (event.reason === "ui_prompt" && ["select","confirm","input","editor","custom"].includes(event.kind)) send(name,ctx);
  });
  pi.on("agent_settled", (event, ctx) => {
    context = ctx;
    const current = round;
    if (typeof event.aborted !== "boolean") return;
    if (!alive || ctx.mode !== "tui" || !current || current.sent ||
        current.epoch !== epoch || session(ctx) !== current.session) return;
    current.sent = true;
    transport.enqueue({ adapter_revision: 2, runtime_version: VERSION, event: "agent_settled",
      session_id: current.session, run_id: current.id, event_id: current.id, instance_id: instance, aborted: event.aborted },
      () => alive && ctx.mode === "tui" && epoch === current.epoch && session(ctx) === current.session);
  });
}
