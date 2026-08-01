import assert from 'node:assert/strict';
import test from 'node:test';

import {
  LangGraphRuntimeError,
  createLangGraphAgentExecutionRuntime,
} from '../dist/runtime-langgraph/index.js';

const investigationView = ({ runtimeRevision = 'night-energy-runtime/v1' } = {}) => ({
  id: 'investigation-runtime-001',
  scope: {
    organizationId: 'organization-runtime-001',
    siteId: 'site-runtime-001',
    equipmentId: null,
    deviceId: null,
  },
  status: 'RUNNING',
  revision: 1,
  activeRunId: 'run-runtime-001',
  runs: [{
    id: 'run-runtime-001',
    runtimeRevision,
    status: 'ACTIVE',
    startedAt: 1_000,
    pausedAt: null,
    endedAt: null,
    lease: {
      id: 'lease-runtime-001',
      runId: 'run-runtime-001',
      acquiredAt: 1_000,
      expiresAt: 20_000,
    },
    leaseHistory: [{
      id: 'lease-runtime-001',
      runId: 'run-runtime-001',
      acquiredAt: 1_000,
      expiresAt: 20_000,
    }],
  }],
  committedEffects: [],
  evidenceIds: [],
  findingIds: [],
  proposedActionIds: [],
});

const program = {
  id: 'night-energy-investigation',
  runtimeRevision: 'night-energy-runtime/v1',
  steps: [
    {
      id: 'read-registry',
      plan: {
        batches: [{
          batchId: 'registry-batch',
          requests: [{
            requestId: 'registry-equipment-001',
            tool: 'registry.getSite',
            input: { siteId: 'equipment-runtime-001' },
          }],
        }],
      },
    },
    {
      id: 'read-second-registry-record',
      plan: {
        batches: [{
          batchId: 'registry-batch-002',
          requests: [{
            requestId: 'registry-equipment-002',
            tool: 'registry.getSite',
            input: { siteId: 'equipment-runtime-002' },
          }],
        }],
      },
    },
  ],
};

const toCheckpoint = (planning, id) => ({
  id,
  investigationId: 'investigation-runtime-001',
  runId: 'run-runtime-001',
  runtimeRevision: 'night-energy-runtime/v1',
  position: planning.checkpoint.position,
  opaqueState: planning.checkpoint.opaqueState,
  savedAt: 2_000,
});

test('LangGraph runtime resumes the same Agent Run at the next logical Step', async () => {
  const firstProcess = createLangGraphAgentExecutionRuntime(program);
  const first = await firstProcess.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: null,
  });

  assert.equal(first.status, 'PLANNED');
  assert.equal(first.plan.batches[0].requests[0].requestId, 'registry-equipment-001');
  assert.equal(first.checkpoint.position, 'before:read-second-registry-record');
  assert.deepEqual(
    Object.keys(JSON.parse(first.checkpoint.opaqueState)).sort(),
    [
      'completedStepIds',
      'investigationId',
      'nextStepIndex',
      'programId',
      'runId',
      'runtimeRevision',
      'schemaVersion',
    ],
  );

  const restartedProcess = createLangGraphAgentExecutionRuntime(program);
  const second = await restartedProcess.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: toCheckpoint(first, 'checkpoint-runtime-001'),
  });

  assert.equal(second.status, 'PLANNED');
  assert.equal(second.plan.batches[0].requests[0].requestId, 'registry-equipment-002');
  assert.equal(second.checkpoint.position, 'complete');

  const repeatedRecovery = await createLangGraphAgentExecutionRuntime(program).planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: toCheckpoint(first, 'checkpoint-runtime-repeated'),
  });
  assert.deepEqual(repeatedRecovery, second);

  const completed = await restartedProcess.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: toCheckpoint(second, 'checkpoint-runtime-002'),
  });
  assert.deepEqual(completed, {
    status: 'UNABLE_TO_CONCLUDE',
    reason: 'Runtime program night-energy-investigation has no remaining READ Step.',
  });
});

test('LangGraph runtime rejects Checkpoints outside the active Run and Runtime Revision', async () => {
  const runtime = createLangGraphAgentExecutionRuntime(program);
  const first = await runtime.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: null,
  });
  assert.equal(first.status, 'PLANNED');

  const invalidCheckpoint = {
    ...toCheckpoint(first, 'checkpoint-runtime-invalid'),
    runtimeRevision: 'night-energy-runtime/v0',
  };

  await assert.rejects(
    () => runtime.planReads({
      investigation: investigationView(),
      runId: 'run-runtime-001',
      checkpoint: invalidCheckpoint,
    }),
    (error) => error instanceof LangGraphRuntimeError
      && error.code === 'CHECKPOINT_IDENTITY_MISMATCH',
  );

  await assert.rejects(
    () => runtime.planReads({
      investigation: investigationView({ runtimeRevision: 'night-energy-runtime/v0' }),
      runId: 'run-runtime-001',
      checkpoint: null,
    }),
    (error) => error instanceof LangGraphRuntimeError
      && error.code === 'RUNTIME_REVISION_MISMATCH',
  );
});

test('LangGraph runtime rejects malformed or position-conflicting opaque state', async () => {
  const runtime = createLangGraphAgentExecutionRuntime(program);
  const first = await runtime.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: null,
  });
  assert.equal(first.status, 'PLANNED');

  await assert.rejects(
    () => runtime.planReads({
      investigation: investigationView(),
      runId: 'run-runtime-001',
      checkpoint: {
        ...toCheckpoint(first, 'checkpoint-runtime-malformed'),
        opaqueState: '{"schemaVersion":1,"businessFinding":"forbidden"}',
      },
    }),
    (error) => error instanceof LangGraphRuntimeError
      && error.code === 'CHECKPOINT_STATE_INVALID',
  );

  await assert.rejects(
    () => runtime.planReads({
      investigation: investigationView(),
      runId: 'run-runtime-001',
      checkpoint: {
        ...toCheckpoint(first, 'checkpoint-runtime-position'),
        position: 'before:read-registry',
      },
    }),
    (error) => error instanceof LangGraphRuntimeError
      && error.code === 'CHECKPOINT_POSITION_MISMATCH',
  );
});

test('LangGraph runtime enforces bounded programs and Checkpoint state', async () => {
  const oversizedProgram = {
    ...program,
    steps: Array.from({ length: 65 }, (_, index) => ({
      id: `read-step-${index}`,
      plan: program.steps[0].plan,
    })),
  };
  assert.throws(
    () => createLangGraphAgentExecutionRuntime(oversizedProgram),
    (error) => error instanceof LangGraphRuntimeError && error.code === 'PROGRAM_INVALID',
  );

  const runtime = createLangGraphAgentExecutionRuntime(program);
  const first = await runtime.planReads({
    investigation: investigationView(),
    runId: 'run-runtime-001',
    checkpoint: null,
  });
  assert.equal(first.status, 'PLANNED');

  await assert.rejects(
    () => runtime.planReads({
      investigation: investigationView(),
      runId: 'run-runtime-001',
      checkpoint: {
        ...toCheckpoint(first, 'checkpoint-runtime-oversized'),
        opaqueState: 'x'.repeat(32_769),
      },
    }),
    (error) => error instanceof LangGraphRuntimeError
      && error.code === 'CHECKPOINT_STATE_INVALID',
  );
});
