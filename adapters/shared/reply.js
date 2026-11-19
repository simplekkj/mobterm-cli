// Each activation keeps at most one helper for its visible original source.
// stdout is private and bounded. Only the adapter holding the original
// context can cross the final native call boundary.
function replyCommand(args, payload, framed, deadline, signal) {
  return new Promise(resolve => {
    if (signal?.aborted || Date.now() >= deadline) { resolve(); return; }
    let child, timer, finished = false;
    const close = result => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      resolve(result);
    };
    const abort = () => { child?.kill("SIGKILL"); close(); };
    try {
      const body = Buffer.from(JSON.stringify(payload));
      const header = Buffer.alloc(4);
      header.writeUInt32BE(body.length);
      child = spawn(executable, ["reply", ...args], {
        env: { ...process.env, MOBTERM_CONFIG_DIR: store },
        stdio: ["pipe", "pipe", "ignore"], shell: false,
      });
      signal?.addEventListener("abort", abort, {once:true});
      timer = setTimeout(abort, Math.max(1, deadline - Date.now()));
      const output = [];
      let size = 0;
      child.stdout.on("data", chunk => {
        size += chunk.length;
        if (size > 128 * 1024) { abort(); return; }
        output.push(chunk);
      });
      child.once("error", abort);
      child.stdin.on("error", abort);
      child.once("close", code => {
        try { close(code === 0 ? JSON.parse(Buffer.concat(output).toString("utf8")) : undefined); }
        catch { close(); }
      });
      child.stdin.end(framed ? Buffer.concat([header, body]) : body);
    } catch { abort(); }
  });
}

function replyReceipt(tuple, state) {
  return replyCommand(["receipt", "--stdin-frame"], {...tuple, state}, true, Date.now() + 5000);
}

function replyOpportunity(agent, source, valid, preflight, submit) {
  const controller = new AbortController();
  const expires = Math.floor(Date.now() / 1000) + 300;
  const current = () => !controller.signal.aborted && Date.now() < expires * 1000 && valid();
  const deliver = async () => {
    const decision = await replyCommand(["wait", "--source", agent], {
      runtime_version: source.runtime_version, session_id: source.session_id,
      run_id: source.run_id, event_id: source.event_id, instance_id: source.instance_id,
      expires_at: expires,
    }, false, expires * 1000, controller.signal);
    if (decision?.v !== 1 || !validID(decision.source_id) || !validID(decision.owner_token)
        || !validID(decision.decision_id) || typeof decision.text !== "string") return;
    const tuple = {v:1, source_id:decision.source_id, owner_token:decision.owner_token, decision_id:decision.decision_id};
    let attempted = false;
    try {
      if (current() && await preflight() && current()) {
        const auth = await replyCommand(["authorize", "--stdin-frame"], tuple, true,
          Math.min(expires * 1000, Date.now() + 5000), controller.signal);
        if (auth?.allowed === true && current()) {
          attempted = true;
          const evidence = await submit(decision, tuple);
          await replyReceipt(tuple, evidence);
          return;
        }
      }
    } catch { /* A result lost after submission stays unknown. */ }
    await replyReceipt(tuple, attempted ? "delivery_unknown" : "not_dispatched");
  };
  void deliver().catch(() => {});
  return () => controller.abort();
}
