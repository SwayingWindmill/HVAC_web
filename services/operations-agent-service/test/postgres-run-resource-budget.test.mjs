import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createInvestigationCoordinator,
} from '../dist/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;

if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const policy = {
  schemaVersion: 1,
  revision: 'postgres-run-resource-policy/v1',
  limits: {
    modelInvocations: 2,
    toolRequests: 2,
    wallClockMs: 10_000,
    queryRangeMs: 86_400_000,
    queryBuckets: 24,
    ownerRecords: 100,
    payloadBytes: 1_000_000,
  },
};

const toolCost = {
  modelInvocations: 0,
  toolRequests: 1,
  queryRangeMs: 0,
  queryBuckets: 0,
  ownerRecords: 0,
  payloadBytes: 0,
};

const coordinatorFor = (persistence, currentTime, identities) => createInvestigationCoordinator({
  investigationRepository: persistence.investigationRepository,
  businessRecordRepository: persistence.businessRecordRepository,
  investigationTransaction: persistence.investigationTransaction,
  checkpointRepository: persistence.checkpointRepository,
  applicationOutbox: persistence.applicationOutbox,
  auditRecorder: persistence.auditRecorder,
  authorizationDecisionReader: {
    async authorizeScope() {
      return { decision: 'ALLOW', decisionId: 'postgres-budget-allow' };
    },
  },
  agentExecutionRuntime: {
    async planReads() {
      throw new Error('Runtime planning is not used by this budget persistence test.');
    },
  },
  budgetGuard: persistence.budgetGuard,
  resourceBudgetPolicy: policy,
  ownerReaders: {
    registry: { async read() { throw new Error('not used'); } },
    currentTelemetry: { async read() { throw new Error('not used'); } },
    energyAnalytics: { async read() { throw new Error('not used'); } },
    commandCapabilities: { async read() { throw new Error('not used'); } },
  },
  clock: { now: () => currentTime.value },
  idGenerator: {
    next(kind) {
      const value = identities[kind]?.shift();
      assert.notEqual(value, undefined, `No identity configured for ${kind}.`);
      return value;
    },
  },
  leaseDurationMs: 10_000,
});

test('PostgreSQL Run budget survives restart, ignores Checkpoint deletion, and serializes concurrency', async (t) => {
  const currentTime = { value: 1_000 };
  let firstPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime.value,
  });
  let secondPersistence = null;
  t.after(async () => {
    await Promise.all(
      [firstPersistence, secondPersistence].filter(Boolean).map((persistence) => persistence.close()),
    );
  });

  const coordinator = coordinatorFor(firstPersistence, currentTime, {
    investigation: ['investigation-postgres-budget'],
    run: ['run-postgres-budget'],
    lease: ['lease-postgres-budget'],
  });
  const created = await coordinator.create({
    scope: {
      organizationId: 'organization-postgres-budget',
      siteId: 'site-postgres-budget',
      equipmentId: null,
      deviceId: null,
    },
  });
  const started = await coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-postgres-budget/v1',
    expectedRevision: created.revision,
  });
  const runId = started.activeRunId;

  const first = await firstPersistence.budgetGuard.check({
    investigationId: started.id,
    runId,
    startedAt: started.runs[0].startedAt,
    at: 1_100,
    operationId: 'tool-read-001',
    policy,
    cost: toolCost,
  });
  assert.equal(first.decision, 'ALLOW');
  assert.equal(first.duplicate, false);
  assert.equal(first.snapshot.usage.toolRequests, 1);

  await firstPersistence.checkpointRepository.delete(started.id, runId);
  await firstPersistence.close();
  firstPersistence = null;
  secondPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime.value,
  });

  const retry = await secondPersistence.budgetGuard.check({
    investigationId: started.id,
    runId,
    startedAt: started.runs[0].startedAt,
    at: 1_200,
    operationId: 'tool-read-001',
    policy,
    cost: toolCost,
  });
  assert.equal(retry.decision, 'ALLOW');
  assert.equal(retry.duplicate, true);
  assert.equal(retry.snapshot.usage.toolRequests, 1);

  const [concurrentA, concurrentB] = await Promise.all([
    secondPersistence.budgetGuard.check({
      investigationId: started.id,
      runId,
      startedAt: started.runs[0].startedAt,
      at: 1_300,
      operationId: 'tool-read-002',
      policy,
      cost: toolCost,
    }),
    secondPersistence.budgetGuard.check({
      investigationId: started.id,
      runId,
      startedAt: started.runs[0].startedAt,
      at: 1_300,
      operationId: 'tool-read-003',
      policy,
      cost: toolCost,
    }),
  ]);
  assert.deepEqual(
    [concurrentA.decision, concurrentB.decision].sort(),
    ['ALLOW', 'DENY'],
  );
  const persisted = await secondPersistence.budgetGuard.get(started.id, runId);
  assert.equal(persisted.usage.toolRequests, 2);
  assert.equal(persisted.exhaustion.dimension, 'TOOL_REQUESTS');
  assert.equal(persisted.exhaustion.consumed, 3);
  assert.equal(persisted.exhaustion.limit, 2);
});
