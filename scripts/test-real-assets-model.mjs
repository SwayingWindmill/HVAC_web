import assert from 'node:assert/strict';
import test from 'node:test';
import {
  listTelemetryKeys,
  resolveRealAssetsProfile,
} from '../apps/hvac-web/src/real/assets/catalog.ts';
import {
  buildRealAssetsRows,
  projectRealAssetsOperatingState,
  resolveDeviceBinding,
} from '../apps/hvac-web/src/real/assets/model.ts';

const organizationId = '01900000-0000-7000-8000-000000000001';
const siteId = '01900000-0001-7000-8000-000000000001';
const equipmentAId = '01900000-0002-7000-8000-000000000001';
const equipmentBId = '01900000-0002-7000-8000-000000000002';
const deviceId = '01900000-0003-7000-8000-000000000001';
const now = new Date('2026-07-30T10:00:00.000Z');

function device(overrides = {}) {
  return {
    id: deviceId,
    owningOrganizationId: organizationId,
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
    owningOrganizationId: organizationId,
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

function binding(id, equipmentId, revision = 1) {
  return {
    id,
    owningOrganizationId: organizationId,
    siteId,
    deviceId,
    equipmentId,
    bindingRole: 'PRIMARY_CONTROLLER',
    status: 'ACTIVE',
    validFrom: '2026-07-01T00:00:00.000Z',
    validTo: null,
    revision,
    createdAt: '2026-07-01T00:00:00.000Z',
    updatedAt: '2026-07-30T00:00:00.000Z',
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
    owningOrganizationId: organizationId,
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

test('missing and suspect critical points require attention without turning valid values into missing', () => {
  const missing = {
    key: 'chiller.cop',
    state: 'MISSING',
    freshness: 'MISSING',
    missingReason: 'ONLY_REJECTED_CANDIDATES',
    policyRevision: 5,
  };
  const values = goodValues.map((value) => value.key === 'chiller.cop' ? missing : value);
  values[0] = { ...values[0], quality: 'SUSPECT', qualityReasons: ['SOURCE_UNTRUSTED'] };
  const projection = projectRealAssetsOperatingState({ status: 'ok', snapshot: snapshot(values) }, chillerProfile);
  assert.equal(projection.state, 'ATTENTION');
  assert.ok(projection.reasons.includes('CRITICAL_POINT_MISSING'));
  assert.ok(projection.reasons.includes('TELEMETRY_SUSPECT'));
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

test('DeviceBinding resolves one current Equipment and exposes ambiguous or unbound states', () => {
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const equipmentB = equipment(equipmentBId, 'Beta Chiller');
  const equipmentById = new Map([[equipmentA.id, equipmentA], [equipmentB.id, equipmentB]]);
  const bindingA = binding('01900000-0004-7000-8000-000000000001', equipmentAId);
  const bindingB = binding('01900000-0004-7000-8000-000000000002', equipmentBId, 2);

  assert.equal(resolveDeviceBinding(device(), [bindingA], equipmentById, now).state, 'bound');
  assert.equal(resolveDeviceBinding(device(), [], equipmentById, now).state, 'unbound');
  const ambiguous = resolveDeviceBinding(device(), [bindingA, bindingB], equipmentById, now);
  assert.equal(ambiguous.state, 'ambiguous');
  assert.equal(ambiguous.bindingIds.length, 2);
});

test('rows sort by Equipment then Device identity while preserving unbound Devices', () => {
  const equipmentA = equipment(equipmentAId, 'Alpha Chiller');
  const equipmentB = equipment(equipmentBId, 'Beta Chiller');
  const deviceA = device({ id: deviceId, displayName: 'Device B' });
  const deviceB = device({ id: '01900000-0003-7000-8000-000000000002', displayName: 'Device A' });
  const deviceC = device({ id: '01900000-0003-7000-8000-000000000003', displayName: 'Unbound' });
  const rows = buildRealAssetsRows({
    devices: [deviceC, deviceA, deviceB],
    equipment: [equipmentB, equipmentA],
    bindings: [
      binding('01900000-0004-7000-8000-000000000010', equipmentBId),
      { ...binding('01900000-0004-7000-8000-000000000011', equipmentAId), deviceId: deviceB.id },
    ],
    snapshots: new Map(),
    now,
  });
  assert.deepEqual(rows.map((row) => row.device.displayName), ['Device A', 'Device B', 'Unbound']);
});
