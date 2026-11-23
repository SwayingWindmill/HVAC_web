import assert from 'node:assert/strict';
import test from 'node:test';
import type { SiteAssetModel, TelemetryPoint } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import type { AssetsSnapshotResult } from '../apps/hvac-web/src/features/assets/model.ts';
import { buildAssetsRows } from '../apps/hvac-web/src/features/assets/model.ts';
import { buildPlantView } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';
import { buildHydraulicSchematic } from '../apps/hvac-web/src/features/operations/realtime/hydraulic-schematic-model.ts';

const TENANT_ID = '018f3d00-0000-7000-8000-000000000001';
const SITE_ID = '018f3e00-1000-7000-8000-000000000001';
const TS = '2026-11-18T00:00:00.000Z';

function makeAsset(
  id: string,
  code: string,
  displayName: string,
  assetType: string,
  extra?: { readonly waterLoop?: 'CHILLED_WATER' | 'CONDENSER_WATER' | 'COOLING_TOWER_LOOP' | null; readonly branchCode?: string | null },
) {
  return {
    id,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    code,
    displayName,
    assetType,
    status: 'ACTIVE' as const,
    revision: 1,
    createdAt: TS,
    updatedAt: TS,
    ...extra,
  };
}

function makeDevice(id: string, code: string, displayName: string, deviceType: string) {
  return {
    id,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    code,
    displayName,
    deviceType,
    status: 'ACTIVE' as const,
    revision: 1,
    createdAt: TS,
    updatedAt: TS,
  };
}

function makeRel(
  id: string,
  fromType: 'ASSET' | 'DEVICE',
  fromId: string,
  toId: string,
  role: string,
) {
  return {
    id,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    fromType,
    fromId,
    toType: 'ASSET' as const,
    toId,
    role,
    status: 'ACTIVE' as const,
    validFrom: TS,
    validTo: null,
    revision: 1,
    createdAt: TS,
    updatedAt: TS,
  };
}

test('parallel hydraulic branches group co-branch equipment and infer ISOLATES valves, falling back cleanly when unconfigured', () => {
  const now = new Date('2026-11-18T00:05:00.000Z');
  const legacyModel: SiteAssetModel = {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [makeAsset('018f3e00-3000-7000-8000-000000000001', 'ch-1', 'CH-01', 'CHILLER')],
    devices: [makeDevice('018f3e00-4000-7000-8000-000000000001', 'dev-ch1', 'CH-01', 'CHILLER_CONTROLLER')],
    sensors: [],
    telemetryPoints: [],
    relationships: [
      makeRel('018f3e00-6400-7000-8000-000000000001', 'DEVICE', '018f3e00-4000-7000-8000-000000000001', '018f3e00-3000-7000-8000-000000000001', 'PRIMARY_CONTROLLER'),
    ],
    counts: { spaces: 0, assets: 1, deviceEndpoints: 1, physicalSensors: 0, points: 0 },
  };
  const unconfigured = buildPlantView(buildAssetsRows({ assetModel: legacyModel, snapshots: new Map(), now }), legacyModel);
  assert.deepEqual(unconfigured.branches, []);
  assert.deepEqual(unconfigured.loopBranches, {});

  const chillerId = '018f3e00-3000-7000-8000-000000000001';
  const pumpId = '018f3e00-3000-7000-8000-000000000002';
  const valveId = '018f3e00-3000-7000-8000-000000000003';
  const devChId = '018f3e00-4000-7000-8000-000000000001';
  const devPumpId = '018f3e00-4000-7000-8000-000000000002';
  const devValveId = '018f3e00-4000-7000-8000-000000000003';

  const branchModel: SiteAssetModel = {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [
      makeAsset(chillerId, 'ch-1', 'CH-01', 'CHILLER', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(pumpId, 'chwp-1', 'CHWP-1', 'CHILLED_WATER_PUMP', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(valveId, 'mv-a', 'MV-A', 'MOTORIZED_VALVE'),
    ],
    devices: [
      makeDevice(devValveId, 'dev-valve1', 'MV-A', 'MOTORIZED_VALVE'),
      makeDevice(devPumpId, 'dev-pump1', 'CHWP-1', 'PUMP_CONTROLLER'),
      makeDevice(devChId, 'dev-ch1', 'CH-01', 'CHILLER_CONTROLLER'),
    ],
    sensors: [],
    telemetryPoints: [],
    relationships: [
      makeRel('018f3e00-6400-7000-8000-000000000001', 'DEVICE', devChId, chillerId, 'PRIMARY_CONTROLLER'),
      makeRel('018f3e00-6400-7000-8000-000000000002', 'DEVICE', devPumpId, pumpId, 'PRIMARY_CONTROLLER'),
      makeRel('018f3e00-6400-7000-8000-000000000003', 'DEVICE', devValveId, valveId, 'PRIMARY_CONTROLLER'),
      makeRel('018f3e00-6600-7000-8000-000000000004', 'ASSET', valveId, chillerId, 'ISOLATES'),
    ],
    counts: { spaces: 0, assets: 3, deviceEndpoints: 3, physicalSensors: 0, points: 0 },
  };
  const configured = buildPlantView(buildAssetsRows({ assetModel: branchModel, snapshots: new Map(), now }), branchModel);
  assert.equal(configured.branches.length, 1);
  assert.equal(configured.branches[0].branchCode, 'CH-A');
  assert.equal(configured.branches[0].loopLabel, '冷冻水环路');
  assert.deepEqual(configured.branches[0].devices.map((d) => d.deviceId), [devChId, devPumpId, devValveId]);
  assert.deepEqual(configured.loopBranches, { CHILLED_WATER: 1 });
});

function makePoint(
  id: string,
  deviceId: string,
  pointCode: string,
  sourceKey: string,
  displayName: string,
  unit: string | null,
  valueType: 'NUMBER' | 'STRING' = 'NUMBER',
): TelemetryPoint {
  return {
    id,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    reportingDeviceId: deviceId,
    sensorId: null,
    pointCode,
    sourceKey,
    displayName,
    pointType: valueType === 'NUMBER' ? ('TELEMETRY' as const) : ('STATE' as const),
    valueType,
    unit,
    writable: false,
    sampleIntervalMs: 5_000,
    publishIntervalMs: 5_000,
    staleAfterMs: 60_000,
    counterDecreaseMode: null,
    counterRolloverModulus: null,
    sourceMetadata: {},
    status: 'ACTIVE' as const,
    revision: 1,
    createdAt: TS,
    updatedAt: TS,
  };
}

function makeSnapshotEntry(
  deviceId: string,
  values: readonly { key: string; value: number | string; unit: string | null; valueType?: 'NUMBER' | 'STRING'; freshness?: 'FRESH' | 'STALE' }[],
): AssetsSnapshotResult {
  return {
    status: 'ok',
    snapshot: {
      schemaVersion: 1,
      deviceId,
      tenantId: TENANT_ID,
      siteId: SITE_ID,
      businessRevision: 1,
      evaluatedAt: TS,
      evaluationAvailability: 'AVAILABLE',
      availabilityReasons: [],
      presence: {
        applicability: 'APPLICABLE',
        currentState: 'ONLINE',
        lastSeenAt: TS,
        policyRevision: 1,
        lastKnown: null,
      },
      telemetryReadiness: 'CURRENT',
      displayState: 'ONLINE',
      values: values.map((entry) => ({
        key: entry.key,
        state: 'PRESENT' as const,
        value: entry.value,
        valueType: entry.valueType ?? 'NUMBER',
        unit: entry.unit,
        sampledAt: TS,
        receivedAt: TS,
        freshness: entry.freshness ?? 'FRESH',
        quality: 'GOOD' as const,
        qualityReasons: [],
        policyRevision: 1,
      })),
    },
  };
}

test('buildHydraulicSchematic projects loop topology, telemetry, and anomalies cleanly', () => {
  const now = new Date('2026-11-18T00:05:00.000Z');
  const ids = {
    chAsset: '018f3e00-3000-7000-8000-000000000101',
    chwpAsset: '018f3e00-3000-7000-8000-000000000102',
    cwpAsset: '018f3e00-3000-7000-8000-000000000103',
    ctAsset: '018f3e00-3000-7000-8000-000000000104',
    btuAsset: '018f3e00-3000-7000-8000-000000000105',
    valveAsset: '018f3e00-3000-7000-8000-000000000106',
    zoneAsset: '018f3e00-3000-7000-8000-000000000107',
    chDev: '018f3e00-4000-7000-8000-000000000101',
    chwpDev: '018f3e00-4000-7000-8000-000000000102',
    cwpDev: '018f3e00-4000-7000-8000-000000000103',
    ctDev: '018f3e00-4000-7000-8000-000000000104',
    btuDev: '018f3e00-4000-7000-8000-000000000105',
    valveDev: '018f3e00-4000-7000-8000-000000000106',
    zoneDev: '018f3e00-4000-7000-8000-000000000107',
  };

  // Meter-only site has no hydraulic equipment topology.
  const meterOnlyModel: SiteAssetModel = {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [makeAsset(ids.btuAsset, 'btu-1', 'BTU-01', 'BTU_METER')],
    devices: [makeDevice(ids.btuDev, 'dev-btu', 'BTU-01', 'BTU_METER')],
    sensors: [],
    telemetryPoints: [],
    relationships: [makeRel('018f3e00-6400-7000-8000-000000000199', 'DEVICE', ids.btuDev, ids.btuAsset, 'PRIMARY_CONTROLLER')],
    counts: { spaces: 0, assets: 1, deviceEndpoints: 1, physicalSensors: 0, points: 0 },
  };
  const emptyView = buildHydraulicSchematic(buildPlantView(buildAssetsRows({ assetModel: meterOnlyModel, snapshots: new Map(), now }), meterOnlyModel));
  assert.equal(emptyView.hasTopology, false);

  const assetModel: SiteAssetModel = {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [
      makeAsset(ids.chAsset, 'ch-1', 'CHILLER-01', 'CHILLER', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(ids.chwpAsset, 'chwp-1', 'CHWP-01', 'CHILLED_WATER_PUMP', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(ids.cwpAsset, 'cwp-1', 'CWP-01', 'COOLING_WATER_PUMP', { waterLoop: 'CONDENSER_WATER', branchCode: 'CW-A' }),
      makeAsset(ids.ctAsset, 'ct-1', 'CT-01', 'COOLING_TOWER', { waterLoop: 'CONDENSER_WATER', branchCode: 'CW-A' }),
      makeAsset(ids.btuAsset, 'btu-1', 'BTU-01', 'BTU_METER'),
      makeAsset(ids.valveAsset, 'mv-1', 'MV-CH-A', 'MOTORIZED_VALVE'),
      makeAsset(ids.zoneAsset, 'zone-1', 'ZONE-01', 'ZONE_TEMPERATURE_SENSOR'),
    ],
    devices: [
      makeDevice(ids.chDev, 'dev-ch', 'CHILLER-01', 'CHILLER_CONTROLLER'),
      makeDevice(ids.chwpDev, 'dev-chwp', 'CHWP-01', 'PUMP_CONTROLLER'),
      makeDevice(ids.cwpDev, 'dev-cwp', 'CWP-01', 'PUMP_CONTROLLER'),
      makeDevice(ids.ctDev, 'dev-ct', 'CT-01', 'COOLING_TOWER_CONTROLLER'),
      makeDevice(ids.btuDev, 'dev-btu', 'BTU-01', 'BTU_METER'),
      makeDevice(ids.valveDev, 'dev-mv', 'MV-CH-A', 'MOTORIZED_VALVE'),
      makeDevice(ids.zoneDev, 'dev-zone', 'ZONE-01', 'ZONE_TEMPERATURE_SENSOR'),
    ],
    sensors: [],
    telemetryPoints: [
      makePoint('p-1', ids.chDev, 'run_state', 'chiller.run_state', '运行状态', null, 'STRING'),
      makePoint('p-2', ids.chDev, 'load', 'chiller.compressor_load', '负荷率', '%'),
      makePoint('p-3', ids.chDev, 'chw_out', 'chiller.leaving_chilled_water_temperature', '冷冻出水', 'Cel'),
      makePoint('p-4', ids.chwpDev, 'run_state', 'chwp.run_state', '运行状态', null, 'STRING'),
      makePoint('p-5', ids.chwpDev, 'freq', 'chwp.frequency', '运行频率', 'Hz'),
      makePoint('p-6', ids.cwpDev, 'run_state', 'cwp.run_state', '运行状态', null, 'STRING'),
      makePoint('p-7', ids.cwpDev, 'freq', 'cwp.frequency', '运行频率', 'Hz'),
      makePoint('p-8', ids.cwpDev, 'flow', 'cwp.flow_rate', '流量', 'm3/h'),
      makePoint('p-9', ids.ctDev, 'run_state', 'cooling_tower.run_state', '运行状态', null, 'STRING'),
      makePoint('p-10', ids.ctDev, 'fan', 'cooling_tower.fan_speed', '风机转速', '%'),
      makePoint('p-11', ids.btuDev, 'sup', 'btu_meter.supply_water_temperature', '供水温度', 'Cel'),
      makePoint('p-12', ids.btuDev, 'ret', 'btu_meter.return_water_temperature', '回水温度', 'Cel'),
      makePoint('p-13', ids.btuDev, 'dt', 'btu_meter.temperature_difference', '供回温差', 'Cel'),
      makePoint('p-14', ids.btuDev, 'flow', 'btu_meter.flow_rate', '流量', 'm3/h'),
      makePoint('p-15', ids.valveDev, 'open_state', 'valve.open_state', '阀门状态', null, 'STRING'),
      makePoint('p-16', ids.zoneDev, 'temp', 'zone.temperature', '室内温度', 'Cel'),
    ],
    relationships: [
      makeRel('r-1', 'DEVICE', ids.chDev, ids.chAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-2', 'DEVICE', ids.chwpDev, ids.chwpAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-3', 'DEVICE', ids.cwpDev, ids.cwpAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-4', 'DEVICE', ids.ctDev, ids.ctAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-5', 'DEVICE', ids.btuDev, ids.btuAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-6', 'DEVICE', ids.valveDev, ids.valveAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-7', 'DEVICE', ids.zoneDev, ids.zoneAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-8', 'ASSET', ids.valveAsset, ids.chAsset, 'ISOLATES'),
    ],
    counts: { spaces: 0, assets: 7, deviceEndpoints: 7, physicalSensors: 0, points: 16 },
  };

  const buildWith = (overrides: { flow?: number; deltaT?: number; flowFreshness?: 'FRESH' | 'STALE'; valveState?: 'OPEN' | 'CLOSED' }) => {
    const snapshots = new Map<string, AssetsSnapshotResult>([
      [ids.chDev, makeSnapshotEntry(ids.chDev, [
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
        { key: 'load', value: 68, unit: '%' },
        { key: 'chw_out', value: 7.0, unit: 'Cel' },
      ])],
      [ids.chwpDev, makeSnapshotEntry(ids.chwpDev, [
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
        { key: 'freq', value: 42.5, unit: 'Hz' },
      ])],
      [ids.cwpDev, makeSnapshotEntry(ids.cwpDev, [
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
        { key: 'freq', value: 45.0, unit: 'Hz' },
        { key: 'flow', value: 165, unit: 'm3/h' },
      ])],
      [ids.ctDev, makeSnapshotEntry(ids.ctDev, [
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
        { key: 'fan', value: 80, unit: '%' },
      ])],
      [ids.btuDev, makeSnapshotEntry(ids.btuDev, [
        { key: 'sup', value: 7.0, unit: 'Cel' },
        { key: 'ret', value: 12.2, unit: 'Cel' },
        { key: 'dt', value: overrides.deltaT ?? 5.2, unit: 'Cel' },
        { key: 'flow', value: overrides.flow ?? 132, unit: 'm3/h', freshness: overrides.flowFreshness ?? 'FRESH' },
      ])],
      [ids.valveDev, makeSnapshotEntry(ids.valveDev, [
        { key: 'open_state', value: overrides.valveState ?? 'OPEN', unit: null, valueType: 'STRING' },
      ])],
      [ids.zoneDev, makeSnapshotEntry(ids.zoneDev, [
        { key: 'temp', value: 24.1, unit: 'Cel' },
      ])],
    ]);
    return buildHydraulicSchematic(buildPlantView(buildAssetsRows({ assetModel, snapshots, now }), assetModel));
  };

  const normal = buildWith({});
  assert.equal(normal.hasTopology, true);
  assert.equal(normal.hasCondenserLoop, true);
  assert.equal(normal.chilledFlowActive, true);
  assert.equal(normal.coolingFlowActive, true);
  assert.equal(normal.towers.length, 1);
  assert.equal(normal.coolingPumps.length, 1);
  assert.equal(normal.chillers.length, 1);
  assert.equal(normal.chilledPumps.length, 1);
  assert.equal(normal.zones.length, 1);
  assert.equal(normal.chillers[0]?.valves[0]?.label, '全开');
  assert.equal(normal.chilled.flowRate?.numeric, 132);
  assert.deepEqual(normal.anomalies, []);

  // Pump running + fresh zero flow triggers CHILLED_PUMP_RUNNING_ZERO_FLOW.
  const zeroFlow = buildWith({ flow: 0 });
  assert.ok(zeroFlow.anomalies.some((item) => item.code === 'CHILLED_PUMP_RUNNING_ZERO_FLOW'));

  // Stale zero flow must NOT fabricate CHILLED_PUMP_RUNNING_ZERO_FLOW.
  const staleFlow = buildWith({ flow: 0, flowFreshness: 'STALE' });
  assert.equal(staleFlow.anomalies.some((item) => item.code === 'CHILLED_PUMP_RUNNING_ZERO_FLOW'), false);

  // High flow + low delta-T raises LOW_DELTA_T_HIGH_FLOW.
  const lowDeltaT = buildWith({ flow: 140, deltaT: 1.6 });
  assert.ok(lowDeltaT.anomalies.some((item) => item.code === 'LOW_DELTA_T_HIGH_FLOW'));

  // Running branch + closed isolation valve raises BRANCH_VALVE_CLOSED_WHILE_RUNNING.
  const closedValve = buildWith({ valveState: 'CLOSED' });
  const closedAnomaly = closedValve.anomalies.find((item) => item.code === 'BRANCH_VALVE_CLOSED_WHILE_RUNNING');
  assert.ok(closedAnomaly);
  assert.equal(closedAnomaly.deviceId, ids.valveDev);
});

import { equipmentStatus, soleReading } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';
import type { AssetsDeviceRow } from '../apps/hvac-web/src/features/assets/model.ts';

test('buildHydraulicSchematic deduplicates multi-device assets, keeps bare controller run_state, and excludes PROVISIONAL_READINGS headers from machine columns', () => {
  const now = new Date('2026-11-18T00:05:00.000Z');
  const ids = {
    chAsset: '018f3e00-3000-7000-8000-000000000201',
    chwpAsset: '018f3e00-3000-7000-8000-000000000202',
    ctAsset: '018f3e00-3000-7000-8000-000000000203',
    meterChDev: '018f3e00-4000-7000-8000-000000000201',
    ctrlChDev: '018f3e00-4000-7000-8000-000000000202',
    chwpDev: '018f3e00-4000-7000-8000-000000000203',
    ctDev: '018f3e00-4000-7000-8000-000000000204',
    provTowerDev: '018f3e00-4000-7000-8000-000000000205',
    provChwpDev: '018f3e00-4000-7000-8000-000000000206',
    provOutdoorDev: '018f3e00-4000-7000-8000-000000000207',
    provRoomDev: '018f3e00-4000-7000-8000-000000000208',
    et1010Dev: '018f3e00-4000-7000-8000-000000000209',
  };

  const assetModel: SiteAssetModel = {
    schemaVersion: 2,
    tenantId: TENANT_ID,
    siteId: SITE_ID,
    spaces: [],
    assets: [
      makeAsset(ids.chAsset, 'chiller-a', 'A 机组', 'CHILLER', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(ids.chwpAsset, 'chwp-1', '1 号冷冻泵', 'CHILLED_WATER_PUMP', { waterLoop: 'CHILLED_WATER', branchCode: 'CH-A' }),
      makeAsset(ids.ctAsset, 'tower-1', '1 号冷却塔', 'COOLING_TOWER'),
    ],
    devices: [
      makeDevice(ids.ctrlChDev, 'ctrl-chiller-a', '1# 螺杆主机控制器', 'CHILLER_CONTROLLER'),
      makeDevice(ids.meterChDev, 'meter-uid4', 'A 机组电表', 'HVAC_POWER_METER'),
      makeDevice(ids.chwpDev, 'ctrl-chwp-1', '1# 冻水泵控制器', 'PUMP_CONTROLLER'),
      makeDevice(ids.ctDev, 'ctrl-tower-1', '1# 冷却塔控制器', 'COOLING_TOWER_CONTROLLER'),
      makeDevice(ids.provTowerDev, 'cooling-tower', '冷却塔', 'PROVISIONAL_READINGS'),
      makeDevice(ids.provChwpDev, 'chwp-main', '冷冻水泵（总管）', 'PROVISIONAL_READINGS'),
      makeDevice(ids.provOutdoorDev, 'outdoor', '室外环境', 'PROVISIONAL_READINGS'),
      makeDevice(ids.provRoomDev, 'room', '房间', 'PROVISIONAL_READINGS'),
      makeDevice(ids.et1010Dev, 'et1010-uid31', 'ET1010 控制模块', 'CONTROL_MODULE'),
    ],
    sensors: [],
    telemetryPoints: [
      makePoint('p-201', ids.meterChDev, 'power_total', 'chiller.power', '有功功率', 'kW'),
      makePoint('p-202', ids.meterChDev, 'chw_out', 'chiller.leaving_chilled_water_temperature', '冷冻出水', 'Cel'),
      makePoint('p-203', ids.meterChDev, 'run_state', 'chiller.run_state', '运行状态', null, 'STRING'),
      makePoint('p-204', ids.ctrlChDev, 'run_state', 'run_state', '1# 主机运行状态', null, 'STRING'),
      makePoint('p-205', ids.chwpDev, 'run_state', 'run_state', '1# 冻水泵运行状态', null, 'STRING'),
      makePoint('p-206', ids.ctDev, 'run_state', 'run_state', '1# 冷却塔运行状态', null, 'STRING'),
      makePoint('p-207', ids.provTowerDev, 'leaving_water_temperature', 'cooling_tower.leaving_water_temperature', '冷却塔出水温度', 'Cel'),
      makePoint('p-208', ids.provChwpDev, 'run_state', 'chwp.run_state', '运行状态', null, 'STRING'),
      makePoint('p-209', ids.provChwpDev, 'flow_rate', 'chwp.flow_rate', '冷冻水流量', 'm3/h'),
      makePoint('p-210', ids.provOutdoorDev, 'ambient_temp', 'weather.ambient_dry_bulb_temperature', '室外温度', 'Cel'),
      makePoint('p-211', ids.provRoomDev, 'room_temp', 'zone.temperature', '室内温度', 'Cel'),
      makePoint('p-212', ids.et1010Dev, 'run_state', 'run_state', '保持通道状态', null, 'STRING'),
    ],
    relationships: [
      makeRel('r-201', 'DEVICE', ids.meterChDev, ids.chAsset, 'MEASURES'),
      makeRel('r-202', 'DEVICE', ids.ctrlChDev, ids.chAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-203', 'DEVICE', ids.chwpDev, ids.chwpAsset, 'PRIMARY_CONTROLLER'),
      makeRel('r-204', 'DEVICE', ids.ctDev, ids.ctAsset, 'PRIMARY_CONTROLLER'),
    ],
    counts: { spaces: 0, assets: 3, deviceEndpoints: 9, physicalSensors: 0, points: 12 },
  };

  const makeCtrlSnap = (off: boolean): AssetsSnapshotResult => {
    const base = makeSnapshotEntry(ids.ctrlChDev, [{ key: 'run_state', value: 'UNKNOWN', unit: null, valueType: 'STRING' }]);
    if (base.status !== 'ok') return base;
    return {
      status: 'ok',
      snapshot: {
        ...base.snapshot,
        presence: {
          applicability: 'APPLICABLE' as const,
          currentState: off ? ('OFFLINE' as const) : ('ONLINE' as const),
          lastSeenAt: TS,
          policyRevision: 1,
          lastKnown: null,
        },
      },
    };
  };

  const buildWithCtrl = (ctrlOffline: boolean) => {
    const snapshots = new Map<string, AssetsSnapshotResult>([
      [ids.meterChDev, makeSnapshotEntry(ids.meterChDev, [
        { key: 'power_total', value: 115, unit: 'kW' },
        { key: 'chw_out', value: 7.1, unit: 'Cel' },
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
      ])],
      [ids.ctrlChDev, ctrlOffline ? makeCtrlSnap(true) : makeSnapshotEntry(ids.ctrlChDev, [{ key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' }])],
      [ids.chwpDev, makeSnapshotEntry(ids.chwpDev, [{ key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' }])],
      [ids.ctDev, makeSnapshotEntry(ids.ctDev, [{ key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' }])],
      [ids.provTowerDev, makeSnapshotEntry(ids.provTowerDev, [{ key: 'leaving_water_temperature', value: 23.7, unit: 'Cel' }])],
      [ids.provChwpDev, makeSnapshotEntry(ids.provChwpDev, [
        { key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' },
        { key: 'flow_rate', value: 118, unit: 'm3/h' },
      ])],
      [ids.provOutdoorDev, makeSnapshotEntry(ids.provOutdoorDev, [{ key: 'ambient_temp', value: 20.8, unit: 'Cel' }])],
      [ids.provRoomDev, makeSnapshotEntry(ids.provRoomDev, [{ key: 'room_temp', value: 24.0, unit: 'Cel' }])],
      [ids.et1010Dev, makeSnapshotEntry(ids.et1010Dev, [{ key: 'run_state', value: 'RUNNING', unit: null, valueType: 'STRING' }])],
    ]);
    const rows: readonly AssetsDeviceRow[] = buildAssetsRows({ assetModel, snapshots, now });
    return buildPlantView(rows, assetModel);
  };

  const plant = buildWithCtrl(false);
  const schematic = buildHydraulicSchematic(plant);

  assert.equal(schematic.chillers.length, 1);
  assert.equal(schematic.chillers[0].device.name, 'A 机组');
  assert.deepEqual(schematic.chillers[0].device.deviceIds, [ids.meterChDev, ids.ctrlChDev]);
  assert.equal(schematic.chillers[0].device.runState, 'RUNNING');
  assert.equal(schematic.chillers[0].primaryReading?.numeric, 115);
  assert.equal(schematic.chillers[0].secondaryReading?.numeric, 7.1);
  assert.equal(soleReading(plant, 'CHILLER', 'chiller.leaving_chilled_water_temperature')?.numeric, 7.1);
  assert.equal(schematic.towers.length, 1);
  assert.equal(schematic.chilledPumps.length, 1);
  assert.equal(schematic.zones.length, 1);
  assert.equal(schematic.weather?.deviceId, ids.provOutdoorDev);
  assert.equal(schematic.condenser.leavingTowerTemp?.numeric, 23.7);
  assert.equal(schematic.chilled.flowRate?.numeric, 118);
  assert.equal(schematic.chilledFlowActive, true);
  assert.equal(plant.branches.length, 1);
  assert.equal(plant.branches[0].equipmentCount, 2);
  assert.equal(plant.branches[0].runningCount, 2);
  assert.deepEqual(equipmentStatus(plant), { total: 3, running: 3, faults: 0, offline: 0, withoutData: 0, stale: 0 });

  // Unbound PROVISIONAL_READINGS classify as METER while keeping chwp.run_state for interlocks;
  // CONTROL_MODULE bare run_state stays UNKNOWN.
  const chwpMain = plant.devices.find((device) => device.deviceId === ids.provChwpDev);
  assert.equal(chwpMain?.category, 'METER');
  assert.equal(chwpMain?.runState, 'RUNNING');
  assert.equal(plant.devices.find((device) => device.deviceId === ids.provTowerDev)?.category, 'METER');
  assert.equal(plant.devices.find((device) => device.deviceId === ids.et1010Dev)?.runState, 'UNKNOWN');

  // Partial offline sibling: A 机组 stays ONLINE & RUNNING via its meter, while flagging hasIssue / offlineMemberCount.
  const partialPlant = buildWithCtrl(true);
  const partialSchematic = buildHydraulicSchematic(partialPlant);
  assert.equal(partialSchematic.chillers[0].device.connection, 'ONLINE');
  assert.equal(partialSchematic.chillers[0].device.offlineMemberCount, 1);
  assert.equal(partialSchematic.chillers[0].hasIssue, true);
  assert.equal(equipmentStatus(partialPlant).offline, 1);
});
