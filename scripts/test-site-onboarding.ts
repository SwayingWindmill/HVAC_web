import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { SUBJECT_BINDING_LIFECYCLE, buildOnboardingSql, pointStatus, updateWhenChanged } from './site-onboarding.mjs';

const songyaxuan = JSON.parse(readFileSync(new URL('../deploy/sites/songyaxuan.site.json', import.meta.url), 'utf8'));

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
