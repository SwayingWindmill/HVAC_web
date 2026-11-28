import assert from 'node:assert/strict';
import test from 'node:test';
// The web's own pipeline: the device rows the 实时运行 and 设备 pages build, then the plant view.
import { buildAssetsRows } from '../../hvac-web/src/features/assets/model.ts';
import {
  EQUIPMENT_CATEGORIES as WEB_EQUIPMENT_CATEGORIES,
  PLANT_CATEGORY_LABELS as WEB_CATEGORY_LABELS,
  PLANT_CATEGORY_ORDER as WEB_CATEGORY_ORDER,
  WATER_LOOP_LABELS as WEB_LOOP_LABELS,
  WATER_LOOP_ORDER as WEB_LOOP_ORDER,
  buildPlantView,
} from '../../hvac-web/src/features/operations/realtime/plant-model.ts';
import {
  cleanBranchCode,
  classifyPlantCategory,
  isPlantDeviceRunning,
  parseRunState,
  parseWaterLoop,
} from '../../hvac-web/src/domain/plantTopology.ts';
import {
  CATEGORY_LABELS as PHONE_CATEGORY_LABELS,
  EQUIPMENT_CATEGORIES as PHONE_EQUIPMENT_CATEGORIES,
  WATER_LOOP_LABELS as PHONE_LOOP_LABELS,
  WATER_LOOP_ORDER as PHONE_LOOP_ORDER,
  buildPlant,
} from '../miniprogram/lib/plant.ts';
import { assetModel, recordedAt, snapshots } from './recorded.ts';

const now = recordedAt;

// The same plant on a bad day: the chiller offline, a pump's readings late and partly missing, the meter unreadable.
function badDay() {
  const items = structuredClone(snapshots.items);
  const deviceNamed = (prefix: string) => assetModel.devices.find((device: { displayName: string }) => device.displayName.startsWith(prefix)).id;
  const item = (prefix: string) => items.find((candidate: { deviceId: string }) => candidate.deviceId === deviceNamed(prefix));
  item('CHILLER-01').snapshot.presence.currentState = 'OFFLINE';
  const pump = item('CHWP-01').snapshot;
  pump.values[0] = { ...pump.values[0], freshness: 'STALE' };
  pump.values[1] = { key: pump.values[1].key, state: 'MISSING', missingReason: 'NEVER_OBSERVED' };
  Object.assign(item('METER-HVAC-TOTAL'), { status: 'ERROR', snapshot: undefined, problem: { code: 'TELEMETRY_UNAVAILABLE' } });
  return { ...snapshots, items };
}

for (const [day, recorded] of [['a normal day', snapshots], ['a bad day', badDay()]] as const) test(`the mini program reads the plant exactly as the web does on ${day}: every device, its state and every reading`, () => {
  const snapshots = recorded;
  const byDeviceId = new Map(snapshots.items.map((item: { deviceId: string; status: string; snapshot: unknown; problem: unknown }) => [
    item.deviceId, item.status === 'OK' ? { status: 'ok', snapshot: item.snapshot } : { status: 'error', problem: item.problem },
  ]));
  const web = buildPlantView(buildAssetsRows({ assetModel, snapshots: byDeviceId as never, now }));
  const phone = buildPlant(assetModel, snapshots, now);

  const sourceKeys = (deviceId: string) => assetModel.telemetryPoints
    .filter((point: { reportingDeviceId: string; status: string }) => point.reportingDeviceId === deviceId && point.status === 'ACTIVE')
    .map((point: { sourceKey: string }) => point.sourceKey);
  const summary = (device: { deviceId: string; name: string; category: string; connection: string; runState: string; latestSampleAt: string | null; hasStaleData: boolean; reading: (key: string) => { label: string; display: string; unit: string | null; numeric: number | null; current: boolean } | null }) => ({
    deviceId: device.deviceId, name: device.name, category: device.category, connection: device.connection, runState: device.runState,
    latestSampleAt: device.latestSampleAt, hasStaleData: device.hasStaleData,
    readings: sourceKeys(device.deviceId).map((key: string) => {
      const reading = device.reading(key);
      return reading === null ? [key, null] : [key, reading.label, reading.display, reading.unit, reading.numeric, reading.current];
    }),
  });

  assert.ok(web.devices.length > 0);
  // The web lists devices by room and asset; the phone lists them by role, so compare them device by device.
  const byId = (left: { deviceId: string }, right: { deviceId: string }) => left.deviceId.localeCompare(right.deviceId);
  assert.deepEqual([...phone.devices].sort(byId).map(summary), [...web.devices].sort(byId).map(summary));
  assert.deepEqual(phone.groups.map((group) => [group.category, group.devices.map((device) => device.deviceId)]),
    web.groups.map((group) => [group.category, group.devices.map((device) => device.deviceId)]));
  assert.deepEqual([phone.onlineCount, phone.latestSampleAt, phone.plantCop, phone.totalPower?.display, phone.coolingCapacity?.display],
    [web.onlineCount, web.latestSampleAt, web.plantCop, web.totalPower?.display, web.coolingCapacity?.display]);
  assert.deepEqual(phone.branches, []);
  assert.deepEqual(web.branches, []);
});

test('the web and mini program group chilled and condenser parallel branches, inferred ISOLATES valves, and DP readings identically', () => {
  const clonedModel = structuredClone(assetModel);
  const clonedSnapshots = structuredClone(snapshots);
  const ts = recordedAt.toISOString();

  const chillerAsset = clonedModel.assets.find((asset: { assetType: string }) => asset.assetType === 'CHILLER');
  const chwpAsset = clonedModel.assets.find((asset: { assetType: string }) => asset.assetType === 'CHILLED_WATER_PUMP');
  const cwpAsset = clonedModel.assets.find((asset: { assetType: string }) => asset.assetType === 'COOLING_WATER_PUMP');
  const btuDevice = clonedModel.devices.find((device: { deviceType: string }) => device.deviceType === 'BTU_METER');
  const firstPoint = clonedModel.telemetryPoints[0];
  const firstSnap = clonedSnapshots.items.find((item: { status: string }) => item.status === 'OK').snapshot;

  chillerAsset.waterLoop = 'CHILLED_WATER';
  chillerAsset.branchCode = 'CH-A';
  chwpAsset.waterLoop = 'CHILLED_WATER';
  chwpAsset.branchCode = 'CH-A';
  cwpAsset.waterLoop = 'CONDENSER_WATER';
  cwpAsset.branchCode = 'CW-A';

  const valveAssetId = '018f3e00-3000-7000-8000-000000000091';
  const valveDeviceId = '018f3e00-4000-7000-8000-000000000091';
  const dpAssetId = '018f3e00-3000-7000-8000-000000000092';
  const dpDeviceId = '018f3e00-4000-7000-8000-000000000092';

  // Omit branchCode on the valve so both web and mini program verify ISOLATES branch inference.
  clonedModel.assets.push(
    {
      ...chillerAsset,
      id: valveAssetId,
      code: 'mv-chw-a',
      displayName: 'CH-A 支路电动阀',
      assetType: 'MOTORIZED_VALVE',
      waterLoop: null,
      branchCode: null,
    },
    {
      ...chillerAsset,
      id: dpAssetId,
      code: 'chw-dp-01',
      displayName: '冷冻总管压差传感器',
      assetType: 'DIFFERENTIAL_PRESSURE_SENSOR',
      waterLoop: 'CHILLED_WATER',
      branchCode: null,
    },
  );
  clonedModel.devices.push(
    { ...btuDevice, id: valveDeviceId, code: 'mv-chw-a-ctrl', displayName: 'CH-A 支路电动阀', deviceType: 'MOTORIZED_VALVE' },
    { ...btuDevice, id: dpDeviceId, code: 'chw-dp-01', displayName: '冷冻总管压差传感器', deviceType: 'DIFFERENTIAL_PRESSURE_SENSOR' },
  );
  clonedModel.relationships.push(
    {
      id: '018f3e00-6400-7000-8000-000000000091',
      tenantId: chillerAsset.tenantId,
      siteId: chillerAsset.siteId,
      fromType: 'DEVICE',
      fromId: valveDeviceId,
      toType: 'ASSET',
      toId: valveAssetId,
      role: 'PRIMARY_CONTROLLER',
      status: 'ACTIVE',
      validFrom: ts,
      validTo: null,
      revision: 1,
      createdAt: ts,
      updatedAt: ts,
    },
    {
      id: '018f3e00-6400-7000-8000-000000000092',
      tenantId: chillerAsset.tenantId,
      siteId: chillerAsset.siteId,
      fromType: 'DEVICE',
      fromId: dpDeviceId,
      toType: 'ASSET',
      toId: dpAssetId,
      role: 'MEASURES',
      status: 'ACTIVE',
      validFrom: ts,
      validTo: null,
      revision: 1,
      createdAt: ts,
      updatedAt: ts,
    },
    {
      id: '018f3e00-6600-7000-8000-000000000093',
      tenantId: chillerAsset.tenantId,
      siteId: chillerAsset.siteId,
      fromType: 'ASSET',
      fromId: valveAssetId,
      toType: 'ASSET',
      toId: chillerAsset.id,
      role: 'ISOLATES',
      status: 'ACTIVE',
      validFrom: ts,
      validTo: null,
      revision: 1,
      createdAt: ts,
      updatedAt: ts,
    },
  );
  clonedModel.telemetryPoints.push(
    {
      ...firstPoint,
      id: '018f3e00-5000-7000-8000-000000000091',
      reportingDeviceId: valveDeviceId,
      pointCode: 'valve_open_state',
      sourceKey: 'valve.open_state',
      displayName: '阀门状态',
      pointType: 'STATE',
      valueType: 'STRING',
      unit: null,
      writable: false,
      status: 'ACTIVE',
    },
    {
      ...firstPoint,
      id: '018f3e00-5000-7000-8000-000000000092',
      reportingDeviceId: valveDeviceId,
      pointCode: 'valve_position',
      sourceKey: 'valve.position_feedback',
      displayName: '阀门开度反馈',
      pointType: 'TELEMETRY',
      valueType: 'NUMBER',
      unit: '%',
      writable: false,
      status: 'ACTIVE',
    },
    {
      ...firstPoint,
      id: '018f3e00-5000-7000-8000-000000000093',
      reportingDeviceId: dpDeviceId,
      pointCode: 'header_dp',
      sourceKey: 'dp_sensor.differential_pressure',
      displayName: '总管压差',
      pointType: 'TELEMETRY',
      valueType: 'NUMBER',
      unit: 'kPa',
      writable: false,
      status: 'ACTIVE',
    },
  );
  const sampleVal = firstSnap.values[0];
  clonedSnapshots.items.push(
    {
      requestId: 'req-valve',
      deviceId: valveDeviceId,
      status: 'OK',
      snapshot: {
        ...firstSnap,
        deviceId: valveDeviceId,
        values: [
          { ...sampleVal, key: 'valve_open_state', value: 'OPEN', valueType: 'STRING', unit: null },
          { ...sampleVal, key: 'valve_position', value: 100, valueType: 'NUMBER', unit: '%' },
        ],
      },
    },
    {
      requestId: 'req-dp',
      deviceId: dpDeviceId,
      status: 'OK',
      snapshot: {
        ...firstSnap,
        deviceId: dpDeviceId,
        values: [
          { ...sampleVal, key: 'header_dp', value: 84.5, valueType: 'NUMBER', unit: 'kPa' },
        ],
      },
    },
  );

  const byDeviceId = new Map(clonedSnapshots.items.map((item: { deviceId: string; status: string; snapshot: unknown; problem: unknown }) => [
    item.deviceId, item.status === 'OK' ? { status: 'ok', snapshot: item.snapshot } : { status: 'error', problem: item.problem },
  ]));
  const web = buildPlantView(buildAssetsRows({ assetModel: clonedModel, snapshots: byDeviceId as never, now }), clonedModel);
  const phone = buildPlant(clonedModel, clonedSnapshots, now);

  assert.deepEqual(phone.loopBranches, web.loopBranches);
  assert.deepEqual(web.loopBranches, { CHILLED_WATER: 1, CONDENSER_WATER: 1 });
  const branchShape = (branch: { key: string; waterLoop: string | null; loopLabel: string; branchCode: string; equipmentCount: number; runningCount: number; hasAlert: boolean; devices: readonly { deviceId: string; waterLoop: string | null; branchCode: string | null }[] }) => ({
    key: branch.key,
    waterLoop: branch.waterLoop,
    loopLabel: branch.loopLabel,
    branchCode: branch.branchCode,
    equipmentCount: branch.equipmentCount,
    runningCount: branch.runningCount,
    hasAlert: branch.hasAlert,
    devices: branch.devices.map((d) => [d.deviceId, d.waterLoop, d.branchCode]),
  });
  assert.deepEqual(phone.branches.map(branchShape), web.branches.map(branchShape));
  assert.equal(web.branches[0].devices.length, 3);
  assert.equal(web.branches[0].devices[2].deviceId, valveDeviceId);
});

test('web and mini program share identical plant taxonomy, water-loop dictionaries, and helper guards', () => {
  assert.deepEqual(PHONE_CATEGORY_LABELS, WEB_CATEGORY_LABELS);
  assert.deepEqual([...WEB_CATEGORY_ORDER].sort(), Object.keys(WEB_CATEGORY_LABELS).sort());
  assert.deepEqual(PHONE_LOOP_ORDER, WEB_LOOP_ORDER);
  assert.deepEqual(PHONE_LOOP_LABELS, WEB_LOOP_LABELS);
  assert.deepEqual([...PHONE_EQUIPMENT_CATEGORIES], [...WEB_EQUIPMENT_CATEGORIES]);

  assert.equal(classifyPlantCategory({ assetType: 'CHILLER', sourceKeys: [], deviceType: 'OTHER' }), 'CHILLER');
  assert.equal(classifyPlantCategory({ assetType: null, sourceKeys: ['dp_sensor.differential_pressure'], deviceType: 'UNKNOWN' }), 'METER');
  assert.equal(classifyPlantCategory({ assetType: null, sourceKeys: ['custom.value'], deviceType: 'ISOLATION_VALVE' }), 'ACQUISITION');
  assert.equal(classifyPlantCategory({ assetType: null, sourceKeys: ['custom.value'], deviceType: 'UNKNOWN' }), 'OTHER');

  assert.equal(parseWaterLoop('CHILLED_WATER'), 'CHILLED_WATER');
  assert.equal(parseWaterLoop('UNKNOWN'), null);
  assert.equal(cleanBranchCode('  CH-A  '), 'CH-A');
  assert.equal(cleanBranchCode('   '), null);
  assert.equal(parseRunState('RUNNING'), 'RUNNING');
  assert.equal(parseRunState('STARTING'), 'UNKNOWN');

  assert.equal(isPlantDeviceRunning({ connection: 'ONLINE', runState: 'RUNNING' }), true);
  assert.equal(isPlantDeviceRunning({ connection: 'SLEEPING', runState: 'RUNNING' }), false);
  assert.equal(isPlantDeviceRunning({ connection: 'OFFLINE', runState: 'RUNNING' }), false);
});

test('web and mini program produce identical PlantUnit aggregation for dual-bound chillers, bare run_state, and PROVISIONAL_READINGS headers', () => {
  const clonedModel = structuredClone(assetModel);
  const clonedSnapshots = structuredClone(snapshots);
  const ts = recordedAt.toISOString();

  const chillerAsset = clonedModel.assets.find((asset: { assetType: string }) => asset.assetType === 'CHILLER');
  const templateDevice = clonedModel.devices.find((device: { deviceType: string }) => device.deviceType === 'CHILLER_CONTROLLER');
  const firstPoint = clonedModel.telemetryPoints[0];
  const firstSnap = clonedSnapshots.items.find((item: { status: string }) => item.status === 'OK').snapshot;

  chillerAsset.waterLoop = 'CHILLED_WATER';
  chillerAsset.branchCode = 'CH-A';

  const ctrlId = '018f3e00-4000-7000-8000-000000000701';
  const chwpMainId = '018f3e00-4000-7000-8000-000000000702';
  const ctHeaderId = '018f3e00-4000-7000-8000-000000000703';

  clonedModel.devices.push(
    { ...templateDevice, id: ctrlId, code: 'ctrl-chiller-a', displayName: '1# 螺杆主机控制器', deviceType: 'CHILLER_CONTROLLER' },
    { ...templateDevice, id: chwpMainId, code: 'chwp-main', displayName: '冷冻水泵（总管）', deviceType: 'PROVISIONAL_READINGS' },
    { ...templateDevice, id: ctHeaderId, code: 'cooling-tower', displayName: '冷却塔', deviceType: 'PROVISIONAL_READINGS' },
  );
  clonedModel.relationships.push({
    id: '018f3e00-6400-7000-8000-000000000701',
    tenantId: chillerAsset.tenantId,
    siteId: chillerAsset.siteId,
    fromType: 'DEVICE',
    fromId: ctrlId,
    toType: 'ASSET',
    toId: chillerAsset.id,
    role: 'PRIMARY_CONTROLLER',
    status: 'ACTIVE',
    validFrom: ts,
    validTo: null,
    revision: 1,
    createdAt: ts,
    updatedAt: ts,
  });
  clonedModel.telemetryPoints.push(
    { ...firstPoint, id: '018f3e00-5000-7000-8000-000000000701', reportingDeviceId: ctrlId, pointCode: 'run_state', sourceKey: 'run_state', displayName: '1# 主机运行状态', pointType: 'STATE', valueType: 'STRING', unit: null, writable: false, status: 'ACTIVE' },
    { ...firstPoint, id: '018f3e00-5000-7000-8000-000000000702', reportingDeviceId: chwpMainId, pointCode: 'run_state', sourceKey: 'chwp.run_state', displayName: '运行状态', pointType: 'STATE', valueType: 'STRING', unit: null, writable: false, status: 'ACTIVE' },
    { ...firstPoint, id: '018f3e00-5000-7000-8000-000000000703', reportingDeviceId: chwpMainId, pointCode: 'flow_rate', sourceKey: 'chwp.flow_rate', displayName: '冷冻水流量', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'm3/h', writable: false, status: 'ACTIVE' },
    { ...firstPoint, id: '018f3e00-5000-7000-8000-000000000704', reportingDeviceId: ctHeaderId, pointCode: 'leaving_water_temperature', sourceKey: 'cooling_tower.leaving_water_temperature', displayName: '冷却塔出水温度', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Cel', writable: false, status: 'ACTIVE' },
  );
  const sampleVal = firstSnap.values[0];
  clonedSnapshots.items.push(
    {
      requestId: 'req-ctrl',
      deviceId: ctrlId,
      status: 'OK',
      snapshot: { ...firstSnap, deviceId: ctrlId, values: [{ ...sampleVal, key: 'run_state', value: 'RUNNING', valueType: 'STRING', unit: null }] },
    },
    {
      requestId: 'req-chwp-main',
      deviceId: chwpMainId,
      status: 'OK',
      snapshot: { ...firstSnap, deviceId: chwpMainId, values: [
        { ...sampleVal, key: 'run_state', value: 'RUNNING', valueType: 'STRING', unit: null },
        { ...sampleVal, key: 'flow_rate', value: 132, valueType: 'NUMBER', unit: 'm3/h' },
      ] },
    },
    {
      requestId: 'req-ct-hdr',
      deviceId: ctHeaderId,
      status: 'OK',
      snapshot: { ...firstSnap, deviceId: ctHeaderId, values: [{ ...sampleVal, key: 'leaving_water_temperature', value: 23.7, valueType: 'NUMBER', unit: 'Cel' }] },
    },
  );

  const byDeviceId = new Map(clonedSnapshots.items.map((item: { deviceId: string; status: string; snapshot: unknown; problem: unknown }) => [
    item.deviceId, item.status === 'OK' ? { status: 'ok', snapshot: item.snapshot } : { status: 'error', problem: item.problem },
  ]));
  const web = buildPlantView(buildAssetsRows({ assetModel: clonedModel, snapshots: byDeviceId as never, now }), clonedModel);
  const phone = buildPlant(clonedModel, clonedSnapshots, now);

  const unitSummary = (unit: { unitId: string; category: string; runState: string; deviceIds: readonly string[]; connection: string }) => ({
    unitId: unit.unitId,
    category: unit.category,
    runState: unit.runState,
    deviceIds: unit.deviceIds,
    connection: unit.connection,
  });
  assert.deepEqual(phone.units.map(unitSummary), web.units.map(unitSummary));
  assert.equal(web.units.filter((u) => u.category === 'CHILLER').length, 1);
  assert.equal(web.units.find((u) => u.unitId === chillerAsset.id)?.deviceIds.length, 2);
  assert.equal(web.branches[0].equipmentCount, 1);
  assert.equal(phone.branches[0].equipmentCount, 1);
  assert.equal(web.devices.find((d) => d.deviceId === chwpMainId)?.category, 'METER');
  assert.equal(phone.devices.find((d) => d.deviceId === chwpMainId)?.runState, 'RUNNING');
});

test('web and mini program preserve SLEEPING presence identically across devices, units, and onlineCount', () => {
  const sleepingSnapshots = structuredClone(snapshots);
  const chillerId = assetModel.devices.find((device: { displayName: string }) => device.displayName.startsWith('CHILLER-01')).id;
  sleepingSnapshots.items.find((item: { deviceId: string }) => item.deviceId === chillerId).snapshot.presence.currentState = 'SLEEPING';

  const byDeviceId = new Map(sleepingSnapshots.items.map((item: { deviceId: string; status: string; snapshot: unknown; problem: unknown }) => [
    item.deviceId, item.status === 'OK' ? { status: 'ok', snapshot: item.snapshot } : { status: 'error', problem: item.problem },
  ]));
  const web = buildPlantView(buildAssetsRows({ assetModel, snapshots: byDeviceId as never, now }), assetModel);
  const phone = buildPlant(assetModel, sleepingSnapshots, now);

  assert.equal(phone.devices.find((d) => d.deviceId === chillerId)?.connection, 'SLEEPING');
  assert.equal(web.devices.find((d) => d.deviceId === chillerId)?.connection, 'SLEEPING');
  assert.equal(phone.units.find((u) => u.deviceIds.includes(chillerId))?.connection, 'SLEEPING');
  assert.equal(web.units.find((u) => u.deviceIds.includes(chillerId))?.connection, 'SLEEPING');
  assert.equal(phone.onlineCount, web.onlineCount);
});
