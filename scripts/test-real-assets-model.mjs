import assert from 'node:assert/strict';
import test from 'node:test';
import {
  listTelemetryKeys,
  resolveRealAssetsProfile,
} from '../apps/hvac-web/src/real/assets/catalog.ts';
import {
  buildRealAssetsHierarchy,
  buildRealAssetsPointRows,
  buildRealAssetsRows,
  projectRealAssetsOperatingState,
  resolveDeviceBinding,
} from '../apps/hvac-web/src/real/assets/model.ts';

const tenantId = '01900000-0000-7000-8000-000000000001';
const siteId = '01900000-0001-7000-8000-000000000001';
const assetAId = '01900000-0002-7000-8000-000000000001';
const assetBId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0003-7000-8000-000000000001';
const spaceId = '01900000-0005-7000-8000-000000000001';
const now = new Date('2026-07-30T10:00:00.000Z');

function device(overrides = {}) {
  return {
    id: deviceId,
    tenantId,
    siteId,
    code: 'CH-01',
    displayName: 'Chiller 01',
    deviceType: 'water-cooled chiller',
    status: 'ACTIVE',
    revision: 7,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function asset(id, displayName) {
  return {
    id,
    tenantId,
    siteId,
    code: displayName.replaceAll(' ', '-').toUpperCase(),
    displayName,
    assetType: 'CHILLER',
    status: 'ACTIVE',
    revision: 3,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
  };
}

function space(overrides = {}) {
  return {
    id: spaceId,
    tenantId,
    siteId,
    parentSpaceId: null,
    code: 'CENTRAL-PLANT',
    displayName: 'Central Plant',
    spaceType: 'PLANT_ROOM',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function sensor(overrides = {}) {
  return {
    id: '01900000-0006-7000-8000-000000000001',
    tenantId,
    siteId,
    code: 'SENSOR-1',
    displayName: 'Temperature Sensor',
    sensorType: 'TEMPERATURE',
    manufacturer: null,
    model: null,
    serialNumber: null,
    calibrationDueAt: null,
    metadata: {},
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function telemetryPoint(id, reportingDeviceId, sensorId, pointType = 'TELEMETRY') {
  return {
    id,
    tenantId,
    siteId,
    reportingDeviceId,
    sensorId,
    pointCode: pointType === 'STATE' ? 'run_state' : 'temperature',
    sourceKey: pointType === 'STATE' ? 'device.run_state' : 'sensor.temperature',
    displayName: pointType === 'STATE' ? 'Run State' : 'Temperature',
    pointType,
    valueType: pointType === 'STATE' ? 'BOOLEAN' : 'NUMBER',
    unit: pointType === 'STATE' ? null : 'Cel',
    writable: false,
    sampleIntervalMs: 1000,
    publishIntervalMs: 1000,
    staleAfterMs: 5000,
    sourceMetadata: {},
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
  };
}

function relationship(id, fromType, fromId, toType, toId, revision = 1, overrides = {}) {
  const role = fromType === 'DEVICE' && toType === 'ASSET'
    ? 'PRIMARY_CONTROLLER'
    : fromType === 'ASSET' && toType === 'SPACE'
      ? 'INSTALLED_IN'
      : fromType === 'SENSOR' && toType === 'DEVICE'
        ? 'REPORTS_THROUGH'
        : 'DESCRIBES';
  return {
    id,
    tenantId,
    siteId,
    fromType,
    fromId,
    toType,
    toId,
    role,
    status: 'ACTIVE',
    validFrom: '2026-07-01T00:00:00.000Z',
    validTo: null,
    revision,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function siteAssetModel({ spaces = [], assets = [], devices = [], sensors = [], telemetryPoints = [], relationships = [] }) {
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    spaces,
    assets,
    devices,
    sensors,
    telemetryPoints,
    relationships,
    counts: {
      spaces: spaces.length,
      assets: assets.length,
      deviceEndpoints: devices.length,
      physicalSensors: sensors.length,
      points: telemetryPoints.length,
    },
  };
}

function present(key, value, overrides = {}) {
  return {
    key,
    state: 'PRESENT',
    value,
    valueType: typeof value === 'number' ? 'NUMBER' : 'STRING',
    unit: null,
    sampledAt: '2026-07-30T09:59:00.000Z',
    receivedAt: '2026-07-30T09:59:01.000Z',
    freshness: 'FRESH',
    quality: 'GOOD',
    qualityReasons: [],
    policyRevision: 5,
    ...overrides,
  };
}

function snapshot(values, overrides = {}) {
  return {
    schemaVersion: 1,
    deviceId,
    tenantId,
    siteId,
    businessRevision: 11,
    evaluatedAt: '2026-07-30T10:00:00.000Z',
    evaluationAvailability: 'AVAILABLE',
    availabilityReasons: [],
    presence: {
      applicability: 'APPLICABLE',
      currentState: 'ONLINE',
      lastSeenAt: '2026-07-30T09:59:01.000Z',
      policyRevision: 5,
      lastKnown: null,
    },
    telemetryReadiness: 'CURRENT',
    displayState: 'ONLINE',
    values,
    ...overrides,
  };
}

const chillerProfile = resolveRealAssetsProfile('water cooled chiller');
const chillerKeys = listTelemetryKeys(chillerProfile);
const goodValues = chillerKeys.map((key, index) => present(key, index === 1 ? 0 : index + 1));

test('catalog resolves aliases but does not silently fallback unknown Device types', () => {
  assert.equal(chillerProfile.state, 'configured');
  assert.deepEqual(chillerKeys, [
    'chiller.run_state',
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
  ]);
  const unknown = resolveRealAssetsProfile('vendor-special-controller');
  assert.equal(unknown.state, 'unconfigured');
  assert.deepEqual(listTelemetryKeys(unknown), []);
});

test('operating projection preserves zero and follows UNKNOWN/OFFLINE/ATTENTION/NORMAL precedence', () => {
  const normal = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot(goodValues) }, chillerProfile);
  assert.equal(normal.state, 'NORMAL');
  assert.equal(normal.points.find((point) => point.key === 'chiller.power').displayValue, '0');

  const staleValues = goodValues.map((value) => value.key === 'chiller.power' ? { ...value, freshness: 'STALE' } : value);
  const stale = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot(staleValues) }, chillerProfile);
  assert.equal(stale.state, 'ATTENTION');
  assert.ok(stale.reasons.includes('TELEMETRY_STALE'));

  const offline = projectRealAssetsOperatingState({
    status: 'ok',
    snapshot: snapshot(staleValues, { presence: { ...snapshot([]).presence, currentState: 'OFFLINE' } }),
  }, chillerProfile);
  assert.equal(offline.state, 'OFFLINE');
  assert.deepEqual(offline.reasons, ['PRESENCE_OFFLINE']);

  const unavailable = projectRealAssetsOperatingState({
    status: 'ok',
    snapshot: snapshot(goodValues, { evaluationAvailability: 'UNAVAILABLE', availabilityReasons: ['SOURCE_UNAVAILABLE'] }),
  }, chillerProfile);
  assert.equal(unavailable.state, 'UNKNOWN');
});

test('missing and degraded-quality critical points remain explicit attention evidence', () => {
  const missing = {
    key: 'chiller.cop',
    state: 'MISSING',
    freshness: 'MISSING',
    missingReason: 'ONLY_REJECTED_CANDIDATES',
    policyRevision: 5,
  };
  const values = goodValues.map((value) => value.key === 'chiller.cop' ? missing : value);
  values[0] = { ...values[0], quality: 'PARTIAL', qualityReasons: ['SOURCE_UNTRUSTED'] };
  const projection = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot(values) }, chillerProfile);
  assert.equal(projection.state, 'ATTENTION');
  assert.ok(projection.reasons.includes('CRITICAL_POINT_MISSING'));
  assert.ok(projection.reasons.includes('TELEMETRY_QUALITY_DEGRADED'));
  assert.equal(projection.points.find((point) => point.key === 'chiller.cop').displayValue, '当前值不可用');
});

test('unknown Device profile and owner read failures never become normal state', () => {
  const unknown = resolveRealAssetsProfile('vendor-special-controller');
  const projection = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot([]) }, unknown);
  assert.equal(projection.state, 'UNKNOWN');
  assert.deepEqual(projection.reasons, ['POINT_CATALOG_UNCONFIGURED']);

  const problem = (code) => ({
    type: 'about:blank',
    title: 'Current state unavailable',
    status: code === 'RESOURCE_NOT_FOUND' ? 404 : 400,
    detail: 'The requested current-state item could not be established.',
    instance: '/api/v1/telemetry/observation-snapshots:batchGet',
    code,
    traceId: '0123456789abcdef0123456789abcdef',
    retryable: false,
  });
  assert.deepEqual(
    projectRealAssetsOperatingState({ status: 'error', problem: problem('RESOURCE_NOT_FOUND') }, chillerProfile).reasons,
    ['CURRENT_STATE_NOT_VISIBLE'],
  );
  assert.deepEqual(
    projectRealAssetsOperatingState({ status: 'error', problem: problem('TELEMETRY_KEY_INVALID') }, chillerProfile).reasons,
    ['POINT_CATALOG_CONTRACT_DRIFT'],
  );
});

test('Device binding resolves only canonical Asset relationships and preserves multi-bindings', () => {
  const assetA = asset(assetAId, 'Alpha Chiller');
  const assetB = asset(assetBId, 'Beta Chiller');
  const assetById = new Map([[assetA.id, assetA], [assetB.id, assetB]]);
  const relationshipA = relationship('01900000-0004-7000-8000-000000000001', 'DEVICE', deviceId, 'ASSET', assetAId);
  const relationshipB = relationship('01900000-0004-7000-8000-000000000002', 'DEVICE', deviceId, 'ASSET', assetBId, 2);

  assert.equal(resolveDeviceBinding(device(), [relationshipA], assetById, now).state, 'bound');
  assert.equal(resolveDeviceBinding(device(), [], assetById, now).state, 'unbound');
  const multiple = resolveDeviceBinding(device(), [relationshipA, relationshipB], assetById, now);
  assert.equal(multiple.state, 'multi-bound');
  assert.deepEqual(multiple.bindings.map((binding) => binding.asset.id), [assetBId, assetAId]);
});

test('rows sort by Space, Asset and Device identity while preserving unbound endpoints', () => {
  const plantSpace = space();
  const assetA = asset(assetAId, 'Alpha Chiller');
  const assetB = asset(assetBId, 'Beta Chiller');
  const deviceA = device({ id: deviceId, displayName: 'Device B' });
  const deviceB = device({ id: '01900000-0003-7000-8000-000000000002', displayName: 'Device A' });
  const deviceC = device({ id: '01900000-0003-7000-8000-000000000003', displayName: 'Unbound' });
  const model = siteAssetModel({
    spaces: [plantSpace],
    assets: [assetB, assetA],
    devices: [deviceC, deviceA, deviceB],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000011', 'ASSET', assetA.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000012', 'ASSET', assetB.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000013', 'DEVICE', deviceA.id, 'ASSET', assetB.id),
      relationship('01900000-0004-7000-8000-000000000014', 'DEVICE', deviceB.id, 'ASSET', assetA.id),
    ],
  });
  const rows = buildRealAssetsRows({ assetModel: model, snapshots: new Map(), now });
  assert.deepEqual(rows.map((row) => row.device.displayName), ['Device A', 'Device B', 'Unbound']);
  assert.equal(rows[0].space.state, 'bound');
  assert.equal(rows[2].registeredPointCount, 0);
});

test('hierarchy uses Site → Space → Asset and preserves Sensor/Point identity', () => {
  const plantSpace = space();
  const plantAsset = asset(assetAId, 'Alpha Chiller');
  const endpoint = device();
  const measurementSensor = sensor();
  const measured = telemetryPoint('01900000-0007-7000-8000-000000000001', endpoint.id, measurementSensor.id);
  const directPoint = {
    ...telemetryPoint('01900000-0007-7000-8000-000000000002', endpoint.id, null),
    pointCode: 'plant_delta_t',
    sourceKey: 'plant.delta_t',
    displayName: 'Delta T',
  };
  const model = siteAssetModel({
    spaces: [plantSpace],
    assets: [plantAsset],
    devices: [endpoint],
    sensors: [measurementSensor],
    telemetryPoints: [measured, directPoint],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000020', 'ASSET', plantAsset.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000021', 'DEVICE', endpoint.id, 'ASSET', plantAsset.id),
      relationship('01900000-0004-7000-8000-000000000022', 'SENSOR', measurementSensor.id, 'DEVICE', endpoint.id),
    ],
  });

  const hierarchy = buildRealAssetsHierarchy(model, 'Test Site', now);
  const spaceNode = hierarchy.children[0];
  const assetNode = spaceNode.children[0];
  const sensorNode = assetNode.children.find((node) => node.kind === 'sensor');
  const virtualSensorNode = assetNode.children.find((node) => node.kind === 'virtual-sensor');

  assert.equal(hierarchy.kind, 'site');
  assert.equal(spaceNode.kind, 'space');
  assert.equal(assetNode.kind, 'asset');
  assert.equal(assetNode.children.some((node) => node.kind === 'device'), false);
  assert.deepEqual(assetNode.deviceIds, [endpoint.id]);
  assert.equal(sensorNode.children[0].kind, 'point');
  assert.equal(virtualSensorNode.children[0].kind, 'point');
  assert.deepEqual(assetNode.pointIds.sort(), [measured.id, directPoint.id].sort());
});

test('Point ledger keeps every registered Point independent of Asset hierarchy rendering', () => {
  const plantSpace = space();
  const plantAsset = asset(assetAId, 'Alpha Chiller');
  const endpoint = device();
  const measurementSensor = sensor();
  const points = [
    telemetryPoint('01900000-0007-7000-8000-000000000010', endpoint.id, measurementSensor.id),
    telemetryPoint('01900000-0007-7000-8000-000000000011', endpoint.id, null),
    telemetryPoint('01900000-0007-7000-8000-000000000012', endpoint.id, null),
    telemetryPoint('01900000-0007-7000-8000-000000000013', endpoint.id, null, 'STATE'),
  ].map((point, index) => ({
    ...point,
    pointCode: ['chiller_power', 'chiller_cooling_capacity', 'chiller_cop', 'chiller_run_state'][index],
    sourceKey: ['chiller.power', 'chiller.cooling_capacity', 'chiller.cop', 'chiller.run_state'][index],
    displayName: ['chiller power', 'chiller cooling capacity', 'chiller cop', 'chiller run state'][index],
    unit: index < 2 ? 'kW' : null,
  }));
  const model = siteAssetModel({
    spaces: [plantSpace],
    assets: [plantAsset],
    devices: [endpoint],
    sensors: [measurementSensor],
    telemetryPoints: points,
    relationships: [
      relationship('01900000-0004-7000-8000-000000000030', 'ASSET', plantAsset.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000031', 'DEVICE', endpoint.id, 'ASSET', plantAsset.id),
      relationship('01900000-0004-7000-8000-000000000032', 'SENSOR', measurementSensor.id, 'DEVICE', endpoint.id),
    ],
  });
  const currentValues = points.map((point, index) => present(point.pointCode, index + 1, { unit: point.unit }));
  const rows = buildRealAssetsRows({
    assetModel: model,
    snapshots: new Map([[endpoint.id, { status: 'ok', snapshot: snapshot(currentValues) }]]),
    now,
  });
  const pointRows = buildRealAssetsPointRows({ assetModel: model, deviceRows: rows });

  assert.equal(pointRows.length, 4);
  assert.deepEqual(pointRows.map((row) => row.point.id).sort(), points.map((point) => point.id).sort());
  assert.ok(pointRows.every((row) => row.device.id === endpoint.id));
  assert.equal(pointRows.find((row) => row.point.pointCode === 'chiller_power').current?.displayValue, '1');
});

test('multi-Asset Device binding is represented under each Asset with unique hierarchy keys', () => {
  const plantSpace = space();
  const assetA = asset(assetAId, 'Alpha Chiller');
  const assetB = asset(assetBId, 'Beta Chiller');
  const endpoint = device();
  const model = siteAssetModel({
    spaces: [plantSpace],
    assets: [assetA, assetB],
    devices: [endpoint],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000040', 'ASSET', assetA.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000041', 'ASSET', assetB.id, 'SPACE', plantSpace.id),
      relationship('01900000-0004-7000-8000-000000000042', 'DEVICE', endpoint.id, 'ASSET', assetA.id),
      relationship('01900000-0004-7000-8000-000000000043', 'DEVICE', endpoint.id, 'ASSET', assetB.id, 2),
    ],
  });

  const rows = buildRealAssetsRows({ assetModel: model, snapshots: new Map(), now });
  assert.equal(rows[0].binding.state, 'multi-bound');
  assert.equal(rows[0].space.state, 'bound');

  const hierarchy = buildRealAssetsHierarchy(model, 'Test Site', now);
  const assetNodes = hierarchy.children[0].children.filter((node) => node.kind === 'asset');
  assert.equal(assetNodes.length, 2);
  assert.deepEqual(assetNodes.map((node) => node.children[0].deviceIds), [[endpoint.id], [endpoint.id]]);
  assert.notEqual(assetNodes[0].children[0].key, assetNodes[1].children[0].key);
});
