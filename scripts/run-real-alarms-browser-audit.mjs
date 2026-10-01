import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer as createHTTPServer } from 'node:http';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { createServer as createViteServer } from 'vite';
import WebSocket from 'ws';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const fixtureRoot = resolve(root, 'scripts/fixtures/real-alarms');
const outputRoot = resolve(root, 'out/real-alarms-certification');
const profileDir = join(tmpdir(), `real-alarms-browser-${process.pid}`);
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

const tenantId = '01910000-0000-7000-8000-000000000001';
const siteAId = '01910000-0001-7000-8000-000000000001';
const siteBId = '01910000-0002-7000-8000-000000000002';
const alarmAActiveId = '01910000-1000-7000-8000-000000000001';
const alarmAClearedId = '01910000-1000-7000-8000-000000000002';
const alarmBId = '01910000-1000-7000-8000-000000000003';
const alarmACorrelatedId = '01910000-1000-7000-8000-000000000004';
const deviceAId = '01910000-2000-7000-8000-000000000001';
const deviceBId = '01910000-2000-7000-8000-000000000002';
const deviceA2Id = '01910000-2000-7000-8000-000000000003';
const ruleAId = '01910000-3000-7000-8000-000000000001';
const ruleARevision3Id = '01910000-3100-7000-8000-000000000003';
const ruleARevision4Id = '01910000-3100-7000-8000-000000000004';
const ruleABindingId = '01910000-3200-7000-8000-000000000001';
const diagnosisAssetAId = '01910000-4000-7000-8000-000000000001';
const diagnosisAssetBId = '01910000-4000-7000-8000-000000000002';
const findingAId = '01910000-4100-7000-8000-000000000001';
const findingACorrelatedId = '01910000-4100-7000-8000-000000000002';
const findingBId = '01910000-4100-7000-8000-000000000003';

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

function timelineEntry({
  operation = 'PUBLISH',
  condition = 'ACTIVE',
  currentSeverity = 'MAJOR',
  reason = `ALARM_${operation}`,
  occurredAt = '2026-07-31T09:00:00Z',
  version = 1,
  actorId = operation === 'PUBLISH' ? 'alarm-evaluator' : 'principal:alarm-operator',
  assigneeId,
} = {}) {
  return {
    operation,
    condition,
    reason,
    actorType: operation === 'PUBLISH' ? 'WORKLOAD' : 'PRINCIPAL',
    actorId,
    ...(assigneeId ? { assigneeId } : {}),
    currentSeverity,
    policyRevision: 'alarm-policy-1',
    correlationId: `alarm-audit-${version}-${operation.toLowerCase()}`,
    occurredAt,
    version,
  };
}

function alarm({
  alarmId,
  siteId,
  deviceId,
  title,
  summary,
  severity,
  condition = 'ACTIVE',
  occurrenceCount,
  sourceReference,
  lastOccurredAt,
  acknowledgement,
  assigneeId,
  incidentCorrelationId = alarmId,
}) {
  const timeline = [timelineEntry({ currentSeverity: severity })];
  let clearedAt;
  if (acknowledgement) {
    timeline.push(timelineEntry({
      operation: 'ACKNOWLEDGE',
      condition: 'ACTIVE',
      currentSeverity: severity,
      reason: acknowledgement.comment ?? 'operator acknowledged alarm',
      occurredAt: acknowledgement.acknowledgedAt,
      version: timeline.length + 1,
      actorId: acknowledgement.acknowledgedBy,
    }));
  }
  if (assigneeId) {
    timeline.push(timelineEntry({
      operation: 'ASSIGN',
      condition: 'ACTIVE',
      currentSeverity: severity,
      reason: 'assigned for investigation',
      occurredAt: lastOccurredAt,
      version: timeline.length + 1,
      assigneeId,
    }));
  }
  if (condition === 'CLEARED') {
    clearedAt = lastOccurredAt;
    timeline.push(timelineEntry({
      operation: 'CLEAR',
      condition: 'CLEARED',
      currentSeverity: severity,
      reason: 'clear predicate matched',
      occurredAt: clearedAt,
      version: timeline.length + 1,
      actorId: 'alarm-evaluator',
    }));
  }
  return {
    schemaVersion: 2,
    alarmId,
    tenantId,
    siteId,
    ...(deviceId ? { deviceId } : {}),
    alarmType: title.includes('temperature') ? 'SUPPLY_TEMPERATURE_DRIFT' : 'PLANT_DIFFERENTIAL_PRESSURE',
    fingerprint: alarmId === alarmAActiveId ? 'a'.repeat(64) : alarmId === alarmAClearedId ? 'b'.repeat(64) : 'c'.repeat(64),
    incidentCorrelationId,
    sourceType: deviceId ? 'DEVICE_RULE' : 'SITE_RULE',
    sourceReference,
    ruleRevision: 'alarm-policy-1',
    title,
    summary,
    condition,
    currentSeverity: severity,
    peakSeverity: severity,
    ...(acknowledgement ? { acknowledgement } : {}),
    ...(assigneeId ? { assigneeId } : {}),
    occurrenceCount,
    firstOccurredAt: '2026-07-31T09:00:00Z',
    lastOccurredAt,
    ...(clearedAt ? { clearedAt } : {}),
    evidence: [{ kind: 'telemetry-snapshot', reference: `snapshot:${alarmId.slice(-3)}`, capturedAt: lastOccurredAt }],
    links: deviceId ? [{ kind: 'DEVICE', targetId: deviceId }] : [],
    timeline,
    version: timeline.length,
    createdAt: '2026-07-31T09:00:00Z',
    updatedAt: lastOccurredAt,
  };
}

const alarmsBySite = new Map([
  [siteAId, [
    alarm({
      alarmId: alarmAActiveId,
      siteId: siteAId,
      deviceId: deviceAId,
      title: 'CH-03 冷水机组出水温度偏高',
      summary: '冷水机组出水温度持续高于告警条件，当前异常仍处于活动状态。',
      severity: 'CRITICAL',
      condition: 'ACTIVE',
      occurrenceCount: 3,
      sourceReference: 'rule:tokyo-supply-temperature-drift:v4',
      lastOccurredAt: '2026-07-31T09:15:00Z',
    }),
    alarm({
      alarmId: alarmACorrelatedId,
      siteId: siteAId,
      deviceId: deviceA2Id,
      title: 'CH-02 冷水机组出水温度偏高',
      summary: '另一台冷水机组出现同类出水温度异常；属于同一事件线索，但不代表已确认共同原因。',
      severity: 'MAJOR',
      condition: 'ACTIVE',
      occurrenceCount: 2,
      sourceReference: 'rule:tokyo-secondary-supply-temperature-drift:v4',
      lastOccurredAt: '2026-07-31T09:14:00Z',
      incidentCorrelationId: alarmAActiveId,
      acknowledgement: {
        acknowledgedAt: '2026-07-31T09:12:00Z',
        acknowledgedBy: 'principal:tokyo-operator',
        comment: 'acknowledged for investigation',
      },
    }),
    alarm({
      alarmId: alarmAClearedId,
      siteId: siteAId,
      title: '冷冻水系统压差偏低',
      summary: '冷冻水系统压差异常已由告警运行时确认恢复，保留本次发生与处置记录。',
      severity: 'MAJOR',
      condition: 'CLEARED',
      occurrenceCount: 1,
      sourceReference: 'rule:tokyo-plant-pressure:v2',
      lastOccurredAt: '2026-07-31T09:10:00Z',
    }),
  ]],
  [siteBId, [
    alarm({
      alarmId: alarmBId,
      siteId: siteBId,
      deviceId: deviceBId,
      title: 'Osaka condenser approach',
      summary: 'Alarm owner published an acknowledged condenser approach exception.',
      severity: 'WARNING',
      condition: 'ACTIVE',
      occurrenceCount: 2,
      sourceReference: 'rule:osaka-condenser-approach:v1',
      lastOccurredAt: '2026-07-31T09:20:00Z',
      acknowledgement: {
        acknowledgedAt: '2026-07-31T09:10:00Z',
        acknowledgedBy: 'principal:alarm-operator',
        comment: 'known and under investigation',
      },
      assigneeId: 'operator:osaka-night-shift',
    }),
  ]],
]);

const findingsBySite = new Map([
  [siteAId, [
    {
      id: findingAId,
      tenantId,
      siteId: siteAId,
      assetId: diagnosisAssetAId,
      findingType: '冷水机组出水温度持续偏高',
      evaluationFrom: '2026-07-31T08:55:00Z',
      evaluationTo: '2026-07-31T09:15:00Z',
      evidenceIds: ['evidence:ch03:supply-temperature', 'evidence:ch03:running-state'],
      modelDeploymentRevisionId: '',
      ruleRevisionId: 'fdd:chiller-supply-temperature:r4',
      confidence: 0.86,
      alarmId: alarmAActiveId,
      workOrderId: '',
      createdAt: '2026-07-31T09:16:00Z',
    },
    {
      id: findingACorrelatedId,
      tenantId,
      siteId: siteAId,
      assetId: diagnosisAssetAId,
      findingType: '同类出水温度异常',
      evaluationFrom: '2026-07-31T09:00:00Z',
      evaluationTo: '2026-07-31T09:14:00Z',
      evidenceIds: ['evidence:ch02:supply-temperature'],
      modelDeploymentRevisionId: '',
      ruleRevisionId: 'fdd:chiller-supply-temperature:r4',
      confidence: 0.74,
      qualityBlocker: '关联负荷测点存在缺口',
      alarmId: alarmACorrelatedId,
      workOrderId: '',
      createdAt: '2026-07-31T09:15:00Z',
    },
  ]],
  [siteBId, [
    {
      id: findingBId,
      tenantId,
      siteId: siteBId,
      assetId: diagnosisAssetBId,
      findingType: '冷凝器逼近温度异常',
      evaluationFrom: '2026-07-31T09:00:00Z',
      evaluationTo: '2026-07-31T09:20:00Z',
      evidenceIds: ['evidence:ch01:condenser-approach'],
      modelDeploymentRevisionId: '',
      ruleRevisionId: 'fdd:condenser-approach:r1',
      confidence: 0.79,
      alarmId: alarmBId,
      workOrderId: '',
      createdAt: '2026-07-31T09:21:00Z',
    },
  ]],
]);

function alarmAssetModel(siteId) {
  const now = '2026-07-31T00:00:00.000Z';
  const devices = siteId === siteAId
    ? [
        {
          id: deviceAId,
          tenantId,
          siteId,
          code: 'CH-03',
          displayName: 'CH-03 冷水机组',
          deviceType: 'CHILLER',
          status: 'ACTIVE',
          revision: 1,
          createdAt: now,
          updatedAt: now,
        },
        {
          id: deviceA2Id,
          tenantId,
          siteId,
          code: 'CH-02',
          displayName: 'CH-02 冷水机组',
          deviceType: 'CHILLER',
          status: 'ACTIVE',
          revision: 1,
          createdAt: now,
          updatedAt: now,
        },
      ]
    : siteId === siteBId
      ? [{
          id: deviceBId,
          tenantId,
          siteId,
          code: 'CH-01',
          displayName: 'CH-01 冷水机组',
          deviceType: 'CHILLER',
          status: 'ACTIVE',
          revision: 1,
          createdAt: now,
          updatedAt: now,
        }]
      : [];
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    spaces: [],
    assets: [],
    devices,
    sensors: [],
    telemetryPoints: [],
    relationships: [],
    counts: {
      spaces: 0,
      assets: 0,
      deviceEndpoints: devices.length,
      physicalSensors: 0,
      points: 0,
    },
  };
}

const ruleRevisions = [
  {
    ruleId: ruleAId,
    catalogVersion: 'core.v1',
    entryNodeId: 'evaluate-temperature',
    nodes: [
      { id: 'evaluate-temperature', definitionId: 'telemetry.threshold', config: { triggerThreshold: '> 9.5°C', clearPredicate: '< 8.7°C', durationMinutes: 5, clearDurationMinutes: 3 } },
      { id: 'notify-operators', definitionId: 'notification.dispatch', config: { recipients: ['值班工程师'], channels: ['站内通知'] } },
    ],
    edges: [{ fromNode: 'evaluate-temperature', fromPort: 'alarm', toNode: 'notify-operators', toPort: 'input' }],
    allowedPermissions: ['owner.snapshot.read', 'alarm.intent.publish'],
    maxNodes: 16,
    maxDepth: 8,
    maxFanout: 4,
    maxResourceCost: 128,
    maxAttempts: 3,
    id: ruleARevision3Id,
    tenantId,
    revision: 3,
    state: 'RELEASED',
    digest: 'd'.repeat(64),
  },
  {
    ruleId: ruleAId,
    catalogVersion: 'core.v1',
    entryNodeId: 'evaluate-temperature',
    nodes: [
      { id: 'evaluate-temperature', definitionId: 'telemetry.threshold', config: { triggerThreshold: '> 9.0°C', clearPredicate: '< 8.5°C', durationMinutes: 5, clearDurationMinutes: 3 } },
      { id: 'maintenance-suppression', definitionId: 'alarm.suppression-policy', config: { suppressionPolicy: '维护窗口抑制' } },
      { id: 'notify-operators', definitionId: 'notification.dispatch', config: { recipients: ['值班工程师', '暖通主管'], channels: ['站内通知', '短信'] } },
    ],
    edges: [
      { fromNode: 'evaluate-temperature', fromPort: 'alarm', toNode: 'maintenance-suppression', toPort: 'input' },
      { fromNode: 'maintenance-suppression', fromPort: 'alarm', toNode: 'notify-operators', toPort: 'input' },
    ],
    allowedPermissions: ['owner.snapshot.read', 'alarm.intent.publish'],
    maxNodes: 16,
    maxDepth: 8,
    maxFanout: 4,
    maxResourceCost: 128,
    maxAttempts: 3,
    id: ruleARevision4Id,
    tenantId,
    revision: 4,
    state: 'RELEASED',
    digest: 'e'.repeat(64),
  },
];

const ruleBindings = [{
  id: ruleABindingId,
  tenantId,
  siteId: siteAId,
  revision: 2,
  ruleRevisionId: ruleARevision4Id,
  priority: 100,
  active: true,
  createdAt: '2026-07-31T08:30:00Z',
}];

const ruleExecutionEvidence = [{
  executionId: 'exec-tokyo-temperature-001',
  siteId: siteAId,
  ruleRevisionId: ruleARevision4Id,
  bindingId: ruleABindingId,
  bindingRevision: 2,
  status: 'SUCCEEDED',
  terminalCode: 'ALARM_INTENT_PUBLISHED',
  trace: [{ nodeId: 'evaluate-temperature', result: 'triggered' }],
  effects: [{ kind: 'alarm.intent', alarmType: 'SUPPLY_TEMPERATURE_DRIFT' }],
  updatedAt: '2026-07-31T09:15:00Z',
}];

function problem(status, code, detail, retryable = false) {
  return {
    type: `https://api.quanlaihe.com/problems/${code.toLowerCase().replaceAll('_', '-')}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    code,
    retryable,
  };
}

function json(response, status, payload, headers = {}) {
  response.writeHead(status, {
    'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
    'cache-control': 'private, no-store',
    ...headers,
  });
  response.end(JSON.stringify(payload));
}

function clone(value) {
  return structuredClone(value);
}

async function requestJson(request) {
  let raw = '';
  for await (const chunk of request) raw += chunk;
  return raw ? JSON.parse(raw) : {};
}

function createGatewayFixture() {
  const requests = [];
  let requestSequence = 0;
  const server = createHTTPServer(async (request, response) => {
    try {
      const url = new URL(request.url ?? '/', 'http://fixture.local');
      const record = {
        method: request.method ?? 'GET',
        path: url.pathname,
        query: url.search,
        body: null,
        status: 0,
      };
      requests.push(record);

      if (request.method === 'GET' && url.pathname === '/api/v1/alarms') {
        const siteId = url.searchParams.get('siteId');
        const condition = url.searchParams.get('condition');
        const severity = url.searchParams.get('severity');
        const limit = Number(url.searchParams.get('limit') ?? '50');
        if (!siteId || !Number.isInteger(limit) || limit < 1 || limit > 200) {
          record.status = 400;
          json(response, 400, problem(400, 'INVALID_ARGUMENT', 'The Alarm list filter is invalid.'));
          return;
        }
        const items = (alarmsBySite.get(siteId) ?? [])
          .filter((entry) => !condition || entry.condition === condition)
          .filter((entry) => !severity || entry.currentSeverity === severity)
          .slice(0, limit)
          .map(clone);
        record.status = 200;
        requestSequence += 1;
        json(response, 200, {
          data: items,
          meta: {
            requestId: `alarm-audit-list-${requestSequence}`,
            limit,
            nextCursor: null,
            hasMore: false,
          },
        });
        return;
      }

      const assetModelMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/asset-model$/);
      if (assetModelMatch && request.method === 'GET') {
        const siteId = decodeURIComponent(assetModelMatch[1]);
        if (!alarmsBySite.has(siteId)) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The Site asset model is not visible.'));
          return;
        }
        record.status = 200;
        json(response, 200, alarmAssetModel(siteId));
        return;
      }

      const findingsMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/fdd\/findings$/);
      if (findingsMatch && request.method === 'GET') {
        const siteId = decodeURIComponent(findingsMatch[1]);
        if (!alarmsBySite.has(siteId)) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The Site diagnostics are not visible.'));
          return;
        }
        record.status = 200;
        json(response, 200, { items: (findingsBySite.get(siteId) ?? []).map(clone) });
        return;
      }

      if (request.method === 'GET' && url.pathname === '/api/v1/rules/revisions') {
        const ruleId = url.searchParams.get('ruleId');
        const items = ruleRevisions.filter((revision) => !ruleId || revision.ruleId === ruleId).map(clone);
        record.status = 200;
        json(response, 200, { items });
        return;
      }

      if (request.method === 'GET' && url.pathname === '/api/v1/rules/bindings') {
        const siteId = url.searchParams.get('siteId');
        const items = ruleBindings.filter((binding) => binding.siteId === siteId).map(clone);
        record.status = 200;
        json(response, 200, { items });
        return;
      }

      if (request.method === 'GET' && url.pathname === '/api/v1/rules/executions') {
        const siteId = url.searchParams.get('siteId');
        const limit = Number(url.searchParams.get('limit') ?? '50');
        const items = ruleExecutionEvidence.filter((item) => item.siteId === siteId).slice(0, limit).map(clone);
        record.status = 200;
        json(response, 200, { items });
        return;
      }

      const detailMatch = url.pathname.match(/^\/api\/v1\/alarms\/([^/]+)$/);
      if (detailMatch && request.method === 'GET') {
        const alarmId = decodeURIComponent(detailMatch[1]);
        const item = [...alarmsBySite.values()].flat().find((entry) => entry.alarmId === alarmId);
        if (!item) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The Alarm resource is not visible.'));
          return;
        }
        record.status = 200;
        requestSequence += 1;
        json(response, 200, { data: clone(item), meta: { requestId: `alarm-audit-detail-${requestSequence}` } });
        return;
      }

      const assignMatch = url.pathname.match(/^\/api\/v1\/alarms\/([^/]+)\/assign$/);
      if (assignMatch && request.method === 'POST') {
        const alarmId = decodeURIComponent(assignMatch[1]);
        const item = [...alarmsBySite.values()].flat().find((entry) => entry.alarmId === alarmId);
        if (!item) {
          record.status = 404;
          json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The Alarm resource is not visible.'));
          return;
        }
        const body = await requestJson(request);
        record.body = body;
        if (item.condition !== 'ACTIVE') {
          record.status = 422;
          json(response, 422, problem(422, 'ALARM_TRANSITION_INVALID', 'Recovered Alarm cannot be assigned.'));
          return;
        }
        if (body.expectedVersion !== item.version) {
          record.status = 409;
          json(response, 409, problem(409, 'ALARM_VERSION_CONFLICT', 'The Alarm changed before assignment.'));
          return;
        }
        const assigneeId = String(body.assigneeId ?? '').trim();
        const reason = String(body.reason ?? '').trim();
        if (!assigneeId || !reason || !request.headers['idempotency-key']) {
          record.status = 400;
          json(response, 400, problem(400, 'INVALID_ARGUMENT', 'The Alarm assignment request is invalid.'));
          return;
        }
        const occurredAt = new Date(Date.parse(item.updatedAt) + 60_000).toISOString();
        item.assigneeId = assigneeId;
        item.version += 1;
        item.updatedAt = occurredAt;
        item.timeline.push(timelineEntry({ operation: 'ASSIGN', condition: item.condition, currentSeverity: item.currentSeverity, reason, occurredAt, version: item.version, assigneeId }));
        record.status = 200;
        requestSequence += 1;
        json(response, 200, { data: clone(item), meta: { requestId: `alarm-audit-assign-${requestSequence}` } });
        return;
      }

      record.status = 404;
      json(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'Route not found.'));
    } catch (error) {
      json(response, 500, problem(500, 'FIXTURE_FAILURE', String(error), true));
    }
  });
  return { server, requests };
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
  const response = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text ?? 'Browser evaluation failed');
  return response.result.value;
}

async function waitForCondition(client, expression, label) {
  let last;
  for (let attempt = 0; attempt < 400; attempt += 1) {
    try {
      last = await evaluate(client, expression);
      if (last) return last;
    } catch {}
    await pause(100);
  }
  const diagnostic = await evaluate(client, `({ url: location.href, text: document.body?.innerText?.slice(0, 7000) ?? '', html: document.body?.innerHTML?.slice(0, 5000) ?? '' })`).catch((error) => ({ error: String(error) }));
  const runtimeErrors = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error'))
    .slice(-8)
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.exceptionDetails?.text ?? event.params?.entry?.text ?? event.method);
  throw new Error(`${label} did not become ready; last=${JSON.stringify(last)} diagnostic=${JSON.stringify(diagnostic)} runtimeErrors=${JSON.stringify(runtimeErrors)}`);
}

async function clickText(client, selector, text) {
  return evaluate(client, `(() => {
    const node = Array.from(document.querySelectorAll(${JSON.stringify(selector)})).find((candidate) => candidate.textContent?.includes(${JSON.stringify(text)}));
    if (!(node instanceof HTMLElement)) return false;
    node.focus({ preventScroll: true });
    if (typeof PointerEvent !== 'undefined') {
      node.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true, button: 0, buttons: 1, pointerId: 1, pointerType: 'mouse', isPrimary: true }));
    }
    node.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0, buttons: 1 }));
    if (typeof PointerEvent !== 'undefined') {
      node.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true, button: 0, buttons: 0, pointerId: 1, pointerType: 'mouse', isPrimary: true }));
    }
    node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true, button: 0, buttons: 0 }));
    node.click();
    return true;
  })()`);
}

async function captureScreenshot(client, filename) {
  const result = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(join(outputRoot, filename), Buffer.from(result.data, 'base64'));
}

async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

const browserPath = resolveLinuxBrowserExecutable();
const browserProfileDir = profileDir;

const gatewayPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const fixture = createGatewayFixture();
let viteServer;
let browserProcess;
let cdpClient;
let conclusion = 'failed';
const assertions = [];
const stateEvidence = {};

try {
  await mkdir(profileDir, { recursive: true });
  await mkdir(outputRoot, { recursive: true });
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  delete process.env.VITE_S4_LOCAL_ALARMS;
  viteServer = await createViteServer({
    root: fixtureRoot,
    cacheDir: resolve(root, 'node_modules/.vite-alarms'),
    publicDir: resolve(root, 'apps/hvac-web/public'),
    configFile: false,
    logLevel: 'error',
    define: {},
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': resolve(root, 'apps/hvac-web/src') } },
    server: {
      host: '0.0.0.0',
      port: Number(process.env.ALARMS_REVIEW_PORT ?? 0),
      strictPort: Boolean(process.env.ALARMS_REVIEW_PORT),
      proxy: { '/api': { target: gatewayURL, changeOrigin: true } },
    },
  });
  await viteServer.listen();
  const viteAddress = viteServer.httpServer?.address();
  assert(viteAddress && typeof viteAddress === 'object', 'Vite fixture server has no address');
  const webURL = `http://127.0.0.1:${viteAddress.port}`;

  if (process.argv.includes('--serve')) {
    console.log(JSON.stringify({ mode: 'serve', url: webURL, gateway: gatewayURL }));
    await new Promise(() => {});
  }

  browserProcess = spawn(browserPath, [
    '--headless=new', '--disable-gpu', '--disable-extensions', '--disable-sync', '--disable-background-networking',
    '--no-sandbox', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--remote-debugging-address=0.0.0.0',
    '--window-size=1440,1000', `--remote-debugging-port=${debugPort ?? 0}`, `--user-data-dir=${browserProfileDir}`, 'about:blank',
  ], { stdio: 'ignore' });

  for (let attempt = 0; attempt < 300; attempt += 1) {
    try { if ((await fetch(`http://127.0.0.1:${debugPort}/json/version`)).ok) break; } catch {}
    if (attempt === 299) throw new Error('Browser debugger did not become ready');
    await pause(100);
  }

  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Log.enable');
  await cdpClient.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });
  await cdpClient.send('Page.navigate', { url: webURL });

  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state') === 'READY' && document.body.innerText.includes('Tokyo supply temperature drift') && document.body.innerText.includes('已恢复 1')`,
    'authoritative Site A Alarm list',
  );

  const initial = await evaluate(cdpClient, `({
    state: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state'),
    siteId: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-site-id'),
    activeRows: document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-body"] [data-slot="table-row"]').length,
    text: document.body.innerText,
    summaryText: document.querySelector('[data-testid="alarm-summary-facts"]')?.textContent ?? '',
    headers: Array.from(document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="table-head"]')).map((node) => node.textContent?.trim() ?? ''),
    legacyAntCount: document.querySelectorAll('.ant-table, .ant-drawer, .ant-modal, .ant-tabs').length,
  })`);
  assert(initial.state === 'READY' && initial.siteId === siteAId, 'Site A Alarm scope was not ready');
  assert(initial.activeRows === 2, 'Current activity view did not contain exactly the two ACTIVE Site A Alarm rows');
  assert(!initial.text.includes('冷冻机房') && !initial.text.includes('冷冻水供水温度过高'), 'Real Alarm UI displayed demo Alarm content');
  for (const fact of ['当前活动', '未确认', '已确认', '处理中', '今日新增', '今日恢复']) {
    assert(initial.summaryText.includes(fact), `Alarm summary lost operational fact: ${fact}`);
  }
  assert(!initial.summaryText.includes('人工关闭'), 'Alarm summary reintroduced unsupported manual Close as a first-class fact');
  for (const column of ['等级', '告警', '设备 / 位置', '处理', '重复', '最近发生', '持续时长', '负责人']) {
    assert(initial.headers.some((header) => header.includes(column)), `Alarm triage ledger lost scan field: ${column}`);
  }
  assert(initial.legacyAntCount === 0, 'Migrated Alarm workbench rendered legacy Ant DOM');
  assert(!initial.text.includes('当前抑制'), 'Alarm summary reintroduced suppression as a top-level KPI instead of keeping it as an orthogonal filter/state');
  assertions.push('site-a-shadcn-triage-ledger-and-compact-facts-use-current-condition');
  await captureScreenshot(cdpClient, '01-active-workbench.png');

  assert(await clickText(cdpClient, '[data-testid="alarm-triage-ledger"] button', '批量处理'), 'Batch processing mode was unavailable');
  await waitForCondition(cdpClient, `document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="checkbox"][aria-label^="选择 "]').length === 2`, 'batch row selection controls');
  assert(await evaluate(cdpClient, `(() => { const boxes = document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="checkbox"][aria-label^="选择 "]'); if (!(boxes[0] instanceof HTMLElement)) return false; boxes[0].click(); return true; })()`), 'First Alarm checkbox was unavailable');
  await waitForCondition(cdpClient, `(() => { const button = Array.from(document.querySelectorAll('[data-testid="alarm-batch-actions"] button')).find((node) => node.textContent?.includes('批量确认')); return button instanceof HTMLButtonElement && !button.disabled && document.querySelector('.alarm-center__batch-summary')?.textContent?.includes('批量确认：可用 1 条'); })()`, 'single eligible Alarm bulk acknowledgement');
  assert(await evaluate(cdpClient, `(() => { const boxes = document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="checkbox"][aria-label^="选择 "]'); if (!(boxes[1] instanceof HTMLElement)) return false; boxes[1].click(); return true; })()`), 'Second Alarm checkbox was unavailable');
  await waitForCondition(cdpClient, `(() => { const button = Array.from(document.querySelectorAll('[data-testid="alarm-batch-actions"] button')).find((node) => node.textContent?.includes('批量确认')); const summary = document.querySelector('.alarm-center__batch-summary')?.textContent ?? ''; return button instanceof HTMLButtonElement && button.disabled && summary.includes('批量确认：可用 1 条') && summary.includes('受限 1 条'); })()`, 'mixed-state bulk acknowledgement disablement');
  const batchActions = await evaluate(cdpClient, `Array.from(document.querySelectorAll('[data-testid="alarm-batch-actions"] button')).map((node) => ({ text: node.textContent ?? '', disabled: node instanceof HTMLButtonElement ? node.disabled : node.getAttribute('aria-disabled') === 'true' }))`);
  assert(batchActions.some((action) => action.text.includes('批量确认') && action.disabled), 'Mixed selection did not disable the bulk acknowledgement action');
  assert(batchActions.some((action) => action.text.includes('批量指派') && action.disabled), 'Bulk workbench exposed unsupported assignment');
  assert(batchActions.some((action) => action.text.includes('导出所选') && !action.disabled), 'Bulk workbench lost the export-selected action');
  assert((await evaluate(cdpClient, `document.querySelector('.alarm-center__batch-summary')?.textContent ?? ''`)).includes('批量指派尚未接入'), 'Bulk summary did not explain the unavailable assignment capability');
  assertions.push('batch-workbench-exposes-selection-capability-intersection-and-explains-unavailable-actions');
  await captureScreenshot(cdpClient, '01b-batch-workbench.png');
  assert(await clickText(cdpClient, '[data-testid="alarm-triage-ledger"] button', '退出批量'), 'Batch processing mode did not exit');
  await waitForCondition(cdpClient, `document.querySelectorAll('[data-testid="alarm-triage-ledger"] [data-slot="checkbox"][aria-label^="选择 "]').length === 0`, 'batch selection teardown');

  assert(await clickText(cdpClient, '[data-testid="alarm-triage-ledger"] button', 'Tokyo supply temperature drift'), 'Site A Alarm detail control was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-alarm-id') === '${alarmAActiveId}' && Boolean(document.querySelector('[data-testid="alarm-context-inspector"]')) && Boolean(document.querySelector('[data-testid="real-alarm-detail"]')) && Boolean(document.querySelector('[data-testid="alarm-diagnosis-panel"]')) && Boolean(document.querySelector('[data-testid="alarm-lifecycle-rail"]')) && document.body.innerText.includes('处理时间线')`,
    'authoritative Alarm context inspector',
  );
  const detailState = await evaluate(cdpClient, `({
    hasDetail: Boolean(document.querySelector('[data-testid="real-alarm-detail"]')),
    actions: Array.from(document.querySelectorAll('[data-testid="alarm-context-inspector"] button, [data-testid="alarm-context-inspector"] a')).map((node) => ({
      text: node.textContent ?? '',
      href: node instanceof HTMLAnchorElement ? node.getAttribute('href') : null,
      disabled: node instanceof HTMLButtonElement ? node.disabled : node.getAttribute('aria-disabled') === 'true',
    })),
    hasCloseLifecycle: Array.from(document.querySelectorAll('[data-testid="alarm-context-inspector"] button')).some((node) => node.textContent?.includes('关闭告警')),
    lifecycleStepCount: document.querySelectorAll('[data-testid="alarm-lifecycle-rail"] ol > li').length,
    hasDiagnosisBoundary: document.querySelector('[data-testid="alarm-diagnosis-panel"]')?.textContent?.includes('待 AI 运维助手调查结论') ?? false,
    handlingFacts: Array.from(document.querySelectorAll('[data-testid="alarm-handling-facts"] > div')).map((node) => node.textContent ?? ''),
    text: document.querySelector('[data-testid="alarm-context-inspector"]')?.textContent ?? '',
    legacyAntCount: document.querySelectorAll('.ant-drawer, .ant-modal, .ant-steps').length,
  })`);
  assert(detailState.hasDetail, 'Alarm context inspector did not render');
  assert(detailState.actions.some((action) => action.text.includes('确认告警') && !action.disabled), 'Unacknowledged ACTIVE Alarm did not expose acknowledge');
  assert(detailState.actions.some((action) => action.text.includes('指派') && !action.disabled), 'ACTIVE Alarm with alarm.assign capability did not expose assignment');
  assert(detailState.actions.some((action) => action.text.includes('转工单')), 'Alarm inspector did not expose the Alarm-to-Work-Order action');
  assert(detailState.actions.some((action) => action.text.includes('设备详情') && action.href === `/sites/${siteAId}/assets/${deviceAId}`), 'Device Alarm inspector did not route directly to the new durable Device detail surface');
  assert(detailState.actions.some((action) => action.text.includes('运行监控定位')), 'Alarm inspector did not expose the investigation handoff to HVAC monitor');
  assert(!detailState.hasCloseLifecycle, 'Alarm inspector exposed a fabricated manual close action without a Close contract');
  assert(detailState.lifecycleStepCount === 5, 'Alarm inspector did not render the five-stage physical-recovery lifecycle view');
  assert(detailState.hasDiagnosisBoundary, 'Alarm diagnosis panel did not preserve the Operations AI authority boundary');
  assert(detailState.handlingFacts.length === 4, `Alarm handling facts are incomplete: ${JSON.stringify(detailState.handlingFacts)}`);
  for (const factLabel of ['确认状态', '指派状态', '处理状态', '恢复方式']) {
    assert(detailState.handlingFacts.some((fact) => fact.includes(factLabel)), `Alarm handling facts lost: ${factLabel}`);
  }
  for (const moduleTitle of ['告警趋势与关键参数', '触发与恢复', '智能诊断', '处置生命周期', '确认 / 指派 / 处理', '处理时间线']) {
    assert(detailState.text.includes(moduleTitle), `Alarm inspector lost responsibility: ${moduleTitle}`);
  }
  assert(detailState.text.includes('恢复由实时规则自动判定'), 'Alarm inspector lost physical recovery semantics');
  assert(detailState.legacyAntCount === 0, 'Alarm inspector fell back to legacy Ant Drawer/Modal/Steps');
  for (const internalText of [alarmAActiveId, 'telemetry-snapshot', '关联事件', '规则版本', '证据条数', 'ACTIVE', 'CLEARED']) {
    assert(!detailState.text.includes(internalText), `Alarm inspector leaked internal tracking text into the operator surface: ${internalText}`);
  }
  assertions.push('context-inspector-preserves-investigation-actions-truth-and-owner-boundaries');
  await captureScreenshot(cdpClient, '02-context-inspector.png');

  assert(await clickText(cdpClient, '[data-testid="alarm-context-inspector"] button', '指派'), 'Alarm assignment action was unavailable');
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="alarm-assign-dialog"]'))`, 'assignment dialog');
  assert(await evaluate(cdpClient, `(() => {
    const dialog = document.querySelector('[data-testid="alarm-assign-dialog"]');
    const input = dialog?.querySelector('#alarm-center-assignee');
    const textarea = dialog?.querySelector('#alarm-center-assign-reason');
    if (!(input instanceof HTMLInputElement) || !(textarea instanceof HTMLTextAreaElement)) return false;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, 'principal:tokyo-maintainer');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(textarea, '交由冷站值班工程师处理');
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`), 'Assignment form could not be populated');
  await waitForCondition(cdpClient, `(() => { const dialog = document.querySelector('[data-testid="alarm-assign-dialog"]'); const button = Array.from(dialog?.querySelectorAll('button') ?? []).find((node) => node.textContent?.includes('确认指派')); return button instanceof HTMLButtonElement && !button.disabled; })()`, 'assignment confirmation enablement');
  assert(await evaluate(cdpClient, `(() => { const dialog = document.querySelector('[data-testid="alarm-assign-dialog"]'); const button = Array.from(dialog?.querySelectorAll('button') ?? []).find((node) => node.textContent?.includes('确认指派')); if (!(button instanceof HTMLButtonElement)) return false; button.click(); return true; })()`), 'Assignment confirmation control was unavailable');
  await waitForCondition(cdpClient, `(() => { const facts = Array.from(document.querySelectorAll('[data-testid="alarm-handling-facts"] > div')).map((node) => node.textContent ?? ''); const inspectorText = document.querySelector('[data-testid="alarm-context-inspector"]')?.textContent ?? ''; return facts.some((value) => value.includes('指派状态') && value.includes('已指派') && value.includes('已建立处理责任人')) && facts.some((value) => value.includes('处理状态') && value.includes('处理中')) && !inspectorText.includes('principal:tokyo-maintainer'); })()`, 'authoritative assignment projection');
  assertions.push('public-alarm-assignment-updates-authoritative-processing-state');
  await captureScreenshot(cdpClient, '02b-assigned-processing.png');

  assert(await evaluate(cdpClient, `(() => { const button = document.querySelector('[data-testid="alarm-context-inspector"] button[aria-label="关闭告警详情"]'); if (!(button instanceof HTMLButtonElement)) return false; button.click(); return true; })()`), 'Context inspector close control was unavailable');
  await waitForCondition(cdpClient, `!document.querySelector('[data-testid="alarm-context-inspector"]')`, 'Alarm context inspector close');

  assert(await clickText(cdpClient, '[data-slot="tabs-trigger"]', '已恢复'), 'Recovered tab was unavailable');
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-business-state') === 'READY' && document.body.innerText.includes('Tokyo plant differential pressure') && !document.body.innerText.includes('Tokyo supply temperature drift')`,
    'recovered Alarm ledger',
  );
  assertions.push('recovered-tab-is-cleared-only');

  assert(await clickText(cdpClient, '[data-slot="tabs-trigger"]', '关联分析'), 'Correlation analysis tab was unavailable');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('.alarm-center__analysis-workbench')) && Boolean(document.querySelector('[data-testid="alarm-correlation-scope"]')) && document.body.innerText.includes('尚不能确认设备之间的影响路径')`,
    'correlation analysis workbench',
  );
  const analysisState = await evaluate(cdpClient, `({
    scopeCount: document.querySelectorAll('[data-testid="alarm-correlation-scope"] > span').length,
    scopeText: document.querySelector('[data-testid="alarm-correlation-scope"]')?.textContent ?? '',
    detailText: document.querySelector('.alarm-center__analysis-detail')?.textContent ?? '',
    links: Array.from(document.querySelectorAll('.alarm-center__analysis-detail a')).map((node) => node.getAttribute('href')),
  })`);
  assert(analysisState.scopeCount === 2, `Correlation analysis did not keep the two real device members: ${JSON.stringify(analysisState)}`);
  assert(!analysisState.scopeText.includes('→'), 'Correlation analysis fabricated a directional device path without topology evidence');
  assert(analysisState.detailText.includes('不代表已经确认因果关系') && analysisState.detailText.includes('尚不能确认设备之间的影响路径'), 'Correlation analysis did not preserve the correlation-versus-causation boundary');
  for (const responsibility of ['成员告警', '仍在活动', '重复次数', '关联范围', '建议处理步骤']) {
    assert(analysisState.detailText.includes(responsibility), `Correlation analysis lost responsibility: ${responsibility}`);
  }
  assert(analysisState.links.some((href) => href?.includes('/ai')), 'Correlation analysis lost the AI investigation handoff');
  assertions.push('correlation-analysis-preserves-evidence-scope-without-fake-topology-or-causality');
  await captureScreenshot(cdpClient, '03-analysis-workbench.png');

  assert(await clickText(cdpClient, '[data-slot="tabs-trigger"]', '历史告警'), 'History tab was unavailable');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('.alarm-center__history-workbench')) && document.body.innerText.includes('历史页展示的是物理恢复事实') && document.body.innerText.includes('Tokyo plant differential pressure')`,
    'history workbench',
  );
  const historyState = await evaluate(cdpClient, `({
    workbench: Boolean(document.querySelector('.alarm-center__history-workbench')),
    text: document.querySelector('.alarm-center__history-workbench')?.textContent ?? '',
    headers: Array.from(document.querySelectorAll('.alarm-center__history-ledger [data-slot="table-head"]')).map((node) => node.textContent?.trim() ?? ''),
    titles: Array.from(document.querySelectorAll('.alarm-center__history-workbench [data-slot="card-title"], .alarm-center__history-workbench h2, .alarm-center__history-workbench h3')).map((node) => node.textContent?.trim() ?? ''),
    legacyAntCount: document.querySelectorAll('.alarm-center__history-workbench .ant-table').length,
  })`);
  assert(historyState.workbench && historyState.text.includes('历史页展示的是物理恢复事实') && historyState.text.includes('恢复来自规则对实时条件的判定'), 'History workbench did not preserve the physical recovery boundary');
  for (const responsibility of ['已恢复记录', '今日恢复', '平均持续时长', '最近恢复趋势', '恢复原因', '历史告警台账']) {
    assert(historyState.text.includes(responsibility), `History workbench lost responsibility: ${responsibility}`);
  }
  for (const columnTitle of ['等级', '告警', '设备 / 位置', '恢复时间', '持续时长', '重复', '工单']) {
    assert(historyState.headers.some((header) => header.includes(columnTitle)), `History ledger lost scan field: ${columnTitle}`);
  }
  assert(historyState.legacyAntCount === 0, 'History workbench fell back to Ant Table');
  assert(!historyState.text.includes('人工关闭') && !historyState.titles.includes('关闭趋势') && !historyState.titles.includes('关闭原因分析'), 'History workbench reintroduced unsupported Close semantics');
  assertions.push('history-workbench-preserves-physical-recovery-analysis-and-ledger-without-fake-close-semantics');
  await captureScreenshot(cdpClient, '03-history-workbench.png');

  assert(await clickText(cdpClient, '[data-slot="tabs-trigger"]', '规则与通知'), 'Rules tab was unavailable');
  await waitForCondition(
    cdpClient,
    `Boolean(document.querySelector('[data-testid="alarm-rules-workbench"]')) && document.body.innerText.includes('> 9.0°C') && document.body.innerText.includes('< 8.5°C') && document.body.innerText.includes('维护窗口抑制') && document.body.innerText.includes('值班工程师、暖通主管') && document.body.innerText.includes('站内通知、短信') && document.body.innerText.includes('执行成功') && document.body.innerText.includes('规则判断已完成')`,
    'rules and notification workbench',
  );
  const rulesState = await evaluate(cdpClient, `({
    text: document.querySelector('[data-testid="alarm-rules-workbench"]')?.textContent ?? '',
    triggerText: document.querySelector('[data-rule-group="trigger"]')?.textContent ?? '',
    recoveryText: document.querySelector('[data-rule-group="recovery"]')?.textContent ?? '',
    groupLabels: Array.from(document.querySelectorAll('[data-rule-group] > span:first-child')).map((node) => node.textContent ?? ''),
    links: Array.from(document.querySelectorAll('[data-testid="alarm-rules-workbench"] a')).map((node) => node.getAttribute('href')),
    tableCount: document.querySelectorAll('[data-testid="alarm-rules-workbench"] [data-slot="table"]').length,
    legacyAntCount: document.querySelectorAll('[data-testid="alarm-rules-workbench"] .ant-table').length,
  })`);
  assert(JSON.stringify(rulesState.groupLabels) === JSON.stringify(['触发条件', '恢复条件', '持续时间', '抑制策略', '通知对象', '通知渠道']), `Rules workbench configuration groups drifted: ${JSON.stringify(rulesState.groupLabels)}`);
  assert(rulesState.text.includes('触发、恢复与通知配置') && rulesState.text.includes('通知配置以规则版本为准'), 'Rules workbench did not preserve the separation between released rule facts and notification delivery');
  assert(rulesState.triggerText.includes('> 9.0°C') && !rulesState.triggerText.includes('< 8.5°C'), `Recovery config leaked into the trigger-condition group: ${JSON.stringify(rulesState)}`);
  assert(rulesState.recoveryText.includes('< 8.5°C') && !rulesState.recoveryText.includes('> 9.0°C') && !rulesState.recoveryText.includes('恢复 3 分钟'), 'Trigger/duration config leaked into the recovery-condition group');
  assert(rulesState.text.includes('持续时间') && rulesState.text.includes('触发 5 分钟') && rulesState.text.includes('恢复 3 分钟'), 'Rule duration facts were not projected from the released revision');
  assert(rulesState.text.includes('r4') && rulesState.text.includes('r3') && rulesState.tableCount >= 2, 'Rule version history or execution evidence tables were incomplete');
  assert(rulesState.legacyAntCount === 0, 'Rules workbench fell back to Ant Table');
  assert(rulesState.links.filter((href) => href === '/system?tab=rules').length >= 2, 'Rule test/manage entry points did not route to the rule management surface');
  assert(!rulesState.text.includes('Rule owner bindings') && !rulesState.text.includes('typed node') && !rulesState.text.includes('terminal code') && !rulesState.text.includes('SUCCEEDED') && !rulesState.text.includes('triggerThreshold') && !rulesState.text.includes('clearPredicate') && !rulesState.text.includes('ALARM_INTENT_PUBLISHED') && !rulesState.text.includes('绑定 ID') && !rulesState.text.includes('规则版本 ID') && !rulesState.text.includes('版本摘要') && !rulesState.text.includes('规则结构') && !rulesState.text.includes('结果代码'), 'Rules workbench exposed implementation-oriented tracking fields');
  assertions.push('rules-workbench-uses-real-revisions-bindings-evidence-and-shadcn-tables');
  await captureScreenshot(cdpClient, '04-rules-workbench.png');

  await cdpClient.send('Page.navigate', { url: `${webURL}/?source=overview&severity=CRITICAL&alarm=${alarmAActiveId}` });
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-alarm-id') === '${alarmAActiveId}' && document.body.innerText.includes('来自：运营总览 / 优先处理') && Boolean(document.querySelector('[data-testid="real-alarm-detail"]'))`,
    'Overview-to-Alarm-Center deep link',
  );
  const dashboardDeepLink = await evaluate(cdpClient, `({
    url: location.href,
    contextText: document.querySelector('.alarm-center__context-banner')?.textContent ?? '',
    selectedAlarmId: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-alarm-id') ?? '',
  })`);
  assert(dashboardDeepLink.url.includes('severity=CRITICAL') && dashboardDeepLink.selectedAlarmId === alarmAActiveId, 'Overview deep link lost severity or Alarm identity');
  assertions.push('overview-deep-link-preserves-source-severity-and-opens-detail');
  await captureScreenshot(cdpClient, '02a-overview-deeplink.png');

  const deviceContext = '设备 000001';
  await cdpClient.send('Page.navigate', { url: `${webURL}/?source=device&device=${encodeURIComponent(deviceContext)}&deviceId=${encodeURIComponent(deviceAId)}&alarm=${alarmAActiveId}` });
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-alarm-id') === '${alarmAActiveId}' && document.body.innerText.includes('来自：设备中心') && document.body.innerText.includes('${deviceContext}') && Boolean(document.querySelector('[data-testid="alarm-context-inspector"]'))`,
    'Device-to-Alarm-Center deep link',
  );
  const deviceDeepLink = await evaluate(cdpClient, `({
    contextText: document.querySelector('.alarm-center__context-banner')?.textContent ?? '',
    links: Array.from(document.querySelectorAll('.alarm-center__context-actions a')).map((node) => node.getAttribute('href')),
    selectedAlarmId: document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-alarm-id') ?? '',
  })`);
  assert(deviceDeepLink.contextText.includes('来自：设备中心') && deviceDeepLink.contextText.includes(deviceContext), 'Device deep link did not preserve human-readable Device Center context');
  assert(!deviceDeepLink.contextText.includes(deviceAId), 'Device deep link leaked the internal device ID into operator copy');
  assert(deviceDeepLink.selectedAlarmId === alarmAActiveId, 'Device deep link did not open the requested Alarm context');
  assert(deviceDeepLink.links.some((href) => href?.includes('/assets') && href.includes('q=')), 'Device deep link did not provide a return path to Device Center');
  assert(deviceDeepLink.links.some((href) => href === `/sites/${siteAId}/assets/${deviceAId}`), 'Device deep link did not provide the new durable Device detail action');
  assertions.push('device-deep-link-preserves-human-context-and-durable-device-route-without-id-leakage');
  await captureScreenshot(cdpClient, '02b-device-deeplink.png');

  await cdpClient.send('Page.navigate', { url: webURL });
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-site-id') === '${siteAId}' && document.body.innerText.includes('Tokyo supply temperature drift')`, 'Site A reset before Site switch');

  await evaluate(cdpClient, `globalThis.__REAL_ALARMS_AUDIT__.switchSite()`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-workbench"]')?.getAttribute('data-site-id') === '${siteBId}' && document.body.innerText.includes('Osaka condenser approach')`,
    'Site B Alarm list',
  );
  const afterSwitch = await evaluate(cdpClient, `({
    siteId: globalThis.__REAL_ALARMS_AUDIT__.siteId(),
    cacheKeys: globalThis.__REAL_ALARMS_AUDIT__.cacheKeys(),
    text: document.body.innerText,
    draftDirty: globalThis.__REAL_ALARMS_AUDIT__.draftDirty(),
  })`);
  assert(afterSwitch.siteId === siteBId, 'Alarm fixture did not switch to Site B');
  assert(!JSON.stringify(afterSwitch.cacheKeys).includes(siteAId), 'old Site Alarm cache survived Site transition');
  assert(!afterSwitch.text.includes('Tokyo supply temperature drift'), 'old Site Alarm content survived Site transition');
  assert(afterSwitch.text.includes('已确认') || afterSwitch.text.includes('处理中'), 'Acknowledgement/assignment projection was not visible for the Site B Alarm');
  assert(afterSwitch.draftDirty === false, 'production Alarm read created a protected lifecycle draft');
  assertions.push('cross-site-cache-and-view-purge');

  await evaluate(cdpClient, `globalThis.__REAL_ALARMS_AUDIT__.denyAlarm()`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-alarms-disabled"]')?.getAttribute('data-business-state') === 'DISABLED' && globalThis.__REAL_ALARMS_AUDIT__.cacheCount() === 0`,
    'Alarm capability denial',
  );
  const denied = await evaluate(cdpClient, `({
    text: document.body.innerText,
    cacheCount: globalThis.__REAL_ALARMS_AUDIT__.cacheCount(),
    disabled: Boolean(document.querySelector('[data-testid="real-alarms-disabled"]')),
  })`);
  assert(denied.disabled && denied.cacheCount === 0, 'Alarm capability denial did not purge protected cache');
  assert(denied.text.includes('当前会话没有告警列表能力'), 'Alarm capability denial did not render the IAM boundary');
  assert(!denied.text.includes('Osaka condenser approach') && !denied.text.includes(alarmBId), 'Alarm capability denial retained protected Alarm data');
  assertions.push('capability-denial-generic-boundary-and-cache-purge');

  const alarmRequests = fixture.requests.filter((entry) => entry.path.startsWith('/api/v1/alarms'));
  assert(alarmRequests.length >= 7, 'Alarm browser audit did not exercise current list, summary, recovered, history, detail and assignment');
  assert(alarmRequests.every((entry) => !entry.path.includes('/local/')), 'Production Alarm browser audit used the local seam');
  assert(alarmRequests.some((entry) => entry.path === `/api/v1/alarms/${alarmAActiveId}`), 'Alarm browser audit did not read the public Alarm detail');
  const assignmentRequest = alarmRequests.find((entry) => entry.method === 'POST' && entry.path === `/api/v1/alarms/${alarmAActiveId}/assign`);
  assert(assignmentRequest?.status === 200, 'Alarm browser audit did not complete the canonical public assignment write');
  assert(assignmentRequest?.body?.assigneeId === 'principal:tokyo-maintainer' && assignmentRequest?.body?.reason === '交由冷站值班工程师处理', 'Alarm assignment request lost assignee or reason');
  assert(alarmRequests.some((entry) => entry.path === '/api/v1/alarms' && entry.query.includes(`siteId=${siteAId}`) && entry.query.includes('condition=ACTIVE')), 'Site A current-activity request did not use the physical ACTIVE condition');
  assert(alarmRequests.some((entry) => entry.path === '/api/v1/alarms' && entry.query.includes(`siteId=${siteAId}`) && entry.query.includes('condition=CLEARED')), 'Recovered/history request did not use the physical CLEARED condition');
  assert(alarmRequests.some((entry) => entry.path === '/api/v1/alarms' && entry.query.includes(`siteId=${siteBId}`)), 'Alarm browser audit did not read Site B through the public Gateway seam');
  assert(fixture.requests.every((entry) => !entry.path.includes('/telemetry/')), 'Alarm browser audit inferred Alarm truth from Telemetry');
  assertions.push('public-gateway-active-cleared-and-assignment-no-local-or-telemetry-inference');

  const ruleRequests = fixture.requests.filter((entry) => entry.path.startsWith('/api/v1/rules/'));
  assert(ruleRequests.some((entry) => entry.path === '/api/v1/rules/revisions'), 'Rules workbench did not read released Rule revisions');
  assert(ruleRequests.some((entry) => entry.path === '/api/v1/rules/bindings' && entry.query.includes(`siteId=${siteAId}`)), 'Rules workbench did not read the Site binding');
  assert(ruleRequests.some((entry) => entry.path === '/api/v1/rules/executions' && entry.query.includes(`siteId=${siteAId}`)), 'Rules workbench did not read execution evidence');
  assert(ruleRequests.every((entry) => entry.method === 'GET'), 'Rules workbench issued a write during read-only Alarm Center inspection');
  assertions.push('rules-workbench-public-gateway-read-only-contract');

  const browserErrors = cdpClient.events
    .filter((event) => event.method === 'Runtime.exceptionThrown'
      || (event.method === 'Log.entryAdded' && event.params?.entry?.level === 'error' && event.params?.entry?.source === 'javascript'))
    .map((event) => event.params?.exceptionDetails?.exception?.description ?? event.params?.entry?.text ?? event.method);
  assert(browserErrors.length === 0, `Browser emitted errors: ${browserErrors.join(' | ')}`);
  assertions.push('browser-console-and-runtime-clean');

  stateEvidence.publicWorkflow = {
    sitesRead: [siteAId, siteBId],
    detailRead: alarmAActiveId,
    publicAssignmentWrite: true,
    localLifecycleWrites: 0,
    capabilityDenied: true,
    screenshots: ['01-active-workbench.png', '01b-batch-workbench.png', '02-context-inspector.png', '02b-assigned-processing.png', '02a-overview-deeplink.png', '02b-device-deeplink.png', '03-analysis-workbench.png', '03-history-workbench.png', '04-rules-workbench.png'],
  };

  conclusion = 'passed';
  const evidence = {
    schemaVersion: 5,
    passed: true,
    generatedAt: new Date().toISOString(),
    assertions,
    stateEvidence,
    network: { requests: fixture.requests },
    safety: {
      publicGatewayReads: true,
      activeClearedConditionModel: true,
      publicAssignmentWrite: true,
      localLifecycleSeam: false,
      telemetryInference: false,
      demoContamination: false,
    },
  };
  await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify(evidence, null, 2));
  console.log(`Real Alarm public workflow browser audit passed. Evidence: ${join(outputRoot, 'browser-evidence.json')}`);
} finally {
  cdpClient?.close();
  await stopBrowser(browserProcess);
  if (viteServer) await viteServer.close();
  await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  try {
    await rm(profileDir, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 });
  } catch (error) {
    console.warn(`Real Alarm browser profile cleanup was deferred: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (conclusion !== 'passed') {
    await mkdir(outputRoot, { recursive: true });
    await writeFile(join(outputRoot, 'browser-evidence.json'), JSON.stringify({ schemaVersion: 5, passed: false, generatedAt: new Date().toISOString(), assertions, stateEvidence, network: { requests: fixture.requests } }, null, 2));
  }
}
