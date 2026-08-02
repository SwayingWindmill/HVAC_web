import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createOperationsAuditEvent,
  operationsAuditEventId,
  parseOperationsAuditEvent,
} from '../dist/application/index.js';
import {
  createOperationsAuditDeliveryWorker,
  createOperationsAuditHttpClient,
} from '../dist/scheduling/index.js';

const scope = Object.freeze({
  organizationId: 'organization-001',
  siteId: 'site-001',
  equipmentId: null,
  deviceId: null,
});

const actor = Object.freeze({
  actorType: 'OPERATOR',
  actorId: 'operator-001',
  actorIssuer: 'https://issuer.example.test',
  executingService: 'operations-agent-service',
  executingSpiffeId: 'spiffe://hvac.local/operations-agent-service',
});

const event = () => createOperationsAuditEvent({
  eventId: operationsAuditEventId({
    organizationId: scope.organizationId,
    siteId: scope.siteId,
    investigationId: 'investigation-001',
    runId: 'run-001',
    revision: 7,
    operation: 'COMMIT_EFFECT',
    outcome: 'SUCCEEDED',
    discriminator: 'evidence-001',
  }),
  scope,
  investigationId: 'investigation-001',
  runId: 'run-001',
  investigationRevision: 7,
  actor,
  authorizationDecisionId: 'decision-001',
  policyRevision: 'policy-v17',
  operation: 'COMMIT_EFFECT',
  outcome: 'SUCCEEDED',
  occurredAt: 1_785_600_000_000,
  recordReferences: [{ recordType: 'EVIDENCE', recordId: 'evidence-001' }],
});

test('Operations Audit event is strict, versioned, idempotent and content-free', () => {
  const first = event();
  const second = event();
  assert.deepEqual(first, second);
  assert.equal(first.schemaVersion, 1);
  assert.equal(first.messageType, 'hvac.operations.audit.v1');
  assert.equal(first.action, first.operation);
  assert.equal(first.actor.actorId, 'operator-001');
  assert.deepEqual(first.recordReferences, [{ recordType: 'EVIDENCE', recordId: 'evidence-001' }]);
  const serialized = JSON.stringify(first);
  for (const forbidden of [
    'raw prompt', 'operator note', 'owner payload', 'checkpoint-state', 'lease-secret', 'model completion',
  ]) {
    assert.equal(serialized.includes(forbidden), false);
  }

  for (const invalid of [
    { ...first, prompt: 'raw prompt' },
    { ...first, operatorNote: 'operator note' },
    { ...first, ownerPayload: { secret: true } },
    { ...first, checkpoint: 'checkpoint-state' },
    { ...first, lease: 'lease-secret' },
    { ...first, action: 'FAIL_AGENT_RUN' },
    { ...first, actor: { ...first.actor, displayName: 'Operator Name' } },
    { ...first, recordReferences: [...first.recordReferences, ...first.recordReferences] },
  ]) {
    assert.throws(() => parseOperationsAuditEvent(invalid), /Operations Audit/u);
  }
});

test('Operations Audit denial may precede Investigation creation but cannot reference records', () => {
  const denied = createOperationsAuditEvent({
    eventId: operationsAuditEventId({
      organizationId: scope.organizationId,
      siteId: scope.siteId,
      investigationId: null,
      runId: null,
      revision: null,
      operation: 'CREATE_INVESTIGATION',
      outcome: 'DENIED',
      discriminator: 'decision-deny',
    }),
    scope,
    investigationId: null,
    runId: null,
    investigationRevision: null,
    actor,
    authorizationDecisionId: 'decision-deny',
    policyRevision: 'policy-v17',
    operation: 'CREATE_INVESTIGATION',
    outcome: 'DENIED',
    occurredAt: 1_785_600_000_000,
  });
  assert.equal(denied.investigationId, null);
  assert.deepEqual(denied.recordReferences, []);
  assert.throws(() => parseOperationsAuditEvent({
    ...denied,
    recordReferences: [{ recordType: 'EVIDENCE', recordId: 'evidence-forged' }],
  }), /record references require/u);
});

test('Audit HTTP client sends only bounded event identity and exact JSON', async () => {
  const calls = [];
  const client = createOperationsAuditHttpClient({
    endpoint: 'https://audit-ledger.internal',
    fetchImplementation: async (url, init) => {
      calls.push({ url, init });
      return new Response(null, { status: 204 });
    },
  });
  const auditEvent = event();
  await client.deliver(auditEvent);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'https://audit-ledger.internal/internal/v1/audit/operations-events');
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].init.headers['Idempotency-Key'], auditEvent.eventId);
  assert.equal(calls[0].init.headers.Authorization, undefined);
  assert.equal(calls[0].init.headers.Cookie, undefined);
  assert.deepEqual(JSON.parse(calls[0].init.body), auditEvent);
  assert.throws(
    () => createOperationsAuditHttpClient({ endpoint: 'https://user:secret@audit-ledger.internal' }),
    /without credentials/u,
  );
});

test('Audit delivery worker marks success and isolates retryable remote failure', async () => {
  const auditEvent = event();
  const delivered = [];
  const failed = [];
  let claimed = 0;
  const repository = {
    async claim() {
      claimed += 1;
      return [{ event: auditEvent, attemptCount: claimed, nextAttemptAt: 1_000 }];
    },
    async markDelivered(eventId, deliveredAt) {
      delivered.push({ eventId, deliveredAt });
    },
    async markFailed(input) {
      failed.push(input);
    },
  };
  let now = 2_000;
  const successWorker = createOperationsAuditDeliveryWorker({
    repository,
    client: { async deliver() {} },
    now: () => now,
    retryBaseMs: 1_000,
    retryMaximumMs: 10_000,
  });
  assert.deepEqual(await successWorker.runOnce(), { claimed: 1, delivered: 1, failed: 0 });
  assert.deepEqual(delivered, [{ eventId: auditEvent.eventId, deliveredAt: 2_000 }]);

  now = 3_000;
  const failureClient = createOperationsAuditHttpClient({
    endpoint: 'https://audit-ledger.internal',
    fetchImplementation: async () => new Response(null, { status: 503 }),
  });
  const failureWorker = createOperationsAuditDeliveryWorker({
    repository,
    client: failureClient,
    now: () => now,
    retryBaseMs: 1_000,
    retryMaximumMs: 10_000,
  });
  assert.deepEqual(await failureWorker.runOnce(), { claimed: 1, delivered: 0, failed: 1 });
  assert.deepEqual(failed, [{
    eventId: auditEvent.eventId,
    failedAt: 3_000,
    retryAt: 5_000,
    failureClass: 'UNAVAILABLE',
  }]);
});
