import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
} from '../dist/agent/index.js';
import { createScriptedPiAgentEngine } from '../dist/runtime-pi/testing.js';

const session = Object.freeze({
  id: 'session-policy-1',
  tenantId: 'tenant-1',
  siteId: 'site-1',
  agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-1',
  status: 'ACTIVE',
  revision: 1,
  activeRunId: 'run-policy-1',
  createdAt: 1,
  updatedAt: 1,
});

const context = Object.freeze({
  tenantId: 'tenant-1',
  siteId: 'site-1',
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read']),
  sessionId: 'session-policy-1',
  runId: 'run-policy-1',
  correlationId: 'correlation-1',
});

const budget = Object.freeze({
  maxModelCalls: 5,
  maxToolCalls: 8,
  maxWallClockMs: 30_000,
  maxParallelToolCalls: 4,
  maxQueryRangeMs: 86_400_000,
  maxToolResultRecords: 100,
  maxToolResultBytes: 64_000,
  maxInputTokens: 8_000,
  maxOutputTokens: 2_000,
});

const message = Object.freeze({
  id: 'message-1',
  sessionId: session.id,
  runId: null,
  role: 'OPERATOR',
  content: 'Investigate the Site.',
  createdAt: 1,
});

const completeUnable = Object.freeze({
  outcome: 'UNABLE_TO_CONCLUDE',
  summary: 'The available evidence was insufficient.',
  evidenceRefs: Object.freeze([]),
  limitations: Object.freeze(['One or more reads were unavailable.']),
  recommendedNext: Object.freeze(['Retry the investigation.']),
});

const projectTool = ({ name, requiredCapabilities = ['site.read'], execute }) => Object.freeze({
  definition: Object.freeze({
    name,
    description: `Test ${name}.`,
    inputSchema: Object.freeze({ type: 'object', properties: Object.freeze({}), additionalProperties: false }),
    executionMode: 'parallel',
    replayPolicy: 'safe',
    requiredCapabilities: Object.freeze(requiredCapabilities),
  }),
  execute,
});

const run = async ({ responses, tools, runBudget = budget, runContext = context }) => {
  const runtime = createScriptedPiAgentEngine({ responses });
  const events = [];
  const result = await runtime.engine({
    session,
    run: Object.freeze({
      id: session.activeRunId,
      sessionId: session.id,
      modelRef: runtime.modelRef,
      status: 'RUNNING',
      startedAt: 2,
      finishedAt: null,
      usage: Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 }),
      failureCode: null,
    }),
    messages: [message],
    tools,
    context: runContext,
    budget: runBudget,
    signal: new AbortController().signal,
    emit: (event) => events.push(event),
  });
  return { events, result };
};

const finishResponse = Object.freeze({
  parts: Object.freeze([{ type: 'tool-call', name: INVESTIGATION_COMPLETE_TOOL_NAME, arguments: completeUnable }]),
  stopReason: 'toolUse',
});

test('runtime removes unauthorized project Tools before constructing the Pi Agent', async () => {
  let calls = 0;
  const restricted = projectTool({
    name: 'assets.list',
    requiredCapabilities: ['asset.list'],
    execute: async () => { calls += 1; return { unexpected: true }; },
  });
  const { result } = await run({
    tools: [restricted],
    responses: [
      { parts: [{ type: 'tool-call', name: 'assets.list', arguments: {} }], stopReason: 'toolUse' },
      finishResponse,
    ],
  });

  assert.equal(calls, 0);
  const execution = result.toolExecutions.find(({ toolName }) => toolName === 'assets.list');
  assert.equal(execution?.status, 'FAILED');
});

test('project Tool call budget fails excess calls with a stable project failure code', async () => {
  let calls = 0;
  const first = projectTool({ name: 'site.first', execute: async () => { calls += 1; return { ok: true }; } });
  const second = projectTool({ name: 'site.second', execute: async () => { calls += 1; return { ok: true }; } });
  const { result } = await run({
    tools: [first, second],
    runBudget: { ...budget, maxToolCalls: 1, maxParallelToolCalls: 2 },
    responses: [
      {
        parts: [
          { type: 'tool-call', name: 'site.first', arguments: {} },
          { type: 'tool-call', name: 'site.second', arguments: {} },
        ],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  assert.equal(calls, 1);
  const failures = result.toolExecutions.filter(({ failureCode }) => failureCode === 'TOOL_CALL_LIMIT');
  assert.equal(failures.length, 1);
});

test('project Tool concurrency budget rejects excess parallel execution without serializing the batch', async () => {
  let releaseFirst;
  let firstStarted;
  const firstStartedPromise = new Promise((resolve) => { firstStarted = resolve; });
  const releasePromise = new Promise((resolve) => { releaseFirst = resolve; });
  const first = projectTool({
    name: 'site.first',
    execute: async () => {
      firstStarted();
      await releasePromise;
      return { ok: true };
    },
  });
  const second = projectTool({
    name: 'site.second',
    execute: async () => {
      await firstStartedPromise;
      return { ok: true };
    },
  });
  setTimeout(() => releaseFirst(), 20);

  const { result } = await run({
    tools: [first, second],
    runBudget: { ...budget, maxToolCalls: 2, maxParallelToolCalls: 1 },
    responses: [
      {
        parts: [
          { type: 'tool-call', name: 'site.first', arguments: {} },
          { type: 'tool-call', name: 'site.second', arguments: {} },
        ],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  const failures = result.toolExecutions.filter(({ failureCode }) => failureCode === 'TOOL_CONCURRENCY_LIMIT');
  assert.equal(failures.length, 1);
});

test('AgentToolError code is preserved while sensitive Tool error text is never projected', async () => {
  const failing = projectTool({
    name: 'site.get_context',
    execute: async () => {
      throw new AgentToolError('TOOL_OWNER_UNAVAILABLE', 'secret owner hostname and credential fragment');
    },
  });
  const { events, result } = await run({
    tools: [failing],
    responses: [
      { parts: [{ type: 'tool-call', name: 'site.get_context', arguments: {} }], stopReason: 'toolUse' },
      finishResponse,
    ],
  });

  const execution = result.toolExecutions.find(({ toolName }) => toolName === 'site.get_context');
  assert.equal(execution?.failureCode, 'TOOL_OWNER_UNAVAILABLE');
  assert.equal(JSON.stringify(events).includes('secret owner hostname'), false);
});
