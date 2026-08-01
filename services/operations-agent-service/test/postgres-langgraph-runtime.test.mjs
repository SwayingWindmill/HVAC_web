import assert from 'node:assert/strict';
import test from 'node:test';

import {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
} from '../dist/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';
import { createLangGraphAgentExecutionRuntime } from '../dist/runtime-langgraph/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;

if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const scope = {
  organizationId: 'organization-langgraph-postgres',
  siteId: 'site-langgraph-postgres',
  equipmentId: null,
  deviceId: null,
};

const program = {
  id: 'night-energy-postgres-recovery',
  runtimeRevision: 'night-energy-runtime/v1',
  steps: [
    {
      id: 'read-registry-first',
      plan: {
        batches: [{
          batchId: 'registry-first',
          requests: [{
            requestId: 'registry-first-request',
            tool: 'registry.getSite',
            input: { siteId: scope.siteId },
          }],
        }],
      },
    },
    {
      id: 'read-registry-second',
      plan: {
        batches: [{
          batchId: 'registry-second',
          requests: [{
            requestId: 'registry-second-request',
            tool: 'registry.getSite',
            input: { siteId: scope.siteId },
          }],
        }],
      },
    },
  ],
};

const assertCoordinatorError = async (run, expectedCode) => {
  await assert.rejects(run, (error) => (
    error instanceof InvestigationCoordinatorError && error.code === expectedCode
  ));
};

const createIdGenerator = (values) => ({
  next(kind) {
    const value = values[kind]?.shift();
    assert.notEqual(value, undefined, `No identity configured for ${kind}.`);
    return value;
  },
});

const ownerReaders = {
  registry: {
    async read({ request }) {
      return {
        requestId: request.requestId,
        owner: 'registry',
        scope,
        revision: `registry:${request.requestId}`,
        quality: 'GOOD',
        provenance: `platform-core-service:${request.requestId}`,
        payload: { equipmentId: request.input.equipmentId },
      };
    },
  },
  currentTelemetry: { async read() { throw new Error('not used'); } },
  energyAnalytics: { async read() { throw new Error('not used'); } },
  commandCapabilities: { async read() { throw new Error('not used'); } },
};

const commonPorts = (persistence, currentTime, idGenerator, runtime) => {
  return {
    investigationRepository: persistence.investigationRepository,
    businessRecordRepository: persistence.businessRecordRepository,
    investigationTransaction: persistence.investigationTransaction,
    checkpointRepository: persistence.checkpointRepository,
    applicationOutbox: persistence.applicationOutbox,
    auditRecorder: persistence.auditRecorder,
    authorizationDecisionReader: {
      async authorizeScope() {
        return { decision: 'ALLOW', decisionId: 'langgraph-postgres-allow' };
      },
    },
    agentExecutionRuntime: runtime,
    budgetGuard: { async check() { return { decision: 'ALLOW' }; } },
    ownerReaders,
    clock: { now: currentTime },
    idGenerator,
    leaseDurationMs: 10_000,
  };
};

test('PostgreSQL restart resumes the LangGraph Runtime at the next logical Step', async (t) => {
  let currentTime = 10_000;
  let firstPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime,
  });
  let secondPersistence = null;
  t.after(async () => {
    const openPersistence = [firstPersistence, secondPersistence].filter(Boolean);
    await Promise.all(openPersistence.map((persistence) => persistence.close()));
  });

  const firstCoordinator = createInvestigationCoordinator(commonPorts(
    firstPersistence,
    () => currentTime,
    createIdGenerator({
      investigation: ['investigation-langgraph-postgres'],
      run: ['run-langgraph-postgres'],
      lease: ['lease-langgraph-postgres-001'],
      checkpoint: ['checkpoint-langgraph-postgres-001'],
    }),
    createLangGraphAgentExecutionRuntime(program),
  ));

  const created = await firstCoordinator.create({ scope });
  const started = await firstCoordinator.start({
    investigationId: created.id,
    runtimeRevision: program.runtimeRevision,
    expectedRevision: created.revision,
  });
  const runId = started.activeRunId;
  const firstLeaseId = started.runs[0].lease.id;
  const firstAdvance = await firstCoordinator.advance({
    investigationId: started.id,
    runId,
    leaseId: firstLeaseId,
    expectedRevision: started.revision,
  });
  assert.equal(firstAdvance.plan.batches[0].requests[0].requestId, 'registry-first-request');
  assert.equal(firstAdvance.investigation.revision, started.revision);
  assert.deepEqual(firstAdvance.investigation.committedEffects, []);
  const firstCheckpoint = await firstPersistence.checkpointRepository.load(started.id, runId);
  assert.notEqual(firstCheckpoint, null);
  assert.equal(firstCheckpoint.savedAt, 10_000);

  await firstPersistence.close();
  firstPersistence = null;
  currentTime = 10_000;
  secondPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime,
  });
  const callOrder = [];
  let runtimeCallCount = 0;
  let checkpointLoadCount = 0;
  const restartedRuntime = createLangGraphAgentExecutionRuntime(program);
  const countingRuntime = {
    async planReads(input) {
      runtimeCallCount += 1;
      return restartedRuntime.planReads(input);
    },
  };
  const orderedRepository = {
    async get(investigationId) {
      callOrder.push('investigation');
      return secondPersistence.investigationRepository.get(investigationId);
    },
  };
  const orderedCheckpoints = {
    ...secondPersistence.checkpointRepository,
    async load(investigationId, requestedRunId) {
      checkpointLoadCount += 1;
      callOrder.push('checkpoint');
      return secondPersistence.checkpointRepository.load(investigationId, requestedRunId);
    },
  };

  const secondCoordinator = createInvestigationCoordinator({
    ...commonPorts(
      secondPersistence,
      () => currentTime,
      createIdGenerator({
        investigation: [],
        run: [],
        lease: ['lease-langgraph-postgres-002'],
        checkpoint: ['checkpoint-langgraph-postgres-002'],
      }),
      countingRuntime,
    ),
    investigationRepository: orderedRepository,
    checkpointRepository: orderedCheckpoints,
  });

  const secondAdvance = await secondCoordinator.advance({
    investigationId: started.id,
    runId,
    leaseId: firstLeaseId,
    expectedRevision: started.revision,
  });
  assert.deepEqual(callOrder.slice(0, 2), ['investigation', 'checkpoint']);
  assert.equal(runtimeCallCount, 1);
  assert.equal(secondAdvance.plan.batches[0].requests[0].requestId, 'registry-second-request');
  assert.equal(secondAdvance.investigation.activeRunId, runId);
  assert.equal(secondAdvance.investigation.revision, started.revision);
  assert.deepEqual(secondAdvance.investigation.committedEffects, []);
  const secondCheckpoint = await secondPersistence.checkpointRepository.load(started.id, runId);
  assert.notEqual(secondCheckpoint, null);
  assert.equal(secondCheckpoint.position, 'complete');
  assert.equal(secondCheckpoint.savedAt, 10_001);

  const invalidCheckpointCoordinator = createInvestigationCoordinator({
    ...commonPorts(
      secondPersistence,
      () => currentTime,
      createIdGenerator({ investigation: [], run: [], lease: [], checkpoint: [] }),
      createLangGraphAgentExecutionRuntime(program),
    ),
    checkpointRepository: {
      ...secondPersistence.checkpointRepository,
      async load(investigationId, requestedRunId) {
        const checkpoint = await secondPersistence.checkpointRepository.load(
          investigationId,
          requestedRunId,
        );
        assert.notEqual(checkpoint, null);
        return {
          ...checkpoint,
          opaqueState: '{"schemaVersion":1,"businessFinding":"forbidden"}',
        };
      },
    },
  });
  await assert.rejects(
    () => invalidCheckpointCoordinator.advance({
      investigationId: started.id,
      runId,
      leaseId: firstLeaseId,
      expectedRevision: started.revision,
    }),
    (error) => error instanceof InvestigationCoordinatorError
      && error.code === 'INVALID_INVESTIGATION_STATE'
      && error.name === 'InvestigationCoordinatorError',
  );

  currentTime = 12_000;
  const paused = await secondCoordinator.pause({
    investigationId: started.id,
    runId,
    leaseId: firstLeaseId,
    expectedRevision: started.revision,
  });
  currentTime = 12_100;
  const resumed = await secondCoordinator.resume({
    investigationId: started.id,
    runId,
    expectedRevision: paused.revision,
  });
  const loadCountBeforeOldLease = checkpointLoadCount;
  const runtimeCallsBeforeOldLease = runtimeCallCount;
  currentTime = 12_200;
  await assertCoordinatorError(() => secondCoordinator.advance({
    investigationId: started.id,
    runId,
    leaseId: firstLeaseId,
    expectedRevision: resumed.revision,
  }), 'LEASE_CONFLICT');
  assert.equal(checkpointLoadCount, loadCountBeforeOldLease);
  assert.equal(runtimeCallCount, runtimeCallsBeforeOldLease);

  const beforeDelete = await secondPersistence.investigationRepository.get(started.id);
  assert.notEqual(beforeDelete, null);
  await secondPersistence.checkpointRepository.delete(started.id, runId);
  assert.equal(await secondPersistence.checkpointRepository.load(started.id, runId), null);
  const afterDelete = await secondPersistence.investigationRepository.get(started.id);
  assert.notEqual(afterDelete, null);
  assert.deepEqual(afterDelete.view(), beforeDelete.view());
});