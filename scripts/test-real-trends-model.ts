import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import type { SiteAssetModel } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { DeviceHistoryAggregateBucket } from '../apps/hvac-web/src/api/generated/s2Telemetry.gen.ts';
import {
  buildTimeSlots,
  fillGaugeSlots,
  fillScalarSlots,
} from '../apps/hvac-web/src/features/operations/trends/timeline.ts';
import {
  MAX_TREND_POINTS,
  buildTrendCatalog,
  buildTrendViewModel,
  deriveBuiltInPresets,
  groupTrendDeviceRequests,
  resolveTrackStateAt,
  resolveTrendWindow,
} from '../apps/hvac-web/src/features/operations/trends/trends-model.ts';

const centralPlantModel = JSON.parse(
  readFileSync(new URL('../apps/hvac-web/e2e/fixtures/asset-model.json', import.meta.url), 'utf8'),
) as SiteAssetModel;

test('buildTimeSlots generates contiguous slots for MINUTE, FIVE_MINUTE, QUARTER_HOUR, HOUR and site-local DAY', () => {
  const minuteSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:02:00Z'),
    toMs: Date.parse('2026-10-11T01:05:00Z'),
    granularity: 'MINUTE',
    timeZone: 'Asia/Shanghai',
  });
  assert.deepEqual(
    minuteSlots.map((slot) => new Date(slot.startMs).toISOString()),
    [
      '2026-10-11T01:02:00.000Z',
      '2026-10-11T01:03:00.000Z',
      '2026-10-11T01:04:00.000Z',
    ],
  );

  const fiveMinSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:00:00Z'),
    toMs: Date.parse('2026-10-11T01:15:00Z'),
    granularity: 'FIVE_MINUTE',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(fiveMinSlots.length, 3);
  assert.equal(fiveMinSlots[1].startMs - fiveMinSlots[0].startMs, 5 * 60_000);

  const quarterSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:00:00Z'),
    toMs: Date.parse('2026-10-11T02:00:00Z'),
    granularity: 'QUARTER_HOUR',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(quarterSlots.length, 4);

  const hourSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T00:00:00Z'),
    toMs: Date.parse('2026-10-11T03:00:00Z'),
    granularity: 'HOUR',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(hourSlots.length, 3);

  // Asia/Shanghai midnight is 16:00Z of the previous UTC day.
  const daySlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-09T16:00:00Z'),
    toMs: Date.parse('2026-10-11T16:00:00Z'),
    granularity: 'DAY',
    timeZone: 'Asia/Shanghai',
  });
  assert.deepEqual(
    daySlots.map((slot) => [new Date(slot.startMs).toISOString(), new Date(slot.endMs).toISOString()]),
    [
      ['2026-10-09T16:00:00.000Z', '2026-10-10T16:00:00.000Z'],
      ['2026-10-10T16:00:00.000Z', '2026-10-11T16:00:00.000Z'],
    ],
  );
});

test('fillGaugeSlots and fillScalarSlots leave null in missing outage buckets instead of bridging or zero-filling', () => {
  const slots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T10:00:00Z'),
    toMs: Date.parse('2026-10-11T14:00:00Z'),
    granularity: 'HOUR',
    timeZone: 'Asia/Shanghai',
  });

  const gauge = fillGaugeSlots(slots, [
    { periodStart: '2026-10-11T10:00:00Z', average: 182.4, minimum: 170.0, maximum: 195.2 },
    { periodStart: '2026-10-11T13:00:00Z', average: 190.1, minimum: 185.0, maximum: 198.0 },
  ]);

  assert.deepEqual(
    gauge.map((row) => ({
      at: new Date(row.startMs).toISOString(),
      average: row.average,
      range: row.minimum !== null && row.maximum !== null ? [row.minimum, row.maximum] : null,
    })),
    [
      { at: '2026-10-11T10:00:00.000Z', average: 182.4, range: [170.0, 195.2] },
      { at: '2026-10-11T11:00:00.000Z', average: null, range: null },
      { at: '2026-10-11T12:00:00.000Z', average: null, range: null },
      { at: '2026-10-11T13:00:00.000Z', average: 190.1, range: [185.0, 198.0] },
    ],
  );

  const scalar = fillScalarSlots(slots, [
    { periodStart: '2026-10-11T10:00:00Z', value: 4.85 },
    { periodStart: '2026-10-11T13:00:00Z', value: 5.12 },
  ]);
  assert.deepEqual(scalar.map((row) => row.value), [4.85, null, null, 5.12]);
});

test('deriveBuiltInPresets adapts to Central Plant and Songyaxuan active points and flags unconnected references', () => {
  const centralCatalog = buildTrendCatalog(centralPlantModel);
  const centralPresets = deriveBuiltInPresets(centralCatalog);
  assert.equal(centralPresets.length, 4);
  assert.equal(centralPresets[0]?.id, 'plant-overview');
  assert.equal(centralPresets[0]?.title, '冷站功率与供回水');
  for (const preset of centralPresets) {
    assert.ok(preset.pointRefs.length > 0 && preset.pointRefs.length <= MAX_TREND_POINTS);
  }
  const allLabels = centralCatalog.points.map((item) => item.pointLabel);
  assert.ok(!allLabels.includes('chiller leaving chilled water temperature'));
  assert.ok(allLabels.includes('冷冻水出水温度'));

  const songyaxuanModel: SiteAssetModel = {
    ...centralPlantModel,
    telemetryPoints: centralPlantModel.telemetryPoints
      .filter((point) => point.sourceKey !== 'chwp.frequency' && point.sourceKey !== 'cwp.frequency')
      .map((point) => {
        if (point.sourceKey === 'weather.ambient_wet_bulb_temperature') {
          return { ...point, status: 'INACTIVE' as const };
        }
        if (point.sourceKey === 'chiller.run_state' || point.sourceKey === 'chwp.run_state') {
          return { ...point, pointType: 'TELEMETRY' as const, valueType: 'STRING' as const };
        }
        return point;
      }),
  };
  const songyaxuanCatalog = buildTrendCatalog(songyaxuanModel);
  const songyaxuanPresets = deriveBuiltInPresets(songyaxuanCatalog);
  const outdoorPreset = songyaxuanPresets.find((preset) => preset.id === 'outdoor-load');
  assert.ok(outdoorPreset);
  assert.ok(outdoorPreset.unconnectedLabels.includes('室外湿球温度'));
  const pumpPreset = songyaxuanPresets.find((preset) => preset.id === 'pump-hydraulics');
  assert.ok(pumpPreset);
  assert.ok(pumpPreset.unconnectedLabels.includes('水泵频率'));

  const weatherOnlyModel: SiteAssetModel = {
    ...centralPlantModel,
    telemetryPoints: centralPlantModel.telemetryPoints.filter(
      (point) => point.sourceKey === 'weather.ambient_dry_bulb_temperature',
    ),
  };
  const weatherPresets = deriveBuiltInPresets(buildTrendCatalog(weatherOnlyModel));
  assert.deepEqual(weatherPresets.map((preset) => preset.id), ['outdoor-load']);
});

test('resolveTrendWindow selects auto granularities and clamps illegal >720-bucket manual overrides', () => {
  const timezone = 'Asia/Shanghai';
  const earlyMorning = new Date('2026-10-11T17:30:00.000Z');
  const oneHour = resolveTrendWindow({ window: '1h', now: earlyMorning, timezone });
  assert.equal(oneHour.granularity, 'MINUTE');
  assert.equal(oneHour.slots.length, 60);
  assert.equal(oneHour.compareLabel, '对比昨日同时段');

  const sixHour = resolveTrendWindow({ window: '6h', now: earlyMorning, timezone });
  assert.equal(sixHour.granularity, 'FIVE_MINUTE');
  assert.equal(sixHour.slots.length, 72);

  const todayEarly = resolveTrendWindow({ window: 'today', now: earlyMorning, timezone });
  assert.equal(todayEarly.granularity, 'MINUTE');
  assert.equal(todayEarly.slots.length, 90);

  const midMorning = new Date('2026-10-11T22:00:00.000Z');
  assert.equal(resolveTrendWindow({ window: 'today', now: midMorning, timezone }).granularity, 'FIVE_MINUTE');

  const afternoon = new Date('2026-10-12T06:00:00.000Z');
  assert.equal(resolveTrendWindow({ window: 'today', now: afternoon, timezone }).granularity, 'QUARTER_HOUR');

  const sevenDays = resolveTrendWindow({ window: '7d', granularity: 'MINUTE', now: afternoon, timezone });
  assert.ok(sevenDays.slots.length <= 720);
  assert.ok(!sevenDays.allowedGranularities.includes('MINUTE'));
  assert.ok(!sevenDays.allowedGranularities.includes('FIVE_MINUTE'));
  assert.equal(sevenDays.compareLabel, '对比上周同时段');
  assert.equal(sevenDays.fromMs - sevenDays.compareFromMs, 7 * 86_400_000);
});

test('buildTrendViewModel splits units into lanes, aligns compare series, preserves outage nulls, and computes stats', () => {
  const catalog = buildTrendCatalog(centralPlantModel);
  const powerPoint = catalog.points.find((point) => point.sourceKey === 'hvac_meter.active_power')!;
  const supplyTempPoint = catalog.points.find((point) => point.sourceKey === 'btu_meter.supply_water_temperature')!;
  const setpointPoint = catalog.points.find((point) => point.sourceKey === 'chiller.chilled_water_temperature_setpoint')!;
  const selected = [powerPoint, supplyTempPoint, setpointPoint];

  const grouped = groupTrendDeviceRequests(selected);
  assert.equal(grouped.length, 3);

  const windowState = resolveTrendWindow({
    window: 'custom',
    from: '2026-10-12T00:00:00.000Z',
    to: '2026-10-12T00:03:00.000Z',
    granularity: 'MINUTE',
    now: new Date('2026-10-12T01:00:00.000Z'),
    timezone: 'Asia/Shanghai',
  });

  const makeGaugeBucket = (
    key: string,
    pointType: 'TELEMETRY' | 'SETTING',
    unit: string,
    periodStart: string,
    periodEnd: string,
    avg: number,
    min: number,
    max: number,
  ): DeviceHistoryAggregateBucket => ({
    telemetryKey: key,
    pointId: '01910000-0000-7000-8000-000000000015',
    pointRevision: 1,
    pointType,
    unit,
    periodStart,
    periodEnd,
    quality: { good: 2, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
    completeness: 1,
    gauge: { average: avg, minimum: min, maximum: max, first: avg, last: avg, sampleCount: 2 },
  });

  const currentBuckets = new Map<string, readonly DeviceHistoryAggregateBucket[]>([
    [
      powerPoint.deviceId,
      [
        makeGaugeBucket(powerPoint.pointCode, 'TELEMETRY', 'kW', '2026-10-12T00:00:00.000Z', '2026-10-12T00:01:00.000Z', 180, 175, 185),
        makeGaugeBucket(powerPoint.pointCode, 'TELEMETRY', 'kW', '2026-10-12T00:02:00.000Z', '2026-10-12T00:03:00.000Z', 220, 210, 230),
      ],
    ],
    [
      supplyTempPoint.deviceId,
      [
        makeGaugeBucket(supplyTempPoint.pointCode, 'TELEMETRY', 'Cel', '2026-10-12T00:00:00.000Z', '2026-10-12T00:01:00.000Z', 7.2, 7.0, 7.4),
        makeGaugeBucket(supplyTempPoint.pointCode, 'TELEMETRY', 'Cel', '2026-10-12T00:01:00.000Z', '2026-10-12T00:02:00.000Z', 7.0, 6.9, 7.1),
        makeGaugeBucket(supplyTempPoint.pointCode, 'TELEMETRY', 'Cel', '2026-10-12T00:02:00.000Z', '2026-10-12T00:03:00.000Z', 6.8, 6.7, 7.0),
      ],
    ],
    [
      setpointPoint.deviceId,
      [
        makeGaugeBucket(setpointPoint.pointCode, 'SETTING', 'Cel', '2026-10-12T00:00:00.000Z', '2026-10-12T00:01:00.000Z', 7.0, 7.0, 7.0),
        makeGaugeBucket(setpointPoint.pointCode, 'SETTING', 'Cel', '2026-10-12T00:01:00.000Z', '2026-10-12T00:02:00.000Z', 7.0, 7.0, 7.0),
        makeGaugeBucket(setpointPoint.pointCode, 'SETTING', 'Cel', '2026-10-12T00:02:00.000Z', '2026-10-12T00:03:00.000Z', 6.5, 6.5, 6.5),
      ],
    ],
  ]);

  const compareBuckets = new Map<string, readonly DeviceHistoryAggregateBucket[]>([
    [
      powerPoint.deviceId,
      [
        makeGaugeBucket(powerPoint.pointCode, 'TELEMETRY', 'kW', '2026-10-11T00:00:00.000Z', '2026-10-11T00:01:00.000Z', 160, 158, 162),
        makeGaugeBucket(powerPoint.pointCode, 'TELEMETRY', 'kW', '2026-10-11T00:01:00.000Z', '2026-10-11T00:02:00.000Z', 165, 160, 170),
        makeGaugeBucket(powerPoint.pointCode, 'TELEMETRY', 'kW', '2026-10-11T00:02:00.000Z', '2026-10-11T00:03:00.000Z', 170, 168, 172),
      ],
    ],
  ]);

  const viewModel = buildTrendViewModel({
    selectedPoints: selected,
    window: windowState,
    currentBucketsByDevice: currentBuckets,
    compareBucketsByDevice: compareBuckets,
  });

  assert.deepEqual(viewModel.gaugeLanes.map((lane) => lane.unit), ['kW', '°C']);
  assert.equal(viewModel.gaugeLanes[1]?.series.length, 2);
  assert.equal(viewModel.gaugeLanes[1]?.series.find((s) => s.ref === setpointPoint.ref)?.isSetpoint, true);

  const powerRows = viewModel.gaugeLanes[0]!.rows;
  assert.equal(powerRows[0]?.values[powerPoint.ref]?.value, 180);
  assert.equal(powerRows[1]?.values[powerPoint.ref]?.value, null);
  assert.equal(powerRows[2]?.values[powerPoint.ref]?.value, 220);
  assert.equal(powerRows[1]?.values[powerPoint.ref]?.compareValue, 165);

  const powerSummary = viewModel.summaryRows.find((row) => row.ref === powerPoint.ref)!;
  assert.equal(powerSummary.latest, 220);
  assert.equal(powerSummary.average, 200);
  assert.equal(powerSummary.minimum?.value, 175);
  assert.equal(powerSummary.minimum?.at, '2026-10-12T00:00:00.000Z');
  assert.equal(powerSummary.maximum?.value, 230);
  assert.equal(powerSummary.maximum?.at, '2026-10-12T00:02:00.000Z');
  assert.ok(Math.abs(powerSummary.coverageRatio - 2 / 3) < 1e-6);
});

test('buildTrendViewModel normalizes discrete state buckets, merges contiguous segments, preserves outage gaps, and resolves cursor state', () => {
  const catalog = buildTrendCatalog(centralPlantModel);
  const runStatePoint = catalog.points.find((point) => point.sourceKey === 'chiller.run_state')!;

  const windowState = resolveTrendWindow({
    window: 'custom',
    from: '2026-10-12T00:00:00.000Z',
    to: '2026-10-12T00:05:00.000Z',
    granularity: 'MINUTE',
    now: new Date('2026-10-12T01:00:00.000Z'),
    timezone: 'Asia/Shanghai',
  });

  const makeStateBucket = (
    periodStart: string,
    periodEnd: string,
    valueType: 'STRING' | 'BOOLEAN' | 'NUMBER',
    lastValue: string | boolean | number,
  ): DeviceHistoryAggregateBucket => ({
    telemetryKey: runStatePoint.pointCode,
    pointId: runStatePoint.pointId,
    pointRevision: 1,
    pointType: 'TELEMETRY',
    unit: null,
    periodStart,
    periodEnd,
    quality: { good: 1, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
    completeness: 1,
    state: { valueType, lastValue, sampleCount: 1, changeCount: 0 },
  });

  // 00:00 RUNNING, 00:01 true (RUNNING -> merged!), 00:02 missing (gap!), 00:03 STOPPED, 00:04 FAULT
  const currentBuckets = new Map<string, readonly DeviceHistoryAggregateBucket[]>([
    [
      runStatePoint.deviceId,
      [
        makeStateBucket('2026-10-12T00:00:00.000Z', '2026-10-12T00:01:00.000Z', 'STRING', 'RUNNING'),
        makeStateBucket('2026-10-12T00:01:00.000Z', '2026-10-12T00:02:00.000Z', 'BOOLEAN', true),
        makeStateBucket('2026-10-12T00:03:00.000Z', '2026-10-12T00:04:00.000Z', 'STRING', 'STOPPED'),
        makeStateBucket('2026-10-12T00:04:00.000Z', '2026-10-12T00:05:00.000Z', 'STRING', 'FAULT'),
      ],
    ],
  ]);

  const viewModel = buildTrendViewModel({
    selectedPoints: [runStatePoint],
    window: windowState,
    currentBucketsByDevice: currentBuckets,
  });

  assert.equal(viewModel.stateTracks.length, 1);
  const track = viewModel.stateTracks[0]!;
  assert.equal(track.segments.length, 3);
  assert.equal(track.segments[0]?.kind, 'RUNNING');
  assert.equal(track.segments[0]?.startPercent, 0);
  assert.equal(track.segments[0]?.widthPercent, 40);
  assert.equal(track.segments[1]?.kind, 'STOPPED');
  assert.equal(track.segments[1]?.startPercent, 60);
  assert.equal(track.segments[1]?.widthPercent, 20);
  assert.equal(track.segments[2]?.kind, 'FAULT');
  assert.equal(track.segments[2]?.startPercent, 80);
  assert.equal(track.segments[2]?.widthPercent, 20);

  // Hovering the outage minute (00:02) returns null instead of stretching RUNNING
  assert.equal(resolveTrackStateAt(track, Date.parse('2026-10-12T00:02:00.000Z')), null);
  assert.deepEqual(resolveTrackStateAt(track, Date.parse('2026-10-12T00:01:00.000Z')), {
    kind: 'RUNNING',
    label: '运行',
  });
  assert.deepEqual(resolveTrackStateAt(track, null), {
    kind: 'FAULT',
    label: '故障',
  });
});
