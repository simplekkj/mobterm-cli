const executable = __MOBTERM_CLI_JSON__;
const store = __MOBTERM_STORE_JSON__;
__MOBTERM_MINIMUM_JS__

let interactive = false, generation = 0, turn, stream;
function invalidate() {
  generation++;
  turn = undefined;
  const old = stream;
  stream = undefined;
  if (old) void old.return().catch(() => {});
}

// The Mod process API accepts UTF-8 input. The receipt tuple is ASCII;
// add legal JSON whitespace so its four length octets are also UTF-8.
function frame(tuple) {
  const json = JSON.stringify(tuple);
  const length = new TextEncoder().encode(json).length;
  const size = Math.ceil(length / 256) * 256;
  if (size >= 32768) throw Error("Invalid local receipt size");
  return String.fromCharCode(0, 0, size / 256, 0) + json + " ".repeat(size - length);
}

async function deliver($, candidate, decision) {
  if (decision?.v !== 1 || typeof decision.text !== "string" ||
      ![decision.source_id, decision.decision_id, decision.owner_token].every(id => typeof id === "string" && id.length > 0)) return;
  const tuple = {v:1,source_id:decision.source_id,decision_id:decision.decision_id,owner_token:decision.owner_token};
  const env = candidate.env;
  let state = "not_dispatched";
  try {
    const auth = await $.process.run([executable,"reply","authorize","--stdin-frame"],
      {env,stdin:frame(tuple),timeoutMs:5000});
    const session = await $.session.id();
    const info = await $.session.info();
    const surfaces = await $.session.surfaces();
    const now = await $.clock.now();
    if (generation === candidate.generation && turn === candidate && interactive &&
        session === candidate.session && info.promptId === candidate.prompt &&
        Array.isArray(surfaces) && surfaces.includes("terminal") && now < candidate.expires * 1000 &&
        auth.exitCode === 0 && !auth.isStdoutTruncated && JSON.parse(auth.stdout).allowed === true) {
      state = "delivery_unknown";
      const result = await $.prompt.submit({text:decision.text,asUser:true});
      if (typeof result?.drop === "string") state = "native_rejected";
      else if (result?.text === decision.text) state = "native_accepted";
    }
  } catch { /* Once the native call starts, a lost result is unknown. */ }
  await $.process.run([executable,"reply","receipt","--stdin-frame"],
    {env,stdin:frame({...tuple,state}),timeoutMs:5000});
}

async function waitForDecision($, candidate, registration) {
  const child = $.process.spawn({
    argv:registration ? [executable,"reply","reopen","--stdin-frame"] : [executable,"reply","wait","--source","claude"], env:candidate.env,
    input:registration ? frame(registration) : JSON.stringify({renewable:true,runtime_version:candidate.version,session_id:candidate.session,
      run_id:candidate.prompt,event_id:candidate.prompt,instance_id:candidate.instance,
      expires_at:candidate.expires}),
  });
  stream = child;
  let output = "";
  try {
    for await (const piece of child) {
      if (piece.stream !== "stdout") continue;
      output += piece.text;
      if (output.length > 128 * 1024) return;
    }
    if ((await child.result).code !== 0 || !output) return;
    const result = JSON.parse(output);
    if (result?.wake === true) {
      if (turn === candidate && /^[a-f0-9]{64}$/.test(result.source_id) &&
          typeof result.owner_token === "string" && result.owner_token.length <= 256) {
        candidate.registration = {v:1, source_id:result.source_id, owner_token:result.owner_token};
      }
    } else {
      await deliver($, candidate, result);
    }
  } finally {
    if (stream === child) stream = undefined;
  }
}

async function checkWake($) {
  const candidate = turn;
  if (!candidate?.registration || candidate.checking || stream || !interactive) return;
  candidate.checking = true;
  try {
    const file = store + "/reply-wake-" + candidate.registration.source_id + ".json";
    if (!await $.fs.exists(file)) return;
    const info = await $.fs.stat(file);
    if (info.isLink || info.kind !== "file" || info.size > 32 || candidate.lastWake === info.mtimeMs) return;
    candidate.lastWake = info.mtimeMs;
    const session = await $.session.id(), prompt = (await $.session.info()).promptId;
    const surfaces = await $.session.surfaces();
    const now = await $.clock.now();
    if (turn !== candidate || session !== candidate.session || prompt !== candidate.prompt ||
        !Array.isArray(surfaces) || !surfaces.includes("terminal")) return;
    candidate.expires = Math.floor(now / 1000) + 60;
    await waitForDecision($, candidate, candidate.registration);
  } finally { candidate.checking = false; }
}

let navigationNonce;
async function checkNavigation($) {
  if (!interactive) return;
  const path = store + "/navigation-request.json";
  if (!await $.fs.exists(path)) return;
  const file = await $.fs.stat(path);
  if (file.isLink || file.kind !== "file" || file.size > 256) return;
  const request = JSON.parse(await $.fs.read(path));
  if (!/^[A-Za-z0-9_-]{43}$/.test(request.nonce) || navigationNonce === request.nonce ||
      request.until * 1000 <= await $.clock.now()) return;
  const env = await $.process.hookEnv();
  if (request.pid !== Number(env.CLAUDE_PID)) return;
  navigationNonce = request.nonce;
  const activation = generation, session = await $.session.id();
  if (!session || session !== env.CLAUDE_CODE_SESSION_ID || !(await $.session.surfaces()).includes("terminal")) return;
  const options = {env:{...env,MOBTERM_CONFIG_DIR:store}, timeoutMs:3000,
    stdin:frame({v:1,agent:"claude",nonce:request.nonce,session_id:session})};
  const result = await $.process.run([executable,"reply","navigation-observe","--stdin-frame"],options);
  if (result.exitCode === 0 && !result.isStdoutTruncated && JSON.parse(result.stdout).ok &&
      interactive && generation === activation && await $.session.id() === session &&
      (await $.session.surfaces()).includes("terminal") && generation === activation) {
    await $.process.run([executable,"reply","navigation-confirm","--stdin-frame"],options);
  }
}

export function register(on) {
  let instance, endedSession, timer;
  on("session.start", ($, e, next) => {
    invalidate();
    endedSession = undefined;
    interactive = e.isInteractive === true && e.surface === "terminal";
    timer?.cancel();
    if (interactive) timer = $.clock.every(1000, () => Promise.all([checkWake($),checkNavigation($)].map(task=>task.catch(()=>{}))));
    instance = crypto.randomUUID();
    return next(e);
  });
  on("session.end", ($, e, next) => {
    invalidate();
    endedSession = e.sessionId;
    // clear and resume replace the conversation inside the same interactive host.
    interactive = interactive && (e.reason === "clear" || e.reason === "resume") &&
      typeof endedSession === "string" && endedSession.length > 0;
    if (!interactive) { timer?.cancel(); timer = undefined; }
    instance = crypto.randomUUID();
    return next(e);
  });
  on("session.stop", ($, e, next) => { invalidate(); return next(e); });
  on("prompt.submit", ($, e, next) => { invalidate(); return next(e); });
  on("tool.call", ($, e, next) => { if (turn?.complete) invalidate(); return next(e); });
  on("turn.start", async ($, e, next) => {
    if (e.agentId === undefined && interactive) {
      invalidate();
      const captured = generation;
      try {
        const session = await $.session.id();
        const info = await $.session.info();
        const version = (await $.session.version()).version;
        const env = await $.process.hookEnv();
        const surfaces = await $.session.surfaces();
        if (generation === captured && info.promptId && e.turnId && session && session !== endedSession &&
            Array.isArray(surfaces) && surfaces.includes("terminal") &&
            env.CLAUDE_CODE_SESSION_ID === session && atLeast(version,minimums.reply.claude)) {
          turn = {generation:captured, instance, session, version, prompt:info.promptId, native:e.turnId,
            env:{...env,MOBTERM_CONFIG_DIR:store}, complete:false};
        }
      } catch { /* Classic hooks still deliver the source notification. */ }
    }
    return next(e);
  });
  on("turn.complete", async ($, e, next) => {
    const candidate = turn;
    if (candidate && e.agentId === undefined && e.turnId === candidate.native && !candidate.complete &&
        e.isAborted === false && e.reason === "answer") {
      candidate.complete = true;
      try {
        const info = await $.session.info();
        const session = await $.session.id();
        const now = await $.clock.now();
        if (turn === candidate && session === candidate.session && info.promptId === candidate.prompt) {
          candidate.expires = Math.floor(now / 1000) + 15;
          void waitForDecision($, candidate).catch(() => {});
        }
      } catch { /* No matching runtime observation; keep terminal handling. */ }
    }
    return next(e);
  });
}
