import assert from 'node:assert/strict';
import test from 'node:test';

import { Pool } from 'pg';

import {
  createInvestigationCoordinator,
  createSiteNightEnergyInvestigationCoordinator,
} from '../dist/index.js';
import { createLangGraphAgentExecutionRuntime } from '../dist/runtime-langgraph/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;
if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const organizationId = '0198f5c0-7c00-7000-8000-000000000101';
const siteId = '0198f5c0-7c00-7000-8000-000000000102';
const investigationId = 'investigation-postgres-night-energy-001';
const runId = 'run-postgres-night-energy-001';
const leaseId = 'lease-postgres-night-energy-001';
const checkpointId = 'checkpoint-postgres-night-energy-001';
const scope = Object.freeze({ organizationId, siteId, equipmentId: null, deviceId: null });
const currentTime = Date.parse('2026-07-31T00:00:00.000Z');

const energySeries = (request, energyPerHour) => {
  const from = Date.parse(request.input.from);
  const to = Date.parse(request.input.to);
  const hours = (to - from) / 3_600_000;
  return {
    schemaVersion: 1,
    points: Array.from({ length: hours }, (_value, index) => ({
      periodStart: new Date(from + index * 3_600_000).toISOString(),
      periodEnd: new Date(from + (index + 1) * 3_600_000).toISOString(),
      energyKWh: energyPerHour,
    })),
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      dataWatermark: new Date(to).toISOString(),
      aggregateWatermark: new Date(to).toISOString(),
      datasetRevision: 'energy-postgres-r17',
      partial: false,
      qualitySummary: { valid: hours, suspect: 0, invalid: 0 },
    },
  };
};

const ownerRead = async ({ request }) => {
  if (request.tool === 'registry.getSite') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-site:17',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site/v1',
      payload: {
        kind: 'SITE',
        site: { id: siteId, owningOrganizationId: organizationId, timezone: 'Asia/Tokyo' },
      },
    };
  }
  if (request.tool === 'registry.listSiteEquipment') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-equipment:29',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site-equipment/v1',
      payload: {
        kind: 'SITE_EQUIPMENT',
        siteId,
        equipment: [{ id: '0198f5c0-7c00-7000-8000-000000000110' }],
      },
    };
  }
  const target = request.requestId.endsWith('energy-target');
  return {
    requestId: request.requestId,
    owner: 'telemetry-query-service',
    scope,
    revision: 'energy-postgres-r17',
    quality: 'GOOD',
    provenance: 'telemetry-query-service:energy-series/v1',
    payload: energySeries(request, target ? 155 : 125),
  };
};

const runtime = () => createLangGraphAgentExecutionRuntime({
  id: 'site-night-energy-program/v1',
  runtimeRevision: 'site-night-energy-investigation/v1',
  steps: [{
    id: 'collect-registry-context',
    plan: {
      batches: [{
        batchId: 'registry-context',
        requests: [{
          requestId: `${investigationId}:registry-site`,
          tool: 'registry.getSite',
          input: { siteId },
        }, {
          requestId: `${investigationId}:registry-equipment`,
          tool: 'registry.listSiteEquipment',
          input: { siteId },
        }],
      }],
    },
  }],
});

const createPorts = (persistence, identities) => ({
  investigationRepository: persistence.investigationRepository,
  businessRecordRepository: persistence.businessRecordRepository,
  investigationTransaction: persistence.investigationTransaction,
  checkpointRepository: persistence.checkpointRepository,
  applicationOutbox: persistence.applicationOutbox,
  auditRecorder: persistence.auditRecorder,
  authorizationDecisionReader: {
    async authorizeScope() {
      return {
        decision: 'ALLOW',
        decisionId: 'postgres-night-energy-allow',
        policyRevision: 'operations-policy-1',
        toolDelegationGrants: {
          'registry.getSite': 'postgres-registry-site-grant',
          'registry.listSiteEquipment': 'postgres-registry-equipment-grant',
          'analytics.getEnergySeries': 'postgres-energy-grant',
        },
      };
    },
  },
  createAgentExecutionRuntime: runtime,
  budgetGuard: { async check() { return { decision: 'ALLOW' }; } },
  ownerReaders: {
    registry: { read: ownerRead },
    currentTelemetry: { async read() { throw new Error('not used'); } },
    energyAnalytics: { read: ownerRead },
    commandCapabilities: { async read() { throw new Error('not used'); } },
  },
  clock: { now: () => currentTime },
  idGenerator: {
    next(kind) {
      const identity = identities[kind]?.shift();
      assert.notEqual(identity, undefined, `No identity configured for ${kind}.`);
      return identity;
    },
  },
  leaseDurationMs: 86_400_000,
});

const journalCounts = async (pool, targetInvestigationId = investigationId) => {
  const [investigation, records, effects, outbox, audit] = await Promise.all([
    pool.query('SELECT revision::int FROM agent_operations.investigations WHERE investigation_id = $1', [targetInvestigationId]),
    pool.query('SELECT count(*)::int AS count FROM agent_operations.investigation_business_records WHERE investigation_id = $1', [targetInvestigationId]),
    pool.query('SELECT count(*)::int AS count FROM agent_operations.investigation_effects WHERE investigation_id = $1', [targetInvestigationId]),
    pool.query('SELECT count(*)::int AS count FROM agent_operations.application_outbox WHERE investigation_id = $1', [targetInvestigationId]),
    pool.query('SELECT count(*)::int AS count FROM agent_operations.audit_records WHERE investigation_id = $1', [targetInvestigationId]),
  ]);
  return {
    revision: investigation.rows[0].revision,
    records: records.rows[0].count,
    effects: effects.rows[0].count,
    outbox: outbox.rows[0].count,
    audit: audit.rows[0].count,
  };
};

test('PostgreSQL resumes a checkpointed night-energy Run without duplicate business effects', async (t) => {
  let persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 86_400_000,
    now: () => currentTime,
  });
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await persistence.close();
    await operationsPool.end();
  });

  const firstPorts = createPorts(persistence, {
    investigation: [investigationId],
    run: [runId],
    lease: [leaseId],
    checkpoint: [checkpointId],
  });
  const application = createSiteNightEnergyInvestigationCoordinator(firstPorts);
  const started = await application.start({ organizationId, siteId });
  const aggregate = await persistence.investigationRepository.get(started.id);
  const internal = aggregate.view();
  const run = internal.runs.find(({ id }) => id === internal.activeRunId);
  const generic = createInvestigationCoordinator({
    ...firstPorts,
    agentExecutionRuntime: runtime(),
  });
  const checkpointed = await generic.advance({
    investigationId: started.id,
    runId: run.id,
    leaseId: run.lease.id,
    expectedRevision: internal.revision,
  });
  assert.equal(checkpointed.results.length, 2);
  assert.notEqual(await persistence.checkpointRepository.load(started.id, run.id), null);
  assert.equal((await journalCounts(operationsPool)).records, 0);

  await persistence.close();
  persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 86_400_000,
    now: () => currentTime,
  });
  const recovered = createSiteNightEnergyInvestigationCoordinator(createPorts(persistence, {}));
  const completed = await recovered.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.outcome, 'SUPPORTED_SITE_FINDING');
  assert.equal(completed.toolReceipts.length, 4);
  assert.equal(completed.evidence.length, 2);
  assert.equal(completed.analysisReferences.length, 1);
  assert.equal(completed.findings.length, 1);
  assert.equal(JSON.stringify(completed).includes('"points"'), false);
  assert.equal(JSON.stringify(completed).includes('synthesis'), false);
  const findingRows = await operationsPool.query(
    `SELECT record_payload
       FROM agent_operations.investigation_business_records
      WHERE investigation_id = $1 AND record_type = 'FINDING'`,
    [investigationId],
  );
  assert.equal(findingRows.rows.length, 1);
  assert.equal(findingRows.rows[0].record_payload.synthesis.source, 'DETERMINISTIC_FALLBACK');
  assert.equal(findingRows.rows[0].record_payload.synthesis.fallbackReason, 'NOT_CONFIGURED');
  assert.equal(findingRows.rows[0].record_payload.synthesis.provider, null);
  const committedCounts = await journalCounts(operationsPool);
  assert.equal(committedCounts.records, 8);
  assert.equal(committedCounts.effects, 8);

  await persistence.close();
  persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 86_400_000,
    now: () => currentTime,
  });
  const restarted = createSiteNightEnergyInvestigationCoordinator(createPorts(persistence, {}));
  assert.deepEqual(await restarted.advance({ investigationId: started.id }), completed);
  assert.deepEqual(await journalCounts(operationsPool), committedCounts);

  await persistence.checkpointRepository.delete(started.id, run.id);
  assert.deepEqual(await restarted.get({ investigationId: started.id }), completed);
  assert.deepEqual(await restarted.list({ organizationId, siteId }), {
    schemaVersion: 1,
    investigations: [{
      schemaVersion: 1,
      id: completed.id,
      scope: completed.scope,
      status: completed.status,
      revision: completed.revision,
      createdAt: completed.createdAt,
      outcome: completed.outcome,
      evidenceCount: completed.evidence.length,
      analysisReferenceCount: completed.analysisReferences.length,
      findingCount: completed.findings.length,
      toolReceiptCount: completed.toolReceipts.length,
      acceptedOperatorInputCount: completed.acceptedOperatorInputs.length,
    }],
  });
  assert.deepEqual(await journalCounts(operationsPool), committedCounts);
});

test('PostgreSQL commits Operator Input atomically and exact retry survives restart', async (t) => {
  const operatorInvestigationId = 'investigation-postgres-operator-input-001';
  const operatorRunId = 'run-postgres-operator-input-001';
  const firstLeaseId = 'lease-postgres-operator-input-001';
  const resumedLeaseId = 'lease-postgres-operator-input-002';
  const requestId = 'operator-input-request-postgres-001';
  const recordId = 'operator-input-record-postgres-001';
  const idempotencyKey = 'operator-input-retry-postgres-001';
  let persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 86_400_000,
    now: () => currentTime,
  });
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await persistence.close();
    await operationsPool.end();
  });

  const ports = createPorts(persistence, {
    investigation: [operatorInvestigationId],
    run: [operatorRunId],
    lease: [firstLeaseId, resumedLeaseId],
    'operator-input-request': [requestId],
    'operator-input-record': [recordId],
  });
  const application = createSiteNightEnergyInvestigationCoordinator(ports);
  const started = await application.start({ organizationId, siteId });
  const waiting = await application.requestOperatorInput({ investigationId: started.id });
  assert.equal(waiting.status, 'WAITING_FOR_OPERATOR_INPUT');
  assert.equal(waiting.activeRun.id, started.activeRun.id);
  assert.equal(waiting.operatorInputRequest.id, requestId);

  const command = {
    investigationId: started.id,
    requestId,
    expectedRevision: waiting.revision,
    idempotencyKey,
    values: {
      analysisScope: 'SITE_ONLY',
      operatorNote: 'Persist one bounded Operator decision.',
    },
  };
  const accepted = await application.acceptOperatorInput(command);
  assert.equal(accepted.outcome, 'COMMITTED');
  assert.equal(accepted.investigation.status, 'RUNNING');
  assert.equal(accepted.investigation.activeRun.id, operatorRunId);
  assert.equal(accepted.investigation.acceptedOperatorInputs.length, 1);
  assert.equal(accepted.investigation.acceptedOperatorInputs[0].id, recordId);
  const committedCounts = await journalCounts(operationsPool, operatorInvestigationId);
  assert.equal(committedCounts.records, 1);
  assert.equal(committedCounts.effects, 0);
  const acceptedRows = await operationsPool.query(
    `SELECT count(*)::int AS count
       FROM agent_operations.investigation_business_records
      WHERE investigation_id = $1 AND record_type = 'OPERATOR_INPUT_ACCEPTED'`,
    [operatorInvestigationId],
  );
  assert.equal(acceptedRows.rows[0].count, 1);

  await persistence.close();
  persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 86_400_000,
    now: () => currentTime,
  });
  const restarted = createSiteNightEnergyInvestigationCoordinator(createPorts(persistence, {}));
  const retry = await restarted.acceptOperatorInput(command);
  assert.equal(retry.outcome, 'DUPLICATE');
  assert.equal(retry.investigation.acceptedOperatorInputs[0].id, recordId);
  assert.equal(retry.investigation.activeRun.id, operatorRunId);
  assert.deepEqual(await journalCounts(operationsPool, operatorInvestigationId), committedCounts);
  const reloaded = await restarted.get({ investigationId: operatorInvestigationId });
  assert.equal(reloaded.acceptedOperatorInputs.length, 1);
  assert.equal(reloaded.acceptedOperatorInputs[0].idempotencyKey, idempotencyKey);
});
