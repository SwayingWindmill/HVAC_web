import assert from 'node:assert/strict';
import test from 'node:test';

import { HVAC_AGENT_EVENT_VERSION } from '../dist/agent/index.js';
import { createAgentSessionEventStreamResponse } from '../dist/transport-http/index.js';

const context = Object.freeze({
  tenantId: 'tenant-1',
  siteId: 'site-1',
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read']),
  correlationId: 'correlation-1',
});

const completedSnapshot = Object.freeze({
  session: Object.freeze({
    id: 'session-1',
    tenantId: context.tenantId,
    siteId: context.siteId,
    agentDefinitionId: 'operations-investigation.v1',
    createdBy: context.principalId,
    revision: 2,
    createdAt: 1_000,
    updatedAt: 1_100,
    status: 'COMPLETED',
    activeRunId: null,
  }),
  runs: Object.freeze([]),
  messages: Object.freeze([]),
  toolExecutions: Object.freeze([]),
  artifacts: Object.freeze([]),
});

test('direct Agent Session SSE emits hvac.agent.event/v1 and closes after terminal durable snapshot', async () => {
  let subscribeCalls = 0;
  const service = Object.freeze({
    async subscribe(receivedContext, sessionId, listener) {
      subscribeCalls += 1;
      assert.deepEqual(receivedContext, context);
      assert.equal(sessionId, 'session-1');
      listener(Object.freeze({
        version: HVAC_AGENT_EVENT_VERSION,
        type: 'session.snapshot',
        sessionId,
        runId: null,
        sequence: 0,
        at: 1_100,
        payload: Object.freeze({ snapshot: completedSnapshot }),
      }));
      return () => undefined;
    },
  });

  const response = await createAgentSessionEventStreamResponse(service, context, 'session-1');
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('content-type'), 'text/event-stream; charset=utf-8');
  assert.equal(response.headers.get('cache-control'), 'no-store, no-transform');
  assert.equal(await response.text(), [
    'event: session.snapshot',
    `data: ${JSON.stringify({
      version: HVAC_AGENT_EVENT_VERSION,
      type: 'session.snapshot',
      sessionId: 'session-1',
      runId: null,
      sequence: 0,
      at: 1_100,
      payload: { snapshot: completedSnapshot },
    })}`,
    '',
    '',
  ].join('\n'));
  assert.equal(subscribeCalls, 1);
});
