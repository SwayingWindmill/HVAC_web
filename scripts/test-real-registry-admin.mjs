import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildRegistryExport,
  canCommitImportPlan,
  makeRegistryMutationMeta,
  registryExportFileName,
} from '../apps/hvac-web/src/real/registry-admin/model.ts';

const tenantId = '01900000-0000-7000-8000-000000000001';
const siteId = '01900000-0001-7000-8000-000000000001';
const spaceId = '01900000-0002-7000-8000-000000000001';
const assetId = '01900000-0003-7000-8000-000000000001';
const deviceId = '01900000-0004-7000-8000-000000000001';
const pointId = '01900000-0005-7000-8000-000000000001';
const now = '2026-08-19T08:00:00.000Z';

const site = {
  id: siteId, tenantId, code: 'central-plant', displayName: 'Central Plant', timezone: 'Asia/Shanghai', status: 'ACTIVE', revision: 7,
  createdAt: now, updatedAt: now,
};

const model = {
  schemaVersion: 2,
  tenantId,
  siteId,
  spaces: [{ id: spaceId, tenantId, siteId, parentSpaceId: null, code: 'plant-room', displayName: 'Plant Room', spaceType: 'PLANT_ROOM', status: 'ACTIVE', revision: 2, createdAt: now, updatedAt: now }],
  assets: [{ id: assetId, tenantId, siteId, code: 'ahu-01', displayName: 'AHU 01', assetType: 'AHU', status: 'ACTIVE', revision: 3, createdAt: now, updatedAt: now }],
  devices: [{ id: deviceId, tenantId, siteId, code: 'ctrl-01', displayName: 'Controller 01', deviceType: 'CONTROLLER', status: 'ACTIVE', revision: 4, createdAt: now, updatedAt: now }],
  sensors: [{ id: '01900000-0006-7000-8000-000000000001', tenantId, siteId, code: 'sensor-01', displayName: 'Sensor 01', sensorType: 'TEMP', manufacturer: null, model: null, serialNumber: null, calibrationDueAt: null, metadata: { privateField: 'omit-me' }, status: 'ACTIVE', revision: 1, createdAt: now, updatedAt: now }],
  telemetryPoints: [{
    id: pointId, tenantId, siteId, reportingDeviceId: deviceId, sensorId: null, pointCode: 'supply_temp', sourceKey: 'modbus.40001', displayName: 'Supply Temp',
    pointType: 'TELEMETRY', valueType: 'NUMBER', unit: 'Cel', writable: false, sampleIntervalMs: 1000, publishIntervalMs: 5000, staleAfterMs: 15000,
    counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata: { privateField: 'omit-me' }, status: 'ACTIVE', revision: 5, createdAt: now, updatedAt: now,
  }],
  relationships: [{ id: '01900000-0007-7000-8000-000000000001', tenantId, siteId, fromType: 'DEVICE', fromId: deviceId, toType: 'ASSET', toId: assetId, role: 'CONTROLLER', status: 'ACTIVE', validFrom: now, validTo: null, revision: 1, createdAt: now, updatedAt: now }],
  counts: { spaces: 1, assets: 1, deviceEndpoints: 1, physicalSensors: 1, points: 1 },
};

test('controlled Registry export keeps canonical topology but drops free-form metadata', () => {
  const exported = buildRegistryExport(site, model, now);
  const serialized = JSON.stringify(exported);
  assert.equal(exported.site.revision, 7);
  assert.equal(exported.points[0].sourceKey, 'modbus.40001');
  assert.equal(exported.relationships[0].role, 'CONTROLLER');
  assert.equal(serialized.includes('omit-me'), false);
  assert.equal(serialized.includes('sourceMetadata'), false);
  assert.equal(serialized.includes('metadata'), false);
});

test('import commit remains disabled until the server plan has only READY rows', () => {
  const plan = {
    schemaVersion: 1, planId: '01900000-0010-7000-8000-000000000001', tenantId, siteId, namespace: 'test', rows: [{ rowNumber: 1, resourceType: 'ASSET', externalId: 'ahu-01', expectedRevision: 0, payload: {} }],
    results: [{ rowNumber: 1, resourceType: 'ASSET', externalId: 'ahu-01', expectedRevision: 0, status: 'READY' }], digest: 'a'.repeat(64),
  };
  assert.equal(canCommitImportPlan(plan), true);
  assert.equal(canCommitImportPlan({ ...plan, results: [{ ...plan.results[0], status: 'ERROR', errorCode: 'REGISTRY_REVISION_CONFLICT' }] }), false);
  assert.equal(canCommitImportPlan(null), false);
});

test('mutation meta preserves the exact expected revision and trims operator reason', () => {
  assert.deepEqual(
    makeRegistryMutationMeta(12, '  replace device binding  ', 'registry:test:12345678'),
    { expectedRevision: 12, reason: 'replace device binding', idempotencyKey: 'registry:test:12345678' },
  );
  assert.equal(registryExportFileName(site), 'registry-central-plant-rev7.json');
});
