// @ts-nocheck -- shipped as standalone host extension; contract tested by endpoint fixtures.
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
  const transport = bridge("pi", () => {
    if (context?.mode === "tui") context.ui.notify("MobTerm notification unavailable. Check integration.", "warning");
  });
  const session = (ctx) => ctx.sessionManager.getSessionId();
  const reset = (ctx) => { context = ctx; epoch++; round = undefined; transport.cancel(); };
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
