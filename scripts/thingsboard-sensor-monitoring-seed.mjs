import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const defaultConfigPath = path.join(repoRoot, 'deploy', 'platform', 'phase1', 'config', 'thingsboard-sensor-monitoring.v1.json');
const defaultRuntimeDir = path.join(repoRoot, 'deploy', 'platform', 'phase1', 'runtime', 'config', 'thingsboard');

export const thingsBoardTelemetryActions = Object.freeze([
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.history.read',
  'telemetry.resubscribe',
  'telemetry.recovery.use',
  'telemetry.recovery.checkpoint',
]);

const uuidV7Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const registryPointCodePattern = /^[a-z][a-z0-9_]{0,127}$/;
const telemetryKeyPattern = /^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/;

const sqlLiteral = (value) => {
  if (value === null || value === undefined) return 'NULL';
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('SQL numeric value must be finite');
    return String(value);
  }
  return `'${String(value).replaceAll("'", "''")}'`;
};

const sqlJSON = (value) => `${sqlLiteral(JSON.stringify(value))}::jsonb`;
const sqlTextArray = (values) => `ARRAY[${values.map(sqlLiteral).join(',')}]::text[]`;

function stableUUID(prefix, group, slot) {
  if (!Number.isInteger(slot) || slot < 1 || slot > 999999999999) throw new Error(`invalid stable slot ${slot}`);
  return `${prefix}-${group}-7000-8000-${String(slot).padStart(12, '0')}`;
}

export const thingsBoardIdentity = Object.freeze({
  sensorId: (slot) => stableUUID('01a05410', '2000', slot),
  pointId: (slot) => stableUUID('01a05410', '3000', slot),
  sensorDeviceBindingId: (slot) => stableUUID('01a05410', '4000', slot),
  pointProjectionId: (slot) => stableUUID('01a05410', '5000', slot),
  externalBindingId: (slot) => stableUUID('01a05410', '6000', slot),
  pointSubjectBindingId: (slot) => stableUUID('01a05410', '7000', slot),
  deviceAssetBindingId: (slot) => stableUUID('01a05420', '2000', slot),
  iamKeyBindingId: (slot) => `01910000-0000-7000-8000-${(0x5000 + slot).toString(16).padStart(12, '0')}`,
});

function requireUUIDv7(value, field) {
  if (!uuidV7Pattern.test(String(value ?? ''))) throw new Error(`${field} must be UUIDv7`);
}

function requireString(value, field) {
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`${field} is required`);
}

function requirePositiveInteger(value, field) {
  if (!Number.isInteger(value) || value <= 0) throw new Error(`${field} must be a positive integer`);
}

function addUnique(set, value, field) {
  if (set.has(value)) throw new Error(`${field} is duplicated: ${value}`);
  set.add(value);
}

export function validateThingsBoardSensorMonitoringConfig(config) {
  if (config?.schemaVersion !== 1) throw new Error('ThingsBoard sensor monitoring config schemaVersion must be 1');
  requireUUIDv7(config.tenantId, 'tenantId');
  requireUUIDv7(config.siteId, 'siteId');
  requireUUIDv7(config.principalId, 'principalId');
  requireUUIDv7(config.integrationInstanceId, 'integrationInstanceId');
  requireUUIDv7(config.asset?.id, 'asset.id');
  requireString(config.asset?.code, 'asset.code');
  requireString(config.asset?.displayName, 'asset.displayName');
  requireString(config.asset?.assetType, 'asset.assetType');
  requireString(config.sourceWorkloadSpiffe, 'sourceWorkloadSpiffe');
  if (!config.sourceWorkloadSpiffe.startsWith('spiffe://')) throw new Error('sourceWorkloadSpiffe must be a SPIFFE URI');
  if (!/^([1-9]|[1-5][0-9]|60)s$/.test(String(config.pollInterval ?? ''))) throw new Error('pollInterval must be between 1s and 60s');
  if (!Array.isArray(config.sources) || config.sources.length === 0) throw new Error('sources must not be empty');

  const sourceSlots = new Set();
  const sourceNames = new Set();
  const sourceExternalIds = new Set();
  const deviceIds = new Set();
  const sensorSlots = new Set();
  const pointSlots = new Set();
  const iamBindingSlots = new Set();
  const pointCodes = new Set();
  const telemetryKeys = new Set();
  let sensorCount = 0;
  let pointCount = 0;

  for (const source of config.sources) {
    requirePositiveInteger(source.slot, `source ${source.name ?? '?'} slot`);
    addUnique(sourceSlots, source.slot, 'source slot');
    requireString(source.name, 'source.name');
    addUnique(sourceNames, source.name, 'source name');
    if (!uuidPattern.test(String(source.externalId ?? ''))) throw new Error(`source ${source.name} externalId must be UUID`);
    addUnique(sourceExternalIds, source.externalId, 'source externalId');
    requireUUIDv7(source.deviceId, `source ${source.name} deviceId`);
    addUnique(deviceIds, source.deviceId, 'Device id');
    requireString(source.deviceCode, `source ${source.name} deviceCode`);
    requireString(source.deviceDisplayName, `source ${source.name} deviceDisplayName`);
    requireString(source.deviceType, `source ${source.name} deviceType`);
    requireString(source.assetBindingRole, `source ${source.name} assetBindingRole`);
    requirePositiveInteger(source.presence?.onlineWithinSeconds, `source ${source.name} onlineWithinSeconds`);
    requirePositiveInteger(source.presence?.offlineAfterSeconds, `source ${source.name} offlineAfterSeconds`);
    requirePositiveInteger(source.presence?.maxSourceLagSeconds, `source ${source.name} maxSourceLagSeconds`);
    if (!Array.isArray(source.sensors) || source.sensors.length === 0) throw new Error(`source ${source.name} sensors must not be empty`);
    if (!Array.isArray(source.points) || source.points.length === 0) throw new Error(`source ${source.name} points must not be empty`);

    const sourceSensorSlots = new Set();
    for (const sensor of source.sensors) {
      requirePositiveInteger(sensor.slot, `source ${source.name} sensor slot`);
      addUnique(sensorSlots, sensor.slot, 'sensor slot');
      sourceSensorSlots.add(sensor.slot);
      requireString(sensor.code, `source ${source.name} sensor code`);
      requireString(sensor.displayName, `source ${source.name} sensor displayName`);
      requireString(sensor.sensorType, `source ${source.name} sensor sensorType`);
      sensorCount += 1;
    }

    const sourceKeys = new Set();
    for (const point of source.points) {
      requirePositiveInteger(point.slot, `source ${source.name} point slot`);
      addUnique(pointSlots, point.slot, 'point slot');
      const iamBindingSlot = point.iamBindingSlot ?? point.slot;
      requirePositiveInteger(iamBindingSlot, `point ${point.pointCode ?? point.slot} IAM binding slot`);
      addUnique(iamBindingSlots, iamBindingSlot, 'IAM binding slot');
      if (!sourceSensorSlots.has(point.sensorSlot)) throw new Error(`point ${point.pointCode ?? point.slot} references a Sensor outside source ${source.name}`);
      requireString(point.sourceKey, `source ${source.name} point sourceKey`);
      addUnique(sourceKeys, point.sourceKey, `source ${source.name} sourceKey`);
      if (!telemetryKeyPattern.test(String(point.telemetryKey ?? ''))) throw new Error(`point ${point.pointCode ?? point.slot} telemetryKey is invalid`);
      addUnique(telemetryKeys, point.telemetryKey, 'telemetryKey');
      if (!registryPointCodePattern.test(String(point.pointCode ?? ''))) throw new Error(`point ${point.pointCode ?? point.slot} pointCode is invalid`);
      addUnique(pointCodes, point.pointCode, 'pointCode');
      if (point.pointCode !== point.telemetryKey) {
        throw new Error(`point ${point.pointCode} must use the same canonical key in Registry and Telemetry Runtime until the public Asset Model exposes telemetryKey explicitly`);
      }
      requireString(point.displayName, `point ${point.pointCode} displayName`);
      if (!['TELEMETRY', 'COUNTER'].includes(point.pointType)) throw new Error(`point ${point.pointCode} pointType is unsupported`);
      if (point.valueType !== 'NUMBER') throw new Error(`point ${point.pointCode} valueType must be NUMBER`);
      requireString(point.unit, `point ${point.pointCode} unit`);
      if (!Number.isFinite(point.scale) || point.scale === 0) throw new Error(`point ${point.pointCode} scale must be finite and non-zero`);
      requirePositiveInteger(point.sampleIntervalMs, `point ${point.pointCode} sampleIntervalMs`);
      requirePositiveInteger(point.staleAfterMs, `point ${point.pointCode} staleAfterMs`);
      requirePositiveInteger(point.freshWithinSeconds, `point ${point.pointCode} freshWithinSeconds`);
      requirePositiveInteger(point.expectedSampleIntervalSeconds, `point ${point.pointCode} expectedSampleIntervalSeconds`);
      for (const [name, value] of [['minimumNumber', point.minimumNumber], ['maximumNumber', point.maximumNumber]]) {
        if (value !== null && value !== undefined && !Number.isFinite(value)) throw new Error(`point ${point.pointCode} ${name} must be finite or null`);
      }
      pointCount += 1;
    }
  }

  return Object.freeze({ sourceCount: config.sources.length, sensorCount, pointCount });
}

export function loadThingsBoardSensorMonitoringConfig(configPath = defaultConfigPath) {
  const config = JSON.parse(readFileSync(configPath, 'utf8'));
  validateThingsBoardSensorMonitoringConfig(config);
  return config;
}

function flattenConfig(config) {
  const sensors = [];
  const points = [];
  for (const source of config.sources) {
    for (const sensor of source.sensors) sensors.push({ ...sensor, source });
    for (const point of source.points) points.push({ ...point, source });
  }
  sensors.sort((left, right) => left.slot - right.slot);
  points.sort((left, right) => left.slot - right.slot);
  return { sensors, points };
}

export function buildThingsBoardReconcilerConfig(config) {
  validateThingsBoardSensorMonitoringConfig(config);
  return {
    integrationInstanceId: config.integrationInstanceId,
    pollInterval: config.pollInterval,
    sources: config.sources.map((source) => ({
      name: source.name,
      externalId: source.externalId,
      points: source.points.map((point) => ({
        sourceKey: point.sourceKey,
        telemetryKey: point.telemetryKey,
        unit: point.unit,
        scale: point.scale,
        ...(point.offset ? { offset: point.offset } : {}),
      })),
    })),
  };
}

export function buildThingsBoardS1SeedSQL(config) {
  validateThingsBoardSensorMonitoringConfig(config);
  const { sensors, points } = flattenConfig(config);
  const sensorSourceBySlot = new Map(sensors.map((sensor) => [sensor.slot, sensor.source]));
  const actions = sqlTextArray(thingsBoardTelemetryActions);

  const deviceRows = config.sources.map((source) => `(${sqlLiteral(source.deviceId)},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(source.deviceCode)},${sqlLiteral(source.deviceDisplayName)},${sqlLiteral(source.deviceType)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const externalBindingRows = config.sources.map((source) => `(${sqlLiteral(thingsBoardIdentity.externalBindingId(source.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(config.integrationInstanceId)},'thingsboard','DEVICE',${sqlLiteral(source.externalId)},'ACTIVE','2000-01-01T00:00:00Z',NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const sensorRows = sensors.map((sensor) => {
    const sourcePointKeys = sensor.source.points.filter((point) => point.sensorSlot === sensor.slot).map((point) => point.sourceKey);
    const metadata = { sourceSystem: 'thingsboard', aggregateDevice: sensor.source.name, sourceKeys: sourcePointKeys };
    return `(${sqlLiteral(thingsBoardIdentity.sensorId(sensor.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(sensor.code)},${sqlLiteral(sensor.displayName)},${sqlLiteral(sensor.sensorType)},NULL,NULL,NULL,NULL,${sqlJSON(metadata)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const sensorDeviceRows = sensors.map((sensor) => `(${sqlLiteral(thingsBoardIdentity.sensorDeviceBindingId(sensor.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(thingsBoardIdentity.sensorId(sensor.slot))},${sqlLiteral(sensor.source.deviceId)},'REPORTS_THROUGH','ACTIVE','2000-01-01T00:00:00Z',NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const pointRows = points.map((point) => {
    const metadata = { sourceSystem: 'thingsboard', aggregateDevice: point.source.name, sourceKey: point.sourceKey, scale: point.scale, offset: point.offset ?? 0 };
    const counterMode = point.pointType === 'COUNTER' ? sqlLiteral('RESET_TO_ZERO') : 'NULL';
    return `(${sqlLiteral(thingsBoardIdentity.pointId(point.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(point.source.deviceId)},${sqlLiteral(thingsBoardIdentity.sensorId(point.sensorSlot))},${sqlLiteral(point.pointCode)},${sqlLiteral(point.sourceKey)},${sqlLiteral(point.displayName)},${sqlLiteral(point.pointType)},${sqlLiteral(point.valueType)},${sqlLiteral(point.unit)},false,${point.sampleIntervalMs},${point.sampleIntervalMs},${point.staleAfterMs},${counterMode},NULL,${sqlJSON(metadata)},'ACTIVE',1,clock_timestamp(),clock_timestamp())`;
  }).join(',\n  ');
  const deviceAssetRows = config.sources.map((source) => `(${sqlLiteral(thingsBoardIdentity.deviceAssetBindingId(source.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(source.deviceId)},${sqlLiteral(config.asset.id)},${sqlLiteral(source.assetBindingRole)},'ACTIVE','2000-01-01T00:00:00Z',NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const pointSubjectRows = points.map((point) => `(${sqlLiteral(thingsBoardIdentity.pointSubjectBindingId(point.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(thingsBoardIdentity.pointId(point.slot))},'ASSET',NULL,${sqlLiteral(config.asset.id)},'DESCRIBES','ACTIVE','2000-01-01T00:00:00Z',NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');
  const keyBindingRows = points.map((point) => `(${sqlLiteral(thingsBoardIdentity.iamKeyBindingId(point.iamBindingSlot ?? point.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.principalId)},${sqlLiteral(point.source.deviceId)},${sqlLiteral(point.telemetryKey)},${actions},'ALLOW','ACTIVE','2000-01-01T00:00:00Z',NULL,1,clock_timestamp(),clock_timestamp())`).join(',\n  ');

  for (const point of points) {
    if (!sensorSourceBySlot.has(point.sensorSlot)) throw new Error(`missing Sensor source for ${point.pointCode}`);
  }

  return `BEGIN;
INSERT INTO core_registry.assets (id,tenant_id,site_id,code,display_name,asset_type,status,revision,created_at,updated_at)
VALUES (${sqlLiteral(config.asset.id)},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(config.asset.code)},${sqlLiteral(config.asset.displayName)},${sqlLiteral(config.asset.assetType)},'ACTIVE',1,clock_timestamp(),clock_timestamp())
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,code=EXCLUDED.code,display_name=EXCLUDED.display_name,asset_type=EXCLUDED.asset_type,status='ACTIVE',updated_at=clock_timestamp();

INSERT INTO core_registry.devices (id,tenant_id,site_id,code,display_name,device_type,status,revision,created_at,updated_at) VALUES
  ${deviceRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,code=EXCLUDED.code,display_name=EXCLUDED.display_name,device_type=EXCLUDED.device_type,status='ACTIVE',updated_at=clock_timestamp();

INSERT INTO core_registry.external_bindings (id,tenant_id,site_id,integration_instance_id,provider,external_entity_type,external_id,binding_status,valid_from,valid_to,revision,created_at,updated_at) VALUES
  ${externalBindingRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,integration_instance_id=EXCLUDED.integration_instance_id,provider=EXCLUDED.provider,external_entity_type=EXCLUDED.external_entity_type,external_id=EXCLUDED.external_id,binding_status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp();

INSERT INTO core_registry.sensors (id,tenant_id,site_id,code,display_name,sensor_type,manufacturer,model,serial_number,calibration_due_at,metadata,status,revision,created_at,updated_at) VALUES
  ${sensorRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,code=EXCLUDED.code,display_name=EXCLUDED.display_name,sensor_type=EXCLUDED.sensor_type,metadata=EXCLUDED.metadata,status='ACTIVE',updated_at=clock_timestamp();

INSERT INTO core_registry.sensor_device_bindings (id,tenant_id,site_id,sensor_id,device_id,binding_role,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
  ${sensorDeviceRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,sensor_id=EXCLUDED.sensor_id,device_id=EXCLUDED.device_id,binding_role='REPORTS_THROUGH',status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp();

INSERT INTO core_registry.telemetry_points (id,tenant_id,site_id,reporting_device_id,sensor_id,point_code,source_key,display_name,point_type,value_type,unit,writable,sample_interval_ms,publish_interval_ms,stale_after_ms,counter_decrease_mode,counter_rollover_modulus,source_metadata,status,revision,created_at,updated_at) VALUES
  ${pointRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,reporting_device_id=EXCLUDED.reporting_device_id,sensor_id=EXCLUDED.sensor_id,point_code=EXCLUDED.point_code,source_key=EXCLUDED.source_key,display_name=EXCLUDED.display_name,point_type=EXCLUDED.point_type,value_type=EXCLUDED.value_type,unit=EXCLUDED.unit,writable=false,sample_interval_ms=EXCLUDED.sample_interval_ms,publish_interval_ms=EXCLUDED.publish_interval_ms,stale_after_ms=EXCLUDED.stale_after_ms,counter_decrease_mode=EXCLUDED.counter_decrease_mode,counter_rollover_modulus=EXCLUDED.counter_rollover_modulus,source_metadata=EXCLUDED.source_metadata,status='ACTIVE',updated_at=clock_timestamp();

INSERT INTO core_registry.device_bindings (id,tenant_id,site_id,device_id,asset_id,binding_role,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
  ${deviceAssetRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,device_id=EXCLUDED.device_id,asset_id=EXCLUDED.asset_id,binding_role=EXCLUDED.binding_role,status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp();

INSERT INTO core_registry.point_subject_bindings (id,tenant_id,site_id,point_id,subject_type,space_id,asset_id,binding_role,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
  ${pointSubjectRows}
ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,point_id=EXCLUDED.point_id,subject_type='ASSET',space_id=NULL,asset_id=EXCLUDED.asset_id,binding_role='DESCRIBES',status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp();

WITH changed AS (
  INSERT INTO iam.telemetry_key_bindings AS existing (id,tenant_id,principal_id,device_id,telemetry_key,actions,effect,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
    ${keyBindingRows}
  ON CONFLICT (id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,principal_id=EXCLUDED.principal_id,device_id=EXCLUDED.device_id,telemetry_key=EXCLUDED.telemetry_key,actions=EXCLUDED.actions,effect='ALLOW',status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp()
  WHERE existing.tenant_id IS DISTINCT FROM EXCLUDED.tenant_id
     OR existing.principal_id IS DISTINCT FROM EXCLUDED.principal_id
     OR existing.device_id IS DISTINCT FROM EXCLUDED.device_id
     OR existing.telemetry_key IS DISTINCT FROM EXCLUDED.telemetry_key
     OR existing.actions IS DISTINCT FROM EXCLUDED.actions
     OR existing.effect IS DISTINCT FROM 'ALLOW'
     OR existing.status IS DISTINCT FROM 'ACTIVE'
     OR existing.valid_to IS NOT NULL
  RETURNING 1
)
UPDATE iam.authorization_revisions
SET revision=revision+1,updated_at=clock_timestamp()
WHERE tenant_id=${sqlLiteral(config.tenantId)} AND EXISTS (SELECT 1 FROM changed);
COMMIT;
`;
}

export function buildThingsBoardS2SeedSQL(config) {
  validateThingsBoardSensorMonitoringConfig(config);
  const { points } = flattenConfig(config);
  const deviceRows = config.sources.map((source) => `(${sqlLiteral(source.deviceId)},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(config.integrationInstanceId)},'DEVICE',${sqlLiteral(source.externalId)},'ACTIVE',1,1,'2000-01-01T00:00:00Z',NULL,clock_timestamp(),'APPLICABLE')`).join(',\n  ');
  const pointRows = points.map((point) => {
    const counterMode = point.pointType === 'COUNTER' ? sqlLiteral('RESET_TO_ZERO') : 'NULL';
    return `(${sqlLiteral(thingsBoardIdentity.pointProjectionId(point.slot))},${sqlLiteral(config.tenantId)},${sqlLiteral(config.siteId)},${sqlLiteral(thingsBoardIdentity.pointId(point.slot))},${sqlLiteral(thingsBoardIdentity.sensorId(point.sensorSlot))},${sqlLiteral(point.source.deviceId)},${sqlLiteral(point.telemetryKey)},${sqlLiteral(point.pointType)},${sqlLiteral(point.valueType)},${sqlLiteral(point.unit)},${counterMode},NULL,'ACTIVE',1,1,'2000-01-01T00:00:00Z',NULL,clock_timestamp())`;
  }).join(',\n  ');
  const presenceRows = config.sources.map((source) => `(${sqlLiteral(source.deviceId)},1,${source.presence.onlineWithinSeconds},${source.presence.offlineAfterSeconds},true,ARRAY['SOURCE_ACTIVITY']::text[],30,${source.presence.maxSourceLagSeconds},clock_timestamp())`).join(',\n  ');
  const freshnessRows = points.map((point) => `(${sqlLiteral(point.source.deviceId)},${sqlLiteral(point.telemetryKey)},1,${point.freshWithinSeconds},true,${point.expectedSampleIntervalSeconds},${sqlLiteral(point.valueType)},${sqlLiteral(point.unit)},${sqlLiteral(point.minimumNumber)},${sqlLiteral(point.maximumNumber)},clock_timestamp())`).join(',\n  ');
  const coverageRows = config.sources.map((source) => `(${sqlLiteral(source.deviceId)},true,clock_timestamp(),NULL,1,clock_timestamp())`).join(',\n  ');

  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
INSERT INTO telemetry_runtime.registry_device_bindings (device_id,tenant_id,site_id,integration_instance_id,external_entity_type,external_id,binding_status,binding_revision,source_registry_revision,valid_from,valid_to,updated_at,presence_applicability) VALUES
  ${deviceRows}
ON CONFLICT (device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,integration_instance_id=EXCLUDED.integration_instance_id,external_entity_type='DEVICE',external_id=EXCLUDED.external_id,binding_status='ACTIVE',valid_to=NULL,presence_applicability='APPLICABLE',updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.registry_point_bindings (projection_id,tenant_id,site_id,point_id,sensor_id,device_id,telemetry_key,point_type,value_type,unit,counter_decrease_mode,counter_rollover_modulus,binding_status,point_revision,source_registry_revision,valid_from,valid_to,updated_at) VALUES
  ${pointRows}
ON CONFLICT (projection_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,site_id=EXCLUDED.site_id,point_id=EXCLUDED.point_id,sensor_id=EXCLUDED.sensor_id,device_id=EXCLUDED.device_id,telemetry_key=EXCLUDED.telemetry_key,point_type=EXCLUDED.point_type,value_type=EXCLUDED.value_type,unit=EXCLUDED.unit,counter_decrease_mode=EXCLUDED.counter_decrease_mode,counter_rollover_modulus=EXCLUDED.counter_rollover_modulus,binding_status='ACTIVE',valid_to=NULL,updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.presence_policies (device_id,policy_revision,online_within_seconds,offline_after_seconds,coverage_required,accepted_signal_types,max_future_clock_skew_seconds,max_source_lag_seconds,updated_at) VALUES
  ${presenceRows}
ON CONFLICT (device_id) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,online_within_seconds=EXCLUDED.online_within_seconds,offline_after_seconds=EXCLUDED.offline_after_seconds,coverage_required=true,accepted_signal_types=EXCLUDED.accepted_signal_types,max_future_clock_skew_seconds=EXCLUDED.max_future_clock_skew_seconds,max_source_lag_seconds=EXCLUDED.max_source_lag_seconds,updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.freshness_policies (device_id,telemetry_key,policy_revision,fresh_within_seconds,configured,expected_sample_interval_seconds,value_type,expected_unit,minimum_number,maximum_number,updated_at) VALUES
  ${freshnessRows}
ON CONFLICT (device_id,telemetry_key) DO UPDATE SET policy_revision=EXCLUDED.policy_revision,fresh_within_seconds=EXCLUDED.fresh_within_seconds,configured=true,expected_sample_interval_seconds=EXCLUDED.expected_sample_interval_seconds,value_type=EXCLUDED.value_type,expected_unit=EXCLUDED.expected_unit,minimum_number=EXCLUDED.minimum_number,maximum_number=EXCLUDED.maximum_number,updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.observation_coverage (device_id,available,continuous_since,reason_code,source_revision,updated_at) VALUES
  ${coverageRows}
ON CONFLICT (device_id) DO UPDATE SET available=true,reason_code=NULL,source_revision=EXCLUDED.source_revision,updated_at=clock_timestamp();
RESET ROLE;
COMMIT;
`;
}

export function writeThingsBoardRuntimeArtifacts(config, outputDirectory = defaultRuntimeDir) {
  validateThingsBoardSensorMonitoringConfig(config);
  mkdirSync(outputDirectory, { recursive: true });
  const paths = {
    reconciler: path.join(outputDirectory, 'thingsboard-reconciler.json'),
    s1: path.join(outputDirectory, 'thingsboard-s1.sql'),
    s2: path.join(outputDirectory, 'thingsboard-s2.sql'),
  };
  writeFileSync(paths.reconciler, `${JSON.stringify(buildThingsBoardReconcilerConfig(config), null, 2)}\n`, { mode: 0o644 });
  writeFileSync(paths.s1, buildThingsBoardS1SeedSQL(config), { mode: 0o600 });
  writeFileSync(paths.s2, buildThingsBoardS2SeedSQL(config), { mode: 0o600 });
  return paths;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options });
  if (result.status !== 0) {
    const detail = String(result.stderr || result.stdout || '').trim();
    throw new Error(`${command} ${args.join(' ')} failed${detail ? `: ${detail}` : ''}`);
  }
  return String(result.stdout ?? '').trim();
}

function resolvePostgresContainer() {
  if (process.env.PHASE1_POSTGRES_CONTAINER?.trim()) return process.env.PHASE1_POSTGRES_CONTAINER.trim();
  const project = process.env.PHASE1_COMPOSE_PROJECT_NAME?.trim() || process.env.COMPOSE_PROJECT_NAME?.trim() || 'hvac-phase1-local';
  const container = run('docker', [
    'ps',
    '--filter', `label=com.docker.compose.project=${project}`,
    '--filter', 'label=com.docker.compose.service=postgres',
    '--format', '{{.ID}}',
  ]).split(/\r?\n/).find(Boolean);
  if (!container) throw new Error(`Postgres container for Compose project ${project} is not running`);
  return container;
}

function applySQL(container, database, sql) {
  const user = process.env.POSTGRES_ADMIN_USER?.trim() || 'postgres';
  const result = spawnSync('docker', ['exec', '-i', container, 'psql', '-v', 'ON_ERROR_STOP=1', '-U', user, '-d', database], {
    input: sql,
    encoding: 'utf8',
    stdio: ['pipe', 'inherit', 'inherit'],
  });
  if (result.status !== 0) throw new Error(`ThingsBoard seed failed for ${database}`);
}

export function applyThingsBoardSensorMonitoringSeed(config) {
  const container = resolvePostgresContainer();
  applySQL(container, 'hvac_s1', buildThingsBoardS1SeedSQL(config));
  applySQL(container, 'hvac_s2', buildThingsBoardS2SeedSQL(config));
  return container;
}

function optionValue(args, name, fallback) {
  const index = args.indexOf(name);
  if (index === -1) return fallback;
  const value = args[index + 1];
  if (!value || value.startsWith('--')) throw new Error(`${name} requires a value`);
  return value;
}

function cli() {
  const args = process.argv.slice(2);
  const command = args[0] && !args[0].startsWith('--') ? args[0] : 'check';
  const configPath = path.resolve(optionValue(args, '--config', defaultConfigPath));
  const config = loadThingsBoardSensorMonitoringConfig(configPath);
  const summary = validateThingsBoardSensorMonitoringConfig(config);

  if (command === 'check') {
    console.log(`thingsboard-sensor-monitoring config=OK sources=${summary.sourceCount} sensors=${summary.sensorCount} points=${summary.pointCount}`);
    return;
  }
  if (command === 'write') {
    const outputDirectory = path.resolve(optionValue(args, '--out-dir', defaultRuntimeDir));
    const written = writeThingsBoardRuntimeArtifacts(config, outputDirectory);
    console.log(`thingsboard-sensor-monitoring wrote ${written.reconciler}`);
    console.log(`thingsboard-sensor-monitoring wrote ${written.s1}`);
    console.log(`thingsboard-sensor-monitoring wrote ${written.s2}`);
    return;
  }
  if (command === 'apply') {
    const outputDirectory = path.resolve(optionValue(args, '--out-dir', defaultRuntimeDir));
    writeThingsBoardRuntimeArtifacts(config, outputDirectory);
    const container = applyThingsBoardSensorMonitoringSeed(config);
    console.log(`thingsboard-sensor-monitoring applied postgres=${container} sources=${summary.sourceCount} sensors=${summary.sensorCount} points=${summary.pointCount}`);
    return;
  }
  throw new Error(`unsupported command ${command}; use check, write, or apply`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    cli();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
