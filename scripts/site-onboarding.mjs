// A real Site's onboarding manifest (deploy/sites/*.site.json, ADR 0028) and the two things done
// with it. validate lists what the Site still lacks against the manifest, reading the database only
// with one read-only SELECT; apply writes what is missing. Both cover the Site, its plant room, the
// Gateway, every Device the Gateway names by source key, their Points, the Telemetry presence and
// freshness policies, the energy model and load forecast, the low-delta-T diagnosis schedule and
// the Optimization deployment and schedule. Alarm rules live in the Alarm database and keep their
// own publisher (alarm-rules); the manifest names their file and the file check verifies every
// device and point they read. Same tables as the simulator seed. Idempotent: a rerun with the same
// file changes no row; names edited later in the Registry are overwritten by the file. Machines the
// site file lists become Assets, with the meter wired to each bound to it.
//
//   node scripts/site-onboarding.mjs validate deploy/sites/songyaxuan.site.json [--offline]
//   node scripts/site-onboarding.mjs apply deploy/sites/songyaxuan.site.json
//   node scripts/site-onboarding.mjs print deploy/sites/songyaxuan.site.json
//
// PHASE1_POSTGRES_CONTAINER selects the database container (default: the local environment's).
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { localContainer } from './lib/local-environment.mjs';

const sqlLiteral = (value) => `'${String(value).replaceAll("'", "''")}'`;
const sqlJson = (value) => `${sqlLiteral(JSON.stringify(value))}::jsonb`;

const POINT_STATUSES = new Set(['ACTIVE', 'INACTIVE', 'RETIRED']);

// A point's registry status; the site file may leave it out (ACTIVE).
export function pointStatus(point) {
  const status = point.status ?? 'ACTIVE';
  if (!POINT_STATUSES.has(status)) throw new Error(`point ${point.code} has status ${status}; expected ACTIVE, INACTIVE or RETIRED`);
  return status;
}

// How a rerun moves an existing point_subject_bindings row to the point's status (EXCLUDED.status).
// Active: valid_to is cleared, and a binding that was closed starts a new validity period now.
// Inactive or retired: a current binding is closed now; one that was already closed keeps its end.
// A row first inserted inactive has valid_to NULL; its status alone keeps it out of current reads.
// valid_to stays after valid_from (CHECK valid_to > valid_from).
export const SUBJECT_BINDING_LIFECYCLE = [
  ['status', `EXCLUDED.status`],
  ['valid_from', `CASE WHEN EXCLUDED.status = 'ACTIVE' AND (point_subject_bindings.status <> 'ACTIVE' OR point_subject_bindings.valid_to IS NOT NULL) THEN clock_timestamp() ELSE point_subject_bindings.valid_from END`],
  ['valid_to', `CASE WHEN EXCLUDED.status = 'ACTIVE' THEN NULL WHEN point_subject_bindings.status = 'ACTIVE' AND point_subject_bindings.valid_to IS NULL THEN GREATEST(clock_timestamp(), point_subject_bindings.valid_from + interval '1 millisecond') ELSE point_subject_bindings.valid_to END`],
];

// The ON CONFLICT action of a rerun: each [column, value] pair is written only when some value
// differs from the row, so a rerun with an unchanged site file leaves the row, its updated_at and
// its revision as they are. A Registry row that does change gets revision + 1, as every Registry
// writer does: revision is the optimistic-lock version (UPDATE ... WHERE revision = $n) and the
// aggregate version of the change it records. A presence or freshness policy that changes gets
// policy_revision + 1 instead: Telemetry stamps each accepted sample and evaluated presence with
// the policy revision it used, so a new revision marks results made under the new values. The
// other Telemetry runtime rows have no revision (revision: false).
export function updateWhenChanged(table, assignments, { revision = 'revision' } = {}) {
  const set = [...assignments.map(([column, value]) => `${column}=${value}`), ...(revision ? [`${revision}=${table}.${revision} + 1`] : []), 'updated_at=clock_timestamp()'];
  return `DO UPDATE SET ${set.join(', ')}
WHERE (${assignments.map(([column]) => `${table}.${column}`).join(', ')}) IS DISTINCT FROM (${assignments.map(([, value]) => value).join(', ')})`;
}

const fromFile = (...columns) => columns.map((column) => [column, `EXCLUDED.${column}`]);
const ACTIVE = [['status', `'ACTIVE'`]];
const CURRENT = [...ACTIVE, ['valid_to', 'NULL']];

// Row ids derive from the Site id so a second run updates the same rows.
function rowId(siteId, kind, index) {
  return siteId.replace(/^([0-9a-f]{8})-[0-9a-f]{4}-7000-8000-[0-9a-f]{12}$/, (_, head) => `${head}-${kind}-7000-8000-${index.toString(16).padStart(12, '0')}`);
}

// A device's points: its own list or the shared meter list, then any it adds (morePoints).
export function expandPoints(device, meterPoints) {
  return [...(device.points === 'meter' ? meterPoints : device.points), ...(device.morePoints ?? [])].map((point) => ({ ...point }));
}

// The site file's devices with their points, each point with the row id it is registered under.
function siteRows(profile) {
  const devices = profile.devices.map((device, deviceIndex) => ({ ...device, deviceIndex: deviceIndex + 1, points: expandPoints(device, profile.meterPoints) }));
  const points = devices.flatMap((device) => device.points.map((point, pointIndex) => ({
    ...point, device, id: rowId(profile.site.id, '5000', device.deviceIndex * 0x10000 + pointIndex + 1),
  })));
  return { devices, points };
}

export function buildOnboardingSql(profile) {
  const tenant = sqlLiteral(profile.tenantId);
  const { site, space, gateway } = profile;
  const siteId = sqlLiteral(site.id);
  const { devices, points } = siteRows(profile);
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
    return `(${sqlLiteral(point.id)}, ${tenant}, ${siteId}, ${sqlLiteral(point.device.id)}, NULL, ${sqlLiteral(point.code)}, ${sqlLiteral(point.sourceKey ?? point.code)}, ${sqlLiteral(point.name)}, ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, false, ${interval}, ${interval}, ${interval * 3}, ${sqlJson({ protocol: 'MODBUS_RTU', address: point.address, gatewaySourceKey: point.device.sourceKey })}, ${sqlLiteral(pointStatus(point))}, 1, clock_timestamp(), clock_timestamp(), NULL, NULL, ${point.pointType === 'COUNTER' ? "'RESET_TO_ZERO'" : 'NULL'}, NULL)`;
  }).join(',\n');
  // A point describes the machine it measures, else the Site. The binding carries the point's
  // status, so a point the site file marks INACTIVE (no probe wired) is not a current binding.
  const subjectRows = points.map((point, index) => {
    const asset = point.asset ?? point.device.asset;
    return `(${sqlLiteral(rowId(site.id, '6300', index + 1))}, ${tenant}, ${siteId}, ${sqlLiteral(point.id)}, ${asset ? "'ASSET'" : "'SITE'"}, NULL, ${asset ? sqlLiteral(asset) : 'NULL'}, 'DESCRIBES', ${sqlLiteral(pointStatus(point))}, clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`;
  }).join(',\n');

  const registry = `BEGIN;
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id, floor_area_m2, climate_zone)
VALUES (${siteId}, ${sqlLiteral(site.code)}, ${sqlLiteral(site.name)}, ${sqlLiteral(site.timezone)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${tenant}, NULL, NULL)
ON CONFLICT (id) ${updateWhenChanged('sites', [...fromFile('code', 'display_name', 'timezone'), ...ACTIVE])};

INSERT INTO core_registry.spaces (id, tenant_id, site_id, parent_space_id, code, display_name, space_type, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(space.id)}, ${tenant}, ${siteId}, NULL, ${sqlLiteral(space.code)}, ${sqlLiteral(space.name)}, ${sqlLiteral(space.type)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) ${updateWhenChanged('spaces', [...fromFile('code', 'display_name', 'space_type'), ...ACTIVE])};

${assets.length ? `INSERT INTO core_registry.assets (id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at, tenant_id) VALUES
${assetRows}
ON CONFLICT (id) ${updateWhenChanged('assets', [...fromFile('code', 'display_name', 'asset_type'), ...ACTIVE])};

INSERT INTO core_registry.asset_space_bindings (id, tenant_id, site_id, asset_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${assetPlacementRows}
ON CONFLICT (id) ${updateWhenChanged('asset_space_bindings', [...fromFile('asset_id', 'space_id'), ...CURRENT])};
` : ''}
INSERT INTO core_registry.devices (id, site_id, code, display_name, device_type, status, revision, created_at, updated_at, tenant_id, product_id, template_version_id) VALUES
${deviceRows}
ON CONFLICT (id) ${updateWhenChanged('devices', [...fromFile('code', 'display_name', 'device_type'), ...ACTIVE])};

INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at) VALUES
${sourceKeyRows}
ON CONFLICT (id) ${updateWhenChanged('gateway_device_source_keys', [...fromFile('source_key', 'device_id'), ...ACTIVE])};

INSERT INTO core_registry.device_space_bindings (id, tenant_id, site_id, device_id, space_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${placementRows}
ON CONFLICT (id) ${updateWhenChanged('device_space_bindings', [...fromFile('device_id', 'space_id'), ...CURRENT])};

${deviceBindingRows ? `INSERT INTO core_registry.device_bindings (id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, tenant_id) VALUES
${deviceBindingRows}
ON CONFLICT (id) ${updateWhenChanged('device_bindings', [...fromFile('device_id', 'asset_id', 'binding_role'), ...CURRENT])};
` : ''}
INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at, point_template_id, template_version_id, counter_decrease_mode, counter_rollover_modulus) VALUES
${pointRows}
ON CONFLICT (id) ${updateWhenChanged('telemetry_points', fromFile('point_code', 'source_key', 'display_name', 'point_type', 'value_type', 'unit', 'sample_interval_ms', 'publish_interval_ms', 'stale_after_ms', 'source_metadata', 'counter_decrease_mode', 'status'))};

INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${subjectRows}
ON CONFLICT (id) ${updateWhenChanged('point_subject_bindings', [...fromFile('point_id', 'subject_type', 'space_id', 'asset_id'), ...SUBJECT_BINDING_LIFECYCLE])};
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
ON CONFLICT (device_id) ${updateWhenChanged('devices', [...fromFile('tenant_id', 'site_id'), ['presence_applicability', `'APPLICABLE'`]], { revision: false })};

INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at) VALUES
${presence}
ON CONFLICT (device_id) ${updateWhenChanged('presence_policies', fromFile('online_within_seconds', 'offline_after_seconds', 'accepted_signal_types', 'max_future_clock_skew_seconds', 'max_source_lag_seconds'), { revision: 'policy_revision' })};

INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
${freshness}
ON CONFLICT (device_id, telemetry_key) ${updateWhenChanged('freshness_policies', [...fromFile('fresh_within_seconds'), ['configured', 'true'], ...fromFile('expected_sample_interval_seconds', 'value_type', 'expected_unit')], { revision: 'policy_revision' })};

INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at) VALUES
${coverage}
ON CONFLICT (device_id) ${updateWhenChanged('observation_coverage', [['available', 'true'], ['reason_code', 'NULL']], { revision: false })};
RESET ROLE;
COMMIT;`;
  return { registry, telemetry, energy: buildEnergySql(profile, devices, points), intelligence: buildIntelligenceSql(profile), deviceCount: devices.length, pointCount: points.length };
}

const ELECTRICITY = '01990000-0000-7000-8000-000000000001';
const COOLING = '01990000-0000-7000-8000-000000000006';

// The Site's energy model, when its site file has one: the total meter on the grid feed into the
// plant, each machine's meter on the plant's feed into that machine (a sub-meter inside the total),
// a cooling meter (when the site has one) on the plant's chilled-water supply to the building, the
// Tenant's released energy Metrics bound to those counters and calculated on schedule, the
// import Tariff on the grid boundary, and the monthly report. A released topology is immutable, so
// a rerun leaves it as it is.
function buildEnergySql(profile, devices, points) {
  const energy = profile.energy;
  if (!energy) return '';
  const { site } = profile;
  const t = sqlLiteral(profile.tenantId);
  const st = sqlLiteral(site.id);
  const id = (kind, index) => sqlLiteral(rowId(site.id, kind, index));
  const deviceByKey = new Map(devices.map((device) => [device.sourceKey, device]));
  const counter = (device) => {
    const point = points.find((candidate) => candidate.device === device && candidate.pointType === 'COUNTER');
    if (!point) throw new Error(`${device.sourceKey} has no energy counter`);
    return point.id;
  };
  const assetName = new Map((profile.assets ?? []).map((asset) => [asset.id, asset.name]));
  const total = deviceByKey.get(energy.totalMeter);
  const branches = energy.branchMeters.map((key) => deviceByKey.get(key));
  const cooling = energy.coolingMeter ? deviceByKey.get(energy.coolingMeter) : null;
  const version = id('7000', 1);
  const nodes = [
    { key: 'grid', type: 'GRID', name: '电网' },
    { key: 'plant', type: 'HVAC', name: energy.plantName },
    ...branches.map((device) => ({ key: device.sourceKey, type: 'LOAD', name: assetName.get(device.asset) ?? device.name, deviceId: device.id })),
    ...(cooling ? [{ key: 'building', type: 'BUILDING', name: '建筑冷负荷' }] : []),
  ].map((node, index) => ({ ...node, id: id('7100', index + 1) }));
  const nodeId = new Map(nodes.map((node) => [node.key, node.id]));
  const meters = [
    { device: total, from: 'grid', to: 'plant', direction: 'IMPORT', energyType: ELECTRICITY },
    ...branches.map((device) => ({ device, from: 'plant', to: device.sourceKey, direction: 'CONSUME', energyType: ELECTRICITY })),
    ...(cooling ? [{ device: cooling, from: 'plant', to: 'building', direction: 'CONSUME', energyType: COOLING }] : []),
  ].map((meter, index) => ({ ...meter, edge: id('7200', index + 1), meter: id('7300', index + 1), binding: id('7400', index + 1), point: sqlLiteral(counter(meter.device)) }));
  const draft = `WHERE EXISTS (SELECT 1 FROM core_registry.energy_topology_versions WHERE id = ${version} AND status = 'DRAFT')`;

  // Metric definitions are the Tenant's; the Site binds their released version by Metric code.
  const metricBinding = (binding) => `INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at)
SELECT ${id('7500', binding.index)}, ${t}, ${st}, v.id, v.metric_id, v.version, 1, '${binding.subject}', ${sqlLiteral(binding.subjectId)}, '${binding.granularity}', ${sqlJson(binding.source)}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp()
FROM core_registry.metric_versions v JOIN core_registry.metrics m ON m.id = v.metric_id
WHERE m.tenant_id = ${t} AND m.metric_code = '${binding.code}' AND v.status = 'RELEASED'
  AND NOT EXISTS (SELECT 1 FROM core_registry.metric_bindings WHERE id = ${id('7500', binding.index)});`;
  const totalEnergy = { points: { site_energy: counter(total) }, counter: { decreaseMode: 'RESET' } };
  const bindings = [
    { code: 'hourly_energy', subject: 'SITE', subjectId: site.id, granularity: 'HOUR', source: totalEnergy, cron: '5 * * * *', name: '空调逐时用电量（每小时）' },
    { code: 'daily_energy', subject: 'SITE', subjectId: site.id, granularity: 'DAY', source: totalEnergy, cron: '10 0 * * *', name: '空调日用电量（每日零点后）' },
    ...branches.map((device) => ({
      code: 'device_hourly_energy', subject: 'DEVICE', subjectId: device.id, granularity: 'HOUR', cron: '5 * * * *',
      source: { points: { device_energy: counter(device) }, counter: { decreaseMode: 'RESET' } },
      name: `${assetName.get(device.asset) ?? device.name}逐时用电量（每小时）`,
    })),
    // Cooling is bound after the electricity Metrics so their binding ids stay where they were.
    ...(cooling ? [{
      code: 'hourly_cooling', subject: 'SITE', subjectId: site.id, granularity: 'HOUR', cron: '5 * * * *', name: '逐时供冷量（每小时）',
      source: { points: { site_cooling: counter(cooling) }, counter: { decreaseMode: 'RESET' } },
    }] : []),
  // Slot 3 was the retired energy_cost binding (#575): the bindings after it keep their ids.
  ].map((binding, index) => ({ ...binding, index: index < 2 ? index + 1 : index + 2 }));
  const nextFire = (cron) => cron.startsWith('5 ') ? 'clock_timestamp()'
    : `(date_trunc('day', clock_timestamp() AT TIME ZONE '${site.timezone}') + interval '1 day' + interval '${Number(cron.split(' ')[0])} minutes') AT TIME ZONE '${site.timezone}'`;
  const schedules = bindings.map((binding) => `  (${id('7600', binding.index)}, ${sqlLiteral(`${binding.code.replaceAll('_', '-')}-${site.code}${binding.subject === 'DEVICE' ? `-${binding.index}` : ''}`)}, ${sqlLiteral(binding.name)}, ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', ${sqlLiteral(binding.cron)}, '5-field-v1', ${sqlLiteral(site.timezone)}, 'FIRE_ONCE', 60, ${sqlJson({ bindingId: rowId(site.id, '7500', binding.index) })}, ${nextFire(binding.cron)})`);
  const tariff = energy.tariff;
  const periods = ['WEEKDAY', 'WEEKEND'].flatMap((dayType) => tariff.slices.map(([code, start, end], ordinal) => ({ dayType, code, start, end, ordinal })))
    .map((period, index) => `  (${id('7900', index + 1)}, ${t}, ${st}, ${id('7800', 2)}, ${sqlLiteral(period.code)}, '${period.dayType}', ${period.start}, ${period.end}, ${sqlJson(tariff.rates[period.code])}, ${period.ordinal}, 1, clock_timestamp(), clock_timestamp())`);

  return `BEGIN;
INSERT INTO core_registry.energy_topology_versions (id, tenant_id, site_id, version, status, revision, created_at, updated_at)
VALUES (${version}, ${t}, ${st}, 1, 'DRAFT', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_nodes (id, tenant_id, site_id, topology_version_id, node_type, name, device_id, status, revision, created_at, updated_at)
SELECT node.* FROM (VALUES
${nodes.map((node) => `  (${node.id}::uuid, ${t}::uuid, ${st}::uuid, ${version}::uuid, ${sqlLiteral(node.type)}, ${sqlLiteral(node.name)}, ${node.deviceId ? `${sqlLiteral(node.deviceId)}::uuid` : 'NULL::uuid'}, 'ACTIVE', 1::bigint, clock_timestamp(), clock_timestamp())`).join(',\n')}
) AS node ${draft}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_edges (id, tenant_id, site_id, topology_version_id, from_node_id, to_node_id, energy_type_id, direction, enabled, revision, created_at, updated_at)
SELECT edge.* FROM (VALUES
${meters.map((meter) => `  (${meter.edge}::uuid, ${t}::uuid, ${st}::uuid, ${version}::uuid, ${nodeId.get(meter.from)}::uuid, ${nodeId.get(meter.to)}::uuid, '${meter.energyType}'::uuid, '${meter.direction}', true, 1::bigint, clock_timestamp(), clock_timestamp())`).join(',\n')}
) AS edge ${draft}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_meters (id, tenant_id, site_id, meter_code, display_name, device_id, energy_type_id, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${meter.meter}, ${t}, ${st}, ${sqlLiteral(meter.device.code.replaceAll('-', '_'))}, ${sqlLiteral(meter.device.name)}, ${sqlLiteral(meter.device.id)}, '${meter.energyType}', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.meter_bindings (id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id, meter_id, device_id, point_id, point_type, meter_role, direction, priority, effective_from, version, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${meter.binding}, ${t}, ${st}, ${version}, ${meter.edge}, '${meter.energyType}', ${meter.meter}, ${sqlLiteral(meter.device.id)}, ${meter.point}, 'COUNTER', 'PRIMARY', '${meter.direction}', 0, '2026-01-01T00:00:00Z', 1, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.energy_topology_versions
SET status = 'ACTIVE', effective_from = '2026-01-01T00:00:00Z', released_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${version} AND status = 'DRAFT';

${bindings.map(metricBinding).join('\n')}
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at) VALUES
${schedules.join(',\n')},
  (${id('7600', 90)}, ${sqlLiteral(`monthly-report-${site.code}`)}, '月度报告快照（每月 1 日 03:00）', ${t}, ${st}, 'REPORT_GENERATE', 'CRON', '0 3 1 * *', '5-field-v1', ${sqlLiteral(site.timezone)}, 'FIRE_ONCE', 120, '{}',
    (date_trunc('month', clock_timestamp() AT TIME ZONE '${site.timezone}') + interval '1 month' + interval '3 hours') AT TIME ZONE '${site.timezone}')
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;

INSERT INTO core_registry.settlement_boundaries (id, tenant_id, site_id, topology_version_id, boundary_code, display_name, boundary_type, energy_type_id, direction, definition_mode, node_id, effective_from, status, revision, created_at, updated_at)
VALUES (${id('7800', 1)}, ${t}, ${st}, ${version}, 'grid_import', '电网进线', 'GRID_CONNECTION', '${ELECTRICITY}', 'IMPORT', 'NODE', ${nodeId.get('grid')}, '2026-01-01T00:00:00Z', 'DRAFT', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.settlement_boundaries SET status = 'ACTIVE', revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${id('7800', 1)} AND status = 'DRAFT';
INSERT INTO core_registry.tariffs (id, tenant_id, site_id, tariff_code, display_name, energy_type_id, status, revision, created_at, updated_at)
VALUES (${id('7800', 3)}, ${t}, ${st}, ${sqlLiteral(tariff.code)}, ${sqlLiteral(tariff.name)}, '${ELECTRICITY}', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) ${updateWhenChanged('tariffs', fromFile('display_name'))};
INSERT INTO core_registry.tariff_versions (id, tenant_id, site_id, tariff_id, version, effective_from, timezone, currency, billing_cycle, status, revision, created_at, updated_at)
VALUES (${id('7800', 2)}, ${t}, ${st}, ${id('7800', 3)}, 1, '2026-01-01T00:00:00Z', ${sqlLiteral(tariff.timezone)}, 'CNY', 'CALENDAR_MONTH', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_periods (id, tenant_id, site_id, tariff_version_id, period_code, day_type, local_start_minute, local_end_minute, pricing_rule, ordinal, revision, created_at, updated_at) VALUES
${periods.join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_assignments (id, tenant_id, site_id, boundary_id, tariff_id, effective_from, status, revision, created_at, updated_at)
VALUES (${id('7800', 4)}, ${t}, ${st}, ${id('7800', 1)}, ${id('7800', 3)}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
${buildLoadForecastSql(profile, total, points, version)}
COMMIT;`;
}

// The Site load forecast, the same chain the simulator seed gives the demo Sites: the total meter's
// active power averaged per quarter-hour as a Site Metric of its own, and a day-ahead baseline model
// (each time of day averaged over the past week) deployed on it and run every hour. The Forecast
// owner needs at least four quarter-hours of that Metric, so after onboarding a Site with history,
// backfill a week of it with scripts/site-metric-recalc.mjs.
function buildLoadForecastSql(profile, total, points, topologyVersion) {
  const { site } = profile;
  const t = sqlLiteral(profile.tenantId);
  const st = sqlLiteral(site.id);
  const rawId = (index) => rowId(site.id, '7a00', index);
  const power = points.filter((point) => point.device === total && point.pointType === 'TELEMETRY' && point.unit === 'kW');
  if (power.length !== 1) throw new Error(`${total.sourceKey} needs exactly one kW telemetry point for the load forecast`);
  const code = site.code.replaceAll('-', '_');
  const [metric, metricVersion, dependency, binding, loadSchedule, featureSet, featureSetVersion, dataset, model, trainingRun, modelVersion, deployment, forecastSchedule] =
    Array.from({ length: 13 }, (_, index) => sqlLiteral(rawId(index + 1)));
  return `INSERT INTO core_registry.metrics (id, tenant_id, metric_code, metric_name, category, status, revision, created_at, updated_at)
VALUES (${metric}, ${t}, ${sqlLiteral(`site_load_15min_${code}`)}, ${sqlLiteral(`${site.name}空调用电负荷（15 分钟）`)}, 'LOAD', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_versions (id, tenant_id, metric_id, version, unit_code, data_type, subject_type, time_granularity, aggregation, calculation_method, formula, quality_policy, effective_from, status, metadata, revision, created_at, updated_at)
VALUES (${metricVersion}, ${t}, ${metric}, 1, 'kW', 'NUMBER', 'SITE', '15MIN', 'AVG', 'IDENTITY', NULL, 'STRICT', '2026-01-01T00:00:00Z', 'DRAFT', '{}', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.metric_dependencies (id, tenant_id, metric_version_id, dependency_type, dependency_code, dependency_metric_id, sort_order, required, metadata, revision, created_at, updated_at)
SELECT ${dependency}::uuid, ${t}::uuid, ${metricVersion}::uuid, 'POINT', 'site_load', NULL::uuid, 0, true, '{}'::jsonb, 1::bigint, clock_timestamp(), clock_timestamp()
WHERE EXISTS (SELECT 1 FROM core_registry.metric_versions WHERE id = ${metricVersion} AND status = 'DRAFT')
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.metric_versions SET status = 'RELEASED', revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${metricVersion} AND status = 'DRAFT';
INSERT INTO core_registry.metric_bindings (id, tenant_id, site_id, metric_version_id, metric_id, metric_version, binding_version, subject_type, subject_id, time_granularity, source_definition, effective_from, status, revision, created_at, updated_at)
VALUES (${binding}, ${t}, ${st}, ${metricVersion}, ${metric}, 1, 1, 'SITE', ${st}, '15MIN', ${sqlJson({ points: { site_load: power[0].id } })}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_feature_sets (id, tenant_id, feature_set_code, target, status, revision, created_at, updated_at)
VALUES (${featureSet}, ${t}, ${sqlLiteral(`site_load_${code}`)}, 'SITE_LOAD', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_feature_set_versions (id, tenant_id, feature_set_id, version, feature_schema, fallback_schema, status, revision, created_at, updated_at)
VALUES (${featureSetVersion}, ${t}, ${featureSet}, 1, ${sqlJson({ targetMetricVersionId: rawId(2), features: ['load_history'] })}, NULL, 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_dataset_snapshots (id, tenant_id, site_id, target, subject_type, subject_id, train_from, train_to, feature_set_version_id, topology_version_id, metric_version_refs, weather_source, data_quality_summary, manifest_uri, manifest_checksum, created_at)
VALUES (${dataset}, ${t}, ${st}, 'SITE_LOAD', 'SITE', ${st}, '2026-01-01T00:00:00Z', '2026-10-01T00:00:00Z', ${featureSetVersion}, ${topologyVersion}, ${sqlJson([rawId(2)])}, NULL, '{}', ${sqlLiteral(`site://${site.code}/site-load/manifest.json`)}, repeat('0', 64), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_models (id, tenant_id, model_code, target, subject_type, horizon_minutes, granularity, status, revision, created_at, updated_at)
VALUES (${model}, ${t}, ${sqlLiteral(`site_load_day_ahead_${code}`)}, 'SITE_LOAD', 'SITE', 2880, '15MIN', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.forecast_training_runs (id, tenant_id, site_id, model_id, dataset_snapshot_id, feature_set_version_id, topology_version_id, algorithm, hyperparameters, code_version, evaluation, status, started_at, finished_at, revision, created_at, updated_at)
VALUES (${trainingRun}, ${t}, ${st}, ${model}, ${dataset}, ${featureSetVersion}, ${topologyVersion}, 'BASELINE', '{"method":"TIME_OF_DAY_PROFILE"}', 'site-onboarding', NULL, 'PENDING', NULL, NULL, 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.forecast_training_runs SET status = 'RUNNING', started_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp()
WHERE id = ${trainingRun} AND status = 'PENDING';
UPDATE core_registry.forecast_training_runs SET status = 'SUCCEEDED', evaluation = '{"baseline":true}', finished_at = clock_timestamp(), revision = revision + 1, updated_at = clock_timestamp() + interval '1 second'
WHERE id = ${trainingRun} AND status = 'RUNNING';
INSERT INTO core_registry.forecast_model_versions (id, tenant_id, site_id, model_id, model_version, training_run_id, dataset_snapshot_id, feature_set_version_id, topology_version_id, artifact_uri, artifact_checksum, evaluation, compatibility, status, revision, created_at, updated_at)
VALUES (${modelVersion}, ${t}, ${st}, ${model}, 1, ${trainingRun}, ${dataset}, ${featureSetVersion}, ${topologyVersion}, ${sqlLiteral(`site://${site.code}/site-load/model.bin`)}, repeat('0', 64), '{"baseline":true}', '{"runtime":"go"}', 'CANDIDATE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
UPDATE core_registry.forecast_model_versions SET status = 'VALIDATED', revision = revision + 1, updated_at = clock_timestamp() + interval '1 second'
WHERE id = ${modelVersion} AND status = 'CANDIDATE';
INSERT INTO core_registry.forecast_deployments (id, tenant_id, site_id, target, subject_type, subject_id, model_version_id, model_id, feature_set_version_id, topology_version_id, status, effective_from, revision, created_at, updated_at)
VALUES (${deployment}, ${t}, ${st}, 'SITE_LOAD', 'SITE', ${st}, ${modelVersion}, ${model}, ${featureSetVersion}, ${topologyVersion}, 'ACTIVE', '2026-01-01T00:00:00Z', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, next_fire_at) VALUES
  (${loadSchedule}, ${sqlLiteral(`site-load-15min-${site.code}`)}, '空调用电负荷（每 15 分钟）', ${t}, ${st}, 'METRIC_WINDOW_CALC', 'CRON', '*/15 * * * *', '5-field-v1', ${sqlLiteral(site.timezone)}, 'FIRE_ONCE', 60, ${sqlJson({ bindingId: rawId(4) })},
    date_trunc('hour', clock_timestamp()) + ceil(extract(minute FROM clock_timestamp()) / 15 + 0.0001) * interval '15 minutes'),
  (${forecastSchedule}, ${sqlLiteral(`site-load-forecast-${site.code}`)}, '空调用电负荷日前预测（每小时）', ${t}, ${st}, 'FORECAST_RUN', 'CRON', '7 * * * *', '5-field-v1', ${sqlLiteral(site.timezone)}, 'FIRE_ONCE', 120,
    ${sqlJson({ target: 'SITE_LOAD', subjectType: 'SITE', subjectId: site.id })}, clock_timestamp())
ON CONFLICT (tenant_id, schedule_code) DO NOTHING;`;
}

// The Site's diagnosis and Optimization, when its manifest has them (ADR 0028). The low-delta-T
// rule and the Optimization policy belong to the Tenant and every Site of it reads them: the first
// Site of a Tenant creates them from its manifest, a later one only checks them. The Site's own
// rows (the diagnosis schedule, the Optimization deployment binding and schedule) follow the file;
// the Optimization binding and schedule are written only while the Tenant's policy in effect is the
// one the manifest describes, since preparation reads that policy's readings and limits for every
// Site of the Tenant.
const LOW_DELTA_T = 'CHILLED_WATER_LOW_DELTA_T';

function deviceByCode(profile, code) {
  const device = profile.devices.find((candidate) => candidate.code === code);
  if (!device) throw new Error(`no device ${code} in the site file`);
  return device;
}

function assetByCode(profile, code) {
  const asset = (profile.assets ?? []).find((candidate) => candidate.code === code);
  if (!asset) throw new Error(`no asset ${code} in the site file`);
  return asset;
}

// The policy in effect for the Tenant now, the one Optimization preparation reads.
function effectivePolicySql(tenant) {
  return `SELECT pv.constraints, pv.objective, pv.dispatch_mode, pv.horizon, pv.horizon_minutes, pv.granularity
FROM core_registry.optimization_policy_versions pv
JOIN core_registry.optimization_policies p ON p.tenant_id = pv.tenant_id AND p.id = pv.policy_id
WHERE pv.tenant_id = ${tenant} AND p.status = 'ACTIVE' AND p.resource_type = 'HVAC' AND pv.status = 'RELEASED'
  AND pv.effective_from <= clock_timestamp() AND (pv.effective_to IS NULL OR pv.effective_to > clock_timestamp())
ORDER BY pv.effective_from DESC, pv.version DESC
LIMIT 1`;
}

// A schedule the manifest owns: a rerun brings its name, cron, payload and enabled back to the file.
function scheduleUpsert({ id, code, name, tenant, site, jobType, cron, timeout, payload, enabled, nextFire, guard }) {
  return `INSERT INTO core_registry.schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, enabled, next_fire_at)
SELECT ${id}::uuid, ${sqlLiteral(code)}, ${sqlLiteral(name)}, ${tenant}::uuid, ${site}::uuid, '${jobType}', 'CRON', ${sqlLiteral(cron)}, '5-field-v1', 'UTC', 'FIRE_ONCE', ${timeout}, ${sqlJson(payload)}, ${enabled ? 'true' : 'false'}, ${nextFire}${guard ? `\nWHERE ${guard}` : ''}
ON CONFLICT (tenant_id, schedule_code) ${updateWhenChanged('schedule_definitions', fromFile('schedule_name', 'cron_expression', 'payload_template', 'enabled'), { revision: 'version' })};`;
}

export function buildIntelligenceSql(profile) {
  const intelligence = profile.intelligence;
  if (!intelligence) return '';
  const { site } = profile;
  const t = sqlLiteral(profile.tenantId);
  const st = sqlLiteral(site.id);
  const id = (kind, index) => sqlLiteral(rowId(site.id, kind, index));
  const parts = [];

  const lowDeltaT = intelligence.diagnosis?.lowDeltaT;
  if (lowDeltaT) {
    const device = deviceByCode(profile, lowDeltaT.device);
    const asset = assetByCode(profile, lowDeltaT.asset);
    const { minimumDeltaTC, windowMinutes } = lowDeltaT.firstRevision;
    parts.push(`-- The Tenant's first low-delta-T rule revision, only when it has none; later ones come from scripts/publish-fdd-rule-revision.mjs.
INSERT INTO core_registry.fdd_rule_revisions (id, tenant_id, rule_code, revision, minimum_delta_tc, window_minutes, effective_from, created_at)
SELECT ${id('7c00', 1)}::uuid, ${t}::uuid, '${LOW_DELTA_T}', 1, ${Number(minimumDeltaTC)}, ${Number(windowMinutes)}, '2026-01-01T00:00:00Z'::timestamptz, clock_timestamp()
WHERE NOT EXISTS (SELECT 1 FROM core_registry.fdd_rule_revisions WHERE tenant_id = ${t} AND rule_code = '${LOW_DELTA_T}');
${scheduleUpsert({
    id: id('7c00', 2), code: `chilled-water-low-delta-t-${site.code}`, name: '冷冻水低温差诊断（每 15 分钟）', tenant: t, site: st,
    jobType: 'FDD_EVALUATION', cron: lowDeltaT.cron, timeout: 60, payload: { assetId: asset.id, deviceId: device.id }, enabled: lowDeltaT.enabled,
    nextFire: `date_trunc('hour', clock_timestamp()) + ceil(extract(minute FROM clock_timestamp()) / 15 + 0.0001) * interval '15 minutes'`,
  })}`);
  }

  const optimization = intelligence.optimization;
  if (optimization) {
    const { policy } = optimization;
    const agrees = `EXISTS (SELECT 1 FROM (${effectivePolicySql(t)}) AS current
  WHERE (current.constraints, current.objective, current.dispatch_mode, current.horizon, current.horizon_minutes, current.granularity)
    = (${sqlJson(policy.constraints)}, ${sqlLiteral(policy.objective)}, ${sqlLiteral(policy.dispatchMode)}, ${sqlLiteral(policy.horizon)}, ${Number(policy.horizonMinutes)}, ${sqlLiteral(policy.granularity)}))`;
    const binding = `EXISTS (SELECT 1 FROM core_registry.ai_deployment_bindings WHERE tenant_id = ${t} AND site_id = ${st} AND use_case = 'OPTIMIZATION' AND status = 'ACTIVE')`;
    parts.push(`-- The Tenant's Optimization policy, only when it has none (a later Site checks it, see validate).
INSERT INTO core_registry.optimization_policies (id, tenant_id, policy_code, subject_type, resource_type, status, revision, created_at, updated_at)
SELECT ${id('7b00', 1)}::uuid, ${t}::uuid, ${sqlLiteral(policy.code)}, 'SITE', 'HVAC', 'ACTIVE', 1, clock_timestamp(), clock_timestamp()
WHERE NOT EXISTS (SELECT 1 FROM core_registry.optimization_policies WHERE tenant_id = ${t} AND resource_type = 'HVAC' AND status = 'ACTIVE');
INSERT INTO core_registry.optimization_policy_versions (id, tenant_id, policy_id, version, objective, weights, constraints, dispatch_mode, fallback_policy, risk_level, horizon, horizon_minutes, granularity, effective_from, status, revision, created_at, updated_at)
SELECT ${id('7b00', 2)}::uuid, ${t}::uuid, ${id('7b00', 1)}::uuid, 1, ${sqlLiteral(policy.objective)}, '{"cost":1}'::jsonb, ${sqlJson(policy.constraints)}, ${sqlLiteral(policy.dispatchMode)}, 'RULE_STRATEGY', 'LOW', ${sqlLiteral(policy.horizon)}, ${Number(policy.horizonMinutes)}, ${sqlLiteral(policy.granularity)}, '2026-01-01T00:00:00Z'::timestamptz, 'RELEASED', 1::bigint, clock_timestamp(), clock_timestamp()
WHERE EXISTS (SELECT 1 FROM core_registry.optimization_policies WHERE id = ${id('7b00', 1)})
  AND NOT EXISTS (SELECT 1 FROM core_registry.optimization_policy_versions WHERE id = ${id('7b00', 2)});
-- The Site runs the Tenant's enabled deployment of the manifest's model, while the policy agrees.
INSERT INTO core_registry.ai_deployment_bindings (id, tenant_id, site_id, use_case, deployment_revision_id, status, revision, created_at, updated_at)
SELECT ${id('7b00', 3)}::uuid, ${t}::uuid, ${st}::uuid, 'OPTIMIZATION', revision.id, 'ACTIVE', 1, clock_timestamp(), clock_timestamp()
FROM core_registry.ai_deployment_revisions revision
JOIN core_registry.ai_model_definitions model ON model.tenant_id = revision.tenant_id AND model.id = revision.model_definition_id
WHERE revision.tenant_id = ${t} AND revision.use_case = 'OPTIMIZATION' AND revision.enabled AND model.status = 'ACTIVE' AND model.model_id = ${sqlLiteral(optimization.model)}
  AND ${agrees}
ORDER BY revision.revision DESC
LIMIT 1
ON CONFLICT DO NOTHING;
${scheduleUpsert({
    id: id('7b00', 4), code: `supply-temperature-optimization-${site.code}`, name: '冷冻水供水温度优化建议（每小时）', tenant: t, site: st,
    jobType: 'OPTIMIZATION_RUN', cron: optimization.cron, timeout: 120, payload: {}, enabled: optimization.enabled, nextFire: 'clock_timestamp()',
    guard: `${agrees}\n  AND ${binding}`,
  })}`);
  }
  return parts.length ? `BEGIN;\n${parts.join('\n\n')}\nCOMMIT;` : '';
}

// What a site file must say consistently before anything is written: every device, asset and
// point it refers to is in it. Returns the problems found, empty when there are none.
export function checkSiteFile(profile, alarmRules = null) {
  const problems = [];
  const { devices } = siteRows(profile);
  const activePoints = new Map(devices.map((device) => [device.code, new Set(device.points.filter((point) => pointStatus(point) === 'ACTIVE').map((point) => point.code))]));
  const siteActive = (code) => devices.filter((device) => activePoints.get(device.code).has(code)).length;
  const intelligence = profile.intelligence ?? {};
  const cron = (value, where) => {
    if (typeof value !== 'string' || value.trim().split(/\s+/).length !== 5) problems.push(`${where}: cron ${value} is not five fields`);
  };

  if (intelligence.alarmRules && !alarmRules) problems.push(`alarm rules file ${intelligence.alarmRules} was not found`);
  if (alarmRules) {
    const inputs = [];
    const walk = (node) => {
      if (!node || typeof node !== 'object') return;
      if (typeof node.input === 'string') inputs.push(node.input);
      for (const child of node.children ?? []) walk(child);
    };
    for (const rule of alarmRules.rules ?? []) { walk(rule.raise); walk(rule.clear); }
    for (const input of new Set(inputs)) {
      const [deviceCode, pointCode] = input.split('/');
      if (!activePoints.has(deviceCode)) problems.push(`alarm rule input ${input}: no device ${deviceCode}`);
      else if (!activePoints.get(deviceCode).has(pointCode)) problems.push(`alarm rule input ${input}: ${deviceCode} has no active point ${pointCode}`);
    }
  }

  const lowDeltaT = intelligence.diagnosis?.lowDeltaT;
  if (lowDeltaT) {
    cron(lowDeltaT.cron, 'diagnosis.lowDeltaT');
    if (typeof lowDeltaT.enabled !== 'boolean') problems.push('diagnosis.lowDeltaT.enabled must be true or false');
    if (!(profile.assets ?? []).some((asset) => asset.code === lowDeltaT.asset)) problems.push(`diagnosis.lowDeltaT: no asset ${lowDeltaT.asset}`);
    const points = activePoints.get(lowDeltaT.device);
    if (!points) problems.push(`diagnosis.lowDeltaT: no device ${lowDeltaT.device}`);
    else for (const code of ['supply_water_temperature', 'return_water_temperature']) if (!points.has(code)) problems.push(`diagnosis.lowDeltaT: ${lowDeltaT.device} has no active point ${code}`);
    const { minimumDeltaTC, windowMinutes } = lowDeltaT.firstRevision ?? {};
    if (!(minimumDeltaTC > 0) || !Number.isInteger(windowMinutes) || windowMinutes < 1 || windowMinutes > 1440) problems.push('diagnosis.lowDeltaT.firstRevision needs minimumDeltaTC > 0 and windowMinutes 1–1440');
  }

  const optimization = intelligence.optimization;
  if (optimization) {
    cron(optimization.cron, 'optimization');
    if (typeof optimization.enabled !== 'boolean') problems.push('optimization.enabled must be true or false');
    const mapping = optimization.policy?.constraints?.inputMapping ?? {};
    for (const key of ['supplyTemperatureKey', 'zoneTemperatureKey']) {
      const count = siteActive(mapping[key]);
      if (count !== 1) problems.push(`optimization.policy inputMapping.${key} ${mapping[key]} is on ${count} active points; preparation needs exactly one`);
    }
    if (!['人工设定', '现场数据'].includes(optimization.policy?.responseModelSource)) problems.push('optimization.policy.responseModelSource must say where the response model comes from: 人工设定 or 现场数据');
  }
  return problems;
}

// One SELECT that reads what validate compares against the manifest. Read-only.
export function siteStateQuery(profile) {
  const t = sqlLiteral(profile.tenantId);
  const st = sqlLiteral(profile.site.id);
  const model = sqlLiteral(profile.intelligence?.optimization?.model ?? '');
  return `BEGIN READ ONLY;
SELECT json_build_object(
  'site', (SELECT status FROM core_registry.sites WHERE id = ${st}),
  'devices', (SELECT coalesce(json_object_agg(id, status), '{}') FROM core_registry.devices WHERE site_id = ${st}),
  'points', (SELECT coalesce(json_object_agg(id, status), '{}') FROM core_registry.telemetry_points WHERE site_id = ${st}),
  'assets', (SELECT coalesce(json_object_agg(id, status), '{}') FROM core_registry.assets WHERE site_id = ${st}),
  'schedules', (SELECT coalesce(json_object_agg(schedule_code, json_build_object('jobType', job_type, 'cron', cron_expression, 'enabled', enabled, 'payload', payload_template)), '{}') FROM core_registry.schedule_definitions WHERE tenant_id = ${t} AND site_id = ${st}),
  'topology', EXISTS (SELECT 1 FROM core_registry.energy_topology_versions WHERE site_id = ${st} AND status = 'ACTIVE'),
  'tariff', EXISTS (SELECT 1 FROM core_registry.tariff_assignments WHERE site_id = ${st} AND status = 'RELEASED'),
  'forecast', EXISTS (SELECT 1 FROM core_registry.forecast_deployments WHERE site_id = ${st} AND status = 'ACTIVE'),
  'policy', (SELECT json_build_object('constraints', constraints, 'objective', objective, 'dispatchMode', dispatch_mode, 'horizon', horizon, 'horizonMinutes', horizon_minutes, 'granularity', granularity) FROM (${effectivePolicySql(t)}) AS current),
  'modelRevision', (SELECT revision.id FROM core_registry.ai_deployment_revisions revision JOIN core_registry.ai_model_definitions model ON model.tenant_id = revision.tenant_id AND model.id = revision.model_definition_id
    WHERE revision.tenant_id = ${t} AND revision.use_case = 'OPTIMIZATION' AND revision.enabled AND model.status = 'ACTIVE' AND model.model_id = ${model} ORDER BY revision.revision DESC LIMIT 1),
  'optimizationBinding', (SELECT json_build_object('status', binding.status, 'enabled', revision.enabled) FROM core_registry.ai_deployment_bindings binding JOIN core_registry.ai_deployment_revisions revision ON revision.tenant_id = binding.tenant_id AND revision.id = binding.deployment_revision_id
    WHERE binding.tenant_id = ${t} AND binding.site_id = ${st} AND binding.use_case = 'OPTIMIZATION'),
  'lowDeltaTRule', (SELECT json_build_object('revision', revision, 'minimumDeltaTC', minimum_delta_tc, 'windowMinutes', window_minutes) FROM core_registry.fdd_rule_revisions
    WHERE tenant_id = ${t} AND rule_code = '${LOW_DELTA_T}' AND effective_from <= clock_timestamp() ORDER BY revision DESC LIMIT 1)
);
ROLLBACK;`;
}

// Object keys in a fixed order, so two JSON values compare by content.
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  return value;
}
const sameJson = (left, right) => JSON.stringify(canonical(left)) === JSON.stringify(canonical(right));

// Paths where two JSON objects differ, e.g. inputMapping.zoneTemperatureKey.
function differences(want, have, path = '') {
  if (want && have && typeof want === 'object' && typeof have === 'object' && !Array.isArray(want)) {
    return [...new Set([...Object.keys(want), ...Object.keys(have)])].flatMap((key) => differences(want[key], have[key], path ? `${path}.${key}` : key));
  }
  return sameJson(want, have) ? [] : [`${path}: 清单 ${JSON.stringify(want)}，现在 ${JSON.stringify(have)}`];
}

// The schedule codes a generated script writes, with their job types.
function scheduleCodes(sql) {
  return [...sql.matchAll(/INSERT INTO core_registry\.schedule_definitions[\s\S]*?ON CONFLICT/g)]
    .flatMap(([block]) => [...block.matchAll(/^\s*(?:SELECT |\()[^,]+, '([a-z0-9-]+)', '[^']*', [^,]+, [^,]+, '([A-Z_]+)'/gm)].map(([, code, jobType]) => ({ code, jobType })));
}

// What the database still lacks against the manifest, as items of four kinds: ok; missing and
// differs, which apply writes; decide, which apply leaves for a person (something the Tenant
// shares with its other Sites, or a choice the manifest leaves open).
export function planOnboarding(profile, state) {
  const items = [];
  const add = (area, status, item, detail = '') => items.push({ area, status, item, detail });
  const { devices, points } = siteRows(profile);
  const sql = buildOnboardingSql(profile);

  add('站点和设备', state.site === 'ACTIVE' ? 'ok' : state.site ? 'differs' : 'missing', `站点 ${profile.site.name}`);
  const wanted = [{ id: profile.gateway.id, code: profile.gateway.code }, ...devices];
  const missingDevices = wanted.filter((device) => !state.devices[device.id]);
  const inactiveDevices = wanted.filter((device) => state.devices[device.id] && state.devices[device.id] !== 'ACTIVE');
  add('站点和设备', missingDevices.length ? 'missing' : inactiveDevices.length ? 'differs' : 'ok', `网关和设备 ${wanted.length} 台`, [...missingDevices.map((device) => `缺 ${device.code}`), ...inactiveDevices.map((device) => `${device.code} 不是 ACTIVE`)].join('；'));
  const missingAssets = (profile.assets ?? []).filter((asset) => !state.assets[asset.id]);
  add('站点和设备', missingAssets.length ? 'missing' : 'ok', `资产 ${(profile.assets ?? []).length} 台`, missingAssets.map((asset) => `缺 ${asset.code}`).join('；'));
  const missingPoints = points.filter((point) => !state.points[point.id]);
  const statusPoints = points.filter((point) => state.points[point.id] && state.points[point.id] !== pointStatus(point));
  add('点位', missingPoints.length ? 'missing' : statusPoints.length ? 'differs' : 'ok', `点位 ${points.length} 个`, [...missingPoints.map((point) => `缺 ${point.device.code}/${point.code}`), ...statusPoints.map((point) => `${point.device.code}/${point.code} 应为 ${pointStatus(point)}`)].join('；'));

  if (profile.energy) {
    add('能耗和负荷预测', state.topology ? 'ok' : 'missing', '能耗拓扑（已发布）');
    add('能耗和负荷预测', state.tariff ? 'ok' : 'missing', '电价');
    add('能耗和负荷预测', state.forecast ? 'ok' : 'missing', '负荷预测模型部署');
  }

  // Energy and forecast schedules are created once and then left to the Registry (DO NOTHING).
  for (const { code } of scheduleCodes(sql.energy)) add('能耗和负荷预测', state.schedules[code] ? 'ok' : 'missing', `调度定义 ${code}`);

  const intelligence = profile.intelligence ?? {};
  if (intelligence.alarmRules) add('告警规则', 'decide', `告警规则文件 ${intelligence.alarmRules}`, '告警规则在告警库里，由 alarm-rules 发布，不归这里写；先用它的 -print 看要新增、修改、停用哪些规则');

  const lowDeltaT = intelligence.diagnosis?.lowDeltaT;
  if (lowDeltaT) {
    const rule = state.lowDeltaTRule;
    if (!rule) add('诊断', 'missing', '低温差诊断规则版本', `租户还没有，apply 建第 1 版：${lowDeltaT.firstRevision.minimumDeltaTC} K、${lowDeltaT.firstRevision.windowMinutes} 分钟`);
    else add('诊断', 'ok', '低温差诊断规则版本', `租户共用，生效的是第 ${rule.revision} 版：${rule.minimumDeltaTC} K、${rule.windowMinutes} 分钟；改门槛用 scripts/publish-fdd-rule-revision.mjs 发新版本`);
    const code = `chilled-water-low-delta-t-${profile.site.code}`;
    const have = state.schedules[code];
    const payload = { assetId: assetByCode(profile, lowDeltaT.asset).id, deviceId: deviceByCode(profile, lowDeltaT.device).id };
    if (!have) add('诊断', 'missing', `调度定义 ${code}`);
    else if (have.cron !== lowDeltaT.cron || have.enabled !== lowDeltaT.enabled || !sameJson(have.payload, payload)) add('诊断', 'differs', `调度定义 ${code}`, '执行时间、对象或启用状态和清单不同');
    else add('诊断', 'ok', `调度定义 ${code}`);
    // The threshold lives in the Tenant's rule revisions, so the hint reads it from there rather than the note.
    if (!lowDeltaT.enabled) {
      const threshold = rule
        ? `现在生效的是第 ${rule.revision} 版：温差低于 ${rule.minimumDeltaTC} K 持续 ${rule.windowMinutes} 分钟`
        : `租户还没有规则版本，apply 建第 1 版：${lowDeltaT.firstRevision.minimumDeltaTC} K、${lowDeltaT.firstRevision.windowMinutes} 分钟`;
      add('诊断', 'decide', '低温差诊断未启用', `${lowDeltaT.note ?? '清单里 enabled 是 false'}（${threshold}）`);
    }
  }

  const optimization = intelligence.optimization;
  if (optimization) {
    const { policy } = optimization;
    const current = state.policy;
    const want = { constraints: policy.constraints, objective: policy.objective, dispatchMode: policy.dispatchMode, horizon: policy.horizon, horizonMinutes: policy.horizonMinutes, granularity: policy.granularity };
    const policyDiff = current ? differences(want, current) : [];
    if (!current) add('优化建议', 'missing', '优化策略', '租户还没有生效的策略，apply 按清单建第 1 版');
    else if (policyDiff.length) add('优化建议', 'decide', '优化策略和清单不一致', `策略是租户共用的，apply 不改：${policyDiff.join('；')}`);
    else add('优化建议', 'ok', '优化策略');
    if (current) {
      const mapping = current.constraints?.inputMapping ?? {};
      const siteCodes = new Set(points.filter((point) => pointStatus(point) === 'ACTIVE').map((point) => point.code));
      for (const key of ['supplyTemperatureKey', 'zoneTemperatureKey']) {
        if (!siteCodes.has(mapping[key])) add('优化建议', 'decide', `租户策略的 ${key} 是 ${mapping[key]}`, '本站没有这个点位，按现在的策略准备会失败');
      }
    }
    if (policy.responseModelSource === '人工设定') add('优化建议', 'decide', '响应系数是人工设定', '四个系数不是本站数据算出来的，页面「计算依据」标「人工设定」；按现场数据核定后改清单');
    if (!state.modelRevision) add('优化建议', 'decide', `模型 ${optimization.model}`, '租户没有启用的这个模型的部署版本，apply 不建');
    // Apply writes the binding and schedule only while the policy agrees and the model is deployed.
    const blocked = policyDiff.length > 0 || !state.modelRevision;
    const binding = state.optimizationBinding;
    if (binding && binding.status === 'ACTIVE' && binding.enabled) add('优化建议', 'ok', '本站的模型部署');
    else if (binding) add('优化建议', 'decide', '本站的模型部署', `已有绑定但${binding.status !== 'ACTIVE' ? '已停用' : '部署版本未启用'}，apply 不改`);
    else add('优化建议', blocked ? 'decide' : 'missing', '本站的模型部署', blocked ? '等策略一致、租户有启用的部署版本之后 apply 才登记' : '');
    const code = `supply-temperature-optimization-${profile.site.code}`;
    const have = state.schedules[code];
    if (!have) add('优化建议', blocked ? 'decide' : 'missing', `调度定义 ${code}`, blocked ? '和本站的模型部署一起登记' : '');
    else if (have.cron !== optimization.cron || have.enabled !== optimization.enabled) add('优化建议', 'differs', `调度定义 ${code}`, '执行时间或启用状态和清单不同');
    else add('优化建议', 'ok', `调度定义 ${code}`);
  }
  return items;
}

const MARK = { ok: '  有  ', missing: '  缺  ', differs: '  改  ', decide: '要决定' };

export function formatPlan(items) {
  const lines = [];
  for (const area of [...new Set(items.map((item) => item.area))]) {
    lines.push(area);
    for (const item of items.filter((candidate) => candidate.area === area)) lines.push(`  [${MARK[item.status]}] ${item.item}${item.detail ? `：${item.detail}` : ''}`);
  }
  const count = (status) => items.filter((item) => item.status === status).length;
  lines.push(`缺 ${count('missing')} 项、要改 ${count('differs')} 项（apply 会写），要人决定 ${count('decide')} 项（apply 不写）`);
  return lines.join('\n');
}

function readAlarmRules(file, profile) {
  const name = profile.intelligence?.alarmRules;
  if (!name) return null;
  const path = join(dirname(file), name);
  return existsSync(path) ? JSON.parse(readFileSync(path, 'utf8')) : null;
}

function psql(container, database, sql, capture = false) {
  const result = spawnSync('docker', ['exec', '-i', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-q', ...(capture ? ['-At'] : [])], { input: sql, encoding: 'utf8', stdio: ['pipe', capture ? 'pipe' : 'inherit', 'inherit'] });
  if (result.status !== 0) throw new Error(`${database} psql failed with ${result.status}`);
  return result.stdout;
}

function readState(container, profile) {
  return JSON.parse(psql(container, 'hvac_s1', siteStateQuery(profile), true).trim());
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [action, file, flag] = process.argv.slice(2);
  if (!['validate', 'apply', 'print'].includes(action) || !file) throw new Error('usage: node scripts/site-onboarding.mjs validate|apply|print <site file> [--offline]');
  const profile = JSON.parse(readFileSync(file, 'utf8'));
  const problems = checkSiteFile(profile, readAlarmRules(file, profile));
  if (problems.length) {
    console.error(`${file} 有 ${problems.length} 处对不上，什么都没写：\n${problems.map((problem) => `  - ${problem}`).join('\n')}`);
    process.exit(1);
  }
  const sql = buildOnboardingSql(profile);
  const container = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
  if (action === 'print') {
    process.stdout.write(`${sql.registry}\n\n${sql.telemetry}\n\n${sql.energy}\n\n${sql.intelligence}\n`);
  } else if (action === 'validate') {
    console.log(`${file}：清单本身没有问题（${sql.deviceCount} 台设备、${sql.pointCount} 个点位）`);
    if (flag !== '--offline') console.log(formatPlan(planOnboarding(profile, readState(container, profile))));
  } else {
    for (const [database, script] of [['hvac_s1', sql.registry], ['hvac_s2', sql.telemetry], ['hvac_s1', sql.energy], ['hvac_s1', sql.intelligence]].filter(([, script]) => script)) psql(container, database, script);
    console.log(`${profile.site.name}: Gateway ${profile.gateway.name}, ${sql.deviceCount} devices, ${sql.pointCount} points registered`);
    const open = planOnboarding(profile, readState(container, profile)).filter((item) => item.status !== 'ok');
    if (open.length) console.log(`还没完成的：\n${formatPlan(open)}`);
  }
}
