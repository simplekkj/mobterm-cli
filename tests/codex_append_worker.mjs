// Run the sibling API's Worker and migrated local D1 on loopback TLS.
// Its existing harness intercepts outbound APNs.
import { createServer } from 'node:https';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [api, state] = process.argv.slice(2);
process.chdir(api);
const { harness } = await import(pathToFileURL(resolve('tests/helpers.mjs')));
let dispose;
const worker = await harness({ after(fn) { dispose = fn; } });
const ids = await worker.setup();

async function management(path, method, body) {
  const result = await worker.api(path, ids.owner, method, body);
  if (result.status !== 200) throw Error('Local Worker setup failed: ' + result.status);
  return result.body;
}

await management(`/v2/devices/${ids.device}/capabilities`, 'PUT', {
  v: 2, enabled: true, capability_generation: 0,
});
const route = `/v2/pairs/${ids.pair}`;
const caps = await management(route + '/interaction-capabilities', 'GET');
caps.interactions.codex.followupMessage = true;
await management(route + '/interaction-policy', 'PATCH', {
  v: 1, policy_generation: caps.policy_generation, interactions: caps.interactions,
});

const tls = { key: readFileSync(join(state, 'tls.key')), cert: readFileSync(join(state, 'tls.pem')) };
const server = createServer(tls, async (req, res) => {
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const raw = Buffer.concat(chunks);
    const response = await worker.mf.dispatchFetch('http://localhost' + req.url, {
      method: req.method, headers: req.headers, body: raw.length ? raw : undefined,
    });
    if (response.ok && req.method === 'POST' && req.url === route + '/events') {
      const envelope = JSON.parse(raw);
      if (/^[0-9a-f]{64}$/.test(envelope.event_id)) {
        writeFileSync(join(state, envelope.event_id + '.delivery'), raw, { mode: 0o600 });
      }
    }
    res.writeHead(response.status, { 'Content-Type': 'application/json' });
    res.end(Buffer.from(await response.arrayBuffer()));
  } catch {
    res.writeHead(500);
    res.end('{}');
  }
});

server.listen(0, '127.0.0.1', () => {
  const origin = 'https://127.0.0.1:' + server.address().port;
  writeFileSync(join(state, 'worker.json'), JSON.stringify({ ...ids, origin }), { mode: 0o600 });
});
process.on('SIGTERM', async () => {
  server.closeAllConnections();
  server.close();
  await dispose();
});
