import assert from 'node:assert/strict';
import test from 'node:test';
import {
  loadRealAssetsCurrentState,
  loadRealAssetsRegistry,
  realAssetsCurrentStateQueryKey,
} from '../apps/hvac-web/src/real/assets/data.ts';
import { runRealAssetsProtectedRequest } from '../apps/hvac-web/src/real/assets/protected-request.ts';
import { createRealAssetsTelemetryRuntime } from '../apps/hvac-web/src/real/assets/telemetry-runtime.ts';

const organizationId = '01900000-0000-7000-8000-000000000001';
const siteId = '01900000-0001-7000-8000-000000000001';
const equipmentId = '01900000-0002-7000-8000-000000000001';
const areaId = '01900000-0005-7000-8000-000000000001';
const sensorId = '01900000-0006-7000-8000-000000000001';
const measuredPointId = '01900000-0007-7000-8000-000000000001';
const calculatedPointId = '01900000-0007-7000-8000-000000000002';
const csrfCapability = '[TEST_CSRF_CAPABILITY]';

function id(kind, index) {
  return `01900000-${kind.toString(16).padStart(4, '0')}-7${index.toString(16).padStart(3, '0')}-8000-${index.toString(16).padStart(12, '0')}`;
}

function device(index, overrides = {}) {
  return {
    id: id(3, index), owningOrganizationId: organizationId, siteId,
    code: `DEV-${index}`, displayName: `Device ${index}`,
    deviceType: index % 2 === 0 ? 'CHILLER' : 'vendor-special-controller',
    status: 'ACTIVE', revision: index + 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function equipment(overrides = {}) {
  return {
    id: equipmentId, owningOrganizationId: organizationId, siteId,
    code: 'CHILLER-A', displayName: 'Chiller A', equipmentType: 'CHILLER', status: 'ACTIVE', revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function area(overrides = {}) {
  return {
    id: areaId, owningOrganizationId: organizationId, siteId, parentAreaId: null,
    code: 'CENTRAL-PLANT', displayName: 'Central Plant', areaType: 'PLANT_ROOM', status: 'ACTIVE', revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function sensor(overrides = {}) {
  return {
    id: sensorId, owningOrganizationId: organizationId, siteId,
    code: 'SENSOR-1', displayName: 'Sensor 1', sensorType: 'TEMPERATURE',
    manufacturer: null, model: null, serialNumber: null, calibrationDueAt: null, metadata: {},
    status: 'ACTIVE', revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function point(pointId, pointKind, overrides = {}) {
  return {
    id: pointId, owningOrganizationId: organizationId, siteId,
    reportingDeviceId: device(1).id, sensorId: pointKind === 'CALCULATED' ? null : sensorId,
    pointKey: pointKind === 'CALCULATED' ? 'plant.delta_t' : 'plant.temperature',
    sourceKey: pointKind === 'CALCULATED' ? 'calculated.delta_t' : 'sensor.temperature',
    displayName: pointKind === 'CALCULATED' ? 'Delta T' : 'Temperature',
    pointKind, valueType: 'NUMBER', unit: 'Cel', writable: false,
    sampleIntervalMs: 1000, publishIntervalMs: 1000, staleAfterMs: 5000,
    formulaRevision: pointKind === 'CALCULATED' ? 'formula-v1' : null,
    sourceMetadata: {}, status: 'ACTIVE', revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function relationship(index, fromType, fromId, toType, toId, role, overrides = {}) {
  return {
    id: id(8, index), owningOrganizationId: organizationId, siteId,
    fromType, fromId, toType, toId, role, status: 'ACTIVE',
    validFrom: '2026-07-01T00:00:00.000Z', validTo: null, revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z', updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function assetModel(overrides = {}) {
  const visibleDevice = device(1);
  const model = {
    schemaVersion: 1,
    siteId,
    areas: [area()],
    equipment: [equipment()],
    devices: [visibleDevice],
    sensors: [sensor()],
    telemetryPoints: [point(measuredPointId, 'MEASURED'), point(calculatedPointId, 'CALCULATED')],
    relationships: [
      relationship(1, 'EQUIPMENT', equipmentId, 'AREA', areaId, 'INSTALLED_IN'),
      relationship(2, 'DEVICE', visibleDevice.id, 'EQUIPMENT', equipmentId, 'PRIMARY_CONTROLLER'),
      relationship(3, 'SENSOR', sensorId, 'DEVICE', visibleDevice.id, 'INDEPENDENT_DEVICE'),
    ],
    calculatedPointInputs: [{
      owningOrganizationId: organizationId, siteId, calculatedPointId, inputPointId: measuredPointId,
      inputRole: 'TEMPERATURE', ordinal: 0, formulaRevision: 'formula-v1',
    }],
    counts: {
      areas: 1, equipment: 1, deviceEndpoints: 1, sensors: 1,
      telemetryPoints: 2, calculatedPoints: 1, independentSensorDevices: 1,
    },
  };
  return { ...model, ...overrides };
}

function platformResponse(data, routePolicyRevision = '12') {
  return { data, requestId: null, traceparent: null, auditMessageId: null, routePolicyRevision };
}

function snapshot(target) {
  return {
    schemaVersion: 1, deviceId: target.deviceId, owningOrganizationId: organizationId, siteId, businessRevision: 10,
    evaluatedAt: '2026-07-30T10:00:00.000Z', evaluationAvailability: 'AVAILABLE', availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-07-30T09:59:01.000Z', policyRevision: 2, lastKnown: null,
    },
    telemetryReadiness: target.keys.length ? 'CURRENT' : 'NOT_APPLICABLE', displayState: 'ONLINE',
    values: target.keys.map((key, index) => ({
      key, state: 'PRESENT', value: index, valueType: 'NUMBER', unit: null,
      sampledAt: '2026-07-30T09:59:00.000Z', receivedAt: '2026-07-30T09:59:01.000Z',
      freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: 2,
    })),
  };
}

test('Registry loader reads one atomic Site Asset Model and preserves route-policy evidence', async () => {
  const model = assetModel();
  const signal = new AbortController().signal;
  const calls = [];
  const client = {
    getSiteAssetModel: async (requestedSiteId, init) => {
      calls.push([requestedSiteId, init.signal]);
      return platformResponse(model);
    },
  };
  const result = await loadRealAssetsRegistry({ client, organizationId, siteId, signal });
  assert.equal(result.assetModel.devices.length, 1);
  assert.equal(result.assetModel.telemetryPoints.length, 2);
  assert.equal(result.assetModel.counts.independentSensorDevices, 1);
  assert.equal(result.routePolicyRevision, '12');
  assert.deepEqual(calls, [[siteId, signal]]);
});

test('Registry loader rejects scope drift and invisible relationship targets', async () => {
  const client = {
    getSiteAssetModel: async () => platformResponse(assetModel({
      relationships: [relationship(1, 'DEVICE', device(1).id, 'EQUIPMENT', id(2, 99), 'PRIMARY_CONTROLLER')],
    })),
  };
  await assert.rejects(
    loadRealAssetsRegistry({ client, organizationId, siteId, signal: new AbortController().signal }),
    /outside the visible Site model/,
  );
  client.getSiteAssetModel = async () => platformResponse(assetModel({ devices: [device(1, { siteId: id(1, 99) })] }));
  await assert.rejects(
    loadRealAssetsRegistry({ client, organizationId, siteId, signal: new AbortController().signal }),
    /crossed the authenticated Organization or Site scope/,
  );
});

test('Registry loader rejects count and calculated-input drift in the atomic graph', async () => {
  const countDriftClient = {
    getSiteAssetModel: async () => platformResponse(assetModel({
      counts: { ...assetModel().counts, telemetryPoints: 3 },
    })),
  };
  await assert.rejects(
    loadRealAssetsRegistry({ client: countDriftClient, organizationId, siteId, signal: new AbortController().signal }),
    /counts do not match/,
  );

  const inputDriftClient = {
    getSiteAssetModel: async () => platformResponse(assetModel({
      calculatedPointInputs: [{
        owningOrganizationId: organizationId, siteId, calculatedPointId: measuredPointId, inputPointId: calculatedPointId,
        inputRole: 'INVALID', ordinal: 0, formulaRevision: 'formula-v1',
      }],
    })),
  };
  await assert.rejects(
    loadRealAssetsRegistry({ client: inputDriftClient, organizationId, siteId, signal: new AbortController().signal }),
    /referenced an invalid Point/,
  );
});

test('Current-state loader splits 200 Devices into two exact bounded batches', async () => {
  const devices = Array.from({ length: 200 }, (_, index) => device(index + 1));
  const calls = [];
  const signal = new AbortController().signal;
  const client = {
    batchGetDeviceObservationSnapshots: async (request, options) => {
      calls.push({ request, options });
      return {
        schemaVersion: 1,
        items: request.requests.map((target) => ({
          requestId: target.requestId, deviceId: target.deviceId, status: 'OK', snapshot: snapshot(target),
        })),
      };
    },
  };
  const result = await loadRealAssetsCurrentState({
    client, devices, organizationId, siteId, csrfToken: csrfCapability, currentRoutePolicyRevision: () => '12', signal,
  });
  assert.equal(result.requestCount, 2);
  assert.equal(result.byDeviceId.size, 200);
  assert.equal(result.routePolicyRevision, '12');
  assert.deepEqual(calls.map(({ request }) => request.requests.length), [100, 100]);
  assert.ok(calls.every(({ request, options }) => request.requests.reduce((total, target) => total + target.keys.length, 0) <= 2048
    && options.signal === signal
    && options.csrfToken === csrfCapability));
  assert.equal(calls[0].request.requests[0].keys.length, 0);
  assert.equal(calls[0].request.requests[1].keys.length, 4);
});

test('Current-state loader preserves per-item failures but rejects response order or scope drift', async () => {
  const devices = [device(1), device(2)];
  const problem = {
    type: 'about:blank', title: 'not visible', status: 404, detail: 'not visible',
    instance: '/api/v1/telemetry/observation-snapshots:batchGet', code: 'RESOURCE_NOT_FOUND',
    traceId: '0123456789abcdef0123456789abcdef', retryable: false,
  };
  const partialClient = {
    batchGetDeviceObservationSnapshots: async (request) => ({
      schemaVersion: 1,
      items: [
        { requestId: request.requests[0].requestId, deviceId: request.requests[0].deviceId, status: 'ERROR', problem },
        { requestId: request.requests[1].requestId, deviceId: request.requests[1].deviceId, status: 'OK', snapshot: snapshot(request.requests[1]) },
      ],
    }),
  };
  const partial = await loadRealAssetsCurrentState({
    client: partialClient, devices, organizationId, siteId, csrfToken: csrfCapability, currentRoutePolicyRevision: () => '12', signal: new AbortController().signal,
  });
  assert.equal(partial.partial, true);
  assert.equal(partial.byDeviceId.get(devices[0].id).status, 'error');

  const driftClient = {
    batchGetDeviceObservationSnapshots: async (request) => ({
      schemaVersion: 1,
      items: request.requests.map((target) => ({
        requestId: target.requestId, deviceId: target.deviceId, status: 'OK',
        snapshot: { ...snapshot(target), siteId: id(1, 99) },
      })),
    }),
  };
  await assert.rejects(
    loadRealAssetsCurrentState({
      client: driftClient, devices, organizationId, siteId, csrfToken: csrfCapability, currentRoutePolicyRevision: () => '12', signal: new AbortController().signal,
    }),
    /scope or selected-key order drifted/,
  );

  const displayDriftClient = {
    batchGetDeviceObservationSnapshots: async (request) => ({
      schemaVersion: 1,
      items: request.requests.map((target) => ({
        requestId: target.requestId, deviceId: target.deviceId, status: 'OK',
        snapshot: { ...snapshot(target), displayState: 'OFFLINE' },
      })),
    }),
  };
  await assert.rejects(
    loadRealAssetsCurrentState({
      client: displayDriftClient, devices, organizationId, siteId, csrfToken: csrfCapability, currentRoutePolicyRevision: () => '12', signal: new AbortController().signal,
    }),
    /display-state evidence drifted/,
  );
});

test('Current-state loader rejects route-policy revision drift across bounded batches', async () => {
  const devices = Array.from({ length: 200 }, (_, index) => device(index + 1));
  let revision = '12';
  let calls = 0;
  const client = {
    batchGetDeviceObservationSnapshots: async (request) => {
      calls += 1;
      revision = calls === 1 ? '12' : '13';
      return {
        schemaVersion: 1,
        items: request.requests.map((target) => ({
          requestId: target.requestId,
          deviceId: target.deviceId,
          status: 'OK',
          snapshot: snapshot(target),
        })),
      };
    },
  };
  await assert.rejects(
    loadRealAssetsCurrentState({
      client,
      devices,
      organizationId,
      siteId,
      csrfToken: csrfCapability,
      currentRoutePolicyRevision: () => revision,
      signal: new AbortController().signal,
    }),
    /route-policy revision changed during bounded batch loading/,
  );
});

test('S2 runtime retains route-policy evidence and publishes material changes once', async () => {
  const revisions = ['12', '12', '13'];
  const changes = [];
  const runtime = createRealAssetsTelemetryRuntime('', async () => new Response(
    JSON.stringify({ schemaVersion: 1, items: [] }),
    {
      status: 200,
      headers: {
        'content-type': 'application/json',
        'x-route-policy-revision': revisions.shift(),
      },
    },
  ));
  const unsubscribe = runtime.subscribeRoutePolicyChange((previousRevision, nextRevision) => {
    changes.push([previousRevision, nextRevision]);
  });
  for (let index = 0; index < 3; index += 1) {
    await runtime.client.batchGetDeviceObservationSnapshots({ requests: [] }, { csrfToken: csrfCapability });
  }
  await new Promise((resolve) => queueMicrotask(resolve));
  unsubscribe();
  assert.equal(runtime.currentRoutePolicyRevision(), '13');
  assert.deepEqual(changes, [['12', '13']]);
});

test('current-state query keys isolate generation, Site, policy epoch and catalog-selected keys', () => {
  const devices = [device(1), device(2)];
  const base = realAssetsCurrentStateQueryKey(4, organizationId, siteId, devices, 0);
  assert.notDeepEqual(base, realAssetsCurrentStateQueryKey(5, organizationId, siteId, devices, 0));
  assert.notDeepEqual(base, realAssetsCurrentStateQueryKey(4, organizationId, id(1, 99), devices, 0));
  assert.notDeepEqual(base, realAssetsCurrentStateQueryKey(4, organizationId, siteId, devices, 1));
  assert.notDeepEqual(base, realAssetsCurrentStateQueryKey(4, organizationId, siteId, [device(1, { revision: 99 }), device(2)], 0));
});

test('protected request returns only through the active Site generation commit guard', async () => {
  const scopeController = new AbortController();
  const queryController = new AbortController();
  let commits = 0;
  const scopeGuard = {
    siteId,
    generation: 7,
    signal: scopeController.signal,
    commit: (commit) => {
      commits += 1;
      commit();
      return true;
    },
  };
  const result = await runRealAssetsProtectedRequest(scopeGuard, queryController.signal, async (signal) => {
    assert.equal(signal.aborted, false);
    return 'accepted';
  });
  assert.equal(result, 'accepted');
  assert.equal(commits, 1);
});

test('protected request propagates query cancellation into the in-flight operation', async () => {
  const scopeController = new AbortController();
  const queryController = new AbortController();
  const scopeGuard = {
    siteId,
    generation: 8,
    signal: scopeController.signal,
    commit: () => true,
  };
  const pending = runRealAssetsProtectedRequest(scopeGuard, queryController.signal, (signal) => new Promise((resolve, reject) => {
    signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  }));
  queryController.abort(new DOMException('query cancelled', 'AbortError'));
  await assert.rejects(pending, (error) => error instanceof DOMException && error.name === 'AbortError');
});

test('protected request refuses commit when cancellation arrives before a resolved operation returns', async () => {
  const queryController = new AbortController();
  let commits = 0;
  const scopeGuard = {
    siteId,
    generation: 9,
    signal: new AbortController().signal,
    commit: (commit) => {
      commits += 1;
      commit();
      return true;
    },
  };
  await assert.rejects(
    runRealAssetsProtectedRequest(scopeGuard, queryController.signal, async () => {
      queryController.abort(new DOMException('query cancelled', 'AbortError'));
      return 'late';
    }),
    (error) => error instanceof DOMException && error.name === 'AbortError',
  );
  assert.equal(commits, 0);
});

test('protected request rejects a late response after the Site generation is revoked', async () => {
  const scopeGuard = {
    siteId,
    generation: 9,
    signal: new AbortController().signal,
    commit: () => false,
  };
  await assert.rejects(
    runRealAssetsProtectedRequest(scopeGuard, new AbortController().signal, async () => 'late'),
    (error) => error instanceof DOMException && error.name === 'AbortError',
  );
});
