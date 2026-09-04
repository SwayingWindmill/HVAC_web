import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AgentSessionLifecycleError,
  createAgentSessionLifecycle,
} from '../dist/application/index.js';

class MemoryAgentSessionStateStore {
  state = null;

  async get(sessionId) {
    return this.state?.session.id === sessionId ? this.state : null;
  }

  async transact(sessionId, update) {
    const current = this.state?.session.id === sessionId ? this.state : null;
    const next = update(current);
    this.state = next;
    return next;
  }
}

const descriptor = Object.freeze({
  id: 'session-durable-1',
  tenantId: 'tenant-1',
  siteId: 'site-1',
  agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-1',
  createdAt: 1_000,
});

const modelRef = Object.freeze({ provider: 'faux', model: 'faux-1' });
const zeroUsage = Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 });

const runningRun = (id, startedAt) => Object.freeze({
  id,
  sessionId: descriptor.id,
  modelRef,
  status: 'RUNNING',
  startedAt,
  finishedAt: null,
  usage: zeroUsage,
  failureCode: null,
});

const operatorMessage = (id, content, createdAt) => Object.freeze({
  id,
  sessionId: descriptor.id,
  runId: null,
  role: 'OPERATOR',
  content,
  createdAt,
});

const completedRun = (run, finishedAt, usage = zeroUsage) => Object.freeze({
  ...run,
  status: 'COMPLETED',
  finishedAt,
  usage,
  failureCode: null,
});

const assistantMessage = (id, runId, content, createdAt) => Object.freeze({
  id,
  sessionId: descriptor.id,
  runId,
  role: 'ASSISTANT',
  content,
  createdAt,
});

const findingArtifact = (id, runId, createdAt) => Object.freeze({
  id,
  sessionId: descriptor.id,
  runId,
  kind: 'FINDING',
  finding: Object.freeze({
    outcome: 'UNABLE_TO_CONCLUDE',
    summary: 'The evidence remains incomplete.',
    evidenceRefs: Object.freeze([]),
    limitations: Object.freeze(['One Energy period is incomplete.']),
    recommendedNext: Object.freeze(['Retry after the dataset is complete.']),
  }),
  createdAt,
});

test('durable lifecycle makes start idempotent and replaces only an interrupted Run after restart', async () => {
  const store = new MemoryAgentSessionStateStore();
  const lifecycle = createAgentSessionLifecycle({ store });
  const firstRun = runningRun('run-durable-1', 1_100);
  const firstMessage = operatorMessage('message-durable-1', 'Investigate overnight energy.', 1_050);

  const started = await lifecycle.start({
    session: descriptor,
    expectedSessionRevision: null,
    run: firstRun,
    operatorMessage: firstMessage,
  });
  assert.equal(started.session.status, 'ACTIVE');
  assert.equal(started.session.activeRunId, firstRun.id);
  assert.equal(started.session.revision, 0);
  assert.deepEqual(started.messages, [firstMessage]);

  const exactRetry = await lifecycle.start({
    session: descriptor,
    expectedSessionRevision: null,
    run: firstRun,
    operatorMessage: firstMessage,
  });
  assert.deepEqual(exactRetry, started);
  assert.equal(exactRetry.runs.length, 1);
  assert.equal(exactRetry.messages.length, 1);

  await assert.rejects(
    () => lifecycle.start({
      session: descriptor,
      expectedSessionRevision: started.session.revision,
      run: runningRun('run-durable-conflict', 1_200),
      operatorMessage: operatorMessage('message-durable-conflict', 'Do not overlap Runs.', 1_150),
    }),
    (error) => error instanceof AgentSessionLifecycleError && error.code === 'RUN_ALREADY_ACTIVE',
  );

  const interrupted = await lifecycle.interrupt({
    sessionId: descriptor.id,
    runId: firstRun.id,
    at: 1_300,
  });
  assert.equal(interrupted.runs[0].status, 'FAILED');
  assert.equal(interrupted.runs[0].failureCode, 'RUN_INTERRUPTED');
  assert.equal(interrupted.messages.length, 1, 'partial provider output must not become a finalized Message');
  assert.equal(interrupted.session.status, 'ACTIVE');
  assert.equal(interrupted.session.activeRunId, firstRun.id);

  const secondRun = runningRun('run-durable-2', 1_400);
  const secondMessage = operatorMessage('message-durable-2', 'Continue from committed state only.', 1_350);
  const restarted = await lifecycle.start({
    session: descriptor,
    expectedSessionRevision: interrupted.session.revision,
    run: secondRun,
    operatorMessage: secondMessage,
  });
  assert.equal(restarted.session.activeRunId, secondRun.id);
  assert.equal(restarted.session.revision, 1);
  assert.deepEqual(restarted.messages, [firstMessage, secondMessage]);
  assert.deepEqual(restarted.runs.map(({ id, status }) => [id, status]), [
    [firstRun.id, 'FAILED'],
    [secondRun.id, 'RUNNING'],
  ]);
});

test('complete commits finalized records atomically and exact retry does not append twice', async () => {
  const store = new MemoryAgentSessionStateStore();
  const lifecycle = createAgentSessionLifecycle({ store });
  const run = runningRun('run-complete-1', 2_100);
  const operator = operatorMessage('message-complete-operator', 'Investigate incomplete Energy.', 2_050);
  const started = await lifecycle.start({
    session: { ...descriptor, id: 'session-durable-1', createdAt: 2_000 },
    expectedSessionRevision: null,
    run: { ...run, sessionId: 'session-durable-1' },
    operatorMessage: { ...operator, sessionId: 'session-durable-1' },
  });
  const terminalRun = completedRun(started.runs[0], 2_300, {
    inputTokens: 120,
    outputTokens: 40,
    modelCalls: 2,
    toolCalls: 1,
  });
  const assistant = assistantMessage('message-complete-assistant', terminalRun.id, 'The current period is incomplete.', 2_250);
  const toolExecution = Object.freeze({
    id: 'tool-complete-1',
    sessionId: started.session.id,
    runId: terminalRun.id,
    toolName: 'energy.compare_periods',
    argumentsDigest: 'abc123',
    status: 'COMPLETED',
    startedAt: 2_150,
    finishedAt: 2_200,
    resultSummary: 'Tool completed.',
    provenance: Object.freeze([]),
    failureCode: null,
  });
  const artifact = findingArtifact('artifact-complete-1', terminalRun.id, 2_300);
  const command = {
    sessionId: started.session.id,
    runId: terminalRun.id,
    expectedSessionRevision: started.session.revision,
    run: terminalRun,
    sessionStatus: 'COMPLETED',
    finalizedMessages: [{ ...assistant, sessionId: started.session.id }],
    toolExecutions: [toolExecution],
    artifacts: [{ ...artifact, sessionId: started.session.id }],
  };

  const completed = await lifecycle.complete(command);
  assert.equal(completed.session.status, 'COMPLETED');
  assert.equal(completed.session.activeRunId, null);
  assert.equal(completed.session.revision, 1);
  assert.equal(completed.messages.length, 2);
  assert.equal(completed.toolExecutions.length, 1);
  assert.equal(completed.artifacts.length, 1);

  const exactRetry = await lifecycle.complete(command);
  assert.deepEqual(exactRetry, completed);
  assert.equal(exactRetry.messages.length, 2);
  assert.equal(exactRetry.toolExecutions.length, 1);
  assert.equal(exactRetry.artifacts.length, 1);
});

test('terminal Run failure metadata cannot contradict its durable status', async () => {
  const completedStore = new MemoryAgentSessionStateStore();
  const completedLifecycle = createAgentSessionLifecycle({ store: completedStore });
  const completedBaseRun = runningRun('run-invalid-completed', 2_700);
  const completedStarted = await completedLifecycle.start({
    session: { ...descriptor, createdAt: 2_600 },
    expectedSessionRevision: null,
    run: completedBaseRun,
    operatorMessage: operatorMessage('message-invalid-completed', 'Validate completed metadata.', 2_650),
  });
  await assert.rejects(
    () => completedLifecycle.complete({
      sessionId: descriptor.id,
      runId: completedBaseRun.id,
      expectedSessionRevision: completedStarted.session.revision,
      run: Object.freeze({
        ...completedBaseRun,
        status: 'COMPLETED',
        finishedAt: 2_800,
        failureCode: 'IMPOSSIBLE_COMPLETION_FAILURE',
      }),
      sessionStatus: 'COMPLETED',
      finalizedMessages: [],
      toolExecutions: [],
      artifacts: [],
    }),
    (error) => error instanceof AgentSessionLifecycleError && error.code === 'LIFECYCLE_INPUT_INVALID',
  );

  const failedStore = new MemoryAgentSessionStateStore();
  const failedLifecycle = createAgentSessionLifecycle({ store: failedStore });
  const failedBaseRun = runningRun('run-invalid-failed', 2_900);
  const failedStarted = await failedLifecycle.start({
    session: { ...descriptor, createdAt: 2_850 },
    expectedSessionRevision: null,
    run: failedBaseRun,
    operatorMessage: operatorMessage('message-invalid-failed', 'Validate failed metadata.', 2_875),
  });
  await assert.rejects(
    () => failedLifecycle.complete({
      sessionId: descriptor.id,
      runId: failedBaseRun.id,
      expectedSessionRevision: failedStarted.session.revision,
      run: Object.freeze({
        ...failedBaseRun,
        status: 'FAILED',
        finishedAt: 3_000,
        failureCode: null,
      }),
      sessionStatus: 'FAILED',
      finalizedMessages: [],
      toolExecutions: [],
      artifacts: [],
    }),
    (error) => error instanceof AgentSessionLifecycleError && error.code === 'LIFECYCLE_INPUT_INVALID',
  );
});

test('cancel is idempotent and prevents a stale Run completion from advancing the Session', async () => {
  const store = new MemoryAgentSessionStateStore();
  const lifecycle = createAgentSessionLifecycle({ store });
  const run = runningRun('run-cancel-1', 3_100);
  const started = await lifecycle.start({
    session: { ...descriptor, createdAt: 3_000 },
    expectedSessionRevision: null,
    run,
    operatorMessage: operatorMessage('message-cancel-1', 'Cancel this investigation.', 3_050),
  });

  const cancelled = await lifecycle.cancel({
    sessionId: started.session.id,
    runId: run.id,
    expectedSessionRevision: started.session.revision,
    at: 3_200,
  });
  assert.equal(cancelled.session.status, 'CANCELLED');
  assert.equal(cancelled.session.revision, 1);
  assert.equal(cancelled.runs[0].status, 'CANCELLED');
  assert.equal(cancelled.runs[0].failureCode, 'RUN_CANCELLED');

  const exactRetry = await lifecycle.cancel({
    sessionId: started.session.id,
    runId: run.id,
    expectedSessionRevision: started.session.revision,
    at: 3_200,
  });
  assert.deepEqual(exactRetry, cancelled);

  await assert.rejects(
    () => lifecycle.complete({
      sessionId: started.session.id,
      runId: run.id,
      expectedSessionRevision: started.session.revision,
      run: completedRun(run, 3_250),
      sessionStatus: 'COMPLETED',
      finalizedMessages: [assistantMessage('message-stale-complete', run.id, 'Stale completion.', 3_240)],
      toolExecutions: [],
      artifacts: [findingArtifact('artifact-stale-complete', run.id, 3_250)],
    }),
    (error) => error instanceof AgentSessionLifecycleError && error.code === 'RUN_STALE',
  );
  assert.deepEqual(await lifecycle.get(started.session.id), cancelled);
});
