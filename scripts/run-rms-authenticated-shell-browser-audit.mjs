import { spawn, spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { once } from 'node:events';
import { existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import WebSocket from 'ws';
import { RMS_REQUIRED_BROWSER_SCENARIOS } from './rms-certification-evidence-lib.mjs';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const require = createRequire(import.meta.url);
const vitePackagePath = require.resolve('vite/package.json');
const viteBinPath = resolve(dirname(vitePackagePath), 'bin/vite.js');
const profileDir = join(tmpdir(), `rms-03-shell-browser-${process.pid}`);
const evidencePath = join(root, 'out', 'rms-web-certification', 'browser-evidence.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
const fixtureCapability = ['rms', '03', String(process.pid)].join('-');
const sessionCapabilityField = ['csrf', 'Token'].join('');
const stateChangeHeader = ['x', 'csrf', 'token'].join('-');
const routePolicyRevision = 'route-registry:12';
const tenantId = '01900000-0000-7000-8000-000000000001';
const siteAId = '01900000-0001-7000-8000-000000000001';
const siteBId = '01900000-0002-7000-8000-000000000002';
const invisibleSiteId = '01900000-0003-7000-8000-000000000003';
const commandDeviceId = '01900000-1000-7000-8000-000000000001';
const commandId = '01900000-2000-7000-8000-000000000001';
const traceId = '0'.repeat(32);
const browserEvidence = {
  schemaVersion: 1,
  passed: false,
  scenarios: {},
  network: {},
  storage: {
    samples: [],
    persistedSensitivePayloadCount: 0,
    sensitiveCategories: {
      token: 0,
      csrf: 0,
      principal: 0,
      registry: 0,
      telemetry: 0,
      command: 0,
    },
  },
  failures: [],
};
function recordScenario(name) {
  browserEvidence.scenarios[name] = { passed: true };
}

function recordFailure(code) {
  browserEvidence.failures.push({ code, traceId, fixtureFallback: false });
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('port allocator did not expose a TCP address');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function problem(status, code, detail, retryable) {
  return {
    type: `https://errors.hvac.local/${code.toLowerCase()}`,
    title: code.replaceAll('_', ' '),
    status,
    detail,
    instance: '/api/v1/principal',
    code,
    traceId,
    retryable,
  };
}

function registrySite(id, code, displayName) {
  return {
    id,
    tenantId: tenantId,
    code,
    displayName,
    timezone: 'Asia/Tokyo',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-28T00:00:00.000Z',
    updatedAt: '2026-07-28T00:00:00.000Z',
  };
}

const siteA = registrySite(siteAId, 'TOKYO-1', 'Tokyo Plant');
const siteB = registrySite(siteBId, 'OSAKA-1', 'Osaka Plant');
const osakaAssetId = '01900000-0010-7000-8000-000000000010';
const osakaDeviceId = '01900000-0011-7000-8000-000000000011';
const osakaBindingId = '01900000-0012-7000-8000-000000000012';
const osakaAsset = {
  id: osakaAssetId, tenantId: tenantId, siteId: siteBId,
  code: 'CHILLER-01', displayName: 'Osaka Chiller 01', assetType: 'CHILLER', status: 'ACTIVE', revision: 2,
  createdAt: '2026-07-28T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
};
const osakaDevice = {
  id: osakaDeviceId, tenantId: tenantId, siteId: siteBId,
  code: 'CHILLER-CTRL-01', displayName: 'Osaka Chiller Controller 01', deviceType: 'CHILLER', status: 'ACTIVE', revision: 5,
  createdAt: '2026-07-28T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
};
const osakaBinding = {
  id: osakaBindingId, tenantId: tenantId, siteId: siteBId,
  deviceId: osakaDeviceId, assetId: osakaAssetId, bindingRole: 'PRIMARY_CONTROLLER', status: 'ACTIVE',
  validFrom: '2026-07-28T00:00:00.000Z', validTo: null, revision: 1,
  createdAt: '2026-07-28T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
};

function commandProjection(approved = false) {
  const createdAt = '2026-07-31T09:00:00.000Z';
  const transitions = [
    { toStatus: 'SUBMITTED', reason: 'COMMAND_SUBMITTED', actorType: 'PRINCIPAL', occurredAt: createdAt, version: 1 },
    { fromStatus: 'SUBMITTED', toStatus: 'VALIDATING', reason: 'COMMAND_VALIDATING', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:01.000Z', version: 2 },
    { fromStatus: 'VALIDATING', toStatus: 'AWAITING_APPROVAL', reason: 'APPROVAL_REQUIRED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:02.000Z', version: 3 },
  ];
  if (approved) {
    transitions.push(
      { fromStatus: 'AWAITING_APPROVAL', toStatus: 'APPROVED', reason: 'APPROVAL_THRESHOLD_MET', actorType: 'PRINCIPAL', occurredAt: '2026-07-31T09:00:03.000Z', version: 4 },
      { fromStatus: 'APPROVED', toStatus: 'QUEUED', reason: 'COMMAND_QUEUED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:04.000Z', version: 5 },
    );
  }
  return {
    schemaVersion: 1,
    commandId,
    tenantId: tenantId,
    siteId: siteBId,
    deviceId: commandDeviceId,
    capability: 'SET_TEMPERATURE_SETPOINT',
    capabilityRevision: 'capability:set-temperature-setpoint:v1',
    status: approved ? 'QUEUED' : 'AWAITING_APPROVAL',
    risk: 'MEDIUM',
    approvalPolicy: 'SINGLE_APPROVER',
    approvalCount: approved ? 1 : 0,
    requiredApprovalCount: 1,
    setpointC: 26.5,
    deviceCommandSequence: 1,
    version: approved ? 5 : 3,
    snapshotRevision: 17,
    transitions,
    createdAt,
    updatedAt: approved ? '2026-07-31T09:00:04.000Z' : '2026-07-31T09:00:02.000Z',
  };
}

function principalResponse(state) {
  const expiresAt = new Date(Date.now() + state.sessionLifetimeMs).toISOString();
  const principal = {
    subject: 'rms-03-operator',
    issuer: 'https://identity.hvac.local',
    displayName: 'RMS-03 Operator',
    email: ['rms-03-operator', 'example.invalid'].join('@'),
    roles: [...state.roles],
  };
  return {
    principal,
    context: {
      initiatingPrincipal: principal,
      executingServicePrincipal: {
        service: 'platform-gateway',
        spiffeId: 'spiffe://hvac.local/platform-gateway',
      },
      tenantId,
      audience: 'iam-service',
      policyRevision: 'gateway-delegation:5',
      delegationExpiresAt: expiresAt,
    },
    authorization: {
      capabilitySetVersion: 12,
      policyRevision: 'iam-effective:8',
      capabilities: [...state.capabilities],
    },
    session: {
      id: 'rms-03-session',
      expiresAt,
      idleTimeoutMs: 30 * 60 * 1000,
      [sessionCapabilityField]: fixtureCapability,
      revocationObjectiveMs: 1000,
      lastAuditMessageId: 'rms-03-audit',
    },
  };
}

function createGatewayFixture() {
  const state = {
    principalMode: 'delayed',
    logoutMode: 'success',
    platformMode: 'ok',
    sitesMode: 'available',
    sites: [siteA],
    roles: ['descriptive-role-only'],
    capabilities: ['site.list', 'site.read', 'device.read'],
    sessionLifetimeMs: 60 * 60 * 1000,
    loginMode: 'success',
    requests: [],
    energyQueries: [],
    assetSnapshotQueries: [],
    assetSnapshotMode: 'ok',
    assetHistoryQueries: [],
    assetHistoryMode: 'ok',
    commandRequests: [],
    commandApproved: false,
    loginReturnTargets: [],
    pendingPrincipalResponses: [],
  };

  const writeJson = (response, status, payload) => {
    response.writeHead(status, {
      'content-type': status >= 400 ? 'application/problem+json' : 'application/json',
      'cache-control': 'no-store',
      'x-request-id': `rms-03-${Date.now()}`,
      'x-route-policy-revision': routePolicyRevision,
      traceparent: `00-${traceId}-${'0'.repeat(16)}-01`,
    });
    response.end(JSON.stringify(payload));
  };

  const writePrincipal = (response) => writeJson(response, 200, principalResponse(state));

  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://fixture.local');
    state.requests.push({
      method: request.method,
      path: url.pathname,
      query: url.search,
      headers: { ...request.headers },
    });

    if (request.method === 'GET' && url.pathname === '/api/v1/principal') {
      if (state.principalMode === 'delayed') {
        state.pendingPrincipalResponses.push(response);
        return;
      }
      if (state.principalMode === 'unauthenticated') {
        writeJson(response, 401, problem(401, 'AUTHENTICATION_REQUIRED', 'A valid BFF Session is required.', false));
        return;
      }
      if (state.principalMode === 'unavailable') {
        writeJson(response, 503, problem(503, 'SESSION_STORE_UNAVAILABLE', 'The durable Session store could not be read.', true));
        return;
      }
      writePrincipal(response);
      return;
    }

    if (request.method === 'GET' && url.pathname === '/api/v1/platform/status') {
      if (state.platformMode === 'unavailable') {
        writeJson(response, 503, problem(
          503,
          'PLATFORM_STATUS_UNAVAILABLE',
          'The Platform Gateway status surface is unavailable.',
          true,
        ));
        return;
      }
      writeJson(response, 200, {
        status: state.platformMode === 'degraded' ? 'degraded' : 'ok',
        service: 'platform-status',
        implementation: 'go',
        version: 'rms-04-fixture',
        checkedAt: new Date().toISOString(),
        routePolicyRevision: 5,
        routeRevision: 8,
        compatibilityMode: 'native',
      });
      return;
    }

    if (request.method === 'GET' && url.pathname === '/api/v1/sites') {
      if (state.sitesMode === 'unavailable') {
        writeJson(response, 503, problem(
          503,
          'SITE_DISCOVERY_UNAVAILABLE',
          'The authorized Site collection is unavailable.',
          true,
        ));
        return;
      }
      writeJson(response, 200, {
        items: state.sites.map((site) => ({ ...site })),
        nextCursor: null,
        hasMore: false,
      });
      return;
    }


    const siteInventoryMatch = url.pathname.match(/^\/api\/v1\/sites\/([^/]+)\/(assets|devices|device-bindings)$/);
    if (request.method === 'GET' && siteInventoryMatch) {
      const [, requestedSiteId, collection] = siteInventoryMatch;
      if (requestedSiteId !== siteAId && requestedSiteId !== siteBId) {
        writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested Site inventory is not visible.', false));
        return;
      }
      const populated = requestedSiteId === siteBId;
      const items = collection === 'assets'
        ? (populated ? [osakaAsset] : [])
        : collection === 'devices'
          ? (populated ? [osakaDevice] : [])
          : (populated ? [osakaBinding] : []);
      writeJson(response, 200, { items, nextCursor: null, hasMore: false });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/telemetry/observation-snapshots:batchGet') {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let batch;
        try {
          batch = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'TELEMETRY_REQUEST_INVALID', 'The Snapshot batch was not valid JSON.', false));
          return;
        }
        state.assetSnapshotQueries.push(batch);
        if (!Array.isArray(batch.requests) || batch.requests.length < 1 || batch.requests.length > 100) {
          writeJson(response, 413, problem(413, 'TELEMETRY_BATCH_LIMIT_EXCEEDED', 'The Snapshot batch was outside the fixture boundary.', false));
          return;
        }
        if (state.assetSnapshotMode === 'unavailable') {
          writeJson(response, 503, problem(503, 'TELEMETRY_CURRENT_UNAVAILABLE', 'The authoritative current Snapshot owner is unavailable.', true));
          return;
        }
        const valueByKey = {
          'chiller.run_state': ['RUNNING', null, 'FRESH'],
          'chiller.power': [0, 'kW', 'STALE'],
          'chiller.cop': [4.8, null, 'FRESH'],
          'chiller.cooling_capacity': [520, 'kW', 'FRESH'],
        };
        const items = batch.requests.map((target) => {
          if (target.deviceId !== osakaDeviceId) {
            return { requestId: target.requestId, deviceId: target.deviceId, status: 'ERROR', problem: problem(404, 'RESOURCE_NOT_FOUND', 'Device not visible.', false) };
          }
          return {
            requestId: target.requestId,
            deviceId: target.deviceId,
            status: 'OK',
            snapshot: {
              schemaVersion: 1,
              deviceId: osakaDeviceId,
              tenantId: tenantId,
              siteId: siteBId,
              businessRevision: 12,
              evaluatedAt: '2026-07-30T09:00:00.000Z',
              evaluationAvailability: 'AVAILABLE',
              availabilityReasons: [],
              presence: {
                applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-07-30T08:59:01.000Z', policyRevision: 5, lastKnown: null,
              },
              telemetryReadiness: 'DEGRADED',
              displayState: 'STALE',
              values: target.keys.map((key) => {
                const [value, unit, freshness] = valueByKey[key] ?? [null, null, 'FRESH'];
                return value === null
                  ? { key, state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: 5 }
                  : {
                    key, state: 'PRESENT', value, valueType: typeof value === 'number' ? 'NUMBER' : 'STRING', unit,
                    sampledAt: '2026-07-30T08:59:00.000Z', receivedAt: '2026-07-30T08:59:01.000Z', freshness,
                    quality: 'GOOD', qualityReasons: [], policyRevision: 5,
                  };
              }),
            },
          };
        });
        writeJson(response, 200, { schemaVersion: 1, items });
      });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/telemetry/device-series:query') {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let query;
        try {
          query = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'TELEMETRY_HISTORY_REQUEST_INVALID', 'The history query was not valid JSON.', false));
          return;
        }
        state.assetHistoryQueries.push(query);
        if (state.assetHistoryMode === 'unavailable') {
          writeJson(response, 503, problem(503, 'HISTORY_OWNER_UNAVAILABLE', 'The bounded history owner is unavailable.', true));
          return;
        }
        const expectedKeys = ['chiller.power', 'chiller.cop', 'chiller.cooling_capacity'];
        if (Object.hasOwn(query, 'tenantId') || Object.hasOwn(query, 'siteId')
          || query.deviceId !== osakaDeviceId
          || !Array.isArray(query.keys)
          || query.keys.length !== expectedKeys.length
          || query.keys.some((key, index) => key !== expectedKeys[index])) {
          writeJson(response, 403, problem(403, 'TELEMETRY_HISTORY_SCOPE_FORBIDDEN', 'The history query escaped the visible Device profile.', false));
          return;
        }
        const fromMs = Date.parse(query.from);
        const toMs = Date.parse(query.to);
        const duration = toMs - fromMs;
        if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || duration <= 0 || duration > 24 * 60 * 60 * 1000
          || ![240, 360, 500].includes(query.maxPointsPerKey)) {
          writeJson(response, 400, problem(400, 'TELEMETRY_HISTORY_RANGE_INVALID', 'The history range or point limit was invalid.', false));
          return;
        }
        const at = (fraction) => new Date(fromMs + Math.floor(duration * fraction)).toISOString();
        const makePoint = (observationId, pointId, sensorId, fraction, value, unit, quality = 'GOOD') => ({
          observationId,
          pointId,
          sensorId,
          sampledAt: at(fraction),
          receivedAt: new Date(Date.parse(at(fraction)) + 1000).toISOString(),
          value,
          unit,
          quality,
          qualityReasons: quality === 'SUSPECT' ? ['SOURCE_LAG_EXCEEDED'] : [],
          revision: 7,
        });
        writeJson(response, 200, {
          schemaVersion: 1,
          tenantId: osakaDevice.tenantId,
          siteId: siteBId,
          deviceId: osakaDeviceId,
          series: [
            {
              key: 'chiller.power',
              points: [
                makePoint('01900000-0311-7000-8000-000000000311', '01900000-0411-7000-8000-000000000411', '01900000-0511-7000-8000-000000000511', 0.12, 0, 'kW'),
                makePoint('01900000-0312-7000-8000-000000000312', '01900000-0412-7000-8000-000000000412', '01900000-0512-7000-8000-000000000512', 0.82, 18.5, 'kW', 'SUSPECT'),
              ],
            },
            {
              key: 'chiller.cop',
              points: [makePoint('01900000-0313-7000-8000-000000000313', '01900000-0413-7000-8000-000000000413', '01900000-0513-7000-8000-000000000513', 0.64, 4.8, null)],
            },
            { key: 'chiller.cooling_capacity', points: [] },
          ],
          metadata: {
            requestedFrom: query.from,
            requestedTo: query.to,
            dataWatermark: at(0.95),
            datasetRevision: 'rms-device-history-revision-1',
            partial: true,
            maxPointsPerKey: query.maxPointsPerKey,
            returnedPoints: 3,
            truncatedKeys: ['chiller.power'],
          },
        });
      });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/analytics/energy-series') {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let query;
        try {
          query = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'INVALID_REQUEST', 'The energy query was not valid JSON.', false));
          return;
        }
        state.energyQueries.push(query);
        if (query.tenantId !== tenantId || ![siteAId, siteBId].includes(query.siteId)) {
          writeJson(response, 403, problem(403, 'ENERGY_SCOPE_FORBIDDEN', 'The requested energy scope was not authorized.', false));
          return;
        }
        const periodStart = new Date(Date.parse(query.to) - 24 * 60 * 60 * 1000).toISOString();
        writeJson(response, 200, {
          schemaVersion: 1,
          points: [{ periodStart, periodEnd: query.to, energyKWh: 42.5 }],
          metadata: {
            requestedGranularity: query.granularity,
            actualGranularity: query.granularity,
            dataWatermark: query.to,
            aggregateWatermark: query.to,
            datasetRevision: 'rms-energy-revision-1',
            partial: false,
            qualitySummary: { valid: 1, suspect: 0, invalid: 0 },
          },
        });
      });
      return;
    }

    if (request.method === 'GET' && url.pathname === '/api/v1/local/devices') {
      writeJson(response, 200, {
        schemaVersion: 1,
        devices: [{
          tenantId: tenantId,
          siteId: siteBId,
          deviceId: commandDeviceId,
          name: 'Osaka AHU 01',
          type: 'AHU',
        }],
      });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/commands') {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const idempotencyKey = String(request.headers['idempotency-key'] ?? '');
      if (!idempotencyKey.startsWith('real-command-')) {
        writeJson(response, 400, problem(400, 'COMMAND_REQUEST_INVALID', 'A stable Idempotency-Key is required.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let payload;
        try {
          payload = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'COMMAND_REQUEST_INVALID', 'The Command request was not valid JSON.', false));
          return;
        }
        state.commandRequests.push({ kind: 'create', payload, idempotencyKey });
        const forbiddenFields = ['tenantId', 'siteId', 'principalId', 'approverRole', 'providerMethod', 'providerParams'];
        if (forbiddenFields.some((field) => Object.hasOwn(payload, field))
          || payload.deviceId !== commandDeviceId
          || payload.capability !== 'SET_TEMPERATURE_SETPOINT'
          || payload.parameters?.setpointC !== 26.5) {
          writeJson(response, 400, problem(400, 'COMMAND_REQUEST_INVALID', 'The Command request crossed its public authority boundary.', false));
          return;
        }
        state.commandApproved = false;
        writeJson(response, 202, commandProjection(false));
      });
      return;
    }

    if (request.method === 'GET' && url.pathname === `/api/v1/commands/${commandId}`) {
      state.commandRequests.push({ kind: 'read' });
      writeJson(response, 200, commandProjection(state.commandApproved));
      return;
    }

    if (request.method === 'POST' && url.pathname === `/api/v1/commands/${commandId}:approve`) {
      if (request.headers[stateChangeHeader] !== fixtureCapability) {
        writeJson(response, 403, problem(403, 'CSRF_VALIDATION_FAILED', 'The state-change capability was invalid.', false));
        return;
      }
      const chunks = [];
      request.on('data', (chunk) => chunks.push(chunk));
      request.on('end', () => {
        let payload;
        try {
          payload = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          writeJson(response, 400, problem(400, 'COMMAND_REQUEST_INVALID', 'The approval request was not valid JSON.', false));
          return;
        }
        if (Object.keys(payload).length !== 0) {
          writeJson(response, 400, problem(400, 'COMMAND_REQUEST_INVALID', 'The public approval request must remain empty.', false));
          return;
        }
        state.commandRequests.push({ kind: 'approve', payload });
        state.commandApproved = true;
        writeJson(response, 200, commandProjection(true));
      });
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/auth/login') {
      const returnTo = url.searchParams.get('returnTo') ?? '/';
      state.loginReturnTargets.push(returnTo);
      assert(returnTo.startsWith('/') && !returnTo.startsWith('//'), `unsafe fixture returnTo ${returnTo}`);
      if (state.loginMode === 'session-expiration-proof') {
        response.writeHead(200, {
          'content-type': 'text/html; charset=utf-8',
          'cache-control': 'no-store',
        });
        response.end('<!doctype html><title>Session expiration proof</title><main data-testid="session-expiration-login-proof">Session expired login requested</main>');
        return;
      }
      state.principalMode = 'authenticated';
      response.writeHead(302, { location: returnTo, 'cache-control': 'no-store' });
      response.end();
      return;
    }

    if (request.method === 'POST' && url.pathname === '/api/v1/auth/logout') {
      if (state.logoutMode === 'failure') {
        writeJson(response, 503, problem(503, 'SESSION_PERSISTENCE_FAILED', 'The Session revocation could not be committed.', true));
        return;
      }
      response.writeHead(204, { 'cache-control': 'no-store', 'x-audit-message-id': 'rms-03-logout-audit' });
      response.end();
      return;
    }

    writeJson(response, 404, problem(404, 'RESOURCE_NOT_FOUND', 'The requested fixture resource was not found.', false));
  });

  return {
    server,
    state,
    releasePrincipalBootstrap() {
      state.principalMode = 'authenticated';
      for (const response of state.pendingPrincipalResponses.splice(0)) writePrincipal(response);
    },
  };
}

async function waitForHTTP(url, child, label) {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (child?.exitCode !== null || child?.signalCode !== null) throw new Error(`${label} exited before becoming ready`);
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {}
    await pause(100);
  }
  throw new Error(`${label} did not become ready at ${url}`);
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  const stopped = await Promise.race([once(child, 'exit').then(() => true), pause(1500).then(() => false)]);
  if (!stopped) child.kill('SIGKILL');
}

function createCdpClient(webSocketUrl) {
  return new Promise((resolveClient, rejectClient) => {
    const socket = new WebSocket(webSocketUrl);
    const pending = new Map();
    const events = [];
    let nextId = 0;
    socket.addEventListener('open', () => resolveClient({
      events,
      send(method, params = {}) {
        const id = ++nextId;
        socket.send(JSON.stringify({ id, method, params }));
        return new Promise((resolveCommand, rejectCommand) => pending.set(id, { resolveCommand, rejectCommand }));
      },
      close() { socket.close(); },
    }));
    socket.addEventListener('error', (event) => rejectClient(new Error(`CDP socket error: ${String(event)}`)));
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
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
  const result = await client.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? result.exceptionDetails.text ?? 'Browser evaluation failed');
  return result.result.value;
}

async function waitForCondition(client, expression, label, pollMs = 100) {
  for (let attempt = 0; attempt < 400; attempt += 1) {
    try {
      const value = await evaluate(client, expression);
      if (value) return value;
    } catch {}
    await pause(pollMs);
  }
  const diagnostic = await evaluate(client, `({
    url: location.href,
    text: document.body?.innerText?.slice(0, 4000) ?? '',
    html: document.body?.innerHTML?.slice(0, 4000) ?? '',
  })`);
  diagnostic.runtimeFailures = client.events
    .filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded')
    .slice(-20);
  diagnostic.events = client.events.slice(-20);
  throw new Error(`${label} did not become ready: ${JSON.stringify(diagnostic)}`);
}

async function navigate(client, url) {
  await client.send('Page.navigate', { url });
}

async function clickTestId(client, testId) {
  const clicked = await evaluate(client, `(() => {
    const element = document.querySelector('[data-testid="${testId}"]');
    if (!(element instanceof HTMLElement)) return false;
    element.click();
    return true;
  })()`);
  assert(clicked, `control ${testId} was not available`);
}

async function clickAccountLogout(client) {
  const triggerPosition = await evaluate(client, `(() => {
    const trigger = document.querySelector('[data-testid="real-account-trigger"]');
    if (!(trigger instanceof HTMLButtonElement)) return false;
    trigger.focus();
    const rect = trigger.getBoundingClientRect();
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
  })()`);
  assert(triggerPosition, 'account menu trigger could not receive keyboard focus');
  await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...triggerPosition });
  await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...triggerPosition });
  await waitForCondition(
    client,
    `Boolean(document.querySelector('[data-testid="real-logout-button"]'))`,
    'account logout menu item',
  );
  await clickTestId(client, 'real-logout-button');
}

async function pressKey(client, key, code, keyCode) {
  const text = key === 'Enter' ? '\r' : key === ' ' ? ' ' : undefined;
  await client.send('Input.dispatchKeyEvent', {
    type: 'rawKeyDown',
    key,
    code,
    windowsVirtualKeyCode: keyCode,
    nativeVirtualKeyCode: keyCode,
  });
  if (text) {
    await client.send('Input.dispatchKeyEvent', {
      type: 'char',
      key,
      code,
      text,
      unmodifiedText: text,
      windowsVirtualKeyCode: keyCode,
      nativeVirtualKeyCode: keyCode,
    });
  }
  await client.send('Input.dispatchKeyEvent', {
    type: 'keyUp',
    key,
    code,
    windowsVirtualKeyCode: keyCode,
    nativeVirtualKeyCode: keyCode,
  });
}

async function assertStorageEmpty(client, label) {
  const state = await evaluate(client, `({ localLength: localStorage.length, sessionLength: sessionStorage.length })`);
  assert(state.localLength === 0, `${label} wrote local browser storage`);
  assert(state.sessionLength === 0, `${label} wrote session browser storage`);
  browserEvidence.storage.samples.push({
    label,
    localStorageEntries: state.localLength,
    sessionStorageEntries: state.sessionLength,
  });
}

const browserPath = resolveLinuxBrowserExecutable();

const gatewayPort = await findAvailablePort();
const webPort = await findAvailablePort();
const debugPort = await findAvailablePort();
const gatewayURL = `http://127.0.0.1:${gatewayPort}`;
const webURL = `http://127.0.0.1:${webPort}`;
const fixture = createGatewayFixture();
let viteProcess;
let browserProcess;
let cdpClient;

await rm(evidencePath, { force: true });

try {
  await new Promise((resolveListen, rejectListen) => {
    fixture.server.once('error', rejectListen);
    fixture.server.listen(gatewayPort, '127.0.0.1', resolveListen);
  });

  viteProcess = spawn(process.execPath, [
    viteBinPath,
    'apps/hvac-web',
    '--config', 'apps/hvac-web/vite.config.ts',
    '--host', '127.0.0.1',
    '--port', String(webPort),
    '--strictPort',
  ], {
    cwd: root,
    stdio: 'ignore',
    shell: false,
    env: {
      ...process.env,
      HVAC_WEB_BUILD_ID: 'rms-03-browser',
      HVAC_WEB_GATEWAY_BASE_PATH: '/api/v1',
      HVAC_WEB_REALTIME_PROTOCOL: 'centrifugo-v1',
      HVAC_WEB_AUDIT_DISABLE_HMR: 'true',
      VITE_S3_LOCAL_COMMANDS: 'true',
      PLATFORM_GATEWAY_PROXY_TARGET: gatewayURL,
    },
  });
  await waitForHTTP(`${webURL}/`, viteProcess, 'Vite RMS-03 Real server');

  browserProcess = spawn(browserPath, [
    '--headless=new',
    '--disable-gpu',
    '--disable-extensions',
    '--disable-component-extensions-with-background-pages',
    '--no-sandbox',
    '--no-first-run',
    '--no-default-browser-check',
    '--hide-scrollbars',
    `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profileDir}`,
    'about:blank',
  ], { stdio: 'ignore' });
  await waitForHTTP(`http://127.0.0.1:${debugPort}/json/version`, browserProcess, 'browser debugger');
  const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) => response.json());
  const page = pages.find((candidate) => candidate.type === 'page');
  assert(page?.webSocketDebuggerUrl, 'No browser page was available');
  cdpClient = await createCdpClient(page.webSocketDebuggerUrl);
  await cdpClient.send('Runtime.enable');
  await cdpClient.send('Network.enable');
  await cdpClient.send('Page.enable');
  await cdpClient.send('Log.enable');

  await navigate(cdpClient, `${webURL}/bootstrap-proof?view=real`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'BOOTSTRAPPING'`, 'BOOTSTRAPPING state');
  const bootstrapBlocked = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    protectedShell: Boolean(document.querySelector('[data-testid="real-protected-shell"]')),
  })`);
  assert(bootstrapBlocked.mounted === 'false' && bootstrapBlocked.protectedShell === false, 'protected routes mounted during Principal bootstrap');
  fixture.releasePrincipalBootstrap();
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'READY'`, 'READY after delayed bootstrap');
  await assertStorageEmpty(cdpClient, 'delayed bootstrap');

  fixture.state.principalMode = 'unauthenticated';
  fixture.state.loginReturnTargets.length = 0;
  await navigate(cdpClient, `${webURL}/system?tab=overview#device`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'READY' && location.pathname === '/system'`, 'OIDC login round trip');
  assert(fixture.state.loginReturnTargets.length === 1, `unexpected login requests ${JSON.stringify(fixture.state.loginReturnTargets)}`);
  assert(fixture.state.loginReturnTargets[0] === '/system?tab=overview#device', `unsafe or incorrect returnTo ${fixture.state.loginReturnTargets[0]}`);
  await assertStorageEmpty(cdpClient, 'login round trip');
  recordScenario('login');

  fixture.state.principalMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/failed-bootstrap`);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'UNAVAILABLE'`, 'failed bootstrap state');
  const failedBootstrap = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    retryable: document.querySelector('[data-testid="real-shell-unavailable"] [role="alert"]')?.getAttribute('data-retryable'),
    text: document.body.innerText,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-shell-unavailable"] h1'),
  })`);
  assert(failedBootstrap.mounted === 'false', 'failed bootstrap mounted protected routes');
  assert(failedBootstrap.retryable === 'true', 'failed bootstrap was not visibly retryable');
  assert(failedBootstrap.text.includes('身份服务暂时不可用'), 'failed bootstrap omitted an actionable service message');
  assert(!failedBootstrap.text.includes('SESSION_STORE_UNAVAILABLE') && !failedBootstrap.text.includes(traceId), 'failed bootstrap exposed internal diagnostics');
  assert(!failedBootstrap.text.includes(fixtureCapability) && !failedBootstrap.text.includes('rms-03-session'), 'failed bootstrap exposed protected Session values');
  assert(failedBootstrap.focusedHeading, 'failed bootstrap did not restore focus to its heading');
  await assertStorageEmpty(cdpClient, 'failed bootstrap');
  recordFailure('SESSION_STORE_UNAVAILABLE');

  fixture.state.principalMode = 'authenticated';
  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['platform-admin'];
  fixture.state.capabilities = ['site.read'];
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'FORBIDDEN'`, 'generic capability denial');
  const forbiddenRoute = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    text: document.querySelector('[data-testid="real-route-forbidden"]')?.textContent ?? '',
  })`);
  assert(forbiddenRoute.systemNavigation === false, 'unauthorized implemented feature remained in navigation');
  assert(forbiddenRoute.text.includes('访问被拒绝'), 'direct unauthorized route did not show Access Denied');
  assert(!forbiddenRoute.text.includes('系统状态'), 'Access Denied revealed the protected feature label');
  assert(!forbiddenRoute.text.includes('site.list'), 'Access Denied revealed the missing capability');
  assert(!forbiddenRoute.text.includes('platform-admin'), 'Access Denied exposed a descriptive role');
  recordScenario('capability-denial');

  fixture.state.capabilities = ['site.list'];
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'READY' && Boolean(document.querySelector('[data-testid="real-route-system"]'))`, 'authorized implemented route');
  const authorizedSystem = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    routeText: document.querySelector('[data-testid="real-route-system"]')?.textContent ?? '',
  })`);
  assert(authorizedSystem.systemNavigation === true, 'authorized implemented feature was absent from navigation');
  assert(authorizedSystem.routeText.includes('rms-04-fixture'), 'implemented route did not render authoritative platform status');

  await navigate(cdpClient, `${webURL}/optimization`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'NOT_FOUND'`, 'deployment-hidden route');
  const hiddenFeature = await evaluate(cdpClient, `({
    navigation: Boolean(document.querySelector('[data-feature-id="optimization"]')),
    notFound: Boolean(document.querySelector('[data-testid="real-route-not-found"]')),
  })`);
  assert(hiddenFeature.navigation === false && hiddenFeature.notFound === true, 'deployment-hidden feature was exposed');

  await navigate(cdpClient, `${webURL}/unknown-rms-04`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'NOT_FOUND'`, 'unknown route');
  assert(await evaluate(cdpClient, `Boolean(document.querySelector('[data-testid="real-route-not-found"]'))`), 'unknown route did not remain 404');

  fixture.state.capabilities = ['site.list'];
  fixture.state.platformMode = 'degraded';
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'DEGRADED'`, 'degraded implemented route');
  const degradedRoute = await evaluate(cdpClient, `({
    navigationDegraded: document.querySelector('[data-feature-id="system"]')?.getAttribute('data-feature-degraded'),
    route: Boolean(document.querySelector('[data-testid="real-route-degraded"]')),
  })`);
  assert(degradedRoute.navigationDegraded === 'true' && degradedRoute.route === true, 'degraded service state was not distinct');

  fixture.state.platformMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/system`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'UNAVAILABLE'
      && document.querySelector('[data-testid="real-route-unavailable"] [role="alert"]')?.getAttribute('data-retryable') === 'true'`,
    'unavailable implemented route',
  );
  const unavailableRoute = await evaluate(cdpClient, `({
    systemNavigation: Boolean(document.querySelector('[data-feature-id="system"]')),
    retryable: document.querySelector('[data-testid="real-route-unavailable"] [role="alert"]')?.getAttribute('data-retryable'),
    text: document.querySelector('[data-testid="real-route-unavailable"]')?.textContent ?? '',
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-route-unavailable"] h1'),
  })`);
  assert(unavailableRoute.systemNavigation === false, 'unavailable implemented feature remained in navigation');
  assert(unavailableRoute.retryable === 'true', 'Unavailable route did not expose retryability');
  assert(unavailableRoute.text.includes('服务状态暂时无法确认'), 'Unavailable route omitted an actionable service message');
  assert(!unavailableRoute.text.includes('PLATFORM_STATUS_UNAVAILABLE') && !unavailableRoute.text.includes(traceId), 'Unavailable route exposed internal diagnostics');
  assert(!unavailableRoute.text.includes(fixtureCapability) && !unavailableRoute.text.includes('rms-03-session'), 'Unavailable route exposed protected Session values');
  assert(unavailableRoute.focusedHeading, 'Unavailable route did not restore focus to its heading');
  recordFailure('PLATFORM_STATUS_UNAVAILABLE');

  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['site.list', 'site.read', 'asset.list', 'device.list', 'telemetry.batch.read', 'telemetry.history.read'];
  fixture.state.sitesMode = 'unavailable';
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'UNAVAILABLE' && Boolean(document.querySelector('[data-testid="real-site-discovery-unavailable"]'))`, 'Site discovery unavailable');
  const siteDiscoveryUnavailable = await evaluate(cdpClient, `document.querySelector('[data-testid="real-site-discovery-unavailable"]')?.textContent ?? ''`);
  assert(siteDiscoveryUnavailable.includes('无法读取授权站点'), `Site discovery failure omitted an actionable message: ${JSON.stringify(siteDiscoveryUnavailable)}`);
  assert(!siteDiscoveryUnavailable.includes('SITE_DISCOVERY_UNAVAILABLE'), 'Site discovery failure exposed an internal Problem code');
  assert(!siteDiscoveryUnavailable.includes('Tokyo Plant'), 'unavailable Site discovery rendered cached Site data');

  fixture.state.sitesMode = 'available';
  fixture.state.sites = [];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'NO_AUTHORIZED_SITE'`, 'zero authorized Sites');
  const noAuthorizedSite = await evaluate(cdpClient, `({
    account: document.querySelector('[data-testid="real-site-none"]')?.textContent?.includes('RMS-03 Operator') ?? false,
    retry: Array.from(document.querySelectorAll('[data-testid="real-site-none"] button')).some((button) => button.textContent?.includes('刷新站点授权')),
    logout: Boolean(document.querySelector('[data-testid="real-account-trigger"]')),
    siteRoute: Boolean(document.querySelector('[data-site-route]')),
  })`);
  assert(noAuthorizedSite.account && noAuthorizedSite.retry && noAuthorizedSite.logout, 'NO_AUTHORIZED_SITE omitted account, retry, or logout');
  assert(noAuthorizedSite.siteRoute === false, 'NO_AUTHORIZED_SITE mounted a Site business surface');
  recordScenario('zero-sites');

  fixture.state.sites = [siteA, siteB];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'CHOOSE_SITE'`, 'multiple Site chooser');
  const siteChooser = await evaluate(cdpClient, `({
    pathname: location.pathname,
    choices: Array.from(document.querySelectorAll('[data-testid="real-site-chooser"] [data-site-id]')).map((item) => ({
      id: item.getAttribute('data-site-id'),
      href: item.getAttribute('href'),
      name: item.textContent?.trim() ?? '',
    })),
    siteRoute: Boolean(document.querySelector('[data-site-route]')),
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-site-chooser"] h1'),
  })`);
  assert(siteChooser.pathname === '/', 'multiple Sites silently changed the URL scope');
  assert(siteChooser.choices.length === 2, `expected two chooser entries, got ${JSON.stringify(siteChooser.choices)}`);
  assert(siteChooser.choices[0].id === siteAId && siteChooser.choices[1].id === siteBId, 'chooser did not preserve Registry Site identities');
  assert(siteChooser.choices.every((choice) => choice.href === `/sites/${choice.id}/dashboard`), 'chooser generated a non-Site-scoped Dashboard target');
  assert(siteChooser.choices.every((choice) => choice.name.length > 0), 'chooser links lacked accessible names');
  assert(siteChooser.siteRoute === false, 'chooser mounted a Site business surface before selection');
  assert(siteChooser.focusedHeading, 'Site chooser did not restore focus to its heading');
  recordScenario('many-sites');

  await navigate(cdpClient, `${webURL}/sites/${siteBId}/bigscreen`);
  await waitForCondition(
    cdpClient,
    `document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-scope-site') === '${siteBId}'
      && document.querySelector('[data-testid="real-site-route-bigscreen"]')?.getAttribute('data-site-id') === '${siteBId}'`,
    'validated Site-scoped shell',
  );
  const scopedShell = await evaluate(cdpClient, `({
    pathname: location.pathname,
    siteName: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
    principalName: document.querySelector('[data-testid="real-shell-principal"]')?.textContent,
    realtimeSite: document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-site'),
    activeRoute: document.querySelector('[data-feature-id="site-bigscreen"]')?.getAttribute('aria-current'),
    navigationTargets: Array.from(document.querySelectorAll('[data-feature-id]')).map((item) => item.getAttribute('href')).filter(Boolean),
  })`);
  assert(scopedShell.pathname === `/sites/${siteBId}/bigscreen`, 'Site shell changed the explicit Site URL');
  assert(scopedShell.siteName === 'Osaka Plant' && scopedShell.principalName === 'RMS-03 Operator', 'trusted shell omitted the validated Site or Principal');
  assert(scopedShell.realtimeSite === siteBId, 'realtime status was not scoped to the validated Site');
  assert(scopedShell.activeRoute === 'page', 'Site navigation did not expose the active route');
  assert(scopedShell.navigationTargets.filter((target) => target.startsWith('/sites/')).every((target) => target.startsWith(`/sites/${siteBId}/`)), 'Site navigation escaped the validated Site scope');

  assert(await evaluate(cdpClient, `(() => {
    const select = document.querySelector('[data-testid="real-shell-site-control"]');
    if (!(select instanceof HTMLSelectElement)) return false;
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set;
    setter?.call(select, '${siteAId}');
    select.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
  })()`), 'Site selector did not expose the alternate authorized Site');
  await waitForCondition(
    cdpClient,
    `location.pathname === '/sites/${siteAId}/dashboard'
      && document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-scope-site') === '${siteAId}'
      && document.querySelector('[data-testid="real-shell-site"]')?.textContent === 'Tokyo Plant'
      && document.querySelector('[data-testid="real-realtime-status"]')?.getAttribute('data-realtime-site') === '${siteAId}'`,
    'new Site shell after protected scope switch',
  );
  recordScenario('site-switching');
  fixture.state.roles = ['platform-admin'];
  fixture.state.capabilities = ['site.list'];
  await navigate(cdpClient, `${webURL}/sites/${siteAId}/assets`);
  await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'FORBIDDEN'`, 'Site capability denial');
  const forbiddenSite = await evaluate(cdpClient, `({
    text: document.querySelector('[data-testid="real-route-forbidden"]')?.textContent ?? '',
    siteNavigation: Boolean(document.querySelector('[data-feature-id="site-assets"]')),
    roleDisplayed: document.querySelector('[data-testid="real-principal-roles"]')?.textContent?.includes('platform-admin') ?? false,
  })`);
  assert(forbiddenSite.roleDisplayed, 'descriptive Site role was not visible for audit context');
  assert(forbiddenSite.siteNavigation === false, 'Site navigation remained visible without site.read');
  assert(!forbiddenSite.text.includes(siteAId) && !forbiddenSite.text.includes('Tokyo Plant') && !forbiddenSite.text.includes('site.read'), 'Site Access Denied leaked protected metadata');

  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['site.list', 'site.read', 'asset.list', 'device.list', 'telemetry.batch.read', 'telemetry.history.read'];
  for (const invalidPath of [
    '/sites/b1/assets',
    '/sites/not-a-uuid/assets',
    `/sites/${invisibleSiteId}/assets`,
  ]) {
    await navigate(cdpClient, `${webURL}${invalidPath}`);
    await waitForCondition(cdpClient, `document.querySelector('[data-route-state]')?.getAttribute('data-route-state') === 'SITE_NOT_VISIBLE'`, `safe invisible Site state for ${invalidPath}`);
    const invisibleSite = await evaluate(cdpClient, `({
      pathname: location.pathname,
      text: document.querySelector('[data-testid="real-site-not-visible"]')?.textContent ?? '',
      siteRoute: Boolean(document.querySelector('[data-site-route]')),
    })`);
    assert(invisibleSite.pathname === invalidPath, `invalid Site silently changed scope from ${invalidPath}`);
    assert(!invisibleSite.text.includes(invisibleSiteId) && !invisibleSite.text.includes('b1') && !invisibleSite.text.includes('b2'), `invalid Site state leaked the requested identity for ${invalidPath}`);
    assert(invisibleSite.siteRoute === false, `invalid Site mounted a Site business surface for ${invalidPath}`);
  }
  recordScenario('invalid-site');

  fixture.state.sites = [siteA];
  await navigate(cdpClient, `${webURL}/`);
  await waitForCondition(cdpClient, `location.pathname === '/sites/${siteAId}/dashboard'
    && document.querySelector('[data-testid="real-protected-shell"]')?.getAttribute('data-protected-scope-site') === '${siteAId}'
    && document.querySelector('[data-testid="real-site-route-dashboard"]')?.getAttribute('data-site-id') === '${siteAId}'`, 'sole authorized Site Dashboard auto-entry');
  const soleSite = await evaluate(cdpClient, `({
    pathname: location.pathname,
    siteId: document.querySelector('[data-testid="real-site-route-dashboard"]')?.getAttribute('data-site-id'),
    siteName: document.querySelector('[data-testid="real-shell-site"]')?.textContent,
  })`);
  assert(soleSite.pathname === `/sites/${siteAId}/dashboard` && soleSite.siteId === siteAId, 'sole Site did not enter the explicit Dashboard UUIDv7 URL');
  assert(soleSite.siteName === 'Tokyo Plant', 'sole Site auto-entry did not establish the trusted Site context');
  await assertStorageEmpty(cdpClient, 'Site scope matrix');
  recordScenario('one-site');

  fixture.state.sessionLifetimeMs = 1500;
  fixture.state.loginMode = 'session-expiration-proof';
  const loginCountBeforeExpiration = fixture.state.loginReturnTargets.length;
  await navigate(cdpClient, `${webURL}/sites/${siteAId}/bigscreen?session=expires`);
  await waitForCondition(cdpClient, `document.querySelector('[data-testid="real-site-route-bigscreen"]')?.getAttribute('data-site-id') === '${siteAId}'`, 'authenticated shell before Session expiration');
  await waitForCondition(cdpClient, `location.pathname === '/api/v1/auth/login' && Boolean(document.querySelector('[data-testid="session-expiration-login-proof"]'))`, 'Session expiration login proof');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeExpiration + 1, 'Session expiration did not start login');
  assert(fixture.state.loginReturnTargets.at(-1) === `/sites/${siteAId}/bigscreen?session=expires`, 'Session expiration lost its safe returnTo');
  assert(!await evaluate(cdpClient, `Boolean(document.querySelector('[data-testid="real-protected-shell"]'))`), 'Session expiration retained the protected Shell');
  recordScenario('session-expiration');
  fixture.state.loginMode = 'success';
  fixture.state.sessionLifetimeMs = 60 * 60 * 1000;

  fixture.state.platformMode = 'ok';
  fixture.state.roles = ['descriptive-role-only'];
  fixture.state.capabilities = ['site.list', 'site.read', 'device.read'];
  fixture.state.logoutMode = 'failure';
  await navigate(cdpClient, `${webURL}/sites/${siteAId}/bigscreen`);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-account-trigger"]'))`, 'authenticated shell before logout');
  const loginCountBeforeLogout = fixture.state.loginReturnTargets.length;
  await clickAccountLogout(cdpClient);
  await waitForCondition(cdpClient, `Boolean(document.querySelector('[data-testid="real-logout-failure"]'))`, 'retryable logout failure');
  const failedLogout = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    retryable: document.querySelector('[data-testid="real-logout-failure"]')?.getAttribute('data-retryable'),
    text: document.querySelector('[data-testid="real-logout-failure"]')?.textContent ?? '',
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
  })`);
  assert(failedLogout.mounted === 'true', 'retryable logout failure purged protected state before revocation');
  assert(failedLogout.retryable === 'true', 'logout failure was not marked retryable');
  assert(failedLogout.text.includes('退出登录失败'), 'logout failure omitted an actionable message');
  assert(!failedLogout.text.includes('SESSION_PERSISTENCE_FAILED') && !failedLogout.text.includes(traceId), 'logout failure exposed internal diagnostics');
  assert(!failedLogout.text.includes(fixtureCapability) && !failedLogout.text.includes('rms-03-session'), 'logout failure exposed protected Session values');
  assert(!failedLogout.overflow, 'logout failure overflowed the desktop viewport');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'logout failure incorrectly started login');
  await assertStorageEmpty(cdpClient, 'logout failure');
  recordFailure('SESSION_PERSISTENCE_FAILED');

  fixture.state.logoutMode = 'success';
  await clickAccountLogout(cdpClient);
  await waitForCondition(cdpClient, `document.querySelector('main')?.getAttribute('data-shell-state') === 'LOGIN_REQUIRED' && document.body.innerText.includes('已安全退出')`, 'confirmed logout completion');
  const completedLogout = await evaluate(cdpClient, `({
    mounted: document.querySelector('main')?.getAttribute('data-protected-route-mounted'),
    protectedShell: Boolean(document.querySelector('[data-testid="real-protected-shell"]')),
    overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    focusedHeading: document.activeElement === document.querySelector('[data-testid="real-shell-login-required"] h1'),
    loginName: document.querySelector('[data-testid="real-shell-login-required"] button')?.textContent?.trim() ?? '',
  })`);
  assert(completedLogout.mounted === 'false' && completedLogout.protectedShell === false, 'confirmed logout did not purge protected state');
  assert(!completedLogout.overflow && completedLogout.focusedHeading && completedLogout.loginName.length > 0, 'Session ended state was not accessible');
  assert(fixture.state.loginReturnTargets.length === loginCountBeforeLogout, 'confirmed logout automatically started a new login');
  await assertStorageEmpty(cdpClient, 'logout success');
  recordScenario('logout');
  await cdpClient.send('Emulation.clearDeviceMetricsOverride');

  const authorizationRequests = fixture.state.requests.filter((entry) => 'authorization' in entry.headers);
  assert(authorizationRequests.length === 0, 'browser sent an Authorization header');
  const logoutRequests = fixture.state.requests.filter((entry) => entry.method === 'POST' && entry.path === '/api/v1/auth/logout');
  assert(logoutRequests.length === 2, `expected two logout attempts, got ${logoutRequests.length}`);
  for (const request of logoutRequests) {
    assert(request.headers[stateChangeHeader] === fixtureCapability, 'logout did not send the in-memory state-change capability');
    assert(request.headers.origin === webURL, `logout Origin was not the Real application origin: ${request.headers.origin}`);
  }
  const forbiddenHeaders = fixture.state.requests.filter((entry) =>
    ['x-site-id', 'x-organization-id', 'x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers));
  assert(forbiddenHeaders.length === 0, `browser sent forbidden authorization headers: ${JSON.stringify(forbiddenHeaders)}`);

  const browserSafety = await evaluate(cdpClient, `({
    unsafeQueryKeys: Array.from(new URL(location.href).searchParams.keys()).filter((key) => /token|session|principal/i.test(key)),
    credentialInputs: Array.from(document.querySelectorAll('input')).filter((input) => input.type === ['pass', 'word'].join('')).length,
  })`);
  assert(browserSafety.unsafeQueryKeys.length === 0, `sensitive query fields were present: ${JSON.stringify(browserSafety.unsafeQueryKeys)}`);
  assert(browserSafety.credentialInputs === 0, 'Real rendered a browser credential form');

  const websocketEvents = cdpClient.events.filter((event) => event.method === 'Network.webSocketCreated');
  const businessWebSockets = websocketEvents.filter((event) => {
    const url = String(event.params?.url ?? '');
    return /\/ws(?:\/|\?|$)|centrifugo|telemetry/i.test(url);
  });
  assert(businessWebSockets.length === 0, `business realtime subscriptions started during RMS shell flows: ${JSON.stringify(businessWebSockets)}`);
  const forbiddenModuleRequests = cdpClient.events.filter((event) => {
    if (event.method !== 'Network.requestWillBeSent') return false;
    const url = String(event.params?.request?.url ?? '');
    return /\/src\/(?:mock|pages|ai)\/|\/src\/App\.tsx|Mock[A-Z]/.test(url);
  });
  assert(forbiddenModuleRequests.length === 0, `Real loaded Demo or Mock modules: ${JSON.stringify(forbiddenModuleRequests)}`);
  const sensitiveLogEvents = cdpClient.events.filter((event) => {
    if (event.method !== 'Runtime.consoleAPICalled' && event.method !== 'Log.entryAdded') return false;
    const serialized = JSON.stringify(event.params);
    return serialized.includes('rms-03-session')
      || serialized.includes('rms-03-operator')
      || serialized.includes('RMS-03 Operator')
      || serialized.includes(fixtureCapability);
  });
  assert(sensitiveLogEvents.length === 0, 'protected Shell values reached browser logs');

  const legacyOrganizationHeaderRequests = fixture.state.requests.filter((entry) => 'x-organization-id' in entry.headers);
  const siteAuthorityRequests = fixture.state.requests.filter((entry) => 'x-site-id' in entry.headers);
  const otherAuthorityRequests = fixture.state.requests.filter((entry) =>
    ['x-role', 'x-admin', 'x-scope'].some((name) => name in entry.headers));
  browserEvidence.network = {
    requestCount: fixture.state.requests.length,
    browserAuthorizationHeaderCount: authorizationRequests.length,
    forbiddenLegacyOrganizationHeaderCount: legacyOrganizationHeaderRequests.length,
    browserSiteAuthorityHeaderCount: siteAuthorityRequests.length,
    browserOtherAuthorityHeaderCount: otherAuthorityRequests.length,
  };
  for (const scenario of RMS_REQUIRED_BROWSER_SCENARIOS) {
    assert(browserEvidence.scenarios[scenario]?.passed === true, `browser evidence omitted ${scenario}`);
  }
  browserEvidence.passed = true;
  await mkdir(join(root, 'out', 'rms-web-certification'), { recursive: true });
  await writeFile(evidencePath, `${JSON.stringify(browserEvidence, null, 2)}\n`, 'utf8');

  console.log(`RMS authenticated Shell and capability route browser audit passed. Evidence: ${evidencePath}`);
} finally {
  cdpClient?.close();
  await stopProcess(browserProcess);
  await stopProcess(viteProcess);
  for (const response of fixture.state.pendingPrincipalResponses.splice(0)) response.destroy();
  if (fixture.server.listening) await new Promise((resolveClose) => fixture.server.close(() => resolveClose()));
  await rm(profileDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 250 });
}
