// Registers a real Site behind one Edge Gateway from its site file (deploy/sites/*.site.json):
// the Site, its plant room, the Gateway, every Device the Gateway names by source key, their
// Points, and the Telemetry presence and freshness policies. Same tables as the simulator seed.
// Idempotent; names edited later in the Registry are overwritten by the file. Machines the
// site file lists become Assets, with the meter wired to each bound to it.
//
//   node scripts/site-onboarding.mjs deploy/sites/songyaxuan.site.json [--print]
//
// PHASE1_POSTGRES_CONTAINER selects the database container (default: the local environment's).
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { localContainer } from './lib/local-environment.mjs';

const sqlLiteral = (value) => `'${String(value).replaceAll("'", "''")}'`;
const sqlJson = (value) => `${sqlLiteral(JSON.stringify(value))}::jsonb`;

// Row ids derive from the Site id so a second run updates the same rows.
function rowId(siteId, kind, index) {
  return siteId.replace(/^([0-9a-f]{8})-[0-9a-f]{4}-7000-8000-[0-9a-f]{12}$/, (_, head) => `${head}-${kind}-7000-8000-${index.toString(16).padStart(12, '0')}`);
}

export function expandPoints(device, meterPoints) {
  return (device.points === 'meter' ? meterPoints : device.points).map((point) => ({ ...point }));
}

export function buildOnboardingSql(profile) {
  const tenant = sqlLiteral(profile.tenantId);
  const { site, space, gateway } = profile;
  const siteId = sqlLiteral(site.id);
  const devices = profile.devices.map((device, deviceIndex) => ({ ...device, deviceIndex: deviceIndex + 1, points: expandPoints(device, profile.meterPoints) }));
  const points = devices.flatMap((device) => device.points.map((point, pointIndex) => ({
    ...point, device, id: rowId(site.id, '5000', device.deviceIndex * 0x10000 + pointIndex + 1),
  })));
  for (const point of points) {
    if (!/^[a-z][a-z0-9_]{0,127}$/.test(point.code)) throw new Error(`point code ${point.code} is not a Gateway wire code`);
  }

  const deviceRows = [
    `(${sqlLiteral(gateway.id)}, ${siteId}, ${sqlLiteral(gateway.code)}, ${sqlLiteral(gateway.name)}, 'GATEWAY', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenant}, NULL, NULL)`,
    ...devices.map((device) => `(${sqlLiteral(device.id)}, ${siteId}, ${sqlLiteral(device.code)}, ${sqlLiteral(device.name)}, ${sqlLiteral(device.type)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenant}, NULL, NULL)`),
  ].join(',\n');
  const sourceKeyRows = devices.map((device) => `(${sqlLiteral(rowId(site.id, '6100', device.deviceIndex))}, ${tenant}, ${siteId}, ${sqlLiteral(gateway.id)}, ${sqlLiteral(device.sourceKey)}, ${sqlLiteral(device.id)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n');
  const placementRows = devices.map((device) => `(${sqlLiteral(rowId(site.id, '6200', device.deviceIndex))}, ${tenant}, ${siteId}, ${sqlLiteral(device.id)}, ${sqlLiteral(space.id)}, 'INSTALLED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`).join(',\n');
  const assets = profile.assets ?? [];
  const assetRows = assets.map((asset) => `(${sqlLiteral(asset.id)}, ${siteId}, ${sqlLiteral(asset.code)}, ${sqlLiteral(asset.name)}, ${sqlLiteral(asset.type)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenant})`).join(',\n');
  const assetPlacementRows = assets.map((asset, index) => `(${sqlLiteral(rowId(site.id, '6500', index + 1))}, ${tenant}, ${siteId}, ${sqlLiteral(asset.id)}, ${sqlLiteral(space.id)}, 'INSTALLED_IN', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`).join(',\n');
  // A meter wired to one machine is that machine's meter.
  const deviceBindingRows = devices.filter((device) => device.asset).map((device) => `(${sqlLiteral(rowId(site.id, '6400', device.deviceIndex))}, ${siteId}, ${sqlLiteral(device.id)}, ${sqlLiteral(device.asset)}, ${device.type.endsWith('METER') ? "'METER'" : "'CONTROLLER'"}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${tenant})`).join(',\n');
  const pointRows = points.map((point) => {
    const interval = point.device.sampleSeconds * 1000;
    return `(${sqlLiteral(point.id)}, ${tenant}, ${siteId}, ${sqlLiteral(point.device.id)}, NULL, ${sqlLiteral(point.code)}, ${sqlLiteral(point.code)}, ${sqlLiteral(point.name)}, ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, false, ${interval}, ${interval}, ${interval * 3}, ${sqlJson({ protocol: 'MODBUS_RTU', address: point.address, gatewaySourceKey: point.device.sourceKey })}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), NULL, NULL, ${point.pointType === 'COUNTER' ? "'RESET_TO_ZERO'" : 'NULL'}, NULL)`;
  }).join(',\n');
  // A point describes the machine it measures, else the Site.
  const subjectRows = points.map((point, index) => {
    const asset = point.asset ?? point.device.asset;
    return `(${sqlLiteral(rowId(site.id, '6300', index + 1))}, ${tenant}, ${siteId}, ${sqlLiteral(point.id)}, ${asset ? "'ASSET'" : "'SITE'"}, NULL, ${asset ? sqlLiteral(asset) : 'NULL'}, 'DESCRIBES', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
  }).join(',\n');

  const registry = `BEGIN;
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id, floor_area_m2, climate_zone)
VALUES (${siteId}, ${sqlLiteral(site.code)}, ${sqlLiteral(site.name)}, ${sqlLiteral(site.timezone)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenant}, NULL, NULL)
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, timezone=EXCLUDED.timezone, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.spaces (id, tenant_id, site_id, parent_space_id, code, display_name, space_type, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(space.id)}, ${tenant}, ${siteId}, NULL, ${sqlLiteral(space.code)}, ${sqlLiteral(space.name)}, ${sqlLiteral(space.type)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, space_type=EXCLUDED.space_type, status='ACTIVE', updated_at=clock_timestamp();

${assets.length ? `INSERT INTO core_registry.assets (id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at, tenant_id) VALUES
${assetRows}
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, asset_type=EXCLUDED.asset_type, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.asset_space_bindings (id, tenant_id, site_id, asset_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${assetPlacementRows}
ON CONFLICT (id) DO UPDATE SET asset_id=EXCLUDED.asset_id, space_id=EXCLUDED.space_id, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();
` : ''}
INSERT INTO core_registry.devices (id, site_id, code, display_name, device_type, status, revision, created_at, updated_at, tenant_id, product_id, template_version_id) VALUES
${deviceRows}
ON CONFLICT (id) DO UPDATE SET code=EXCLUDED.code, display_name=EXCLUDED.display_name, device_type=EXCLUDED.device_type, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at) VALUES
${sourceKeyRows}
ON CONFLICT (id) DO UPDATE SET source_key=EXCLUDED.source_key, device_id=EXCLUDED.device_id, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.device_space_bindings (id, tenant_id, site_id, device_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${placementRows}
ON CONFLICT (id) DO UPDATE SET device_id=EXCLUDED.device_id, space_id=EXCLUDED.space_id, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();

${deviceBindingRows ? `INSERT INTO core_registry.device_bindings (id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, tenant_id) VALUES
${deviceBindingRows}
ON CONFLICT (id) DO UPDATE SET device_id=EXCLUDED.device_id, asset_id=EXCLUDED.asset_id, binding_role=EXCLUDED.binding_role, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();
` : ''}
INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at, point_template_id, template_version_id, counter_decrease_mode, counter_rollover_modulus) VALUES
${pointRows}
ON CONFLICT (id) DO UPDATE SET point_code=EXCLUDED.point_code, source_key=EXCLUDED.source_key, display_name=EXCLUDED.display_name, point_type=EXCLUDED.point_type, value_type=EXCLUDED.value_type, unit=EXCLUDED.unit, sample_interval_ms=EXCLUDED.sample_interval_ms, publish_interval_ms=EXCLUDED.publish_interval_ms, stale_after_ms=EXCLUDED.stale_after_ms, source_metadata=EXCLUDED.source_metadata, counter_decrease_mode=EXCLUDED.counter_decrease_mode, status='ACTIVE', updated_at=clock_timestamp();

INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${subjectRows}
ON CONFLICT (id) DO UPDATE SET point_id=EXCLUDED.point_id, subject_type=EXCLUDED.subject_type, space_id=EXCLUDED.space_id, asset_id=EXCLUDED.asset_id, status='ACTIVE', valid_to=NULL, updated_at=clock_timestamp();
COMMIT;`;

  // A Device is online while it reported within two of its sample periods. The Gateway's outbox
  // replays after an outage, so samples up to a day late are still accepted.
  const runtimeDevices = devices.map((device) => `(${sqlLiteral(device.id)}, ${tenant}, ${siteId}, 'APPLICABLE', clock_timestamp())`).join(',\n');
  const presence = devices.map((device) => `(${sqlLiteral(device.id)}, 1, ${device.sampleSeconds * 2 + 30}, ${device.sampleSeconds * 5}, true, ARRAY['SOURCE_ACTIVITY']::text[], 60, 86400, clock_timestamp())`).join(',\n');
  const freshness = points.map((point) => `(${sqlLiteral(point.device.id)}, ${sqlLiteral(point.code)}, 1, ${point.device.sampleSeconds * 3}, true, ${point.device.sampleSeconds}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, NULL, NULL, clock_timestamp())`).join(',\n');
  const coverage = devices.map((device) => `(${sqlLiteral(device.id)}, true, clock_timestamp(), NULL, 1, clock_timestamp())`).join(',\n');
  const telemetry = `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at) VALUES
${runtimeDevices}
ON CONFLICT (device_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id, site_id=EXCLUDED.site_id, presence_applicability='APPLICABLE', updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at) VALUES
${presence}
ON CONFLICT (device_id) DO UPDATE SET online_within_seconds=EXCLUDED.online_within_seconds, offline_after_seconds=EXCLUDED.offline_after_seconds, accepted_signal_types=EXCLUDED.accepted_signal_types, max_future_clock_skew_seconds=EXCLUDED.max_future_clock_skew_seconds, max_source_lag_seconds=EXCLUDED.max_source_lag_seconds, updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
${freshness}
ON CONFLICT (device_id, telemetry_key) DO UPDATE SET fresh_within_seconds=EXCLUDED.fresh_within_seconds, configured=true, expected_sample_interval_seconds=EXCLUDED.expected_sample_interval_seconds, value_type=EXCLUDED.value_type, expected_unit=EXCLUDED.expected_unit, updated_at=clock_timestamp();

INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at) VALUES
${coverage}
ON CONFLICT (device_id) DO UPDATE SET available=true, reason_code=NULL, updated_at=clock_timestamp();
RESET ROLE;
COMMIT;`;
  return { registry, telemetry, deviceCount: devices.length, pointCount: points.length };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [file, flag] = process.argv.slice(2);
  if (!file) throw new Error('usage: node scripts/site-onboarding.mjs <site file> [--print]');
  const profile = JSON.parse(readFileSync(file, 'utf8'));
  const { registry, telemetry, deviceCount, pointCount } = buildOnboardingSql(profile);
  if (flag === '--print') {
    process.stdout.write(`${registry}\n\n${telemetry}\n`);
  } else {
    const container = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
    for (const [database, sql] of [['hvac_s1', registry], ['hvac_s2', telemetry]]) {
      const result = spawnSync('docker', ['exec', '-i', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-q'], { input: sql, stdio: ['pipe', 'inherit', 'inherit'] });
      if (result.status !== 0) throw new Error(`${database} onboarding failed with ${result.status}`);
    }
    console.log(`${profile.site.name}: Gateway ${profile.gateway.name}, ${deviceCount} devices, ${pointCount} points registered`);
  }
}
