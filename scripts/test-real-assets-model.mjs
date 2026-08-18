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
const equipmentAId = '01900000-0002-7000-8000-000000000001';
const equipmentBId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0003-7000-8000-000000000001';
const now = new Date('2026-07-30T10:00:00.000Z');

function device(overrides = {}) {
  return {
    id: deviceId,
    tenantId: tenantId,
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

function equipment(id, displayName) {
  return {
    id,
    tenantId: tenantId,
    siteId,
    code: displayName.replaceAll(' ', '-').toUpperCase(),
    displayName,
    equipmentType: 'CHILLER',
    status: 'ACTIVE',
    revision: 3,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
  };
}

function relationship(id, fromType, fromId, toType, toId, revision = 1, overrides = {}) {
  return {
    id,
    tenantId: tenantId,
    siteId,
    fromType,
    fromId,
    toType,
    toId,
    role: fromType === 'DEVICE' ? 'PRIMARY_CONTROLLER' : 'INSTALLED_IN',
    status: 'ACTIVE',
    validFrom: '2026-07-01T00:00:00.000Z',
    validTo: null,
    revision,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
    ...overrides,
  };
}

function area(id = '01900000-0005-7000-8000-000000000001', displayName = 'Central Plant') {
  return {
    id,
    tenantId: tenantId,
    siteId,
    parentAreaId: null,
    code: displayName.replaceAll(' ', '-').toUpperCase(),
    displayName,
    areaType: 'PLANT_ROOM',
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
  };
}

function sensor(id = '01900000-0006-7000-8000-000000000001') {
  return {
    id,
    tenantId: tenantId,
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

function siteAssetModel({ areas = [], equipment: equipmentItems = [], devices = [], sensors = [], telemetryPoints = [], relationships = [] }) {
  return {
    schemaVersion: 2,
    tenantId,
    siteId,
    areas,
    equipment: equipmentItems,
    devices,
    sensors,
    telemetryPoints,
    relationships,
    counts: {
      areas: areas.length,
      equipment: equipmentItems.length,
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
    tenantId: tenantId,
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

test('missing and degraded-quality critical points require attention without turning valid values into missing', () => {
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

test('unknown configured profile absence remains visible and cannot be classified as normal', () => {
  const unknown = resolveRealAssetsProfile('vendor-special-controller');
  const projection = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot([]) }, unknown);
  assert.equal(projection.state, 'UNKNOWN');
  assert.deepEqual(projection.reasons, ['POINT_CATALOG_UNCONFIGURED']);
  assert.deepEqual(projection.points, []);
});

test('per-Device current-state failures preserve not-visible and catalog-drift evidence', () => {
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
  const invisible = projectRealAssetsOperatingState({ status: 'error', problem: problem('RESOURCE_NOT_FOUND') }, chillerProfile);
  const drift = projectRealAssetsOperatingState({ status: 'error', problem: problem('TELEMETRY_KEY_INVALID') }, chillerProfile);
  assert.deepEqual(invisible.reasons, ['CURRENT_STATE_NOT_VISIBLE']);
  assert.deepEqual(drift.reasons, ['POINT_CATALOG_CONTRACT_DRIFT']);
});

test('Asset relationships resolve one or several current Equipment bindings and expose unbound states', () => {
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const equipmentB = equipment(equipmentBId, 'Beta Chiller');
  const equipmentById = new Map([[equipmentA.id, equipmentA], [equipmentB.id, equipmentB]]);
  const relationshipA = relationship('01900000-0004-7000-8000-000000000001', 'DEVICE', deviceId, 'EQUIPMENT', equipmentAId);
  const relationshipB = relationship('01900000-0004-7000-8000-000000000002', 'DEVICE', deviceId, 'EQUIPMENT', equipmentBId, 2);

  assert.equal(resolveDeviceBinding(device(), [relationshipA], equipmentById, now).state, 'bound');
  assert.equal(resolveDeviceBinding(device(), [], equipmentById, now).state, 'unbound');
  const multiple = resolveDeviceBinding(device(), [relationshipA, relationshipB], equipmentById, now);
  assert.equal(multiple.state, 'multi-bound');
  assert.deepEqual(multiple.bindings.map((binding) => binding.equipment.id), [equipmentBId, equipmentAId]);
});

test('rows sort by Area, Equipment and Device identity while preserving unbound Device Endpoints', () => {
  const plantArea = area();
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const equipmentB = equipment(equipmentBId, 'Beta Chiller');
  const deviceA = device({ id: deviceId, displayName: 'Device B' });
  const deviceB = device({ id: '01900000-0003-7000-8000-000000000002', displayName: 'Device A' });
  const deviceC = device({ id: '01900000-0003-7000-8000-000000000003', displayName: 'Unbound' });
  const model = siteAssetModel({
    areas: [plantArea],
    equipment: [equipmentB, equipmentA],
    devices: [deviceC, deviceA, deviceB],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000001', 'EQUIPMENT', equipmentAId, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000002', 'EQUIPMENT', equipmentBId, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000010', 'DEVICE', deviceA.id, 'EQUIPMENT', equipmentBId),
      relationship('01900000-0004-7000-8000-000000000011', 'DEVICE', deviceB.id, 'EQUIPMENT', equipmentAId),
    ],
  });
  const rows = buildRealAssetsRows({ assetModel: model, snapshots: new Map(), now });
  assert.deepEqual(rows.map((row) => row.device.displayName), ['Device A', 'Device B', 'Unbound']);
  assert.equal(rows[0].area.state, 'bound');
  assert.equal(rows[2].registeredPointCount, 0);
});

test('hierarchy collapses a one-to-one Device Endpoint while preserving Sensor and Telemetry Point selection', () => {
  const plantArea = area();
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const endpoint = device();
  const measurementSensor = sensor();
  const measured = telemetryPoint('01900000-0007-7000-8000-000000000001', endpoint.id, measurementSensor.id);
  const directPoint = {
    ...telemetryPoint('01900000-0007-7000-8000-000000000002', endpoint.id, null, 'TELEMETRY'),
    pointCode: 'plant_delta_t',
    displayName: 'Delta T',
  };
  const model = siteAssetModel({
    areas: [plantArea],
    equipment: [equipmentA],
    devices: [endpoint],
    sensors: [measurementSensor],
    telemetryPoints: [measured, directPoint],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000020', 'EQUIPMENT', equipmentA.id, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000021', 'DEVICE', endpoint.id, 'EQUIPMENT', equipmentA.id),
      relationship('01900000-0004-7000-8000-000000000022', 'SENSOR', measurementSensor.id, 'DEVICE', endpoint.id),
    ],
  });
  const hierarchy = buildRealAssetsHierarchy(model, 'Test Site', now);
  const areaNode = hierarchy.children[0];
  const equipmentNode = areaNode.children[0];
  const sensorNode = equipmentNode.children.find((node) => node.kind === 'sensor');
  const virtualSensorNode = equipmentNode.children.find((node) => node.kind === 'virtual-sensor');

  assert.equal(hierarchy.kind, 'site');
  assert.equal(areaNode.kind, 'area');
  assert.equal(equipmentNode.kind, 'equipment');
  assert.equal(equipmentNode.label, '冷水机组');
  assert.equal(equipmentNode.meta, '设备 · ALPHA-CHILLER');
  assert.equal(equipmentNode.children.some((node) => node.kind === 'device'), false);
  assert.deepEqual(equipmentNode.deviceIds, [endpoint.id]);
  assert.equal(sensorNode.children[0].kind, 'point');
  assert.equal(sensorNode.children[0].label, '温度');
  assert.equal(sensorNode.children[0].meta, '遥测 · °C');
  assert.equal(virtualSensorNode.children[0].label, '温差');
  assert.deepEqual(equipmentNode.pointIds.sort(), [measured.id, directPoint.id].sort());
  assert.deepEqual(areaNode.deviceIds, [endpoint.id]);
});

test('point ledger projects every registered Telemetry Point as an independent row', () => {
  const plantArea = area();
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const endpoint = device();
  const measurementSensor = sensor();
  const points = [
    telemetryPoint('01900000-0007-7000-8000-000000000010', endpoint.id, measurementSensor.id, 'TELEMETRY'),
    telemetryPoint('01900000-0007-7000-8000-000000000011', endpoint.id, null, 'TELEMETRY'),
    telemetryPoint('01900000-0007-7000-8000-000000000012', endpoint.id, null, 'TELEMETRY'),
    telemetryPoint('01900000-0007-7000-8000-000000000013', endpoint.id, null, 'STATE'),
  ].map((point, index) => ({
    ...point,
    pointCode: ['chiller_power', 'chiller_cooling_capacity', 'chiller_cop', 'chiller_run_state'][index],
    displayName: ['chiller power', 'chiller cooling capacity', 'chiller cop', 'chiller run state'][index],
    unit: index === 3 ? null : index === 2 ? null : 'kW',
  }));
  const model = siteAssetModel({
    areas: [plantArea],
    equipment: [equipmentA],
    devices: [endpoint],
    sensors: [measurementSensor],
    telemetryPoints: points,
    relationships: [
      relationship('01900000-0004-7000-8000-000000000040', 'EQUIPMENT', equipmentA.id, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000041', 'DEVICE', endpoint.id, 'EQUIPMENT', equipmentA.id),
      relationship('01900000-0004-7000-8000-000000000042', 'SENSOR', measurementSensor.id, 'DEVICE', endpoint.id),
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
  assert.deepEqual(pointRows.map((row) => row.label), ['运行状态', '制冷量', '主机 COP', '主机功率']);
  assert.deepEqual(pointRows.map((row) => row.point.id).sort(), points.map((point) => point.id).sort());
  assert.ok(pointRows.every((row) => row.device.id === endpoint.id));
  assert.equal(pointRows.find((row) => row.point.pointCode === 'chiller_power').current?.displayValue, '1');
});

test('hierarchy projects one Device Endpoint under every active Equipment binding with unique tree keys', () => {
  const plantArea = area();
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const equipmentB = equipment(equipmentBId, 'Beta Chiller');
  const endpoint = device();
  const model = siteAssetModel({
    areas: [plantArea],
    equipment: [equipmentA, equipmentB],
    devices: [endpoint],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000030', 'EQUIPMENT', equipmentA.id, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000031', 'EQUIPMENT', equipmentB.id, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000032', 'DEVICE', endpoint.id, 'EQUIPMENT', equipmentA.id),
      relationship('01900000-0004-7000-8000-000000000033', 'DEVICE', endpoint.id, 'EQUIPMENT', equipmentB.id, 2),
    ],
  });

  const rows = buildRealAssetsRows({ assetModel: model, snapshots: new Map(), now });
  assert.equal(rows[0].binding.state, 'multi-bound');
  assert.equal(rows[0].area.state, 'bound');

  const hierarchy = buildRealAssetsHierarchy(model, 'Test Site', now);
  const areaNode = hierarchy.children[0];
  const equipmentNodes = areaNode.children.filter((node) => node.kind === 'equipment');
  assert.equal(equipmentNodes.length, 2);
  assert.deepEqual(equipmentNodes.map((node) => node.children[0].deviceIds), [[endpoint.id], [endpoint.id]]);
  assert.notEqual(equipmentNodes[0].children[0].key, equipmentNodes[1].children[0].key);
  assert.deepEqual(areaNode.deviceIds, [endpoint.id]);
});

test('hierarchy keeps the Device Endpoint layer when one Equipment has several communication endpoints', () => {
  const plantArea = area();
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const endpointA = device({ id: deviceId, displayName: 'Primary Controller' });
  const endpointB = device({ id: '01900000-0003-7000-8000-000000000002', displayName: 'Meter Gateway' });
  const model = siteAssetModel({
    areas: [plantArea],
    equipment: [equipmentA],
    devices: [endpointA, endpointB],
    relationships: [
      relationship('01900000-0004-7000-8000-000000000050', 'EQUIPMENT', equipmentA.id, 'AREA', plantArea.id),
      relationship('01900000-0004-7000-8000-000000000051', 'DEVICE', endpointA.id, 'EQUIPMENT', equipmentA.id),
      relationship('01900000-0004-7000-8000-000000000052', 'DEVICE', endpointB.id, 'EQUIPMENT', equipmentA.id),
    ],
  });

  const hierarchy = buildRealAssetsHierarchy(model, 'Test Site', now);
  const equipmentNode = hierarchy.children[0].children[0];
  assert.deepEqual(equipmentNode.children.map((node) => node.kind), ['device', 'device']);
  assert.deepEqual(equipmentNode.children.map((node) => node.deviceIds), [[endpointB.id], [endpointA.id]]);
  assert.notEqual(equipmentNode.children[0].key, equipmentNode.children[1].key);
});
