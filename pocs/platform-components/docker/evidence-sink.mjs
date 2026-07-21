import { createServer } from 'node:http';

const port = Number(process.env.PORT ?? 18080);
const events = [];

function writeJson(response, status, body) {
  response.writeHead(status, { 'content-type': 'application/json' });
  response.end(`${JSON.stringify(body)}\n`);
}

async function readBody(request) {
  const chunks = [];
  let bytes = 0;
  for await (const chunk of request) {
    bytes += chunk.length;
    if (bytes > 1024 * 1024) throw new Error('request body exceeds 1 MiB');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

const server = createServer(async (request, response) => {
  try {
    if (request.method === 'GET' && request.url === '/health') {
      return writeJson(response, 200, { status: 'ok' });
    }
    if (request.method === 'GET' && request.url === '/events') {
      return writeJson(response, 200, { count: events.length, events });
    }
    if (request.method === 'DELETE' && request.url === '/events') {
      events.splice(0, events.length);
      return writeJson(response, 200, { count: 0 });
    }
    if (request.method === 'POST' && request.url === '/events') {
      const body = await readBody(request);
      const value = JSON.parse(body);
      const received = Array.isArray(value) ? value : [value];
      for (const item of received) {
        events.push({ receivedAt: new Date().toISOString(), value: item });
      }
      return writeJson(response, 202, { accepted: received.length, total: events.length });
    }
    return writeJson(response, 404, { status: 'not-found' });
  } catch (error) {
    return writeJson(response, 400, { status: 'invalid', detail: error instanceof Error ? error.message : String(error) });
  }
});

server.listen(port, '0.0.0.0', () => {
  console.log(JSON.stringify({ event: 'evidence_sink_ready', port }));
});
