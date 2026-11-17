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
  HVAC_AGENT_EVENT_VERSION,
} from '../dist/agent/index.js';
import { composePiAgentRuntime } from '../dist/runtime-pi/internal/model-runtime.js';
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
    name: 'site_get_context',
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
  evidenceRefs: Object.freeze([
    Object.freeze({
      owner: 'ENERGY',
      resourceType: 'period-comparison',
      resourceId: 'comparison-001',
      revision: 'rev-1',
      toolExecutionId: 'E1',
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
    site: async () => ({ name: 'Plant', timezone: 'Asia/Shanghai' }),
    budget: baseBudget,
    signal: controller.signal,
    emit: (event) => {
      events.push(event);
      onEvent?.(event, controller);
    },
  });
  return { events, result };
};

test('Pi runtime telemetry emits bounded run, model, and Tool metadata without prompt or payload content', async () => {
  const telemetryEvents = [];
  const privateToolValue = 'private-owner-payload-value';
  const { result } = await runEngine({
    scripted: {
      telemetry: { record: (event) => telemetryEvents.push(event) },
      responses: [
        {
          parts: [
            { type: 'thinking', text: 'private hidden reasoning text' },
            { type: 'tool-call', id: 'read-site-context', name: 'site_get_context', arguments: {} },
          ],
          stopReason: 'toolUse',
        },
        {
          parts: [{ type: 'tool-call', name: INVESTIGATION_COMPLETE_TOOL_NAME, arguments: completion }],
          stopReason: 'toolUse',
        },
      ],
    },
    tool: siteContextTool(async () => ({ value: privateToolValue })),
  });

  assert.equal(result.runStatus, 'COMPLETED');
  const types = telemetryEvents.map(({ type }) => type);
  assert.equal(types[0], 'run.started');
  assert.equal(types.filter((type) => type === 'model.started').length, 2);
  assert.equal(types.filter((type) => type === 'model.completed').length, 2);
  assert.equal(types.filter((type) => type === 'tool.started').length, 2);
  assert.equal(types.filter((type) => type === 'tool.completed').length, 2);
  assert.equal(types.at(-1), 'run.completed');
  const modelCompleted = telemetryEvents.find(({ type }) => type === 'model.completed');
  assert.equal(typeof modelCompleted.durationMs, 'number');
  assert.equal(typeof modelCompleted.inputTokens, 'number');
  assert.equal(typeof modelCompleted.outputTokens, 'number');
  const toolStarted = telemetryEvents.find(({ type, toolName }) => type === 'tool.started' && toolName === 'site_get_context');
  assert.equal(toolStarted.activeToolCalls, 1);
  const serialized = JSON.stringify(telemetryEvents);
  assert.doesNotMatch(serialized, /Why was overnight energy behavior abnormal|private hidden reasoning text|private-owner-payload-value|expected overnight load/u);
});

test('Pi Agent executes the project Tool loop and terminates through investigation_complete', async () => {
  let siteReads = 0;
  const { events, result } = await runEngine({
    scripted: {
      responses: [
        {
          parts: [
            { type: 'thinking', text: 'I should inspect the Site context before concluding.' },
            { type: 'tool-call', id: 'read-site-context', name: 'site_get_context', arguments: {} },
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
      ['site_get_context', 'COMPLETED'],
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
    evidenceRefs: [],
    limitations: ['The Site context owner was unavailable.'],
    recommendedNext: ['Retry after the owner service recovers.'],
  };
  const { result } = await runEngine({
    scripted: {
      responses: [
        {
          parts: [{ type: 'tool-call', name: 'site_get_context', arguments: {} }],
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

  const readExecution = result.toolExecutions.find(({ toolName }) => toolName === 'site_get_context');
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

test('a later Run reconstructs Pi context from committed finalized project Messages only', async () => {
  const runtime = createScriptedPiAgentEngine({
    responses: [{
      parts: [{
        type: 'tool-call',
        name: INVESTIGATION_COMPLETE_TOOL_NAME,
        arguments: {
          outcome: 'UNABLE_TO_CONCLUDE',
          evidenceRefs: [],
          limitations: ['A safe READ may be repeated after restart.'],
          recommendedNext: [],
        },
      }],
      stopReason: 'toolUse',
    }],
  });
  const previousOperator = Object.freeze({
    id: 'message-previous-operator',
    sessionId: baseSession.id,
    runId: null,
    role: 'OPERATOR',
    content: 'Investigate overnight energy use.',
    createdAt: 800,
  });
  const previousAssistant = Object.freeze({
    id: 'message-previous-assistant',
    sessionId: baseSession.id,
    runId: 'run-pi-previous',
    role: 'ASSISTANT',
    content: 'The first Run needs the expected weekday schedule before it can conclude.',
    createdAt: 900,
  });
  const currentOperator = Object.freeze({
    id: 'message-current-operator',
    sessionId: baseSession.id,
    runId: null,
    role: 'OPERATOR',
    content: 'Use the weekday schedule and continue.',
    createdAt: 1_000,
  });

  const result = await runtime.engine({
    session: baseSession,
    run: createRun(runtime.modelRef),
    messages: [previousOperator, previousAssistant, currentOperator],
    tools: [],
    context: baseContext,
    site: async () => ({ name: 'Plant', timezone: 'Asia/Shanghai' }),
    budget: baseBudget,
    signal: new AbortController().signal,
    emit: () => {},
  });

  assert.equal(result.runStatus, 'COMPLETED');
  // Since pi 0.86 the system policy travels as the first transcript message; the rest is the committed conversation.
  assert.equal(runtime.requests[0]?.[0]?.role, 'system');
  assert.deepEqual(runtime.requests[0]?.slice(1), [
    { role: 'user', text: previousOperator.content },
    { role: 'assistant', text: previousAssistant.content },
    { role: 'user', text: currentOperator.content },
  ]);
});

const runCodemodeEngine = async ({
  responses,
  tools,
  budget = baseBudget,
  telemetry,
  controller = new AbortController(),
  onEvent,
}) => {
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
        return (requestModel, context, options) => {
          requests.push(context.messages.map((message) => ({
            role: message.role,
            text: typeof message.content === 'string'
              ? message.content
              : message.content.filter((part) => part.type === 'text').map((part) => part.text).join('\n'),
          })));
          return models.streamSimple(requestModel, context, options);
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
    ...(telemetry === undefined ? {} : { telemetry }),
  });
  const events = [];
  const result = await runtime.engine({
    session: baseSession,
    run: createRun(runtime.modelRef),
    messages: [operatorMessage],
    tools,
    context: baseContext,
    site: async () => ({ name: '松雅轩', timezone: 'Asia/Shanghai' }),
    budget,
    signal: controller.signal,
    emit: (event) => {
      events.push(event);
      onEvent?.(event, controller);
    },
  });
  return { events, result, requests };
};

test('codemode executes Promise.all nested HVAC tools, emits real tool events, preserves evidenceRefs and chart, and suppresses process talk', async () => {
  const telemetryEvents = [];
  const hourlyChart = Object.freeze({
    kind: 'LINE',
    title: '10月15日用电',
    unit: 'kWh',
    categories: ['08:00', '09:00'],
    series: [{ name: '用电', values: [120, 140] }],
  });
  const energyTool = Object.freeze({
    definition: Object.freeze({
      name: 'energy_query_series',
      description: 'Read energy series.',
      inputSchema: Object.freeze({
        type: 'object',
        properties: Object.freeze({ startDate: Object.freeze({ type: 'string' }) }),
        required: Object.freeze(['startDate']),
        additionalProperties: false,
      }),
      executionMode: 'parallel',
      replayPolicy: 'safe',
      requiredCapabilities: Object.freeze(['site.read']),
    }),
    execute: async () => ({
      totalKWh: 1634,
      rawIntermediateDetail: 'should-not-leak-back-to-conversation',
    }),
    chart: () => hourlyChart,
  });
  const alarmTool = Object.freeze({
    definition: Object.freeze({
      name: 'alarm_list',
      description: 'List active alarms.',
      inputSchema: Object.freeze({
        type: 'object',
        properties: Object.freeze({}),
        additionalProperties: false,
      }),
      executionMode: 'parallel',
      replayPolicy: 'safe',
      requiredCapabilities: Object.freeze(['site.read']),
    }),
    execute: async () => ({ alarms: [{ title: '1 号冷机回水温度高' }] }),
  });

  const finalAnswer = '**10 月 15 日用电 1634 kWh，目前有 1 条告警。**';
  const { events, result, requests } = await runCodemodeEngine({
    telemetry: { record: (event) => telemetryEvents.push(event) },
    tools: [energyTool, alarmTool],
    responses: [
      {
        parts: [
          { type: 'text', text: '先通过 codemode 查一下用电和告警。' },
          {
            type: 'tool-call',
            id: 'cm-call-1',
            name: 'codemode',
            arguments: {
              code: [
                'const [energy, alarms] = await Promise.all([',
                '  tools.energy_query_series({ startDate: "2026-10-15" }),',
                '  tools.alarm_list({}),',
                ']);',
                'return {',
                '  energyId: energy.evidenceId,',
                '  alarmId: alarms.evidenceId,',
                '  totalKWh: energy.result.totalKWh,',
                '  alarmCount: alarms.result.alarms.length,',
                '};',
              ].join('\n'),
            },
          },
        ],
        stopReason: 'toolUse',
      },
      {
        parts: [
          { type: 'text', text: finalAnswer },
          {
            type: 'tool-call',
            id: 'complete-1',
            name: INVESTIGATION_COMPLETE_TOOL_NAME,
            arguments: {
              outcome: 'SUPPORTED_FINDING',
              evidenceRefs: [
                { owner: 'ENERGY', resourceType: 'energy-series', resourceId: 'day-2026-10-15', toolExecutionId: 'E1' },
                { owner: 'ALARM', resourceType: 'alarm-list', resourceId: 'active', toolExecutionId: 'E2' },
              ],
              keyFigures: [{ label: '10月15日用电', value: '1634', unit: 'kWh' }],
              limitations: [],
              recommendedNext: ['查看 1 号冷机告警详情。'],
              chartFrom: 'E1',
            },
          },
        ],
        stopReason: 'toolUse',
      },
    ],
  });

  assert.equal(result.runStatus, 'COMPLETED');
  assert.match(requests[0][0].text, /codemode/u);

  const codemodeToolResult = requests[1].find((message) => message.role === 'toolResult');
  assert.ok(codemodeToolResult);
  assert.deepEqual(JSON.parse(codemodeToolResult.text), {
    energyId: 'E1',
    alarmId: 'E2',
    totalKWh: 1634,
    alarmCount: 1,
  });
  assert.equal(codemodeToolResult.text.includes('should-not-leak-back-to-conversation'), false);

  assert.deepEqual(
    result.toolExecutions.map(({ toolName, status }) => [toolName, status]),
    [
      ['energy_query_series', 'COMPLETED'],
      ['alarm_list', 'COMPLETED'],
      [INVESTIGATION_COMPLETE_TOOL_NAME, 'COMPLETED'],
    ],
  );
  assert.equal(result.toolExecutions.some(({ toolName }) => toolName === 'codemode'), false);
  assert.equal(result.usage.toolCalls, 3);

  const startedTools = events
    .filter((event) => event.type === 'tool.started')
    .map((event) => event.payload.toolName);
  const completedTools = events
    .filter((event) => event.type === 'tool.completed')
    .map((event) => event.payload.toolExecution.toolName);
  assert.deepEqual(startedTools, ['energy_query_series', 'alarm_list', INVESTIGATION_COMPLETE_TOOL_NAME]);
  assert.deepEqual(completedTools, ['energy_query_series', 'alarm_list', INVESTIGATION_COMPLETE_TOOL_NAME]);
  assert.equal(telemetryEvents.some((event) => 'toolName' in event && event.toolName === 'codemode'), false);

  const energyExec = result.toolExecutions.find(({ toolName }) => toolName === 'energy_query_series');
  const alarmExec = result.toolExecutions.find(({ toolName }) => toolName === 'alarm_list');
  assert.equal(result.artifacts[0].kind, 'FINDING');
  assert.deepEqual(
    result.artifacts[0].finding.evidenceRefs.map(({ toolExecutionId }) => toolExecutionId),
    [energyExec.id, alarmExec.id],
  );
  assert.deepEqual(result.artifacts[0].finding.chart, hourlyChart);
  assert.deepEqual(result.finalizedMessages.map(({ content }) => content), [finalAnswer]);
});

test('codemode QuickJS syntax or runtime errors feed back cleanly so pi retries and completes without recording a fake codemode execution', async () => {
  const { result, requests } = await runCodemodeEngine({
    tools: [siteContextTool(async () => ({ site: '松雅轩' }))],
    responses: [
      {
        parts: [{ type: 'tool-call', id: 'cm-syntax', name: 'codemode', arguments: { code: 'const broken = ;' } }],
        stopReason: 'toolUse',
      },
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-valid',
          name: 'codemode',
          arguments: { code: 'const site = await tools.site_get_context({}); return { id: site.evidenceId };' },
        }],
        stopReason: 'toolUse',
      },
      {
        parts: [{
          type: 'tool-call',
          id: 'complete-retry',
          name: INVESTIGATION_COMPLETE_TOOL_NAME,
          arguments: completion,
        }],
        stopReason: 'toolUse',
      },
    ],
  });

  assert.equal(result.runStatus, 'COMPLETED');
  assert.match(requests[1].find((message) => message.role === 'toolResult')?.text ?? '', /SyntaxError/u);
  assert.deepEqual(
    result.toolExecutions.map(({ toolName, status }) => [toolName, status]),
    [
      ['site_get_context', 'COMPLETED'],
      [INVESTIGATION_COMPLETE_TOOL_NAME, 'COMPLETED'],
    ],
  );
});

test('codemode surfaces nested tool errors and recovers on retry while keeping failed nested execution recorded', async () => {
  let attempt = 0;
  const flakyTool = siteContextTool(async () => {
    attempt += 1;
    if (attempt === 1) throw new AgentToolError('TOOL_OWNER_UNAVAILABLE', 'internal host secret');
    return { ok: true };
  });

  const { result, requests } = await runCodemodeEngine({
    tools: [flakyTool],
    responses: [
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-fail',
          name: 'codemode',
          arguments: { code: 'return await tools.site_get_context({});' },
        }],
        stopReason: 'toolUse',
      },
      {
        parts: [{
          type: 'tool-call',
          id: 'cm-recover',
          name: 'codemode',
          arguments: { code: 'return await tools.site_get_context({});' },
        }],
        stopReason: 'toolUse',
      },
      {
        parts: [{
          type: 'tool-call',
          id: 'complete-recovered',
          name: INVESTIGATION_COMPLETE_TOOL_NAME,
          arguments: {
            ...completion,
            evidenceRefs: [{ owner: 'REGISTRY', resourceType: 'site', resourceId: 'site-1', toolExecutionId: 'E2' }],
          },
        }],
        stopReason: 'toolUse',
      },
    ],
  });

  assert.equal(result.runStatus, 'COMPLETED');
  const firstToolResult = requests[1].find((message) => message.role === 'toolResult')?.text ?? '';
  assert.match(firstToolResult, /TOOL_OWNER_UNAVAILABLE/u);
  assert.equal(firstToolResult.includes('internal host secret'), false);
  assert.deepEqual(
    result.toolExecutions.map(({ toolName, status, failureCode }) => [toolName, status, failureCode]),
    [
      ['site_get_context', 'FAILED', 'TOOL_OWNER_UNAVAILABLE'],
      ['site_get_context', 'COMPLETED', null],
      [INVESTIGATION_COMPLETE_TOOL_NAME, 'COMPLETED', null],
    ],
  );
});
