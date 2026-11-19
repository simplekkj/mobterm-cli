// Installed by mobterm-cli. No event content is persisted or printed.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
const executable = __MOBTERM_CLI_JSON__;
const store = __MOBTERM_STORE_JSON__;
const validID = (value) => typeof value === "string" && value.length > 0 && Buffer.byteLength(value) <= 1024 && !/[\u0000-\u001f\u007f]/.test(value);
__MOBTERM_MINIMUM_JS__
__MOBTERM_REPLY_JS__

// Bounded prose only. The CLI applies the same final wire limits before storage.
const messageSummary = (content) => {
  if (!Array.isArray(content)) return;
  for (const part of content.slice(0, 128)) {
    if (part?.type !== "text" || typeof part.text !== "string") continue;
    const clean = part.text.slice(0, 16384)
      .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\|$)/g, "")
      .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
      .replace(/\r\n?/g, "\n")
      .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g, "");
    if (/^(?:[\[{]|<(?:think|thinking|analysis|reasoning)(?:\s|>))/i.test(clean.trimStart())) continue;
    let fence, paragraph = [];
    for (const line of clean.split("\n")) {
      const trimmed = line.trim();
      const marker = /^(\x60{3,}|~{3,})/.exec(trimmed)?.[0]?.[0];
      if (marker) { if (paragraph.length) break; fence = fence === marker ? undefined : fence ?? marker; continue; }
      if (fence) continue;
      if (!trimmed) { if (paragraph.length) break; continue; }
      if (/^(?:[\[{]|!\[)/.test(trimmed) || /^(?: {4}|\t)/.test(line)) { if (paragraph.length) break; continue; }
      paragraph.push(trimmed);
    }
    const text = paragraph.join(" ").replace(/\s+/gu, " ").trim();
    if (!text) continue;
    let result = "", scalars = 0, bytes = 0;
    for (const {segment} of new Intl.Segmenter(undefined, {granularity:"grapheme"}).segment(text)) {
      const count = [...segment].length, size = Buffer.byteLength(segment);
      if (scalars + count > 120 || bytes + size > 512) break;
      result += segment; scalars += count; bytes += size;
    }
    return result.trim() || undefined;
  }
};

// This optional extension belongs to the current completed assistant message.
// Reject oversized input before joining/encoding it. Preserve text-part order.
const completionBody = (parts) => {
  try {
    if (!Array.isArray(parts)) return;
    const chunks = [];
    let size = 0;
    for (const part of parts) {
      if (part?.type !== "text") continue;
      if (typeof part.text !== "string" || !part.text.isWellFormed()) return;
      if (!part.text.length) continue;
      if (/^<(?:think|thinking|analysis|reasoning)(?:\s|>)/i.test(part.text.trimStart())) return;
      size += Buffer.byteLength(part.text);
      if (size > 512 * 1024) return { completion_body_too_large: true };
      chunks.push(part.text);
    }
    if (size) return { completion_body_base64: Buffer.from(chunks.join("")).toString("base64") };
  } catch { /* The base event remains deliverable without the extension. */ }
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
      let body = JSON.stringify(job.payload);
      if (Buffer.byteLength(body) > 4096 && job.payload.summary) {
        const {summary, ...required} = job.payload; body = JSON.stringify(required);
      }
      if (Buffer.byteLength(body) > 4096) { diagnostic(); queueMicrotask(drain); return; }
      const child = spawn(executable, ["notify", "--source", source], {
        env: { ...process.env, MOBTERM_CONFIG_DIR: store, MOBTERM_NATIVE_COMPLETION: job.extension ? "1" : "0" },
        stdio: ["pipe", job.extension ? "pipe" : "ignore", "ignore"], shell: false,
      });
      const running = { child, cancelled: false };
      active = running;
      const timer = running.timer = setTimeout(() => { diagnostic(); child.kill("SIGKILL"); }, 10000);
      let finished = false, handshake;
      const finish = (failed) => {
        if (finished) return;
        finished = true; clearTimeout(timer); clearTimeout(handshake);
        if (failed && !running.cancelled) diagnostic();
        if (active === running) active = undefined;
        queueMicrotask(drain);
      };
      child.once("error", () => finish(true));
      child.once("close", (code) => finish(code !== 0));
      child.stdin.on("error", () => { if (!running.cancelled) diagnostic(); child.kill("SIGKILL"); });
      if (!job.extension) {
        child.stdin.end(body);
      } else {
        // Negotiate with this exact child. A binary replaced between events
        // cannot receive fields its strict parser rejects. Write stdin once.
        let sent = false, reply = "";
        const send = (extended) => {
          if (sent || finished || running.cancelled) return;
          sent = true;
          clearTimeout(handshake);
          if (!job.valid()) { running.cancelled = true; child.kill("SIGKILL"); return; }
          if (extended) {
            try {
              const candidate = JSON.stringify({...JSON.parse(body), ...job.extension});
              if (Buffer.byteLength(candidate) <= 1024 * 1024) body = candidate;
            } catch { /* Keep the bounded old-shape event. */ }
          }
          child.stdin.end(body);
        };
        handshake = setTimeout(() => send(false), 500);
        child.stdout.on("data", chunk => {
          if (sent || chunk.length + reply.length > 64) { send(false); return; }
          reply += chunk.toString("ascii");
          if (reply.includes("\n")) send(reply === "mobterm/message-body/1\n");
        });
      }
    } catch { active = undefined; diagnostic(); queueMicrotask(drain); }
  };
  return {
    enqueue(payload, valid, extension) {
      if (stopped) return;
      const critical = ["session.execution.succeeded","session.execution.failed","session.execution.interrupted","permission.asked","form.created","agent_settled","ui_prompt_start","permission.replied","form.replied","form.cancelled"].includes(payload.event);
      // A resolved/invalidated pending request must not occupy the bounded
      // queue ahead of the observation that clears its waiting state.
      for (let i=queue.length-1;i>=0;i--) { if (!queue[i].valid()) queue.splice(i,1); }
      if (queue.length >= 8) {
        const replace=queue.findIndex(job=>!job.critical);
        if(critical&&replace>=0)queue.splice(replace,1);else{diagnostic();return;}
      }
      const job={payload,valid,critical,extension};
      // Keep chronological events in a run; reserve capacity by dropping only
      // intermediate observations, never preempt a pending business reminder.
      queue.push(job); queueMicrotask(drain);
    },
    cancel() { queue.length = 0; if (active) { active.cancelled = true; clearTimeout(active.timer); active.child.kill("SIGKILL"); } },
    stop() { stopped = true; this.cancel(); },
  };
}
import { Plugin } from "@opencode/plugin/tui";
import { createEffect, createRoot } from "solid-js";

export default Plugin.define({
  id: "mobterm.jumpto",
  setup(context) {
    const version = context.app.version;
    if (!atLeast(version, minimums.notification.opencode)) return;
    const [memory, update] = context.storage.memory("mobterm.jumpto.v1", {
      initial: { instance: randomUUID(), seen: [] },
    });
    let alive = true, routeEpoch = 0, previousRoute, previousLocation;
    const activation = randomUUID();
    const lifecycles = new Map();
    let stopReply, replySession;
    const inputReceipts = new Map();
    const invalidateReply = sessionID => {
      if (sessionID !== undefined && replySession !== sessionID && root(sessionID) !== replySession) return;
      stopReply?.(); stopReply = undefined; replySession = undefined;
    };
    const tools = new Map();
    const approvalChildren = new Map();
    const resolved = new Set();
    const questionReceipts = new Map();
    const pendingObservations = new Map();
    const transport = bridge("opencode", () => context.ui.toast.show({
      title: "MobTerm", message: "Notification unavailable. Check MobTerm integration.", variant: "warning",
    }));
    const route = () => {
      const current = context.ui.router.current();
      return current?.type === "session" && validID(current.sessionID) ? current.sessionID : undefined;
    };
    const root = (sessionID) => {
      const visited = new Set();
      let id = sessionID;
      while (validID(id) && visited.size < 64 && !visited.has(id)) {
        visited.add(id);
        const session = context.data.session.get(id);
        if (!session || session.id !== id) return;
        if (session.parentID === undefined) {
          if (context.data.session.root(sessionID) !== id) return;
          if (!context.data.session.family(id).includes(sessionID)) return;
          return id;
        }
        id = session.parentID;
      }
    };
    const locationKey = (location) => location ? JSON.stringify([location.directory, location.workspaceID ?? null]) : "";
    const remember = (id) => update((draft) => { draft.seen.push(id); if (draft.seen.length > 512) draft.seen.shift(); });
    const forgetRequests = (sessionID) => {
      for (const [key, observation] of pendingObservations) {
        if (observation.payload.session_id === sessionID || root(observation.payload.session_id) === sessionID) {
          approvalChildren.get(key)?.kill("SIGTERM"); approvalChildren.delete(key);
          pendingObservations.delete(key);
        }
      }
    };
    const executionSequence = (event) => {
      const durable = event.durable;
      if (durable?.aggregateID === event.data?.sessionID && durable?.version === 1 &&
          Number.isSafeInteger(durable.seq) && durable.seq >= 0) return durable.seq;
    };
    const staleExecution = (event) => {
      const id = event.data?.sessionID;
      if (!validID(id) || !validID(event.id)) return true;
      const location = context.data.session.get(id)?.location;
      if (event.location && location && locationKey(event.location) !== locationKey(location)) return true;
      const seen = lifecycles.get(id)?.sequence, seq = executionSequence(event);
      return seen !== undefined && seq !== undefined && seq <= seen;
    };
    const lifecycle = (sessionID, terminal, event) => {
      if (!validID(sessionID)) return;
      invalidateReply(sessionID);
      forgetRequests(sessionID);
      const previous = lifecycles.get(sessionID);
      lifecycles.delete(sessionID);
      lifecycles.set(sessionID, { start: terminal ? undefined : event.durable, sequence: executionSequence(event) ?? previous?.sequence, generation: (previous?.generation ?? 0) + 1, terminal, run: terminal ? previous?.run ?? randomUUID() : randomUUID() });
      if (lifecycles.size > 512) lifecycles.delete(lifecycles.keys().next().value);
    };
    const clearRequest = (event, id) => {
      if (!validID(id) || !validID(event.data.sessionID)) return;
      const key = JSON.stringify([event.data.sessionID,id]);
      if (resolved.has(key)) return;
      resolved.add(key);
      approvalChildren.get(key)?.kill("SIGTERM");
      approvalChildren.delete(key);
      const observed = pendingObservations.get(key);
      pendingObservations.delete(key);
      if (observed && validID(event.id) && observed.valid() &&
          (!event.location || locationKey(event.location) === observed.location)) {
        transport.enqueue({...observed.payload, event:event.type, event_id:event.id}, observed.valid);
      }
      if (resolved.size > 512) resolved.delete(resolved.values().next().value);
    };
    const state = (event, name, extra = {}) => {
      const sessionID = event.data?.sessionID;
      if (!alive || !validID(event.id) || !validID(sessionID) || root(route()) !== sessionID || root(sessionID) !== sessionID) return;
      const current = lifecycles.get(sessionID);
      if (!current?.run || current.terminal) return;
      const epoch = routeEpoch, location = locationKey(context.data.session.get(sessionID)?.location);
      if (event.location && locationKey(event.location) !== location) return;
      current.summary = undefined; current.body = undefined; current.messageID = undefined;
      transport.enqueue({adapter_revision:2,runtime_version:version,event:name,session_id:sessionID,
        run_id:current.run,event_id:event.id,instance_id:activation,...extra},
        () => alive && epoch === routeEpoch && root(route()) === sessionID && locationKey(context.data.session.get(sessionID)?.location) === location);
    };
    const summaryStep = (event, ended = false) => {
      const sessionID = event.data?.sessionID, current = lifecycles.get(sessionID);
      if (!alive || !validID(event.id) || !current || current.terminal ||
          root(route()) !== sessionID || root(sessionID) !== sessionID ||
          !validID(event.data.assistantMessageID)) return;
      const location = locationKey(context.data.session.get(sessionID)?.location);
      if (event.location && locationKey(event.location) !== location) return;
      current.summary = undefined; current.body = undefined;
      if (!ended) { current.messageID = event.data.assistantMessageID; current.summaryLocation = location; return; }
      if (current.messageID !== event.data.assistantMessageID || current.summaryLocation !== location) return;
      // Read exactly this step's assistant from the official synchronous cache.
      // Missing cache entries degrade to status; never sync/scan old messages.
      try {
        const message = context.data.session.message?.get(sessionID, current.messageID);
        if (message?.id === current.messageID && message.type === "assistant") {
          current.summary = messageSummary(message.content);
          current.body = completionBody(message.content);
        }
      } catch { /* Optional content cannot interrupt completion. */ }
    };
    const stableJSON = value => {
      if (Array.isArray(value)) return "["+value.map(stableJSON).join(",")+"]";
      if (value && typeof value === "object") return "{"+Object.keys(value).sort().map(k=>JSON.stringify(k)+":"+stableJSON(value[k])).join(",")+"}";
      return JSON.stringify(value);
    };
    const receipt = (result, state, evidence) => new Promise(resolve => {
      if (!result?.owner_token || !result?.request_id) {resolve();return;}
      try {
        const body=Buffer.from(JSON.stringify({v:1,request_id:result.request_id,owner_token:result.owner_token,state,...(evidence?{evidence}:{})}));
        const header=Buffer.alloc(4);header.writeUInt32BE(body.length);
        const child=spawn(executable,["interaction","receipt","--stdin-frame"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","ignore","ignore"],shell:false});
        const timer=setTimeout(()=>{child.kill("SIGKILL");resolve();},5000);
        child.once("error",()=>{clearTimeout(timer);resolve();});child.once("close",()=>{clearTimeout(timer);resolve();});child.stdin.on("error",()=>child.kill("SIGKILL"));child.stdin.end(Buffer.concat([header,body]));
      } catch {resolve();}
    });
    const authorize = (result, legacy = false) => new Promise(resolve=>{
      if(!result?.request_id||!(legacy?result.digest:result.owner_token)){resolve(false);return;}
      let child,done=false,timer;const finish=value=>{if(done)return;done=true;clearTimeout(timer);child?.kill("SIGKILL");resolve(value);};
      try {const body=Buffer.from(JSON.stringify({v:1,request_id:result.request_id,...(legacy?{digest:result.digest}:{owner_token:result.owner_token})})),header=Buffer.alloc(4);header.writeUInt32BE(body.length);
        child=spawn(executable,["interaction",legacy?"authorize-approval":"authorize","--stdin-frame"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","pipe","ignore"],shell:false});let output="";
        timer=setTimeout(()=>finish(false),5000);child.stdout.on("data",bytes=>{output+=bytes;if(Buffer.byteLength(output)>4096)finish(false);});
        child.once("error",()=>finish(false));child.stdin.on("error",()=>finish(false));child.once("close",code=>{try{finish(code===0&&JSON.parse(output).allowed===true);}catch{finish(false);}});child.stdin.end(Buffer.concat([header,body]));
      }catch{finish(false);}
    });
    const startInteraction = async (payload, requestID, location, valid, isForm) => {
      const sessionID=payload.session_id, key=JSON.stringify([sessionID,requestID]);
      if(!atLeast(version, minimums.interaction.opencode)){transport.enqueue({...payload,unavailable_reason:"agent_version_unverified"},valid);return;}
      if(approvalChildren.has(key))return;
      if(approvalChildren.size>=8){transport.enqueue(payload,valid);return;}
      // Reserve before awaiting the official client, so duplicate events cannot
      // create two waiters for the same native continuation.
      const reserved={kill:()=>{reserved.cancelled=true;},cancelled:false};approvalChildren.set(key,reserved);
      let pending,snapshot,batch;
      try {
        pending=isForm?await context.client.session.form.get({sessionID,formID:requestID}):await context.client.permission.get({sessionID,requestID});
        if(reserved.cancelled||!valid()||pending.sessionID!==sessionID||pending.id!==requestID||isForm&&pending.state?.status!=="pending")return;
        const {state,...immutable}=pending;snapshot=stableJSON(immutable);
        batch=isForm?undefined:(await context.client.permission.list({sessionID})).filter(p=>p.sessionID===sessionID).map(p=>p.id).sort();
        if(reserved.cancelled||!valid())return;
        if(!isForm&&(!Array.isArray(batch)||!batch.includes(requestID))){transport.enqueue(payload,valid);return;}
        const body=JSON.stringify({...payload,cwd:location?.directory??context.data.session.get(sessionID)?.location?.directory,
          tool_name:isForm?"form":"permission",tool_use_id:requestID,
          ...(isForm?{native_form:pending}:{tool_input:pending,pending_permissions:batch})});
        if(Buffer.byteLength(body)>24576){transport.enqueue({...payload,unavailable_reason:"interaction_request_too_large"},valid);return;}
        const child=spawn(executable,["interaction","wait","--source","opencode"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","pipe","ignore"],shell:false});
        approvalChildren.set(key,child);let output="",overflow=false;
        const timer=setTimeout(()=>child.kill("SIGKILL"),isForm?305000:125000);
        child.stdout.on("data",bytes=>{output+=bytes;if(Buffer.byteLength(output)>65536){overflow=true;child.kill("SIGKILL");}});
        child.on("error",()=>{});child.stdin.on("error",()=>child.kill("SIGKILL"));
        child.once("close",async code=>{
          clearTimeout(timer);if(approvalChildren.get(key)===child)approvalChildren.delete(key);
          if(code!==0||overflow)return;
          let result;try {result=JSON.parse(output);}catch{return;}
          if(result?.legacy_approval) {
            const legacy=result.legacy_approval;
            if(legacy.hookSpecificOutput?.decision?.behavior!=="allow"||!valid()||!Number.isSafeInteger(legacy.expires_at)||Date.now()>=legacy.expires_at*1000)return;
            try {const current=await context.client.permission.get({sessionID,requestID});const {state,...immutable}=current;
              if(valid()&&Date.now()<legacy.expires_at*1000&&stableJSON(immutable)===snapshot&&await authorize(legacy,true)&&valid()&&Date.now()<legacy.expires_at*1000)await context.client.permission.reply({sessionID,requestID,decision:"once"});
            }catch{}return;
          }
          if(!result?.request_id)return;
          if(result.action==="release")return;
          if(!valid()||!Number.isSafeInteger(result.expires_at)||Date.now()>=result.expires_at*1000){await receipt(result,"native_cancelled");return;}
          let attempted=false;
          try {
            const current=isForm?await context.client.session.form.get({sessionID,formID:requestID}):await context.client.permission.get({sessionID,requestID});
            const {state,...immutable}=current;
            if(!valid()||Date.now()>=result.expires_at*1000||stableJSON(immutable)!==snapshot||isForm&&state?.status!=="pending"){await receipt(result,"native_cancelled");return;}
            if(!isForm&&result.action==="deny") {
              const now=(await context.client.permission.list({sessionID})).filter(p=>p.sessionID===sessionID).map(p=>p.id).sort();
              if(stableJSON(now)!==stableJSON(batch)){await receipt(result,"native_cancelled");return;}
            }
            if(!await authorize(result)||!valid()||Date.now()>=result.expires_at*1000){await receipt(result,"native_cancelled");return;}
            if(isForm){
              if(result.action!=="answer"&&result.action!=="cancel")return;
              const answer=JSON.parse(result.response_json).answers??{};
              const tool=pending.metadata?.tool;
              if(questionReceipts.size<128&&result.action==="answer"&&pending.metadata?.kind==="question"&&typeof tool?.messageID==="string"&&typeof tool?.id==="string")questionReceipts.set(key,{result,sessionID,messageID:tool.messageID,toolID:tool.id,answer,fields:pending.fields,expires:Date.now()+3600000});
              attempted=true;
              if(result.action==="cancel")await context.client.session.form.cancel({sessionID,formID:requestID});
              else await context.client.session.form.reply({sessionID,formID:requestID,answer});
            }else{
              if(result.action!=="allow_once"&&result.action!=="deny")return;
              attempted=true;await context.client.permission.reply({sessionID,requestID,decision:result.action==="allow_once"?"once":"reject"});
            }
            await receipt(result,"response_written");
          } catch {await receipt(result,attempted?"delivery_unknown":"native_cancelled");}
        });child.stdin.end(body);
      }catch {if(valid())transport.enqueue(payload,valid);}
      finally {if(approvalChildren.get(key)===reserved)approvalChildren.delete(key);}
    };
    const confirmQuestion = async event => {
      // Form events and terminal cache are not a consumer ACK. Only a result of
      // the precise originating question tool may confirm the answer value.
      for(const [key,item] of questionReceipts){
        if(Date.now()>item.expires){questionReceipts.delete(key);continue;}
        if(event.data?.sessionID!==item.sessionID||event.data?.id!==item.toolID||event.data?.assistantMessageID!==item.messageID)continue;
        const actual=event.data.metadata?.answers;
        if(!actual||typeof actual!=="object")continue;
        const expected=item.fields.map(f=>item.answer[f.key]===undefined?[]:Array.isArray(item.answer[f.key])?item.answer[f.key]:[String(item.answer[f.key])]);
        if(Array.isArray(actual)){if(stableJSON(actual)===stableJSON(expected))await receipt(item.result,"native_confirmed","question_answer_match");else await receipt(item.result,"superseded","question_answer_differs");}
        questionReceipts.delete(key);
      }
    };
    const registerReply = (payload, sessionID, location, valid) => {
      const api = context.client?.session;
      if (!atLeast(version, minimums.reply.opencode) || !api?.get || !api?.prompt || !api?.active || !api?.inbox?.list ||
          !api?.form?.list || !context.client.permission?.list || !location || route() !== sessionID) return;
      invalidateReply();
      replySession = sessionID;
      const current = () => valid() && replySession === sessionID && route() === sessionID &&
        !(context.data.session.form.list("global", context.data.session.get(sessionID)?.location)?.length) &&
        context.data.session.family(sessionID).every(id => context.data.session.permission.list(id)?.length === 0 &&
          context.data.session.form.list(id, context.data.session.get(id)?.location)?.length === 0);
      if (!current()) return;
      const preflight = async () => {
        const [session, active, inbox, forms, permissions] = await Promise.all([
          api.get({sessionID}), api.active(), api.inbox.list({sessionID}),
          api.form.list({sessionID}), context.client.permission.list({sessionID}),
        ]);
        return current() && session.id === sessionID && !session.parentID && !session.time?.archived &&
          locationKey(session.location) === location && active && typeof active === "object" && !Array.isArray(active) && !(sessionID in active) &&
          [inbox, forms, permissions].every(list => Array.isArray(list) && list.length === 0);
      };
      stopReply = replyOpportunity("opencode", payload, current, preflight, async (decision, tuple) => {
        const id = "msg_mobterm_" + decision.source_id;
        // A delivery event may arrive before the admission promise resolves.
        // Retain only identity and receipt proof; never retain the reply text.
        for (const [key, item] of inputReceipts) if (item.expiry <= Date.now()) inputReceipts.delete(key);
        if (inputReceipts.size < 128) inputReceipts.set(id, {sessionID, location, tuple, expiry:Date.now()+300000});
        const result = await api.prompt({sessionID, id, text:decision.text, delivery:"queue"});
        return result?.id === id && result.sessionID === sessionID && result.type === "user"
          && result.payload?.text === decision.text && result.delivery === "queue" ? "native_accepted" : "delivery_unknown";
      });
    };
    const handle = (event) => {
      try {
        if (!alive || !validID(event.id) || memory.seen.includes(event.id)) return;
        const nativeType = event.type;
        const eventLocation = event.location ? { directory: event.location.directory, workspaceID: event.location.workspaceID } : undefined;
        const completion = nativeType === "session.execution.succeeded";
        const sessionID = nativeType === "form.created" ? event.data.form?.sessionID : event.data.sessionID;
        const requestID = nativeType === "form.created" ? event.data.form?.id : event.data.id;
        if(sessionID === "global") {invalidateReply();if(nativeType==="form.created")context.ui.toast.show({title:"MobTerm",message:"This MCP form has no session owner. Respond in the original native interface.",variant:"warning"});return;}
        if (!validID(sessionID) || (!completion && !validID(requestID))) return;
        const selected = route(), selectedRoot = root(selected), eventRoot = root(sessionID);
        if (!selectedRoot || selectedRoot !== eventRoot || (completion && sessionID !== eventRoot)) return;
        const session = context.data.session.get(sessionID);
        const location = locationKey(session.location);
        if (eventLocation && locationKey(eventLocation) !== location) return;
        const epoch = routeEpoch, generation = lifecycles.get(sessionID);
        if (completion && (generation?.terminal || staleExecution(event))) return;
        const requestKey = JSON.stringify([sessionID,requestID]);
        const valid = () => {
          if (!alive || epoch !== routeEpoch || route() !== selected || root(sessionID) !== selectedRoot || root(selected) !== selectedRoot) return false;
          if (locationKey(context.data.session.get(sessionID)?.location) !== location) return false;
          if (lifecycles.get(sessionID) !== generation) return false;
          if (completion) return true;
          if (resolved.has(requestKey)) return false;
          const pending = nativeType === "permission.asked"
            ? context.data.session.permission.list(sessionID)
            : context.data.session.form.list(sessionID, eventLocation);
          return pending?.some((item) => item.id === requestID && item.sessionID === sessionID &&
            (!item.location || locationKey(item.location) === location)) === true;
        };
        // The official lists are pending-only. Unknown/not-yet-synced requests are skipped.
        if (!valid()) return;
        remember(event.id);
        const runID = lifecycles.get(eventRoot)?.run ?? generation?.run ?? randomUUID();
        const payload = { adapter_revision: 2, runtime_version: version, event: nativeType,
          session_id: sessionID, run_id: runID, event_id: event.id, instance_id: activation,
          ...(completion ? (generation?.summary && generation.summaryLocation === location ? {summary:generation.summary} : {}) : {request_id:requestID}) };
        if (!completion) {
          invalidateReply(sessionID);
          const rootGeneration=lifecycles.get(eventRoot);
          const validResolved=()=>alive && epoch===routeEpoch && route()===selected &&
            root(sessionID)===selectedRoot && root(selected)===selectedRoot &&
            locationKey(context.data.session.get(sessionID)?.location)===location &&
            lifecycles.get(sessionID)===generation && lifecycles.get(eventRoot)===rootGeneration &&
            !generation?.terminal && !rootGeneration?.terminal;
          if (pendingObservations.size>=128 && !pendingObservations.has(requestKey)) return;
          pendingObservations.set(requestKey,{payload,valid:validResolved,location});
        }
        if (nativeType === "permission.asked" || nativeType === "form.created") void startInteraction(payload,requestID,eventLocation,valid,nativeType === "form.created");
        else transport.enqueue(payload, valid, completion && generation && generation.summaryLocation === location ? generation.body : undefined);
        if (completion) {
          if (generation) {
            generation.terminal = true; generation.body = undefined;
            generation.sequence = executionSequence(event) ?? generation.sequence;
            const start = generation.start, end = event.durable;
            if (generation.run === payload.run_id && start?.aggregateID === sessionID && end?.aggregateID === sessionID &&
                start.version === 1 && end.version === 1 && Number.isSafeInteger(start.seq) && Number.isSafeInteger(end.seq) &&
                end.seq > start.seq) registerReply(payload, sessionID, location, valid);
          }
          forgetRequests(sessionID);
        }
      } catch { /* A changed upstream schema must never interrupt the TUI. */ }
    };
    const dispose = [];
    let disposeRoute;
    const cleanup = () => {
      if (!alive) return;
      invalidateReply(); inputReceipts.clear(); alive = false; routeEpoch++; transport.stop(); pendingObservations.clear(); questionReceipts.clear();
      for(const child of approvalChildren.values())child.kill("SIGTERM"); approvalChildren.clear();
      disposeRoute?.();
      for (const unsubscribe of dispose.splice(0).reverse()) {
        try { unsubscribe(); } catch { /* Continue releasing the other listeners. */ }
      }
    };
    try {
      // The host calls setup outside a Solid owner. Each activation owns its observer.
      createRoot((release) => {
        disposeRoute = release;
        createEffect(() => {
          const current = route();
          const currentLocation = current && locationKey(context.data.session.get(current)?.location);
          if (current !== previousRoute || currentLocation !== previousLocation) { previousRoute = current; previousLocation = currentLocation; routeEpoch++; invalidateReply(); transport.cancel(); pendingObservations.clear(); for (const current of lifecycles.values()) {current.summary=undefined;current.body=undefined;current.messageID=undefined;} for(const child of approvalChildren.values())child.kill("SIGTERM"); approvalChildren.clear(); }
        });
      });
      for (const [type, handler] of [
        ["session.execution.started", (event) => { if (validID(event.id) && !staleExecution(event)) {lifecycle(event.data.sessionID, false, event);state(event,event.type);} }],
        ["session.execution.failed", (event) => {if (!staleExecution(event)) {state(event,event.type);lifecycle(event.data.sessionID,true,event);}}],
        ["session.execution.interrupted", (event) => {if (!staleExecution(event)) {state(event,event.type);lifecycle(event.data.sessionID,true,event);}}],
        ["session.execution.succeeded", handle],
        ["session.inbox.enqueued", event => invalidateReply(event.data?.sessionID)],
        ["session.status", event => { if (event.data?.status?.type !== "idle") invalidateReply(event.data?.sessionID); }],
        ["session.inbox.delivered", event => {
          const id = event.data?.inboxID, item = inputReceipts.get(id);
          if (item && item.sessionID === event.data.sessionID && item.expiry > Date.now() &&
              (!event.location || locationKey(event.location) === item.location)) {
            inputReceipts.delete(id); void replyReceipt(item.tuple, "native_consumed");
          }
          invalidateReply(event.data?.sessionID);
        }],
        ["session.step.started", event => summaryStep(event)],
        ["session.step.ended", event => summaryStep(event, true)],
        ["session.tool.input.started", event => {if(validID(event.data.id)&&typeof event.data.name==="string"){tools.set(event.data.id,event.data.name);if(tools.size>128)tools.delete(tools.keys().next().value);state(event,"tool_pending",{tool_id:event.data.id,tool_name:event.data.name});}}],
        ["session.tool.called", event => {if(validID(event.data.id))state(event,event.data.executed===true?"tool_execution_start":"tool_pending",{tool_id:event.data.id,tool_name:tools.get(event.data.id)??"other"});}],
        ["session.tool.success", event => {void confirmQuestion(event);if(validID(event.data.id))state(event,"tool_execution_end",{tool_id:event.data.id,tool_name:tools.get(event.data.id)??"other",tool_status:"succeeded"});}],
        ["session.tool.failed", event => {if(validID(event.data.id))state(event,"tool_execution_end",{tool_id:event.data.id,tool_name:tools.get(event.data.id)??"other",tool_status:"failed"});}],
        ["permission.asked", handle],
        ["form.created", handle],
        ["permission.replied", (event) => clearRequest(event, event.data.requestID)],
        ["form.replied", (event) => clearRequest(event, event.data.id)],
        ["form.cancelled", (event) => clearRequest(event, event.data.id)],
      ]) dispose.push(context.data.on(type, handler));
      return cleanup;
    } catch {
      cleanup();
    }
  },
});
