import assert from 'node:assert/strict';
import test from 'node:test';
import {
  assistantErrorText,
  AssistantRequestError,
  listAssistantSessions,
} from '../apps/hvac-web/src/api/agent-sessions.ts';
import {
  failureMessage,
  failureStatus,
  gatewayPath,
  gatewayRequest,
  GatewayRequestError,
  publishedValueOf,
  ROUTE_MISSING_MESSAGE,
  valueOf,
  type FailureMessages,
} from '../apps/hvac-web/src/api/gateway-client.ts';
import {
  listWorkOrders,
  WorkOrderApiError,
} from '../apps/hvac-web/src/api/work-orders.ts';

const problem = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ status, code, title: 't', detail: `detail ${code}`, ...extra }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });

const answer = (response: Response | (() => Response)) => async () =>
  (typeof response === 'function' ? response() : response);

const withStubbedFetch = async (stub: () => Promise<Response>, run: () => Promise<void>): Promise<void> => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = Object.assign(stub, originalFetch);
  try {
    await run();
  } finally {
    globalThis.fetch = originalFetch;
  }
};

test('a read sends the session cookie and asks for problem+json, with no CSRF token', async () => {
  let seen: { path: string | URL | Request; init?: RequestInit } | undefined;
  await gatewayRequest('/api/v1/x', {
    fetch: async (path, init) => {
      seen = { path, init };
      return new Response('{"a":1}', { status: 200 });
    },
  });
  assert.equal(seen?.init?.method, 'GET');
  assert.equal(seen?.init?.credentials, 'same-origin');
  const headers = seen?.init?.headers as Record<string, string>;
  assert.equal(headers.Accept, 'application/json, application/problem+json');
  assert.equal(headers['X-CSRF-Token'], undefined);
  assert.equal(seen?.init?.body, undefined);
});

test('a write carries the CSRF token, optional idempotency key, and a JSON body', async () => {
  let seen: RequestInit | undefined;
  const result = await gatewayRequest('/api/v1/runs', {
    method: 'POST',
    csrfToken: 'tok',
    idempotencyKey: 'idem-1',
    body: { siteId: 's' },
    fetch: async (_path, init) => {
      seen = init;
      return new Response('{"optimizationRunId":"r"}', { status: 202 });
    },
    parse: (payload) => (payload as { optimizationRunId: string }).optimizationRunId,
  });
  assert.deepEqual(result, { kind: 'ok', value: 'r' });
  const headers = seen?.headers as Record<string, string>;
  assert.equal(headers['X-CSRF-Token'], 'tok');
  assert.equal(headers['Idempotency-Key'], 'idem-1');
  assert.equal(headers['Content-Type'], 'application/json');
  assert.equal(seen?.body, '{"siteId":"s"}');
});

test('nested error envelopes are parsed as problems and failureStatus extracts status codes', async () => {
  const denied = await gatewayRequest('/api/v1/alarms', {
    fetch: async () => new Response(JSON.stringify({ error: { code: 'FORBIDDEN', message: 'Alarm list access is forbidden.' } }), { status: 403 }),
  });
  assert.equal(denied.kind, 'refused');
  assert.equal(denied.problem.code, 'FORBIDDEN');
  assert.equal(denied.problem.detail, 'Alarm list access is forbidden.');
  assert.equal(failureStatus(denied), 403);
  assert.equal(failureStatus({ kind: 'unavailable', status: null }), 503);
});

test('a missing result and a missing route are different outcomes', async () => {
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'RESOURCE_NOT_FOUND')) })).kind, 'notPublished');
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'ROUTE_NOT_FOUND')) })).kind, 'routeMissing');
  // A proxy or static host 404 carries no problem: the route is not served here.
  assert.equal((await gatewayRequest('/x', { fetch: answer(new Response('404 page not found', { status: 404 })) })).kind, 'routeMissing');
  assert.equal((await gatewayRequest('/x', { fetch: answer(problem(404, 'SITE_NOT_FOUND')) })).kind, 'refused');
});

test('declines are refused with their problem; outages preserve optional problem and bad bodies are unavailable', async () => {
  const conflict = await gatewayRequest('/x', { fetch: answer(problem(409, 'CONFLICT')) });
  assert.equal(conflict.kind, 'refused');
  assert.equal(conflict.problem.code, 'CONFLICT');
  const rejected = await gatewayRequest('/x', { fetch: answer(problem(502, 'INTELLIGENCE_UPSTREAM_REJECTED', { retryable: true })) });
  assert.equal(rejected.kind, 'refused');
  assert.equal(rejected.problem.retryable, true);
  const stepUpNotConfigured = await gatewayRequest('/x', { fetch: answer(problem(428, 'STEP_UP_REQUIRED', { reason: 'MFA_NOT_CONFIGURED' })) });
  assert.equal(stepUpNotConfigured.kind, 'refused');
  assert.equal(stepUpNotConfigured.problem.reason, 'MFA_NOT_CONFIGURED');
  const unknownReason = await gatewayRequest('/x', { fetch: answer(problem(428, 'STEP_UP_REQUIRED', { reason: 'OTHER' })) });
  assert.equal(unknownReason.kind, 'refused');
  assert.equal(unknownReason.problem.reason, undefined);
  assert.deepEqual(
    await gatewayRequest('/x', { fetch: answer(problem(502, 'INTELLIGENCE_UPSTREAM_UNAVAILABLE')) }),
    {
      kind: 'unavailable',
      status: 502,
      problem: {
        status: 502,
        code: 'INTELLIGENCE_UPSTREAM_UNAVAILABLE',
        reason: undefined,
        title: 't',
        detail: 'detail INTELLIGENCE_UPSTREAM_UNAVAILABLE',
        retryable: undefined,
      },
    },
  );
  assert.deepEqual(await gatewayRequest('/x', { fetch: answer(new Response('<html>', { status: 200 })) }), { kind: 'unavailable', status: 200 });
  assert.deepEqual(await gatewayRequest('/x', { fetch: async () => { throw new TypeError('network'); } }), { kind: 'unavailable', status: null });
  assert.deepEqual(await gatewayRequest('/x', { fetch: answer(new Response('{}', { status: 200 })), parse: () => { throw new Error('shape'); } }), { kind: 'unavailable', status: 200 });
});

test('a cancelled request rethrows instead of reading as an outage', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    gatewayRequest('/x', {
      signal: controller.signal,
      fetch: async () => {
        throw new DOMException('aborted', 'AbortError');
      },
    }),
    { name: 'AbortError' },
  );
});

test('values and messages: nothing yet is null, a missing route says so, a refusal keeps its own sentence', () => {
  const messages: FailureMessages = {
    unavailable: '服务暂时不可用',
    refused: (p) => (p.retryable ? '稍后再试' : '无法处理'),
  };
  assert.equal(publishedValueOf({ kind: 'notPublished', problem: { status: 404, code: 'RESOURCE_NOT_FOUND' } }, messages), null);
  assert.equal(publishedValueOf({ kind: 'ok', value: 3 }, messages), 3);
  assert.throws(() => valueOf({ kind: 'notPublished', problem: { status: 404 } }, messages), { message: '服务暂时不可用' });
  assert.throws(
    () => publishedValueOf({ kind: 'routeMissing', status: 404 }, messages),
    (error: unknown) => error instanceof GatewayRequestError && error.message === ROUTE_MISSING_MESSAGE && error.failure.kind === 'routeMissing',
  );
  assert.equal(failureMessage({ kind: 'refused', problem: { status: 502, retryable: true } }, messages), '稍后再试');
  const refused = (status: number, detail: string) =>
    failureMessage({ kind: 'refused', problem: { status, detail } }, { unavailable: 'u' });
  assert.equal(refused(409, '该名称已被使用'), '该名称已被使用');
  assert.equal(refused(409, 'Conflict'), '数据刚刚有更新，请刷新后再试');
  assert.equal(refused(401, 'Sign in'), '登录已失效，请重新登录');
  assert.equal(refused(422, 'Invalid input'), 'u');
  assert.equal(failureMessage({ kind: 'unavailable', status: null }, messages), '服务暂时不可用');
});

test('path templates are filled and encoded, and a missing value is a bug', () => {
  assert.equal(gatewayPath('/api/v1/sites/{siteId}/reports/monthly/{month}', { siteId: 'a b', month: '2026-10' }), '/api/v1/sites/a%20b/reports/monthly/2026-10');
  assert.throws(() => gatewayPath('/api/v1/sites/{siteId}', {}));
});

test('agent-sessions preserves 503 OPERATIONS_AGENT_UNAVAILABLE problem code for assistantErrorText', async () => {
  await withStubbedFetch(
    async () => problem(503, 'OPERATIONS_AGENT_UNAVAILABLE', { retryable: false }),
    async () => {
      await assert.rejects(
        listAssistantSessions('018f3e00-1000-7000-8000-000000000001'),
        (error: unknown) => {
          assert.ok(error instanceof AssistantRequestError);
          assert.equal(error.code, 'OPERATIONS_AGENT_UNAVAILABLE');
          assert.equal(error.status, 503);
          assert.equal(assistantErrorText(error), '这个环境还没有接入 AI 助手。');
          return true;
        },
      );
    },
  );
});

test('work-orders preserves 503 retryable/detail and distinguishes 404 routeMissing', async () => {
  await withStubbedFetch(
    async () => new Response(JSON.stringify({
      status: 503,
      code: 'WORK_ORDER_UNAVAILABLE',
      detail: '工单队列正在维护，请稍后重试。',
      retryable: true,
    }), { status: 503, headers: { 'Content-Type': 'application/problem+json' } }),
    async () => {
      await assert.rejects(
        listWorkOrders({}, { siteId: '018f3e00-1000-7000-8000-000000000001' }),
        (error: unknown) => {
          assert.ok(error instanceof WorkOrderApiError);
          assert.equal(error.status, 503);
          assert.equal(error.code, 'WORK_ORDER_UNAVAILABLE');
          assert.equal(error.retryable, true);
          assert.equal(error.message, '工单队列正在维护，请稍后重试。');
          return true;
        },
      );
    },
  );

  await withStubbedFetch(
    async () => problem(404, 'ROUTE_NOT_FOUND'),
    async () => {
      await assert.rejects(
        listWorkOrders({}, { siteId: '018f3e00-1000-7000-8000-000000000001' }),
        (error: unknown) => {
          assert.ok(error instanceof WorkOrderApiError);
          assert.equal(error.status, 404);
          assert.equal(error.code, 'ROUTE_NOT_FOUND');
          assert.equal(error.retryable, false);
          assert.equal(error.message, ROUTE_MISSING_MESSAGE);
          return true;
        },
      );
    },
  );
});
