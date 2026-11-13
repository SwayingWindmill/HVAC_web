import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PlatformApiError,
  type EnergySeriesResponse,
  type SiteAssetModel,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { WorkOrder } from '../apps/hvac-web/src/api/work-orders.ts';
import type { AssetsSnapshotResult } from '../apps/hvac-web/src/features/assets/operational-projection.ts';
import {
  QUALITY_REASON_EXPLANATIONS,
  buildMeterReconciliationModel,
  buildSensorCalibrationModel,
  describeIntegrationsError,
  explainQualityReason,
  findMainElectricityCounterDevices,
  formatProbePairLabel,
  formatSourceChannel,
} from '../apps/hvac-web/src/features/integrations/integrations-model.ts';

const TENANT_ID = '018f3d00-0000-7000-8000-000000000001';
const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
const MAIN_METER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000005';
const SUB_METER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000001';
const BTU_METER_DEVICE_ID = '018f3e00-4000-7000-8000-000000000006';

function createSampleAssetModel(): SiteAssetModel {
  return {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [
      {
        id: '01910000-0000-7000-8000-000000000002',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        parentSpaceId: null,
        code: 'central-plant-room',
        displayName: '中央机房',
        spaceType: 'PLANT_ROOM',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.293Z',
        updatedAt: '2026-09-29T08:51:35.734Z',
      },
    ],
    assets: [
      {
        id: '01910000-0000-7000-8000-000000000009',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'hvac-main-meter',
        displayName: '中央空调总电表',
        assetType: 'HVAC_POWER_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.295Z',
        updatedAt: '2026-09-29T08:51:35.735Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000005',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chiller-01',
        displayName: '1 号冷水机组',
        assetType: 'CHILLER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.295Z',
        updatedAt: '2026-09-29T08:51:35.735Z',
        ratedPower: 350,
        ratedCapacity: 1950,
      },
      {
        id: '01910000-0000-7000-8000-00000000000a',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'btu-meter',
        displayName: '冷冻水总冷量表',
        assetType: 'BTU_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.295Z',
        updatedAt: '2026-09-29T08:51:35.735Z',
      },
    ],
    devices: [
      {
        id: MAIN_METER_DEVICE_ID,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'hvac-meter',
        displayName: 'METER-HVAC-TOTAL 通信端点',
        deviceType: 'POWER_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.297Z',
        updatedAt: '2026-09-29T08:51:35.737Z',
      },
      {
        id: SUB_METER_DEVICE_ID,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'chiller-01',
        displayName: 'CHILLER-01 通信端点',
        deviceType: 'CHILLER_CONTROLLER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.296Z',
        updatedAt: '2026-09-29T08:51:35.736Z',
      },
      {
        id: BTU_METER_DEVICE_ID,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'btu-meter',
        displayName: 'BTU-METER-01 通信端点',
        deviceType: 'BTU_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.297Z',
        updatedAt: '2026-09-29T08:51:35.737Z',
      },
    ],
    sensors: [],
    telemetryPoints: [
      {
        id: '01910000-0000-7000-8000-000000000101',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: MAIN_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'active_energy_total',
        sourceKey: 'hvac_meter.active_energy_total',
        displayName: '总正向有功电能',
        pointType: 'COUNTER',
        valueType: 'NUMBER',
        unit: 'kWh',
        writable: false,
        sampleIntervalMs: 60000,
        publishIntervalMs: 60000,
        staleAfterMs: 180000,
        counterDecreaseMode: 'ROLLOVER',
        counterRolloverModulus: 1000000,
        sourceMetadata: { address: '40001', protocol: 'MODBUS_TCP' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000102',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: MAIN_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'active_power',
        sourceKey: 'hvac_meter.active_power',
        displayName: '总有功功率',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'kW',
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 120000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { address: '40003', protocol: 'MODBUS_TCP' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000103',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: SUB_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'energy_consumption',
        sourceKey: 'chiller.energy_consumption',
        displayName: '累计耗电量',
        pointType: 'COUNTER',
        valueType: 'NUMBER',
        unit: 'kWh',
        writable: false,
        sampleIntervalMs: 60000,
        publishIntervalMs: 60000,
        staleAfterMs: 180000,
        counterDecreaseMode: 'RESET_TO_ZERO',
        counterRolloverModulus: null,
        sourceMetadata: { address: 'CH01:kwh', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000104',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: SUB_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'power',
        sourceKey: 'chiller.power',
        displayName: '电功率',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'kW',
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 120000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { address: 'CH01:kw', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000105',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: BTU_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'accumulated_cooling_energy',
        sourceKey: 'btu_meter.accumulated_cooling_energy',
        displayName: '累计冷量',
        pointType: 'COUNTER',
        valueType: 'NUMBER',
        unit: 'kWh',
        writable: false,
        sampleIntervalMs: 60000,
        publishIntervalMs: 60000,
        staleAfterMs: 180000,
        counterDecreaseMode: 'INVALID',
        counterRolloverModulus: null,
        sourceMetadata: { address: 'BTU:01', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000106',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: BTU_METER_DEVICE_ID,
        sensorId: null,
        pointCode: 'instant_cooling_capacity',
        sourceKey: 'btu_meter.instant_cooling_capacity',
        displayName: '瞬时冷量',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'kW',
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 120000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { address: 'BTU:02', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
    ],
    relationships: [
      {
        id: '01910000-0000-7000-8000-000000000201',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: MAIN_METER_DEVICE_ID,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-000000000009',
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-01-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000202',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: SUB_METER_DEVICE_ID,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-000000000005',
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-01-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000203',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: BTU_METER_DEVICE_ID,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-00000000000a',
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-01-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
    ],
    counts: {
      spaces: 1,
      assets: 3,
      deviceEndpoints: 3,
      physicalSensors: 0,
      points: 6,
    },
  };
}

test('buildMeterReconciliationModel reconciles valid vs suspect hourly buckets, counter rollover policies, trend links and deduplicated work orders', () => {
  const assetModel = createSampleAssetModel();
  const now = new Date('2026-10-10T11:30:00.000Z');
  const windowFrom = new Date('2026-10-09T12:00:00.000Z');
  const windowTo = new Date('2026-10-10T12:00:00.000Z');

  // Build 23 completed hours + 1 in-progress hour:
  // Hour 0..20: 100 kWh valid, 100 kWh valid+suspect -> VALID
  // Hour 21: 80 kWh valid, 105 kWh valid+suspect -> SUSPECT (25 kWh suspect)
  // Hour 22: omitted from series -> MISSING
  // Hour 23 (11:00..12:00, contains now 11:30): 40 kWh valid -> IN_PROGRESS
  const validPoints: EnergySeriesResponse['points'] = [];
  const allPoints: EnergySeriesResponse['points'] = [];
  const subValidPoints: EnergySeriesResponse['points'] = [];
  const subAllPoints: EnergySeriesResponse['points'] = [];

  for (let i = 0; i < 24; i += 1) {
    if (i === 22) continue;
    const start = new Date(windowFrom.getTime() + i * 3_600_000).toISOString();
    const end = new Date(windowFrom.getTime() + (i + 1) * 3_600_000).toISOString();
    const validKwh = i === 21 ? 80 : i === 23 ? 40 : 100;
    const totalKwh = i === 21 ? 105 : i === 23 ? 40 : 100;
    validPoints.push({ periodStart: start, periodEnd: end, energyKWh: validKwh });
    allPoints.push({ periodStart: start, periodEnd: end, energyKWh: totalKwh });
    subValidPoints.push({ periodStart: start, periodEnd: end, energyKWh: validKwh / 2 });
    subAllPoints.push({
      periodStart: start,
      periodEnd: end,
      energyKWh: i === 21 ? validKwh / 2 + 15 : validKwh / 2,
    });
  }

  const electricityValid: EnergySeriesResponse = {
    schemaVersion: 1,
    points: validPoints,
    devices: [{ deviceId: SUB_METER_DEVICE_ID, points: subValidPoints }],
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      datasetRevision: 'r1',
      partial: true,
      qualitySummary: { valid: 22, suspect: 1, invalid: 0 },
    },
  };
  const electricityWithSuspect: EnergySeriesResponse = {
    schemaVersion: 1,
    points: allPoints,
    devices: [{ deviceId: SUB_METER_DEVICE_ID, points: subAllPoints }],
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      datasetRevision: 'r1',
      partial: true,
      qualitySummary: { valid: 22, suspect: 1, invalid: 0 },
    },
  };

  const snapshots = new Map<string, AssetsSnapshotResult>([
    [
      MAIN_METER_DEVICE_ID,
      {
        status: 'ok',
        snapshot: {
          schemaVersion: 1,
          deviceId: MAIN_METER_DEVICE_ID,
          tenantId: TENANT_ID,
          siteId: SITE_ID,
          businessRevision: 10,
          evaluatedAt: '2026-10-10T11:29:50.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-10-10T11:29:50.000Z', policyRevision: 1, lastKnown: null },
          telemetryReadiness: 'CURRENT',
          displayState: 'ONLINE',
          values: [
            {
              key: 'active_energy_total',
              state: 'PRESENT',
              value: 128450.5,
              valueType: 'NUMBER',
              unit: 'kWh',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'GOOD',
              qualityReasons: [],
              policyRevision: 1,
              health: { status: 'HEALTHY', score: 100, reasons: [] },
            },
            {
              key: 'active_power',
              state: 'PRESENT',
              value: 192.4,
              valueType: 'NUMBER',
              unit: 'kW',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'GOOD',
              qualityReasons: [],
              policyRevision: 1,
            },
          ],
        },
      },
    ],
    [
      SUB_METER_DEVICE_ID,
      {
        status: 'ok',
        snapshot: {
          schemaVersion: 1,
          deviceId: SUB_METER_DEVICE_ID,
          tenantId: TENANT_ID,
          siteId: SITE_ID,
          businessRevision: 11,
          evaluatedAt: '2026-10-10T11:29:50.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-10-10T11:29:50.000Z', policyRevision: 1, lastKnown: null },
          telemetryReadiness: 'CURRENT',
          displayState: 'ONLINE',
          values: [
            {
              key: 'energy_consumption',
              state: 'PRESENT',
              value: 45210.2,
              valueType: 'NUMBER',
              unit: 'kWh',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'PARTIAL',
              qualityReasons: ['SPIKE', 'PHYSICS_POWER'],
              policyRevision: 1,
              health: { status: 'SUSPECT', score: 65, reasons: ['SPIKE', 'PHYSICS_POWER'] },
            },
          ],
        },
      },
    ],
  ]);

  const existingWorkOrders: WorkOrder[] = [
    {
      schemaVersion: 1,
      workOrderId: '01a10000-0000-7000-8000-000000000099',
      tenantId: TENANT_ID,
      siteId: SITE_ID,
      title: '1 号冷水机组电表跳变核查',
      description: '现场核对脉冲倍率与接线',
      priority: 'MEDIUM',
      status: 'OPEN',
      sourceReferences: [
        {
          domain: 'EXTERNAL',
          resourceId: `integrations-meter:${SITE_ID}:chiller-01`,
          relationship: 'ORIGIN',
        },
      ],
      tasks: { total: 0, completed: 0, blocked: 0 },
      noteCount: 0,
      attachmentCount: 0,
      completionEvidence: [],
      timeline: [],
      version: 1,
      createdAt: '2026-10-10T10:00:00.000Z',
      updatedAt: '2026-10-10T10:00:00.000Z',
    },
  ];

  const model = buildMeterReconciliationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    snapshots,
    electricityValid,
    electricityWithSuspect,
    coolingValid: null,
    coolingWithSuspect: null,
    workOrders: existingWorkOrders,
    window: { fromMs: windowFrom.getTime(), toMs: windowTo.getTime() },
    now,
  });

  // 3 meters discovered: main power meter, chiller sub-meter, BTU thermal meter
  assert.equal(model.summary.totalMeters, 3);
  assert.equal(model.hourlyBuckets.length, 24);
  assert.equal(model.hourlyBuckets[20].status, 'VALID');
  assert.equal(model.hourlyBuckets[21].status, 'SUSPECT');
  assert.equal(model.hourlyBuckets[21].validKwh, 80);
  assert.equal(model.hourlyBuckets[21].suspectKwh, 25);
  assert.equal(model.hourlyBuckets[22].status, 'MISSING');
  assert.equal(model.hourlyBuckets[23].status, 'IN_PROGRESS');

  // Completed hours = 23: 21 valid, 1 suspect, 1 missing
  assert.equal(model.summary.completedHours, 23);
  assert.equal(model.summary.validHours, 21);

  const mainRow = model.rows.find((row) => row.deviceCode === 'hvac-meter');
  assert.ok(mainRow);
  assert.equal(mainRow.meterRoleLabel, '总电表');
  assert.equal(mainRow.supportsHourlyEnergy, true);
  assert.equal(mainRow.counterPolicyLabel, '满量程回零（模数 1,000,000）');
  assert.equal(mainRow.channelLabel, 'Modbus TCP · 40001');
  // 21*100 + 80 + 40 = 2220 valid kWh
  assert.equal(mainRow.validEnergyKwh, 2220);
  assert.equal(mainRow.suspectEnergyKwh, 25);
  assert.equal(mainRow.trendsSearch.points, `${MAIN_METER_DEVICE_ID}:active_power`);
  assert.equal(mainRow.openWorkOrder, null);

  const subRow = model.rows.find((row) => row.deviceCode === 'chiller-01');
  assert.ok(subRow);
  assert.equal(subRow.meterRoleLabel, '设备电表');
  assert.equal(subRow.counterPolicyLabel, '清零重置后继续累计');
  assert.deepEqual(subRow.diagnosticNotes, [
    '读数变化超出单步允许跳变阈值',
    '实测功率超出设备铭牌额定功率合理范围',
    '近 24 小时缺 1 段小时计量数据',
  ]);
  assert.equal(subRow.openWorkOrder?.workOrderId, '01a10000-0000-7000-8000-000000000099');
});

test('buildSensorCalibrationModel surfaces unapplied probe zero-offset suggestions, measurement uncertainty, calibration due status and uncalibrated site fallbacks', () => {
  const baseModel = createSampleAssetModel();
  const sensorDueSoonId = '01910000-0000-7000-8000-000000000301';
  const sensorOverdueId = '01910000-0000-7000-8000-000000000302';
  const assetModel: SiteAssetModel = {
    ...baseModel,
    sensors: [
      {
        id: sensorDueSoonId,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'sensor-ch01-chws',
        displayName: '1 号冷机供水温度探头',
        sensorType: 'TEMPERATURE',
        manufacturer: 'Siemens',
        model: 'QAE2121.010',
        serialNumber: 'SN-CH01-S',
        calibrationDueAt: '2026-10-25T00:00:00.000Z',
        metadata: {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.300Z',
        updatedAt: '2026-09-29T08:51:35.739Z',
      },
      {
        id: sensorOverdueId,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'sensor-btu-cooling',
        displayName: '总管供冷功率热量计探头',
        sensorType: 'THERMAL_METER',
        manufacturer: 'Kamstrup',
        model: 'MULTICAL 603',
        serialNumber: 'SN-BTU-01',
        calibrationDueAt: '2026-10-01T00:00:00.000Z',
        metadata: {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.300Z',
        updatedAt: '2026-09-29T08:51:35.739Z',
      },
    ],
    telemetryPoints: [
      ...baseModel.telemetryPoints.map((pt) =>
        pt.pointCode === 'instant_cooling_capacity'
          ? { ...pt, sensorId: sensorOverdueId }
          : pt,
      ),
      {
        id: '01910000-0000-7000-8000-000000000107',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: SUB_METER_DEVICE_ID,
        sensorId: sensorDueSoonId,
        pointCode: 'leaving_chilled_water_temperature',
        sourceKey: 'chiller.leaving_chilled_water_temperature',
        displayName: '冷冻水出水温度',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'Cel',
        writable: false,
        sampleIntervalMs: 30000,
        publishIntervalMs: 30000,
        staleAfterMs: 120000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { channel: 't1', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
    ],
    counts: {
      ...baseModel.counts,
      physicalSensors: 2,
      points: 7,
    },
  };

  const snapshots = new Map<string, AssetsSnapshotResult>([
    [
      SUB_METER_DEVICE_ID,
      {
        status: 'ok',
        snapshot: {
          schemaVersion: 1,
          deviceId: SUB_METER_DEVICE_ID,
          tenantId: TENANT_ID,
          siteId: SITE_ID,
          businessRevision: 20,
          evaluatedAt: '2026-10-10T11:29:50.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-10-10T11:29:50.000Z', policyRevision: 1, lastKnown: null },
          telemetryReadiness: 'CURRENT',
          displayState: 'ONLINE',
          values: [
            {
              key: 'leaving_chilled_water_temperature',
              state: 'PRESENT',
              value: 7.42,
              valueType: 'NUMBER',
              unit: 'Cel',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'PARTIAL',
              qualityReasons: ['PROBE_OFFSET', 'PHYSICS_CHW_DIRECTION'],
              policyRevision: 1,
              health: { status: 'SUSPECT', score: 72, reasons: ['PROBE_OFFSET', 'PHYSICS_CHW_DIRECTION'] },
              probeOffsets: [
                {
                  pairCode: 'chiller-a-chw',
                  offsetK: 0.38,
                  stddevK: 0.04,
                  sampleCount: 48,
                  windowStartedAt: '2026-10-10T01:00:00.000Z',
                  windowEndedAt: '2026-10-10T01:30:00.000Z',
                  suggested: true,
                  applied: false,
                  alarm: 'OFFSET',
                },
              ],
            },
          ],
        },
      },
    ],
    [
      BTU_METER_DEVICE_ID,
      {
        status: 'ok',
        snapshot: {
          schemaVersion: 1,
          deviceId: BTU_METER_DEVICE_ID,
          tenantId: TENANT_ID,
          siteId: SITE_ID,
          businessRevision: 21,
          evaluatedAt: '2026-10-10T11:29:50.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-10-10T11:29:50.000Z', policyRevision: 1, lastKnown: null },
          telemetryReadiness: 'CURRENT',
          displayState: 'ONLINE',
          values: [
            {
              key: 'instant_cooling_capacity',
              state: 'PRESENT',
              value: 412.5,
              valueType: 'NUMBER',
              unit: 'kW',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'PARTIAL',
              qualityReasons: ['LOW_CONFIDENCE'],
              policyRevision: 1,
              health: { status: 'HEALTHY', score: 90, reasons: ['LOW_CONFIDENCE'] },
              measurement: {
                relative: 0.284,
                low: 295.3,
                high: 529.7,
                lowConfidence: true,
                deltaTK: 1.15,
                deltaTAccuracyK: 0.28,
              },
            },
          ],
        },
      },
    ],
  ]);

  const now = new Date('2026-10-10T11:30:00.000Z');
  const window = {
    fromMs: Date.parse('2026-10-09T12:00:00.000Z'),
    toMs: Date.parse('2026-10-10T12:00:00.000Z'),
  };

  const model = buildSensorCalibrationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    snapshots,
    workOrders: [],
    window,
    now,
  });

  // 4 active numeric TELEMETRY points: active_power, power, instant_cooling_capacity, leaving_chilled_water_temperature
  assert.equal(model.summary.totalPoints, 4);
  assert.equal(model.summary.probeOffsetCount, 1);
  assert.equal(model.summary.physicsOrLowConfidenceCount, 2);
  assert.equal(model.summary.calibrationConfiguredCount, 2);
  assert.equal(model.summary.calibrationDueOrExpiringCount, 2);

  const tempRow = model.rows.find((row) => row.pointCode === 'leaving_chilled_water_temperature');
  assert.ok(tempRow);
  assert.equal(tempRow.primaryProbeOffset?.pairLabel, 'A 机组冷冻水供回水');
  assert.equal(tempRow.primaryProbeOffset?.summaryLabel, '建议减去 0.38 K · 未采用');
  assert.equal(tempRow.primaryProbeOffset?.windowStartedAt, '2026-10-10T01:00:00.000Z');
  assert.equal(tempRow.unit, '°C');
  assert.equal(tempRow.calibration.status, 'EXPIRING_SOON');
  assert.equal(tempRow.calibration.daysRemaining, 15);
  assert.equal(tempRow.sensorMeta?.manufacturer, 'Siemens');
  assert.equal(tempRow.boundAsset?.ratedCapacityKw, 1950);
  assert.equal(
    tempRow.workOrderSourceRef,
    `integrations-sensor:${SITE_ID}:chiller-01:leaving_chilled_water_temperature`,
  );

  const coolingRow = model.rows.find((row) => row.pointCode === 'instant_cooling_capacity');
  assert.ok(coolingRow);
  assert.equal(coolingRow.measurementSummaryLabel, '低可信 · ±28%（295.3–529.7 kW）');
  assert.equal(coolingRow.calibration.status, 'OVERDUE');
  assert.equal(coolingRow.calibration.daysRemaining, -9);

  // When a site has zero calibrationDueAt configured (like Songyaxuan), summary marks calibrationConfiguredCount = 0
  const uncalibratedModel = buildSensorCalibrationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel: baseModel,
    snapshots: new Map(),
    workOrders: [],
    window,
    now,
  });
  assert.equal(uncalibratedModel.summary.calibrationConfiguredCount, 0);
  assert.equal(uncalibratedModel.summary.calibrationDueDisplay, '未登记');
});

test('Songyaxuan HVAC_POWER_METER bound to CHILLER classifies as SUB_ELECTRICITY without inheriting plant total, FLOW_METER marks supportsHourlyEnergy=false, and health.reasons surface OUT_OF_RANGE & PROBE_OFFSET', () => {
  const base = createSampleAssetModel();
  const chillerMeterDeviceId = '018f3e00-4000-7000-8000-000000000091';
  const flowMeterDeviceId = '018f3e00-4000-7000-8000-000000000092';
  const validSensorId = '01910000-0000-7000-8000-000000000399';

  const assetModel: SiteAssetModel = {
    ...base,
    sensors: [
      {
        id: validSensorId,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'sensor-flow-main',
        displayName: '总管超声波流量探头',
        sensorType: 'FLOW_METER',
        manufacturer: 'Flexim',
        model: 'FLUXUS F601',
        serialNumber: 'SN-FLOW-01',
        calibrationDueAt: '2027-01-08T00:00:00.000Z',
        metadata: {},
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.300Z',
        updatedAt: '2026-09-29T08:51:35.739Z',
      },
    ],
    devices: [
      ...base.devices,
      {
        id: chillerMeterDeviceId,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'meter-uid2',
        displayName: 'B 机组电表（UID 2）通信端点',
        deviceType: 'HVAC_POWER_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.297Z',
        updatedAt: '2026-09-29T08:51:35.737Z',
      },
      {
        id: flowMeterDeviceId,
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        code: 'flow-uid10',
        displayName: '冷冻水流量计（UID 10）通信端点',
        deviceType: 'FLOW_METER',
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.297Z',
        updatedAt: '2026-09-29T08:51:35.737Z',
      },
    ],
    telemetryPoints: [
      ...base.telemetryPoints,
      {
        id: '01910000-0000-7000-8000-000000000191',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: chillerMeterDeviceId,
        sensorId: null,
        pointCode: 'active_energy_total',
        sourceKey: 'meter_uid2.active_energy_total',
        displayName: '累计有功电量',
        pointType: 'COUNTER',
        valueType: 'NUMBER',
        unit: 'kWh',
        writable: false,
        sampleIntervalMs: 60000,
        publishIntervalMs: 60000,
        staleAfterMs: 180000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { address: 'UID2:0001', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000192',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        reportingDeviceId: flowMeterDeviceId,
        sensorId: validSensorId,
        pointCode: 'flow_rate',
        sourceKey: 'flow_uid10.flow_rate',
        displayName: '冷冻水总流量',
        pointType: 'TELEMETRY',
        valueType: 'NUMBER',
        unit: 'm3/h',
        writable: false,
        sampleIntervalMs: 60000,
        publishIntervalMs: 60000,
        staleAfterMs: 180000,
        counterDecreaseMode: null,
        counterRolloverModulus: null,
        sourceMetadata: { address: 'UID10:0001', protocol: 'MODBUS_RTU' },
        status: 'ACTIVE',
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
    ],
    relationships: [
      ...base.relationships,
      {
        id: '01910000-0000-7000-8000-000000000291',
        tenantId: TENANT_ID,
        siteId: SITE_ID,
        fromType: 'DEVICE',
        fromId: chillerMeterDeviceId,
        toType: 'ASSET',
        toId: '01910000-0000-7000-8000-000000000005', // CHILLER asset
        role: 'MONITORS',
        status: 'ACTIVE',
        validFrom: '2026-01-01T00:00:00.000Z',
        validTo: null,
        revision: 1,
        createdAt: '2026-09-28T07:15:32.306Z',
        updatedAt: '2026-09-29T08:51:35.743Z',
      },
    ],
  };

  const now = new Date('2026-10-10T11:30:00.000Z');
  const window = {
    fromMs: Date.parse('2026-10-09T12:00:00.000Z'),
    toMs: Date.parse('2026-10-10T12:00:00.000Z'),
  };

  const plantSeries: EnergySeriesResponse = {
    schemaVersion: 1,
    points: [
      {
        periodStart: '2026-10-10T10:00:00.000Z',
        periodEnd: '2026-10-10T11:00:00.000Z',
        energyKWh: 320,
      },
    ],
    devices: [],
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      datasetRevision: 'r1',
      partial: false,
      qualitySummary: { valid: 1, suspect: 0, invalid: 0 },
    },
  };

  // Snapshot where OUT_OF_RANGE and PROBE_OFFSET live ONLY in health.reasons (matching Go backend)
  const snapshots = new Map<string, AssetsSnapshotResult>([
    [
      flowMeterDeviceId,
      {
        status: 'ok',
        snapshot: {
          schemaVersion: 1,
          deviceId: flowMeterDeviceId,
          tenantId: TENANT_ID,
          siteId: SITE_ID,
          businessRevision: 30,
          evaluatedAt: '2026-10-10T11:29:50.000Z',
          evaluationAvailability: 'AVAILABLE',
          availabilityReasons: [],
          presence: { applicability: 'APPLICABLE', currentState: 'ONLINE', lastSeenAt: '2026-10-10T11:29:50.000Z', policyRevision: 1, lastKnown: null },
          telemetryReadiness: 'CURRENT',
          displayState: 'ONLINE',
          values: [
            {
              key: 'flow_rate',
              state: 'PRESENT',
              value: 489.2,
              valueType: 'NUMBER',
              unit: 'm3/h',
              sampledAt: '2026-10-10T11:29:50.000Z',
              receivedAt: '2026-10-10T11:29:51.000Z',
              freshness: 'FRESH',
              quality: 'GOOD',
              qualityReasons: [],
              policyRevision: 1,
              health: { status: 'BAD', score: 0, reasons: ['OUT_OF_RANGE', 'PROBE_OFFSET'] },
              probeOffsets: [
                {
                  pairCode: 'header-chw',
                  offsetK: -0.25,
                  stddevK: 0.03,
                  sampleCount: 22,
                  windowStartedAt: '2026-10-10T05:00:00.000Z',
                  windowEndedAt: '2026-10-10T05:25:00.000Z',
                  suggested: true,
                  applied: false,
                },
              ],
            },
          ],
        },
      },
    ],
  ]);

  const meterModel = buildMeterReconciliationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    snapshots,
    electricityValid: plantSeries,
    electricityWithSuspect: plantSeries,
    coolingValid: null,
    coolingWithSuspect: null,
    window,
    now,
  });

  const subMeterRow = meterModel.rows.find((r) => r.deviceCode === 'meter-uid2');
  assert.ok(subMeterRow);
  assert.equal(subMeterRow.meterRole, 'SUB_ELECTRICITY');
  assert.equal(subMeterRow.meterRoleLabel, '设备电表');
  assert.equal(subMeterRow.supportsHourlyEnergy, true);
  // Must NOT inherit plant-total 320 kWh when devices[] does not contain meter-uid2
  assert.equal(subMeterRow.hasHourlySeries, false);
  assert.equal(subMeterRow.validEnergyKwh, null);

  const flowRow = meterModel.rows.find((r) => r.deviceCode === 'flow-uid10');
  assert.ok(flowRow);
  assert.equal(flowRow.meterRole, 'FLOW_METER');
  assert.equal(flowRow.supportsHourlyEnergy, false);
  assert.equal(flowRow.status.label, '读数无效');
  assert.ok(flowRow.diagnosticNotes.includes('读数超出物理量程范围'));

  const sensorModel = buildSensorCalibrationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    snapshots,
    window,
    now,
  });

  const flowSensorRow = sensorModel.rows.find((r) => r.pointCode === 'flow_rate');
  assert.ok(flowSensorRow);
  assert.equal(flowSensorRow.unit, 'm³/h');
  assert.equal(flowSensorRow.calibration.status, 'VALID');
  assert.equal(flowSensorRow.calibration.label, '剩余 90 天');
  assert.equal(flowSensorRow.primaryProbeOffset?.pairLabel, '冷冻水总管供回水');
  assert.equal(flowSensorRow.primaryProbeOffset?.summaryLabel, '建议加上 0.25 K · 未采用');
  assert.deepEqual(flowSensorRow.diagnosticNotes, [
    '读数超出物理量程范围',
    '仅水泵运行稳态下检测到供回水探头零点偏差',
  ]);
});

test('all 14 quality reason codes, protocol/pair formatters, and Chinese gateway error descriptions are covered', () => {
  assert.equal(Object.keys(QUALITY_REASON_EXPLANATIONS).length, 14);
  for (const [code, explanation] of Object.entries(QUALITY_REASON_EXPLANATIONS)) {
    assert.equal(explainQualityReason(code), explanation);
    assert.ok(explanation.length >= 6);
  }
  assert.equal(explainQualityReason('UNKNOWN_FUTURE_CODE'), '读数存在质量异常，建议现场核查');

  assert.equal(formatSourceChannel({ protocol: 'MODBUS_RTU', address: 'UID20:30401' }), 'Modbus RTU · UID20:30401');
  assert.equal(formatSourceChannel({ protocol: 'COPIES' }), '共享通道');
  assert.equal(formatSourceChannel({}), '未登记通道');

  assert.equal(formatProbePairLabel('header-chw'), '冷冻水总管供回水');
  assert.equal(formatProbePairLabel('chiller-d-chw'), 'D 机组冷冻水供回水');
  assert.equal(formatProbePairLabel('chiller_02_cw'), '2 号机组冷却水进出水');
  assert.equal(formatProbePairLabel('custom_raw_pair'), '供回水温度探头对');

  const makeProblem = (status: number) =>
    new PlatformApiError({
      type: 'about:blank',
      title: 'Error',
      status,
      detail: 'raw english backend detail',
      instance: '/api/v1/test',
      code: 'TEST_ERROR',
      traceId: 'trace-test',
      retryable: false,
    });

  assert.equal(describeIntegrationsError(makeProblem(403)), '当前账号没有读取该站点接入或能耗统计数据的权限');
  assert.equal(describeIntegrationsError(makeProblem(404)), '未找到所选站点的资产或接入登记数据');
  assert.equal(describeIntegrationsError(makeProblem(503)), '平台服务暂时无法返回接入核对数据，请稍后重试');
  assert.equal(describeIntegrationsError(new Error('network fail'), '自定义兜底'), '自定义兜底');
});

test('synthesized site total does not copy sub-meter sum onto physical check meter row (#798)', () => {
  const assetModel = createSampleAssetModel();
  const targets = findMainElectricityCounterDevices(assetModel);
  assert.deepEqual(targets, [{ deviceId: MAIN_METER_DEVICE_ID, pointCode: 'active_energy_total' }]);

  const now = new Date('2026-11-12T04:15:00.000Z');
  const window = {
    fromMs: Date.parse('2026-11-11T05:00:00.000Z'),
    toMs: Date.parse('2026-11-12T05:00:00.000Z'),
  };
  const synthesizedSeries: EnergySeriesResponse = {
    schemaVersion: 1,
    points: [
      { periodStart: '2026-11-12T01:00:00.000Z', periodEnd: '2026-11-12T02:00:00.000Z', energyKWh: 185 },
      { periodStart: '2026-11-12T02:00:00.000Z', periodEnd: '2026-11-12T03:00:00.000Z', energyKWh: 190 },
    ],
    devices: [
      {
        deviceId: SUB_METER_DEVICE_ID,
        points: [
          { periodStart: '2026-11-12T01:00:00.000Z', periodEnd: '2026-11-12T02:00:00.000Z', energyKWh: 185 },
          { periodStart: '2026-11-12T02:00:00.000Z', periodEnd: '2026-11-12T03:00:00.000Z', energyKWh: 190 },
        ],
      },
    ],
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      datasetRevision: 'rev-1',
      partial: false,
      synthesized: true,
      qualitySummary: { valid: 2, suspect: 0, invalid: 0 },
    },
  };

  // Case A: check meter stopped reporting active_energy_total (empty own history).
  const missingCheckModel = buildMeterReconciliationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    electricityValid: synthesizedSeries,
    electricityWithSuspect: synthesizedSeries,
    coolingValid: null,
    coolingWithSuspect: null,
    checkMeterHourlyByDeviceId: new Map([[MAIN_METER_DEVICE_ID, { validPoints: [], allPoints: [] }]]),
    window,
    now,
  });
  // Site summary strip still reflects the synthesized total (2 valid hours, 375 kWh).
  assert.equal(missingCheckModel.summary.totalValidElectricityKwh, 375);
  const checkRowMissing = missingCheckModel.rows.find((r) => r.deviceId === MAIN_METER_DEVICE_ID);
  assert.ok(checkRowMissing);
  assert.equal(checkRowMissing.hasHourlySeries, true);
  assert.equal(checkRowMissing.validEnergyKwh, 0);
  assert.equal(checkRowMissing.missingCompletedHours, 23);
  assert.equal(checkRowMissing.status.level, 'WARNING');
  assert.ok(checkRowMissing.diagnosticNotes.includes('近 24 小时缺 23 段小时计量数据'));

  // Case B: check meter has its own hourly counter history (independent of synthesized total).
  const ownCheckPoints = [
    { periodStart: '2026-11-12T01:00:00.000Z', periodEnd: '2026-11-12T02:00:00.000Z', energyKWh: 240 },
  ];
  const ownCheckModel = buildMeterReconciliationModel({
    siteId: SITE_ID,
    timezone: 'Asia/Shanghai',
    assetModel,
    electricityValid: synthesizedSeries,
    electricityWithSuspect: synthesizedSeries,
    coolingValid: null,
    coolingWithSuspect: null,
    checkMeterHourlyByDeviceId: new Map([
      [MAIN_METER_DEVICE_ID, { validPoints: ownCheckPoints, allPoints: ownCheckPoints }],
    ]),
    window,
    now,
  });
  const checkRowOwn = ownCheckModel.rows.find((r) => r.deviceId === MAIN_METER_DEVICE_ID);
  assert.ok(checkRowOwn);
  assert.equal(checkRowOwn.validEnergyKwh, 240);
  assert.equal(checkRowOwn.missingCompletedHours, 22);
});
