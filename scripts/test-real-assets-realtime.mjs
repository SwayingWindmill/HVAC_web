import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveRealAssetsProfile } from '../apps/hvac-web/src/real/assets/catalog.ts';
import { createBoundedRealtimePublisher } from '../apps/hvac-web/src/real/assets/realtime-publisher.ts';
import {
  createRealAssetsRealtimeScope,
  createRealAssetsRealtimeTarget,
  describeRealAssetsRealtimeState,
  listRealAssetsRealtimeKeys,
  realAssetsRealtimeSubscriptionEligibility,
  projectRealAssetsRealtimeRow,
  validateRealAssetsRealtimeState,
} from '../apps/hvac-web/src/real/assets/realtime.ts';
import { projectRealAssetsDeviceOperationalState } from '../apps/hvac-web/src/real/assets/operational-projection.ts';

const tenantId = '01900000-0001-7000-8000-000000000001';
const siteId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0011-7000-8000-000000000011';
const otherDeviceId = '01900000-0012-7000-8000-000000000012';

function values(revision, power = 0) {
  const timing = {
    sampledAt: `2026-07-31T04:0${revision}:00.000Z`,
    receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
    freshness: 'FRESH',
    policyRevision: revision,
  };
  return [
    { key: 'chiller_cooling_capacity', state: 'PRESENT', value: 520, valueType: 'NUMBER', unit: 'kW', quality: 'GOOD', qualityReasons: [], ...timing },
    {
      key: 'chiller_cop', state: 'PRESENT', value: 4.8, valueType: 'NUMBER', unit: null,
      quality: revision > 2 ? 'PARTIAL' : 'GOOD', qualityReasons: revision > 2 ? ['SOURCE_LAG_EXCEEDED'] : [], ...timing,
    },
    { key: 'chiller_power', state: 'PRESENT', value: power, valueType: 'NUMBER', unit: 'kW', quality: 'GOOD', qualityReasons: [], ...timing },
    { key: 'chiller_run_state', state: 'PRESENT', value: 'RUNNING', valueType: 'STRING', unit: null, quality: 'GOOD', qualityReasons: [], ...timing },
  ];
}

function snapshot(revision, overrides = {}) {
  return {
    schemaVersion: 1,
    deviceId,
    tenantId: tenantId,
    siteId,
    businessRevision: revision,
    evaluatedAt: `2026-07-31T04:0${revision}:02.000Z`,
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: `2026-07-31T04:0${revision}:00.000Z`,
      policyRevision: revision, lastKnown: null,
    },
    telemetryReadiness: revision > 2 ? 'DEGRADED' : 'CURRENT',
    displayState: revision > 2 ? 'STALE' : 'ONLINE',
    values: values(revision, revision === 2 ? 0 : 18.5),
    ...overrides,
  };
}

function registryPoint(pointCode, index) {
  return {
    id: `01900000-0013-7000-8000-00000000001${index}`,
    tenantId,
    siteId,
    reportingDeviceId: deviceId,
    sensorId: null,
    pointCode,
    sourceKey: pointCode,
    displayName: pointCode,
    pointType: 'TELEMETRY',
    valueType: 'NUMBER',
    unit: null,
    writable: false,
    sampleIntervalMs: 1000,
    publishIntervalMs: 1000,
    staleAfterMs: 5000,
    sourceMetadata: {},
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-31T00:00:00.000Z',
    updatedAt: '2026-07-31T00:00:00.000Z',
  };
}

function row(revision = 2, deviceType = 'CHILLER') {
  const profile = resolveRealAssetsProfile(deviceType);
  const device = {
    id: deviceId, tenantId: tenantId, siteId, code: 'CH-01', displayName: 'Chiller 01',
    deviceType, status: 'ACTIVE', revision: 5,
  };
  const telemetryPoints = values(revision).map((value, index) => registryPoint(value.key, index));
  const snapshotResult = { status: 'ok', snapshot: snapshot(revision) };
  return {
    device,
    profile,
    binding: { state: 'unbound' },
    space: { state: 'unbound' },
    registeredPointCount: telemetryPoints.length,
    telemetryPoints,
    snapshotResult,
    operational: projectRealAssetsDeviceOperationalState({ device, telemetryPoints, snapshotResult, profile }),
  };
}

function liveState(status, revision = 3, overrides = {}) {
  const scope = createRealAssetsRealtimeScope(row(), 7);
  const base = {
    status,
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId,
    keys: [...scope.keys],
    updatedAt: '2026-07-31T04:03:03.000Z',
    snapshot: status === 'initializing' || status === 'revoked' ? null : snapshot(revision),
  };
  if (status === 'live') Object.assign(base, { recovered: false });
  if (status === 'snapshot') Object.assign(base, { reason: 'reconnecting' });
  if (status === 'unavailable') Object.assign(base, { reason: 'transport-unavailable', retryable: true });
  return { ...base, ...overrides };
}

test('realtime scope follows active Registry Points rather than frontend profile configuration', () => {
  const visible = row();
  assert.deepEqual(listRealAssetsRealtimeKeys(visible), [
    'chiller_cooling_capacity', 'chiller_cop', 'chiller_power', 'chiller_run_state',
  ]);
  const scope = createRealAssetsRealtimeScope(visible, 7);
  assert.equal(scope.clientSubscriptionId, `real-assets-detail:7:${deviceId}`);
  assert.deepEqual(createRealAssetsRealtimeTarget(scope), {
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId,
    keys: [...scope.keys],
  });
  const unprofiled = row(2, 'VENDOR_SPECIAL_CONTROLLER');
  assert.equal(unprofiled.profile.state, 'unconfigured');
  assert.deepEqual(listRealAssetsRealtimeKeys(unprofiled), [...scope.keys]);
});

test('realtime scope reports the public key limit before the hook can throw during render', () => {
  const visible = row();
  const crowded = {
    ...visible,
    telemetryPoints: Array.from({ length: 65 }, (_, index) => ({
      ...visible.telemetryPoints[index % visible.telemetryPoints.length],
      id: `crowded-point-${index}`,
      pointCode: `vendor.point_${String(index).padStart(2, '0')}`,
    })),
  };
  assert.deepEqual(realAssetsRealtimeSubscriptionEligibility(crowded), {
    state: 'too-many-points',
    pointCount: 65,
    limit: 64,
  });
});

test('realtime state validation rejects Tenant, Site, Device and exact-key drift', () => {
  const visible = row();
  const scope = createRealAssetsRealtimeScope(visible, 7);
  const state = liveState('live');
  assert.equal(validateRealAssetsRealtimeState(state, scope), state);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, deviceId: otherDeviceId }, scope), /exact subscription scope/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, keys: [...state.keys].reverse() }, scope), /exact subscription scope/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, snapshot: snapshot(3, { siteId: otherDeviceId }) }, scope), /authorized Tenant/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, snapshot: snapshot(3, { tenantId: otherDeviceId }) }, scope), /authorized Tenant/);
});

test('newer realtime Snapshot reprojects detail while valid zero and degraded quality remain explicit', () => {
  const projection = projectRealAssetsRealtimeRow(row(2), liveState('live', 3));
  assert.equal(projection.source, 'realtime');
  assert.equal(projection.realtimeRevision, 3);
  assert.equal(projection.row.snapshotResult.snapshot.businessRevision, 3);
  assert.equal(projection.row.operational.points.find((point) => point.key === 'chiller_cop').quality, 'PARTIAL');
  assert.equal(projection.row.operational.telemetry.quality, 'DEGRADED');
  const zeroProjection = projectRealAssetsRealtimeRow(row(1), liveState('live', 2));
  assert.equal(zeroProjection.row.operational.points.find((point) => point.key === 'chiller_power').displayValue, '0');
});

test('older realtime Snapshot never overwrites a newer current-query baseline', () => {
  const projection = projectRealAssetsRealtimeRow(row(3), liveState('live', 2));
  assert.equal(projection.source, 'current-query');
  assert.equal(projection.realtimeOlderThanBaseline, true);
  assert.equal(projection.row.snapshotResult.snapshot.businessRevision, 3);
});

test('revocation suppresses previously authorized detail Snapshot', () => {
  const projection = projectRealAssetsRealtimeRow(row(3), liveState('revoked'));
  assert.equal(projection.source, 'none');
  assert.equal(projection.suppressedByRevocation, true);
  assert.equal(projection.row.snapshotResult, undefined);
  assert.equal(projection.row.operational.connection.state, 'UNAVAILABLE');
  assert.equal(projection.row.operational.telemetry.freshness, 'UNAVAILABLE');
  assert.ok(projection.row.operational.points.every((point) => point.state === 'UNAVAILABLE'));
});

test('bounded publisher renders at most once per frame while publishing the latest fully-applied state', () => {
  const callbacks = new Map();
  let nextHandle = 1;
  const published = [];
  const publisher = createBoundedRealtimePublisher(
    (callback) => { const handle = nextHandle++; callbacks.set(handle, callback); return handle; },
    (handle) => callbacks.delete(handle),
    (value) => published.push(value),
  );
  publisher.push(1);
  publisher.push(2);
  publisher.push(3);
  assert.equal(callbacks.size, 1);
  assert.equal(publisher.pending(), true);
  const [scheduledHandle, scheduledCallback] = callbacks.entries().next().value;
  callbacks.delete(scheduledHandle);
  scheduledCallback();
  assert.deepEqual(published, [3]);
  assert.equal(publisher.pending(), false);
  publisher.push(4);
  publisher.cancel();
  assert.equal(callbacks.size, 0);
  assert.deepEqual(published, [3]);
});

test('transport presentation keeps Snapshot degradation separate from revocation', () => {
  assert.equal(describeRealAssetsRealtimeState(liveState('live')).degraded, false);
  assert.match(describeRealAssetsRealtimeState(liveState('snapshot')).label, /重连/);
  assert.equal(describeRealAssetsRealtimeState(liveState('unavailable')).retryable, true);
  assert.match(describeRealAssetsRealtimeState(liveState('revoked')).detail, /晚到 event/);
});
