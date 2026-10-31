// Installed by mobterm-cli. No event content is persisted or printed.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
const executable = __MOBTERM_CLI_JSON__;
const store = __MOBTERM_STORE_JSON__;
const validID = (value) => typeof value === "string" && /^[A-Za-z0-9_.:-]{1,256}$/.test(value);
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
      const critical = ["session.execution.succeeded","session.execution.failed","session.execution.interrupted","permission.asked","form.created","agent_settled","ui_prompt_start","permission.replied","form.replied","form.cancelled"].includes(payload.event);
      // A resolved/invalidated pending request must not occupy the bounded
      // queue ahead of the observation that clears its waiting state.
      for (let i=queue.length-1;i>=0;i--) { if (!queue[i].valid()) queue.splice(i,1); }
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
import { Plugin } from "@opencode/plugin/tui";
import { createEffect, createRoot } from "solid-js";

export default Plugin.define({
  id: "mobterm.jumpto",
  setup(context) {
    const version = context.app.version;
    if (!stableVersion(version, [2, 0, 0]) || context.app.channel !== "latest") return;
    const [memory, update] = context.storage.memory("mobterm.jumpto.v1", {
      initial: { instance: randomUUID(), seen: [] },
    });
    let alive = true, routeEpoch = 0, previousRoute;
    const lifecycles = new Map();
    const tools = new Map();
    const approvalChildren = new Map();
    const resolved = new Set();
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
    const lifecycle = (sessionID, terminal) => {
      if (!validID(sessionID)) return;
      forgetRequests(sessionID);
      const previous = lifecycles.get(sessionID);
      lifecycles.delete(sessionID);
      lifecycles.set(sessionID, { generation: (previous?.generation ?? 0) + 1, terminal, run: terminal ? previous?.run ?? randomUUID() : randomUUID() });
      if (lifecycles.size > 512) lifecycles.delete(lifecycles.keys().next().value);
    };
    const clearRequest = (event, id) => {
      if (!validID(id) || !validID(event.data.sessionID)) return;
      const key = event.data.sessionID + ":" + id;
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
      transport.enqueue({adapter_revision:2,runtime_version:version,event:name,session_id:sessionID,
        run_id:current.run,event_id:event.id,instance_id:memory.instance,...extra},
        () => alive && epoch === routeEpoch && root(route()) === sessionID && locationKey(context.data.session.get(sessionID)?.location) === location);
    };
    const stableJSON = value => {
      if (Array.isArray(value)) return "["+value.map(stableJSON).join(",")+"]";
      if (value && typeof value === "object") return "{"+Object.keys(value).sort().map(k=>JSON.stringify(k)+":"+stableJSON(value[k])).join(",")+"}";
      return JSON.stringify(value);
    };
    const startApproval = (payload, requestID, location, valid) => {
      const sessionID=payload.session_id, key=sessionID+":"+requestID;
      const pending=context.data.session.permission.list(sessionID)?.find(r=>r.id===requestID&&r.sessionID===sessionID);
      if (!pending || typeof pending.action!=="string" || !Array.isArray(pending.resources) || !pending.resources.every(x=>typeof x==="string")) {
        transport.enqueue(payload,valid); return;
      }
      const snapshot=stableJSON(pending);
      if (Buffer.byteLength(snapshot)>6144 || approvalChildren.has(key) || approvalChildren.size>=8) {transport.enqueue(payload,valid);return;}
      const body=JSON.stringify({...payload,hook_event_name:"PermissionRequest",tool_name:"permission",tool_use_id:requestID,tool_input:pending});
      try {
        const child=spawn(executable,["approval","wait","--source","opencode"],{env:{...process.env,MOBTERM_CONFIG_DIR:store},stdio:["pipe","pipe","ignore"],shell:false});
        approvalChildren.set(key,child);
        let output="",overflow=false;
        const timer=setTimeout(()=>child.kill("SIGTERM"),125000);
        child.stdout.on("data",bytes=>{output+=bytes;if(Buffer.byteLength(output)>4096){overflow=true;child.kill("SIGTERM");}});
        child.on("error",()=>{});
        child.stdin.on("error",()=>child.kill("SIGTERM"));
        child.once("close",async code=>{
          clearTimeout(timer);if(approvalChildren.get(key)===child)approvalChildren.delete(key);
          if(code!==0||overflow||!valid())return;
          try {
            const result=JSON.parse(output);
            if(!Number.isSafeInteger(result.expires_at)||Date.now()>=result.expires_at*1000)return;
            const behavior=result.hookSpecificOutput?.decision?.behavior;
            if(behavior!=="allow"&&behavior!=="deny")return;
            const current=await context.client.permission.get({sessionID,requestID});
            if(!valid()||Date.now()>=result.expires_at*1000||stableJSON(current)!==snapshot)return;
            await context.client.permission.reply({sessionID,requestID,reply:behavior==="allow"?"once":"reject"});
          }catch{/* An ambiguous native result is never replayed. */}
        });
        child.stdin.end(body);
      }catch{transport.enqueue(payload,valid);}
    };
    const handle = (event) => {
      try {
        if (!alive || !validID(event.id) || memory.seen.includes(event.id)) return;
        const nativeType = event.type;
        const eventLocation = event.location ? { directory: event.location.directory, workspaceID: event.location.workspaceID } : undefined;
        const completion = nativeType === "session.execution.succeeded";
        const sessionID = nativeType === "form.created" ? event.data.form?.sessionID : event.data.sessionID;
        const requestID = nativeType === "form.created" ? event.data.form?.id : event.data.id;
        if (!validID(sessionID) || sessionID === "global" || (!completion && !validID(requestID))) return;
        const selected = route(), selectedRoot = root(selected), eventRoot = root(sessionID);
        if (!selectedRoot || selectedRoot !== eventRoot || (completion && sessionID !== eventRoot)) return;
        const session = context.data.session.get(sessionID);
        const location = locationKey(session.location);
        if (eventLocation && locationKey(eventLocation) !== location) return;
        const epoch = routeEpoch, generation = lifecycles.get(sessionID);
        if (completion && generation?.terminal) return;
        const requestKey = sessionID + ":" + requestID;
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
          session_id: sessionID, run_id: runID, event_id: event.id, instance_id: memory.instance,
          ...(completion ? {} : {request_id:requestID}) };
        if (!completion) {
          const rootGeneration=lifecycles.get(eventRoot);
          const validResolved=()=>alive && epoch===routeEpoch && route()===selected &&
            root(sessionID)===selectedRoot && root(selected)===selectedRoot &&
            locationKey(context.data.session.get(sessionID)?.location)===location &&
            lifecycles.get(sessionID)===generation && lifecycles.get(eventRoot)===rootGeneration &&
            !generation?.terminal && !rootGeneration?.terminal;
          if (pendingObservations.size>=128 && !pendingObservations.has(requestKey)) return;
          pendingObservations.set(requestKey,{payload,valid:validResolved,location});
        }
        if (nativeType === "permission.asked") startApproval(payload, requestID, eventLocation, valid);
        else transport.enqueue(payload, valid);
        if (completion) { if (generation) generation.terminal=true; forgetRequests(sessionID); }
      } catch { /* A changed upstream schema must never interrupt the TUI. */ }
    };
    const dispose = [];
    let disposeRoute;
    const cleanup = () => {
      if (!alive) return;
      alive = false; routeEpoch++; transport.stop(); pendingObservations.clear();
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
          if (current !== previousRoute) { previousRoute = current; routeEpoch++; transport.cancel(); pendingObservations.clear(); for(const child of approvalChildren.values())child.kill("SIGTERM"); approvalChildren.clear(); }
        });
      });
      for (const [type, handler] of [
        ["session.execution.started", (event) => { if (validID(event.id)) {lifecycle(event.data.sessionID, false);state(event,event.type);} }],
        ["session.execution.failed", (event) => {state(event,event.type);lifecycle(event.data.sessionID,true);}],
        ["session.execution.interrupted", (event) => {state(event,event.type);lifecycle(event.data.sessionID,true);}],
        ["session.execution.succeeded", handle],
        ["session.tool.input.started", event => {if(validID(event.data.id)&&typeof event.data.name==="string"){tools.set(event.data.id,event.data.name);if(tools.size>128)tools.delete(tools.keys().next().value);state(event,"tool_pending",{tool_id:event.data.id,tool_name:event.data.name});}}],
        ["session.tool.called", event => {if(validID(event.data.id))state(event,event.data.executed===true?"tool_execution_start":"tool_pending",{tool_id:event.data.id,tool_name:tools.get(event.data.id)??"other"});}],
        ["session.tool.success", event => {if(validID(event.data.id))state(event,"tool_execution_end",{tool_id:event.data.id,tool_name:tools.get(event.data.id)??"other",tool_status:"succeeded"});}],
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
