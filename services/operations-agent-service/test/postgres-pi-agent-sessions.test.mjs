import assert from 'node:assert/strict';
import test from 'node:test';

import { Pool } from 'pg';

import {
  AgentSessionLifecycleError,
  createAgentSessionLifecycle,
} from '../dist/application/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;

if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const modelRef = Object.freeze({ provider: 'faux', model: 'faux-1' });
const zeroUsage = Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 });
const sessionDescriptor = Object.freeze({
  id: 'pi-session-postgres-1',
  tenantId: 'tenant-postgres-pi',
  siteId: 'site-postgres-pi',
  agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-postgres-pi',
  createdAt: 10_000,
});

const run = (id, startedAt) => Object.freeze({
  id,
  sessionId: sessionDescriptor.id,
  modelRef,
  status: 'RUNNING',
  startedAt,
  finishedAt: null,
  usage: zeroUsage,
  failureCode: null,
});

const operatorMessage = (id, content, createdAt) => Object.freeze({
  id,
  sessionId: sessionDescriptor.id,
  runId: null,
  role: 'OPERATOR',
  content,
  createdAt,
});

const assistantMessage = (id, runId, content, createdAt) => Object.freeze({
  id,
  sessionId: sessionDescriptor.id,
  runId,
  role: 'ASSISTANT',
  content,
  createdAt,
});

const inputRequestArtifact = (id, runId, createdAt) => Object.freeze({
  id,
  sessionId: sessionDescriptor.id,
  runId,
  kind: 'INPUT_REQUEST',
  request: Object.freeze({
    prompt: 'Which operating schedule should be used as the baseline?',
    response: Object.freeze({ kind: 'TEXT', maxLength: 200 }),
  }),
  createdAt,
});

const findingArtifact = (id, runId, createdAt) => Object.freeze({
  id,
  sessionId: sessionDescriptor.id,
  runId,
  kind: 'FINDING',
  finding: Object.freeze({
    outcome: 'UNABLE_TO_CONCLUDE',
    summary: 'The restarted Run was cancelled before another conclusion was committed.',
    evidenceRefs: Object.freeze([]),
    limitations: Object.freeze(['Cancellation ended the active Run.']),
    recommendedNext: Object.freeze([]),
  }),
  createdAt,
});

const completedRun = (value, finishedAt, usage) => Object.freeze({
  ...value,
  status: 'COMPLETED',
  finishedAt,
  usage,
  failureCode: null,
});

const createPersistence = () => createPostgresOperationsAgentPersistence({
  operationsConnectionString,
  checkpointsConnectionString,
  checkpointRetentionMs: 60_000,
});

test('PostgreSQL Pi Session state survives restart without checkpoint or partial-stream authority', async (t) => {
  let persistence = createPersistence();
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await Promise.all([
      persistence.close(),
      operationsPool.end(),
    ]);
  });

  let lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const firstRun = run('pi-run-postgres-1', 10_100);
  const firstOperator = operatorMessage('pi-message-operator-1', 'Investigate overnight Energy.', 10_050);
  const started = await lifecycle.start({
    session: sessionDescriptor,
    expectedSessionRevision: null,
    run: firstRun,
    operatorMessage: firstOperator,
  });

  const firstTool = Object.freeze({
    id: 'pi-tool-postgres-1',
    sessionId: sessionDescriptor.id,
    runId: firstRun.id,
    toolName: 'energy.compare_periods',
    argumentsDigest: 'digest-energy-periods-v1',
    status: 'COMPLETED',
    startedAt: 10_120,
    finishedAt: 10_160,
    resultSummary: 'Tool completed.',
    provenance: Object.freeze([]),
    failureCode: null,
  });
  const firstAssistant = assistantMessage(
    'pi-message-assistant-1',
    firstRun.id,
    'I need the expected operating schedule before comparing the periods.',
    10_170,
  );
  const firstArtifact = inputRequestArtifact('pi-artifact-input-1', firstRun.id, 10_200);
  const waitingRun = completedRun(firstRun, 10_200, {
    inputTokens: 150,
    outputTokens: 45,
    modelCalls: 2,
    toolCalls: 1,
  });
  const completeWaiting = {
    sessionId: sessionDescriptor.id,
    runId: firstRun.id,
    expectedSessionRevision: started.session.revision,
    run: waitingRun,
    sessionStatus: 'WAITING_FOR_INPUT',
    finalizedMessages: [firstAssistant],
    toolExecutions: [firstTool],
    artifacts: [firstArtifact],
  };
  const waiting = await lifecycle.complete(completeWaiting);
  assert.equal(waiting.session.status, 'WAITING_FOR_INPUT');
  assert.equal(waiting.session.revision, 1);
  assert.equal(waiting.messages.length, 2);
  assert.equal(waiting.toolExecutions.length, 1);
  assert.equal(waiting.artifacts.length, 1);
  assert.deepEqual(await lifecycle.get(sessionDescriptor.id), waiting);

  const exactCompleteRetry = await lifecycle.complete(completeWaiting);
  assert.deepEqual(exactCompleteRetry, waiting);

  await persistence.close();
  persistence = createPersistence();
  lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });

  const recovered = await lifecycle.get(sessionDescriptor.id);
  assert.deepEqual(recovered, waiting);

  await persistence.checkpointRepository.save({
    id: 'legacy-langgraph-checkpoint-for-pi-session',
    investigationId: sessionDescriptor.id,
    runId: firstRun.id,
    runtimeRevision: 'legacy-langgraph-runtime',
    position: 'legacy-position',
    opaqueState: 'legacy checkpoint text that must never become Pi Session context',
    savedAt: 10_250,
  });
  assert.deepEqual(await lifecycle.get(sessionDescriptor.id), waiting);

  const secondRun = run('pi-run-postgres-2', 10_300);
  const secondOperator = operatorMessage(
    'pi-message-operator-2',
    'Use the weekday schedule and continue from committed records.',
    10_280,
  );
  const secondStarted = await lifecycle.start({
    session: sessionDescriptor,
    expectedSessionRevision: recovered.session.revision,
    run: secondRun,
    operatorMessage: secondOperator,
  });
  assert.equal(secondStarted.session.revision, 2);
  assert.deepEqual(secondStarted.messages.map(({ id }) => id), [
    firstOperator.id,
    firstAssistant.id,
    secondOperator.id,
  ]);

  const uncommittedPartialAssistantText = 'UNCOMMITTED_STREAM_TEXT_SHOULD_NEVER_BE_DURABLE';
  assert.equal(typeof uncommittedPartialAssistantText, 'string');
  await persistence.close();
  persistence = createPersistence();
  lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });

  const afterCrash = await lifecycle.get(sessionDescriptor.id);
  assert.equal(afterCrash.messages.some(({ content }) => content.includes('UNCOMMITTED_STREAM_TEXT')), false);
  const interrupted = await lifecycle.interrupt({
    sessionId: sessionDescriptor.id,
    runId: secondRun.id,
    at: 10_400,
  });
  assert.equal(interrupted.runs.find(({ id }) => id === secondRun.id).failureCode, 'RUN_INTERRUPTED');
  assert.equal(interrupted.messages.some(({ content }) => content.includes('UNCOMMITTED_STREAM_TEXT')), false);

  const thirdRun = run('pi-run-postgres-3', 10_500);
  const thirdOperator = operatorMessage('pi-message-operator-3', 'Retry the safe Energy read.', 10_480);
  const thirdStarted = await lifecycle.start({
    session: sessionDescriptor,
    expectedSessionRevision: interrupted.session.revision,
    run: thirdRun,
    operatorMessage: thirdOperator,
  });
  assert.equal(thirdStarted.session.activeRunId, thirdRun.id);
  assert.deepEqual(thirdStarted.toolExecutions.map(({ toolName }) => toolName), ['energy.compare_periods']);

  const cancelled = await lifecycle.cancel({
    sessionId: sessionDescriptor.id,
    runId: thirdRun.id,
    expectedSessionRevision: thirdStarted.session.revision,
    at: 10_600,
  });
  assert.equal(cancelled.session.status, 'CANCELLED');

  await assert.rejects(
    () => lifecycle.complete({
      sessionId: sessionDescriptor.id,
      runId: thirdRun.id,
      expectedSessionRevision: thirdStarted.session.revision,
      run: completedRun(thirdRun, 10_650, zeroUsage),
      sessionStatus: 'COMPLETED',
      finalizedMessages: [assistantMessage('pi-message-stale', thirdRun.id, 'Stale completion.', 10_640)],
      toolExecutions: [],
      artifacts: [findingArtifact('pi-artifact-stale', thirdRun.id, 10_650)],
    }),
    (error) => error instanceof AgentSessionLifecycleError && error.code === 'RUN_STALE',
  );

  const productTables = [
    'agent_sessions',
    'agent_runs',
    'agent_messages',
    'agent_tool_executions',
    'agent_artifacts',
  ];
  const columns = await operationsPool.query(
    `SELECT table_name, column_name
     FROM information_schema.columns
     WHERE table_schema = 'agent_operations'
       AND table_name = ANY($1::text[])
     ORDER BY table_name, ordinal_position`,
    [productTables],
  );
  assert.deepEqual([...new Set(columns.rows.map(({ table_name }) => table_name))].sort(), [...productTables].sort());
  const forbiddenColumns = new Set([
    'checkpoint',
    'opaque_state',
    'pi_state',
    'pi_message',
    'reasoning',
    'thinking',
    'provider_payload',
    'transcript',
  ]);
  assert.equal(columns.rows.some(({ column_name }) => forbiddenColumns.has(column_name)), false);

  const persistedText = await operationsPool.query(
    `SELECT
       COALESCE(string_agg(content, ' '), '') AS messages,
       COALESCE((SELECT string_agg(artifact_payload::text, ' ') FROM agent_operations.agent_artifacts WHERE session_id = $1), '') AS artifacts
     FROM agent_operations.agent_messages
     WHERE session_id = $1`,
    [sessionDescriptor.id],
  );
  assert.equal(JSON.stringify(persistedText.rows).includes('UNCOMMITTED_STREAM_TEXT'), false);
  assert.equal(JSON.stringify(persistedText.rows).includes('legacy checkpoint text'), false);
});

test('PostgreSQL serializes concurrent replacement starts to one durable active Run', async (t) => {
  const persistence = createPersistence();
  t.after(() => persistence.close());
  const first = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const second = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const descriptor = Object.freeze({
    id: 'pi-session-concurrency-1',
    tenantId: 'tenant-postgres-pi',
    siteId: 'site-postgres-pi',
    agentDefinitionId: 'operations-investigation.v1',
    createdBy: 'principal-postgres-pi',
    createdAt: 20_000,
  });
  const initialRun = Object.freeze({
    id: 'pi-run-concurrency-initial',
    sessionId: descriptor.id,
    modelRef,
    status: 'RUNNING',
    startedAt: 20_100,
    finishedAt: null,
    usage: zeroUsage,
    failureCode: null,
  });
  await first.start({
    session: descriptor,
    expectedSessionRevision: null,
    run: initialRun,
    operatorMessage: Object.freeze({
      id: 'pi-message-concurrency-initial',
      sessionId: descriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: 'Start the first Run.',
      createdAt: 20_050,
    }),
  });
  const interrupted = await first.interrupt({
    sessionId: descriptor.id,
    runId: initialRun.id,
    at: 20_200,
  });

  const replacement = (suffix, at) => ({
    session: descriptor,
    expectedSessionRevision: interrupted.session.revision,
    run: Object.freeze({
      id: `pi-run-concurrency-${suffix}`,
      sessionId: descriptor.id,
      modelRef,
      status: 'RUNNING',
      startedAt: at,
      finishedAt: null,
      usage: zeroUsage,
      failureCode: null,
    }),
    operatorMessage: Object.freeze({
      id: `pi-message-concurrency-${suffix}`,
      sessionId: descriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: `Replacement ${suffix}.`,
      createdAt: at - 1,
    }),
  });
  const outcomes = await Promise.allSettled([
    first.start(replacement('a', 20_300)),
    second.start(replacement('b', 20_301)),
  ]);

  assert.equal(outcomes.filter(({ status }) => status === 'fulfilled').length, 1);
  assert.equal(outcomes.filter(({ status }) => status === 'rejected').length, 1);
  const rejected = outcomes.find(({ status }) => status === 'rejected');
  assert.ok(
    rejected?.status === 'rejected'
      && rejected.reason instanceof AgentSessionLifecycleError
      && rejected.reason.code === 'SESSION_REVISION_CONFLICT',
  );
  const durable = await first.get(descriptor.id);
  assert.equal(durable.runs.filter(({ status }) => status === 'RUNNING').length, 1);
  assert.equal(durable.session.status, 'ACTIVE');
  assert.equal(durable.session.activeRunId, durable.runs.find(({ status }) => status === 'RUNNING').id);
});

test('PostgreSQL rolls back a terminal Session transition when finalized append fails', async (t) => {
  const persistence = createPersistence();
  t.after(() => persistence.close());
  const lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const sourceDescriptor = Object.freeze({
    id: 'pi-session-atomic-source',
    tenantId: 'tenant-postgres-pi',
    siteId: 'site-postgres-pi',
    agentDefinitionId: 'operations-investigation.v1',
    createdBy: 'principal-postgres-pi',
    createdAt: 30_000,
  });
  const sourceRun = Object.freeze({
    id: 'pi-run-atomic-source',
    sessionId: sourceDescriptor.id,
    modelRef,
    status: 'RUNNING',
    startedAt: 30_100,
    finishedAt: null,
    usage: zeroUsage,
    failureCode: null,
  });
  await lifecycle.start({
    session: sourceDescriptor,
    expectedSessionRevision: null,
    run: sourceRun,
    operatorMessage: Object.freeze({
      id: 'pi-message-global-conflict',
      sessionId: sourceDescriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: 'Reserve a globally unique Message identity.',
      createdAt: 30_050,
    }),
  });

  const targetDescriptor = Object.freeze({
    ...sourceDescriptor,
    id: 'pi-session-atomic-target',
    createdAt: 31_000,
  });
  const targetRun = Object.freeze({
    ...sourceRun,
    id: 'pi-run-atomic-target',
    sessionId: targetDescriptor.id,
    startedAt: 31_100,
  });
  const targetStarted = await lifecycle.start({
    session: targetDescriptor,
    expectedSessionRevision: null,
    run: targetRun,
    operatorMessage: Object.freeze({
      id: 'pi-message-atomic-target-operator',
      sessionId: targetDescriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: 'Attempt a terminal commit.',
      createdAt: 31_050,
    }),
  });
  const terminalRun = Object.freeze({
    ...targetRun,
    status: 'COMPLETED',
    finishedAt: 31_300,
  });

  await assert.rejects(
    () => lifecycle.complete({
      sessionId: targetDescriptor.id,
      runId: targetRun.id,
      expectedSessionRevision: targetStarted.session.revision,
      run: terminalRun,
      sessionStatus: 'COMPLETED',
      finalizedMessages: [Object.freeze({
        id: 'pi-message-global-conflict',
        sessionId: targetDescriptor.id,
        runId: targetRun.id,
        role: 'ASSISTANT',
        content: 'This append must fail at the database uniqueness boundary.',
        createdAt: 31_250,
      })],
      toolExecutions: [],
      artifacts: [Object.freeze({
        ...findingArtifact('pi-artifact-atomic-target', targetRun.id, 31_300),
        sessionId: targetDescriptor.id,
      })],
    }),
    (error) => typeof error === 'object' && error !== null && error.code === '23505',
  );

  const afterFailure = await lifecycle.get(targetDescriptor.id);
  assert.equal(afterFailure.session.status, 'ACTIVE');
  assert.equal(afterFailure.session.revision, 0);
  assert.equal(afterFailure.session.activeRunId, targetRun.id);
  assert.equal(afterFailure.runs[0].status, 'RUNNING');
  assert.equal(afterFailure.messages.length, 1);
  assert.equal(afterFailure.artifacts.length, 0);
});

test('PostgreSQL persists typed INPUT_RESPONSE attribution across restart', async (t) => {
  let persistence = createPersistence();
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await Promise.all([persistence.close(), operationsPool.end()]);
  });
  let lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const descriptor = Object.freeze({
    id: 'pi-session-input-response-1',
    tenantId: 'tenant-postgres-pi',
    siteId: 'site-postgres-pi',
    agentDefinitionId: 'operations-investigation.v1',
    createdBy: 'principal-postgres-pi',
    createdAt: 40_000,
  });
  const firstRun = Object.freeze({
    id: 'pi-run-input-request-1',
    sessionId: descriptor.id,
    modelRef,
    status: 'RUNNING',
    startedAt: 40_100,
    finishedAt: null,
    usage: zeroUsage,
    failureCode: null,
  });
  const started = await lifecycle.start({
    session: descriptor,
    expectedSessionRevision: null,
    run: firstRun,
    operatorMessage: Object.freeze({
      id: 'pi-message-input-request-1',
      sessionId: descriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: 'Ask me for the operating schedule.',
      createdAt: 40_050,
    }),
  });
  const request = Object.freeze({
    id: 'pi-artifact-input-request-durable',
    sessionId: descriptor.id,
    runId: firstRun.id,
    kind: 'INPUT_REQUEST',
    request: Object.freeze({
      prompt: 'Which schedule applies?',
      response: Object.freeze({
        kind: 'SINGLE_SELECT',
        choices: Object.freeze([
          Object.freeze({ value: 'weekday', label: 'Weekday' }),
          Object.freeze({ value: 'holiday', label: 'Holiday' }),
        ]),
      }),
    }),
    createdAt: 40_200,
  });
  const waiting = await lifecycle.complete({
    sessionId: descriptor.id,
    runId: firstRun.id,
    expectedSessionRevision: started.session.revision,
    run: completedRun(firstRun, 40_200, zeroUsage),
    sessionStatus: 'WAITING_FOR_INPUT',
    finalizedMessages: [],
    toolExecutions: [],
    artifacts: [request],
  });
  const secondRun = Object.freeze({
    id: 'pi-run-input-response-1',
    sessionId: descriptor.id,
    modelRef,
    status: 'RUNNING',
    startedAt: 40_300,
    finishedAt: null,
    usage: zeroUsage,
    failureCode: null,
  });
  const response = Object.freeze({
    id: 'pi-artifact-input-response-durable',
    sessionId: descriptor.id,
    runId: secondRun.id,
    kind: 'INPUT_RESPONSE',
    requestArtifactId: request.id,
    value: 'weekday',
    submittedBy: 'principal-authenticated-input',
    createdAt: 40_300,
  });
  const continued = await lifecycle.continueWithInput({
    sessionId: descriptor.id,
    expectedSessionRevision: waiting.session.revision,
    run: secondRun,
    operatorMessage: Object.freeze({
      id: 'pi-message-input-response-1',
      sessionId: descriptor.id,
      runId: null,
      role: 'OPERATOR',
      content: 'Weekday',
      createdAt: 40_300,
    }),
    inputResponse: response,
  });
  assert.equal(continued.session.status, 'ACTIVE');
  assert.equal(continued.artifacts.at(-1).kind, 'INPUT_RESPONSE');

  await persistence.close();
  persistence = createPersistence();
  lifecycle = createAgentSessionLifecycle({ store: persistence.agentSessionStateStore });
  const recovered = await lifecycle.get(descriptor.id);
  assert.deepEqual(recovered, continued);
  assert.deepEqual(recovered.artifacts.find(({ id }) => id === response.id), response);

  const row = await operationsPool.query(
    `SELECT kind, artifact_payload->>'requestArtifactId' AS request_artifact_id,
            artifact_payload->>'value' AS value,
            artifact_payload->>'submittedBy' AS submitted_by
     FROM agent_operations.agent_artifacts
     WHERE artifact_id = $1`,
    [response.id],
  );
  assert.deepEqual(row.rows, [{
    kind: 'INPUT_RESPONSE',
    request_artifact_id: request.id,
    value: 'weekday',
    submitted_by: 'principal-authenticated-input',
  }]);
});
