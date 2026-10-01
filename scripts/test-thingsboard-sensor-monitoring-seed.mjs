import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildThingsBoardReconcilerConfig,
  buildThingsBoardS1SeedSQL,
  buildThingsBoardS2SeedSQL,
  loadThingsBoardSensorMonitoringConfig,
  thingsBoardIdentity,
  thingsBoardTelemetryActions,
  validateThingsBoardSensorMonitoringConfig,
} from './thingsboard-sensor-monitoring-seed.mjs';

const config = loadThingsBoardSensorMonitoringConfig();

function allPoints() {
  return config.sources.flatMap((source) => source.points.map((point) => ({ source, point })));
}

test('ThingsBoard sensor monitoring config preserves the accepted 3/22/24 topology', () => {
  assert.deepEqual(validateThingsBoardSensorMonitoringConfig(config), {
    sourceCount: 3,
    sensorCount: 22,
    pointCount: 24,
  });
  assert.deepEqual(config.sources.map((source) => source.name), ['temperature', 'waterflow', 'ammeter']);
  assert.equal(config.asset.id, '01a05420-1000-7000-8000-000000000001');
  assert.equal(config.integrationInstanceId, '01a05400-0000-7000-8000-000000000001');
  assert.equal(config.sourceWorkloadSpiffe, 'spiffe://hvac.local/thingsboard-reconciler');
});

test('ThingsBoard source keys stay external while Registry and S2 use the canonical accepted keys', () => {
  const points = allPoints();
  assert.equal(points.length, 24);
  for (const { point } of points) {
    assert.equal(point.pointCode, point.telemetryKey);
    assert.match(point.pointCode, /^[a-z][a-z0-9_]{0,127}$/);
    assert.match(point.telemetryKey, /^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/);
  }
  const temperature = points.find(({ point }) => point.sourceKey === 't10')?.point;
  assert.equal(temperature?.telemetryKey, 'temperaturet10');
  assert.equal(temperature?.scale, 0.1);
  const flow = points.find(({ point }) => point.sourceKey === 'flowMeter')?.point;
  assert.equal(flow?.telemetryKey, 'waterflowflowmeter');
  const energy = points.find(({ point }) => point.sourceKey === 'combinedActiveTotalElectricalEnergy')?.point;
  assert.equal(energy?.telemetryKey, 'ammeteractiveenergytotal');
  assert.equal(energy?.pointType, 'COUNTER');
});

test('ThingsBoard reconciler config uses three authorized REST reads and contains no credentials or persistence contract', () => {
  const reconciler = buildThingsBoardReconcilerConfig(config);
  assert.equal(reconciler.sources.length, 3);
  assert.equal(reconciler.sources.reduce((count, source) => count + source.points.length, 0), 24);
  assert.deepEqual(reconciler.sources[0].points[9], {
    sourceKey: 't10',
    telemetryKey: 'temperaturet10',
    unit: '°C',
    scale: 0.1,
  });
  const serialized = JSON.stringify(reconciler);
  assert.doesNotMatch(serialized, /password|username|token|ts_kv_latest|key_dictionary/i);
});

test('S1 seed converges stable Registry, Asset and exact-key IAM identities', () => {
  const sql = buildThingsBoardS1SeedSQL(config);
  assert.match(sql, /INSERT INTO core_registry\.assets/);
  assert.match(sql, /INSERT INTO core_registry\.external_bindings/);
  assert.match(sql, /INSERT INTO core_registry\.telemetry_points/);
  assert.match(sql, /INSERT INTO iam\.telemetry_key_bindings AS existing/);
  assert.match(sql, /UPDATE iam\.authorization_revisions/);
  assert.match(sql, new RegExp(thingsBoardIdentity.sensorId(1)));
  assert.match(sql, new RegExp(thingsBoardIdentity.pointId(24)));
  assert.match(sql, new RegExp(thingsBoardIdentity.deviceAssetBindingId(3)));
  assert.match(sql, new RegExp(`${thingsBoardIdentity.iamKeyBindingId(23)}[^\\n]*ammeteractiveenergytotal`));
  assert.match(sql, new RegExp(`${thingsBoardIdentity.iamKeyBindingId(24)}[^\\n]*ammeterpowertotal`));
  for (const action of thingsBoardTelemetryActions) assert.match(sql, new RegExp(action.replaceAll('.', '\\.')));
  for (const { point } of allPoints()) assert.match(sql, new RegExp(point.telemetryKey));
});

test('S2 seed projects the same 24 Point identities and canonical keys', () => {
  const sql = buildThingsBoardS2SeedSQL(config);
  assert.match(sql, /SET LOCAL ROLE s2_telemetry_migrator/);
  assert.match(sql, /INSERT INTO telemetry_runtime\.registry_device_bindings/);
  assert.match(sql, /INSERT INTO telemetry_runtime\.registry_point_bindings/);
  assert.match(sql, /INSERT INTO telemetry_runtime\.presence_policies/);
  assert.match(sql, /INSERT INTO telemetry_runtime\.freshness_policies/);
  assert.match(sql, /INSERT INTO telemetry_runtime\.observation_coverage/);
  for (const { point } of allPoints()) {
    assert.match(sql, new RegExp(thingsBoardIdentity.pointProjectionId(point.slot)));
    assert.match(sql, new RegExp(point.telemetryKey));
  }
});
