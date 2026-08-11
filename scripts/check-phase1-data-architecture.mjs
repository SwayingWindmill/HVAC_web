import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const sourcePath = '架构规划/智慧能源系统数据架构设计.md';
const baselinePath = 'contracts/data/phase1-data-architecture.v1.json';
const registryMigrationPath = 'infra/s1-registry/postgres/init/009-energy-data-foundation.sql';
const rollupMigrationPath = 'infra/s2-telemetry/clickhouse/init/003-telemetry-rollups.sql';

const [source, baseline, registrySQL, rollupSQL, historySQL, telemetrySQL, commandTenantSQL, commandPointSQL, alarmTenantSQL, alarmEventSQL, workOrderTenantSQL, delegationGo, gatewayRegistryGo, gatewayCommandGo, commandOpenAPI, commandContractTS, gatewayAlarmGo, gatewayWorkOrderGo] = await Promise.all([
  read(sourcePath),
  readJSON(baselinePath),
  read(registryMigrationPath),
  read(rollupMigrationPath),
  read('infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql'),
  read('infra/s2-telemetry/postgres/init/001-s2-telemetry-baseline.sql'),
  read('services/command-service/migrations/003_s3_tenant_scope.sql'),
  read('services/command-service/migrations/004_s3_command_point_identity.sql'),
  read('services/alarm-service/migrations/003_s4_tenant_scope.sql'),
  read('services/alarm-service/migrations/004_s4_event_provenance.sql'),
  read('services/work-order-service/migrations/004_s5_tenant_scope.sql'),
  read('libs/identitycontext/delegation.go'),
  read('services/platform-gateway/internal/gateway/registry.go'),
  read('services/platform-gateway/internal/gateway/command.go'),
  readJSON('contracts/http/s3-command-public.openapi.json'),
  read('apps/hvac-web/src/api/command-contract.ts'),
  read('services/platform-gateway/internal/gateway/alarm.go'),
  read('services/platform-gateway/internal/gateway/work_order.go'),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(source.includes('统一对象模型 + 统一测点模型 + 统一时序模型 + 统一能源模型 + 统一拓扑模型'), 'source data design no longer contains the canonical unified-model principle');
for (const marker of ['Asset Model', 'Device Model', 'Point Model', 'Time Series Model', 'Energy Topology Model', 'Metric Model']) {
  assert(source.includes(marker), `source data design is missing core model: ${marker}`);
}

assert(baseline.schemaVersion === 1, 'data architecture baseline schemaVersion must be 1');
assert(baseline.sourceOfTruth === sourcePath, 'data architecture baseline must cite the canonical data design');
assert(JSON.stringify(baseline.coreModels) === JSON.stringify(['ASSET','DEVICE','POINT','TIME_SERIES','ENERGY_TOPOLOGY','METRIC']), 'data architecture baseline core model order changed');
assert(baseline.phase1AcceptanceEligible === false, 'data architecture must not claim full Phase 1 acceptance while tracked gaps remain');

const byId = new Map(baseline.items.map((item) => [item.id, item]));
for (const id of ['TENANT','SITE','SPACE','ASSET','DEVICE','DEVICE_PRODUCT','POINT_TEMPLATE','POINT','UNIT_REGISTRY','TIME_SERIES_CURRENT','TIME_SERIES_HISTORY','TIME_SERIES_ROLLUP','DATA_QUALITY','ENERGY_TYPE','ENERGY_DIRECTION','ENERGY_TOPOLOGY','COMMAND']) {
  assert(byId.get(id)?.status === 'PASS', `${id} must be implemented as a Phase 1 data foundation`);
}
for (const id of ['METRIC','EVENT','ALARM','WEATHER']) {
  assert(byId.get(id)?.status === 'PARTIAL', `${id} must remain PARTIAL until its documented gap is implemented`);
}
for (const id of ['ENERGY_BALANCE','TAG','TARIFF','AI_DATA','OPTIMIZATION']) {
  assert(byId.get(id)?.status === 'MISSING', `${id} must remain visibly MISSING instead of being represented by placeholders`);
}

for (const table of ['unit_registry','device_products','point_templates','energy_types','energy_directions','energy_nodes','energy_edges','metric_definitions']) {
  assert(registrySQL.includes(`core_registry.${table}`), `Registry data foundation is missing core_registry.${table}`);
}
for (const marker of ['devices_tenant_product_fk','telemetry_points_tenant_template_fk','equipment_tenant_parent_fk','equipment_reject_cycle']) {
  assert(registrySQL.includes(marker), `Registry data foundation is missing invariant ${marker}`);
}
for (const pointType of ['TELEMETRY','STATE','COUNTER','COMMAND','SETTING']) {
  assert(registrySQL.includes(`'${pointType}'`), `Point Template taxonomy is missing ${pointType}`);
}
for (const direction of ['IMPORT','EXPORT','GENERATE','CONSUME','CHARGE','DISCHARGE']) {
  assert(registrySQL.includes(`'${direction}'`), `Energy Direction taxonomy is missing ${direction}`);
}
for (const energyType of ['electricity','water','gas','steam','heat','cooling','compressed_air','hydrogen']) {
  assert(registrySQL.includes(`'${energyType}'`), `Energy Type taxonomy is missing ${energyType}`);
}
assert(registrySQL.includes("'electricity', 'Electricity', 'kWh'"), 'Electricity must have the document-supported kWh default unit');
assert(registrySQL.includes("'water', 'Water', NULL") && registrySQL.includes("'hydrogen', 'Hydrogen', NULL"), 'unspecified Energy Type default units must remain unset instead of being guessed');

assert(historySQL.includes('telemetry_history.observations'), 'raw telemetry history authority is missing');
assert(historySQL.includes('telemetry_history.numeric_hourly'), 'hourly telemetry rollup is missing');
for (const layer of ['numeric_1min','numeric_15min','numeric_daily']) {
  assert(rollupSQL.includes(`telemetry_history.${layer}`), `telemetry rollup layer is missing ${layer}`);
}
assert(telemetrySQL.includes('latest_accepted_telemetry'), 'authoritative current telemetry state is missing');
assert(telemetrySQL.includes('quality_reasons') && telemetrySQL.includes('ingest_quarantine'), 'telemetry data-quality evidence is missing');

for (const field of ['event_id','event_type','tenant_id','site_id','device_id','point_id','severity','message','start_time','end_time','status']) {
  assert(alarmEventSQL.includes(field), `canonical Event model is missing source-defined field ${field}`);
}
assert(alarmEventSQL.includes('alarm_runtime.events'), 'canonical Event table is missing');
assert(alarmEventSQL.includes('alarm_current_event_scope_fk') && alarmEventSQL.includes('event_id') && alarmEventSQL.includes('point_id') && alarmEventSQL.includes('alarm_type'), 'Alarm does not retain Event/Point provenance');
assert(alarmEventSQL.includes('FORCE ROW LEVEL SECURITY') && alarmEventSQL.includes('alarm_runtime.current_tenant_id()'), 'canonical Event model is not Tenant-scoped with forced RLS');

for (const [domain, sql] of [['Command', commandTenantSQL], ['Alarm', alarmTenantSQL], ['Work Order', workOrderTenantSQL]]) {
  for (const marker of ['organization_tenant_scope', 'tenant_id', 'current_tenant_id()', "app.tenant_id", 'FORCE ROW LEVEL SECURITY']) {
    assert(sql.includes(marker), `${domain} Tenant scope is missing invariant ${marker}`);
  }
}
assert(commandPointSQL.includes('ADD COLUMN point_id uuid NOT NULL'), 'Command Intent does not persist canonical point_id');
assert(commandPointSQL.includes('command_intents_tenant_point_sequence_idx'), 'Command point identity lacks Tenant/Organization/Site index evidence');
assert(gatewayCommandGo.includes('pointID: target.point.ID') && gatewayCommandGo.includes('PointID: prepared.pointID'), 'Gateway does not propagate authoritative Registry Command Point identity');
assert(gatewayCommandGo.includes('view.PointID != prepared.pointID'), 'Gateway does not fail closed on Command Point projection drift');
assert(commandOpenAPI.info?.version === '0.5.0-command-point-identity', 'Command public contract is not bound to canonical Point identity');
assert(commandOpenAPI.components?.schemas?.Command?.required?.includes('pointId') && commandOpenAPI.components?.schemas?.Command?.properties?.pointId?.format === 'uuid', 'Command public projection does not require UUID pointId');
assert(commandOpenAPI.paths?.['/api/v1/commands']?.post?.['x-client-forbidden-fields']?.includes('pointId'), 'browser is allowed to submit authoritative pointId directly');
assert(commandContractTS.includes('pointId: commandUUIDv7Schema'), 'HVAC Web strict Command schema does not validate canonical pointId');
assert(!baseline.acceptanceBlockers.includes('COMMAND'), 'COMMAND must not remain an acceptance blocker after canonical point identity is implemented');
assert(delegationGo.includes('TenantID') && delegationGo.includes('json:"tenantId,omitempty"'), 'signed internal delegation context does not carry TenantID');
assert(gatewayRegistryGo.includes('resolveAuthoritativeSiteForDomain') && gatewayRegistryGo.includes('site.TenantID'), 'Gateway lacks authoritative Registry Site Tenant resolution');
assert(gatewayAlarmGo.includes('resolveAuthoritativeSiteForDomain') && gatewayAlarmGo.includes('TenantID: tenantID'), 'Alarm Gateway path does not sign authoritative TenantID');
assert(gatewayWorkOrderGo.includes('resolveAuthoritativeSiteForDomain') && gatewayWorkOrderGo.includes('TenantID: tenantID'), 'Work Order Gateway path does not sign authoritative TenantID');
assert(!baseline.acceptanceBlockers.includes('TENANT'), 'TENANT must not remain an acceptance blocker after cross-domain Tenant scope is implemented');

const statusCounts = Object.fromEntries(
  baseline.statuses.map((status) => [status, baseline.items.filter((item) => item.status === status).length]),
);

if (failures.length > 0) {
  console.error(`Phase 1 data architecture check failed:\n${failures.map((failure) => `- ${failure}`).join('\n')}`);
  process.exit(1);
}

console.log(`Phase 1 data architecture baseline passed: items=${baseline.items.length}; ${Object.entries(statusCounts).map(([status, count]) => `${status}=${count}`).join(', ')}; acceptanceEligible=${baseline.phase1AcceptanceEligible}`);
