import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import type { SiteAssetModel } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import { siteAssetModelQueryKey } from '../apps/hvac-web/src/api/site-asset-model.ts';
import {
  assetsCurrentStateQueryKey,
  assetsProtectedQueryRoot,
  assetsRegistryQueryKey,
} from '../apps/hvac-web/src/features/assets/data.ts';
import { plantMachinesFromAssetModel } from '../apps/hvac-web/src/features/breakdown/plant-machines.ts';
import { createRealtimeDemandController } from '../apps/hvac-web/src/features/operations/realtime/realtime-demand.ts';

test('assetsProtectedQueryRoot is an exact prefix of both asset-model and current-state keys', () => {
  const root = assetsProtectedQueryRoot(7, 'tenant-1', 'site-1');
  const registryKey = assetsRegistryQueryKey(7, 'tenant-1', 'site-1', 2);
  const currentKey = assetsCurrentStateQueryKey(7, 'tenant-1', 'site-1', [], [], 2);

  assert.deepEqual(root, ['real-assets', 7, 'tenant-1', 'site-1']);
  assert.deepEqual(registryKey.slice(0, root.length), root);
  assert.deepEqual(currentKey.slice(0, root.length), root);
});

test('siteAssetModelQueryKey produces one canonical key per site or NONE fallback', () => {
  assert.deepEqual(siteAssetModelQueryKey('site-abc'), ['site-asset-model', 'site-abc']);
  assert.deepEqual(siteAssetModelQueryKey(null), ['site-asset-model', 'NONE']);
});

test('createRealtimeDemandController handles activation, idempotent release, grace handoff, and reset', () => {
  const transitions: boolean[] = [];
  const scheduled = new Map<number, () => void>();
  let nextHandle = 1;

  const controller = createRealtimeDemandController<number>({
    graceMs: 250,
    onChange: (active) => transitions.push(active),
    setTimer: (callback) => {
      const handle = nextHandle;
      nextHandle += 1;
      scheduled.set(handle, callback);
      return handle;
    },
    clearTimer: (handle) => {
      scheduled.delete(handle);
    },
  });

  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);

  // 0 -> 1 activates immediately.
  const releaseFirst = controller.acquire();
  assert.equal(controller.isActive(), true);
  assert.equal(controller.count(), 1);
  assert.deepEqual(transitions, [true]);

  // Duplicate release is idempotent; calling twice keeps count at 0 without multiple timers.
  releaseFirst();
  releaseFirst();
  assert.equal(controller.count(), 0);
  assert.equal(controller.isActive(), true);
  assert.equal(scheduled.size, 1);

  // Handoff 1 -> 0 -> 1 inside the grace period cancels deactivation without flicker.
  const releaseSecond = controller.acquire();
  assert.equal(scheduled.size, 0);
  assert.equal(controller.isActive(), true);
  assert.equal(controller.count(), 1);
  assert.deepEqual(transitions, [true]);

  // Dropping to 0 and letting the grace timer fire deactivates cleanly.
  releaseSecond();
  assert.equal(scheduled.size, 1);
  const [[onlyHandle, onlyCallback]] = [...scheduled.entries()];
  scheduled.delete(onlyHandle);
  onlyCallback();
  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);
  assert.deepEqual(transitions, [true, false]);

  // Re-acquire, start a pending grace deactivation, and verify reset() cancels it and deactivates immediately.
  const releaseThird = controller.acquire();
  assert.equal(controller.isActive(), true);
  releaseThird();
  assert.equal(scheduled.size, 1);
  controller.reset();
  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);
  assert.equal(scheduled.size, 0);
  assert.deepEqual(transitions, [true, false, true, false]);
});

test('plantMachinesFromAssetModel derives plant machines from E2E asset-model fixture without snapshots', () => {
  const assetModel = JSON.parse(
    readFileSync(new URL('../apps/hvac-web/e2e/fixtures/asset-model.json', import.meta.url), 'utf8'),
  ) as SiteAssetModel;
  const machines = plantMachinesFromAssetModel(assetModel);
  assert.ok(machines.length > 0);
  assert.ok(machines.some((machine) => machine.category === 'CHILLER'));
  assert.ok(machines.every((machine) => Boolean(machine.deviceId && machine.name)));
});

import { energyBreakdown } from '../apps/hvac-web/src/features/breakdown/breakdown-model.ts';
import { buildTwinPlant } from '../apps/hvac-web/src/features/operations/digital-twin/twin-model.ts';
import { buildAssetsRows } from '../apps/hvac-web/src/features/assets/model.ts';
import { buildPlantView } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';

test('plantMachinesFromAssetModel, energyBreakdown, and buildTwinPlant deduplicate dual-bound chillers and skip PROVISIONAL_READINGS headers', () => {
  const baseModel = JSON.parse(
    readFileSync(new URL('../apps/hvac-web/e2e/fixtures/asset-model.json', import.meta.url), 'utf8'),
  ) as SiteAssetModel;
  const chillerAsset = baseModel.assets.find((asset) => asset.assetType === 'CHILLER')!;
  const baseDevice = baseModel.devices[0];
  const basePoint = baseModel.telemetryPoints[0];
  const baseRel = baseModel.relationships.find((rel) => rel.fromType === 'DEVICE' && rel.toType === 'ASSET')!;
  const ts = baseDevice.createdAt;

  const extraCtrlId = '018f3e00-4000-7000-8000-000000000801';
  const extraMeterId = '018f3e00-4000-7000-8000-000000000802';
  const provTowerId = '018f3e00-4000-7000-8000-000000000803';
  const dualAssetId = '018f3e00-3000-7000-8000-000000000801';

  const synthetic: SiteAssetModel = {
    ...baseModel,
    assets: [
      ...baseModel.assets,
      { ...chillerAsset, id: dualAssetId, code: 'chiller-b', displayName: 'B 机组' },
    ],
    devices: [
      ...baseModel.devices,
      { ...baseDevice, id: extraCtrlId, code: 'ctrl-chiller-b', displayName: '2# 螺杆主机控制器', deviceType: 'CHILLER_CONTROLLER' },
      { ...baseDevice, id: extraMeterId, code: 'meter-uid3', displayName: 'B 机组电表', deviceType: 'HVAC_POWER_METER' },
      { ...baseDevice, id: provTowerId, code: 'cooling-tower', displayName: '冷却塔', deviceType: 'PROVISIONAL_READINGS' },
    ],
    telemetryPoints: [
      ...baseModel.telemetryPoints,
      { ...basePoint, id: '018f3e00-5000-7000-8000-000000000801', reportingDeviceId: extraCtrlId, pointCode: 'run_state', sourceKey: 'run_state', displayName: '运行状态', pointType: 'STATE', valueType: 'STRING', status: 'ACTIVE' },
      { ...basePoint, id: '018f3e00-5000-7000-8000-000000000802', reportingDeviceId: extraMeterId, pointCode: 'active_energy_total', sourceKey: 'hvac_meter.energy', displayName: '累计电量', pointType: 'COUNTER', valueType: 'NUMBER', unit: 'kWh', status: 'ACTIVE' },
      { ...basePoint, id: '018f3e00-5000-7000-8000-000000000803', reportingDeviceId: extraMeterId, pointCode: 'power_total', sourceKey: 'chiller.power', displayName: '有功功率', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'kW', status: 'ACTIVE' },
      { ...basePoint, id: '018f3e00-5000-7000-8000-000000000804', reportingDeviceId: provTowerId, pointCode: 'leaving_temp', sourceKey: 'cooling_tower.leaving_water_temperature', displayName: '冷却塔出水温度', pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Cel', status: 'ACTIVE' },
    ],
    relationships: [
      ...baseModel.relationships,
      { ...baseRel, id: '018f3e00-6400-7000-8000-000000000801', fromId: extraCtrlId, toId: dualAssetId, role: 'PRIMARY_CONTROLLER', validFrom: ts, createdAt: ts, updatedAt: ts },
      { ...baseRel, id: '018f3e00-6400-7000-8000-000000000802', fromId: extraMeterId, toId: dualAssetId, role: 'MEASURES', validFrom: ts, createdAt: ts, updatedAt: ts },
    ],
    counts: {
      ...baseModel.counts,
      assets: baseModel.assets.length + 1,
      deviceEndpoints: baseModel.devices.length + 3,
      points: baseModel.telemetryPoints.length + 4,
    },
  };

  const machines = plantMachinesFromAssetModel(synthetic);
  const dualMachines = machines.filter((machine) => machine.name === 'B 机组');
  assert.equal(dualMachines.length, 1);
  assert.equal(dualMachines[0].deviceId, extraMeterId);
  assert.deepEqual([...(dualMachines[0].deviceIds ?? [])].sort(), [extraCtrlId, extraMeterId]);
  assert.equal(machines.some((machine) => machine.deviceId === provTowerId), false);

  // Breakdown matches metered deviceId on the dual-bound machine without fabricating an unmetered duplicate.
  const hour = '2026-11-23T00:00:00.000Z';
  const breakdown = energyBreakdown(
    dualMachines,
    [{ periodStart: hour, energyKWh: 100 }],
    [{ deviceId: extraMeterId, points: [{ periodStart: hour, energyKWh: 85 }] }],
  );
  assert.equal(breakdown.items.length, 1);
  assert.equal(breakdown.items[0].name, 'B 机组');
  assert.equal(breakdown.items[0].energyKWh, 85);
  assert.deepEqual(breakdown.unmetered, []);

  // Digital twin merges controller runState + meter power reading onto one TwinEquipment even if controller appears first.
  const snapBase = {
    schemaVersion: 1 as const,
    tenantId: baseModel.tenantId,
    siteId: baseModel.siteId,
    businessRevision: 1,
    evaluatedAt: ts,
    evaluationAvailability: 'AVAILABLE' as const,
    availabilityReasons: [],
    presence: { applicability: 'APPLICABLE' as const, currentState: 'ONLINE' as const, lastSeenAt: ts, policyRevision: 1, lastKnown: null },
    telemetryReadiness: 'CURRENT' as const,
    displayState: 'ONLINE' as const,
  };
  const snapshots = new Map([
    [extraCtrlId, {
      status: 'ok' as const,
      snapshot: {
        ...snapBase,
        deviceId: extraCtrlId,
        values: [{ key: 'run_state', state: 'PRESENT' as const, value: 'RUNNING', valueType: 'STRING' as const, unit: null, sampledAt: ts, receivedAt: ts, freshness: 'FRESH' as const, quality: 'GOOD' as const, qualityReasons: [], policyRevision: 1 }],
      },
    }],
    [extraMeterId, {
      status: 'ok' as const,
      snapshot: {
        ...snapBase,
        deviceId: extraMeterId,
        values: [
          { key: 'active_energy_total', state: 'PRESENT' as const, value: 1200, valueType: 'NUMBER' as const, unit: 'kWh', sampledAt: ts, receivedAt: ts, freshness: 'FRESH' as const, quality: 'GOOD' as const, qualityReasons: [], policyRevision: 1 },
          { key: 'power_total', state: 'PRESENT' as const, value: 95, valueType: 'NUMBER' as const, unit: 'kW', sampledAt: ts, receivedAt: ts, freshness: 'FRESH' as const, quality: 'GOOD' as const, qualityReasons: [], policyRevision: 1 },
        ],
      },
    }],
  ]);
  const plant = buildPlantView(buildAssetsRows({ assetModel: synthetic, snapshots, now: new Date(ts) }), synthetic);
  const twin = buildTwinPlant(synthetic.assets, plant);
  const twinDual = twin.chillers.find((item) => item.assetId === dualAssetId);
  assert.ok(twinDual);
  assert.equal(twinDual.state, 'running');
  assert.equal(twinDual.values['chiller.power']?.display, '95');
});
