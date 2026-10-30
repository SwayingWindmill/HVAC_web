import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  SUBJECT_BINDING_LIFECYCLE, buildOnboardingSql, checkSiteFile, planOnboarding, pointStatus, siteStateQuery, updateWhenChanged,
} from './site-onboarding.mjs';

const songyaxuan = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8'));
const songyaxuanAlarmRules = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.alarm-rules.json', import.meta.url), 'utf8'));
const copy = <T>(value: T): T => structuredClone(value);

function bindingSection(sql) {
  const start = sql.indexOf('INSERT INTO core_registry.point_subject_bindings');
  assert.notEqual(start, -1, 'point_subject_bindings insert is missing');
  return sql.slice(start, sql.indexOf('COMMIT;', start));
}

function pointRows(sql) {
  const start = sql.indexOf('INSERT INTO core_registry.telemetry_points');
  return sql.slice(start, sql.indexOf('ON CONFLICT', start)).split('\n').filter((line) => line.startsWith('('));
}

test('each DESCRIBES binding carries its point status', () => {
  const { registry } = buildOnboardingSql(songyaxuan);
  const points = pointRows(registry);
  const bindings = bindingSection(registry).split('\n').filter((line) => line.includes("'DESCRIBES'"));
  assert.equal(bindings.length, points.length);
  const inactive = points.filter((row) => row.includes("'INACTIVE'"));
  assert.ok(inactive.length >= 2, 'the songyaxuan site file marks unwired channels INACTIVE');
  for (const [index, row] of points.entries()) {
    const status = row.includes("'INACTIVE'") ? 'INACTIVE' : 'ACTIVE';
    assert.match(bindings[index], new RegExp(`'DESCRIBES', '${status}', clock_timestamp\\(\\), NULL`), `binding ${index + 1} should be ${status}`);
  }
  const t14 = points.findIndex((row) => row.includes("'t14'"));
  assert.notEqual(t14, -1);
  assert.match(bindings[t14], /'DESCRIBES', 'INACTIVE'/);
});

test('a rerun follows the site file instead of forcing ACTIVE', () => {
  const section = bindingSection(buildOnboardingSql(songyaxuan).registry);
  const conflict = section.slice(section.indexOf('ON CONFLICT'));
  assert.ok(!conflict.includes("status='ACTIVE'"), 'binding upsert must not hard-code ACTIVE');
  const lifecycle = new Map(SUBJECT_BINDING_LIFECYCLE);
  assert.equal(lifecycle.get('status'), 'EXCLUDED.status');
  assert.match(lifecycle.get('valid_to'), /^CASE WHEN EXCLUDED\.status = 'ACTIVE' THEN NULL/);
  assert.match(lifecycle.get('valid_from'), /^CASE WHEN EXCLUDED\.status = 'ACTIVE' AND/);
  for (const [column, value] of SUBJECT_BINDING_LIFECYCLE) assert.ok(conflict.includes(`${column}=${value}`), `binding upsert sets ${column}`);
});

// Every ON CONFLICT ... DO UPDATE in a generated script, with the table it belongs to.
function upserts(sql: string) {
  return [...sql.matchAll(/INSERT INTO (\w+)\.(\w+) \([^)]*\)[\s\S]*?ON CONFLICT \([^)]*\) (DO UPDATE[\s\S]*?|DO NOTHING);/g)]
    .map(([, schema, table, action]) => ({ schema, table, action }))
    .filter(({ action }) => action.startsWith('DO UPDATE'));
}

const POLICY_TABLES = new Set(['presence_policies', 'freshness_policies']);

test('a rerun with an unchanged site file updates no row', () => {
  const { registry, telemetry, energy } = buildOnboardingSql(songyaxuan);
  const all = [...upserts(registry), ...upserts(telemetry), ...upserts(energy)];
  assert.ok(all.length >= 15, `expected every upsert to be found, got ${all.length}`);
  for (const { schema, table, action } of all) {
    assert.match(action, new RegExp(`\\nWHERE \\(${table}\\.[\\s\\S]*\\) IS DISTINCT FROM \\(`), `${schema}.${table} must update only rows whose values change`);
    const set = action.slice('DO UPDATE SET '.length, action.indexOf('\nWHERE'));
    if (schema === 'core_registry') assert.ok(set.includes(`revision=${table}.revision + 1`), `${table} must count a real change in revision`);
    else if (POLICY_TABLES.has(table)) assert.ok(set.includes(`policy_revision=${table}.policy_revision + 1`), `${table} must count a real change in policy_revision`);
    else assert.ok(!/(^|[ ,_])revision=/.test(set), `${schema}.${table} has no revision`);
    assert.ok(set.endsWith('updated_at=clock_timestamp()'), `${schema}.${table} stamps updated_at only with a change`);
  }
  const tables = new Set(all.map(({ schema, table }) => `${schema}.${table}`));
  for (const table of ['core_registry.telemetry_points', 'core_registry.point_subject_bindings', 'core_registry.devices', 'core_registry.tariffs', 'telemetry_runtime.freshness_policies']) assert.ok(tables.has(table), `${table} upsert is checked`);
});

test('the change guard compares exactly the columns it sets', () => {
  const action = updateWhenChanged('devices', [['code', 'EXCLUDED.code'], ['status', "'ACTIVE'"]]);
  assert.equal(action, `DO UPDATE SET code=EXCLUDED.code, status='ACTIVE', revision=devices.revision + 1, updated_at=clock_timestamp()
WHERE (devices.code, devices.status) IS DISTINCT FROM (EXCLUDED.code, 'ACTIVE')`);
  assert.equal(updateWhenChanged('observation_coverage', [['available', 'true']], { revision: false }), `DO UPDATE SET available=true, updated_at=clock_timestamp()
WHERE (observation_coverage.available) IS DISTINCT FROM (true)`);
  assert.equal(updateWhenChanged('presence_policies', [['online_within_seconds', 'EXCLUDED.online_within_seconds']], { revision: 'policy_revision' }), `DO UPDATE SET online_within_seconds=EXCLUDED.online_within_seconds, policy_revision=presence_policies.policy_revision + 1, updated_at=clock_timestamp()
WHERE (presence_policies.online_within_seconds) IS DISTINCT FROM (EXCLUDED.online_within_seconds)`);
});

test('a policy that changes gets a new policy_revision; the inserted revision stays 1', () => {
  const { telemetry } = buildOnboardingSql(songyaxuan);
  const policies = upserts(telemetry).filter(({ table }) => POLICY_TABLES.has(table));
  assert.deepEqual(policies.map(({ table }) => table).sort(), ['freshness_policies', 'presence_policies']);
  for (const { table, action } of policies) {
    const guarded = action.slice(action.indexOf('\nWHERE'));
    assert.ok(!guarded.includes('policy_revision'), `${table} compares policy values, not its revision`);
  }
  assert.match(telemetry, /INSERT INTO telemetry_runtime\.presence_policies [^\n]*\n\('[^']+', 1, /, 'a new presence policy starts at revision 1');
});

test('point status defaults to ACTIVE and rejects unknown values', () => {
  assert.equal(pointStatus({ code: 'a' }), 'ACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'INACTIVE' }), 'INACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'RETIRED' }), 'RETIRED');
  assert.throws(() => pointStatus({ code: 'a', status: 'inactive' }), /expected ACTIVE, INACTIVE or RETIRED/);
});

// A database that already holds everything the songyaxuan manifest describes.
function completeState(profile: any) {
  const state: any = { site: 'ACTIVE', devices: {}, points: {}, assets: {}, schedules: {}, topology: true, tariff: true, forecast: true };
  for (const match of buildOnboardingSql(profile).registry.matchAll(/'(01a14000-4000-7000-8000-[0-9a-f]{12})'/g)) state.devices[match[1]] = 'ACTIVE';
  for (const row of pointRows(buildOnboardingSql(profile).registry)) {
    const [, id] = row.match(/^\('([0-9a-f-]{36})'/) ?? [];
    state.points[id] = row.includes("'INACTIVE'") ? 'INACTIVE' : 'ACTIVE';
  }
  for (const asset of profile.assets) state.assets[asset.id] = 'ACTIVE';
  for (const { item } of planOnboarding(profile, { ...state, schedules: {}, policy: null, modelRevision: null, optimizationBinding: null, lowDeltaTRule: null })) {
    const code = item.match(/^调度定义 (.+)$/)?.[1];
    if (code) state.schedules[code] = { jobType: '', cron: '', enabled: true, payload: {} };
  }
  const { lowDeltaT } = profile.intelligence.diagnosis;
  state.schedules[`chilled-water-low-delta-t-${profile.site.code}`] = {
    cron: lowDeltaT.cron, enabled: lowDeltaT.enabled,
    payload: { assetId: profile.assets.find((asset: any) => asset.code === lowDeltaT.asset).id, deviceId: profile.devices.find((device: any) => device.code === lowDeltaT.device).id },
  };
  const { optimization } = profile.intelligence;
  state.schedules[`supply-temperature-optimization-${profile.site.code}`] = { cron: optimization.cron, enabled: optimization.enabled, payload: {} };
  const { policy } = optimization;
  state.policy = { constraints: copy(policy.constraints), objective: policy.objective, dispatchMode: policy.dispatchMode, horizon: policy.horizon, horizonMinutes: policy.horizonMinutes, granularity: policy.granularity };
  state.modelRevision = '01910000-0000-7000-8000-80000000000a';
  state.optimizationBinding = { status: 'ACTIVE', enabled: true };
  state.lowDeltaTRule = { revision: 1, minimumDeltaTC: 3.5, windowMinutes: 15 };
  return state;
}

const byStatus = (items: any[], status: string) => items.filter((item) => item.status === status).map((item) => item.item);

test('the songyaxuan manifest and its alarm rules pass the file check', () => {
  assert.deepEqual(checkSiteFile(songyaxuan, songyaxuanAlarmRules), []);
});

test('the file check names alarm inputs, keys and fields that do not match the site', () => {
  const profile = copy(songyaxuan);
  profile.intelligence.optimization.policy.constraints.inputMapping.zoneTemperatureKey = 'temperature';
  delete profile.intelligence.optimization.policy.responseModelSource;
  profile.intelligence.diagnosis.lowDeltaT.asset = 'no-such-asset';
  const rules = copy(songyaxuanAlarmRules);
  rules.rules[0].raise = { op: 'AND', children: [{ op: 'COMPARE', input: 'meter-uid5/no_such_point' }, { op: 'COMPARE', input: 'no-such-device/run_state' }] };
  const problems = checkSiteFile(profile, rules).join('\n');
  assert.match(problems, /meter-uid5\/no_such_point: meter-uid5 has no active point no_such_point/);
  assert.match(problems, /no-such-device\/run_state: no device no-such-device/);
  assert.match(problems, /zoneTemperatureKey temperature is on 0 active points/);
  assert.match(problems, /responseModelSource/);
  assert.match(problems, /diagnosis.lowDeltaT: no asset no-such-asset/);
  assert.match(checkSiteFile(songyaxuan, null).join('\n'), /alarm rules file songyaxuan.alarm-rules.json was not found/);
});

test('validate on a complete site lists only what a person still decides', () => {
  const items = planOnboarding(songyaxuan, completeState(songyaxuan));
  assert.deepEqual(byStatus(items, 'missing'), []);
  assert.deepEqual(byStatus(items, 'differs'), []);
  assert.deepEqual(byStatus(items, 'decide'), ['告警规则文件 songyaxuan.alarm-rules.json', '低温差诊断未启用', '响应系数是人工设定']);
});

test('a Tenant policy that disagrees blocks the Optimization deployment and is left for a person', () => {
  const state = completeState(songyaxuan);
  state.policy.constraints.inputMapping.zoneTemperatureKey = 'temperature';
  state.optimizationBinding = null;
  delete state.schedules['supply-temperature-optimization-songyaxuan'];
  delete state.assets[songyaxuan.assets.at(-1).id];
  const items = planOnboarding(songyaxuan, state);
  assert.deepEqual(byStatus(items, 'missing'), ['资产 17 台']);
  const decide = items.filter((item) => item.status === 'decide');
  assert.match(decide.find((item) => item.item === '优化策略和清单不一致').detail, /zoneTemperatureKey: 清单 "room_temperature"，现在 "temperature"/);
  assert.ok(decide.some((item) => item.item === '租户策略的 zoneTemperatureKey 是 temperature'));
  assert.ok(decide.some((item) => item.item === '本站的模型部署'));
  assert.ok(decide.some((item) => item.item === '调度定义 supply-temperature-optimization-songyaxuan'));
});

test('a Tenant without a policy or rule gets them from the manifest', () => {
  const state = completeState(songyaxuan);
  state.policy = null;
  state.lowDeltaTRule = null;
  state.optimizationBinding = null;
  delete state.schedules['supply-temperature-optimization-songyaxuan'];
  const missing = byStatus(planOnboarding(songyaxuan, state), 'missing');
  assert.deepEqual(missing, ['低温差诊断规则版本', '优化策略', '本站的模型部署', '调度定义 supply-temperature-optimization-songyaxuan']);
});

test('apply never rewrites what the Tenant shares and registers Optimization only under an agreeing policy', () => {
  const { intelligence } = buildOnboardingSql(songyaxuan);
  assert.ok(!/UPDATE\s+core_registry\.(optimization_polic|fdd_rule_revisions)|ON CONFLICT[^;]*DO UPDATE SET[^;]*constraints/.test(intelligence), 'shared policy and rule are never updated');
  assert.match(intelligence, /INSERT INTO core_registry\.fdd_rule_revisions[\s\S]*?WHERE NOT EXISTS \(SELECT 1 FROM core_registry\.fdd_rule_revisions WHERE tenant_id = '[^']+' AND rule_code = 'CHILLED_WATER_LOW_DELTA_T'\);/);
  assert.match(intelligence, /INSERT INTO core_registry\.optimization_policies[\s\S]*?WHERE NOT EXISTS \(SELECT 1 FROM core_registry\.optimization_policies WHERE tenant_id = '[^']+' AND resource_type = 'HVAC' AND status = 'ACTIVE'\);/);
  for (const table of ['ai_deployment_bindings', 'schedule_definitions (schedule_id, schedule_code, schedule_name, tenant_id, site_id, job_type, schedule_type, cron_expression, cron_format_version, timezone, misfire_policy, timeout_seconds, payload_template, enabled, next_fire_at)\nSELECT \'01a14000-7b00']) {
    const start = intelligence.indexOf(`INSERT INTO core_registry.${table}`);
    assert.notEqual(start, -1, table);
    const statement = intelligence.slice(start, intelligence.indexOf(';\n', start));
    assert.match(statement, /(?:WHERE|AND) EXISTS \(SELECT 1 FROM \(SELECT pv\.constraints/, `${table} waits for the policy to agree`);
    assert.ok(statement.includes('"zoneTemperatureKey":"room_temperature"'), `${table} compares against the manifest's constraints`);
  }
  assert.match(intelligence, /'chilled-water-low-delta-t-songyaxuan'[^\n]*'FDD_EVALUATION'[^\n]*, false, /, 'the diagnosis schedule follows enabled: false');
  for (const statement of intelligence.split(/;\n/).filter((sql) => sql.includes('DO UPDATE SET'))) {
    assert.match(statement, /version=schedule_definitions\.version \+ 1/);
    assert.match(statement, /\nWHERE \(schedule_definitions\.schedule_name, schedule_definitions\.cron_expression, schedule_definitions\.payload_template, schedule_definitions\.enabled\) IS DISTINCT FROM/);
  }
});

test('validate reads the database with one read-only SELECT', () => {
  const query = siteStateQuery(songyaxuan);
  assert.ok(query.startsWith('BEGIN READ ONLY;\nSELECT json_build_object('));
  assert.ok(query.endsWith('ROLLBACK;'));
  assert.ok(!/\b(INSERT|UPDATE|DELETE|TRUNCATE|ALTER|DROP)\b/i.test(query));
});
