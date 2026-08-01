import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { existsSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/operations-reconnect');
const outputRoot = resolve(root, 'out/operations-reconnect-certification');
const profileDir = join(tmpdir(), `operations-reconnect-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const organizationId = '01910000-0000-7000-8000-000000000001';
const siteId = '01910000-0001-7000-8000-000000000001';
const investigationId = 'investigation-browser-001';
const unableInvestigationId = 'investigation-browser-unable';
const operatorInputInvestigationId = 'investigation-browser-operator-input';
const cancelInvestigationId = 'investigation-browser-cancel';
const hiddenInvestigationId = 'hidden-investigation';
const digest = (character) => `sha256:${character.repeat(64)}`;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  assert(address && typeof address === 'object', 'port allocator did not expose an address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function event(id, type, payload) {
  return `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ type, ...payload })}\n\n`;
}

function plan(status) {
  const terminal = status === 'COMPLETED' || status === 'FAILED' || status === 'CANCELLED';
  const waiting = status === 'WAITING_FOR_OPERATOR_INPUT';
  return {
    schemaVersion: 1,
    id: 'site-night-energy-investigation',
    label: 'Site night-energy investigation',
    completedSteps: terminal ? 4 : 2,
    totalSteps: 4,
    progressPercent: terminal ? 100 : 50,
    steps: [
      { id: 'READ_SITE_CONTEXT', label: 'Read authoritative Site context', status: 'COMPLETED' },
      { id: 'READ_ENERGY_SERIES', label: 'Read authoritative night-energy periods', status: 'COMPLETED' },
      { id: 'ANALYZE', label: 'Run deterministic night-energy analysis', status: terminal ? 'COMPLETED' : waiting ? 'PAUSED' : 'IN_PROGRESS' },
      { id: 'COMMIT_RESULT', label: 'Commit Evidence, Analysis and Finding', status: terminal ? 'COMPLETED' : 'PENDING' },
    ],
  };
}

const stableActivity = {
  recordId: 'receipt-stable',
  logicalTool: 'registry.getSite',
  owner: 'registry',
  resultCategory: 'SUCCEEDED',
  startedAt: 1,
  completedAt: 2,
};
const finalActivity = {
  recordId: 'receipt-final',
  logicalTool: 'analytics.getEnergySeries',
  owner: 'telemetry-query-service',
  resultCategory: 'SUCCEEDED',
  startedAt: 3,
  completedAt: 4,
};

function scope() {
  return { organizationId, siteId, equipmentId: null, deviceId: null };
}

function evidenceSource({ owner, requestId, registryRevision = null, datasetRevision = null, partial = false, quality = 'GOOD' }) {
  return {
    owner,
    scope: scope(),
    requestId,
    registryRevision,
    datasetRevision,
    watermark: {
      data: datasetRevision ? '2026-08-01T00:00:00.000Z' : null,
      aggregate: datasetRevision ? '2026-08-01T00:05:00.000Z' : null,
    },
    partial,
    quality: { classification: quality, valid: quality === 'GOOD' ? 8 : 4, suspect: quality === 'GOOD' ? 0 : 4, invalid: 0 },
    capturedAt: 5,
    evaluatedAt: 6,
    provenanceDigest: digest(owner === 'registry' ? 'a' : 'b'),
  };
}

function supportedRecords(id) {
  const evidenceId = `${id}:evidence:comparison`;
  const analysisId = `${id}:analysis:comparison`;
  return {
    evidence: [{
      schemaVersion: 1,
      recordType: 'EVIDENCE',
      id: evidenceId,
      investigationId: id,
      recordedAt: 7,
      evidenceKind: 'SITE_ENERGY_PERIOD_COMPARISON',
      classification: 'ALGORITHM_RESULT',
      statement: 'Committed browser recovery evidence confirms a Site night-energy increase.',
      analysisReferenceDigest: digest('c'),
      sources: [
        evidenceSource({ owner: 'registry', requestId: 'request-registry', registryRevision: 'registry-r17' }),
        evidenceSource({ owner: 'telemetry-query-service', requestId: 'request-energy', datasetRevision: 'dataset-r42' }),
      ],
    }],
    analysisReferences: [{
      schemaVersion: 1,
      recordType: 'ANALYSIS_REFERENCE',
      id: analysisId,
      investigationId: id,
      recordedAt: 8,
      analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
      authority: 'DETERMINISTIC_ALGORITHM',
      algorithmVersion: 'night-energy-v1',
      policyVersion: 'quality-policy-v1',
      inputEvidenceIds: [evidenceId],
      parameterDigest: digest('d'),
      resultDigest: digest('e'),
      executedAt: 8,
      outcome: 'SUPPORTED_SITE_FINDING',
    }],
    findings: [{
      schemaVersion: 1,
      recordType: 'FINDING',
      id: `${id}:finding:site`,
      investigationId: id,
      recordedAt: 9,
      findingKind: 'SITE_NIGHT_ENERGY_INCREASE',
      classification: 'INFERENCE',
      statement: 'Recovered Investigation reached the committed Site finding.',
      evidenceIds: [evidenceId],
      analysisReferenceIds: [analysisId],
      conclusion: {
        status: 'SUPPORTED',
        scope: 'SITE',
        organizationId,
        siteId,
      },
    }],
  };
}

function unableRecords(id) {
  const evidenceId = `${id}:evidence:readiness`;
  const analysisId = `${id}:analysis:readiness`;
  const targetPeriod = {
    localDate: '2026-07-31',
    from: '2026-07-31T00:00:00+09:00',
    to: '2026-07-31T08:00:00+09:00',
    expectedBuckets: 8,
  };
  const baselinePeriod = {
    localDate: '2026-07-24',
    from: '2026-07-24T00:00:00+09:00',
    to: '2026-07-24T08:00:00+09:00',
    expectedBuckets: 8,
  };
  return {
    evidence: [{
      schemaVersion: 1,
      recordType: 'EVIDENCE',
      id: evidenceId,
      investigationId: id,
      recordedAt: 10,
      evidenceKind: 'SITE_ENERGY_SERIES_READINESS_ASSESSED',
      classification: 'FACT',
      statement: 'Site energy is available, but Equipment attribution evidence is absent.',
      analysisReferenceDigest: null,
      sources: [
        evidenceSource({ owner: 'telemetry-query-service', requestId: 'request-unable-energy', datasetRevision: 'dataset-r43', partial: true, quality: 'UNCERTAIN' }),
      ],
    }],
    analysisReferences: [{
      schemaVersion: 1,
      recordType: 'ANALYSIS_REFERENCE',
      id: analysisId,
      investigationId: id,
      recordedAt: 11,
      analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
      authority: 'DETERMINISTIC_ALGORITHM',
      algorithmVersion: 'night-energy-v1',
      policyVersion: 'quality-policy-v1',
      inputEvidenceIds: [evidenceId],
      parameterDigest: digest('f'),
      resultDigest: digest('1'),
      executedAt: 11,
      outcome: 'UNABLE_TO_CONCLUDE',
    }],
    findings: [{
      schemaVersion: 1,
      recordType: 'FINDING',
      id: `${id}:finding:unable`,
      investigationId: id,
      recordedAt: 12,
      findingKind: 'UNABLE_TO_CONCLUDE',
      classification: 'INFERENCE',
      statement: 'The Site Investigation cannot produce an Equipment root-cause conclusion.',
      evidenceIds: [evidenceId],
      analysisReferenceIds: [analysisId],
      conclusion: {
        status: 'UNABLE_TO_CONCLUDE',
        scope: 'EQUIPMENT',
        reasonCode: 'EQUIPMENT_ATTRIBUTION_EVIDENCE_MISSING',
        detail: 'Required Equipment binding and period comparison evidence is not available.',
        requiredNext: [{
          status: 'REQUIRED_NEXT',
          kind: 'EQUIPMENT_ENERGY_BINDINGS',
          owner: 'registry',
          capability: 'registry.getEquipmentEnergyBindings',
          organizationId,
          siteId,
          equipmentIds: ['equipment-ahu-01'],
          targetPeriod,
          baselinePeriod,
          requiredMetadata: ['BUSINESS_REVISION', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
        }, {
          status: 'REQUIRED_NEXT',
          kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
          owner: 'telemetry-query-service',
          capability: 'analytics.energy.getEquipmentSeries',
          organizationId,
          siteId,
          equipmentIds: ['equipment-ahu-01'],
          targetPeriod,
          baselinePeriod,
          requiredMetadata: ['DATASET_REVISION', 'WATERMARK', 'PARTIAL', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
        }],
      },
    }],
  };
}

function toolReceipts(id, activities) {
  return activities.map((activity, index) => ({
    schemaVersion: 1,
    recordType: 'TOOL_EXECUTION_RECEIPT',
    id: activity.recordId,
    investigationId: id,
    recordedAt: activity.completedAt,
    logicalTool: activity.logicalTool,
    owner: activity.owner,
    requestId: `${activity.recordId}:request`,
    attemptId: `${activity.recordId}:attempt`,
    runId: `${id}:run`,
    stepId: index === 0 ? 'READ_SITE_CONTEXT' : 'READ_ENERGY_SERIES',
    startedAt: activity.startedAt,
    completedAt: activity.completedAt,
    resultCategory: activity.resultCategory,
    metadata: activity.owner === 'registry'
      ? { registryRevision: 'registry-r17', quality: 'GOOD' }
      : { datasetRevision: 'dataset-r42', watermark: '2026-08-01T00:05:00.000Z', partial: false, quality: 'GOOD' },
  }));
}

function operatorInputRequest(id) {
  return {
    schemaVersion: 1,
    id: `${id}:operator-input-request`,
    investigationId: id,
    runId: `${id}:active-run`,
    scope: scope(),
    kind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
    requestedAt: 9,
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
}

function acceptedOperatorInput(id, idempotencyKey) {
  return {
    schemaVersion: 1,
    recordType: 'OPERATOR_INPUT_ACCEPTED',
    id: `${id}:operator-input-accepted`,
    investigationId: id,
    recordedAt: 10,
    requestId: `${id}:operator-input-request`,
    runId: `${id}:active-run`,
    idempotencyKey,
    inputKind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
    inputDigest: digest('f'),
    scope: scope(),
    values: { analysisScope: 'SITE_ONLY', operatorNote: 'Browser exact-retry acceptance.' },
    provenance: {
      actorType: 'OPERATOR',
      source: 'PLATFORM_GATEWAY',
      authorizationDecisionId: 'browser-operator-decision',
      policyRevision: 'operations-policy-1',
      submittedAt: 10,
    },
  };
}

function investigation(id, revision, status, outcome, activities = []) {
  const records = outcome === 'SUPPORTED_SITE_FINDING'
    ? supportedRecords(id)
    : outcome === 'UNABLE_TO_CONCLUDE'
      ? unableRecords(id)
      : { evidence: [], analysisReferences: [], findings: [] };
  return {
    schemaVersion: 1,
    id,
    scope: scope(),
    status,
    revision,
    createdAt: id === unableInvestigationId ? 2 : 1,
    activeRun: status === 'RUNNING'
      ? { id: `${id}:active-run`, status: 'ACTIVE', startedAt: 1 }
      : status === 'WAITING_FOR_OPERATOR_INPUT'
        ? { id: `${id}:active-run`, status: 'WAITING_FOR_OPERATOR_INPUT', startedAt: 1 }
        : null,
    outcome,
    ...records,
    operatorInputRequest: status === 'WAITING_FOR_OPERATOR_INPUT' ? operatorInputRequest(id) : null,
    acceptedOperatorInputs: [],
    toolReceipts: toolReceipts(id, activities),
  };
}

function stream(id, revision, status, outcome, activities) {
  const runId = `${id}:projection:${revision}`;
  const view = investigation(id, revision, status, outcome, activities);
  const { toolReceipts: _toolReceipts, ...projection } = view;
  const frames = [
    event(`${revision}:0`, 'RUN_STARTED', { threadId: id, runId }),
    event(`${revision}:1`, 'STATE_SNAPSHOT', {
      snapshot: {
        schemaVersion: 'operations-investigation-ui/v1',
        investigation: projection,
        plan: plan(status),
        toolActivities: activities,
      },
    }),
  ];
  activities.forEach((activity, index) => {
    const sequence = 2 + index * 3;
    frames.push(
      event(`${revision}:${sequence}`, 'TOOL_CALL_START', {
        toolCallId: activity.recordId,
        toolCallName: activity.logicalTool,
      }),
      event(`${revision}:${sequence + 1}`, 'TOOL_CALL_ARGS', {
        toolCallId: activity.recordId,
        delta: JSON.stringify(activity),
      }),
      event(`${revision}:${sequence + 2}`, 'TOOL_CALL_END', { toolCallId: activity.recordId }),
    );
  });
  const latest = 2 + activities.length * 3;
  frames.push(event(`${revision}:${latest}`, 'RUN_FINISHED', {
    threadId: id,
    runId,
    outcome: { type: 'success' },
  }));
  return { body: frames.join(''), latest: `${revision}:${latest}` };
}

function problem(response, status, code, detail, retryable) {
  response.writeHead(status, {
    'content-type': 'application/problem+json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify({
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    code,
    retryable,
  }));
}

function json(response, body, status = 200) {
  response.writeHead(status, {
    'content-type': 'application/json',
    'cache-control': 'private, no-store',
  });
  response.end(JSON.stringify(body));
}

function summary(view) {
  return {
    schemaVersion: 1,
    id: view.id,
    scope: view.scope,
    status: view.status,
    revision: view.revision,
    createdAt: view.createdAt,
    outcome: view.outcome,
    evidenceCount: view.evidence.length,
    analysisReferenceCount: view.analysisReferences.length,
    findingCount: view.findings.length,
    toolReceiptCount: view.toolReceipts.length,
    acceptedOperatorInputCount: view.acceptedOperatorInputs.length,
  };
}

function createGatewayFixture() {
  const requests = [];
  let supportedEventRequestCount = 0;
  let hiddenRequestCount = 0;
  let listRequestCount = 0;
  let operatorInputAccepted = false;
  let operatorInputRetryAcknowledged = false;
  let operatorInputIdempotencyKey = null;
  let cancelAccepted = false;
  let cancelRequestCount = 0;
  const operatorInputSubmissions = [];
  const collectionPath = `/api/v1/sites/${siteId}/operations/investigations`;
  const itemPrefix = `${collectionPath}/`;
  const operatorInputSubmitPath = `${itemPrefix}${operatorInputInvestigationId}:submit-operator-input`;
  const cancelPath = `${itemPrefix}${cancelInvestigationId}:cancel`;
  const currentSupported = () => supportedEventRequestCount >= 3
    ? investigation(investigationId, 2, 'COMPLETED', 'SUPPORTED_SITE_FINDING', [stableActivity, finalActivity])
    : investigation(investigationId, 1, 'RUNNING', null, [stableActivity]);
  const unable = investigation(unableInvestigationId, 5, 'COMPLETED', 'UNABLE_TO_CONCLUDE', [stableActivity]);
  const currentCancel = () => cancelAccepted
    ? investigation(cancelInvestigationId, 4, 'CANCELLED', null, [stableActivity])
    : investigation(cancelInvestigationId, 3, 'RUNNING', null, [stableActivity]);
  const currentOperatorInput = () => {
    if (!operatorInputRetryAcknowledged) {
      return investigation(operatorInputInvestigationId, 7, 'WAITING_FOR_OPERATOR_INPUT', null, [stableActivity]);
    }
    const completed = investigation(
      operatorInputInvestigationId,
      8,
      'COMPLETED',
      'SUPPORTED_SITE_FINDING',
      [stableActivity, finalActivity],
    );
    return {
      ...completed,
      acceptedOperatorInputs: [acceptedOperatorInput(operatorInputInvestigationId, operatorInputIdempotencyKey)],
    };
  };

  const server = createHTTPServer(async (request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    if (request.method === 'GET' && url.pathname === '/api/v1/principal') {
      json(response, {
        principal: {
          subject: 'operations-reconnect-audit',
          issuer: 'https://identity.example.test',
          displayName: 'Operations Auditor',
          email: '',
          roles: ['operator'],
        },
        context: {
          initiatingPrincipal: {
            subject: 'operations-reconnect-audit',
            issuer: 'https://identity.example.test',
            displayName: 'Operations Auditor',
            email: '',
            roles: ['operator'],
          },
          executingServicePrincipal: {
            service: 'platform-gateway',
            spiffeId: 'spiffe://hvac.local/platform-gateway',
          },
          actingOrganizationId: organizationId,
          audience: 'iam-service',
          policyRevision: 'operations-policy-1',
          delegationExpiresAt: '2026-08-02T00:00:00.000Z',
        },
        authorization: {
          capabilitySetVersion: 4,
          policyRevision: 'operations-policy-1',
          capabilities: ['site.read'],
        },
        session: {
          id: 'operations-reconnect-session',
          expiresAt: '2026-08-02T00:00:00.000Z',
          csrfToken: '[REDACTED_SECRET]',
          revocationObjectiveMs: 30_000,
          lastAuditMessageId: 'operations-reconnect-audit-message',
        },
      });
      return;
    }
    if (request.method === 'POST' && url.pathname === cancelPath) {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      if (Object.keys(body).length !== 0) {
        problem(response, 400, 'REQUEST_INVALID', 'Cancel accepts an empty bounded object.', false);
        return;
      }
      cancelRequestCount += 1;
      cancelAccepted = true;
      json(response, currentCancel());
      return;
    }
    if (request.method === 'POST' && url.pathname === operatorInputSubmitPath) {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      const idempotencyKey = String(request.headers['idempotency-key'] ?? '');
      operatorInputSubmissions.push({ idempotencyKey, body });
      if (!operatorInputAccepted) {
        operatorInputAccepted = true;
        operatorInputIdempotencyKey = idempotencyKey;
        problem(response, 502, 'OPERATIONS_AGENT_BAD_GATEWAY', 'Synthetic response loss after atomic acceptance.', true);
        return;
      }
      if (idempotencyKey !== operatorInputIdempotencyKey) {
        problem(response, 409, 'DUPLICATE_EFFECT', 'Exact retry must reuse the original identity.', false);
        return;
      }
      operatorInputRetryAcknowledged = true;
      json(response, { outcome: 'DUPLICATE', investigation: currentOperatorInput() });
      return;
    }
    if (request.method !== 'GET') {
      problem(response, 405, 'METHOD_NOT_ALLOWED', 'Fixture accepts this method only on the Operator Input route.', false);
      return;
    }
    if (url.pathname === collectionPath) {
      listRequestCount += 1;
      json(response, {
        schemaVersion: 1,
        investigations: [summary(unable), summary(currentSupported())]
          .sort((left, right) => right.createdAt - left.createdAt || right.id.localeCompare(left.id)),
      });
      return;
    }
    if (!url.pathname.startsWith(itemPrefix)) {
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'Route not found.', false);
      return;
    }
    const relative = url.pathname.slice(itemPrefix.length);
    const isEvents = relative.endsWith('/events');
    const encodedId = isEvents ? relative.slice(0, -'/events'.length) : relative;
    const requestedId = decodeURIComponent(encodedId);
    if (requestedId !== investigationId
      && requestedId !== unableInvestigationId
      && requestedId !== operatorInputInvestigationId
      && requestedId !== cancelInvestigationId) {
      hiddenRequestCount += 1;
      problem(response, 404, 'RESOURCE_NOT_FOUND', 'The Investigation is not visible.', false);
      return;
    }
    if (!isEvents) {
      json(response, requestedId === investigationId
        ? currentSupported()
        : requestedId === unableInvestigationId
          ? unable
          : requestedId === operatorInputInvestigationId
            ? currentOperatorInput()
            : currentCancel());
      return;
    }

    if (requestedId === cancelInvestigationId) {
      const view = currentCancel();
      const current = stream(
        cancelInvestigationId,
        view.revision,
        view.status,
        view.outcome,
        view.toolReceipts.map((receipt) => ({
          recordId: receipt.id,
          logicalTool: receipt.logicalTool,
          owner: receipt.owner,
          resultCategory: receipt.resultCategory,
          startedAt: receipt.startedAt,
          completedAt: receipt.completedAt,
        })),
      );
      response.writeHead(200, {
        'content-type': 'text/event-stream; charset=utf-8',
        'cache-control': 'no-store, no-transform',
        'x-operations-recovery-mode': 'FULL_SNAPSHOT',
        'x-operations-recovery-reason': 'INITIAL',
        'x-operations-snapshot-position': `${view.revision}:1`,
        'x-operations-latest-position': current.latest,
      });
      response.end(current.body);
      return;
    }

    if (requestedId === operatorInputInvestigationId) {
      const view = currentOperatorInput();
      const current = stream(
        operatorInputInvestigationId,
        view.revision,
        view.status,
        view.outcome,
        view.toolReceipts.map((receipt) => ({
          recordId: receipt.id,
          logicalTool: receipt.logicalTool,
          owner: receipt.owner,
          resultCategory: receipt.resultCategory,
          startedAt: receipt.startedAt,
          completedAt: receipt.completedAt,
        })),
      );
      const projected = current.body.replace(
        '"acceptedOperatorInputs":[]',
        `"acceptedOperatorInputs":${JSON.stringify(view.acceptedOperatorInputs)}`,
      );
      response.writeHead(200, {
        'content-type': 'text/event-stream; charset=utf-8',
        'cache-control': 'no-store, no-transform',
        'x-operations-recovery-mode': 'FULL_SNAPSHOT',
        'x-operations-recovery-reason': 'INITIAL',
        'x-operations-snapshot-position': `${view.revision}:1`,
        'x-operations-latest-position': current.latest,
      });
      response.end(projected);
      return;
    }

    if (requestedId === unableInvestigationId) {
      const current = stream(unableInvestigationId, 5, 'COMPLETED', 'UNABLE_TO_CONCLUDE', [stableActivity]);
      response.writeHead(200, {
        'content-type': 'text/event-stream; charset=utf-8',
        'cache-control': 'no-store, no-transform',
        'x-operations-recovery-mode': 'FULL_SNAPSHOT',
        'x-operations-recovery-reason': 'INITIAL',
        'x-operations-snapshot-position': '5:1',
        'x-operations-latest-position': current.latest,
      });
      response.end(current.body);
      return;
    }

    supportedEventRequestCount += 1;
    const recoveryPosition = request.headers['last-event-id'] ?? null;
    requests.push({ requestNumber: supportedEventRequestCount, recoveryPosition });
    if (supportedEventRequestCount === 2) {
      problem(response, 502, 'OPERATIONS_AGENT_BAD_GATEWAY', 'Synthetic network interruption.', true);
      return;
    }
    const current = supportedEventRequestCount === 1
      ? stream(investigationId, 1, 'RUNNING', null, [stableActivity])
      : stream(investigationId, 2, 'COMPLETED', 'SUPPORTED_SITE_FINDING', [stableActivity, finalActivity]);
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-store, no-transform',
      'x-operations-recovery-mode': 'FULL_SNAPSHOT',
      'x-operations-recovery-reason': supportedEventRequestCount === 1 ? 'INITIAL' : 'EXPIRED',
      'x-operations-snapshot-position': supportedEventRequestCount === 1 ? '1:1' : '2:1',
      'x-operations-latest-position': current.latest,
    });
    response.end(current.body);
  });
  return {
    server,
    requests,
    operatorInputSubmissions,
    cancelRequests: () => cancelRequestCount,
    hiddenRequests: () => hiddenRequestCount,
    listRequests: () => listRequestCount,
  };
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.on('open', () => resolveClient({
      events,
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.on('error', rejectClient);
    socket.on('message', (raw) => {
      const message = JSON.parse(String(raw));
      if (!message.id) {
        events.push(message);
        return;
      }
      const command = pending.get(message.id);
      if (!command) return;
      pending.delete(message.id);
      if (message.error) command.rejectCommand(new Error(message.error.message));
      else command.resolveCommand(message.result);
    });
  });
}

async function evaluate(client, expression) {
  const response = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  }
  return response.result.value;
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped && process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else if (!stopped) {
    child.kill('SIGKILL');
  }
}

const browserCandidates = [
  process.env.BROWSER_BINARY,
  process.env['PROGRAMFILES(X86)'] ? join(process.env['PROGRAMFILES(X86)'], 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  process.env.PROGRAMFILES ? join(process.env.PROGRAMFILES, 'Microsoft', 'Edge', 'Application', 'msedge.exe') : null,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
].filter(Boolean);
const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
if (!browserPath) throw new Error('A CDP-compatible browser was not found');

const gatewayPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const fixture = createGatewayFixture();
let viteServer;
let browserProcess;
let cdpClient;
let conclusion = 'failed';
const assertions = [];

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });
  viteServer = await createViteServer({
    root: fixtureRoot,
    configFile: false,
    logLevel: 'error',
    define: { __HVAC_WEB_BUILD_TARGET__: JSON.stringify('real') },
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '127.0.0.1',
      port: 0,
      strictPort: false,
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const viteOrigin = `http://127.0.0.1:${viteAddress.port}`;
  const webURL = `${viteOrigin}/operations?investigation=${investigationId}`;

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    'about:blank',
  ], { stdio: 'ignore' });
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break;
    } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Log.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Page.navigate', { url: webURL });

  let sawRetrying = false;
  let terminal = false;
  let lastState = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      href: location.href,
      readyState: document.readyState,
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      investigation: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      outcome: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-outcome') ?? null,
      text: document.body?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    lastState = state;
    if (state?.connection === 'RETRYING') sawRetrying = true;
    if (state?.connection === 'TERMINAL'
      && state?.investigation === 'COMPLETED'
      && state?.outcome === 'SUPPORTED_SITE_FINDING'
      && state.text.includes('Revision 2')
      && state.text.includes('Recovered Investigation reached the committed Site finding.')) {
      terminal = true;
      break;
    }
    await pause(100);
  }
  assert(
    terminal,
    `Operations reconnect browser audit did not reach the committed terminal snapshot; last=${JSON.stringify(lastState)} requests=${JSON.stringify(fixture.requests)} events=${JSON.stringify(cdpClient.events.slice(-20))}`,
  );
  assert(sawRetrying, 'Operations reconnect browser audit did not expose a stable retrying state');
  assertions.push('retryable-interruption-visible-and-recovered');

  let supportedState = null;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    await evaluate(cdpClient, `document.querySelectorAll('.operations-record-card details').forEach((details) => { details.open = true; })`);
    supportedState = await evaluate(cdpClient, `({
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status'),
      investigation: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status'),
      toolCount: document.querySelectorAll('.operations-tools > li').length,
      evidenceCount: document.querySelectorAll('[data-record-type="EVIDENCE"]').length,
      analysisCount: document.querySelectorAll('[data-record-type="ANALYSIS_REFERENCE"]').length,
      findingCount: document.querySelectorAll('[data-record-type="FINDING"]').length,
      listCount: document.querySelectorAll('.operations-list-item').length,
      text: document.body.textContent ?? '',
      protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
    })`);
    if (supportedState.listCount === 2 && supportedState.toolCount === 2) break;
    await pause(100);
  }
  assert(supportedState.connection === 'TERMINAL' && supportedState.investigation === 'COMPLETED', 'terminal UI state is unstable');
  assert(supportedState.toolCount === 2, 'committed Tool receipts were duplicated or lost');
  assert(supportedState.evidenceCount === 1 && supportedState.analysisCount === 1 && supportedState.findingCount === 1, 'typed committed records were duplicated or lost');
  assert(supportedState.listCount === 2, 'Site Investigation list did not expose both authorized records');
  for (const requiredText of [
    'dataset-r42',
    'Data watermark',
    'GOOD',
    'Partial',
    'DETERMINISTIC_ALGORITHM',
    'Site-only conclusion',
    '不构成 Equipment root cause',
  ]) {
    assert(supportedState.text.includes(requiredText), `supported Workspace omitted ${requiredText}`);
  }
  assert(supportedState.protectedResourceId === `operations-investigation:${siteId}:${investigationId}`, 'protected resource was not registered');
  assertions.push('typed-provenance-and-site-only-finding');

  assert(fixture.requests.length === 3, `expected exactly three authorized event requests, got ${fixture.requests.length}`);
  assert(fixture.requests[0].recoveryPosition === null, 'initial connection unexpectedly supplied a recovery position');
  assert(fixture.requests[1].recoveryPosition === '1:5' && fixture.requests[2].recoveryPosition === '1:5', 'reconnect did not retain the stable last position');
  assertions.push('stable-last-event-id-across-retry');

  await evaluate(cdpClient, `(() => {
    history.pushState(null, '', '?investigation=${operatorInputInvestigationId}');
    dispatchEvent(new PopStateEvent('popstate'));
  })()`);
  let operatorWaiting = null;
  for (let attempt = 0; attempt < 150; attempt += 1) {
    operatorWaiting = await evaluate(cdpClient, `({
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      form: Boolean(document.querySelector('.operations-operator-input-form')),
      advanceDisabled: document.querySelector('.operations-open button[title="先提交当前 Operator Input。"]')?.disabled ?? false,
      text: document.body?.innerText ?? '',
    })`);
    if (operatorWaiting.status === 'WAITING_FOR_OPERATOR_INPUT'
      && operatorWaiting.form
      && operatorWaiting.advanceDisabled
      && operatorWaiting.text.includes('ACTION REQUIRED')) break;
    await pause(100);
  }
  assert(operatorWaiting?.status === 'WAITING_FOR_OPERATOR_INPUT' && operatorWaiting.form, `Operator Input form did not stabilize: ${JSON.stringify(operatorWaiting)}`);
  assert(operatorWaiting.advanceDisabled, 'ordinary advance remained enabled during Operator Input interrupt');

  const submitOperatorInput = async () => evaluate(cdpClient, `(() => {
    const note = document.querySelector('#operations-operator-note');
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
    setter.call(note, 'Browser exact-retry acceptance.');
    note.dispatchEvent(new Event('input', { bubbles: true }));
    document.querySelector('.operations-operator-input-form button[type="submit"]').click();
    return true;
  })()`);
  await submitOperatorInput();
  let ambiguousFailureVisible = false;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      alert: document.querySelector('[role="alert"]')?.innerText ?? '',
      form: Boolean(document.querySelector('.operations-operator-input-form')),
    })`);
    if (state.form && state.alert.includes('Synthetic response loss after atomic acceptance.')) {
      ambiguousFailureVisible = true;
      break;
    }
    await pause(100);
  }
  assert(ambiguousFailureVisible, 'ambiguous Operator Input response loss did not preserve the retry form');
  await submitOperatorInput();

  let operatorCompleted = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    operatorCompleted = await evaluate(cdpClient, `({
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      acceptedCount: document.querySelectorAll('.operations-operator-input-history li').length,
      form: Boolean(document.querySelector('.operations-operator-input-form')),
      text: document.body?.innerText ?? '',
    })`);
    if (operatorCompleted.status === 'COMPLETED'
      && operatorCompleted.acceptedCount === 1
      && !operatorCompleted.form
      && operatorCompleted.text.includes('Browser exact-retry acceptance.')) break;
    await pause(100);
  }
  assert(operatorCompleted?.status === 'COMPLETED' && operatorCompleted.acceptedCount === 1, `Operator Input exact retry did not complete: ${JSON.stringify(operatorCompleted)}`);
  assert(fixture.operatorInputSubmissions.length === 2, `expected two Operator Input submissions, got ${fixture.operatorInputSubmissions.length}`);
  assert(
    fixture.operatorInputSubmissions[0].idempotencyKey === fixture.operatorInputSubmissions[1].idempotencyKey,
    'ambiguous Operator Input retry changed the Idempotency Key',
  );
  assert(
    fixture.operatorInputSubmissions[0].body.requestId === `${operatorInputInvestigationId}:operator-input-request`
      && fixture.operatorInputSubmissions[1].body.expectedRevision === 7,
    'Operator Input retry changed the committed Request identity or revision',
  );
  assertions.push('operator-input-exact-retry-same-run');

  const openedUnable = await evaluate(cdpClient, `(() => {
    const button = [...document.querySelectorAll('.operations-list-item')]
      .find((candidate) => candidate.textContent.includes(${JSON.stringify(unableInvestigationId)}));
    if (!button) return false;
    button.click();
    return true;
  })()`);
  assert(openedUnable, 'unable-to-conclude Investigation was not navigable from the Site list');

  let unableVisible = false;
  let unableState = null;
  for (let attempt = 0; attempt < 150; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      href: location.href,
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      outcome: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-outcome') ?? null,
      requiredNextCount: document.querySelectorAll('.operations-required-next-card').length,
      text: document.body?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    unableState = state;
    if (state.connection === 'TERMINAL'
      && state.outcome === 'UNABLE_TO_CONCLUDE'
      && state.requiredNextCount === 2
      && state.href.includes(`investigation=${unableInvestigationId}`)) {
      unableVisible = true;
      break;
    }
    await pause(100);
  }
  assert(unableVisible, `unable-to-conclude Workspace did not stabilize: ${JSON.stringify(unableState)}`);
  for (const requiredText of [
    'UNABLE TO CONCLUDE',
    'EQUIPMENT_ATTRIBUTION_EVIDENCE_MISSING',
    'registry.getEquipmentEnergyBindings',
    'analytics.energy.getEquipmentSeries',
    'BUSINESS_REVISION',
    'DATASET_REVISION',
    'WATERMARK',
    'PARTIAL',
  ]) {
    assert(unableState.text.includes(requiredText), `unable Workspace omitted ${requiredText}`);
  }
  assertions.push('unable-to-conclude-required-next');

  await evaluate(cdpClient, `globalThis.__OPERATIONS_RECONNECT_AUDIT__.navigate('/operations?investigation=${cancelInvestigationId}')`);
  let cancelRunning = null;
  for (let attempt = 0; attempt < 150; attempt += 1) {
    cancelRunning = await evaluate(cdpClient, `({
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      cancelDisabled: document.querySelector('[data-testid="operations-cancel"]')?.disabled ?? true,
      primary: document.querySelector('[data-primary-agent-experience="true"]')?.getAttribute('data-primary-agent-experience') ?? null,
      protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
    })`);
    if (cancelRunning.status === 'RUNNING'
      && !cancelRunning.cancelDisabled
      && cancelRunning.primary === 'true'
      && cancelRunning.protectedResourceId === `operations-investigation:${siteId}:${cancelInvestigationId}`) break;
    await pause(100);
  }
  assert(cancelRunning?.status === 'RUNNING' && !cancelRunning.cancelDisabled, `cancellable Investigation did not stabilize: ${JSON.stringify(cancelRunning)}`);
  await evaluate(cdpClient, `document.querySelector('[data-testid="operations-cancel"]').click()`);

  let cancelledState = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    cancelledState = await evaluate(cdpClient, `({
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      cancelDisabled: document.querySelector('[data-testid="operations-cancel"]')?.disabled ?? false,
      advanceDisabled: document.querySelector('[data-testid="operations-advance"]')?.disabled ?? false,
    })`);
    if (cancelledState.connection === 'TERMINAL'
      && cancelledState.status === 'CANCELLED'
      && cancelledState.cancelDisabled
      && cancelledState.advanceDisabled) break;
    await pause(100);
  }
  assert(cancelledState?.status === 'CANCELLED' && cancelledState.connection === 'TERMINAL', `cancelled Investigation did not stabilize: ${JSON.stringify(cancelledState)}`);
  assert(fixture.cancelRequests() === 1, `cancel mutation executed ${fixture.cancelRequests()} times`);

  await cdpClient.send('Page.reload', { ignoreCache: true });
  let terminalReload = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    terminalReload = await evaluate(cdpClient, `({
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      href: location.href,
    })`).catch((error) => ({ error: String(error) }));
    if (terminalReload.connection === 'TERMINAL'
      && terminalReload.status === 'CANCELLED'
      && terminalReload.href.includes(`investigation=${cancelInvestigationId}`)) break;
    await pause(100);
  }
  assert(terminalReload?.status === 'CANCELLED' && terminalReload.connection === 'TERMINAL', `terminal reload did not rebuild the cancelled Investigation: ${JSON.stringify(terminalReload)}`);
  assert(fixture.cancelRequests() === 1, 'terminal reload repeated the cancel mutation');

  await evaluate(cdpClient, `globalThis.__OPERATIONS_RECONNECT_AUDIT__.navigate('/dashboard')`);
  let routeLeft = null;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    routeLeft = await evaluate(cdpClient, `({
      left: Boolean(document.querySelector('[data-testid="operations-route-left"]')),
      workspace: Boolean(document.querySelector('.operations-workspace')),
      protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
    })`);
    if (routeLeft.left && !routeLeft.workspace && routeLeft.protectedResourceId === null) break;
    await pause(50);
  }
  assert(routeLeft?.left && !routeLeft.workspace && routeLeft.protectedResourceId === null, `route leave did not purge protected Operations state: ${JSON.stringify(routeLeft)}`);

  await evaluate(cdpClient, `globalThis.__OPERATIONS_RECONNECT_AUDIT__.navigate('/operations?investigation=${cancelInvestigationId}')`);
  let returnedTerminal = null;
  for (let attempt = 0; attempt < 200; attempt += 1) {
    returnedTerminal = await evaluate(cdpClient, `({
      connection: document.querySelector('.operations-connection')?.getAttribute('data-connection-status') ?? null,
      status: document.querySelector('.operations-workspace')?.getAttribute('data-investigation-status') ?? null,
      protectedResourceId: globalThis.__OPERATIONS_RECONNECT_AUDIT__?.protectedResourceId(),
    })`);
    if (returnedTerminal.connection === 'TERMINAL'
      && returnedTerminal.status === 'CANCELLED'
      && returnedTerminal.protectedResourceId === `operations-investigation:${siteId}:${cancelInvestigationId}`) break;
    await pause(100);
  }
  assert(returnedTerminal?.status === 'CANCELLED' && returnedTerminal.connection === 'TERMINAL', `route return did not rebuild authoritative terminal state: ${JSON.stringify(returnedTerminal)}`);
  assert(fixture.cancelRequests() === 1, 'route return repeated the cancel mutation');
  assertions.push('cancel-terminal-reload-and-route-leave-purge');

  await cdpClient.send('Page.navigate', {
    url: `${viteOrigin}/operations?investigation=${hiddenInvestigationId}`,
  });
  let nondiscoverableVisible = false;
  let hiddenDiagnostic = null;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const state = await evaluate(cdpClient, `({
      text: document.body?.innerText ?? '',
      alert: document.querySelector('[role="alert"]')?.innerText ?? '',
    })`).catch((error) => ({ error: String(error) }));
    hiddenDiagnostic = state;
    if (state.alert.includes('Investigation not visible')) {
      nondiscoverableVisible = true;
      break;
    }
    await pause(100);
  }
  assert(nondiscoverableVisible, `nondiscoverable UX did not stabilize: ${JSON.stringify(hiddenDiagnostic)}`);
  await pause(1000);
  assert(fixture.hiddenRequests() === 1, `nondiscoverable Investigation retried ${fixture.hiddenRequests()} times`);
  assertions.push('nondiscoverable-stable-no-retry');

  conclusion = 'passed';
  const evidence = {
    schemaVersion: 1,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    requests: fixture.requests,
    operatorInputSubmissions: fixture.operatorInputSubmissions,
    cancelRequestCount: fixture.cancelRequests(),
    listRequestCount: fixture.listRequests(),
    hiddenRequestCount: fixture.hiddenRequests(),
    finalState: {
      connection: supportedState.connection,
      investigation: supportedState.investigation,
      toolCount: supportedState.toolCount,
      evidenceCount: supportedState.evidenceCount,
      analysisCount: supportedState.analysisCount,
      findingCount: supportedState.findingCount,
      requiredNextCount: unableState.requiredNextCount,
      cancelledInvestigation: returnedTerminal.status,
    },
    safety: {
      productionTrafficPercent: 0,
      localOnly: true,
      duplicateDurableRecords: false,
      businessWrites: 2,
      exactRetryBusinessWrites: 0,
      rawPointsRendered: false,
      equipmentRootCauseClaimed: false,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Operations Investigation Workspace browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  try {
    await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 });
  } catch (error) {
    console.warn(`Operations reconnect browser profile cleanup was deferred: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({
      schemaVersion: 1,
      passed: false,
      generatedAt: new Date().toISOString(),
      assertions,
      requests: fixture.requests,
      cancelRequestCount: fixture.cancelRequests(),
      listRequestCount: fixture.listRequests(),
      hiddenRequestCount: fixture.hiddenRequests(),
    }, null, 2));
  }
}
