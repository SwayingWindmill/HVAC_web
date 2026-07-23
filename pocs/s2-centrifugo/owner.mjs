import { createServer } from 'node:http';

const port = Number(process.env.PORT ?? 18081);
const primaryChannel = 's2:org-a:site-a:device-1';
const loadChannel = 's2:org-a:site-a:device-load';
const permissions = new Map([
  ['s2-user', new Set([primaryChannel, loadChannel])],
]);
const snapshots = new Map([
  [primaryChannel, { revision: 1, deviceId: 'device-1', values: { supplyTemp: 18.5 } }],
  [loadChannel, { revision: 1, deviceId: 'device-load', values: { load: 0 } }],
]);
const events = [];

function json(response, status, body) {
  const payload = JSON.stringify(body);
  response.writeHead(status, {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(payload),
  });
  response.end(payload);
}

async function body(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  if (chunks.length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function record(type, detail = {}) {
  events.push({ type, at: new Date().toISOString(), ...detail });
  if (events.length > 500) events.shift();
}

function hasPermission(user, channel) {
  return permissions.get(user)?.has(channel) === true;
}

const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url ?? '/', `http://${request.headers.host ?? 'localhost'}`);

    if (request.method === 'GET' && url.pathname === '/health') {
      return json(response, 200, { status: 'ok' });
    }

    if (request.method === 'POST' && url.pathname === '/centrifugo/subscribe') {
      const input = await body(request);
      const user = String(input.user ?? '');
      const channel = String(input.channel ?? '');
      const allowed = hasPermission(user, channel);
      record('subscribe-decision', { user, channel, allowed });
      if (!allowed) {
        return json(response, 200, {
          error: { code: 403, message: 'scope denied' },
        });
      }
      return json(response, 200, {
        result: {
          data: {
            authorizationSource: 'platform-owner',
            scopeRevision: 1,
          },
        },
      });
    }

    if (request.method === 'GET' && url.pathname === '/snapshot') {
      const channel = url.searchParams.get('channel') ?? '';
      const delayMs = Math.min(2_000, Math.max(0, Number(url.searchParams.get('delayMs') ?? 0)));
      const snapshot = snapshots.get(channel);
      if (!snapshot) return json(response, 404, { code: 'RESOURCE_NOT_FOUND' });
      const captured = structuredClone(snapshot);
      record('snapshot-captured', { channel, revision: captured.revision, delayMs });
      if (delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
      return json(response, 200, {
        channel,
        evaluatedAt: new Date().toISOString(),
        snapshot: captured,
      });
    }

    if (request.method === 'POST' && url.pathname === '/control/state') {
      const input = await body(request);
      const channel = String(input.channel ?? '');
      const revision = Number(input.revision);
      const current = snapshots.get(channel);
      if (!current) return json(response, 404, { code: 'RESOURCE_NOT_FOUND' });
      if (!Number.isSafeInteger(revision) || revision <= current.revision) {
        return json(response, 409, { code: 'REVISION_NOT_MONOTONIC', currentRevision: current.revision });
      }
      const next = {
        revision,
        deviceId: current.deviceId,
        values: input.values && typeof input.values === 'object' ? input.values : current.values,
      };
      snapshots.set(channel, next);
      record('state-committed', { channel, revision });
      return json(response, 200, { status: 'ok', snapshot: next });
    }

    if (request.method === 'POST' && url.pathname === '/control/revoke') {
      const input = await body(request);
      const user = String(input.user ?? '');
      const channel = String(input.channel ?? '');
      permissions.get(user)?.delete(channel);
      record('permission-revoked', { user, channel });
      return json(response, 200, { status: 'ok' });
    }

    if (request.method === 'POST' && url.pathname === '/control/grant') {
      const input = await body(request);
      const user = String(input.user ?? '');
      const channel = String(input.channel ?? '');
      if (!permissions.has(user)) permissions.set(user, new Set());
      permissions.get(user).add(channel);
      record('permission-granted', { user, channel });
      return json(response, 200, { status: 'ok' });
    }

    if (request.method === 'GET' && url.pathname === '/control/events') {
      return json(response, 200, { events });
    }

    return json(response, 404, { code: 'NOT_FOUND' });
  } catch (error) {
    record('owner-error', { message: error instanceof Error ? error.message : String(error) });
    return json(response, 500, { code: 'INTERNAL_ERROR' });
  }
});

server.listen(port, '0.0.0.0', () => {
  console.log(JSON.stringify({ component: 's2-realtime-owner-poc', status: 'ready', port }));
});
