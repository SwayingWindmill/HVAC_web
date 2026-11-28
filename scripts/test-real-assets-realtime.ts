import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveAssetsProfile } from '../apps/hvac-web/src/features/assets/catalog.ts';
import { createBoundedRealtimePublisher } from '../apps/hvac-web/src/features/assets/realtime-publisher.ts';
import {
  createAssetsRealtimeScope,
  createAssetsRealtimeTarget,
  listAssetsRealtimeKeys,
  assetsRealtimeSubscriptionEligibility,
  projectAssetsRealtimeRow,
  validateAssetsRealtimeState,
} from '../apps/hvac-web/src/features/assets/realtime.ts';
import { siteDevicesRealtimeShellState } from '../apps/hvac-web/src/features/assets/realtime-shell-status.ts';
import { selectAssetsRepresentativePoints } from '../apps/hvac-web/src/features/assets/model.ts';
import { projectAssetsDeviceOperationalState } from '../apps/hvac-web/src/features/assets/operational-projection.ts';

type OperationalInput = Parameters<typeof projectAssetsDeviceOperationalState>[0];
type AssetsRow = Parameters<typeof createAssetsRealtimeScope>[0];
type RealtimeState = NonNullable<Parameters<typeof projectAssetsRealtimeRow>[1]>;
type RealtimeSnapshot = NonNullable<RealtimeState['snapshot']>;
type ShellPhase = Parameters<typeof siteDevicesRealtimeShellState>[0];
type ShellStates = Parameters<typeof siteDevicesRealtimeShellState>[1];

const tenantId = '01900000-0001-7000-8000-000000000001';
const siteId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0011-7000-8000-000000000011';
const otherDeviceId = '01900000-0012-7000-8000-000000000012';

function values(revision: number, power = 0) {
  const timing = {
    sampledAt: `2026-07-31T04:0${revision}:00.000Z`,
    receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
    freshness: 'FRESH' as const,
    policyRevision: revision,
  };
  return [
    { key: 'chiller_cooling_capacity', state: 'PRESENT' as const, value: 520, valueType: 'NUMBER' as const, unit: 'kW', quality: 'GOOD' as const, qualityReasons: [] as string[], ...timing },
    {
      key: 'chiller_cop',
      state: 'PRESENT' as const,
      value: 4.8,
      valueType: 'NUMBER' as const,
      unit: null,
      quality: (revision > 2 ? 'PARTIAL' : 'GOOD') as 'PARTIAL' | 'GOOD',
      qualityReasons: revision > 2 ? ['SOURCE_LAG_EXCEEDED'] : [],
      ...timing,
    },
    { key: 'chiller_power', state: 'PRESENT' as const, value: power, valueType: 'NUMBER' as const, unit: 'kW', quality: 'GOOD' as const, qualityReasons: [] as string[], ...timing },
    { key: 'chiller_run_state', state: 'PRESENT' as const, value: 'RUNNING', valueType: 'STRING' as const, unit: null, quality: 'GOOD' as const, qualityReasons: [] as string[], ...timing },
  ];
}

function snapshot(revision: number, overrides: Record<string, unknown> = {}): RealtimeSnapshot {
  return {
    schemaVersion: 1,
    deviceId,
    tenantId,
    siteId,
    businessRevision: revision,
    evaluatedAt: `2026-07-31T04:0${revision}:02.000Z`,
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: `2026-07-31T04:0${revision}:00.000Z`,
      policyRevision: revision,
      lastKnown: null,
    },
    telemetryReadiness: revision > 2 ? 'DEGRADED' : 'CURRENT',
    displayState: revision > 2 ? 'STALE' : 'ONLINE',
    values: values(revision, revision === 2 ? 0 : 18.5),
    ...overrides,
  } as RealtimeSnapshot;
}

function registryPoint(pointCode: string, index: number): OperationalInput['telemetryPoints'][number] {
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
  } as OperationalInput['telemetryPoints'][number];
}

function row(revision = 2, deviceType = 'CHILLER'): AssetsRow {
  const profile = resolveAssetsProfile(deviceType);
  const device = {
    id: deviceId,
    tenantId,
    siteId,
    code: 'CH-01',
    displayName: 'Chiller 01',
    deviceType,
    status: 'ACTIVE',
    revision: 5,
  } as OperationalInput['device'];
  const telemetryPoints = values(revision).map((value, index) => registryPoint(value.key, index));
  const snapshotResult = { status: 'ok', snapshot: snapshot(revision) } as OperationalInput['snapshotResult'];
  const operational = projectAssetsDeviceOperationalState({ device, telemetryPoints, snapshotResult });
  return {
    device,
    profile,
    binding: { state: 'unbound' },
    space: { state: 'unbound' },
    registeredPointCount: telemetryPoints.length,
    telemetryPoints,
    snapshotResult,
    operational,
    representativePoints: selectAssetsRepresentativePoints(telemetryPoints, profile, operational.points),
  } as AssetsRow;
}

function liveState(status: RealtimeState['status'], revision = 3, overrides: Record<string, unknown> = {}): RealtimeState {
  const scope = createAssetsRealtimeScope(row(), 7);
  const base: Record<string, unknown> = {
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
  return { ...base, ...overrides } as unknown as RealtimeState;
}

test('realtime scope follows active Registry Points rather than frontend profile configuration', () => {
  const visible = row();
  assert.deepEqual(listAssetsRealtimeKeys(visible), [
    'chiller_cooling_capacity', 'chiller_cop', 'chiller_power', 'chiller_run_state',
  ]);
  const scope = createAssetsRealtimeScope(visible, 7);
  assert.equal(scope.clientSubscriptionId, `real-assets-detail:7:${deviceId}`);
  assert.deepEqual(createAssetsRealtimeTarget(scope), {
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId,
    keys: [...scope.keys],
  });
  const unprofiled = row(2, 'VENDOR_SPECIAL_CONTROLLER');
  assert.equal(unprofiled.profile.state, 'unconfigured');
  assert.deepEqual(listAssetsRealtimeKeys(unprofiled), [...scope.keys]);
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
  } as AssetsRow;
  assert.deepEqual(assetsRealtimeSubscriptionEligibility(crowded), {
    state: 'too-many-points',
    pointCount: 65,
    limit: 64,
  });
});

test('realtime state validation rejects Tenant, Site, Device and exact-key drift', () => {
  const visible = row();
  const scope = createAssetsRealtimeScope(visible, 7);
  const state = liveState('live');
  assert.equal(validateAssetsRealtimeState(state, scope), state);
  assert.throws(() => validateAssetsRealtimeState({ ...state, deviceId: otherDeviceId } as RealtimeState, scope), /exact subscription scope/);
  assert.throws(() => validateAssetsRealtimeState({ ...state, keys: [...state.keys].reverse() } as RealtimeState, scope), /exact subscription scope/);
  assert.throws(() => validateAssetsRealtimeState({ ...state, snapshot: snapshot(3, { siteId: otherDeviceId }) } as RealtimeState, scope), /authorized Tenant/);
  assert.throws(() => validateAssetsRealtimeState({ ...state, snapshot: snapshot(3, { tenantId: otherDeviceId }) } as RealtimeState, scope), /authorized Tenant/);
});

test('newer realtime Snapshot reprojects detail while valid zero and degraded quality remain explicit', () => {
  const projection = projectAssetsRealtimeRow(row(2), liveState('live', 3));
  assert.equal(projection.source, 'realtime');
  assert.equal(projection.realtimeRevision, 3);
  assert.equal((projection.row.snapshotResult as { status: 'ok'; snapshot: RealtimeSnapshot } | undefined)?.snapshot.businessRevision, 3);
  assert.equal(projection.row.operational.points.find((point) => point.key === 'chiller_cop')?.quality, 'PARTIAL');
  assert.equal(projection.row.operational.telemetry.quality, 'DEGRADED');
  const zeroProjection = projectAssetsRealtimeRow(row(1), liveState('live', 2));
  assert.equal(zeroProjection.row.operational.points.find((point) => point.key === 'chiller_power')?.displayValue, '0');
});

test('older realtime Snapshot never overwrites a newer current-query baseline', () => {
  const projection = projectAssetsRealtimeRow(row(3), liveState('live', 2));
  assert.equal(projection.source, 'current-query');
  assert.equal(projection.realtimeOlderThanBaseline, true);
  assert.equal((projection.row.snapshotResult as { status: 'ok'; snapshot: RealtimeSnapshot } | undefined)?.snapshot.businessRevision, 3);
});

test('revocation suppresses previously authorized detail Snapshot', () => {
  const projection = projectAssetsRealtimeRow(row(3), liveState('revoked'));
  assert.equal(projection.source, 'none');
  assert.equal(projection.suppressedByRevocation, true);
  assert.equal(projection.row.snapshotResult, undefined);
  assert.equal(projection.row.operational.connection.state, 'UNAVAILABLE');
  assert.equal(projection.row.operational.telemetry.freshness, 'UNAVAILABLE');
  assert.ok(projection.row.operational.points.every((point) => point.state === 'UNAVAILABLE'));
});

test('bounded publisher renders at most once per frame while publishing the latest fully-applied state', () => {
  const callbacks = new Map<number, () => void>();
  let nextHandle = 1;
  const published: number[] = [];
  const publisher = createBoundedRealtimePublisher<number>(
    (callback) => {
      const handle = nextHandle++;
      callbacks.set(handle, callback);
      return handle;
    },
    (handle) => {
      callbacks.delete(handle);
    },
    (value) => published.push(value),
  );
  publisher.push(1);
  publisher.push(2);
  publisher.push(3);
  assert.equal(callbacks.size, 1);
  assert.equal(publisher.pending(), true);
  const firstEntry = callbacks.entries().next().value;
  assert.ok(firstEntry);
  const [scheduledHandle, scheduledCallback] = firstEntry;
  callbacks.delete(scheduledHandle);
  scheduledCallback();
  assert.deepEqual(published, [3]);
  assert.equal(publisher.pending(), false);
  publisher.push(4);
  publisher.cancel();
  assert.equal(callbacks.size, 0);
  assert.deepEqual(published, [3]);
});

test('the header state follows the page session and shows its most severe Device', () => {
  const none = new Map() as ShellStates;
  for (const phase of ['closed', 'not-authorized', 'scope-too-large', 'purged'] as ShellPhase[]) {
    assert.equal(siteDevicesRealtimeShellState(phase, none), 'idle', phase);
  }
  assert.equal(siteDevicesRealtimeShellState('opening', none), 'connecting');
  assert.equal(siteDevicesRealtimeShellState('error', none), 'unavailable');
  assert.equal(siteDevicesRealtimeShellState('active', none), 'connecting');

  const states = (...list: RealtimeState[]): ShellStates =>
    new Map(list.map((state, index) => [`device-${index}`, state])) as ShellStates;
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('live'))), 'live');
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('initializing'))), 'connecting');
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('snapshot', 3, { reason: 'authoritative-snapshot' }))), 'connecting');
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('snapshot'))), 'reconnecting');
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('snapshot', 3, { reason: 'recovering' }))), 'resync-required');
  assert.equal(
    siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('unavailable', 3, { reason: 'recovery-required' }))),
    'resync-required',
  );
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('snapshot'), liveState('unavailable'))), 'unavailable');
  // A revoked Device is being purged and no longer counts; only revoked Devices means no subscription.
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('live'), liveState('revoked'))), 'live');
  assert.equal(siteDevicesRealtimeShellState('active', states(liveState('revoked'))), 'idle');
});
