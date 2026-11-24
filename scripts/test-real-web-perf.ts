import assert from 'node:assert/strict';
import test from 'node:test';
import { buildSparklineGeometry } from '../apps/hvac-web/src/components/ui/sparkline-path.ts';
import { selectAssetsRepresentativePoints, type AssetsDeviceRow } from '../apps/hvac-web/src/features/assets/model.ts';
import { resolveAssetsProfile } from '../apps/hvac-web/src/features/assets/catalog.ts';
import { projectAssetsDeviceOperationalState } from '../apps/hvac-web/src/features/assets/operational-projection.ts';
import { createAssetsRealtimeScope, projectAssetsRealtimeRow, type AssetsRealtimeState } from '../apps/hvac-web/src/features/assets/realtime.ts';
import { createTrendsHoverStore } from '../apps/hvac-web/src/features/operations/trends/trends-hover-store.ts';
import { createConcurrencyLimiter } from '../apps/hvac-web/src/lib/concurrency-limiter.ts';
import {
  formatCompactTime,
  formatDecimal,
  formatTime,
  getDateTimeFormatter,
  getNumberFormatter,
} from '../apps/hvac-web/src/lib/operator-format.ts';
import type { Device, TelemetryPoint } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { DeviceObservationSnapshot } from '../apps/hvac-web/src/api/generated/s2Telemetry.gen.ts';

test('buildSparklineGeometry supports monotone, linear, step curves and null gaps', () => {
  const empty = buildSparklineGeometry([]);
  assert.equal(empty.segments.length, 0);
  const allNull = buildSparklineGeometry([null, Number.NaN, null]);
  assert.equal(allNull.segments.length, 0);

  const single = buildSparklineGeometry([42]);
  assert.equal(single.segments.length, 1);
  assert.ok(single.segments[0]?.linePath.startsWith('M '));
  assert.equal(single.segments[0]?.areaPath, '');

  const constant = buildSparklineGeometry([10, 10, 10], 'linear');
  assert.equal(constant.segments.length, 1);
  assert.match(constant.segments[0]?.linePath ?? '', /^M 0 18\.5 L 60 18\.5 L 120 18\.5$/);

  const split = buildSparklineGeometry([10, 20, null, 30, 40], 'monotone');
  assert.equal(split.segments.length, 2);
  assert.ok(split.segments[0]?.areaPath.endsWith(' Z'));
  assert.ok(split.segments[1]?.areaPath.endsWith(' Z'));

  const smooth = buildSparklineGeometry([10, 30, 20, 40], 'monotone');
  assert.equal(smooth.segments.length, 1);
  assert.ok(smooth.segments[0]?.linePath.includes(' C '));

  const stepped = buildSparklineGeometry([10, 30, 20], 'step');
  assert.equal(stepped.segments.length, 1);
  assert.ok(stepped.segments[0]?.linePath.includes(' H '));
  assert.ok(stepped.segments[0]?.linePath.includes(' V '));
});

test('operator-format caches Intl formatters with bounded eviction and guards invalid inputs', () => {
  const n1 = getNumberFormatter(1);
  const n1Again = getNumberFormatter(1);
  assert.equal(n1, n1Again);

  const d1 = getDateTimeFormatter('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
  const d1Again = getDateTimeFormatter('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
  assert.equal(d1, d1Again);

  // Evict oldest when filling past the 64-entry cache cap without evicting newer entries.
  const firstUnique = getDateTimeFormatter('en-US', { timeZone: 'UTC', hour: '2-digit', second: '2-digit' });
  const secondUnique = getDateTimeFormatter('en-US', { timeZone: 'Asia/Shanghai', hour: '2-digit', second: '2-digit' });
  const fillerZones = Intl.supportedValuesOf('timeZone').slice(0, 63);
  assert.equal(fillerZones.length, 63);
  for (const tz of fillerZones) {
    getDateTimeFormatter('en-US', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' });
  }
  const secondAfterFill = getDateTimeFormatter('en-US', { timeZone: 'Asia/Shanghai', hour: '2-digit', second: '2-digit' });
  const firstAfterEvict = getDateTimeFormatter('en-US', { timeZone: 'UTC', hour: '2-digit', second: '2-digit' });
  assert.notEqual(firstAfterEvict, firstUnique);
  assert.equal(secondAfterFill, secondUnique);

  const hot = getDateTimeFormatter('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit' });
  assert.equal(getDateTimeFormatter('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit' }), hot);

  assert.equal(formatDecimal(Number.NaN, 1), '—');
  assert.equal(formatDecimal(1234.56, 1), '1,234.6');
  assert.equal(formatTime('not-a-date', 'Asia/Shanghai'), '—');
  assert.equal(formatCompactTime(undefined, 'Asia/Shanghai'), '—');
  const sameDay = formatCompactTime('2026-10-15T01:05:00.000Z', 'Asia/Shanghai', Date.parse('2026-10-15T08:00:00.000Z'));
  assert.equal(sameDay, '09:05');
  const earlierDay = formatCompactTime('2026-10-14T01:05:00.000Z', 'Asia/Shanghai', Date.parse('2026-10-15T08:00:00.000Z'));
  assert.equal(earlierDay, '10/14 09:05');
});

test('createTrendsHoverStore notifies on changes, deduplicates repeats, normalizes non-finite, and unsubscribes', () => {
  const store = createTrendsHoverStore();
  const seen: (number | null)[] = [];
  const unsubscribe = store.subscribe(() => seen.push(store.getSnapshot()));

  assert.equal(store.getSnapshot(), null);
  store.setHoverAt(null);
  assert.deepEqual(seen, []);

  store.setHoverAt(1_700_000_000_000);
  store.setHoverAt(1_700_000_000_000);
  assert.deepEqual(seen, [1_700_000_000_000]);

  store.setHoverAt(Number.NaN);
  assert.deepEqual(seen, [1_700_000_000_000, null]);

  unsubscribe();
  store.setHoverAt(1_700_000_060_000);
  assert.deepEqual(seen, [1_700_000_000_000, null]);
});

test('createConcurrencyLimiter enforces max concurrency, respects aborts, and releases failed slots', async () => {
  const limiter = createConcurrencyLimiter(4);
  let active = 0;
  let peak = 0;

  const preAborted = new AbortController();
  preAborted.abort();
  let preRan = false;
  await assert.rejects(
    limiter.run(async () => {
      preRan = true;
      return 1;
    }, preAborted.signal),
    { name: 'AbortError' },
  );
  assert.equal(preRan, false);

  const blockers: (() => void)[] = [];
  const held = Array.from({ length: 4 }, (_, index) =>
    limiter.run(
      () =>
        new Promise<number>((resolve) => {
          active += 1;
          peak = Math.max(peak, active);
          blockers.push(() => {
            active -= 1;
            resolve(index);
          });
        }),
    ),
  );

  // Queue an item then abort it while queued; its task must never execute.
  const queuedController = new AbortController();
  let queuedRan = false;
  const abortedWhileQueued = limiter.run(async () => {
    queuedRan = true;
    return 99;
  }, queuedController.signal);
  queuedController.abort();
  await assert.rejects(abortedWhileQueued, { name: 'AbortError' });
  assert.equal(queuedRan, false);

  const extra = Array.from({ length: 6 }, (_, index) =>
    limiter.run(async () => {
      active += 1;
      peak = Math.max(peak, active);
      await Promise.resolve();
      if (index === 0) throw new Error('boom');
      active -= 1;
      return index;
    }).catch((err: unknown) => {
      active -= 1;
      throw err;
    }),
  );

  for (const release of blockers) release();
  const heldResults = await Promise.all(held);
  assert.deepEqual(heldResults, [0, 1, 2, 3]);

  const settled = await Promise.allSettled(extra);
  assert.equal(settled[0]?.status, 'rejected');
  assert.deepEqual(
    settled.slice(1).map((item) => (item.status === 'fulfilled' ? item.value : -1)),
    [1, 2, 3, 4, 5],
  );
  assert.ok(peak <= 4, `peak concurrency was ${peak}`);
});

test('projectAssetsRealtimeRow reuses the realtime snapshot reference without deep cloning', () => {
  const tenantId = '01900000-0001-7000-8000-000000000001';
  const siteId = '01900000-0002-7000-8000-000000000002';
  const deviceId = '01900000-0011-7000-8000-000000000011';
  const device: Device = {
    id: deviceId,
    tenantId,
    siteId,
    code: 'CH-01',
    displayName: 'Chiller 01',
    deviceType: 'CHILLER',
    status: 'ACTIVE',
    revision: 5,
    createdAt: '2026-07-31T00:00:00.000Z',
    updatedAt: '2026-07-31T00:00:00.000Z',
  };
  const telemetryPoints: readonly TelemetryPoint[] = [
    {
      id: '01900000-0013-7000-8000-000000000010',
      tenantId,
      siteId,
      reportingDeviceId: deviceId,
      sensorId: null,
      pointCode: 'chiller_power',
      sourceKey: 'chiller_power',
      displayName: 'chiller_power',
      pointType: 'TELEMETRY',
      valueType: 'NUMBER',
      unit: 'kW',
      writable: false,
      sampleIntervalMs: 1000,
      publishIntervalMs: 1000,
      staleAfterMs: 5000,
      counterDecreaseMode: null,
      counterRolloverModulus: null,
      sourceMetadata: {},
      status: 'ACTIVE',
      revision: 1,
      createdAt: '2026-07-31T00:00:00.000Z',
      updatedAt: '2026-07-31T00:00:00.000Z',
    },
  ];
  const makeSnap = (revision: number): DeviceObservationSnapshot => ({
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
    telemetryReadiness: 'CURRENT',
    displayState: 'ONLINE',
    values: [
      {
        key: 'chiller_power',
        state: 'PRESENT',
        value: 18.5,
        valueType: 'NUMBER',
        unit: 'kW',
        quality: 'GOOD',
        qualityReasons: [],
        sampledAt: `2026-07-31T04:0${revision}:00.000Z`,
        receivedAt: `2026-07-31T04:0${revision}:01.000Z`,
        freshness: 'FRESH',
        policyRevision: revision,
      },
    ],
  });

  const profile = resolveAssetsProfile('CHILLER');
  const baselineResult = { status: 'ok', snapshot: makeSnap(2) } as const;
  const operational = projectAssetsDeviceOperationalState({
    device,
    telemetryPoints,
    snapshotResult: baselineResult,
  });
  const baseRow: AssetsDeviceRow = {
    device,
    profile,
    binding: { state: 'unbound' },
    space: { state: 'unbound' },
    registeredPointCount: telemetryPoints.length,
    telemetryPoints,
    snapshotResult: baselineResult,
    operational,
    representativePoints: selectAssetsRepresentativePoints(telemetryPoints, profile, operational.points),
  };
  const scope = createAssetsRealtimeScope(baseRow, 1);
  const liveSnapshot = makeSnap(3);
  const liveState: AssetsRealtimeState = {
    status: 'live',
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId,
    keys: [...scope.keys],
    snapshot: liveSnapshot,
    recovered: false,
    updatedAt: '2026-07-31T04:03:03.000Z',
  };

  const projection = projectAssetsRealtimeRow(baseRow, liveState);
  assert.equal(projection.source, 'realtime');
  assert.equal(projection.row.snapshotResult?.status, 'ok');
  if (projection.row.snapshotResult?.status === 'ok') {
    assert.equal(projection.row.snapshotResult.snapshot, liveSnapshot);
  }
});
