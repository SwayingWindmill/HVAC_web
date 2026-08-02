import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { build } from 'esbuild';

async function loadBundledModule(entryPoint, { stubCopilot = false } = {}) {
  const plugins = stubCopilot ? [{
    name: 'stub-copilot-agent',
    setup(buildContext) {
      buildContext.onResolve(
        { filter: /^@copilotkit\/react-core\/v2$/u },
        () => ({ path: 'copilot-agent-stub', namespace: 'test-stub' }),
      );
      buildContext.onLoad(
        { filter: /.*/u, namespace: 'test-stub' },
        () => ({
          contents: 'export class AbstractAgent { constructor(config) { Object.assign(this, config); } }',
          loader: 'js',
        }),
      );
    },
  }] : [];
  const result = await build({
    entryPoints: [entryPoint],
    bundle: true,
    platform: 'node',
    format: 'esm',
    write: false,
    logLevel: 'silent',
    plugins,
  });
  const source = result.outputFiles[0].text;
  return import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
}

const loadContract = () => loadBundledModule('apps/hvac-web/src/api/operations-contract.ts');

const investigation = {
  schemaVersion: 1,
  id: 'investigation-001',
  scope: {
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentId: null,
    deviceId: null,
  },
  status: 'COMPLETED',
  revision: 9,
  createdAt: 1,
  activeRun: null,
  outcome: 'SUPPORTED_SITE_FINDING',
  evidence: [],
  analysisReferences: [],
  findings: [],
  operatorInputRequest: null,
  acceptedOperatorInputs: [],
  resourceBudget: null,
};
const plan = {
  schemaVersion: 1,
  id: 'site-night-energy-investigation',
  label: 'Site night-energy investigation',
  completedSteps: 4,
  totalSteps: 4,
  progressPercent: 100,
  steps: [
    { id: 'READ_SITE_CONTEXT', label: 'Read authoritative Site context', status: 'COMPLETED' },
    { id: 'READ_ENERGY_SERIES', label: 'Read authoritative night-energy periods', status: 'COMPLETED' },
    { id: 'ANALYZE', label: 'Run deterministic night-energy analysis', status: 'COMPLETED' },
    { id: 'COMMIT_RESULT', label: 'Commit Evidence, Analysis and Finding', status: 'COMPLETED' },
  ],
};

function event(id, type, payload) {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ type, ...payload })}\n\n`;
}

function recoveryHeaders({ mode = 'FULL_SNAPSHOT', reason = 'INITIAL', replayFrom } = {}) {
  return {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'X-Operations-Recovery-Mode': mode,
    'X-Operations-Recovery-Reason': reason,
    'X-Operations-Snapshot-Position': '9:1',
    'X-Operations-Latest-Position': '9:2',
    ...(replayFrom ? { 'X-Operations-Replay-From': replayFrom } : {}),
  };
}

const validStream = [
  event('9:0', 'RUN_STARTED', { threadId: investigation.id, runId: 'run-001' }),
  event('9:1', 'STATE_SNAPSHOT', {
    snapshot: {
      schemaVersion: 'operations-investigation-ui/v1',
      investigation,
      plan,
      toolActivities: [],
    },
  }),
  event('9:2', 'RUN_FINISHED', {
    threadId: investigation.id,
    runId: 'run-001',
    outcome: { type: 'success' },
  }),
].join('');

test('Operations Workspace parser accepts the bounded committed event lifecycle', async () => {
  const { parseOperationsAgUiEventStream } = await loadContract();
  const parsed = parseOperationsAgUiEventStream(validStream);
  assert.deepEqual(parsed.map((item) => item.event.type), [
    'RUN_STARTED', 'STATE_SNAPSHOT', 'RUN_FINISHED',
  ]);
  assert.equal(parsed[1].event.snapshot.plan.progressPercent, 100);
  assert.equal(parsed[1].event.snapshot.investigation.revision, 9);
});

test('Operations Workspace parser rejects internal state and arbitrary Tool payloads', async () => {
  const { parseOperationsAgUiEventStream } = await loadContract();
  const unsafeState = validStream.replace(
    '"findings":[]',
    '"findings":[],"checkpoint":{"opaqueState":"secret"}',
  );
  assert.throws(() => parseOperationsAgUiEventStream(unsafeState), /forbidden field checkpoint/u);

  const unsafeTool = validStream.replace(
    event('9:2', 'RUN_FINISHED', {
      threadId: investigation.id,
      runId: 'run-001',
      outcome: { type: 'success' },
    }),
    event('9:2', 'TOOL_CALL_ARGS', {
      toolCallId: 'receipt-001',
      delta: JSON.stringify({
        recordId: 'receipt-001',
        logicalTool: 'registry.getSite',
        owner: 'registry',
        resultCategory: 'SUCCEEDED',
        startedAt: 1,
        completedAt: 2,
        metadata: { raw: true },
      }),
    }) + event('9:3', 'RUN_FINISHED', {
      threadId: investigation.id,
      runId: 'run-001',
      outcome: { type: 'success' },
    }),
  );
  assert.throws(() => parseOperationsAgUiEventStream(unsafeTool), /forbidden field metadata/u);

  const outOfOrder = validStream.replace('id: 9:1', 'id: 9:2');
  assert.throws(() => parseOperationsAgUiEventStream(outOfOrder), /identity is invalid/u);
  const crossRevision = validStream.replace('id: 9:2', 'id: 10:2');
  assert.throws(() => parseOperationsAgUiEventStream(crossRevision), /identity is invalid/u);
});

test('scoped Operations API accepts the authorized stream and rejects a mismatched Organization', async () => {
  const { streamSiteNightEnergyInvestigationEvents } = await loadBundledModule('apps/hvac-web/src/api/operations.ts');
  const requests = [];
  const fetchImplementation = async (input, init) => {
    requests.push({ input: String(input), init });
    const requestedPosition = init.headers['Last-Event-ID'];
    return new Response(validStream, {
      status: 200,
      headers: recoveryHeaders(requestedPosition
        ? { mode: 'RESUME', reason: 'VALID', replayFrom: requestedPosition }
        : undefined),
    });
  };
  const batch = await streamSiteNightEnergyInvestigationEvents(investigation.id, {
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    fetchImplementation,
  });
  assert.equal(batch.events.length, 3);
  assert.equal(batch.recovery.mode, 'FULL_SNAPSHOT');
  assert.equal(batch.recovery.latestPosition, '9:2');
  assert.equal(requests[0].input, '/api/v1/sites/site-001/operations/investigations/investigation-001/events');
  assert.equal(requests[0].init.credentials, 'same-origin');
  assert.equal(requests[0].init.headers.Accept, 'text/event-stream, application/problem+json');

  const resumed = await streamSiteNightEnergyInvestigationEvents(investigation.id, {
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    recoveryPosition: '9:2',
    fetchImplementation,
  });
  assert.equal(requests[1].init.headers['Last-Event-ID'], '9:2');
  assert.equal(resumed.recovery.mode, 'RESUME');
  assert.equal(resumed.recovery.replayFromPosition, '9:2');

  await assert.rejects(
    streamSiteNightEnergyInvestigationEvents(investigation.id, {
      trustedOrganizationId: 'organization-other',
      trustedSiteId: investigation.scope.siteId,
      fetchImplementation,
    }),
    /超出当前已验证 Site Scope/u,
  );
});

test('scoped Operations API lists only exact authorized Site summaries', async () => {
  const { listSiteNightEnergyInvestigations } = await loadBundledModule('apps/hvac-web/src/api/operations.ts');
  const summary = {
    schemaVersion: 1,
    id: investigation.id,
    scope: investigation.scope,
    status: investigation.status,
    revision: investigation.revision,
    createdAt: investigation.createdAt,
    outcome: investigation.outcome,
    evidenceCount: 1,
    analysisReferenceCount: 1,
    findingCount: 1,
    toolReceiptCount: 4,
    acceptedOperatorInputCount: 0,
    resourceBudget: null,
  };
  const requests = [];
  const fetchImplementation = async (input, init) => {
    requests.push({ input: String(input), init });
    return new Response(JSON.stringify({ schemaVersion: 1, investigations: [summary] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };
  const listed = await listSiteNightEnergyInvestigations({
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    fetchImplementation,
  });
  assert.deepEqual(listed.investigations, [summary]);
  assert.equal(requests[0].input, '/api/v1/sites/site-001/operations/investigations');
  assert.equal(requests[0].init.method, 'GET');
  assert.equal(requests[0].init.credentials, 'same-origin');

  await assert.rejects(
    listSiteNightEnergyInvestigations({
      trustedOrganizationId: 'organization-other',
      trustedSiteId: investigation.scope.siteId,
      fetchImplementation,
    }),
    /超出当前已验证 Site Scope/u,
  );
});

test('scoped Operations API cancels the selected Investigation through the authoritative mutation route', async () => {
  const { cancelSiteNightEnergyInvestigation } = await loadBundledModule('apps/hvac-web/src/api/operations.ts');
  const cancelled = {
    ...investigation,
    status: 'CANCELLED',
    activeRun: null,
    outcome: null,
    toolReceipts: [],
  };
  const requests = [];
  const fetchImplementation = async (input, init) => {
    requests.push({ input: String(input), init });
    return new Response(JSON.stringify(cancelled), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };
  const result = await cancelSiteNightEnergyInvestigation(investigation.id, {
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    csrfToken: '[REDACTED_SECRET]',
    fetchImplementation,
  });
  assert.equal(result.status, 'CANCELLED');
  assert.equal(requests.length, 1);
  assert.equal(requests[0].input, '/api/v1/sites/site-001/operations/investigations/investigation-001:cancel');
  assert.equal(requests[0].init.method, 'POST');
  assert.equal(requests[0].init.credentials, 'same-origin');
  assert.equal(new Headers(requests[0].init.headers).get('X-CSRF-Token'), '[REDACTED_SECRET]');
  assert.equal(requests[0].init.body, '{}');

  await assert.rejects(
    cancelSiteNightEnergyInvestigation(investigation.id, {
      trustedOrganizationId: 'organization-other',
      trustedSiteId: investigation.scope.siteId,
      csrfToken: '[REDACTED_SECRET]',
      fetchImplementation,
    }),
    /超出当前已验证 Site Scope/u,
  );
});

test('Operations Workspace accepts one bounded waiting Request and rejects forged Operator Input state', async () => {
  const {
    operationsInvestigationViewSchema,
    operationsOperatorInputAcceptedSchema,
  } = await loadContract();
  const request = {
    schemaVersion: 1,
    id: 'operator-input-request-001',
    investigationId: investigation.id,
    runId: 'run-001',
    scope: investigation.scope,
    kind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
    requestedAt: 10,
    requestedBy: 'DETERMINISTIC_POLICY',
    policyVersion: 'operator-input-policy/v1',
    fields: [{
      id: 'analysisScope',
      type: 'SINGLE_SELECT',
      required: true,
      options: ['SITE_ONLY', 'DEFER'],
    }, {
      id: 'operatorNote',
      type: 'SHORT_TEXT',
      required: false,
      maximumLength: 500,
    }],
  };
  const waiting = operationsInvestigationViewSchema.parse({
    ...investigation,
    status: 'WAITING_FOR_OPERATOR_INPUT',
    revision: 10,
    activeRun: { id: 'run-001', status: 'WAITING_FOR_OPERATOR_INPUT', startedAt: 1 },
    outcome: null,
    operatorInputRequest: request,
    acceptedOperatorInputs: [],
    toolReceipts: [],
  });
  assert.equal(waiting.operatorInputRequest.id, request.id);
  assert.deepEqual(waiting.operatorInputRequest.fields[0].options, ['SITE_ONLY', 'DEFER']);

  const accepted = {
    schemaVersion: 1,
    recordType: 'OPERATOR_INPUT_ACCEPTED',
    id: 'operator-input-record-001',
    investigationId: investigation.id,
    recordedAt: 11,
    requestId: request.id,
    runId: request.runId,
    idempotencyKey: 'operator-input-retry-001',
    inputKind: request.kind,
    inputDigest: `sha256:${'a'.repeat(64)}`,
    scope: investigation.scope,
    values: { analysisScope: 'SITE_ONLY', operatorNote: 'Proceed with Site-only authority.' },
    provenance: {
      actorType: 'OPERATOR',
      source: 'PLATFORM_GATEWAY',
      authorizationDecisionId: 'decision-fixture-001',
      policyRevision: 'policy-v17',
      submittedAt: 11,
    },
  };
  assert.deepEqual(operationsOperatorInputAcceptedSchema.parse(accepted), accepted);
  assert.throws(
    () => operationsOperatorInputAcceptedSchema.parse({
      ...accepted,
      values: { ...accepted.values, rawPrompt: 'forbidden' },
    }),
    /Unrecognized key/u,
  );
  assert.throws(
    () => operationsOperatorInputAcceptedSchema.parse({
      ...accepted,
      provenance: { ...accepted.provenance, actorType: 'MODEL' },
    }),
    /Invalid literal value/u,
  );
});

test('scoped Operations API submits Operator Input and preserves exact-retry identity', async () => {
  const { submitSiteNightEnergyOperatorInput } = await loadBundledModule('apps/hvac-web/src/api/operations.ts');
  const acceptedRecord = {
    schemaVersion: 1,
    recordType: 'OPERATOR_INPUT_ACCEPTED',
    id: 'operator-input-record-001',
    investigationId: investigation.id,
    recordedAt: 11,
    requestId: 'operator-input-request-001',
    runId: 'run-001',
    idempotencyKey: 'operator-input-retry-001',
    inputKind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
    inputDigest: `sha256:${'b'.repeat(64)}`,
    scope: investigation.scope,
    values: { analysisScope: 'SITE_ONLY', operatorNote: null },
    provenance: {
      actorType: 'OPERATOR',
      source: 'PLATFORM_GATEWAY',
      authorizationDecisionId: 'decision-fixture-001',
      policyRevision: 'policy-v17',
      submittedAt: 11,
    },
  };
  const resumed = {
    ...investigation,
    status: 'RUNNING',
    revision: 11,
    activeRun: { id: 'run-001', status: 'ACTIVE', startedAt: 1 },
    outcome: null,
    operatorInputRequest: null,
    acceptedOperatorInputs: [acceptedRecord],
    toolReceipts: [],
  };
  const requests = [];
  const outcomes = ['COMMITTED', 'DUPLICATE'];
  const fetchImplementation = async (input, init) => {
    requests.push({ input: String(input), init });
    return new Response(JSON.stringify({ outcome: outcomes.shift(), investigation: resumed }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  };
  const command = {
    requestId: acceptedRecord.requestId,
    expectedRevision: 10,
    idempotencyKey: acceptedRecord.idempotencyKey,
    values: acceptedRecord.values,
  };
  const options = {
    trustedOrganizationId: investigation.scope.organizationId,
    trustedSiteId: investigation.scope.siteId,
    csrfToken: '[REDACTED_SECRET]',
    fetchImplementation,
  };
  const committed = await submitSiteNightEnergyOperatorInput(investigation.id, command, options);
  const duplicate = await submitSiteNightEnergyOperatorInput(investigation.id, command, options);
  assert.equal(committed.outcome, 'COMMITTED');
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.equal(requests.length, 2);
  assert.equal(requests[0].input, '/api/v1/sites/site-001/operations/investigations/investigation-001:submit-operator-input');
  assert.equal(requests[0].init.headers['Idempotency-Key'], acceptedRecord.idempotencyKey);
  assert.equal(requests[1].init.headers['Idempotency-Key'], acceptedRecord.idempotencyKey);
  assert.deepEqual(JSON.parse(requests[0].init.body), {
    schemaVersion: 1,
    requestId: acceptedRecord.requestId,
    expectedRevision: 10,
    values: acceptedRecord.values,
  });

  await assert.rejects(
    submitSiteNightEnergyOperatorInput(investigation.id, command, {
      ...options,
      fetchImplementation: async () => new Response(JSON.stringify({
        outcome: 'COMMITTED',
        investigation: { ...resumed, id: 'investigation-other' },
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    }),
    /响应与请求的 Investigation 不一致/u,
  );
});

test('Operations recovery positions persist only opaque scoped cursors in session storage', async () => {
  const {
    createOperationsInvestigationRecoveryPositionStore,
    normalizeOperationsRecoveryPosition,
  } = await loadBundledModule(
    'apps/hvac-web/src/real/operations/operations-recovery-position.ts',
  );
  const values = new Map();
  const removed = [];
  const storage = {
    get length() {
      return values.size;
    },
    key(index) {
      return [...values.keys()][index] ?? null;
    },
    getItem(key) {
      return values.get(key) ?? null;
    },
    setItem(key, value) {
      values.set(key, value);
    },
    removeItem(key) {
      removed.push(key);
      values.delete(key);
    },
  };
  const store = createOperationsInvestigationRecoveryPositionStore(storage);
  const firstScope = {
    organizationId: 'organization-001',
    siteId: 'site-001',
    investigationId: 'investigation-001',
  };
  const secondScope = {
    ...firstScope,
    investigationId: 'investigation-002',
  };
  const thirdScope = {
    ...firstScope,
    siteId: 'site-002',
    investigationId: 'investigation-003',
  };

  store.save(firstScope, '9:2');
  assert.equal(store.load(firstScope), '9:2');
  assert.equal(store.load(secondScope), undefined);
  assert.equal(values.size, 1);
  assert.equal([...values.values()][0], '9:2');
  assert.equal(JSON.stringify([...values.entries()]).includes('findings'), false);

  const [firstKey] = values.keys();
  values.set(firstKey, 'invalid-cursor');
  assert.equal(store.load(firstScope), undefined);
  assert.equal(values.has(firstKey), false);
  assert.equal(removed.includes(firstKey), true);

  assert.equal(normalizeOperationsRecoveryPosition(' 12:4 '), '12:4');
  assert.equal(normalizeOperationsRecoveryPosition('12:04'), undefined);
  assert.equal(normalizeOperationsRecoveryPosition('snapshot-payload'), undefined);

  store.save(firstScope, '10:5');
  store.clear(firstScope);
  assert.equal(store.load(firstScope), undefined);
  store.save(firstScope, '11:1');
  store.save(secondScope, '11:2');
  store.save(thirdScope, '11:3');
  store.clearSite(firstScope);
  assert.equal(store.load(firstScope), undefined);
  assert.equal(store.load(secondScope), undefined);
  assert.equal(store.load(thirdScope), '11:3');
});

test('Headless Operations agent reconnects after interruption without duplicating durable Tool records', async () => {
  const { OperationsInvestigationAgent } = await loadBundledModule(
    'apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts',
    { stubCopilot: true },
  );
  const activity = {
    recordId: 'receipt-stable',
    logicalTool: 'registry.getSite',
    owner: 'registry',
    resultCategory: 'SUCCEEDED',
    startedAt: 1,
    completedAt: 2,
  };
  const streamFor = (revision, status) => {
    const view = {
      ...investigation,
      status,
      revision,
      activeRun: status === 'RUNNING'
        ? { id: 'active-run', status: 'ACTIVE', startedAt: 1 }
        : null,
      outcome: status === 'COMPLETED' ? 'SUPPORTED_SITE_FINDING' : null,
    };
    return [
      event(`${revision}:0`, 'RUN_STARTED', { threadId: investigation.id, runId: `projection-${revision}` }),
      event(`${revision}:1`, 'STATE_SNAPSHOT', {
        snapshot: {
          schemaVersion: 'operations-investigation-ui/v1',
          investigation: view,
          plan,
          toolActivities: [activity],
        },
      }),
      event(`${revision}:2`, 'TOOL_CALL_START', {
        toolCallId: activity.recordId,
        toolCallName: activity.logicalTool,
      }),
      event(`${revision}:3`, 'TOOL_CALL_ARGS', {
        toolCallId: activity.recordId,
        delta: JSON.stringify(activity),
      }),
      event(`${revision}:4`, 'TOOL_CALL_END', { toolCallId: activity.recordId }),
      event(`${revision}:5`, 'RUN_FINISHED', {
        threadId: investigation.id,
        runId: `projection-${revision}`,
        outcome: { type: 'success' },
      }),
    ].join('');
  };
  const responses = [
    new Response(streamFor(1, 'RUNNING'), {
      status: 200,
      headers: {
        'Content-Type': 'text/event-stream; charset=utf-8',
        'X-Operations-Recovery-Mode': 'FULL_SNAPSHOT',
        'X-Operations-Recovery-Reason': 'INITIAL',
        'X-Operations-Snapshot-Position': '1:1',
        'X-Operations-Latest-Position': '1:5',
      },
    }),
    new TypeError('network interrupted'),
    new Response(streamFor(2, 'COMPLETED'), {
      status: 200,
      headers: {
        'Content-Type': 'text/event-stream; charset=utf-8',
        'X-Operations-Recovery-Mode': 'FULL_SNAPSHOT',
        'X-Operations-Recovery-Reason': 'EXPIRED',
        'X-Operations-Snapshot-Position': '2:1',
        'X-Operations-Latest-Position': '2:5',
      },
    }),
  ];
  const requests = [];
  const snapshots = [];
  const connectionStates = [];
  const recoveryOperations = [];
  const recoveryPositionStore = {
    load(scope) {
      recoveryOperations.push({ operation: 'load', scope });
      return undefined;
    },
    save(scope, position) {
      recoveryOperations.push({ operation: 'save', scope, position });
    },
    clear(scope) {
      recoveryOperations.push({ operation: 'clear', scope });
    },
  };
  const fetchImplementation = async (_input, init) => {
    requests.push(init);
    const next = responses.shift();
    if (next instanceof Error) throw next;
    return next;
  };
  const agent = new OperationsInvestigationAgent({
    organizationId: investigation.scope.organizationId,
    siteId: investigation.scope.siteId,
    investigationId: investigation.id,
    reconnectDelayMs: 25,
    maximumRetryDelayMs: 25,
    recoveryPositionStore,
    fetchImplementation,
    onSnapshot: (snapshot) => snapshots.push(snapshot),
    onConnectionState: (state) => connectionStates.push(state.status),
  });
  const delivered = [];
  await new Promise((resolve, reject) => {
    agent.run({ threadId: 'ui-thread', runId: 'ui-run' }).subscribe({
      next: (nextEvent) => delivered.push(nextEvent),
      error: reject,
      complete: resolve,
    });
  });
  assert.equal(requests.length, 3);
  assert.equal(requests[1].headers['Last-Event-ID'], '1:5');
  assert.equal(requests[2].headers['Last-Event-ID'], '1:5');
  assert.deepEqual(snapshots.map((snapshot) => snapshot.investigation.revision), [1, 2]);
  assert.deepEqual(connectionStates, ['CONNECTING', 'LIVE', 'RETRYING', 'LIVE', 'TERMINAL']);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'RUN_STARTED').length, 1);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'STATE_SNAPSHOT').length, 2);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'TOOL_CALL_START').length, 1);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'TOOL_CALL_ARGS').length, 1);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'TOOL_CALL_END').length, 1);
  assert.equal(delivered.filter((nextEvent) => nextEvent.type === 'RUN_FINISHED').length, 1);
  assert.deepEqual(recoveryOperations.map(({ operation, position }) => (
    position === undefined ? operation : `${operation}:${position}`
  )), ['load', 'save:1:5', 'clear']);
  assert.deepEqual(recoveryOperations[0].scope, {
    organizationId: investigation.scope.organizationId,
    siteId: investigation.scope.siteId,
    investigationId: investigation.id,
  });
});

test('Headless Operations agent restores a scoped cursor on the first request after reload', async () => {
  const { OperationsInvestigationAgent } = await loadBundledModule(
    'apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts',
    { stubCopilot: true },
  );
  const requests = [];
  const recoveryOperations = [];
  const recoveryPositionStore = {
    load(scope) {
      recoveryOperations.push({ operation: 'load', scope });
      return '9:2';
    },
    save() {
      assert.fail('terminal reload must not save another cursor');
    },
    clear(scope) {
      recoveryOperations.push({ operation: 'clear', scope });
    },
  };
  const agent = new OperationsInvestigationAgent({
    organizationId: investigation.scope.organizationId,
    siteId: investigation.scope.siteId,
    investigationId: investigation.id,
    recoveryPositionStore,
    fetchImplementation: async (_input, init) => {
      requests.push(init);
      return new Response(validStream, {
        status: 200,
        headers: recoveryHeaders({ mode: 'RESUME', reason: 'VALID', replayFrom: '9:2' }),
      });
    },
    onSnapshot: () => undefined,
  });

  await new Promise((resolve, reject) => {
    agent.run({ threadId: 'reload-thread', runId: 'reload-run' }).subscribe({
      error: reject,
      complete: resolve,
    });
  });
  assert.equal(requests.length, 1);
  assert.equal(requests[0].headers['Last-Event-ID'], '9:2');
  assert.deepEqual(recoveryOperations.map(({ operation }) => operation), ['load', 'clear']);
});

test('Headless Operations agent does not retry a nondiscoverable Investigation', async () => {
  const { OperationsInvestigationAgent } = await loadBundledModule(
    'apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts',
    { stubCopilot: true },
  );
  let requests = 0;
  const agent = new OperationsInvestigationAgent({
    organizationId: investigation.scope.organizationId,
    siteId: investigation.scope.siteId,
    investigationId: 'hidden-investigation',
    reconnectDelayMs: 25,
    maximumRetryDelayMs: 25,
    fetchImplementation: async () => {
      requests += 1;
      return new Response(JSON.stringify({
        type: 'https://api.quanlaihe.com/problems/resource-not-found',
        title: 'Resource not found',
        status: 404,
        code: 'RESOURCE_NOT_FOUND',
        detail: 'The Investigation is not visible.',
        retryable: false,
      }), {
        status: 404,
        headers: { 'Content-Type': 'application/problem+json' },
      });
    },
    onSnapshot: () => assert.fail('nondiscoverable Investigation emitted a snapshot'),
  });
  const failure = await new Promise((resolve) => {
    agent.run({ threadId: 'ui-thread', runId: 'ui-run' }).subscribe({
      error: resolve,
      complete: () => resolve(new Error('unexpected completion')),
    });
  });
  assert.equal(requests, 1);
  assert.equal(failure.status, 404);
  assert.equal(failure.code, 'RESOURCE_NOT_FOUND');
});

test('Real Site shell resolves a URL Operations route backed by CopilotKit Headless', async () => {
  const routingModule = await loadBundledModule('apps/hvac-web/src/real/site-routing.ts');
  const site = {
    id: '0198f5c0-7c00-7000-8000-000000000002',
    owningOrganizationId: '0198f5c0-7c00-7000-8000-000000000001',
    code: 'SITE-001',
    displayName: 'Authorized Site',
    timezone: 'Asia/Shanghai',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-31T00:00:00.000',
    updatedAt: '2026-07-31T00:00:00.000',
  };
  const path = routingModule.siteRoute(site, 'operations');
  assert.equal(path, `/sites/${site.id}/operations`);
  assert.deepEqual(
    routingModule.resolveSiteRouting(path, [site], ['site.read']),
    {
      state: 'READY',
      route: 'operations',
      context: { site, actingOrganizationId: site.owningOrganizationId },
    },
  );
  assert.deepEqual(routingModule.resolveSiteRouting(path, [site], []), { state: 'FORBIDDEN' });

  const [shell, workspace, agent, dashboard, realEntry, realApp, demoEntry] = await Promise.all([
    readFile('apps/hvac-web/src/real/SiteScopedShell.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/OperationsInvestigation.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/operations/OperationsInvestigationAgent.ts', 'utf8'),
    readFile('apps/hvac-web/src/real/RealDashboard.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/main.tsx', 'utf8'),
    readFile('apps/hvac-web/src/real/RealApp.tsx', 'utf8'),
    readFile('apps/hvac-web/src/demo/main.tsx', 'utf8'),
  ]);
  assert.match(shell, /siteRoute\(site, 'operations'\)/u);
  assert.match(shell, /label: 'Operations Workspace'/u);
  assert.match(shell, /primary: true/u);
  assert.match(shell, /<OperationsInvestigation/u);
  assert.match(dashboard, /real-dashboard-open-operations/u);
  assert.match(workspace, /<CopilotKit/u);
  assert.match(workspace, /registerProtectedResource/u);
  assert.match(workspace, /cancelSiteNightEnergyInvestigation/u);
  assert.match(workspace, /data-primary-agent-experience="true"/u);
  assert.match(agent, /streamSiteNightEnergyInvestigationEvents/u);
  assert.doesNotMatch(
    `${realEntry}\n${realApp}\n${shell}\n${workspace}\n${agent}`,
    /HvacMockAgent|AiProvider|GlobalAiAssistant|useAiHistory|localStorage|mock telemetry|variant="popup"/iu,
  );
  assert.match(demoEntry, /AiProvider/u);
});
