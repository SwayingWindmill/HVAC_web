import assert from 'node:assert/strict';
import test from 'node:test';
import { createGateway, type ChunkedTransport, type Transport } from '../miniprogram/lib/gateway.ts';

/** For tests that make no streamed request. */
const noStreams: ChunkedTransport = () => { throw new Error('not used'); };

const memorySession = (token: string | null) => {
  let current = token;
  return { get: () => current, set: (value: string) => { current = value; }, clear: () => { current = null; } };
};

test('every request carries the Session in the Authorization header and nothing a browser would send', async () => {
  const seen: Parameters<Transport>[0][] = [];
  const transport: Transport = async (request) => { seen.push(request); return { statusCode: 200, data: { ok: true } }; };
  const gateway = createGateway({ origin: 'https://hvac.example.test', transport, chunked: noStreams, session: memorySession('session-1'), onSignedOut: () => {} });
  assert.deepEqual(await gateway.get('/api/v1/principal'), { ok: true });
  assert.equal(seen[0].url, 'https://hvac.example.test/api/v1/principal');
  assert.equal(seen[0].header.Authorization, 'Bearer session-1');
  assert.equal('Cookie' in seen[0].header || 'X-CSRF-Token' in seen[0].header, false);
});

test('a Session the platform no longer accepts signs the mini program out once and says so', async () => {
  let signedOut = 0;
  const session = memorySession('session-1');
  const transport: Transport = async () => ({ statusCode: 401, data: { code: 'SESSION_INVALID' } });
  const gateway = createGateway({ origin: 'https://hvac.example.test', transport, chunked: noStreams, session, onSignedOut: () => { signedOut += 1; } });
  await assert.rejects(gateway.get('/api/v1/principal'), { code: 'SESSION_INVALID' });
  assert.equal(session.get(), null);
  assert.equal(signedOut, 1);
});

test('a refused request reports the platform problem code, a network failure says it could not reach the platform', async () => {
  const refused = createGateway({ origin: 'https://hvac.example.test', chunked: noStreams, session: memorySession('s'), onSignedOut: () => {},
    transport: async () => ({ statusCode: 503, data: { code: 'OWNER_UNAVAILABLE' } }) });
  await assert.rejects(refused.get('/api/v1/alarms'), { code: 'OWNER_UNAVAILABLE', status: 503 });
  const offline = createGateway({ origin: 'https://hvac.example.test', chunked: noStreams, session: memorySession('s'), onSignedOut: () => {},
    transport: async () => { throw new Error('request:fail timeout'); } });
  await assert.rejects(offline.get('/api/v1/alarms'), { code: 'NETWORK_UNAVAILABLE' });
});

test('signing in keeps the Session, or asks for the one-time code, or explains in plain words why not', async () => {
  const answers: Record<string, { statusCode: number; data: unknown }> = {
    right: { statusCode: 201, data: { sessionToken: 'new-session', expiresAt: '2026-10-21T08:00:00Z' } },
    totp: { statusCode: 202, data: { step: 'totp', pendingLogin: 'pending-1' } },
    wrong: { statusCode: 401, data: { code: 'INVALID_CREDENTIALS' } },
  };
  for (const [password, expected] of [['right', { kind: 'signed-in' }], ['totp', { kind: 'totp', pendingLogin: 'pending-1' }], ['wrong', { kind: 'failed', message: '账号或密码不对。连续输错 5 次，账号会锁 15 分钟。' }]] as const) {
    const session = memorySession(null);
    const gateway = createGateway({ origin: 'https://hvac.example.test', chunked: noStreams, session, onSignedOut: () => {},
      transport: async (request) => { assert.equal(request.header.Authorization, undefined); return answers[password]; } });
    assert.deepEqual(await gateway.signIn('zhangsan', password), expected);
    assert.equal(session.get(), password === 'right' ? 'new-session' : null);
  }
});

/** A streamed exchange that answers with a status and these chunks. */
const streamed = (statusCode: number, chunks: readonly string[], seen: Parameters<ChunkedTransport>[0][] = []): ChunkedTransport => (request) => {
  seen.push(request);
  return {
    done: (async () => {
      request.onHeaders(statusCode);
      for (const chunk of chunks) request.onChunk(new TextEncoder().encode(chunk).buffer as ArrayBuffer);
    })(),
    abort: () => {},
  };
};
const noRequests: Transport = async () => { throw new Error('not used'); };

test('a stream carries the Session too and hands over its chunks as they come; a refusal reports its problem code', async () => {
  const seen: Parameters<ChunkedTransport>[0][] = [];
  const received: string[] = [];
  const gateway = createGateway({ origin: 'https://hvac.example.test', transport: noRequests, chunked: streamed(200, ['event: a\n', 'data: {}\n\n'], seen), session: memorySession('session-1'), onSignedOut: () => {} });
  await gateway.stream('/api/v1/sites/s/operations/agent-sessions/a/events', (chunk) => received.push(new TextDecoder().decode(chunk))).done;
  assert.equal(seen[0].header.Authorization, 'Bearer session-1');
  assert.equal(seen[0].header.Accept, 'text/event-stream, application/problem+json');
  assert.deepEqual(received, ['event: a\n', 'data: {}\n\n']);

  const refused = createGateway({ origin: 'https://hvac.example.test', transport: noRequests, chunked: streamed(429, ['{"code":"OPERATIONS_', 'RATE_LIMITED"}']), session: memorySession('s'), onSignedOut: () => {} });
  const nothing: string[] = [];
  await assert.rejects(refused.stream('/events', () => nothing.push('chunk')).done, { code: 'OPERATIONS_RATE_LIMITED', status: 429 });
  assert.deepEqual(nothing, []);

  let signedOut = 0;
  const ended = createGateway({ origin: 'https://hvac.example.test', transport: noRequests, chunked: streamed(401, ['{"code":"SESSION_INVALID"}']), session: memorySession('s'), onSignedOut: () => { signedOut += 1; } });
  await assert.rejects(ended.stream('/events', () => {}).done, { code: 'SESSION_INVALID' });
  assert.equal(signedOut, 1);
});

test('a change without a body sends none, and a change can carry its own headers such as an idempotency key', async () => {
  const seen: Parameters<Transport>[0][] = [];
  const transport: Transport = async (request) => { seen.push(request); return { statusCode: 200, data: { data: {} } }; };
  const gateway = createGateway({ origin: 'https://hvac.example.test', transport, chunked: noStreams, session: memorySession('session-1'), onSignedOut: () => {} });
  await gateway.post('/api/v1/notifications/inbox/item-1/read');
  assert.equal(seen[0].data, undefined);
  assert.equal('Content-Type' in seen[0].header, false);
  await gateway.post('/api/v1/alarms/alarm-1/ack', { comment: '已通知厂家' }, { 'Idempotency-Key': 'alarm-ack-0001' });
  assert.deepEqual([seen[1].header['Idempotency-Key'], seen[1].header['Content-Type'], seen[1].header.Authorization], ['alarm-ack-0001', 'application/json', 'Bearer session-1']);
});
