const executable = __MOBTERM_CLI_JSON__;
const store = __MOBTERM_STORE_JSON__;
__MOBTERM_MINIMUM_JS__

let interactive = false, canRestore = false, generation = 0, turn, stream;
function invalidate() {
  canRestore = false;
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

// A user-resumed Claude TUI can have no promptId. The main transcript
// exposes the UUID of its last row on a bounded read. Only that ID leaves
// the Mod, and only an assistant row can match a completed source.
async function assistantEntry($) {
  try {
    const rows = await $.session.messages({last:1});
    const row = rows.length === 1 ? rows[0] : undefined;
    if (row?.role === "assistant" && typeof row.uuid === "string" && row.uuid.length > 0) return row.uuid;
  } catch { /* The original interactive prompt can still use its own epoch. */ }
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
    const entryCurrent = !candidate.rebound || await assistantEntry($) === candidate.entry;
    if (entryCurrent && generation === candidate.generation && turn === candidate && interactive &&
        session === candidate.session && info.promptId === candidate.prompt && info.transcriptPath === candidate.storage &&
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
    argv:registration ? [executable,"reply",registration.owner_token ? "reopen" : "rebind","--stdin-frame"] : [executable,"reply","wait","--source","claude"], env:candidate.env,
    input:registration ? frame(registration) : JSON.stringify({renewable:true,runtime_version:candidate.version,session_id:candidate.session,
      run_id:candidate.prompt,event_id:candidate.prompt,instance_id:candidate.instance,
      expires_at:candidate.expires,storage:candidate.storage,turn:candidate.entry}),
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
    const stat = await $.fs.stat(file);
    if (stat.isLink || stat.kind !== "file" || stat.size > 32 || candidate.lastWake === stat.mtimeMs) return;
    candidate.lastWake = stat.mtimeMs;
    const session = await $.session.id(), info = await $.session.info(), prompt = info.promptId;
    const surfaces = await $.session.surfaces();
    const now = await $.clock.now();
    const entryCurrent = !candidate.rebound || await assistantEntry($) === candidate.entry;
    if (!entryCurrent || turn !== candidate || session !== candidate.session || prompt !== candidate.prompt || info.transcriptPath !== candidate.storage ||
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
  const storage = (await $.session.info()).transcriptPath;
  const options = {env:{...env,MOBTERM_CONFIG_DIR:store}, timeoutMs:3000,
    stdin:frame({v:1,agent:"claude",nonce:request.nonce,session_id:session,storage})};
  const result = await $.process.run([executable,"reply","navigation-observe","--stdin-frame"],options);
  if (result.exitCode === 0 && !result.isStdoutTruncated && JSON.parse(result.stdout).ok &&
      interactive && generation === activation && await $.session.id() === session && (await $.session.info()).transcriptPath === storage &&
      (await $.session.surfaces()).includes("terminal") && generation === activation) {
    await $.process.run([executable,"reply","navigation-confirm","--stdin-frame"],options);
  }
}

let endedSession, registered, registering, registrationAttempt, lastHostEpoch = 0;
async function hostEpoch($) {
  // The store compares epochs only within this PID/birth. Multiplying
  // milliseconds leaves room for multiple lifecycle changes in one tick.
  const now = await $.clock.now();
  lastHostEpoch = Math.max(lastHostEpoch + 1, now * 1000);
  return String(lastHostEpoch);
}

async function unregisterHost($, captured) {
  const env = {...await $.process.hookEnv(), MOBTERM_CONFIG_DIR:store};
  const epoch = await hostEpoch($);
  if (generation !== captured) return;
  await $.process.run([executable,"reply","host","--stdin-frame"], {
    env, timeoutMs:4000,
    stdin:frame({v:1,host_token:"__MOBTERM_HOST_TOKEN__",agent:"claude",session_id:"",storage:"",epoch})
  });
}

async function registerHost($) {
  if (!interactive || registering || registered || registrationAttempt === generation) return;
  registering = true;
  try {
    const captured = generation, session = await $.session.id(), info = await $.session.info();
    const storage = info.transcriptPath;
    if (!session || session === endedSession || !storage || !await $.fs.exists(storage)) return;
    const env = {...await $.process.hookEnv(),MOBTERM_CONFIG_DIR:store};
    if (env.CLAUDE_CODE_SESSION_ID !== session || !(await $.session.surfaces()).includes("terminal")) return;
    const entry = canRestore ? await assistantEntry($) : undefined;
    const epoch = await hostEpoch($);
    if (captured !== generation || !interactive) return;
    const proof = {v:1,host_token:"__MOBTERM_HOST_TOKEN__",agent:"claude",session_id:session,storage,turn:entry,epoch};
    registrationAttempt = captured;
    const result = await $.process.run([executable,"reply","host","--stdin-frame"],
      {env,stdin:frame(proof),timeoutMs:4000});
    if (captured !== generation || result.exitCode !== 0 || result.isStdoutTruncated) return;
    registered = true;
    const source = JSON.parse(result.stdout).source_id;
    if (turn || !canRestore || !entry || !/^[a-f0-9]{64}$/.test(source)) return;
    const version = (await $.session.version()).version;
    if (captured !== generation || !atLeast(version,minimums.reply.claude)) return;
    turn = {generation:captured,session,storage,entry,prompt:info.promptId,env,complete:true,rebound:true,
      registration:{...proof,source_id:source}};
  } finally {registering = false;}
}

export function register(on) {
  let instance, timer;
  on("session.start", ($, e, next) => {
    invalidate();
    endedSession = undefined; registered = false;
    interactive = e.isInteractive === true && e.surface === "terminal";
    canRestore = interactive;
    timer?.cancel();
    if (interactive) timer = $.clock.every(1000, () => Promise.all([registerHost($),checkWake($),checkNavigation($)].map(task=>task.catch(()=>{}))));
    instance = crypto.randomUUID();
    if (interactive) void registerHost($).catch(()=>{});
    return next(e);
  });
  on("session.end", async ($, e, next) => {
    invalidate();
    endedSession = e.sessionId; registered = undefined;
    // clear and resume replace the conversation inside the same interactive host.
    interactive = interactive && (e.reason === "clear" || e.reason === "resume") &&
      typeof endedSession === "string" && endedSession.length > 0;
    canRestore = interactive;
    if (!interactive) { timer?.cancel(); timer = undefined; }
    try { await unregisterHost($, generation); } catch { /* A dead PID cannot answer a challenge. */ }
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
          turn = {generation:captured, instance, session, version, prompt:info.promptId, storage:info.transcriptPath, native:e.turnId,
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
        const entry = await assistantEntry($);
        if (turn === candidate && session === candidate.session && info.promptId === candidate.prompt) {
          candidate.entry = entry;
          candidate.expires = Math.floor(now / 1000) + 15;
          void waitForDecision($, candidate).catch(() => {});
        }
      } catch { /* No matching runtime observation; keep terminal handling. */ }
    }
    return next(e);
  });
}
