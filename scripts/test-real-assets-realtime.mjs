import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveRealAssetsProfile } from '../apps/hvac-web/src/real/assets/catalog.ts';
import { createBoundedRealtimePublisher } from '../apps/hvac-web/src/real/assets/realtime-publisher.ts';
import {
  createRealAssetsRealtimeScope,
  createRealAssetsRealtimeTarget,
  describeRealAssetsRealtimeState,
  listRealAssetsRealtimeKeys,
  projectRealAssetsRealtimeRow,
  validateRealAssetsRealtimeState,
} from '../apps/hvac-web/src/real/assets/realtime.ts';
import { projectRealAssetsOperatingState } from '../apps/hvac-web/src/real/assets/model.ts';

const organizationId = '01900000-0001-7000-8000-000000000001';
const siteId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0011-7000-8000-000000000011';
const otherDeviceId = '01900000-0012-7000-8000-000000000012';

function values(revision, power = 0) {
  return [
    {
      key: 'chiller.run_state', state: 'PRESENT', value: 'RUNNING', valueType: 'STRING', unit: null,
      sampledAt: `2026-07-31T04:0${revision}:00.000Z`, receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
      freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: revision,
    },
    {
      key: 'chiller.power', state: 'PRESENT', value: power, valueType: 'NUMBER', unit: 'kW',
      sampledAt: `2026-07-31T04:0${revision}:00.000Z`, receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
      freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: revision,
    },
    {
      key: 'chiller.cop', state: 'PRESENT', value: 4.8, valueType: 'NUMBER', unit: null,
      sampledAt: `2026-07-31T04:0${revision}:00.000Z`, receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
      freshness: 'FRESH', quality: revision > 2 ? 'SUSPECT' : 'GOOD',
      qualityReasons: revision > 2 ? ['SOURCE_LAG_EXCEEDED'] : [], policyRevision: revision,
    },
    {
      key: 'chiller.cooling_capacity', state: 'PRESENT', value: 520, valueType: 'NUMBER', unit: 'kW',
      sampledAt: `2026-07-31T04:0${revision}:00.000Z`, receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
      freshness: 'FRESH', quality: 'GOOD', qualityReasons: [], policyRevision: revision,
    },
  ];
}

function snapshot(revision, overrides = {}) {
  return {
    schemaVersion: 1,
    deviceId,
    owningOrganizationId: organizationId,
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

function row(revision = 2) {
  const profile = resolveRealAssetsProfile('CHILLER');
  const snapshotResult = { status: 'ok', snapshot: snapshot(revision) };
  const projection = projectRealAssetsOperatingState(snapshotResult, profile);
  return {
    device: {
      id: deviceId, owningOrganizationId: organizationId, siteId, code: 'CH-01', displayName: 'Chiller 01',
      deviceType: 'CHILLER', status: 'ACTIVE', revision: 5,
    },
    profile,
    binding: { state: 'unbound' },
    snapshotResult,
    operatingState: projection.state,
    attentionReasons: projection.reasons,
    points: projection.points,
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

test('realtime scope selects only versioned critical detail keys and one exact Device target', () => {
  const visible = row();
  assert.deepEqual(listRealAssetsRealtimeKeys(visible), [
    'chiller.run_state', 'chiller.power', 'chiller.cop', 'chiller.cooling_capacity',
  ]);
  const scope = createRealAssetsRealtimeScope(visible, 7);
  assert.equal(scope.clientSubscriptionId, `real-assets-detail:7:${deviceId}`);
  assert.deepEqual(createRealAssetsRealtimeTarget(scope), {
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId,
    keys: [...scope.keys],
  });
  assert.deepEqual(listRealAssetsRealtimeKeys({ profile: resolveRealAssetsProfile('UNKNOWN_DEVICE') }), []);
});

test('realtime state validation rejects Device, owner, Site and exact-key drift', () => {
  const visible = row();
  const scope = createRealAssetsRealtimeScope(visible, 7);
  const state = liveState('live');
  assert.equal(validateRealAssetsRealtimeState(state, scope), state);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, deviceId: otherDeviceId }, scope), /exact subscription scope/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, keys: [...state.keys].reverse() }, scope), /exact subscription scope/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, snapshot: snapshot(3, { siteId: otherDeviceId }) }, scope), /authorized Device/);
  assert.throws(() => validateRealAssetsRealtimeState({ ...state, snapshot: snapshot(3, { owningOrganizationId: otherDeviceId }) }, scope), /authorized Device/);
});

test('newer realtime Snapshot reprojects detail while valid zero and suspect quality remain explicit', () => {
  const projection = projectRealAssetsRealtimeRow(row(2), liveState('live', 3));
  assert.equal(projection.source, 'realtime');
  assert.equal(projection.realtimeRevision, 3);
  assert.equal(projection.row.snapshotResult.snapshot.businessRevision, 3);
  assert.equal(projection.row.points.find((point) => point.key === 'chiller.cop').quality, 'SUSPECT');
  const zeroProjection = projectRealAssetsRealtimeRow(row(1), liveState('live', 2));
  assert.equal(zeroProjection.row.points.find((point) => point.key === 'chiller.power').displayValue, '0');
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
  assert.equal(projection.row.operatingState, 'UNKNOWN');
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
