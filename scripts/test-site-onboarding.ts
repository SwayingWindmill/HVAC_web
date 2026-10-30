import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { SUBJECT_BINDING_LIFECYCLE, buildOnboardingSql, pointStatus } from './site-onboarding.mjs';

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
  assert.ok(conflict.includes(SUBJECT_BINDING_LIFECYCLE));
  assert.ok(!conflict.includes("status='ACTIVE'"), 'binding upsert must not hard-code ACTIVE');
  assert.match(SUBJECT_BINDING_LIFECYCLE, /status=EXCLUDED\.status/);
  assert.match(SUBJECT_BINDING_LIFECYCLE, /valid_to=CASE WHEN EXCLUDED\.status = 'ACTIVE' THEN NULL/);
  assert.match(SUBJECT_BINDING_LIFECYCLE, /valid_from=CASE WHEN EXCLUDED\.status = 'ACTIVE' AND/);
});

test('point status defaults to ACTIVE and rejects unknown values', () => {
  assert.equal(pointStatus({ code: 'a' }), 'ACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'INACTIVE' }), 'INACTIVE');
  assert.equal(pointStatus({ code: 'a', status: 'RETIRED' }), 'RETIRED');
  assert.throws(() => pointStatus({ code: 'a', status: 'inactive' }), /expected ACTIVE, INACTIVE or RETIRED/);
});
