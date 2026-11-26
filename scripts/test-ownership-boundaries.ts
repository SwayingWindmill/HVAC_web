import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  checkCrossSchemaSqlInFile,
  checkPeerModuleImportsInFile,
  checkUntypedMapsInFile,
  inspectOwnershipBoundaries,
} from './check-ownership-boundaries.ts';

const withTempWorkspace = (run: (rootDir: string) => void): void => {
  const dir = mkdtempSync(join(tmpdir(), 'hvac-ownership-boundaries-'));
  try {
    run(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
};

const writeGoFile = (rootDir: string, relPath: string, content: string): void => {
  const fullPath = join(rootDir, relPath);
  mkdirSync(join(fullPath, '..'), { recursive: true });
  writeFileSync(fullPath, content, 'utf8');
};

test('Rule 1 catches P1-A cross-schema reads in optimization-service', () => {
  const src = [
    'package optimization',
    '',
    'const latestQuery = `',
    "SELECT to_regclass('telemetry_runtime.latest_accepted_telemetry'),",
    "       has_table_privilege('telemetry_runtime.latest_accepted_telemetry', 'SELECT')",
    'FROM telemetry_runtime.latest_accepted_telemetry lat',
    'JOIN telemetry_runtime.probe_offset_estimates poe ON poe.point_id = lat.point_id',
    '`',
  ].join('\n');

  const violations = checkCrossSchemaSqlInFile(
    'services/optimization-service/internal/optimization/preparation_postgres.go',
    src,
  );

  assert.equal(violations.length, 4);
  assert.deepEqual(
    violations.map((v) => ({ line: v.line, symbol: v.symbol, rule: v.rule })),
    [
      { line: 4, symbol: 'telemetry_runtime.latest_accepted_telemetry', rule: 'cross-schema-sql' },
      { line: 5, symbol: 'telemetry_runtime.latest_accepted_telemetry', rule: 'cross-schema-sql' },
      { line: 6, symbol: 'telemetry_runtime.latest_accepted_telemetry', rule: 'cross-schema-sql' },
      { line: 7, symbol: 'telemetry_runtime.probe_offset_estimates', rule: 'cross-schema-sql' },
    ],
  );
  assert.match(violations[0].message, /services\/optimization-service\/internal\/optimization\/preparation_postgres\.go:4/);
  assert.match(violations[0].message, /telemetry_runtime\.latest_accepted_telemetry/);
  assert.match(violations[0].message, /modules\/telemetry/);
  assert.match(violations[0].message, /telemetry_runtime\.\*_v1/);
});

test('Rule 1 catches P1-B direct core_registry table reads in IAM and allows *_v1 contract views', () => {
  const badIam = [
    'package iam',
    '',
    'const q = "SELECT 1 " +',
    '  "FROM core_registry.site_auto_control_leases l " +',
    '  "JOIN core_registry.devices d ON d.id = l.device_id"',
  ].join('\n');

  const violations = checkCrossSchemaSqlInFile(
    'modules/iam/internal/iam/postgres_command_authorization.go',
    badIam,
  );

  assert.equal(violations.length, 2);
  assert.equal(violations[0].line, 4);
  assert.equal(violations[0].symbol, 'core_registry.site_auto_control_leases');
  assert.match(violations[0].message, /owned by modules\/registry/);
  assert.match(violations[0].message, /core_registry\.\*_v1/);
  assert.equal(violations[1].line, 5);
  assert.equal(violations[1].symbol, 'core_registry.devices');

  const goodIam = [
    'package iam',
    '',
    'const q = `',
    "SELECT to_regclass('core_registry.sites_v1')",
    'FROM core_registry.site_auto_control_leases_v1 l',
    'JOIN core_registry.devices_v1 d ON d.id = l.device_id',
    '`',
  ].join('\n');

  assert.deepEqual(
    checkCrossSchemaSqlInFile('modules/iam/internal/iam/postgres_command_authorization.go', goodIam),
    [],
  );
});

test('Rule 1 rejects cross-schema writes even against foreign *_v1 contract views', () => {
  const writeViews = [
    'package iam',
    '',
    'const badInsert = `INSERT INTO core_registry.sites_v1 (id) VALUES ($1)`',
    'const badUpdate = `UPDATE core_registry.devices_v1 SET name = $1`',
    'const badDelete = `DELETE FROM telemetry_runtime.latest_accepted_telemetry_v1 WHERE id = $1`',
  ].join('\n');

  const violations = checkCrossSchemaSqlInFile('modules/iam/internal/iam/writer.go', writeViews);
  assert.equal(violations.length, 3);
  assert.match(violations[0].message, /\*_v1 contract views are read-only/);
  assert.match(violations[1].message, /\*_v1 contract views are read-only/);
  assert.match(violations[2].message, /\*_v1 contract views are read-only/);
});

test('Rule 1 enforces declared ownership exceptions and table-level analytics rules', () => {
  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'modules/iam/cmd/identity-reconciler/main.go',
      'package main\nconst q = `SELECT id FROM identity.users`',
    ),
    [],
  );
  assert.equal(
    checkCrossSchemaSqlInFile(
      'modules/iam/cmd/identity-reconciler/main.go',
      'package main\nconst q = `UPDATE identity.users SET email = $1`',
    ).length,
    1,
  );
  assert.equal(
    checkCrossSchemaSqlInFile(
      'modules/iam/internal/iam/store.go',
      'package iam\nconst q = `SELECT id FROM identity.users`',
    ).length,
    1,
  );

  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'modules/registry/internal/core/outbox_relay.go',
      'package core\nconst q = `SELECT id FROM iam.tenants`',
    ),
    [],
  );
  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'modules/maintenance/pkg/maintenance/operations.go',
      'package maintenance\nconst q = `SELECT device_id FROM connectivity.gateway_credentials`',
    ),
    [],
  );
  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'modules/audit/internal/audit/store.go',
      'package audit\nconst q = `UPDATE gateway.outbox SET status = $1`',
    ),
    [],
  );
  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'services/notification-service/internal/notify/relay.go',
      'package notify\nconst q = `DELETE FROM alarm_runtime.notification_outbox WHERE id = $1`',
    ),
    [],
  );

  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'services/mv-service/internal/mv/clickhouse.go',
      'package mv\nconst q = `SELECT val FROM analytics.metric_result_facts`',
    ),
    [],
  );
  assert.equal(
    checkCrossSchemaSqlInFile(
      'services/mv-service/internal/mv/clickhouse.go',
      'package mv\nconst q = `SELECT val FROM analytics.energy_interval_facts`',
    ).length,
    1,
  );
  assert.equal(
    checkCrossSchemaSqlInFile(
      'services/mv-service/internal/mv/clickhouse.go',
      'package mv\nconst q = `INSERT INTO analytics.metric_result_facts VALUES ($1)`',
    ).length,
    1,
  );
});

test('Rule 1 ignores SQL table aliases, pg_catalog, non-SQL strings, comments, and tests', () => {
  const benign = [
    'package iam',
    '',
    '// SELECT * FROM core_registry.sites',
    'const spanName = "connectivity.command_dispatch"',
    'const capability = "audit.read"',
    'const sql = `',
    'SELECT s.id, p.relname',
    'FROM iam.sessions s',
    'JOIN pg_catalog.pg_class p ON p.oid = s.oid',
    "WHERE s.event_type = 'core_registry.sites'",
    '`',
  ].join('\n');

  assert.deepEqual(checkCrossSchemaSqlInFile('modules/iam/internal/iam/store.go', benign), []);
  assert.deepEqual(
    checkCrossSchemaSqlInFile(
      'modules/iam/internal/iam/store_test.go',
      'package iam\nconst q = `SELECT * FROM core_registry.sites`',
    ),
    [],
  );
});

test('Rule 2 rejects peer module/service imports and allows telemetryapi and same-unit imports', () => {
  const badImports = [
    'package iam',
    '',
    'import (',
    '  "context"',
    '  "github.com/quanlaihe/hvac-web/modules/registry/internal/core"',
    '  "github.com/quanlaihe/hvac-web/services/optimization-service/pkg/opt"',
    ')',
  ].join('\n');

  const violations = checkPeerModuleImportsInFile('modules/iam/internal/iam/store.go', badImports);
  assert.equal(violations.length, 2);
  assert.deepEqual(
    violations.map((v) => ({ line: v.line, symbol: v.symbol, rule: v.rule })),
    [
      {
        line: 5,
        symbol: 'github.com/quanlaihe/hvac-web/modules/registry/internal/core',
        rule: 'peer-module-import',
      },
      {
        line: 6,
        symbol: 'github.com/quanlaihe/hvac-web/services/optimization-service/pkg/opt',
        rule: 'peer-module-import',
      },
    ],
  );
  assert.equal(
    checkPeerModuleImportsInFile('modules/iam/internal/iam/store_test.go', badImports).length,
    2,
  );

  const allowedImports = [
    'package alarmservice',
    '',
    'import (',
    '  "github.com/quanlaihe/hvac-web/modules/alarm/internal/store"',
    '  "github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"',
    '  "github.com/quanlaihe/hvac-web/libs/auth"',
    ')',
  ].join('\n');

  assert.deepEqual(checkPeerModuleImportsInFile('modules/alarm/pkg/alarmservice/service.go', allowedImports), []);
  assert.deepEqual(
    checkPeerModuleImportsInFile(
      'cmd/energy-api/main.go',
      'package main\nimport "github.com/quanlaihe/hvac-web/modules/registry/internal/core"',
    ),
    [],
  );
});

test('Rule 3 bans map[string]any and map[string]interface{} in optimization-service and intelligencemodel', () => {
  const badOpt = [
    'package optimization',
    '',
    '// map[string]any inside a comment is ignored',
    'const note = "map[string]any inside string is ignored"',
    'type Decision struct {',
    '  Raw map[string]any',
    '  Legacy map[string]interface{}',
    '  Spaced map[string] interface { }',
    '}',
  ].join('\n');

  const optViolations = checkUntypedMapsInFile(
    'services/optimization-service/internal/optimization/service.go',
    badOpt,
  );
  assert.equal(optViolations.length, 3);
  assert.deepEqual(
    optViolations.map((v) => ({ line: v.line, rule: v.rule })),
    [
      { line: 6, rule: 'untyped-map' },
      { line: 7, rule: 'untyped-map' },
      { line: 8, rule: 'untyped-map' },
    ],
  );

  const modelViolations = checkUntypedMapsInFile(
    'libs/intelligencemodel/model.go',
    'package intelligencemodel\ntype Payload struct {\n  Data map[string]any\n}\n',
  );
  assert.equal(modelViolations.length, 1);
  assert.equal(modelViolations[0].line, 3);

  assert.deepEqual(
    checkUntypedMapsInFile('services/optimization-service/internal/optimization/service_test.go', badOpt),
    [],
  );
  assert.deepEqual(
    checkUntypedMapsInFile(
      'services/optimization-service/internal/optimization/typed.go',
      'package optimization\ntype Typed struct {\n  Flags map[string]bool\n}\n',
    ),
    [],
  );
  assert.deepEqual(
    checkUntypedMapsInFile('modules/telemetry/internal/store.go', badOpt),
    [],
  );
});

test('inspectOwnershipBoundaries discovers custom schemas from data-ownership.v1.json and scans temp workspaces', () => {
  withTempWorkspace((rootDir) => {
    mkdirSync(join(rootDir, 'contracts/ownership'), { recursive: true });
    writeFileSync(
      join(rootDir, 'contracts/ownership/data-ownership.v1.json'),
      JSON.stringify({
        resources: [{ kind: 'schema', name: 'custom_billing', writer: 'billing-service' }],
      }),
      'utf8',
    );

    writeGoFile(
      rootDir,
      'modules/iam/internal/iam/good.go',
      'package iam\nimport "github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetryapi"\nconst q = `SELECT id FROM core_registry.sites_v1`\n',
    );
    writeGoFile(
      rootDir,
      'modules/iam/internal/iam/bad.go',
      'package iam\nimport "github.com/quanlaihe/hvac-web/modules/registry/internal/core"\nconst q = `SELECT id FROM custom_billing.invoices`\n',
    );
    writeGoFile(
      rootDir,
      'libs/intelligencemodel/bad.go',
      'package intelligencemodel\ntype X map[string]any\n',
    );

    const result = inspectOwnershipBoundaries(rootDir);
    assert.equal(result.checkedFiles, 3);
    assert.deepEqual(
      result.violations.map((v) => ({ rule: v.rule, file: v.file, symbol: v.symbol })),
      [
        { rule: 'untyped-map', file: 'libs/intelligencemodel/bad.go', symbol: 'map[string]any' },
        { rule: 'cross-schema-sql', file: 'modules/iam/internal/iam/bad.go', symbol: 'custom_billing.invoices' },
        {
          rule: 'peer-module-import',
          file: 'modules/iam/internal/iam/bad.go',
          symbol: 'github.com/quanlaihe/hvac-web/modules/registry/internal/core',
        },
      ],
    );
    assert.match(result.violations[1].message, /owned by billing-service/);
  });
});
