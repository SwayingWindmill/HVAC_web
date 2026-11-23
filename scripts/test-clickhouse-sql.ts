import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import test from 'node:test';

import {
  checkClickHouseSqlRepository,
  checkGoFmtVerbMix,
  findClickHouseSqlViolations,
  findClickHouseViolationsInSource,
} from './check-clickhouse-sql.ts';

test('CH-ALIAS-SHADOW catches PR #917 calc_version alias collision inside argMax', () => {
  const buggySql = `
CREATE OR REPLACE VIEW analytics.energy_hourly_v2 AS
SELECT tenant_id, energy_kwh, calc_version
FROM (
  SELECT
    f.tenant_id AS tenant_id,
    argMax(f.energy_kwh, f.calc_version) AS energy_kwh,
    max(f.calc_version) AS calc_version
  FROM analytics.energy_hourly_facts_v2 AS f
  GROUP BY f.tenant_id
);`;
  const violations = findClickHouseSqlViolations('022-bug.sql', buggySql);
  assert.ok(
    violations.some((v) => v.rule === 'CH-ALIAS-SHADOW' && v.message.includes('calc_version')),
    `expected CH-ALIAS-SHADOW for calc_version, got: ${JSON.stringify(violations)}`,
  );
});

test('CH-ALIAS-SHADOW catches PR #803 HAVING argMax shadowing and WHERE aggregate alias shadowing', () => {
  const buggyHavingSql = `
SELECT tenant_id, site_id, max(generation) AS generation
FROM analytics.site_total_submeter_syncs FINAL
GROUP BY tenant_id, site_id
HAVING argMax(synthesized, generation) = 1;`;
  const havingViolations = findClickHouseSqlViolations('016-bug.sql', buggyHavingSql);
  assert.ok(
    havingViolations.some((v) => v.rule === 'CH-ALIAS-SHADOW' && v.message.includes('HAVING')),
    `expected CH-ALIAS-SHADOW in HAVING, got: ${JSON.stringify(havingViolations)}`,
  );

  const buggyWhereSql = `
SELECT max(sampled_at) AS sampled_at
FROM telemetry_history.observations
WHERE sampled_at >= now() - INTERVAL 1 HOUR;`;
  const whereViolations = findClickHouseSqlViolations('where-bug.sql', buggyWhereSql);
  assert.ok(
    whereViolations.some((v) => v.rule === 'CH-ALIAS-SHADOW' && v.message.includes('WHERE/PREWHERE')),
    `expected CH-ALIAS-SHADOW in WHERE, got: ${JSON.stringify(whereViolations)}`,
  );
});

test('CH-RESERVED-ALIAS catches PR #895 AS rollup while allowing CTEs, CAST types, and CREATE VIEW AS SELECT', () => {
  const buggySql = `
SELECT bucket, total_kwh
FROM (
  SELECT toStartOfHour(sampled_at) AS bucket, sum(value_number) AS total_kwh
  FROM telemetry_history.observations
  GROUP BY bucket
) AS rollup;`;
  const violations = findClickHouseSqlViolations('rollup-bug.sql', buggySql);
  assert.ok(
    violations.some((v) => v.rule === 'CH-RESERVED-ALIAS' && v.message.includes('AS rollup')),
    `expected CH-RESERVED-ALIAS for AS rollup, got: ${JSON.stringify(violations)}`,
  );

  const validSql = `
CREATE OR REPLACE VIEW analytics.valid_v
DEFINER = CURRENT_USER SQL SECURITY DEFINER AS
WITH
  rollup AS (
    SELECT tenant_id, CAST(sum(energy_kwh) AS Nullable(Float64)) AS total_kwh
    FROM analytics.energy_hourly
    GROUP BY tenant_id
  )
SELECT tenant_id, total_kwh AS "rollup"
FROM rollup
FORMAT JSONEachRow;`;
  assert.deepEqual(findClickHouseSqlViolations('valid.sql', validSql), []);
});

test('CH-WITH-ALIAS-SHADOW catches cyclic and aggregate WITH expressions used in aggregates or WHERE', () => {
  const buggyWithSql = `
WITH max(calc_version) AS latest_ver
SELECT argMax(energy_kwh, latest_ver) AS energy_kwh
FROM analytics.energy_hourly_facts_v2;`;
  const violations = findClickHouseSqlViolations('with-bug.sql', buggyWithSql);
  assert.ok(
    violations.some((v) => v.rule === 'CH-WITH-ALIAS-SHADOW'),
    `expected CH-WITH-ALIAS-SHADOW, got: ${JSON.stringify(violations)}`,
  );
});

test('CH-FMT-VERB-MIX catches PR #916 mixed fmt.Sprintf positional and unindexed verbs while allowing %% date specifiers', () => {
  const buggyGo = `package sample
import "fmt"
func q() string {
  return fmt.Sprintf(\`SELECT tenant_id FROM %[1]s.%[2]s WHERE site_id = toUUID('%s')\`, "analytics", "facts", "id")
}`;
  const violations = findClickHouseViolationsInSource('sample_clickhouse.go', buggyGo);
  assert.ok(
    violations.some((v) => v.rule === 'CH-FMT-VERB-MIX'),
    `expected CH-FMT-VERB-MIX, got: ${JSON.stringify(violations)}`,
  );

  const validFormat = `SELECT formatDateTime(period_start, '%%Y-%%m-%%dT%%H:%%i:%%SZ', 'UTC') FROM %[1]s.%[2]s WHERE tenant_id = '%[3]s'`;
  assert.equal(checkGoFmtVerbMix(validFormat), false);
});

test('CH-ALIAS-SHADOW catches unqualified period_end shadowing inside Cube YAML sql blocks', () => {
  const buggyCubeYml = `
cubes:
  - name: site_energy_facts
    sql: >
      SELECT tenant_id, site_id, addMilliseconds(period_end, -1) AS period_end, energy_kwh
      FROM analytics.energy_interval_facts_cube
      WHERE {FILTER_PARAMS.site_energy_facts.period_end.filter('addMilliseconds(period_end, -1)')}
        AND {FILTER_PARAMS.site_energy_facts.period_end.filter(lambda f, t: f"(period_end >= parseDateTime64BestEffort({f}, 3, 'UTC'))")}
`;
  const violations = findClickHouseViolationsInSource('energy_facts.yml', buggyCubeYml);
  assert.ok(
    violations.some((v) => v.rule === 'CH-ALIAS-SHADOW' && v.message.includes('period_end')),
    `expected CH-ALIAS-SHADOW on Cube YAML period_end, got: ${JSON.stringify(violations)}`,
  );

  const fixedCubeYml = `
cubes:
  - name: site_energy_facts
    sql: >
      SELECT e.tenant_id AS tenant_id, e.site_id AS site_id, addMilliseconds(e.period_end, -1) AS period_end, e.energy_kwh AS energy_kwh
      FROM analytics.energy_interval_facts_cube AS e
      WHERE {FILTER_PARAMS.site_energy_facts.period_end.filter('addMilliseconds(e.period_end, -1)')}
        AND {FILTER_PARAMS.site_energy_facts.period_end.filter(lambda f, t: f"(e.period_end >= parseDateTime64BestEffort({f}, 3, 'UTC'))")}
`;
  assert.deepEqual(findClickHouseViolationsInSource('energy_facts.yml', fixedCubeYml), []);
});

test('current repository ClickHouse SQL files and Go clients pass with zero violations', () => {
  const violations = checkClickHouseSqlRepository(resolve(process.cwd()));
  assert.deepEqual(violations, []);
});
