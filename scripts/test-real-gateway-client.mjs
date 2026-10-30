import assert from 'node:assert/strict';
import test from 'node:test';
import {
  failureMessage, gatewayPath, gatewayRequest, GatewayRequestError, publishedValueOf, ROUTE_MISSING_MESSAGE, valueOf,
} from '../apps/hvac-web/src/api/gateway-client.ts';

const problem = (status, code, extra = {}) => new Response(JSON.stringify({ status, code, title: 't', detail: `detail ${code}`, ...extra }), {
  status, headers: { 'Content-Type': 'application/problem+json' },
});
const answer = (response) => async () => (typeof response === 'function' ? response() : response);

test('a read sends the session cookie and asks for problem+json, with no CSRF token', async () => {
  let seen;
  await gatewayRequest('/api/v1/x', { fetch: async (path, init) => { seen = { path, init }; return new Response('{"a":1}', { status: 200 }); } });
  assert.equal(seen.init.method, 'GET');
  assert.equal(seen.init.credentials, 'same-origin');
  assert.equal(seen.init.headers.Accept, 'application/json, application/problem+json');
  assert.equal(seen.init.headers['X-CSRF-Token'], undefined);
  assert.equal(seen.init.body, undefined);
});

test('a write carries the CSRF token and a JSON body', async () => {
  let seen;
  const result = await gatewayRequest('/api/v1/runs', {
    method: 'POST', csrfToken: 'tok', body: { siteId: 's' },
    fetch: async (path, init) => { seen = init; return new Response('{"optimizationRunId":"r"}', { status: 202 }); },
    parse: (payload) => payload.optimizationRunId,
  });
  assert.deepEqual(result, { kind: 'ok', value: 'r' });
  assert.equal(seen.headers['X-CSRF-Token'], 'tok');
  assert.equal(seen.headers['Content-Type'], 'application/json');
  assert.equal(seen.body, '{"siteId":"s"}');
});

test('a missing result and a missing route are different outcomes', async () => {
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'RESOURCE_NOT_FOUND')) })).kind, 'notPublished');
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'ROUTE_NOT_FOUND')) })).kind, 'routeMissing');
  // A proxy or static host 404 carries no problem: the route is not served here.
  assert.equal((await gatewayRequest('/x', { fetch: answer(new Response('404 page not found', { status: 404 })) })).kind, 'routeMissing');
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'SITE_NOT_FOUND')) })).kind, 'refused');
});

test('declines are refused with their problem; outages and bad bodies are unavailable', async () => {
  const conflict = await gatewayRequest('/x', { fetch: answer(problem(409, 'CONFLICT')) });
  assert.equal(conflict.kind, 'refused');
  assert.equal(conflict.problem.code, 'CONFLICT');
  const rejected = await gatewayRequest('/x', { fetch: answer(problem(502, 'INTELLIGENCE_UPSTREAM_REJECTED', { retryable: true })) });
  assert.deepEqual([rejected.kind, rejected.problem.retryable], ['refused', true]);
  assert.deepEqual(await gatewayRequest('/x', { fetch: answer(problem(502, 'INTELLIGENCE_UPSTREAM_UNAVAILABLE')) }), { kind: 'unavailable', status: 502 });
  assert.deepEqual(await gatewayRequest('/x', { fetch: answer(new Response('<html>', { status: 200 })) }), { kind: 'unavailable', status: 200 });
  assert.deepEqual(await gatewayRequest('/x', { fetch: async () => { throw new TypeError('network'); } }), { kind: 'unavailable', status: null });
  assert.deepEqual(await gatewayRequest('/x', { fetch: answer(new Response('{}', { status: 200 })), parse: () => { throw new Error('shape'); } }), { kind: 'unavailable', status: 200 });
});

test('a cancelled request rethrows instead of reading as an outage', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(gatewayRequest('/x', { signal: controller.signal, fetch: async () => { throw new DOMException('aborted', 'AbortError'); } }), { name: 'AbortError' });
});

test('values and messages: nothing yet is null, a missing route says so, a refusal keeps its own sentence', async () => {
  const messages = { unavailable: '服务暂时不可用', refused: (p) => (p.retryable ? '稍后再试' : '无法处理') };
  assert.equal(publishedValueOf({ kind: 'notPublished', problem: { status: 404, code: 'RESOURCE_NOT_FOUND' } }, messages), null);
  assert.equal(publishedValueOf({ kind: 'ok', value: 3 }, messages), 3);
  assert.throws(() => valueOf({ kind: 'notPublished', problem: { status: 404 } }, messages), { message: '服务暂时不可用' });
  assert.throws(() => publishedValueOf({ kind: 'routeMissing', status: 404 }, messages), (error) => error instanceof GatewayRequestError && error.message === ROUTE_MISSING_MESSAGE && error.failure.kind === 'routeMissing');
  assert.equal(failureMessage({ kind: 'refused', problem: { status: 502, retryable: true } }, messages), '稍后再试');
  assert.equal(failureMessage({ kind: 'refused', problem: { status: 409, detail: 'd' } }, { unavailable: 'u' }), 'd');
  assert.equal(failureMessage({ kind: 'unavailable', status: null }, messages), '服务暂时不可用');
});

test('path templates are filled and encoded, and a missing value is a bug', () => {
  assert.equal(gatewayPath('/api/v1/sites/{siteId}/reports/monthly/{month}', { siteId: 'a b', month: '2026-10' }), '/api/v1/sites/a%20b/reports/monthly/2026-10');
  assert.throws(() => gatewayPath('/api/v1/sites/{siteId}', {}));
});
