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
      if (queue.length >= 8) { diagnostic(); return; }
      queue.push({ payload, valid }); queueMicrotask(drain);
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
    const resolved = new Set();
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
    const lifecycle = (sessionID, terminal) => {
      if (!validID(sessionID)) return;
      const previous = lifecycles.get(sessionID);
      lifecycles.delete(sessionID);
      lifecycles.set(sessionID, { generation: (previous?.generation ?? 0) + 1, terminal });
      if (lifecycles.size > 512) lifecycles.delete(lifecycles.keys().next().value);
    };
    const clearRequest = (event, id) => {
      if (!validID(id) || !validID(event.data.sessionID)) return;
      resolved.add(event.data.sessionID + ":" + id);
      if (resolved.size > 512) resolved.delete(resolved.values().next().value);
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
        transport.enqueue({ adapter_revision: 1, runtime_version: version, event: nativeType,
          session_id: sessionID, event_id: event.id, instance_id: memory.instance }, valid);
      } catch { /* A changed upstream schema must never interrupt the TUI. */ }
    };
    const dispose = [];
    let disposeRoute;
    const cleanup = () => {
      if (!alive) return;
      alive = false; routeEpoch++; transport.stop();
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
          if (current !== previousRoute) { previousRoute = current; routeEpoch++; transport.cancel(); }
        });
      });
      for (const [type, handler] of [
        ["session.execution.started", (event) => { if (validID(event.id)) lifecycle(event.data.sessionID, false); }],
        ["session.execution.failed", (event) => lifecycle(event.data.sessionID, true)],
        ["session.execution.interrupted", (event) => lifecycle(event.data.sessionID, true)],
        ["session.execution.succeeded", handle],
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
