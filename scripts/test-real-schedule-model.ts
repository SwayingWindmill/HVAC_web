import assert from 'node:assert/strict';
import test from 'node:test';
import type {
  OperatingException,
  OperatingSchedule,
  SiteAssetModel,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { DeviceHistoryAggregateBucket } from '../apps/hvac-web/src/api/generated/s2Telemetry.gen.ts';
import {
  buildDeviationTrendsSearch,
  buildDeviationWorkOrderDraft,
  detectUnscheduledRunningDeviations,
  extractSchedulePlantMonitorTargets,
  findActiveDeviationWorkOrder,
  findOverlappingMinuteSlot,
  mergeWeeklyScheduleSpans,
  resolveEffectiveWeek,
  splitScheduleSlotDraft,
  validateCoolingSlotRowInput,
} from '../apps/hvac-web/src/features/schedules/schedule-model.ts';

const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
const TENANT_ID = '018f3d00-0000-7000-8000-000000000001';
const TIMEZONE = 'Asia/Shanghai';
const CHILLER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000001';
const PUMP_DEVICE_ID = '018f3e00-4000-7000-8000-000000000002';

function buildMinimalAssetModel(ratedPowerKw?: number): SiteAssetModel {
  return {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [
      {
        id: '01910000-0000-7000-8000-000000000005',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chiller-01',
        displayName: '1 号离心冷水机',
        assetType: 'CHILLER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000006',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chwp-01',
        displayName: '1 号冷冻水泵',
        assetType: 'CHILLED_WATER_PUMP',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
    ],
    devices: [
      {
        id: CHILLER_DEVICE_ID,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chiller-01',
        displayName: '1 号离心冷水机通信端点',
        deviceType: 'CHILLER_CONTROLLER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
      {
        id: PUMP_DEVICE_ID,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chwp-01',
        displayName: '1 号冷冻水泵通信端点',
        deviceType: 'PUMP_CONTROLLER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
    ],
    sensors: [],
    telemetryPoints: [
      {
        id: '01910000-0000-7000-8000-00000000001c',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: CHILLER_DEVICE_ID,
        sensorId: null,
        pointCode: 'power',
        sourceKey: 'chiller.power',
        displayName: '冷水机有功功率',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'kW',
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 60000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: ratedPowerKw ? { ratedPowerKw } : {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000014',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: CHILLER_DEVICE_ID,
        sensorId: null,
        pointCode: 'run_state',
        sourceKey: 'chiller.run_state',
        displayName: '冷水机运行状态',
        pointType: 'STATE',
        valueType: 'STRING',
        unit: null,
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 60000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000024',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: PUMP_DEVICE_ID,
        sensorId: null,
        pointCode: 'run_state',
        sourceKey: 'chwp.run_state',
        displayName: '冷冻水泵运行状态',
        pointType: 'STATE',
        valueType: 'STRING',
        unit: null,
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 60000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
    ],
    relationships: [
      {
        id: '01910000-0000-7000-8000-000000000091',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: CHILLER_DEVICE_ID,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-000000000005',
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-10-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000092',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: PUMP_DEVICE_ID,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-000000000006',
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-10-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-10-01T00:00:00.000Z',
        updatedAt: '2026-10-01T00:00:00.000Z',
      },
    ],
    counts: { spaces: 0, assets: 2, deviceEndpoints: 2, physicalSensors: 0, points: 3 },
  };
}

test('splitScheduleSlotDraft splits cross-midnight slot across Sunday->Monday with shared UUIDv7 spanGroupId and mergeWeeklyScheduleSpans restores it', () => {
  const split = splitScheduleSlotDraft({
    dayOfWeek: 7,
    startMinute: 22 * 60,
    endMinute: 6 * 60,
    crossesMidnight: true,
    targetSupplyTempC: 7.5,
  });

  assert.equal(split.length, 2);
  assert.ok(split[0].spanGroupId);
  assert.match(
    split[0].spanGroupId!,
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  );
  assert.equal(split[0].spanGroupId, split[1].spanGroupId);
  assert.deepEqual(
    split.map((s) => ({
      dayOfWeek: s.dayOfWeek,
      startMinute: s.startMinute,
      endMinute: s.endMinute,
      targetSupplyTempC: s.targetSupplyTempC,
    })),
    [
      { dayOfWeek: 7, startMinute: 1320, endMinute: 1440, targetSupplyTempC: 7.5 },
      { dayOfWeek: 1, startMinute: 0, endMinute: 360, targetSupplyTempC: 7.5 },
    ],
  );

  const normal = splitScheduleSlotDraft({
    dayOfWeek: 1,
    startMinute: 8 * 60,
    endMinute: 21 * 60,
    crossesMidnight: false,
    targetSupplyTempC: 7.0,
  });
  assert.equal(normal.length, 1);
  assert.equal(normal[0].spanGroupId, undefined);

  const merged = mergeWeeklyScheduleSpans([...normal, ...split]);
  assert.equal(merged.length, 2);
  const crossSpan = merged.find((item) => item.crossesMidnight);
  assert.ok(crossSpan);
  assert.equal(crossSpan.dayOfWeek, 7);
  assert.equal(crossSpan.startMinute, 1320);
  assert.equal(crossSpan.endMinute, 360);
  assert.equal(crossSpan.targetSupplyTempC, 7.5);
});

test('resolveEffectiveWeek applies APPROVED exceptions as full-day 00:00-24:00 overrides without leaving truncated weekly remnants', () => {
  const crossMidnightFriday = splitScheduleSlotDraft({
    dayOfWeek: 5,
    startMinute: 22 * 60,
    endMinute: 6 * 60,
    crossesMidnight: true,
    targetSupplyTempC: 7.5,
  });

  const schedule: OperatingSchedule = {
    id: '01990000-6000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    timezone: TIMEZONE,
    name: '办公楼工作日计划',
    preCoolLeadMinutes: 45,
    coastDownLagMinutes: 30,
    revision: 2,
    updatedBy: '018f3e00-2000-7000-8000-000000000001',
    updatedAt: '2026-10-12T01:00:00.000Z',
    slots: [
      { dayOfWeek: 1, startMinute: 480, endMinute: 1260, targetSupplyTempC: 7.0 },
      { dayOfWeek: 2, startMinute: 480, endMinute: 1260, targetSupplyTempC: 7.0 },
      { dayOfWeek: 3, startMinute: 480, endMinute: 1260, targetSupplyTempC: 7.0 },
      { dayOfWeek: 4, startMinute: 480, endMinute: 1260, targetSupplyTempC: 7.0 },
      { dayOfWeek: 5, startMinute: 480, endMinute: 1200, targetSupplyTempC: 7.0 },
      ...crossMidnightFriday, // Spills 00:00-06:00 into Saturday (dayOfWeek=6)!
    ],
  };

  // Week of Monday 2026-10-12 .. Sunday 2026-10-18 (Asia/Shanghai)
  const exceptions: OperatingException[] = [
    {
      id: '01990000-7000-7000-8000-000000000001',
      tenantId: TENANT_ID,
      siteId: SITE_ID,
      title: '周三冷机保养全天停机',
      kind: 'MAINTENANCE_WINDOW',
      startDate: '2026-10-14',
      endDate: '2026-10-14',
      replacementSlots: [],
      reason: '蒸发器清洗',
      status: 'APPROVED',
      requestedBy: '018f3e00-2000-7000-8000-000000000002',
      requestedByDisplay: '周敏',
      decidedBy: '018f3e00-2000-7000-8000-000000000001',
      decidedByDisplay: '李工',
      decidedAt: '2026-10-12T02:00:00.000Z',
      decisionNote: '同意',
      revision: 2,
      createdAt: '2026-10-12T01:30:00.000Z',
      updatedAt: '2026-10-12T02:00:00.000Z',
    },
    {
      id: '01990000-7000-7000-8000-000000000002',
      tenantId: TENANT_ID,
      siteId: SITE_ID,
      title: '周六展厅加班供冷',
      kind: 'OVERTIME_COOLING',
      startDate: '2026-10-17',
      endDate: '2026-10-17',
      replacementSlots: [{ startMinute: 600, endMinute: 1080, targetSupplyTempC: 7.5 }],
      reason: '周末客户接待',
      status: 'APPROVED',
      requestedBy: '018f3e00-2000-7000-8000-000000000002',
      requestedByDisplay: '周敏',
      decidedBy: '018f3e00-2000-7000-8000-000000000001',
      decidedByDisplay: '李工',
      decidedAt: '2026-10-12T03:00:00.000Z',
      decisionNote: '同意',
      revision: 2,
      createdAt: '2026-10-12T02:30:00.000Z',
      updatedAt: '2026-10-12T03:00:00.000Z',
    },
    {
      // Pending approval must NOT override Thursday!
      id: '01990000-7000-7000-8000-000000000003',
      tenantId: TENANT_ID,
      siteId: SITE_ID,
      title: '周四临时停机待批',
      kind: 'HOLIDAY_SHUTDOWN',
      startDate: '2026-10-15',
      endDate: '2026-10-15',
      replacementSlots: [],
      reason: '待确认',
      status: 'PENDING_APPROVAL',
      requestedBy: '018f3e00-2000-7000-8000-000000000002',
      requestedByDisplay: '周敏',
      decidedBy: null,
      decidedByDisplay: null,
      decidedAt: null,
      decisionNote: '',
      revision: 1,
      createdAt: '2026-10-12T04:00:00.000Z',
      updatedAt: '2026-10-12T04:00:00.000Z',
    },
  ];

  const week = resolveEffectiveWeek({
    schedule,
    exceptions,
    anchorDate: '2026-10-14',
    timezone: TIMEZONE,
  });

  assert.equal(week.days.length, 7);
  assert.equal(week.mondayDate, '2026-10-12');
  assert.equal(week.sundayDate, '2026-10-18');

  // Wednesday (index 2): MAINTENANCE_WINDOW -> 0 effective slots!
  const wednesday = week.days[2];
  assert.equal(wednesday.date, '2026-10-14');
  assert.equal(wednesday.approvedException?.id, '01990000-7000-7000-8000-000000000001');
  assert.equal(wednesday.effectiveSlots.length, 0);

  // Thursday (index 3): PENDING_APPROVAL does not override weekly 08:00-21:00!
  const thursday = week.days[3];
  assert.equal(thursday.approvedException, null);
  assert.equal(thursday.effectiveSlots.length, 1);
  assert.equal(thursday.effectiveSlots[0].startMinute, 480);

  // Saturday (index 5): OVERTIME_COOLING replaces entire 00:00-24:00, eliminating Friday's 00:00-06:00 spillover!
  const saturday = week.days[5];
  assert.equal(saturday.date, '2026-10-17');
  assert.equal(saturday.approvedException?.kind, 'OVERTIME_COOLING');
  assert.deepEqual(
    saturday.effectiveSlots.map((slot) => [slot.startMinute, slot.endMinute]),
    [[600, 1080]],
  );
});

test('detectUnscheduledRunningDeviations respects pre-cool/coast-down grace windows, detects OFF-window plant operation, clears after OVERTIME_COOLING approval, and builds Trends + EXTERNAL WorkOrder links', () => {
  const assetModel = buildMinimalAssetModel(400); // 10% ratedPower = 40 kW
  const targets = extractSchedulePlantMonitorTargets(assetModel);
  assert.equal(targets.length, 2);

  const schedule: OperatingSchedule = {
    id: '01990000-6000-7000-8000-000000000001',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    timezone: TIMEZONE,
    name: '办公楼工作日计划',
    preCoolLeadMinutes: 45, // 07:15-08:00 is grace window
    coastDownLagMinutes: 30, // 21:00-21:30 is grace window
    revision: 1,
    updatedBy: '018f3e00-2000-7000-8000-000000000001',
    updatedAt: '2026-10-12T00:00:00.000Z',
    slots: [{ dayOfWeek: 1, startMinute: 480, endMinute: 1260, targetSupplyTempC: 7.0 }],
  };

  // Monday 2026-10-12 in Asia/Shanghai (UTC+8):
  // 00:00 CST = 2026-10-11T16:00:00.000Z
  const mondayMidnightUtc = Date.parse('2026-10-11T16:00:00.000Z');
  const quarterHour = (localMinuteStart: number, powerKw: number): DeviceHistoryAggregateBucket => {
    const startMs = mondayMidnightUtc + localMinuteStart * 60_000;
    return {
      telemetryKey: 'power',
      pointId: '01910000-0000-7000-8000-00000000001c',
      pointRevision: 1,
      pointType: 'TELEMETRY',
      unit: 'kW',
      periodStart: new Date(startMs).toISOString(),
      periodEnd: new Date(startMs + 15 * 60_000).toISOString(),
      quality: { good: 30, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
      completeness: 1,
      gauge: {
        average: powerKw,
        minimum: powerKw,
        maximum: powerKw,
        first: powerKw,
        last: powerKw,
        sampleCount: 30,
      },
    };
  };

  const bucketsByDevice = new Map<string, readonly DeviceHistoryAggregateBucket[]>([
    [
      CHILLER_DEVICE_ID,
      [
        quarterHour(7 * 60 + 15, 180), // 07:15-07:30 inside 45m pre-cool grace -> NO deviation!
        quarterHour(7 * 60 + 30, 210), // 07:30-07:45 inside 45m pre-cool grace -> NO deviation!
        quarterHour(21 * 60 + 15, 120), // 21:15-21:30 inside 30m coast-down grace -> NO deviation!
        quarterHour(22 * 60 + 0, 18), // 22:00-22:15 < 40 kW (10% of 400 kW) -> standby, NO deviation!
        quarterHour(22 * 60 + 30, 165), // 22:30-22:45 >= 40 kW in OFF window -> DEVIATION!
        quarterHour(22 * 60 + 45, 175), // 22:45-23:00 >= 40 kW in OFF window -> merged into 30m DEVIATION!
      ],
    ],
  ]);

  const nowMs = mondayMidnightUtc + 23 * 60 * 60_000 + 30 * 60_000; // Monday 23:30 CST
  const weekWithoutException = resolveEffectiveWeek({
    schedule,
    exceptions: [],
    anchorDate: '2026-10-12',
    timezone: TIMEZONE,
  });

  const deviations = detectUnscheduledRunningDeviations({
    siteId: SITE_ID,
    week: weekWithoutException,
    targets,
    bucketsByDevice,
    nowMs,
  });

  assert.equal(deviations.length, 1);
  const [dev] = deviations;
  assert.equal(dev.date, '2026-10-12');
  assert.equal(dev.dayOfWeek, 1);
  assert.equal(dev.startMinute, 22 * 60 + 30);
  assert.equal(dev.endMinute, 23 * 60);
  assert.equal(dev.durationMinutes, 30);
  assert.equal(dev.averagePowerKw, 170);
  assert.deepEqual(dev.runningDeviceNames, ['1 号离心冷水机']);

  // Trends deep-link jumps directly to the deviation window with QUARTER_HOUR granularity and monitored points.
  const trendsSearch = buildDeviationTrendsSearch(dev, targets);
  assert.equal(trendsSearch.window, 'custom');
  assert.equal(trendsSearch.granularity, 'QUARTER_HOUR');
  assert.ok(trendsSearch.points?.includes(`${CHILLER_DEVICE_ID}:power`));

  // Work-order draft uses EXTERNAL origin and deduplicates unclosed work orders.
  const draft = buildDeviationWorkOrderDraft(SITE_ID, dev);
  assert.deepEqual(draft.sourceReferences, [
    {
      domain: 'EXTERNAL',
      resourceId: `operating-schedule-deviation:${SITE_ID}:${new Date(dev.startMs).toISOString()}`,
      relationship: 'ORIGIN',
    },
  ]);
  assert.match(draft.initial.title, /计划外开机核查/);
  assert.equal(
    findActiveDeviationWorkOrder(
      [
        { workOrderId: 'wo-closed', status: 'COMPLETED', createdAt: '2026-10-12T15:05:00Z', sourceReferences: draft.sourceReferences },
        { workOrderId: 'wo-active', status: 'IN_PROGRESS', createdAt: '2026-10-12T15:10:00Z', sourceReferences: draft.sourceReferences },
      ],
      SITE_ID,
      dev,
    )?.workOrderId,
    'wo-active',
  );

  // Once an OVERTIME_COOLING exception covering Monday 2026-10-12 08:00-23:00 is APPROVED, the deviation clears!
  const approvedOvertime: OperatingException = {
    id: '01990000-7000-7000-8000-000000000099',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    title: '周一晚间加班供冷补登',
    kind: 'OVERTIME_COOLING',
    startDate: '2026-10-12',
    endDate: '2026-10-12',
    replacementSlots: [{ startMinute: 480, endMinute: 23 * 60, targetSupplyTempC: 7.0 }],
    reason: '租户晚间活动延时供冷',
    status: 'APPROVED',
    requestedBy: '018f3e00-2000-7000-8000-000000000002',
    requestedByDisplay: '周敏',
    decidedBy: '018f3e00-2000-7000-8000-000000000001',
    decidedByDisplay: '李工',
    decidedAt: '2026-10-12T15:20:00.000Z',
    decisionNote: '补批通过',
    revision: 2,
    createdAt: '2026-10-12T15:15:00.000Z',
    updatedAt: '2026-10-12T15:20:00.000Z',
  };

  const weekWithApprovedOvertime = resolveEffectiveWeek({
    schedule,
    exceptions: [approvedOvertime],
    anchorDate: '2026-10-12',
    timezone: TIMEZONE,
  });
  const clearedDeviations = detectUnscheduledRunningDeviations({
    siteId: SITE_ID,
    week: weekWithApprovedOvertime,
    targets,
    bucketsByDevice,
    nowMs,
  });
  assert.equal(clearedDeviations.length, 0);
});

test('extractSchedulePlantMonitorTargets reads Asset.ratedPower, matches Songyaxuan power_total + PROVISIONAL_READINGS chwp-main, and validates slot rows', () => {
  const baseModel = buildMinimalAssetModel();
  const songyaxuanStyleModel: SiteAssetModel = {
    ...baseModel,
    assets: [
      {
        ...baseModel.assets[0],
        code: 'chiller-a',
        displayName: 'A 机组',
        ratedPower: 300, // 10% threshold = 30 kW on Asset directly!
      },
    ],
    devices: [
      {
        ...baseModel.devices[0],
        code: 'meter-uid1',
        displayName: 'A 机组电表（1 号，UID 1）',
        deviceType: 'HVAC_POWER_METER',
      },
      {
        ...baseModel.devices[1],
        code: 'chwp-main',
        displayName: '冷冻水泵（总管）',
        deviceType: 'PROVISIONAL_READINGS',
      },
    ],
    telemetryPoints: [
      {
        ...baseModel.telemetryPoints[0],
        pointCode: 'power_total',
        sourceKey: 'chiller.power',
        sourceMetadata: {}, // Empty metadata: must pick up Asset.ratedPower = 300
      },
      baseModel.telemetryPoints[1],
      baseModel.telemetryPoints[2],
    ],
    relationships: [baseModel.relationships[0]], // Chiller meter has MONITORS -> CHILLER; chwp-main has no Asset relationship!
  };

  const targets = extractSchedulePlantMonitorTargets(songyaxuanStyleModel);
  assert.equal(targets.length, 2);
  const chillerTarget = targets.find((item) => item.role === 'CHILLER');
  const pumpTarget = targets.find((item) => item.role === 'CHILLED_WATER_PUMP');
  assert.ok(chillerTarget);
  assert.ok(pumpTarget);
  assert.equal(chillerTarget.powerKey, 'power_total');
  assert.equal(chillerTarget.ratedPowerKw, 300);

  const mondayMidnightUtc = Date.parse('2026-10-11T16:00:00.000Z');
  const schedule: OperatingSchedule = {
    id: '01990000-6000-7000-8000-000000000002',
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    timezone: TIMEZONE,
    name: '松雅轩运行计划',
    preCoolLeadMinutes: 0,
    coastDownLagMinutes: 0,
    revision: 1,
    updatedBy: '018f3e00-2000-7000-8000-000000000001',
    updatedAt: '2026-10-12T00:00:00.000Z',
    slots: [{ dayOfWeek: 1, startMinute: 480, endMinute: 1080 }],
  };
  const week = resolveEffectiveWeek({
    schedule,
    exceptions: [],
    anchorDate: '2026-10-12',
    timezone: TIMEZONE,
  });

  const makeBucket = (minuteStart: number, kw: number): DeviceHistoryAggregateBucket => ({
    telemetryKey: 'power_total',
    pointId: '01910000-0000-7000-8000-00000000001c',
    pointRevision: 1,
    pointType: 'TELEMETRY',
    unit: 'kW',
    periodStart: new Date(mondayMidnightUtc + minuteStart * 60_000).toISOString(),
    periodEnd: new Date(mondayMidnightUtc + (minuteStart + 15) * 60_000).toISOString(),
    quality: { good: 30, partial: 0, estimated: 0, manual: 0, stale: 0, invalid: 0 },
    completeness: 1,
    gauge: { average: kw, minimum: kw, maximum: kw, first: kw, last: kw, sampleCount: 30 },
  });

  const deviations = detectUnscheduledRunningDeviations({
    siteId: SITE_ID,
    week,
    targets,
    bucketsByDevice: new Map([
      [
        CHILLER_DEVICE_ID,
        [
          makeBucket(2 * 60, 25), // 25 kW < 30 kW (10% of 300 kW) -> ignored standby
          makeBucket(3 * 60, 35), // 35 kW >= 30 kW -> flagged!
        ],
      ],
    ]),
    nowMs: mondayMidnightUtc + 6 * 60 * 60_000,
  });
  assert.equal(deviations.length, 1);
  assert.equal(deviations[0].startMinute, 180);
  assert.equal(deviations[0].averagePowerKw, 35);

  const invalidCross = validateCoolingSlotRowInput({
    rowLabel: '第 1 行',
    startTime: '22:00',
    endTime: '02:00',
    crossesMidnight: false,
    allowCrossMidnight: true,
    targetSupplyTempInput: '7',
  });
  assert.equal(invalidCross.ok, false);

  const validCross = validateCoolingSlotRowInput({
    rowLabel: '第 1 行',
    startTime: '22:00',
    endTime: '02:00',
    crossesMidnight: true,
    allowCrossMidnight: true,
    targetSupplyTempInput: '7.5',
  });
  assert.equal(validCross.ok, true);

  assert.ok(
    findOverlappingMinuteSlot([
      { startMinute: 480, endMinute: 720 },
      { startMinute: 700, endMinute: 900 },
    ]),
  );
});
