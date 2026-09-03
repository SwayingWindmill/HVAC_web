import assert from 'node:assert/strict';
import test from 'node:test';

import {
  INVESTIGATION_COMPLETE_TOOL_NAME,
  HVAC_AGENT_EVENT_VERSION,
} from '../dist/agent/index.js';
import { createScriptedPiAgentEngine } from '../dist/runtime-pi/testing.js';

const baseSession = Object.freeze({
  id: 'session-pi-001',
  tenantId: 'tenant-001',
  siteId: 'site-001',
  agentDefinitionId: 'operations-investigation.v1',
  createdBy: 'principal-001',
  status: 'ACTIVE',
  revision: 1,
  activeRunId: 'run-pi-001',
  createdAt: 1_000,
  updatedAt: 1_000,
});

const baseBudget = Object.freeze({
  maxModelCalls: 4,
  maxToolCalls: 4,
  maxWallClockMs: 30_000,
  maxParallelToolCalls: 2,
  maxQueryRangeMs: 86_400_000,
  maxToolResultRecords: 100,
  maxToolResultBytes: 64_000,
  maxInputTokens: 8_000,
  maxOutputTokens: 2_000,
});

const baseContext = Object.freeze({
  tenantId: 'tenant-001',
  siteId: 'site-001',
  principalId: 'principal-001',
  capabilities: Object.freeze(['site.read']),
  sessionId: 'session-pi-001',
  runId: 'run-pi-001',
  correlationId: 'correlation-pi-001',
});

const operatorMessage = Object.freeze({
  id: 'message-user-001',
  sessionId: 'session-pi-001',
  runId: null,
  role: 'OPERATOR',
  content: 'Why was overnight energy behavior abnormal?',
  createdAt: 1_000,
});

const createRun = (modelRef) => Object.freeze({
  id: 'run-pi-001',
  sessionId: 'session-pi-001',
  modelRef,
  status: 'RUNNING',
  startedAt: 1_100,
  finishedAt: null,
  usage: Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 0, toolCalls: 0 }),
  failureCode: null,
});

const siteContextTool = (execute) => Object.freeze({
  definition: Object.freeze({
    name: 'site.get_context',
    description: 'Read the current Site context.',
    inputSchema: Object.freeze({
      type: 'object',
      properties: Object.freeze({}),
      additionalProperties: false,
    }),
    executionMode: 'parallel',
    replayPolicy: 'safe',
    requiredCapabilities: Object.freeze(['site.read']),
  }),
  execute,
});

const completion = Object.freeze({
  outcome: 'SUPPORTED_FINDING',
  summary: 'The Site remained above its expected overnight load after the shutdown window.',
  evidenceRefs: Object.freeze([
    Object.freeze({
      owner: 'ENERGY',
      resourceType: 'period-comparison',
      resourceId: 'comparison-001',
      revision: 'rev-1',
      toolExecutionId: 'read-site-context',
    }),
  ]),
  limitations: Object.freeze([]),
  recommendedNext: Object.freeze(['Inspect the overnight schedule.']),
});

const runEngine = async ({ scripted, tool, controller = new AbortController(), onEvent }) => {
  const runtime = createScriptedPiAgentEngine(scripted);
  const events = [];
  const result = await runtime.engine({
    session: baseSession,
    run: createRun(runtime.modelRef),
    messages: [operatorMessage],
    tools: [tool],
    context: baseContext,
    budget: baseBudget,
    signal: controller.signal,
    emit: (event) => {
      events.push(event);
      onEvent?.(event, controller);
    },
  });
  return { events, result };
};

test('Pi Agent executes the project Tool loop and terminates through investigation.complete', async () => {
  let siteReads = 0;
  const { events, result } = await runEngine({
    scripted: {
      responses: [
        {
          parts: [
            { type: 'thinking', text: 'I should inspect the Site context before concluding.' },
            { type: 'tool-call', id: 'read-site-context', name: 'site.get_context', arguments: {} },
          ],
          stopReason: 'toolUse',
        },
        {
          parts: [
            { type: 'tool-call', name: INVESTIGATION_COMPLETE_TOOL_NAME, arguments: completion },
          ],
          stopReason: 'toolUse',
        },
      ],
    },
    tool: siteContextTool(async ({ context }) => {
      siteReads += 1;
      return { siteId: context.siteId, timezone: 'Asia/Shanghai' };
    }),
  });

  assert.equal(siteReads, 1);
  assert.equal(result.runStatus, 'COMPLETED');
  assert.equal(result.sessionStatus, 'COMPLETED');
  assert.equal(result.usage.modelCalls, 2);
  assert.equal(result.usage.toolCalls, 2);
  assert.equal(result.artifacts.length, 1);
  assert.equal(result.artifacts[0].kind, 'FINDING');
  assert.equal(result.artifacts[0].finding.outcome, 'SUPPORTED_FINDING');
  assert.deepEqual(
    result.toolExecutions.map(({ toolName, status }) => [toolName, status]),
    [
      ['site.get_context', 'COMPLETED'],
      [INVESTIGATION_COMPLETE_TOOL_NAME, 'COMPLETED'],
    ],
  );
  assert.ok(events.every((event) => event.version === HVAC_AGENT_EVENT_VERSION));
  assert.ok(events.some((event) => event.type === 'artifact.created'));
  assert.ok(events.some((event) => event.type === 'run.completed'));
  assert.equal(JSON.stringify(events).includes('I should inspect the Site context before concluding.'), false);
});

test('a thrown project READ Tool is observed as a failed Tool execution, not valid empty data', async () => {
  const unableToConclude = {
    outcome: 'UNABLE_TO_CONCLUDE',
    summary: 'The Site context could not be read.',
    evidenceRefs: [],
    limitations: ['The Site context owner was unavailable.'],
    recommendedNext: ['Retry after the owner service recovers.'],
  };
  const { result } = await runEngine({
    scripted: {
      responses: [
        {
          parts: [{ type: 'tool-call', name: 'site.get_context', arguments: {} }],
          stopReason: 'toolUse',
        },
        {
          parts: [{
            type: 'tool-call',
            name: INVESTIGATION_COMPLETE_TOOL_NAME,
            arguments: unableToConclude,
          }],
          stopReason: 'toolUse',
        },
      ],
    },
    tool: siteContextTool(async () => {
      throw new Error('owner unavailable');
    }),
  });

  const readExecution = result.toolExecutions.find(({ toolName }) => toolName === 'site.get_context');
  assert.equal(readExecution?.status, 'FAILED');
  assert.equal(readExecution?.failureCode, 'TOOL_EXECUTION_FAILED');
  assert.equal(result.artifacts[0].kind, 'FINDING');
  assert.equal(result.artifacts[0].finding.outcome, 'UNABLE_TO_CONCLUDE');
});

test('AbortSignal cancels an active Pi provider loop and returns a project-owned cancelled status', async () => {
  const controller = new AbortController();
  const { events, result } = await runEngine({
    scripted: {
      tokensPerSecond: 25,
      responses: [
        {
          parts: [{
            type: 'text',
            text: 'This intentionally long response exists only to exercise cancellation while streaming.',
          }],
          stopReason: 'stop',
        },
      ],
    },
    tool: siteContextTool(async () => ({ siteId: 'site-001' })),
    controller,
    onEvent: (event, activeController) => {
      if (event.type === 'assistant.delta') activeController.abort();
    },
  });

  assert.equal(result.runStatus, 'CANCELLED');
  assert.equal(result.sessionStatus, 'CANCELLED');
  assert.equal(events.some((event) => event.type === 'run.completed'), false);
  assert.ok(events.some((event) => event.type === 'run.failed'));
});
