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
  resolveDeviceBinding,
} from '../apps/hvac-web/src/real/assets/model.ts';
import { projectRealAssetsDeviceOperationalState } from '../apps/hvac-web/src/real/assets/operational-projection.ts';

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

test('Device operational projection keeps unknown Presence independent from healthy generic telemetry', () => {
  const point = {
    ...telemetryPoint('01900000-0007-7000-8000-000000000001', deviceId, null),
    pointCode: 'vendor.temperature',
    sourceKey: 'vendor.temperature',
    displayName: 'Supply Temperature',
  };
  const profile = resolveRealAssetsProfile('vendor-special-controller');
  const projection = projectRealAssetsDeviceOperationalState({
    device: device({ deviceType: 'vendor-special-controller' }),
    telemetryPoints: [point],
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot([present('vendor.temperature', 21.5)], {
        presence: { ...snapshot([]).presence, currentState: 'UNKNOWN' },
      }),
    },
    profile,
  });

  assert.equal(projection.connection.state, 'UNKNOWN');
  assert.equal(projection.telemetry.readiness, 'CURRENT');
  assert.equal(projection.telemetry.freshness, 'FRESH');
  assert.equal(projection.telemetry.quality, 'GOOD');
  assert.equal(projection.registryLifecycle, 'ACTIVE');
  assert.equal(projection.needsAttention, false);
  assert.deepEqual(projection.attentionReasons, []);
  assert.equal(projection.points.length, 1);
  assert.equal(projection.points[0].displayValue, '21.5');
  assert.equal(projection.points[0].label, 'Supply Temperature');
});

test('Device operational projection preserves not-applicable Presence and telemetry without creating attention', () => {
  const point = telemetryPoint('01900000-0007-7000-8000-000000000002', deviceId, null);
  const projection = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: [point],
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot([], {
        presence: {
          ...snapshot([]).presence,
          applicability: 'NOT_APPLICABLE',
          currentState: null,
          lastSeenAt: null,
        },
        telemetryReadiness: 'NOT_APPLICABLE',
        displayState: null,
      }),
    },
    profile: chillerProfile,
  });

  assert.equal(projection.connection.applicability, 'NOT_APPLICABLE');
  assert.equal(projection.connection.state, 'NOT_APPLICABLE');
  assert.equal(projection.telemetry.readiness, 'NOT_APPLICABLE');
  assert.equal(projection.telemetry.freshness, 'NOT_APPLICABLE');
  assert.equal(projection.telemetry.quality, 'NOT_APPLICABLE');
  assert.equal(projection.needsAttention, false);
  assert.deepEqual(projection.attentionReasons, []);
});

test('Device operational projection never presents a value as current when evaluation is unavailable', () => {
  const point = telemetryPoint('01900000-0007-7000-8000-000000000004', deviceId, null);
  const projection = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: [point],
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot([present('temperature', 22.4)], {
        evaluationAvailability: 'UNAVAILABLE',
        availabilityReasons: ['SOURCE_UNAVAILABLE'],
      }),
    },
    profile: resolveRealAssetsProfile('GENERIC'),
  });

  assert.equal(projection.connection.state, 'UNAVAILABLE');
  assert.equal(projection.telemetry.evaluationAvailability, 'UNAVAILABLE');
  assert.equal(projection.telemetry.readiness, 'UNAVAILABLE');
  assert.equal(projection.telemetry.freshness, 'UNAVAILABLE');
  assert.equal(projection.telemetry.quality, 'UNAVAILABLE');
  assert.equal(projection.telemetry.missingPointCount, 0);
  assert.equal(projection.telemetry.unavailablePointCount, 1);
  assert.equal(projection.points[0].state, 'UNAVAILABLE');
  assert.equal(projection.points[0].displayValue, '当前值不可用');
  assert.equal(projection.attentionReasons.includes('CURRENT_STATE_UNAVAILABLE'), true);
  assert.equal(projection.attentionReasons.includes('PRESENCE_OFFLINE'), false);
});

const chillerProfile = resolveRealAssetsProfile('water cooled chiller');
const chillerKeys = listTelemetryKeys(chillerProfile);
const chillerPoints = chillerKeys.map((key, index) => ({
  ...telemetryPoint(`01900000-0007-7000-8000-00000000001${index}`, deviceId, null),
  pointCode: key,
  sourceKey: key,
  displayName: key,
}));
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

test('operational projection preserves zero and keeps connection independent from stale telemetry', () => {
  const healthy = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: chillerPoints,
    snapshotResult: { status: 'ok', snapshot: snapshot(goodValues) },
    profile: chillerProfile,
  });
  assert.equal(healthy.connection.state, 'ONLINE');
  assert.equal(healthy.telemetry.freshness, 'FRESH');
  assert.equal(healthy.needsAttention, false);
  assert.equal(healthy.points.find((point) => point.key === 'chiller.power').displayValue, '0');

  const staleValues = goodValues.map((value) => value.key === 'chiller.power' ? { ...value, freshness: 'STALE' } : value);
  const stale = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: chillerPoints,
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot(staleValues, { telemetryReadiness: 'DEGRADED', displayState: 'STALE' }),
    },
    profile: chillerProfile,
  });
  assert.equal(stale.connection.state, 'ONLINE');
  assert.equal(stale.telemetry.freshness, 'STALE');
  assert.ok(stale.attentionReasons.includes('TELEMETRY_STALE'));

  const offline = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: chillerPoints,
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot(staleValues, {
        presence: { ...snapshot([]).presence, currentState: 'OFFLINE' },
        telemetryReadiness: 'DEGRADED',
        displayState: 'OFFLINE',
      }),
    },
    profile: chillerProfile,
  });
  assert.equal(offline.connection.state, 'OFFLINE');
  assert.ok(offline.attentionReasons.includes('PRESENCE_OFFLINE'));
  assert.ok(offline.attentionReasons.includes('TELEMETRY_STALE'));
});

test('missing and degraded-quality Points remain independent attention evidence', () => {
  const missing = {
    key: 'chiller.cop',
    state: 'MISSING',
    freshness: 'MISSING',
    missingReason: 'ONLY_REJECTED_CANDIDATES',
    policyRevision: 5,
  };
  const values = goodValues.map((value) => value.key === 'chiller.cop' ? missing : value);
  values[0] = { ...values[0], quality: 'PARTIAL', qualityReasons: ['SOURCE_UNTRUSTED'] };
  const projection = projectRealAssetsDeviceOperationalState({
    device: device(),
    telemetryPoints: chillerPoints,
    snapshotResult: {
      status: 'ok',
      snapshot: snapshot(values, { telemetryReadiness: 'INCOMPLETE', displayState: 'UNKNOWN' }),
    },
    profile: chillerProfile,
  });
  assert.equal(projection.telemetry.readiness, 'INCOMPLETE');
  assert.ok(projection.attentionReasons.includes('TELEMETRY_MISSING'));
  assert.ok(projection.attentionReasons.includes('TELEMETRY_QUALITY_DEGRADED'));
  assert.equal(projection.points.find((point) => point.key === 'chiller.cop').displayValue, '当前值不可用');
});

test('owner read failures map to unavailable Point evidence without inventing Device state', () => {
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
  const notVisible = projectRealAssetsDeviceOperationalState({
    device: device(), telemetryPoints: chillerPoints,
    snapshotResult: { status: 'error', problem: problem('RESOURCE_NOT_FOUND') }, profile: chillerProfile,
  });
  assert.equal(notVisible.connection.state, 'UNAVAILABLE');
  assert.deepEqual(notVisible.attentionReasons, ['CURRENT_STATE_NOT_VISIBLE']);
  assert.ok(notVisible.points.every((point) => point.state === 'UNAVAILABLE'));

  const contractDrift = projectRealAssetsDeviceOperationalState({
    device: device(), telemetryPoints: chillerPoints,
    snapshotResult: { status: 'error', problem: problem('TELEMETRY_KEY_INVALID') }, profile: chillerProfile,
  });
  assert.deepEqual(contractDrift.attentionReasons, ['POINT_CATALOG_CONTRACT_DRIFT']);
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

test('Device rows expose the independent operational projection for unprofiled Registry Points', () => {
  const unprofiledDevice = device({ deviceType: 'sensor-aggregator' });
  const point = {
    ...telemetryPoint('01900000-0007-7000-8000-000000000003', deviceId, null),
    pointCode: 'aggregator.temperature_1',
    sourceKey: 'temperature_1',
    displayName: 'Temperature 1',
  };
  const model = siteAssetModel({ devices: [unprofiledDevice], telemetryPoints: [point] });
  const row = buildRealAssetsRows({
    assetModel: model,
    snapshots: new Map([[deviceId, {
      status: 'ok',
      snapshot: snapshot([present('aggregator.temperature_1', 18.75)], {
        presence: { ...snapshot([]).presence, currentState: 'UNKNOWN' },
      }),
    }]]),
    now,
  })[0];

  assert.equal(row.profile.state, 'unconfigured');
  assert.equal(row.operational.connection.state, 'UNKNOWN');
  assert.equal(row.operational.telemetry.readiness, 'CURRENT');
  assert.equal(row.operational.needsAttention, false);
  assert.equal(row.operational.points[0].label, 'Temperature 1');
  assert.equal(row.operational.points[0].displayValue, '18.75');
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

test('Operations hierarchy stops at Device and keeps Sensor/Point inside detail', () => {
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
  const deviceNode = assetNode.children.find((node) => node.kind === 'device');
  const kinds = [];
  const visit = (node) => {
    kinds.push(node.kind);
    node.children.forEach(visit);
  };
  visit(hierarchy);

  assert.equal(hierarchy.kind, 'site');
  assert.equal(spaceNode.kind, 'space');
  assert.equal(assetNode.kind, 'asset');
  assert.equal(deviceNode.kind, 'device');
  assert.deepEqual(deviceNode.deviceIds, [endpoint.id]);
  assert.deepEqual(deviceNode.children, []);
  assert.equal(kinds.includes('sensor'), false);
  assert.equal(kinds.includes('point'), false);
  assert.equal(kinds.includes('virtual-sensor'), false);
});

test('Point projection keeps every registered Point available for entity detail', () => {
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
