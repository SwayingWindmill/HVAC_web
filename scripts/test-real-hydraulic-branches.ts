import assert from 'node:assert/strict';
import test from 'node:test';
import type { SiteAssetModel } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import { buildAssetsRows } from '../apps/hvac-web/src/features/assets/model.ts';
import { buildPlantView } from '../apps/hvac-web/src/features/operations/realtime/plant-model.ts';

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
