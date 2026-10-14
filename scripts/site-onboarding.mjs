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
    return `(${sqlLiteral(point.id)}, ${tenant}, ${siteId}, ${sqlLiteral(point.device.id)}, NULL, ${sqlLiteral(point.code)}, ${sqlLiteral(point.sourceKey ?? point.code)}, ${sqlLiteral(point.name)}, ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, false, ${interval}, ${interval}, ${interval * 3}, ${sqlJson({ protocol: 'MODBUS_RTU', address: point.address, gatewaySourceKey: point.device.sourceKey })}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), NULL, NULL, ${point.pointType === 'COUNTER' ? "'RESET_TO_ZERO'" : 'NULL'}, NULL)`;
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
  return { registry, telemetry, energy: buildEnergySql(profile, devices, points), deviceCount: devices.length, pointCount: points.length };
}

const ELECTRICITY = '01990000-0000-7000-8000-000000000001';

// The Site's energy model, when its site file has one: the total meter on the grid feed into the
// plant, each machine's meter on the plant's feed into that machine (a sub-meter inside the total),
// the Tenant's released energy Metrics bound to those counters and calculated on schedule, the
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
  const version = id('7000', 1);
  const nodes = [
    { key: 'grid', type: 'GRID', name: '电网' },
    { key: 'plant', type: 'HVAC', name: energy.plantName },
    ...branches.map((device) => ({ key: device.sourceKey, type: 'LOAD', name: assetName.get(device.asset) ?? device.name, deviceId: device.id })),
  ].map((node, index) => ({ ...node, id: id('7100', index + 1) }));
  const nodeId = new Map(nodes.map((node) => [node.key, node.id]));
  const meters = [
    { device: total, from: 'grid', to: 'plant', direction: 'IMPORT' },
    ...branches.map((device) => ({ device, from: 'plant', to: device.sourceKey, direction: 'CONSUME' })),
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
    { code: 'energy_cost', subject: 'SITE', subjectId: site.id, granularity: 'DAY', source: {}, cron: '20 0 * * *', name: '空调日电度电费（每日零点后）' },
    ...branches.map((device) => ({
      code: 'device_hourly_energy', subject: 'DEVICE', subjectId: device.id, granularity: 'HOUR', cron: '5 * * * *',
      source: { points: { device_energy: counter(device) }, counter: { decreaseMode: 'RESET' } },
      name: `${assetName.get(device.asset) ?? device.name}逐时用电量（每小时）`,
    })),
  ].map((binding, index) => ({ ...binding, index: index + 1 }));
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
${meters.map((meter) => `  (${meter.edge}::uuid, ${t}::uuid, ${st}::uuid, ${version}::uuid, ${nodeId.get(meter.from)}::uuid, ${nodeId.get(meter.to)}::uuid, '${ELECTRICITY}'::uuid, '${meter.direction}', true, 1::bigint, clock_timestamp(), clock_timestamp())`).join(',\n')}
) AS edge ${draft}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.energy_meters (id, tenant_id, site_id, meter_code, display_name, device_id, energy_type_id, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${meter.meter}, ${t}, ${st}, ${sqlLiteral(meter.device.code.replaceAll('-', '_'))}, ${sqlLiteral(meter.device.name)}, ${sqlLiteral(meter.device.id)}, '${ELECTRICITY}', 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.meter_bindings (id, tenant_id, site_id, topology_version_id, energy_edge_id, energy_type_id, meter_id, device_id, point_id, point_type, meter_role, direction, priority, effective_from, version, status, revision, created_at, updated_at) VALUES
${meters.map((meter) => `  (${meter.binding}, ${t}, ${st}, ${version}, ${meter.edge}, '${ELECTRICITY}', ${meter.meter}, ${sqlLiteral(meter.device.id)}, ${meter.point}, 'COUNTER', 'PRIMARY', '${meter.direction}', 0, '2026-01-01T00:00:00Z', 1, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
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
ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = clock_timestamp();
INSERT INTO core_registry.tariff_versions (id, tenant_id, site_id, tariff_id, version, effective_from, timezone, currency, billing_cycle, status, revision, created_at, updated_at)
VALUES (${id('7800', 2)}, ${t}, ${st}, ${id('7800', 3)}, 1, '2026-01-01T00:00:00Z', ${sqlLiteral(tariff.timezone)}, 'CNY', 'CALENDAR_MONTH', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_periods (id, tenant_id, site_id, tariff_version_id, period_code, day_type, local_start_minute, local_end_minute, pricing_rule, ordinal, revision, created_at, updated_at) VALUES
${periods.join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.tariff_assignments (id, tenant_id, site_id, boundary_id, tariff_id, effective_from, status, revision, created_at, updated_at)
VALUES (${id('7800', 4)}, ${t}, ${st}, ${id('7800', 1)}, ${id('7800', 3)}, '2026-01-01T00:00:00Z', 'RELEASED', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
COMMIT;`;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [file, flag] = process.argv.slice(2);
  if (!file) throw new Error('usage: node scripts/site-onboarding.mjs <site file> [--print]');
  const profile = JSON.parse(readFileSync(file, 'utf8'));
  const { registry, telemetry, energy, deviceCount, pointCount } = buildOnboardingSql(profile);
  if (flag === '--print') {
    process.stdout.write(`${registry}\n\n${telemetry}\n\n${energy}\n`);
  } else {
    const container = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
    for (const [database, sql] of [['hvac_s1', registry], ['hvac_s2', telemetry], ['hvac_s1', energy]].filter(([, sql]) => sql)) {
      const result = spawnSync('docker', ['exec', '-i', container, 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-q'], { input: sql, stdio: ['pipe', 'inherit', 'inherit'] });
      if (result.status !== 0) throw new Error(`${database} onboarding failed with ${result.status}`);
    }
    console.log(`${profile.site.name}: Gateway ${profile.gateway.name}, ${deviceCount} devices, ${pointCount} points registered`);
  }
}
