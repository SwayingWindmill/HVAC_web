import assert from 'node:assert/strict';
import test from 'node:test';

import { createAgentSessionHttpHandler } from '../dist/transport-http/index.js';

const state = Object.freeze({
  session: Object.freeze({
    id: 'session-1',
    tenantId: 'tenant-1',
    siteId: 'site-1',
    agentDefinitionId: 'operations-investigation.v1',
    createdBy: 'principal-authenticated',
    revision: 0,
    createdAt: 1_000,
    updatedAt: 1_001,
    status: 'ACTIVE',
    activeRunId: 'run-1',
  }),
  runs: Object.freeze([]),
  messages: Object.freeze([]),
  toolExecutions: Object.freeze([]),
  artifacts: Object.freeze([]),
});

const authorization = Object.freeze({
  decision: 'ALLOW',
  decisionId: 'decision-1',
  policyRevision: 'policy-1',
  capabilities: Object.freeze(['site.read', 'analytics.energy-series.read']),
  auditActor: Object.freeze({
    actorType: 'OPERATOR',
    actorId: 'principal-authenticated',
    actorIssuer: 'issuer-1',
    executingService: 'operations-agent-service',
    executingSpiffeId: 'spiffe://hvac.local/operations-agent-service',
  }),
});

const headers = Object.freeze({
  'Content-Type': 'application/json',
  'X-Tenant-ID': 'tenant-1',
  'X-Delegation-Grant': 'delegation-1',
  'X-Route-Policy-Revision': 'route-1',
  'X-Request-ID': 'request-1',
});

const createHandler = (
  service,
  eventStream = async () => new Response('event stream'),
  expectedSessionId = undefined,
) => createAgentSessionHttpHandler({
  authorizer: Object.freeze({
    async authorize(input) {
      assert.equal(input.tenantId, 'tenant-1');
      assert.equal(input.siteId, 'site-1');
      assert.equal(input.gatewayDelegationGrant, 'delegation-1');
      if (expectedSessionId !== undefined) assert.equal(input.sessionId, expectedSessionId);
      return authorization;
    },
  }),
  service,
  createEventStreamResponse: eventStream,
});

test('Agent Session create derives principal and capabilities only from authorized context', async () => {
  let receivedContext;
  const service = Object.freeze({
    async create(context, input) {
      receivedContext = context;
      assert.deepEqual(input, { message: 'Investigate this Site.' });
      return state;
    },
    async list() { return []; },
    async get() { return state; },
    async start() { return state; },
    async cancel() { return state; },
    async submitInput() { return state; },
    async subscribe() { return () => undefined; },
  });
  const handler = createHandler(service);
  const response = await handler.handle(new Request(
    'http://operations.internal/internal/v1/sites/site-1/operations/agent-sessions',
    { method: 'POST', headers, body: JSON.stringify({ message: 'Investigate this Site.' }) },
  ));

  assert.equal(response.status, 201);
  assert.equal(receivedContext.principalId, 'principal-authenticated');
  assert.deepEqual(receivedContext.capabilities, ['site.read', 'analytics.energy-series.read']);
  assert.equal(receivedContext.correlationId, 'request-1');
  assert.deepEqual(await response.json(), state);
});

test('Agent Session stream authorizes the durable Session before opening direct SSE', async () => {
  let getCalls = 0;
  let streamCalls = 0;
  const service = Object.freeze({
    async create() { return state; },
    async list() { return []; },
    async get(context, sessionId) {
      getCalls += 1;
      assert.equal(context.principalId, 'principal-authenticated');
      assert.equal(sessionId, 'session-1');
      return state;
    },
    async start() { return state; },
    async cancel() { return state; },
    async submitInput() { return state; },
    async subscribe() { return () => undefined; },
  });
  const handler = createHandler(service, async (receivedService, context, sessionId) => {
    streamCalls += 1;
    assert.equal(receivedService, service);
    assert.equal(context.siteId, 'site-1');
    assert.equal(sessionId, 'session-1');
    return new Response('event: session.snapshot\n\n', {
      headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
    });
  }, 'session-1');
  const response = await handler.handle(new Request(
    'http://operations.internal/internal/v1/sites/site-1/operations/agent-sessions/session-1/events',
    {
      method: 'GET',
      headers: {
        'X-Tenant-ID': 'tenant-1',
        'X-Delegation-Grant': 'delegation-1',
        'X-Route-Policy-Revision': 'route-1',
        'X-Request-ID': 'request-1',
      },
    },
  ));

  assert.equal(response.status, 200);
  assert.match(response.headers.get('content-type'), /^text\/event-stream/u);
  assert.equal(getCalls, 1);
  assert.equal(streamCalls, 1);
});

test('Operator Input request cannot synthesize submittedBy and service receives the authenticated principal', async () => {
  let submitCalls = 0;
  const service = Object.freeze({
    async create() { return state; },
    async list() { return []; },
    async get() { return state; },
    async start() { return state; },
    async cancel() { return state; },
    async submitInput(context, input) {
      submitCalls += 1;
      assert.equal(context.principalId, 'principal-authenticated');
      assert.deepEqual(input, {
        sessionId: 'session-1',
        expectedRevision: 2,
        requestArtifactId: 'request-artifact-1',
        value: 'weekday',
      });
      return state;
    },
    async subscribe() { return () => undefined; },
  });
  const handler = createHandler(service);

  const forged = await handler.handle(new Request(
    'http://operations.internal/internal/v1/sites/site-1/operations/agent-sessions/session-1:submit-input',
    {
      method: 'POST',
      headers,
      body: JSON.stringify({
        expectedRevision: 2,
        requestArtifactId: 'request-artifact-1',
        value: 'weekday',
        submittedBy: 'forged-principal',
      }),
    },
  ));
  assert.equal(forged.status, 400);
  assert.equal(submitCalls, 0);

  const accepted = await handler.handle(new Request(
    'http://operations.internal/internal/v1/sites/site-1/operations/agent-sessions/session-1:submit-input',
    {
      method: 'POST',
      headers,
      body: JSON.stringify({
        expectedRevision: 2,
        requestArtifactId: 'request-artifact-1',
        value: 'weekday',
      }),
    },
  ));
  assert.equal(accepted.status, 202);
  assert.equal(submitCalls, 1);
});
