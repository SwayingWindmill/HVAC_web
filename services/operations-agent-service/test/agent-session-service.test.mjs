import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createAgentSessionLifecycle,
  createAgentSessionService,
} from '../dist/application/index.js';
import { HVAC_AGENT_EVENT_VERSION } from '../dist/agent/index.js';

class MemoryAgentSessionStateStore {
  states = new Map();

  async get(sessionId) {
    return this.states.get(sessionId) ?? null;
  }

  async list(tenantId, siteId) {
    return [...this.states.values()]
      .filter((state) => state.session.tenantId === tenantId && state.session.siteId === siteId)
      .sort((left, right) => right.session.updatedAt - left.session.updatedAt);
  }

  async transact(sessionId, update) {
    const next = update(this.states.get(sessionId) ?? null);
    this.states.set(sessionId, next);
    return next;
  }
}

const modelRef = Object.freeze({ provider: 'faux', model: 'faux-1' });
const budget = Object.freeze({
  maxModelCalls: 4,
  maxToolCalls: 8,
  maxWallClockMs: 5_000,
  maxParallelToolCalls: 2,
  maxQueryRangeMs: 86_400_000,
  maxToolResultRecords: 1_000,
  maxToolResultBytes: 64_000,
  maxInputTokens: 16_000,
  maxOutputTokens: 4_000,
});
const zeroUsage = Object.freeze({ inputTokens: 0, outputTokens: 0, modelCalls: 1, toolCalls: 0 });
const context = Object.freeze({
  tenantId: 'tenant-1',
  siteId: 'site-1',
  principalId: 'principal-1',
  capabilities: Object.freeze(['site.read']),
  correlationId: 'correlation-1',
  authorization: Object.freeze({
    decision: 'ALLOW',
    decisionId: 'decision-1',
    capabilities: Object.freeze(['site.read']),
  }),
});

const waitFor = async (predicate, message = 'condition was not met') => {
  const deadline = Date.now() + 2_000;
  while (Date.now() < deadline) {
    const value = await predicate();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  throw new Error(message);
};

const createIds = () => {
  const counters = new Map();
  return (kind) => {
    const next = (counters.get(kind) ?? 0) + 1;
    counters.set(kind, next);
    return `${kind}-${next}`;
  };
};

const findingFor = (input, summary = 'Investigation completed.') => Object.freeze({
  id: `finding-${input.run.id}`,
  sessionId: input.session.id,
  runId: input.run.id,
  kind: 'FINDING',
  finding: Object.freeze({
    outcome: 'UNABLE_TO_CONCLUDE',
    summary,
    evidenceRefs: Object.freeze([]),
    limitations: Object.freeze([]),
    recommendedNext: Object.freeze([]),
  }),
  createdAt: input.run.startedAt + 1,
});

const completedResult = (input, artifacts = [findingFor(input)]) => Object.freeze({
  runStatus: 'COMPLETED',
  sessionStatus: artifacts.at(-1)?.kind === 'INPUT_REQUEST' ? 'WAITING_FOR_INPUT' : 'COMPLETED',
  failureCode: null,
  usage: zeroUsage,
  finalizedMessages: Object.freeze([
    Object.freeze({
      id: `assistant-${input.run.id}`,
      sessionId: input.session.id,
      runId: input.run.id,
      role: 'ASSISTANT',
      content: artifacts.at(-1)?.kind === 'INPUT_REQUEST' ? 'I need one answer.' : 'Investigation completed.',
      createdAt: input.run.startedAt + 1,
    }),
  ]),
  toolExecutions: Object.freeze([]),
  artifacts: Object.freeze(artifacts),
});

const createService = (engine) => {
  const store = new MemoryAgentSessionStateStore();
  const lifecycle = createAgentSessionLifecycle({ store });
  let clock = 1_000;
  const service = createAgentSessionService({
    lifecycle,
    engine,
    modelRef,
    createTools: () => Object.freeze([]),
    budget,
    now: () => ++clock,
    nextId: createIds(),
  });
  return { service, lifecycle, store };
};

test('reconnect starts from one durable Session snapshot and never duplicates the Run mutation', async () => {
  let engineCalls = 0;
  const { service } = createService(async (input) => {
    engineCalls += 1;
    input.emit(Object.freeze({
      version: HVAC_AGENT_EVENT_VERSION,
      type: 'assistant.delta',
      sessionId: input.session.id,
      runId: input.run.id,
      sequence: 1,
      at: input.run.startedAt,
      payload: Object.freeze({ messageId: `assistant-${input.run.id}`, delta: 'partial' }),
    }));
    return completedResult(input);
  });

  const created = await service.create(context, { message: 'Investigate this Site.' });
  const completed = await waitFor(async () => {
    const state = await service.get(context, created.session.id);
    return state.session.status === 'COMPLETED' ? state : null;
  }, 'Session did not complete');

  const events = [];
  const unsubscribe = await service.subscribe(context, completed.session.id, (event) => events.push(event));
  unsubscribe();

  assert.equal(engineCalls, 1);
  assert.equal(events.length, 1);
  assert.equal(events[0].version, HVAC_AGENT_EVENT_VERSION);
  assert.equal(events[0].type, 'session.snapshot');
  assert.deepEqual(events[0].payload.snapshot, completed);
  assert.equal(events[0].payload.snapshot.messages.filter(({ role }) => role === 'ASSISTANT').length, 1);
});

test('subscribe refreshes durable truth when a Run settles between snapshot read and live registration', async () => {
  const runningRun = Object.freeze({
    id: 'run-race-1',
    sessionId: 'session-race-1',
    modelRef,
    status: 'RUNNING',
    startedAt: 2_000,
    finishedAt: null,
    usage: zeroUsage,
    failureCode: null,
  });
  const active = Object.freeze({
    session: Object.freeze({
      id: 'session-race-1',
      tenantId: context.tenantId,
      siteId: context.siteId,
      agentDefinitionId: 'operations-investigation.v1',
      createdBy: context.principalId,
      revision: 0,
      createdAt: 1_900,
      updatedAt: 2_000,
      status: 'ACTIVE',
      activeRunId: runningRun.id,
    }),
    runs: Object.freeze([runningRun]),
    messages: Object.freeze([]),
    toolExecutions: Object.freeze([]),
    artifacts: Object.freeze([]),
  });
  const terminalRun = Object.freeze({
    ...runningRun,
    status: 'COMPLETED',
    finishedAt: 2_100,
  });
  const terminal = Object.freeze({
    ...active,
    session: Object.freeze({
      ...active.session,
      revision: 1,
      updatedAt: 2_100,
      status: 'COMPLETED',
      activeRunId: null,
    }),
    runs: Object.freeze([terminalRun]),
  });
  let reads = 0;
  const lifecycle = Object.freeze({
    async get() { return ++reads === 1 ? active : terminal; },
    async list() { return []; },
  });
  const service = createAgentSessionService({
    lifecycle,
    engine: async () => { throw new Error('engine must not run during reconnect'); },
    modelRef,
    createTools: () => Object.freeze([]),
    budget,
    now: () => 2_200 + reads,
    nextId: createIds(),
  });
  const events = [];

  const unsubscribe = await service.subscribe(context, active.session.id, (event) => events.push(event));
  unsubscribe();

  assert.equal(reads, 2);
  assert.deepEqual(events.map((event) => event.type), ['session.snapshot', 'session.snapshot']);
  assert.equal(events[0].payload.snapshot.session.status, 'ACTIVE');
  assert.equal(events[1].payload.snapshot.session.status, 'COMPLETED');
  assert.equal(events[1].payload.snapshot.session.revision, 1);
});

test('Session list stays within the generated public contract bound', async () => {
  const states = Array.from({ length: 60 }, (_, index) => Object.freeze({
    session: Object.freeze({
      id: `session-list-${index}`,
      tenantId: context.tenantId,
      siteId: context.siteId,
      agentDefinitionId: 'operations-investigation.v1',
      createdBy: context.principalId,
      revision: 0,
      createdAt: 1_000 + index,
      updatedAt: 1_000 + index,
      status: 'COMPLETED',
      activeRunId: null,
    }),
    runs: Object.freeze([]),
    messages: Object.freeze([]),
    toolExecutions: Object.freeze([]),
    artifacts: Object.freeze([]),
  }));
  const lifecycle = Object.freeze({
    async list(tenantId, siteId) {
      assert.equal(tenantId, context.tenantId);
      assert.equal(siteId, context.siteId);
      return states;
    },
  });
  const service = createAgentSessionService({
    lifecycle,
    engine: async () => { throw new Error('engine must not run during list'); },
    modelRef,
    createTools: () => Object.freeze([]),
    budget,
    now: () => 1_000,
    nextId: createIds(),
  });

  const listed = await service.list(context);

  assert.equal(listed.length, 50);
  assert.deepEqual(listed, states.slice(0, 50));
});

test('live assistant deltas are observable but never become durable finalized messages before completion', async () => {
  let release;
  const released = new Promise((resolve) => { release = resolve; });
  const { service } = createService(async (input) => {
    input.emit(Object.freeze({
      version: HVAC_AGENT_EVENT_VERSION,
      type: 'run.started',
      sessionId: input.session.id,
      runId: input.run.id,
      sequence: 0,
      at: input.run.startedAt,
      payload: Object.freeze({ run: input.run }),
    }));
    input.emit(Object.freeze({
      version: HVAC_AGENT_EVENT_VERSION,
      type: 'assistant.delta',
      sessionId: input.session.id,
      runId: input.run.id,
      sequence: 1,
      at: input.run.startedAt,
      payload: Object.freeze({ messageId: `assistant-${input.run.id}`, delta: 'not-final' }),
    }));
    await released;
    return completedResult(input);
  });

  const created = await service.create(context, { message: 'Investigate current energy.' });
  const events = [];
  const unsubscribe = await service.subscribe(context, created.session.id, (event) => events.push(event));

  assert.equal(events[0].type, 'session.snapshot');
  assert.ok(events.some((event) => event.type === 'assistant.delta'));
  const durableWhileRunning = await service.get(context, created.session.id);
  assert.deepEqual(durableWhileRunning.messages.map(({ role }) => role), ['OPERATOR']);

  release();
  await waitFor(async () => (await service.get(context, created.session.id)).session.status === 'COMPLETED');
  unsubscribe();
});

test('cancel aborts the live engine while durable cancellation remains authoritative', async () => {
  let observedAbort = false;
  const { service } = createService(async (input) => {
    await new Promise((resolve) => {
      input.signal.addEventListener('abort', () => {
        observedAbort = true;
        resolve();
      }, { once: true });
    });
    return Object.freeze({
      runStatus: 'CANCELLED',
      sessionStatus: 'CANCELLED',
      failureCode: 'RUN_CANCELLED',
      usage: zeroUsage,
      finalizedMessages: Object.freeze([]),
      toolExecutions: Object.freeze([]),
      artifacts: Object.freeze([]),
    });
  });

  const created = await service.create(context, { message: 'Investigate this Site.' });
  const cancelled = await service.cancel(context, {
    sessionId: created.session.id,
    expectedRevision: created.session.revision,
  });

  assert.equal(cancelled.session.status, 'CANCELLED');
  await waitFor(() => observedAbort);
  assert.equal((await service.get(context, created.session.id)).session.status, 'CANCELLED');
});

test('typed operator input is durable, attributed to the authenticated principal, and starts exactly one continuation Run', async () => {
  let engineCalls = 0;
  const { service } = createService(async (input) => {
    engineCalls += 1;
    if (engineCalls === 1) {
      const requestArtifact = Object.freeze({
        id: 'request-artifact-1',
        sessionId: input.session.id,
        runId: input.run.id,
        kind: 'INPUT_REQUEST',
        request: Object.freeze({
          prompt: 'Which schedule applies?',
          response: Object.freeze({
            kind: 'SINGLE_SELECT',
            choices: Object.freeze([
              Object.freeze({ value: 'weekday', label: 'Weekday' }),
              Object.freeze({ value: 'weekend', label: 'Weekend' }),
            ]),
          }),
        }),
        createdAt: input.run.startedAt + 1,
      });
      return completedResult(input, [requestArtifact]);
    }
    return completedResult(input);
  });

  const created = await service.create(context, { message: 'Check the operating schedule.' });
  const waiting = await waitFor(async () => {
    const state = await service.get(context, created.session.id);
    return state.session.status === 'WAITING_FOR_INPUT' ? state : null;
  }, 'Session did not wait for input');

  const continued = await service.submitInput(context, {
    sessionId: waiting.session.id,
    expectedRevision: waiting.session.revision,
    requestArtifactId: 'request-artifact-1',
    value: 'weekday',
  });

  assert.equal(continued.session.status, 'ACTIVE');
  const response = continued.artifacts.find(({ kind }) => kind === 'INPUT_RESPONSE');
  assert.equal(response.submittedBy, context.principalId);
  assert.equal(response.value, 'weekday');
  assert.equal(response.requestArtifactId, 'request-artifact-1');
  assert.equal(continued.messages.at(-1).content, 'Weekday');

  await waitFor(async () => (await service.get(context, created.session.id)).session.status === 'COMPLETED');
  assert.equal(engineCalls, 2);
});
