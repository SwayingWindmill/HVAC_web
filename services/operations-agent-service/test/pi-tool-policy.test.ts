import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createModels,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxToolCall,
} from '@earendil-works/pi-ai';

import {
  AgentToolError,
  INVESTIGATION_COMPLETE_TOOL_NAME,
} from '../dist/agent/index.js';
import { composePiAgentRuntime } from '../dist/runtime-pi/internal/model-runtime.js';
import { createPiTools } from '../dist/runtime-pi/internal/pi-tools.js';
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
    site: async () => ({ name: 'Plant', timezone: 'Asia/Shanghai' }),
    budget: runBudget,
    signal: new AbortController().signal,
    emit: (event) => events.push(event),
  });
  return { events, result, requests: runtime.requests };
};

const finishResponse = Object.freeze({
  parts: Object.freeze([{ type: 'tool-call', name: INVESTIGATION_COMPLETE_TOOL_NAME, arguments: completeUnable }]),
  stopReason: 'toolUse',
});

test('runtime removes unauthorized project Tools before constructing the Pi Agent', async () => {
  let calls = 0;
  const restricted = projectTool({
    name: 'assets_list',
    requiredCapabilities: ['asset.list'],
    execute: async () => { calls += 1; return { unexpected: true }; },
  });
  const { result } = await run({
    tools: [restricted],
    responses: [
      { parts: [{ type: 'tool-call', name: 'assets_list', arguments: {} }], stopReason: 'toolUse' },
      finishResponse,
    ],
  });

  assert.equal(calls, 0);
  const execution = result.toolExecutions.find(({ toolName }) => toolName === 'assets_list');
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

test('project Tool concurrency budget queues a wider batch instead of failing the Run', async () => {
  // A real model sent six reads at once against a limit of four; the excess waits for a slot.
  let active = 0;
  let widest = 0;
  const read = (name) => projectTool({
    name,
    execute: async () => {
      active += 1;
      widest = Math.max(widest, active);
      await new Promise((resolve) => setTimeout(resolve, 10));
      active -= 1;
      return { ok: true };
    },
  });

  const { result } = await run({
    tools: [read('site.first'), read('site.second'), read('site.third')],
    runBudget: { ...budget, maxToolCalls: 3, maxParallelToolCalls: 2 },
    responses: [
      {
        parts: [
          { type: 'tool-call', name: 'site.first', arguments: {} },
          { type: 'tool-call', name: 'site.second', arguments: {} },
          { type: 'tool-call', name: 'site.third', arguments: {} },
        ],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  assert.equal(widest, 2);
  assert.deepEqual(result.toolExecutions.filter(({ toolName }) => toolName.startsWith('site.')).map(({ status }) => status), ['COMPLETED', 'COMPLETED', 'COMPLETED']);
  assert.equal(result.runStatus, 'COMPLETED');
});

test('AgentToolError code is preserved while sensitive Tool error text is never projected', async () => {
  const failing = projectTool({
    name: 'site_get_context',
    execute: async () => {
      throw new AgentToolError('TOOL_OWNER_UNAVAILABLE', 'secret owner hostname and credential fragment');
    },
  });
  const { events, result } = await run({
    tools: [failing],
    responses: [
      { parts: [{ type: 'tool-call', name: 'site_get_context', arguments: {} }], stopReason: 'toolUse' },
      finishResponse,
    ],
  });

  const execution = result.toolExecutions.find(({ toolName }) => toolName === 'site_get_context');
  assert.equal(execution?.failureCode, 'TOOL_OWNER_UNAVAILABLE');
  assert.equal(JSON.stringify(events).includes('secret owner hostname'), false);
});

test('a Tool refusal reaches the model with what to change, never its internal message', async () => {
  const refusing = projectTool({
    name: 'site_get_context',
    execute: async () => {
      throw new AgentToolError('TOOL_ARGUMENTS_INVALID', 'internal parser detail', '日期用站点本地日期 YYYY-MM-DD，首尾都包含。');
    },
  });
  const { events, result, requests } = await run({
    tools: [refusing],
    responses: [
      { parts: [{ type: 'tool-call', name: 'site_get_context', arguments: {} }], stopReason: 'toolUse' },
      finishResponse,
    ],
  });

  const seen = requests[1].filter((entry) => entry.role === 'toolResult').map((entry) => entry.text).join(' ');
  assert.match(seen, /TOOL_ARGUMENTS_INVALID/u);
  assert.match(seen, /日期用站点本地日期 YYYY-MM-DD，首尾都包含。/u);
  assert.equal(seen.includes('internal parser detail'), false);
  assert.equal(JSON.stringify(events).includes('internal parser detail'), false);
  assert.equal(result.toolExecutions.find(({ toolName }) => toolName === 'site_get_context')?.failureCode, 'TOOL_ARGUMENTS_INVALID');
});

const runCodemode = async ({ responses, tools, runBudget = budget, runContext = context }) => {
  const faux = fauxProvider();
  faux.setResponses(responses.map((response) => fauxAssistantMessage(
    response.parts.map((part) => (
      part.type === 'text'
        ? fauxText(part.text)
        : fauxToolCall(part.name, part.arguments, part.id === undefined ? {} : { id: part.id })
    )),
    { stopReason: response.stopReason },
  )));
  const models = createModels();
  models.setProvider(faux.provider);
  const requests = [];
  const observedModels = new Proxy(models, {
    get(target, property, receiver) {
      if (property === 'streamSimple') {
        return (requestModel, requestContext, options) => {
          requests.push(requestContext.messages.map((entry) => ({
            role: entry.role,
            text: typeof entry.content === 'string'
              ? entry.content
              : entry.content.filter((part) => part.type === 'text').map((part) => part.text).join('\n'),
          })));
          return models.streamSimple(requestModel, requestContext, options);
        };
      }
      const value = Reflect.get(target, property, receiver);
      return typeof value === 'function' ? value.bind(target) : value;
    },
  });
  const runtime = composePiAgentRuntime({
    models: observedModels,
    model: faux.getModel(),
    thinkingLevel: 'off',
    timeoutMs: 30_000,
    maxOutputTokens: 2_048,
    toolExecutionMode: 'codemode',
  });
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
    site: async () => ({ name: 'Plant', timezone: 'Asia/Shanghai' }),
    budget: runBudget,
    signal: new AbortController().signal,
    emit: (event) => events.push(event),
  });
  return { events, result, requests };
};

test('codemode enforces maxToolCalls budget inside QuickJS and aborts the Run with TOOL_CALL_LIMIT', async () => {
  let calls = 0;
  const first = projectTool({ name: 'site_first', execute: async () => { calls += 1; return { ok: true }; } });
  const second = projectTool({ name: 'site_second', execute: async () => { calls += 1; return { ok: true }; } });
  const { result } = await runCodemode({
    tools: [first, second],
    runBudget: { ...budget, maxToolCalls: 1, maxParallelToolCalls: 2 },
    responses: [
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-limit',
          name: 'codemode',
          arguments: { code: 'return await Promise.all([tools.site_first({}), tools.site_second({})]);' },
        }],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  assert.equal(calls, 1);
  assert.equal(result.runStatus, 'FAILED');
  assert.equal(result.failureCode, 'TOOL_CALL_LIMIT');
  assert.equal(result.usage.modelCalls, 1);
  assert.equal(result.toolExecutions.find(({ toolName }) => toolName === 'site_first')?.status, 'COMPLETED');
  assert.equal(result.toolExecutions.find(({ toolName }) => toolName === 'site_second')?.failureCode, 'TOOL_CALL_LIMIT');
});

test('codemode queues concurrent QuickJS Promise.all calls within maxParallelToolCalls', async () => {
  let active = 0;
  let widest = 0;
  const read = (name) => projectTool({
    name,
    execute: async () => {
      active += 1;
      widest = Math.max(widest, active);
      await new Promise((resolve) => setTimeout(resolve, 15));
      active -= 1;
      return { ok: true };
    },
  });

  const { result } = await runCodemode({
    tools: [read('site_first'), read('site_second'), read('site_third')],
    runBudget: { ...budget, maxToolCalls: 3, maxParallelToolCalls: 2 },
    responses: [
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-queue',
          name: 'codemode',
          arguments: {
            code: 'return await Promise.all([tools.site_first({}), tools.site_second({}), tools.site_third({})]);',
          },
        }],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  assert.equal(widest, 2);
  assert.equal(result.runStatus, 'COMPLETED');
  assert.deepEqual(
    result.toolExecutions.filter(({ toolName }) => toolName.startsWith('site_')).map(({ status }) => status),
    ['COMPLETED', 'COMPLETED', 'COMPLETED'],
  );
});

test('codemode respects maxToolResultBytes and safe AgentToolError hints without leaking internal error text', async () => {
  const oversized = projectTool({
    name: 'site_big',
    execute: async () => ({ blob: 'x'.repeat(2_000) }),
  });
  const { result: tooLargeResult } = await runCodemode({
    tools: [oversized],
    runBudget: { ...budget, maxToolResultBytes: 256 },
    responses: [
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-big',
          name: 'codemode',
          arguments: { code: 'try { return await tools.site_big({}); } catch (err) { return err.message; }' },
        }],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  assert.equal(tooLargeResult.runStatus, 'FAILED');
  assert.equal(tooLargeResult.failureCode, 'TOOL_RESULT_TOO_LARGE');
  assert.equal(tooLargeResult.toolExecutions.find(({ toolName }) => toolName === 'site_big')?.failureCode, 'TOOL_RESULT_TOO_LARGE');

  const refusing = projectTool({
    name: 'site_refuse',
    execute: async () => {
      throw new AgentToolError('TOOL_ARGUMENTS_INVALID', 'internal secret parser stack', '日期用站点本地日期 YYYY-MM-DD。');
    },
  });
  const { events, result: refusedResult, requests } = await runCodemode({
    tools: [refusing],
    responses: [
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-refuse',
          name: 'codemode',
          arguments: { code: 'return await tools.site_refuse({});' },
        }],
        stopReason: 'toolUse',
      },
      finishResponse,
    ],
  });

  const seen = requests[1].filter((entry) => entry.role === 'toolResult').map((entry) => entry.text).join(' ');
  assert.match(seen, /TOOL_ARGUMENTS_INVALID/u);
  assert.match(seen, /日期用站点本地日期 YYYY-MM-DD。/u);
  assert.equal(seen.includes('internal secret parser stack'), false);
  assert.equal(JSON.stringify(events).includes('internal secret parser stack'), false);
  assert.equal(refusedResult.toolExecutions.find(({ toolName }) => toolName === 'site_refuse')?.failureCode, 'TOOL_ARGUMENTS_INVALID');
});

test('codemode sandbox enforces timeoutMs on runaway QuickJS scripts cleanly', async () => {
  const tools = createPiTools({
    tools: [],
    context,
    budget: { ...budget, maxWallClockMs: 80 },
    runSignal: new AbortController().signal,
    sessionId: session.id,
    runId: session.activeRunId,
    toolExecutionMode: 'codemode',
    onArtifact: () => {},
    onBudgetExhausted: () => {},
    answers: () => [],
    keptAnswer: () => '',
    onRefused: () => {},
  });
  const codemodeTool = tools.find(({ name }) => name === 'codemode');
  assert.ok(codemodeTool);

  await assert.rejects(
    () => codemodeTool.execute('cm-timeout', { code: 'while (true) {}' }),
    /timed out/iu,
  );
});
