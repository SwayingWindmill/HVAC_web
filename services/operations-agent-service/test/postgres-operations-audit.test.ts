import assert from 'node:assert/strict';
import test from 'node:test';

import { Pool } from 'pg';

import {
  createOperationsAuditEvent,
  operationsAuditEventId,
  InvestigationRepositoryConflictError,
} from '../dist/application/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;

if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const scope = Object.freeze({
  tenantId: 'organization-audit-postgres',
  siteId: 'site-audit-postgres',
  assetId: null,
  deviceId: null,
});

const createEvent = () => createOperationsAuditEvent({
  eventId: operationsAuditEventId({
    tenantId: scope.tenantId,
    siteId: scope.siteId,
    investigationId: null,
    runId: null,
    revision: null,
    operation: 'CREATE_INVESTIGATION',
    outcome: 'DENIED',
    discriminator: 'decision-audit-postgres',
  }),
  scope,
  investigationId: null,
  runId: null,
  investigationRevision: null,
  actor: {
    actorType: 'SERVICE',
    actorId: 'operations-agent-service',
    actorIssuer: 'spiffe://hvac.local',
    executingService: 'operations-agent-service',
    executingSpiffeId: 'spiffe://hvac.local/operations-agent-service',
  },
  authorizationDecisionId: 'decision-audit-postgres',
  policyRevision: 'policy-audit-postgres/v1',
  operation: 'CREATE_INVESTIGATION',
  outcome: 'DENIED',
  occurredAt: 10_000,
});

test('PostgreSQL Audit outbox deduplicates content and survives lease, retry, and delivery', async (t) => {
  const persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 1_000,
    now: () => 10_000,
  });
  const pool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await Promise.all([persistence.close(), pool.end()]);
  });

  const auditEvent = createEvent();
  const claimOwn = async (at) => {
    const records = await persistence.auditDeliveryRepository.claim({
      at,
      limit: 100,
      leaseDurationMs: 5_000,
    });
    const own = records.filter(({ event }) => event.eventId === auditEvent.eventId);
    for (const record of records) {
      if (record.event.eventId !== auditEvent.eventId) {
        await persistence.auditDeliveryRepository.markDelivered(record.event.eventId, at);
      }
    }
    return own;
  };
  await persistence.auditRecorder.record(auditEvent);
  await persistence.auditRecorder.record(auditEvent);
  const count = await pool.query(
    `SELECT count(*)::int AS count
     FROM agent_operations.audit_records
     WHERE event_id = $1`,
    [auditEvent.eventId],
  );
  assert.equal(count.rows[0].count, 1);

  await assert.rejects(
    () => persistence.auditRecorder.record({ ...auditEvent, outcome: 'FAILED' }),
    (error) => error instanceof InvestigationRepositoryConflictError
      && error.code === 'RECORD_REFERENCE_CONFLICT',
  );

  const firstClaim = await claimOwn(10_000);
  assert.equal(firstClaim.length, 1);
  assert.deepEqual(firstClaim[0].event, auditEvent);
  assert.equal(firstClaim[0].attemptCount, 1);

  const whileLeased = await claimOwn(14_999);
  assert.deepEqual(whileLeased, []);

  await persistence.auditDeliveryRepository.markFailed({
    eventId: auditEvent.eventId,
    failedAt: 15_000,
    retryAt: 20_000,
    failureClass: 'UNAVAILABLE',
  });
  const beforeRetry = await claimOwn(19_999);
  assert.deepEqual(beforeRetry, []);

  const retryClaim = await claimOwn(20_000);
  assert.equal(retryClaim.length, 1);
  assert.equal(retryClaim[0].attemptCount, 2);
  await persistence.auditDeliveryRepository.markDelivered(auditEvent.eventId, 20_100);

  const afterDelivery = await claimOwn(30_000);
  assert.deepEqual(afterDelivery, []);
  const row = await pool.query(
    `SELECT delivery_status, attempt_count, delivered_at_ms, last_failure_class
     FROM agent_operations.audit_records
     WHERE event_id = $1`,
    [auditEvent.eventId],
  );
  assert.deepEqual(row.rows[0], {
    delivery_status: 'DELIVERED',
    attempt_count: 2,
    delivered_at_ms: '20100',
    last_failure_class: null,
  });
});
