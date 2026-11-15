import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';

import {
  findMigrationConventionViolations,
  inspectMigrationConventions,
  loadRepoConventionInput,
  type MigrationConventionBaseline,
} from './check-migration-conventions.ts';

const repoRoot = resolve(import.meta.dirname, '..');
const checkScript = resolve(repoRoot, 'scripts/check-migration-conventions.ts');

const emptyBaseline = (overrides: Partial<MigrationConventionBaseline> = {}): MigrationConventionBaseline => ({
  schemaVersion: 1,
  maxGrandfatheredMigrations: overrides.maxGrandfatheredMigrations ?? 0,
  legacyDatabases: overrides.legacyDatabases ?? [],
  grandfatheredMigrations: overrides.grandfatheredMigrations ?? [],
});

test('repository satisfies migration conventions with 12 live schemas and 221 live tables', () => {
  const result = inspectMigrationConventions(loadRepoConventionInput(repoRoot));
  assert.deepEqual(result.violations, []);
  assert.equal(result.liveSchemas.size, 12);
  assert.equal(result.liveTables.size, 221);
});

test('real historical 005_s13_alarm_orthogonal.sql fails when evaluated as a new migration', () => {
  const real005 = readFileSync(resolve(repoRoot, 'modules/alarm/migrations/005_s13_alarm_orthogonal.sql'), 'utf8');
  const violations = findMigrationConventionViolations({
    baseline: emptyBaseline({ legacyDatabases: ['hvac_s4'], grandfatheredMigrations: ['modules/alarm/migrations/001_seed.sql'], maxGrandfatheredMigrations: 1 }),
    migrations: [
      { database: 'hvac_s4', path: 'modules/alarm/migrations/001_seed.sql' },
      { database: 'hvac_s4', path: 'modules/alarm/migrations/005_s13_alarm_orthogonal.sql' },
    ],
    files: {
      'modules/alarm/migrations/001_seed.sql': `
        CREATE SCHEMA alarm_runtime;
        CREATE TABLE alarm_runtime.alarm_current (alarm_id uuid PRIMARY KEY);
        CREATE TABLE alarm_runtime.alarm_idempotency (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA alarm_runtime IS '告警域。';
        COMMENT ON TABLE alarm_runtime.alarm_current IS '当前告警。';
        COMMENT ON TABLE alarm_runtime.alarm_idempotency IS '告警幂等键。';
      `,
      'modules/alarm/migrations/005_s13_alarm_orthogonal.sql': real005,
    },
  });

  assert.ok(violations.some((v) => v.includes('005_s13_alarm_orthogonal.sql') && v.includes('stage or temporary tag')), violations.join('\n'));
  assert.ok(violations.some((v) => v.includes('CREATE TABLE alarm_runtime.alarm_current_pre_s13_backup AS is forbidden')), violations.join('\n'));
  assert.ok(violations.some((v) => v.includes('s13_alarm_migration_report')), violations.join('\n'));
  assert.ok(violations.some((v) => v.includes('s13_alarm_identity_map')), violations.join('\n'));
  assert.ok(violations.some((v) => v.includes('alarm_runtime.alarm_timeline') && v.includes('missing a non-empty COMMENT ON TABLE')), violations.join('\n'));
});

test('real historical 016_s32_rules_unevaluable.sql fails stage filename check when evaluated as a new migration', () => {
  const real016 = readFileSync(resolve(repoRoot, 'modules/alarm/migrations/016_s32_rules_unevaluable.sql'), 'utf8');
  const violations = findMigrationConventionViolations({
    baseline: emptyBaseline({ legacyDatabases: ['hvac_s4'], grandfatheredMigrations: ['modules/alarm/migrations/001_seed.sql'], maxGrandfatheredMigrations: 1 }),
    migrations: [
      { database: 'hvac_s4', path: 'modules/alarm/migrations/001_seed.sql' },
      { database: 'hvac_s4', path: 'modules/alarm/migrations/016_s32_rules_unevaluable.sql' },
    ],
    files: {
      'modules/alarm/migrations/001_seed.sql': `
        CREATE SCHEMA alarm_runtime;
        COMMENT ON SCHEMA alarm_runtime IS '告警域。';
      `,
      'modules/alarm/migrations/016_s32_rules_unevaluable.sql': real016,
    },
  });

  assert.ok(
    violations.some((v) => v.includes('016_s32_rules_unevaluable.sql') && v.includes('stage or temporary tag')),
    violations.join('\n'),
  );
});

test('rejects new stage-numbered database hvac_s6 while allowing business-domain databases', () => {
  const bad = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_s6', path: 'modules/billing/migrations/001_ledger.sql' }],
    files: {
      'modules/billing/migrations/001_ledger.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '账单域。';
        COMMENT ON TABLE billing.invoices IS '结算电费单。';
      `,
    },
  });
  assert.ok(bad.some((v) => v.includes('hvac_s6') && v.includes('hvac_<domain>')), bad.join('\n'));

  const good = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_billing', path: 'modules/billing/migrations/001_ledger.sql' }],
    files: {
      'modules/billing/migrations/001_ledger.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '账单域。';
        COMMENT ON TABLE billing.invoices IS '结算电费单。';
      `,
    },
  });
  assert.deepEqual(good, []);
});

test('rejects redundant schema_stem_ table prefix and guides to short table name', () => {
  const bad = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_alarm', path: 'modules/alarm/migrations/001_escalations.sql' }],
    files: {
      'modules/alarm/migrations/001_escalations.sql': `
        CREATE SCHEMA alarm_runtime;
        CREATE TABLE alarm_runtime.alarm_escalations (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA alarm_runtime IS '告警域。';
        COMMENT ON TABLE alarm_runtime.alarm_escalations IS '升级规则。';
      `,
    },
  });
  assert.ok(
    bad.some((v) => v.includes('repeats schema prefix "alarm_"') && v.includes('"alarm_runtime.escalations"')),
    bad.join('\n'),
  );

  const good = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_alarm', path: 'modules/alarm/migrations/001_escalations.sql' }],
    files: {
      'modules/alarm/migrations/001_escalations.sql': `
        CREATE SCHEMA alarm_runtime;
        CREATE TABLE alarm_runtime.escalations (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA alarm_runtime IS '告警域。';
        COMMENT ON TABLE alarm_runtime.escalations IS '升级规则。';
      `,
    },
  });
  assert.deepEqual(good, []);
});

test('enforces live schema and table COMMENT ON coverage and handles cleared comments and dropped tables', () => {
  const missingAndCleared = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_billing', path: 'modules/billing/migrations/001_init.sql' }],
    files: {
      'modules/billing/migrations/001_init.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        CREATE TABLE billing.payments (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '   ';
        COMMENT ON TABLE billing.invoices IS '结算电费单。';
        COMMENT ON TABLE billing.invoices IS NULL;
      `,
    },
  });
  assert.ok(missingAndCleared.some((v) => v.includes('schema "billing" is missing a non-empty COMMENT ON SCHEMA')), missingAndCleared.join('\n'));
  assert.ok(missingAndCleared.some((v) => v.includes('live table "billing.invoices" is missing a non-empty COMMENT ON TABLE')), missingAndCleared.join('\n'));
  assert.ok(missingAndCleared.some((v) => v.includes('live table "billing.payments" is missing a non-empty COMMENT ON TABLE')), missingAndCleared.join('\n'));

  const droppedLater = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [
      { database: 'hvac_billing', path: 'modules/billing/migrations/001_init.sql' },
      { database: 'hvac_billing', path: 'modules/billing/migrations/002_drop_staging.sql' },
    ],
    files: {
      'modules/billing/migrations/001_init.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        CREATE TABLE billing.staging_rows (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '账单域。';
        COMMENT ON TABLE billing.invoices IS '结算账单。';
      `,
      'modules/billing/migrations/002_drop_staging.sql': `
        DROP TABLE IF EXISTS billing.staging_rows;
      `,
    },
  });
  assert.deepEqual(droppedLater, []);
});

test('rejects strict COMMENT ON targeting unknown or dropped schemas and tables without polluting live maps', () => {
  const result = inspectMigrationConventions({
    baseline: emptyBaseline(),
    migrations: [
      { database: 'hvac_billing', path: 'modules/billing/migrations/001_init.sql' },
      { database: 'hvac_billing', path: 'modules/billing/migrations/002_phantom_comments.sql' },
    ],
    files: {
      'modules/billing/migrations/001_init.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        CREATE TABLE billing.staging_rows (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '账单域。';
        COMMENT ON TABLE billing.invoices IS '结算电费单。';
      `,
      'modules/billing/migrations/002_phantom_comments.sql': `
        DROP TABLE billing.staging_rows;
        COMMENT ON SCHEMA unknown_schema IS '幽灵域注释。';
        COMMENT ON TABLE billing.staging_rows IS '已删除表的注释。';
        COMMENT ON TABLE billing.phantom_table IS '不存在表的注释。';
      `,
    },
  });

  assert.ok(
    result.violations.some((v) => v.includes('COMMENT ON SCHEMA "unknown_schema" targets an unknown or dropped schema')),
    result.violations.join('\n'),
  );
  assert.ok(
    result.violations.some((v) => v.includes('COMMENT ON TABLE "billing.staging_rows" targets an unknown or dropped table')),
    result.violations.join('\n'),
  );
  assert.ok(
    result.violations.some((v) => v.includes('COMMENT ON TABLE "billing.phantom_table" targets an unknown or dropped table')),
    result.violations.join('\n'),
  );
  assert.equal(result.liveSchemas.size, 1);
  assert.equal(result.liveTables.size, 1);
  assert.ok(result.liveSchemas.has('hvac_billing:billing'));
  assert.ok(result.liveTables.has('hvac_billing:billing.invoices'));
});

test('baseline ratchet rejects bloated count or stale paths no longer in migration-list', () => {
  const violations = findMigrationConventionViolations({
    baseline: emptyBaseline({
      maxGrandfatheredMigrations: 0,
      grandfatheredMigrations: ['modules/legacy/migrations/001_gone.sql'],
    }),
    migrations: [{ database: 'hvac_billing', path: 'modules/billing/migrations/001_init.sql' }],
    files: {
      'modules/billing/migrations/001_init.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS '账单域。';
        COMMENT ON TABLE billing.invoices IS '结算电费单。';
      `,
    },
  });
  assert.ok(violations.some((v) => v.includes('exceeds maxGrandfatheredMigrations')), violations.join('\n'));
  assert.ok(violations.some((v) => v.includes('no longer registered in migration-list.tsv')), violations.join('\n'));
});

test('enforces Chinese comments, validates CREATE USER and CTAS with options, and permits HVAC temp identifiers', () => {
  const englishOnly = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_billing', path: 'modules/billing/migrations/001_init.sql' }],
    files: {
      'modules/billing/migrations/001_init.sql': `
        CREATE SCHEMA billing;
        CREATE TABLE billing.invoices (id uuid PRIMARY KEY);
        COMMENT ON SCHEMA billing IS 'Billing schema.';
        COMMENT ON TABLE billing.invoices IS 'Customer utility bills.';
      `,
    },
  });
  assert.ok(
    englishOnly.some((v) => v.includes('COMMENT ON SCHEMA "billing" must be written in Chinese')),
    englishOnly.join('\n'),
  );
  assert.ok(
    englishOnly.some((v) => v.includes('COMMENT ON TABLE "billing.invoices" must be written in Chinese')),
    englishOnly.join('\n'),
  );
  assert.ok(englishOnly.some((v) => v.includes('(Chinese description required)')), englishOnly.join('\n'));

  const userAndCtas = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_cooling', path: 'modules/cooling/migrations/001_limits.sql' }],
    files: {
      'modules/cooling/migrations/001_limits.sql': `
        CREATE USER tmp_reader;
        CREATE SCHEMA cooling;
        CREATE TABLE cooling.copy_table WITH (fillfactor = 70) AS SELECT 1 AS x;
        COMMENT ON SCHEMA cooling IS '冷站运行温度域。';
        COMMENT ON TABLE cooling.copy_table IS '冷机复制表。';
      `,
    },
  });
  assert.ok(userAndCtas.some((v) => v.includes('role "tmp_reader"')), userAndCtas.join('\n'));
  assert.ok(userAndCtas.some((v) => v.includes('CREATE TABLE cooling.copy_table AS is forbidden')), userAndCtas.join('\n'));

  const allowedHvacTemp = findMigrationConventionViolations({
    baseline: emptyBaseline(),
    migrations: [{ database: 'hvac_cooling', path: 'modules/cooling/migrations/002_chiller_temp_limits.sql' }],
    files: {
      'modules/cooling/migrations/002_chiller_temp_limits.sql': `
        CREATE SCHEMA cooling;
        CREATE TABLE cooling.supply_temp_bounds (
          site_id uuid PRIMARY KEY,
          max_temp_c double precision GENERATED ALWAYS AS (50.0) STORED
        );
        COMMENT ON SCHEMA cooling IS '冷站供回水温度域。';
        COMMENT ON TABLE cooling.supply_temp_bounds IS '冷站供水温度上下限配置。';
      `,
    },
  });
  assert.deepEqual(allowedHvacTemp, []);
});

test('CLI reports repository migration conventions passing', () => {
  const cli = spawnSync(process.execPath, ['--experimental-strip-types', checkScript], {
    cwd: repoRoot,
    encoding: 'utf8',
  });
  assert.equal(cli.status, 0, cli.stderr || cli.stdout);
  assert.match(cli.stdout, /12 live schemas, 221 live tables commented/);
});
