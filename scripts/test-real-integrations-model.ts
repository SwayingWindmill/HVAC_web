import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PlatformApiError,
  gatewayQuarantineRecordSchema,
  type ConcentratorAuditItem,
  type ConcentratorChannelReading,
  type ConcentratorEidSlot,
  type ConcentratorParams,
  type EnergySeriesResponse,
  type GatewayConfigShadow,
  type SiteAssetModel,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { WorkOrder } from '../apps/hvac-web/src/api/work-orders.ts';
import type { AssetsSnapshotResult } from '../apps/hvac-web/src/features/assets/operational-projection.ts';
import {
  AUTONOMY_POLICY_STATE_PRESENTATION,
  MODBUS_CAPABILITY_PROFILES,
  MODBUS_CAPABILITY_PROFILE_LABELS,
  MODBUS_DRIVER_TYPE_LABELS,
  MODBUS_FUNCTION_CODE_LABELS,
  MODBUS_POLL_PRIORITY_PRESENTATION,
  MODBUS_RAW_TYPE_LABELS,
  MODBUS_WRITE_FUNCTION_CODE_LABELS,
  buildValidatedShadowUpdateRequest,
  buildValidatedShadowUpdateRequestFromDesired,
  countDriverRegisters,
  createDefaultAutonomyPolicyDraft,
  createEmptyBACnetObjectDraft,
  createEmptyModbusDriverDraft,
  createEmptyModbusRegisterDraft,
  createEmptyOPCUANodeDraft,
  createEmptySamplingPolicyDraft,
  createShadowEditorDraft,
  formatModbusEnumMap,
  formatPointCopyTargets,
  isHardRealtimeProtectionPoint,
  parseModbusEnumMap,
  parseOPCUANodeId,
  parsePointCopyTargets,
  summarizeAutonomyPolicyState,
  summarizeConcentratorShadowUnits,
  summarizeDriverPointCounts,
  type GatewayConfigShadowEditorDraft,
  type GatewayModbusRegisterDraft,
} from '../apps/hvac-web/src/features/connectivity/gateway-config-shadow-model.ts';
import {
  CONFIG_SHADOW_APPLIED_LABELS,
  CONFIG_SHADOW_REASON_LABELS,
  CONFIG_SHADOW_SECTION_LABELS,
  CONFIG_SHADOW_SYNC_PRESENTATION,
  describeGatewayConfigShadowError,
} from '../apps/hvac-web/src/features/connectivity/gateway-config-shadow-labels.ts';
import {
  CONCENTRATOR_AUDIT_OPERATION_LABELS,
  CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION,
  PROTECTED_RS485_UIDS,
  classifyConcentratorBattery,
  describeConcentratorError,
  filterConcentratorChannels,
  formatConcentratorBaudRate,
  formatConcentratorBandwidth,
  formatConcentratorEidDisplay,
  inferDefaultConcentratorUid,
  isProtectedConcentratorUid,
  mergeConcentratorAudits,
  buildConcentratorChannelRow,
  buildConcentratorSummary,
} from '../apps/hvac-web/src/features/integrations/concentrator-model.ts';
import {
  applyConcentratorEidBatchFill,
  buildConcentratorEidDiff,
  buildConcentratorParamsDiff,
  detectDuplicateNonZeroEids,
  validateConcentratorParamsDraft,
} from '../apps/hvac-web/src/features/integrations/concentrator-config-model.ts';
import {
  QUALITY_REASON_EXPLANATIONS,
  describeIntegrationsError,
  explainQualityReason,
  formatSourceChannel,
} from '../apps/hvac-web/src/features/integrations/governance-shared-model.ts';
import {
  buildMeterReconciliationModel,
  findMainElectricityCounterDevices,
} from '../apps/hvac-web/src/features/integrations/meter-reconciliation-model.ts';
import {
  buildSensorCalibrationModel,
  formatProbePairLabel,
} from '../apps/hvac-web/src/features/integrations/sensor-calibration-model.ts';
import {
  OTA_ARCH_LABELS,
  OTA_CAMPAIGN_STATUS_PRESENTATION,
  OTA_COMPONENT_LABELS,
  OTA_MAX_PACKAGE_BYTES,
  OTA_PACKAGE_STATUS_PRESENTATION,
  OTA_PAUSE_REASON_LABELS,
  OTA_TASK_ACTION_LABELS,
  OTA_TASK_STAGE_PRESENTATION,
  OTA_TASK_WAVE_LABELS,
  describeOtaError,
  extractSuggestedTagsFromDevices,
  formatByteSize,
  formatOtaPauseReason,
  isOTACampaignActive,
  normalizeTargetTagDrafts,
  parseHardwareModelsInput,
  summarizeOtaWorkspace,
  validateHardwareModelsInput,
  validateOptionalSemver,
  validateSemver,
  validateSha256Hex,
  validateSignaturePair,
} from '../apps/hvac-web/src/features/integrations/ota-model.ts';

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

test('concentrator protected RS485 UIDs, formatters, and battery classification cover Songyaxuan boundaries', () => {
  assert.deepEqual([...PROTECTED_RS485_UIDS], [2, 3, 4, 5, 10, 31]);
  for (const protectedUid of PROTECTED_RS485_UIDS) {
    assert.equal(isProtectedConcentratorUid(protectedUid), true);
  }
  assert.equal(isProtectedConcentratorUid(1), false);
  assert.equal(isProtectedConcentratorUid(11), false);
  assert.equal(isProtectedConcentratorUid(1.5), false);

  assert.equal(formatConcentratorBaudRate(3), '9600 bps');
  assert.equal(formatConcentratorBandwidth(0), '125 kHz');
  assert.equal(formatConcentratorEidDisplay(0), '未绑定 (0)');
  assert.equal(formatConcentratorEidDisplay(50), '50 (0x00000032)');

  assert.equal(classifyConcentratorBattery(2449).health, 'CRITICAL_LOW');
  assert.equal(classifyConcentratorBattery(2449).status.tone, 'danger');
  assert.equal(classifyConcentratorBattery(2450).health, 'LOW_VOLTAGE');
  assert.equal(classifyConcentratorBattery(2599).health, 'LOW_VOLTAGE');
  assert.equal(classifyConcentratorBattery(2600).health, 'NORMAL');
  assert.equal(classifyConcentratorBattery(2600).status.tone, 'success');
  assert.equal(classifyConcentratorBattery(null).health, 'UNKNOWN');
});

test('concentrator channel summary, filter, and search handle Songyaxuan telemetry accurately', () => {
  const readings: ConcentratorChannelReading[] = [
    { channel: 1, eid: 50, online: true, sensorType: 1, sensorModel: 'PT1000-W', temperatureC: 24.6, voltageMv: 2980, rssiDbm: -68 },
    { channel: 2, eid: 51, online: true, sensorType: 1, sensorModel: 'PT1000-W', temperatureC: 11.8, voltageMv: 2540, rssiDbm: -74 },
    { channel: 3, eid: 55, online: true, sensorType: 1, sensorModel: 'PT1000-W', temperatureC: 12.1, voltageMv: 2410, rssiDbm: -79 },
    { channel: 4, eid: 0, online: false, sensorType: 1, sensorModel: 'UNBOUND', temperatureC: null, voltageMv: null, rssiDbm: null },
  ];
  const rows = readings.map(buildConcentratorChannelRow);
  const summary = buildConcentratorSummary(rows);

  assert.equal(summary.totalChannels, 4);
  assert.equal(summary.boundChannels, 3);
  assert.equal(summary.onlineCount, 3);
  assert.equal(summary.offlineCount, 1);
  assert.equal(summary.lowBatteryCount, 2);
  assert.equal(summary.criticalBatteryCount, 1);
  assert.equal(summary.avgTemperatureC, 16.2);

  assert.equal(filterConcentratorChannels(rows, 'LOW_BATTERY', '').length, 2);
  assert.equal(filterConcentratorChannels(rows, 'OFFLINE', '').length, 1);
  assert.deepEqual(
    filterConcentratorChannels(rows, 'ALL', '0x00000037').map((row) => row.channel),
    [3],
  );
  assert.deepEqual(
    filterConcentratorChannels(rows, 'LOW_BATTERY', '51').map((row) => row.channel),
    [2],
  );
});

test('concentrator params validation & diff catch protected UID and build exact partial patch', () => {
  const original: ConcentratorParams = {
    modbusId: 1,
    baudRateCode: 3,
    offLineTime: 10,
    dataUplinkCycle: 10,
    localNet: 1,
    timeMark: 1,
    rfTxPower: 20,
    rfFrequency: 433,
    rfBandWidth: 0,
    rfSf: 9,
    rfLeadCode: 8,
    txTimeout: 5,
    rxTimeout: 5,
    rssiOut: 1,
  };

  assert.equal(validateConcentratorParamsDraft(original).valid, true);
  assert.equal(buildConcentratorParamsDiff(original, original).hasChanges, false);

  const protectedDraft: ConcentratorParams = { ...original, modbusId: 5 };
  const protectedResult = validateConcentratorParamsDraft(protectedDraft);
  assert.equal(protectedResult.valid, false);
  assert.equal(protectedResult.protectedModbusId, true);
  assert.match(protectedResult.errors.modbusId ?? '', /受保护/);

  const validEdit: ConcentratorParams = { ...original, rfTxPower: 22, dataUplinkCycle: 15 };
  const diff = buildConcentratorParamsDiff(original, validEdit);
  assert.equal(diff.hasChanges, true);
  assert.deepEqual(diff.patch, { dataUplinkCycle: 15, rfTxPower: 22 });
  assert.equal(diff.changes.length, 2);
  assert.equal(diff.changes[0]?.beforeDisplay, '10 秒');
  assert.equal(diff.changes[1]?.afterDisplay, '22 dBm');
});

test('concentrator 200-slot EID duplicate guard, batch fill, diff, and error messages work end to end', () => {
  const initialSlots: ConcentratorEidSlot[] = Array.from({ length: 200 }, (_, index) => ({
    slot: index + 1,
    eid: index < 3 ? 50 + index : 0,
  }));

  // Duplicate 0s are unbound and must never conflict.
  assert.equal(detectDuplicateNonZeroEids(initialSlots).hasConflicts, false);

  // Batch fill 4 consecutive EIDs starting at slot 10, then clear slots 12..13.
  const filled = applyConcentratorEidBatchFill(initialSlots, {
    startSlot: 10,
    count: 4,
    startEid: 101,
    step: 1,
  });
  assert.equal(filled[9]?.eid, 101);
  assert.equal(filled[12]?.eid, 104);

  const cleared = applyConcentratorEidBatchFill(filled, {
    startSlot: 12,
    count: 2,
    startEid: 0,
    step: 1,
  });
  assert.equal(cleared[11]?.eid, 0);
  assert.equal(cleared[12]?.eid, 0);

  const eidDiff = buildConcentratorEidDiff(initialSlots, cleared);
  assert.deepEqual(eidDiff, [
    { slot: 10, beforeEid: 0, afterEid: 101 },
    { slot: 11, beforeEid: 0, afterEid: 102 },
  ]);

  // Setting slot 20 to an existing positive EID (50) flags both slot 1 and slot 20.
  const duplicated = cleared.map((entry) =>
    entry.slot === 20 ? { slot: 20, eid: 50 } : entry,
  );
  const conflict = detectDuplicateNonZeroEids(duplicated);
  assert.equal(conflict.hasConflicts, true);
  assert.deepEqual(conflict.duplicatesByEid.get(50), [1, 20]);
  assert.equal(conflict.conflictingSlotSet.has(1), true);
  assert.equal(conflict.conflictingSlotSet.has(20), true);

  assert.equal(CONCENTRATOR_AUDIT_OPERATION_LABELS.WRITE_PARAMS, '串口与射频参数写入');
  assert.equal(CONCENTRATOR_AUDIT_OUTCOME_PRESENTATION.APPLIED.label, '已写入 Flash');

  const merged = mergeConcentratorAudits(
    [
      {
        id: '01910000-0000-7000-8000-000000000001',
        unitId: 1,
        actorId: '01910000-0000-7000-8000-000000000010',
        operation: 'WRITE_PARAMS',
        requestId: '01910000-0000-7000-8000-000000000011',
        outcome: 'APPLIED',
        reason: 'earlier',
        diff: {},
        occurredAt: '2026-11-15T01:00:00Z',
      } satisfies ConcentratorAuditItem,
    ],
    [
      {
        id: '01910000-0000-7000-8000-000000000001',
        unitId: 1,
        actorId: '01910000-0000-7000-8000-000000000010',
        operation: 'WRITE_PARAMS',
        requestId: '01910000-0000-7000-8000-000000000011',
        outcome: 'APPLIED',
        reason: 'duplicate id skipped',
        diff: {},
        occurredAt: '2026-11-15T01:00:00Z',
      },
      {
        id: '01910000-0000-7000-8000-000000000002',
        unitId: 1,
        actorId: '01910000-0000-7000-8000-000000000010',
        operation: 'WRITE_EIDS',
        requestId: '01910000-0000-7000-8000-000000000012',
        outcome: 'TIMEOUT',
        reason: 'newer',
        diff: {},
        occurredAt: '2026-11-15T02:00:00Z',
      },
    ],
  );
  assert.equal(merged.length, 2);
  assert.equal(merged[0]?.id, '01910000-0000-7000-8000-000000000002');

  const uidError = new PlatformApiError({
    type: 'about:blank',
    title: 'Protected UID',
    status: 409,
    detail: 'protected',
    instance: '/api/v1/gateways/g/concentrators/2/status',
    code: 'CONCENTRATOR_UID_PROTECTED',
    traceId: '0'.repeat(32),
    retryable: false,
  });
  assert.match(describeConcentratorError(uidError), /受保护/);

  const rpcTimeoutError = new PlatformApiError({
    type: 'about:blank',
    title: 'RPC Timeout',
    status: 504,
    detail: 'timeout',
    instance: '/api/v1/gateways/g/concentrators/1/params',
    code: 'CONCENTRATOR_RPC_TIMEOUT',
    traceId: '0'.repeat(32),
    retryable: true,
  });
  assert.match(describeConcentratorError(rpcTimeoutError), /未及时应答/);

  const transportError = new PlatformApiError({
    type: 'about:blank',
    title: 'Transport Unavailable',
    status: 503,
    detail: 'unavailable',
    instance: '/api/v1/gateways/g/concentrators/1/status',
    code: 'CONCENTRATOR_TRANSPORT_UNAVAILABLE',
    traceId: '0'.repeat(32),
    retryable: true,
  });
  assert.match(describeConcentratorError(transportError), /远程连接通道暂未就绪/);

  const revokedError = new PlatformApiError({
    type: 'about:blank',
    title: 'Revoked',
    status: 409,
    detail: 'revoked',
    instance: '/api/v1/gateways/g/concentrators/1/eids',
    code: 'GATEWAY_REVOKED',
    traceId: '0'.repeat(32),
    retryable: false,
  });
  assert.match(describeConcentratorError(revokedError), /接入凭据已停用/);

  assert.match(
    describeConcentratorError(new Error('unknown failure'), '参数写入集中器 Flash 失败'),
    /参数写入集中器 Flash 失败/,
  );

  assert.equal(
    inferDefaultConcentratorUid([
      { deviceType: 'GATEWAY', code: 'gw-1', displayName: '网关' },
      { deviceType: 'TEMPERATURE_MODULE', code: 'temperature-uid20', displayName: '温度采集模块（UID 20）' },
    ]),
    20,
  );
  assert.equal(
    inferDefaultConcentratorUid([
      { deviceType: 'TEMPERATURE_MODULE', code: 'temperature-uid2', displayName: '受保护地址模拟' },
    ]),
    1,
  );
  assert.equal(inferDefaultConcentratorUid([]), 1);
});

test('gateway config shadow helpers validate copy targets, preserve concentrator units, and surface revision conflicts', () => {
  assert.equal(CONFIG_SHADOW_SYNC_PRESENTATION.PENDING_SYNC.label, '待同步下发');
  assert.equal(CONFIG_SHADOW_APPLIED_LABELS.PARTIAL, '部分生效');
  assert.equal(CONFIG_SHADOW_SECTION_LABELS.POINT_MAPPINGS, '点位映射');
  assert.equal(CONFIG_SHADOW_REASON_LABELS.EDGE_APPLY_FAILED, '边缘端应用失败');

  const parsedCopy = parsePointCopyTargets('chiller-02:supply_temp\nchiller-03:supply_temp');
  assert.equal(parsedCopy.ok, true);
  if (parsedCopy.ok) {
    assert.deepEqual(parsedCopy.targets, [
      { deviceKey: 'chiller-02', pointCode: 'supply_temp' },
      { deviceKey: 'chiller-03', pointCode: 'supply_temp' },
    ]);
    assert.equal(formatPointCopyTargets(parsedCopy.targets), 'chiller-02:supply_temp, chiller-03:supply_temp');
  }

  const badCopy = parsePointCopyTargets('chiller-02:BadCode');
  assert.equal(badCopy.ok, false);

  const sampleShadow = {
    tenantId: TENANT_ID,
    gatewayId: '01910000-0000-7000-8000-000000000901',
    syncStatus: 'DRIFTED' as const,
    desired: {
      desiredRevision: 4,
      pointMappings: [
        {
          sourceChannel: 'rs485-u1:r1',
          primaryDeviceKey: 'chiller-01',
          primaryPointCode: 'supply_temp',
          copyTargets: [{ deviceKey: 'chiller-02', pointCode: 'supply_temp' }],
        },
      ],
      samplingPolicies: [
        { deviceKey: 'chiller-01', pointCode: 'supply_temp', sampleIntervalMs: 2000, publishIntervalMs: 5000 },
      ],
      concentratorConfigs: {
        '20': { params: undefined, eids: [{ slot: 1, eid: 101 }, { slot: 2, eid: 0 }] },
        '1': {
          params: {
            modbusId: 1,
            baudRateCode: 3,
            offLineTime: 10,
            dataUplinkCycle: 10,
            localNet: 1,
            timeMark: 1,
            rfTxPower: 20,
            rfFrequency: 433,
            rfBandWidth: 0,
            rfSf: 9,
            rfLeadCode: 8,
            txTimeout: 5,
            rxTimeout: 5,
            rssiOut: 1,
          },
          eids: [],
        },
      },
      updatedBy: TENANT_ID,
      reason: 'init shadow',
      createdAt: '2026-11-16T00:00:00Z',
      updatedAt: '2026-11-16T01:00:00Z',
    },
    drift: [],
  };

  const unitSummaries = summarizeConcentratorShadowUnits(sampleShadow.desired.concentratorConfigs);
  assert.deepEqual(unitSummaries, [
    { unitId: '1', hasParams: true, boundEidCount: 0 },
    { unitId: '20', hasParams: false, boundEidCount: 1 },
  ]);

  const draft = createShadowEditorDraft(sampleShadow);
  assert.equal(draft.pointMappings[0]?.copyTargetsText, 'chiller-02:supply_temp');

  const missingReason = buildValidatedShadowUpdateRequest(draft, sampleShadow);
  assert.equal(missingReason.ok, false);

  const built = buildValidatedShadowUpdateRequest({ ...draft, reason: '调整复用映射' }, sampleShadow);
  assert.equal(built.ok, true);
  if (built.ok) {
    assert.equal(built.request.expectedDesiredRevision, 4);
    assert.equal('concentratorConfigs' in built.request, false);
    assert.equal(built.request.pointMappings?.[0]?.copyTargets?.length, 1);
  }

  const badSampling = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      reason: 'invalid period',
      samplingPolicies: [
        {
          ...createEmptySamplingPolicyDraft(),
          deviceKey: 'chiller-01',
          pointCode: '',
          sampleIntervalMs: '10000',
          publishIntervalMs: '2000',
        },
      ],
    },
    sampleShadow,
  );
  assert.equal(badSampling.ok, false);

  const conflictErr = new PlatformApiError({
    type: 'about:blank',
    title: 'Conflict',
    status: 409,
    detail: 'revision conflict',
    instance: '/api/v1/gateways/g/config-shadow',
    code: 'CONFIG_SHADOW_REVISION_CONFLICT',
    traceId: '0'.repeat(32),
    retryable: false,
  });
  assert.match(describeGatewayConfigShadowError(conflictErr), /期望修订/);
});

test('gateway config shadow supports declarative driverConfigs, autonomyPolicy bounds, partial PUT clearing, and P2 drift labels', () => {
  assert.equal(CONFIG_SHADOW_SECTION_LABELS.DRIVER_CONFIGS, '协议驱动点表');
  assert.equal(CONFIG_SHADOW_SECTION_LABELS.AUTONOMY_POLICY, '断网自治策略');
  assert.equal(MODBUS_DRIVER_TYPE_LABELS.MODBUS_TCP, 'Modbus TCP');
  assert.equal(MODBUS_DRIVER_TYPE_LABELS.MODBUS_RTU, 'Modbus RTU');
  assert.equal(MODBUS_CAPABILITY_PROFILES.length, 7);
  assert.equal(MODBUS_CAPABILITY_PROFILE_LABELS.CHILLER, '冷机');
  assert.equal(MODBUS_RAW_TYPE_LABELS.BIT_BOOL, '位布尔');
  assert.equal(MODBUS_RAW_TYPE_LABELS.ENUM16, '16 位枚举');
  assert.equal(MODBUS_FUNCTION_CODE_LABELS[3], '保持寄存器 FC3');
  assert.equal(MODBUS_FUNCTION_CODE_LABELS[4], '输入寄存器 FC4');
  assert.equal(MODBUS_WRITE_FUNCTION_CODE_LABELS['6'], '单写 FC6');
  assert.equal(MODBUS_WRITE_FUNCTION_CODE_LABELS['16'], '多写 FC16');
  assert.equal(MODBUS_POLL_PRIORITY_PRESENTATION.VERY_HIGH.label, '极高');
  assert.equal(AUTONOMY_POLICY_STATE_PRESENTATION.ENABLED.label, '已启用本地自治');
  assert.equal(AUTONOMY_POLICY_STATE_PRESENTATION.DISABLED.label, '仅云端受控');
  assert.equal(AUTONOMY_POLICY_STATE_PRESENTATION.UNCONFIGURED.label, '未配置');
  assert.ok(gatewayQuarantineRecordSchema.shape.reasonCode.options.includes('RATE_LIMITED'));

  const parsedEnum = parseModbusEnumMap('1=运行, 0=停机');
  assert.equal(parsedEnum.ok, true);
  if (parsedEnum.ok) {
    assert.deepEqual(parsedEnum.enumMap, { '0': '停机', '1': '运行' });
    assert.equal(formatModbusEnumMap(parsedEnum.enumMap), '0=停机, 1=运行');
  }
  assert.equal(parseModbusEnumMap('').ok, false);
  assert.equal(parseModbusEnumMap('-1=异常').ok, false);
  assert.equal(parseModbusEnumMap('65536=超限').ok, false);
  assert.equal(parseModbusEnumMap('0x1=十六进制').ok, false);
  assert.equal(parseModbusEnumMap('01=前导零').ok, false);
  assert.equal(parseModbusEnumMap('0=').ok, false);
  assert.equal(parseModbusEnumMap(`0=${'a'.repeat(65)}`).ok, false);
  assert.equal(parseModbusEnumMap('0=停机, 0=运行').ok, false);

  const initialShadow: GatewayConfigShadow = {
    tenantId: TENANT_ID,
    gatewayId: '01910000-0000-7000-8000-000000000902',
    syncStatus: 'UNCONFIGURED',
    drift: [],
  };

  const baseDriver = createEmptyModbusDriverDraft();
  const regFloat: GatewayModbusRegisterDraft = {
    ...createEmptyModbusRegisterDraft(),
    pointCode: 'chw_supply_temp',
    functionCode: '3',
    writeFunctionCode: '16',
    address: '40001',
    registerCount: '2',
    rawType: 'FLOAT32',
    scale: '1',
    offset: '0',
    pollPriority: 'VERY_HIGH',
  };
  const regBit: GatewayModbusRegisterDraft = {
    ...createEmptyModbusRegisterDraft(),
    pointCode: 'compressor_running',
    functionCode: '4',
    address: '100',
    registerCount: '1',
    rawType: 'BIT_BOOL',
    bitIndex: '3',
    scale: '1',
    offset: '0',
    pollPriority: 'HIGH',
  };
  const regEnum: GatewayModbusRegisterDraft = {
    ...createEmptyModbusRegisterDraft(),
    pointCode: 'run_mode',
    functionCode: '3',
    writeFunctionCode: '6',
    address: '200',
    registerCount: '1',
    rawType: 'ENUM16',
    enumMapText: '0=停机, 1=运行',
    scale: '1',
    offset: '0',
    pollPriority: 'MEDIUM',
  };

  const initDraft: GatewayConfigShadowEditorDraft = {
    ...createShadowEditorDraft(initialShadow),
    driverConfigs: [
      {
        ...baseDriver,
        deviceKey: 'chiller-01',
        driverType: 'MODBUS_TCP',
        endpoint: '192.168.10.21:502',
        unitId: '2',
        capabilityProfile: 'CHILLER',
        registers: [regFloat, regBit, regEnum],
      },
    ],
    autonomyPolicy: {
      ...createDefaultAutonomyPolicyDraft(),
      enabled: true,
      offlineGraceSeconds: '180',
      defaultChilledWaterSupplySetpointC: '7.5',
      minPumpFrequencyHz: '28',
      defaultPumpFrequencyHz: '38',
      maxPumpFrequencyHz: '48',
    },
    reason: '下发冷机协议驱动点表与断网自治策略',
  };

  const initReq = buildValidatedShadowUpdateRequest(initDraft, initialShadow);
  assert.equal(initReq.ok, true);
  if (initReq.ok) {
    assert.equal(initReq.request.expectedDesiredRevision, 0);
    assert.equal('pointMappings' in initReq.request, false);
    assert.equal('samplingPolicies' in initReq.request, false);
    assert.equal('concentratorConfigs' in initReq.request, false);
    assert.equal(initReq.request.driverConfigs?.length, 1);
    assert.equal(initReq.request.driverConfigs?.[0]?.capabilityProfile, 'CHILLER');
    assert.equal(countDriverRegisters(initReq.request.driverConfigs), 3);
    assert.equal(initReq.request.driverConfigs?.[0]?.registers?.[1]?.bitIndex, 3);
    assert.deepEqual(initReq.request.driverConfigs?.[0]?.registers?.[2]?.enumMap, {
      '0': '停机',
      '1': '运行',
    });
    assert.equal(initReq.request.autonomyPolicy?.enabled, true);
    assert.equal(summarizeAutonomyPolicyState(initReq.request.autonomyPolicy), 'ENABLED');
    assert.equal(summarizeAutonomyPolicyState(null), 'UNCONFIGURED');
    assert.equal(
      summarizeAutonomyPolicyState({ ...initReq.request.autonomyPolicy!, enabled: false }),
      'DISABLED',
    );
  }

  // Canonical Modbus validation guards matching backend & edgecontrol invariants.
  const expectDriverInvalid = (
    patchDriver: Partial<typeof initDraft.driverConfigs[0]>,
    msg: string,
  ) => {
    const res = buildValidatedShadowUpdateRequest(
      {
        ...initDraft,
        driverConfigs: [{ ...initDraft.driverConfigs[0]!, ...patchDriver }],
      },
      initialShadow,
    );
    assert.equal(res.ok, false, msg);
  };

  expectDriverInvalid({ registers: [{ ...regBit, bitIndex: '' }] }, 'BIT_BOOL requires bitIndex');
  expectDriverInvalid(
    { registers: [{ ...regFloat, bitIndex: '0' }] },
    'non-BIT_BOOL rejects bitIndex',
  );
  expectDriverInvalid(
    {
      registers: [
        regBit,
        { ...regBit, pointCode: 'compressor_fault' },
      ],
    },
    'duplicate BIT_BOOL (FC, address, bitIndex) rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regBit, writeFunctionCode: '6' }] },
    'FC4 read-only forbids writeFunctionCode',
  );
  expectDriverInvalid(
    { registers: [{ ...regFloat, writeFunctionCode: '6' }] },
    'FC6 on 2-register FLOAT32 rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regFloat, registerCount: '1' }] },
    'registerCount mismatch rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regFloat, address: '65535' }] },
    'address + registerCount > 65536 rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regFloat, scale: '0' }] },
    'scale === 0 rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regEnum, enumMapText: '' }] },
    'ENUM16 empty enumMap rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regEnum, enumMapText: '65536=超限' }] },
    'ENUM16 non-uint16 key rejected',
  );
  expectDriverInvalid(
    { registers: [{ ...regFloat, enumMapText: '0=停机' }] },
    'non-ENUM16 with enumMap rejected',
  );
  expectDriverInvalid({ capabilityProfile: 'chiller_v1' }, 'non-standard capabilityProfile rejected');
  expectDriverInvalid({ deviceKey: 'chiller/01' }, 'deviceKey with slash rejected');
  expectDriverInvalid({ endpoint: '192.168.10.21' }, 'MODBUS_TCP without port rejected');
  expectDriverInvalid({ endpoint: ':502' }, 'MODBUS_TCP without host rejected');
  expectDriverInvalid({ endpoint: '192.168.10.21:65536' }, 'MODBUS_TCP port > 65535 rejected');

  const duplicateDeviceKeyRes = buildValidatedShadowUpdateRequest(
    {
      ...initDraft,
      driverConfigs: [
        initDraft.driverConfigs[0]!,
        { ...initDraft.driverConfigs[0]!, endpoint: '192.168.10.22:502' },
      ],
    },
    initialShadow,
  );
  assert.equal(duplicateDeviceKeyRes.ok, false);

  const validIpv6BoundaryRes = buildValidatedShadowUpdateRequest(
    {
      ...initDraft,
      driverConfigs: [
        {
          ...initDraft.driverConfigs[0]!,
          endpoint: '[::1]:502',
          registers: [{ ...regFloat, address: '65534' }],
        },
      ],
    },
    initialShadow,
  );
  assert.equal(validIpv6BoundaryRes.ok, true);

  const badPumpOrder = buildValidatedShadowUpdateRequest(
    {
      ...initDraft,
      autonomyPolicy: {
        ...initDraft.autonomyPolicy!,
        minPumpFrequencyHz: '42',
        defaultPumpFrequencyHz: '35',
        maxPumpFrequencyHz: '48',
      },
    },
    initialShadow,
  );
  assert.equal(badPumpOrder.ok, false);

  // Partial update when shadow already has driverConfigs + autonomyPolicy:
  // modifying only autonomyPolicy omits unchanged driverConfigs (even across register reordering); clearing sends [] / null.
  assert.equal(initReq.ok, true);
  if (initReq.ok) {
    const activeShadow: GatewayConfigShadow = {
      tenantId: TENANT_ID,
      gatewayId: initialShadow.gatewayId,
      syncStatus: 'SYNCED',
      desired: {
        desiredRevision: 5,
        pointMappings: [],
        samplingPolicies: [],
        concentratorConfigs: {},
        driverConfigs: initReq.request.driverConfigs,
        autonomyPolicy: initReq.request.autonomyPolicy,
        updatedBy: TENANT_ID,
        reason: 'applied',
        createdAt: '2026-11-16T00:00:00Z',
        updatedAt: '2026-11-16T01:00:00Z',
      },
      drift: [],
    };
    const hydrated = createShadowEditorDraft(activeShadow);
    assert.equal(hydrated.driverConfigs[0]?.capabilityProfile, 'CHILLER');
    assert.equal(hydrated.driverConfigs[0]?.registers[1]?.bitIndex, '3');
    assert.equal(hydrated.driverConfigs[0]?.registers[2]?.enumMapText, '0=停机, 1=运行');
    assert.equal(hydrated.autonomyPolicy?.defaultChilledWaterSupplySetpointC, '7.5');

    const toggleOffOnly = buildValidatedShadowUpdateRequestFromDesired(
      {
        ...hydrated,
        driverConfigs: [
          {
            ...hydrated.driverConfigs[0]!,
            registers: [...hydrated.driverConfigs[0]!.registers].reverse(),
          },
        ],
        autonomyPolicy: { ...hydrated.autonomyPolicy!, enabled: false },
        reason: '切换为仅云端受控',
      },
      activeShadow.desired!.desiredRevision,
      activeShadow.desired,
    );
    assert.equal(toggleOffOnly.ok, true);
    if (toggleOffOnly.ok) {
      assert.equal(toggleOffOnly.request.expectedDesiredRevision, 5);
      assert.equal('driverConfigs' in toggleOffOnly.request, false);
      assert.equal(toggleOffOnly.request.autonomyPolicy?.enabled, false);
    }

    const clearBoth = buildValidatedShadowUpdateRequest(
      {
        ...hydrated,
        driverConfigs: [],
        autonomyPolicy: null,
        reason: '清空驱动点表与断网自治策略',
      },
      activeShadow,
    );
    assert.equal(clearBoth.ok, true);
    if (clearBoth.ok) {
      assert.deepEqual(clearBoth.request.driverConfigs, []);
      assert.equal(clearBoth.request.autonomyPolicy, null);
    }
  }
});

test('gateway config shadow supports BACnet/IP and OPC-UA driver validation, EDGE-003 boundary, and canonical equality', () => {
  const bacBase = createEmptyModbusDriverDraft();
  const bacObjRead = createEmptyBACnetObjectDraft();
  const bacObjWrite = {
    ...createEmptyBACnetObjectDraft(),
    pointCode: 'supply_air_temp_sp',
    semanticChannel: 'SupplyTempSetpoint',
    objectType: 'ANALOG_VALUE' as const,
    objectInstance: '2',
    propertyId: '85',
    writable: true,
    covSubscribe: true,
    pollPriority: 'HIGH' as const,
    unit: '°C',
  };
  const bacDriver = {
    ...bacBase,
    deviceKey: 'ahu-bacnet-01',
    driverType: 'BACNET_IP' as const,
    endpoint: '192.168.10.30:47808',
    bacnet: {
      ...bacBase.bacnet,
      deviceInstance: '2048',
      writePriority: '8',
      covLifetimeSeconds: '300',
      objects: [
        {
          ...bacObjRead,
          pointCode: 'supply_air_temp',
          semanticChannel: 'SupplyTemp',
          objectType: 'ANALOG_INPUT' as const,
          objectInstance: '1',
          unit: '°C',
        },
        bacObjWrite,
      ],
    },
  };

  const uaBase = createEmptyModbusDriverDraft();
  const uaNode = {
    ...createEmptyOPCUANodeDraft(),
    pointCode: 'chiller_power',
    semanticChannel: 'PowerKW',
    nodeId: 'ns=2;s=Chiller1.Power',
    subscribe: true,
    pollPriority: 'HIGH' as const,
    unit: 'kW',
  };
  const uaDriver = {
    ...uaBase,
    deviceKey: 'chiller-opcua-01',
    driverType: 'OPC_UA' as const,
    endpoint: 'opc.tcp://192.168.10.40:4840',
    opcua: {
      securityPolicy: 'Basic256Sha256' as const,
      securityMode: 'SignAndEncrypt' as const,
      authMode: 'USERNAME' as const,
      username: 'hvac_edge',
      credentialRef: 'edge-secret:opcua-chiller-01',
      subscriptionIntervalMs: '1000',
      nodes: [uaNode],
    },
  };

  const draft: GatewayConfigShadowEditorDraft = {
    pointMappings: [],
    samplingPolicies: [],
    driverConfigs: [bacDriver, uaDriver],
    autonomyPolicy: null,
    reason: '新增 BACnet/IP 与 OPC-UA 南向协议驱动',
  };

  const built = buildValidatedShadowUpdateRequest(draft, undefined);
  assert.equal(built.ok, true);
  if (built.ok) {
    assert.equal(built.request.driverConfigs?.length, 2);
    const bacWire = built.request.driverConfigs?.[0];
    const uaWire = built.request.driverConfigs?.[1];
    assert.equal(bacWire?.unitId, undefined);
    assert.equal(bacWire?.registers, undefined);
    assert.equal(bacWire?.bacnet?.deviceInstance, 2048);
    assert.equal(bacWire?.bacnet?.objects.length, 2);
    assert.equal(uaWire?.unitId, undefined);
    assert.equal(uaWire?.registers, undefined);
    assert.equal(uaWire?.opcua?.securityPolicy, 'Basic256Sha256');
    assert.equal(uaWire?.opcua?.nodes[0]?.namespaceIndex, 2);
    assert.deepEqual(summarizeDriverPointCounts(built.request.driverConfigs), {
      driverCount: 2,
      modbusRegisterCount: 0,
      bacnetObjectCount: 2,
      opcuaNodeCount: 1,
      totalPointCount: 3,
    });
    assert.equal(countDriverRegisters(built.request.driverConfigs), 3);

    // Reorder objects + re-hydrate: unchanged driverConfigs must be omitted on partial PUT.
    const existingShadow = {
      tenantId: '018f3e00-0000-7000-8000-000000000001',
      gatewayId: '018f3e00-4000-7000-8000-000000000100',
      syncStatus: 'SYNCED' as const,
      drift: [],
      desired: {
        desiredRevision: 2,
        pointMappings: [],
        samplingPolicies: [],
        concentratorConfigs: {},
        driverConfigs: built.request.driverConfigs,
        autonomyPolicy: null,
        edgeRuleBundle: null,
        updatedBy: '018f3e00-0000-7000-8000-000000000002',
        reason: '初始下发',
        createdAt: '2026-11-22T00:00:00.000Z',
        updatedAt: '2026-11-22T00:00:00.000Z',
      },
    };
    const rehydrated = createShadowEditorDraft(existingShadow);
    const reordered = {
      ...rehydrated,
      driverConfigs: [
        rehydrated.driverConfigs[1]!,
        {
          ...rehydrated.driverConfigs[0]!,
          bacnet: {
            ...rehydrated.driverConfigs[0]!.bacnet,
            objects: [...rehydrated.driverConfigs[0]!.bacnet.objects].reverse(),
          },
        },
      ],
      autonomyPolicy: createDefaultAutonomyPolicyDraft(),
      reason: '仅启用自治策略',
    };
    const partialReq = buildValidatedShadowUpdateRequest(reordered, existingShadow);
    assert.equal(partialReq.ok, true);
    if (partialReq.ok) {
      assert.equal('driverConfigs' in partialReq.request, false);
    }
  }

  // BACnet & OPC-UA negative validation checks
  const inputWritableRes = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      driverConfigs: [
        {
          ...bacDriver,
          bacnet: {
            ...bacDriver.bacnet,
            objects: [{ ...bacObjRead, pointCode: 'supply_air_temp', objectType: 'ANALOG_INPUT', writable: true }],
          },
        },
      ],
    },
    undefined,
  );
  assert.equal(inputWritableRes.ok, false, 'BACnet ANALOG_INPUT cannot be writable');

  const bacnetSafetyBoundaryRes = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      driverConfigs: [
        {
          ...bacDriver,
          bacnet: {
            ...bacDriver.bacnet,
            objects: [{ ...bacObjWrite, pointCode: 'plc_estop', writable: true }],
          },
        },
      ],
    },
    undefined,
  );
  assert.equal(bacnetSafetyBoundaryRes.ok, false, 'EDGE-003 plc_estop cannot be writable');
  assert.equal(isHardRealtimeProtectionPoint('fire_interlock_trip', '', ''), true);

  const uaMismatchSecurityRes = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      driverConfigs: [
        {
          ...uaDriver,
          opcua: { ...uaDriver.opcua, securityPolicy: 'None', securityMode: 'SignAndEncrypt' },
        },
      ],
    },
    undefined,
  );
  assert.equal(uaMismatchSecurityRes.ok, false, 'securityPolicy None requires securityMode None');

  const uaMissingCredRes = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      driverConfigs: [
        {
          ...uaDriver,
          opcua: { ...uaDriver.opcua, authMode: 'USERNAME', username: 'u', credentialRef: '' },
        },
      ],
    },
    undefined,
  );
  assert.equal(uaMissingCredRes.ok, false, 'OPC-UA USERNAME authMode requires credentialRef');

  const uaBadNodeIdRes = buildValidatedShadowUpdateRequest(
    {
      ...draft,
      driverConfigs: [
        {
          ...uaDriver,
          opcua: {
            ...uaDriver.opcua,
            nodes: [{ ...uaNode, nodeId: 'bad-node-id' }],
          },
        },
      ],
    },
    undefined,
  );
  assert.equal(uaBadNodeIdRes.ok, false, 'invalid OPC-UA NodeId rejected');

  assert.equal(parseOPCUANodeId('i=85').ok, true);
  assert.equal(parseOPCUANodeId('ns=3;g=12345678-1234-5678-9abc-1234567890ab').ok, true);
  assert.equal(parseOPCUANodeId('ns=bad;s=x').ok, false);
});

test('OTA pure model dictionaries, validators, and error messages avoid banned words and cover all backend enums', () => {
  const BANNED = ['回执', '台账', '核算', '待核对', '工况', '认领', '本期间', '通信端点'];
  const allCopy: string[] = [
    ...Object.values(OTA_COMPONENT_LABELS),
    ...Object.values(OTA_ARCH_LABELS),
    ...Object.values(OTA_PAUSE_REASON_LABELS),
    ...Object.values(OTA_TASK_WAVE_LABELS),
    ...Object.values(OTA_TASK_ACTION_LABELS),
    ...Object.values(OTA_PACKAGE_STATUS_PRESENTATION).map((item) => item.label),
    ...Object.values(OTA_CAMPAIGN_STATUS_PRESENTATION).map((item) => item.label),
    ...Object.values(OTA_TASK_STAGE_PRESENTATION).map((item) => item.label),
  ];

  for (const label of allCopy) {
    assert.ok(label.length > 0);
    for (const banned of BANNED) {
      assert.equal(label.includes(banned), false, `OTA copy ${label} must not contain banned term ${banned}`);
    }
  }

  assert.equal(OTA_MAX_PACKAGE_BYTES, 64 * 1024 * 1024);
  assert.equal(formatByteSize(512), '512 B');
  assert.equal(formatByteSize(2048), '2.0 KiB');
  assert.equal(formatByteSize(3 * 1024 * 1024), '3.00 MiB');

  assert.equal(validateSemver('1.5.0'), null);
  assert.equal(validateSemver('1.5.0-rc.1+build7'), null);
  assert.notEqual(validateSemver('v1.5'), null);
  assert.equal(validateOptionalSemver(''), null);
  assert.notEqual(validateOptionalSemver('bad'), null);

  assert.equal(validateSha256Hex(''), null);
  assert.equal(validateSha256Hex('a'.repeat(64)), null);
  assert.notEqual(validateSha256Hex('short'), null);

  assert.deepEqual(parseHardwareModelsInput('EG8200, eg8200 ， EG8200-Pro '), ['EG8200', 'EG8200-Pro']);
  assert.equal(validateHardwareModelsInput('EG8200'), null);

  assert.equal(validateSignaturePair('', ''), null);
  assert.notEqual(validateSignaturePair('key-1', ''), null);
  assert.notEqual(validateSignaturePair('', 'a'.repeat(32)), null);
  assert.equal(validateSignaturePair('key-1', 'a'.repeat(32)), null);

  const validTags = normalizeTargetTagDrafts([
    { key: 'env', value: 'pilot' },
    { key: ' ', value: ' ' },
  ]);
  assert.equal(validTags.ok, true);
  if (validTags.ok) {
    assert.deepEqual(validTags.tags, { env: 'pilot' });
  }
  const dupTags = normalizeTargetTagDrafts([
    { key: 'env', value: 'pilot' },
    { key: 'env', value: 'prod' },
  ]);
  assert.equal(dupTags.ok, false);

  assert.equal(isOTACampaignActive('CANARY_RUNNING'), true);
  assert.equal(isOTACampaignActive('ROLLOUT_RUNNING'), true);
  assert.equal(isOTACampaignActive('PAUSED'), false);
  assert.equal(isOTACampaignActive('COMPLETED'), false);
  assert.equal(
    formatOtaPauseReason('FAILURE_RATE_THRESHOLD_EXCEEDED'),
    '失败率达到熔断阈值，已自动暂停',
  );

  const summary = summarizeOtaWorkspace(
    [
      {
        id: '0193f100-0000-7000-8000-000000000001',
        tenantId: TENANT_ID,
        component: 'EDGE_GATEWAY',
        version: '1.4.0',
        targetOs: 'linux',
        targetArch: 'armv7l',
        hardwareModels: ['EG8200'],
        fileName: 'edge-gateway-1.4.0.tar.gz',
        fileSizeBytes: 1024000,
        sha256: 'a'.repeat(64),
        signatureAlg: 'ED25519',
        signingKeyId: 'platform-ota-key-1',
        signatureB64: 'b'.repeat(64),
        releaseNotes: 'base release',
        status: 'ACTIVE',
        uploadedBy: TENANT_ID,
        createdAt: '2026-11-20T00:00:00Z',
        updatedAt: '2026-11-20T00:00:00Z',
      },
    ],
    [
      {
        id: '0193f200-0000-7000-8000-000000000001',
        tenantId: TENANT_ID,
        name: 'canary-1',
        packageId: '0193f100-0000-7000-8000-000000000001',
        packageVersion: '1.4.0',
        component: 'EDGE_GATEWAY',
        targetOs: 'linux',
        targetArch: 'armv7l',
        targetSiteIds: [SITE_ID],
        targetTags: { env: 'pilot' },
        canaryPercent: 20,
        failureRateThresholdPct: 20,
        healthCheckTimeoutSec: 180,
        autoPromoteRollout: true,
        status: 'PAUSED',
        pauseReason: 'FAILURE_RATE_THRESHOLD_EXCEEDED',
        reason: 'pilot rollout',
        totalCount: 2,
        pendingCount: 0,
        inProgressCount: 0,
        succeededCount: 1,
        failedCount: 1,
        rolledBackCount: 0,
        cancelledCount: 0,
        failureRatePct: 50,
        createdBy: TENANT_ID,
        updatedBy: TENANT_ID,
        createdAt: '2026-11-20T01:00:00Z',
        updatedAt: '2026-11-20T01:10:00Z',
      },
    ],
  );
  assert.equal(summary.activePackages, 1);
  assert.equal(summary.pausedCampaigns, 1);
  assert.equal(summary.circuitBrokenCampaigns, 1);

  const assetModel = createSampleAssetModel();
  const suggested = extractSuggestedTagsFromDevices([
    {
      ...assetModel.devices[0]!,
      deviceType: 'GATEWAY',
      tags: { env: 'pilot', zone: 'north' },
    },
  ]);
  assert.deepEqual(suggested, [
    { key: 'env', value: 'pilot' },
    { key: 'zone', value: 'north' },
  ]);

  assert.equal(
    describeOtaError(
      new PlatformApiError({
        type: 'about:blank',
        title: 'Unprocessable',
        status: 422,
        detail: 'no matching gateways',
        instance: '/api/v1/ota/campaigns',
        code: 'OTA_NO_TARGETS',
        traceId: '0'.repeat(32),
        retryable: false,
      }),
      'fallback',
    ),
    '没有匹配到符合条件的在线或已登记网关，请调整站点或标签筛选条件。',
  );
});
