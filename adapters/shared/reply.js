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

// A host activation owns one watcher and one current completed source.
// File events only wake an on-demand check. No idle child or reply text is kept.
function replyWakeMailbox(agent, navigationSession) {
  let active, watcher, timer, generation = 0, navigationNonce;
  const checkNavigation = async () => {
    const file = store + "/navigation-request.json";
    try {
      const fd = openSync(file, fsFlags.O_RDONLY | fsFlags.O_NOFOLLOW | fsFlags.O_NONBLOCK);
      let request;
      try {
        const info = fstatSync(fd);
        if (!info.isFile() || info.uid !== process.getuid() || (info.mode & 0o077) || info.size > 256) return;
        const bytes = Buffer.alloc(257), count = readSync(fd, bytes, 0, bytes.length, 0);
        if (count !== info.size) return;
        request = JSON.parse(bytes.toString("utf8", 0, count));
      } finally { closeSync(fd); }
      if (request.pid !== process.pid || !/^[A-Za-z0-9_-]{43}$/.test(request.nonce) ||
          request.nonce === navigationNonce || request.until * 1000 <= Date.now()) return;
      navigationNonce = request.nonce;
      const initial = navigationSession(), activation = generation;
      if (!initial || !validID(initial.id)) return;
      const observation = {v:1, agent, nonce:request.nonce, session_id:initial.id};
      const sampled = await replyCommand(["navigation-observe", "--stdin-frame"], observation, true, request.until * 1000);
      const current = navigationSession();
      if (sampled?.ok && generation === activation && current?.id === initial.id && current.epoch === initial.epoch) {
        await replyCommand(["navigation-confirm", "--stdin-frame"], observation, true, request.until * 1000);
      }
    } catch { /* An absent challenge is the normal idle state. */ }
  };
  const scan = (filename) => {
    if (!filename || filename === "navigation-request.json") void checkNavigation();
    const entry = active;
    if (!entry || entry.busy || (filename && filename !== entry.name)) return;
    try {
      const info = wakeStat(store + "/" + entry.name);
      if (!info.isFile() || info.uid !== process.getuid() || (info.mode & 0o077) ||
          info.size > 32 || info.mtimeMs === entry.last) return;
      entry.last = info.mtimeMs;
      entry.busy = true;
      void entry.handle().catch(() => {}).finally(() => { entry.busy = false; });
    } catch { /* Missing marker is the normal idle path. */ }
  };
  return {
    start() {
      if (timer) return;
      try {
        watcher = watchReplyStore(store, (_event, name) => scan(name));
        watcher.on("error", () => { watcher?.close(); watcher = undefined; });
        watcher.unref();
      } catch { /* The bounded rescan also covers missed filesystem events. */ }
      timer = setInterval(scan, 1500);
      timer.unref();
    },
    arm(source, handle) {
      const entry = {name:"reply-wake-" + source + ".json", handle};
      active = entry;
      scan();
      return () => { if (active === entry) active = undefined; };
    },
    stop() {
      generation++; navigationNonce = undefined;
      active = undefined;
      watcher?.close(); watcher = undefined;
      clearInterval(timer); timer = undefined;
    },
  };
}

function replyOpportunity(mailbox, agent, source, valid, preflight, submit) {
  const controller = new AbortController();
  const current = () => !controller.signal.aborted && valid();
  let disarm;
  const consume = async (decision, deadline) => {
    if (decision?.v !== 1 || !validID(decision.source_id) || !validID(decision.owner_token)
        || !validID(decision.decision_id) || typeof decision.text !== "string") return;
    const tuple = {v:1, source_id:decision.source_id, owner_token:decision.owner_token, decision_id:decision.decision_id};
    let attempted = false;
    try {
      if (current() && await preflight() && current()) {
        const auth = await replyCommand(["authorize", "--stdin-frame"], tuple, true,
          Math.min(deadline, Date.now() + 5000), controller.signal);
        if (auth?.allowed === true && current() && Date.now() < deadline) {
          attempted = true;
          const evidence = await submit(decision, tuple);
          await replyReceipt(tuple, evidence);
          return;
        }
      }
    } catch { /* A result lost after submission stays unknown. */ }
    await replyReceipt(tuple, attempted ? "delivery_unknown" : "not_dispatched");
  };
  const open = async () => {
    const expires = Math.floor(Date.now() / 1000) + 15;
    const first = await replyCommand(["wait", "--source", agent], {
      runtime_version: source.runtime_version, session_id: source.session_id,
      run_id: source.run_id, event_id: source.event_id, instance_id: source.instance_id,
      expires_at: expires, renewable:true,
    }, false, (expires + 5) * 1000, controller.signal);
    if (first?.wake !== true) { await consume(first, expires * 1000); return; }
    if (!current() || !/^[a-f0-9]{64}$/.test(first.source_id) || !validID(first.owner_token)) return;
    disarm = mailbox.arm(first.source_id, async () => {
      if (!current() || !await preflight() || !current()) return;
      const deadline = Date.now() + 60000;
      const result = await replyCommand(["reopen","--stdin-frame"],
        {v:1,source_id:first.source_id,owner_token:first.owner_token}, true, deadline, controller.signal);
      if (result?.wake !== true) await consume(result, deadline);
    });
  };
  void open().catch(() => {});
  return () => { controller.abort(); disarm?.(); };
}
