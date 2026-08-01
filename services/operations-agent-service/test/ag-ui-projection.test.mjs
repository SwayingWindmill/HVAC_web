import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createOperationsAgUiEventStreamResponse,
  encodeOperationsAgUiEventStream,
  projectOperationsInvestigationToAgUiEventBatch,
  projectOperationsInvestigationToAgUiEvents,
} from '../dist/transport-ag-ui/index.js';

const organizationId = '0198f5c0-7c00-7000-8000-000000000001';
const siteId = '0198f5c0-7c00-7000-8000-000000000002';
const investigationId = 'investigation-ag-ui-001';
const scope = Object.freeze({ organizationId, siteId, equipmentId: null, deviceId: null });

const receipt = (overrides) => ({
  schemaVersion: 1,
  recordType: 'TOOL_EXECUTION_RECEIPT',
  id: overrides.id,
  investigationId,
  recordedAt: overrides.recordedAt,
  logicalTool: overrides.logicalTool,
  owner: overrides.owner,
  requestId: `${overrides.id}:request`,
  attemptId: `${overrides.id}:attempt`,
  runId: 'run-001',
  stepId: 'step-read',
  startedAt: overrides.recordedAt - 10,
  completedAt: overrides.recordedAt,
  resultCategory: 'SUCCEEDED',
  metadata: { datasetRevision: 'internal-r17', bucketCount: 8 },
});

const completedView = Object.freeze({
  schemaVersion: 1,
  id: investigationId,
  scope,
  status: 'COMPLETED',
  revision: 7,
  createdAt: 1_700_000_000_000,
  activeRun: null,
  outcome: 'SUPPORTED_SITE_FINDING',
  evidence: [{
    schemaVersion: 1,
    recordType: 'EVIDENCE',
    id: 'evidence-001',
    investigationId,
    recordedAt: 1_700_000_000_100,
    evidenceKind: 'SITE_ENERGY_PERIOD_COMPARISON',
    classification: 'ALGORITHM_RESULT',
    statement: 'Target period used 24% more energy than the baseline period.',
    analysisReferenceDigest: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    sources: [],
  }],
  analysisReferences: [{
    schemaVersion: 1,
    recordType: 'ANALYSIS_REFERENCE',
    id: 'analysis-001',
    investigationId,
    recordedAt: 1_700_000_000_200,
    analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
    authority: 'DETERMINISTIC_ALGORITHM',
    algorithmVersion: 'night-energy/v1',
    policyVersion: 'night-energy-policy/v1',
    inputEvidenceIds: ['evidence-001'],
    parameterDigest: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
    resultDigest: 'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',
    executedAt: 1_700_000_000_200,
    outcome: 'SUPPORTED_SITE_FINDING',
  }],
  findings: [{
    schemaVersion: 1,
    recordType: 'FINDING',
    id: 'finding-001',
    investigationId,
    recordedAt: 1_700_000_000_300,
    findingKind: 'SITE_NIGHT_ENERGY_INCREASE',
    classification: 'INFERENCE',
    statement: 'Site night energy increased by 24%.',
    evidenceIds: ['evidence-001'],
    analysisReferenceIds: ['analysis-001'],
    conclusion: { status: 'SUPPORTED', scope: 'SITE', organizationId, siteId },
  }],
  toolReceipts: [
    receipt({
      id: 'receipt-energy-baseline',
      recordedAt: 1_700_000_000_050,
      logicalTool: 'analytics.getEnergySeries',
      owner: 'telemetry-query-service',
    }),
    receipt({
      id: 'receipt-site',
      recordedAt: 1_700_000_000_010,
      logicalTool: 'registry.getSite',
      owner: 'registry',
    }),
    receipt({
      id: 'receipt-equipment',
      recordedAt: 1_700_000_000_020,
      logicalTool: 'registry.listSiteEquipment',
      owner: 'registry',
    }),
    receipt({
      id: 'receipt-energy-target',
      recordedAt: 1_700_000_000_040,
      logicalTool: 'analytics.getEnergySeries',
      owner: 'telemetry-query-service',
    }),
  ],
});

test('committed Investigation state projects to a stable bounded AG-UI event batch', () => {
  const events = projectOperationsInvestigationToAgUiEvents(completedView);
  assert.deepEqual(events.map((event) => event.type), [
    'RUN_STARTED',
    'STATE_SNAPSHOT',
    'TOOL_CALL_START', 'TOOL_CALL_ARGS', 'TOOL_CALL_END',
    'TOOL_CALL_START', 'TOOL_CALL_ARGS', 'TOOL_CALL_END',
    'TOOL_CALL_START', 'TOOL_CALL_ARGS', 'TOOL_CALL_END',
    'TOOL_CALL_START', 'TOOL_CALL_ARGS', 'TOOL_CALL_END',
    'RUN_FINISHED',
  ]);

  const state = events[1].snapshot;
  assert.equal(state.schemaVersion, 'operations-investigation-ui/v1');
  assert.equal(state.investigation.revision, 7);
  assert.equal(state.investigation.findings[0].id, 'finding-001');
  assert.equal(state.plan.progressPercent, 100);
  assert.deepEqual(state.plan.steps.map((step) => step.status), [
    'COMPLETED', 'COMPLETED', 'COMPLETED', 'COMPLETED',
  ]);
  assert.deepEqual(state.toolActivities.map((activity) => activity.recordId), [
    'receipt-site',
    'receipt-equipment',
    'receipt-energy-target',
    'receipt-energy-baseline',
  ]);

  const firstToolArguments = JSON.parse(events[3].delta);
  assert.deepEqual(Object.keys(firstToolArguments).sort(), [
    'completedAt', 'logicalTool', 'owner', 'recordId', 'resultCategory', 'startedAt',
  ]);
  const serialized = JSON.stringify(events);
  for (const forbidden of [
    'metadata', 'requestId', 'attemptId', 'lease', 'checkpoint', 'runtimeRevision',
    'providerMessage', 'rawPrompt', 'points', 'delegationGrant', 'instructions',
    'ownerPayload', 'modelOutput', 'allowedReadTools', 'effectPolicy',
    'scopePolicy', 'untrustedContentPolicy', 'TRUSTED_CONTROL',
  ]) {
    assert.equal(serialized.includes(forbidden), false, `must not expose ${forbidden}`);
  }
  assert.deepEqual(
    projectOperationsInvestigationToAgUiEvents(completedView),
    events,
    'the same committed revision must produce the same ordered events',
  );
});

test('running Investigation projects only committed state and marks the first incomplete step active', () => {
  const running = {
    ...completedView,
    status: 'RUNNING',
    revision: 1,
    activeRun: { id: 'run-active', status: 'ACTIVE', startedAt: 1_700_000_000_000 },
    outcome: null,
    evidence: [],
    analysisReferences: [],
    findings: [],
    toolReceipts: [],
  };
  const events = projectOperationsInvestigationToAgUiEvents(running);
  assert.deepEqual(events.map((event) => event.type), [
    'RUN_STARTED', 'STATE_SNAPSHOT', 'RUN_FINISHED',
  ]);
  assert.equal(events[0].runId, `${investigationId}:projection:1`);
  assert.notEqual(events[0].runId, running.activeRun.id);
  assert.deepEqual(events[1].snapshot.plan.steps.map((step) => step.status), [
    'IN_PROGRESS', 'PENDING', 'PENDING', 'PENDING',
  ]);
  assert.equal(events[1].snapshot.investigation.evidence.length, 0);
  assert.equal(events[1].snapshot.toolActivities.length, 0);
});

test('SSE encoding carries stable revision-based identities and no-store event JSON', () => {
  const stream = encodeOperationsAgUiEventStream(completedView);
  assert.match(stream, /^id: 7:0\nevent: RUN_STARTED\ndata: /u);
  assert.match(stream, /id: 7:1\nevent: STATE_SNAPSHOT\ndata: /u);
  assert.equal(stream.endsWith('\n\n'), true);
  assert.equal(stream.includes('internal-r17'), false);
});

test('valid recovery positions replay only the committed missing suffix after a fresh snapshot', async () => {
  const batch = projectOperationsInvestigationToAgUiEventBatch(completedView, '7:4');
  assert.deepEqual(batch.recovery, {
    mode: 'RESUME',
    reason: 'VALID',
    snapshotPosition: '7:1',
    latestPosition: '7:14',
    replayFromPosition: '7:4',
  });
  assert.deepEqual(batch.frames.map((frame) => frame.id), [
    '7:0', '7:1',
    '7:5', '7:6', '7:7',
    '7:8', '7:9', '7:10',
    '7:11', '7:12', '7:13',
    '7:14',
  ]);
  assert.equal(batch.frames[1].event.type, 'STATE_SNAPSHOT');
  assert.equal(batch.frames[2].event.type, 'TOOL_CALL_START');
  assert.equal(batch.frames[2].event.toolCallId, 'receipt-equipment');

  const response = createOperationsAgUiEventStreamResponse(completedView, '7:4');
  assert.equal(response.headers.get('X-Operations-Recovery-Mode'), 'RESUME');
  assert.equal(response.headers.get('X-Operations-Replay-From'), '7:4');
  assert.equal(response.headers.get('X-Operations-Latest-Position'), '7:14');
  const body = await response.text();
  assert.equal(body.includes('id: 7:2\n'), false);
  assert.equal(body.includes('id: 7:5\n'), true);
});

test('unknown expired and conflicting recovery positions fall back to a full authoritative snapshot', () => {
  const cases = [
    ['not-a-position', 'UNKNOWN'],
    ['6:14', 'EXPIRED'],
    ['8:1', 'CONFLICT'],
    ['7:3', 'CONFLICT'],
    ['7:99', 'CONFLICT'],
  ];
  for (const [requestedPosition, reason] of cases) {
    const batch = projectOperationsInvestigationToAgUiEventBatch(completedView, requestedPosition);
    assert.equal(batch.recovery.mode, 'FULL_SNAPSHOT');
    assert.equal(batch.recovery.reason, reason);
    assert.equal(batch.recovery.replayFromPosition, null);
    assert.equal(batch.frames.length, 15);
    assert.deepEqual(batch.frames.slice(0, 2).map((frame) => frame.id), ['7:0', '7:1']);
    assert.equal(batch.frames.at(-1).id, '7:14');
  }
});
