import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, dirname, relative, resolve } from 'node:path';

const root = resolve(process.cwd());
const docker = 'docker';
const suffix = `${process.pid}-${Date.now()}`;
const network = `hvac-phase1-ch-restore-${suffix}`;
const clickhouseContainer = `${network}-clickhouse`;
const clickhouseAlias = 'clickhouse';
const dataVolume = `${network}-data`;
const backupVolume = `${network}-backups`;
const clickhouseImage = process.env.PHASE1_CLICKHOUSE_IMAGE ?? 'clickhouse/clickhouse-server:26.3.39.7';
const productVersion = process.env.PHASE1_PRODUCT_VERSION ?? '0.1.0';
const migrateScriptRel = process.env.PHASE1_CLICKHOUSE_MIGRATE_SCRIPT ?? 'deploy/platform/phase1/clickhouse-migrations/run-clickhouse-migrations.sh';
const verifyScriptRel = process.env.PHASE1_CLICKHOUSE_VERIFY_SCRIPT ?? 'deploy/platform/phase1/clickhouse-migrations/verify-clickhouse-schema.sh';
const reportPath = resolve(root, process.env.PHASE1_CLICKHOUSE_RESTORE_REPORT_PATH ?? 'out/phase1-clickhouse-restore/report.json');

const expectedTables = Object.freeze([
  'analytics.energy_hourly_facts_v2',
  'analytics.energy_hourly_states',
  'analytics.energy_interval_facts',
  'analytics.energy_projection_checkpoints',
  'analytics.energy_rebuild_runs',
  'analytics.forecast_series',
  'analytics.metric_result_facts',
  'analytics.optimization_evaluations',
  'analytics.site_condition_anchors_hourly',
  'analytics.site_total_submeter_syncs',
  'analytics.site_total_submeters',
  'analytics.tariff_price_window_syncs',
  'analytics.tariff_price_windows',
  'telemetry_history.numeric_gauge_15min_states',
  'telemetry_history.numeric_gauge_1min_states',
  'telemetry_history.observations',
]);

const expectedMaterializedViews = Object.freeze([
  'analytics.energy_fact_corrections_to_hourly',
  'analytics.energy_facts_to_hourly',
  'telemetry_history.observations_to_numeric_gauge_15min',
  'telemetry_history.observations_to_numeric_gauge_1min',
]);

const expectedViews = Object.freeze([
  'analytics.energy_hourly',
  'analytics.energy_hourly_v2',
  'analytics.energy_interval_fact_revisions',
  'analytics.energy_interval_facts_visible',
  'analytics.site_condition_anchors_hourly_latest',
  'analytics.site_meter_balance_hourly',
  'analytics.site_total_submeters_visible',
  'analytics.tariff_price_windows_visible',
  'analytics.thermal_cop_asof_audit',
  'telemetry_history.counter_15min',
  'telemetry_history.counter_1min',
  'telemetry_history.counter_arrivals',
  'telemetry_history.counter_deltas',
  'telemetry_history.counter_deltas_from',
  'telemetry_history.counter_hourly',
  'telemetry_history.counter_observations',
  'telemetry_history.numeric_15min',
  'telemetry_history.numeric_1min',
  'telemetry_history.numeric_hourly',
]);

const expectedUsers = Object.freeze([
  'analytics_projector_reader',
  'analytics_projector_writer',
  'cube_analytics_reader',
  'fdd_service_reader',
  'forecast_service_reader',
  'forecast_service_writer',
  'metric_engine_reader',
  'metric_engine_runtime',
  'metric_engine_writer',
  'mv_service_reader',
  'optimization_service_reader',
  'optimization_service_writer',
  'phase1_schema_reader',
  'settlement_reader',
  'telemetry_history_writer',
  'telemetry_query_history_reader',
  'telemetry_retention_writer',
]);

type CommandOptions = {
  input?: string;
};

type SchemaSnapshot = {
  tables: string[];
  materializedViews: string[];
  views: string[];
  observationCount: number;
  energyIntervalFactCount: number;
  numericGauge1MinStatesCount: number;
  energyHourlyStatesCount: number;
  observationsDigest: string;
  energyFactsDigest: string;
};

type RestoreReport = {
  schemaVersion: number;
  capability: string;
  status: 'passed' | 'failed';
  startedAt: string;
  finishedAt?: string;
  clickHouseImage: string;
  isolatedEnvironment: boolean;
  assertions: Record<string, unknown>;
  error?: string;
};

function execute(command: string, args: string[], options: CommandOptions = {}) {
  return spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    input: options.input,
  });
}

function run(command: string, args: string[], options: CommandOptions = {}): string {
  const result = execute(command, args, options);
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function runMustFail(command: string, args: string[], expectedPattern: string | RegExp, options: CommandOptions = {}): string {
  const result = execute(command, args, options);
  if (!result.error && result.status === 0) {
    throw new Error(`${command} ${args.join(' ')} unexpectedly succeeded`);
  }
  const detail = `${result.stderr ?? ''}\n${result.stdout ?? ''}`.trim();
  const matched = typeof expectedPattern === 'string'
    ? detail.includes(expectedPattern)
    : expectedPattern.test(detail);
  if (!matched) {
    throw new Error(`expected failure matching ${String(expectedPattern)}, got: ${detail.slice(-1600)}`);
  }
  return detail.slice(-1600);
}

function clickhouseExec(sql: string, options: CommandOptions = {}): string {
  return run(
    docker,
    [
      'exec',
      ...(options.input !== undefined ? ['-i'] : []),
      clickhouseContainer,
      'clickhouse-client',
      '--host', '127.0.0.1',
      '--port', '9000',
      '--user', 'default',
      '--query', sql,
    ],
    options,
  );
}

function clickhouseExecMustFail(sql: string, expectedPattern: string | RegExp): string {
  return runMustFail(
    docker,
    [
      'exec',
      clickhouseContainer,
      'clickhouse-client',
      '--host', '127.0.0.1',
      '--port', '9000',
      '--user', 'default',
      '--query', sql,
    ],
    expectedPattern,
  );
}

async function waitForClickHouse(): Promise<void> {
  let stableChecks = 0;
  for (let attempt = 0; attempt < 240; attempt += 1) {
    const probe = execute(docker, [
      'exec',
      clickhouseContainer,
      'clickhouse-client',
      '--host', '127.0.0.1',
      '--port', '9000',
      '--user', 'default',
      '--query', 'SELECT 1',
    ]);
    if (!probe.error && probe.status === 0 && String(probe.stdout ?? '').trim() === '1') {
      stableChecks += 1;
      if (stableChecks >= 3) return;
    } else {
      stableChecks = 0;
    }
    await new Promise<void>((resolveWait) => setTimeout(resolveWait, 250));
  }
  let logs = '';
  try {
    logs = run(docker, ['logs', '--tail', '100', clickhouseContainer]);
  } catch (error) {
    logs = String(error);
  }
  throw new Error(`isolated ClickHouse did not become ready\n${logs}`);
}

function isWithinRoot(filePath: string): boolean {
  const rel = relative(root, filePath);
  return rel !== '' && !rel.startsWith('..') && !rel.startsWith('/') && !rel.includes(':');
}

function buildScriptRunnerArgs(
  networkMode: string,
  scriptHostPath: string,
  envVars: Record<string, string>,
): string[] {
  const migrationsDir = dirname(scriptHostPath);
  const containerScriptPath = isWithinRoot(scriptHostPath)
    ? `/opt/hvac/repo/${relative(root, scriptHostPath).replaceAll('\\', '/')}`
    : `/opt/hvac/clickhouse-migrations/${basename(scriptHostPath)}`;
  const args = [
    'run', '--rm',
    '--network', networkMode,
    '--mount', `type=bind,src=${root},dst=/opt/hvac/repo,readonly`,
    '--mount', `type=bind,src=${migrationsDir},dst=/opt/hvac/clickhouse-migrations,readonly`,
    '-w', '/opt/hvac/repo',
  ];
  for (const [key, value] of Object.entries(envVars)) {
    args.push('-e', `${key}=${value}`);
  }
  args.push('--entrypoint', '/bin/bash', clickhouseImage, containerScriptPath);
  return args;
}

function migratorArgs(migrateScriptAbs: string): string[] {
  return buildScriptRunnerArgs(`container:${clickhouseContainer}`, migrateScriptAbs, {
    CLICKHOUSE_HOST: '127.0.0.1',
    CLICKHOUSE_PORT: '9000',
    CLICKHOUSE_USER: 'default',
    PHASE1_PRODUCT_VERSION: productVersion,
    PHASE1_RELEASE_REVISION: 'clickhouse-restore-drill',
  });
}

function verifierArgs(verifyScriptAbs: string, version: string): string[] {
  return buildScriptRunnerArgs(network, verifyScriptAbs, {
    CLICKHOUSE_HOST: clickhouseAlias,
    CLICKHOUSE_PORT: '9000',
    CLICKHOUSE_USER: 'phase1_schema_reader',
    PHASE1_PRODUCT_VERSION: version,
  });
}

function escapeSqlString(value: string): string {
  return value.replaceAll('\\', '\\\\').replaceAll("'", "\\'");
}

function readWritableMigrationColumns(): string[] {
  const raw = clickhouseExec('DESCRIBE TABLE phase1_deployment.schema_migrations FORMAT JSONEachRow');
  const columns: string[] = [];
  for (const line of raw.split(/\r?\n/u).filter(Boolean)) {
    const parsed = JSON.parse(line) as { name?: string; default_type?: string };
    if (!parsed.name) continue;
    if (parsed.default_type === 'MATERIALIZED' || parsed.default_type === 'ALIAS') continue;
    columns.push(parsed.name);
  }
  if (!columns.includes('migration_path') || !columns.includes('sha256') || !columns.includes('status') || !columns.includes('version')) {
    throw new Error(`phase1_deployment.schema_migrations lacks required tracking columns: ${columns.join(', ')}`);
  }
  return columns;
}

function readLatestMigrationRow(migrationPath: string): Record<string, unknown> {
  const raw = clickhouseExec(
    `SELECT * FROM phase1_deployment.schema_migrations WHERE migration_path = '${escapeSqlString(migrationPath)}' ORDER BY version DESC LIMIT 1 FORMAT JSONEachRow`,
  );
  const firstLine = raw.split(/\r?\n/u).find(Boolean);
  if (!firstLine) {
    throw new Error(`migration tracking row not found for ${migrationPath}`);
  }
  return JSON.parse(firstLine) as Record<string, unknown>;
}

function insertMigrationRow(writableColumns: readonly string[], row: Record<string, unknown>): void {
  const filtered: Record<string, unknown> = {};
  for (const col of writableColumns) {
    if (Object.hasOwn(row, col)) {
      filtered[col] = row[col];
    }
  }
  clickhouseExec('INSERT INTO phase1_deployment.schema_migrations FORMAT JSONEachRow', {
    input: `${JSON.stringify(filtered)}\n`,
  });
}

function formatUtcDateTime64(date: Date): string {
  return date.toISOString().replace('T', ' ').replace('Z', '');
}

function captureSnapshot(): SchemaSnapshot {
  const rawObjects = clickhouseExec(
    "SELECT database || '.' || name || '|' || engine FROM system.tables WHERE database IN ('telemetry_history', 'analytics') ORDER BY database, name FORMAT TSVRaw",
  );
  const tables: string[] = [];
  const materializedViews: string[] = [];
  const views: string[] = [];
  for (const line of rawObjects.split(/\r?\n/u).filter(Boolean)) {
    const [fullName, engine] = line.split('|');
    if (!fullName || !engine) continue;
    if (engine === 'MaterializedView') materializedViews.push(fullName);
    else if (engine === 'View') views.push(fullName);
    else tables.push(fullName);
  }

  return {
    tables,
    materializedViews,
    views,
    observationCount: Number(clickhouseExec('SELECT count() FROM telemetry_history.observations FORMAT TSVRaw')),
    energyIntervalFactCount: Number(clickhouseExec('SELECT count() FROM analytics.energy_interval_facts FORMAT TSVRaw')),
    numericGauge1MinStatesCount: Number(clickhouseExec('SELECT count() FROM telemetry_history.numeric_gauge_1min_states FORMAT TSVRaw')),
    energyHourlyStatesCount: Number(clickhouseExec('SELECT count() FROM analytics.energy_hourly_states FORMAT TSVRaw')),
    observationsDigest: clickhouseExec(
      "SELECT toString(observation_id) || '|' || ifNull(toString(tenant_id), '') || '|' || ifNull(toString(site_id), '') || '|' || ifNull(toString(device_id), '') || '|' || ifNull(toString(point_id), '') || '|' || telemetry_key || '|' || ifNull(toString(value_number), '') || '|' || toString(payload_sha256) || '|' || toString(history_sequence) FROM telemetry_history.observations ORDER BY observation_id FORMAT TSVRaw",
    ),
    energyFactsDigest: clickhouseExec(
      "SELECT toString(fact_id) || '|' || toString(tenant_id) || '|' || toString(site_id) || '|' || toString(meter_id) || '|' || telemetry_key || '|' || toString(energy_kwh) || '|' || toString(fact_revision) FROM analytics.energy_interval_facts ORDER BY fact_id FORMAT TSVRaw",
    ),
  };
}

function assertExpectedSchemaObjects(snapshot: SchemaSnapshot): void {
  for (const name of expectedTables) {
    if (!snapshot.tables.includes(name)) {
      throw new Error(`missing expected ClickHouse table after migration: ${name}`);
    }
  }
  for (const name of expectedMaterializedViews) {
    if (!snapshot.materializedViews.includes(name)) {
      throw new Error(`missing expected ClickHouse materialized view after migration: ${name}`);
    }
  }
  for (const name of expectedViews) {
    if (!snapshot.views.includes(name)) {
      throw new Error(`missing expected ClickHouse view after migration: ${name}`);
    }
  }
  const actualUsers = new Set(
    clickhouseExec('SELECT name FROM system.users ORDER BY name FORMAT TSVRaw').split(/\r?\n/u).filter(Boolean),
  );
  for (const user of expectedUsers) {
    if (!actualUsers.has(user)) {
      throw new Error(`missing expected ClickHouse user after migration: ${user}`);
    }
  }
}

const report: RestoreReport = {
  schemaVersion: 1,
  capability: 'phase1-clickhouse-migration-and-backup-restore',
  status: 'failed',
  startedAt: new Date().toISOString(),
  clickHouseImage: clickhouseImage,
  isolatedEnvironment: true,
  assertions: {},
};

try {
  const migrateScriptAbs = resolve(root, migrateScriptRel);
  const verifyScriptAbs = resolve(root, verifyScriptRel);
  const backupScriptAbs = resolve(root, 'deploy/platform/phase1/backup/clickhouse-backup.sh');
  const xmlConfigs = [
    ['deploy/platform/phase1/backup/clickhouse-backup.xml', '/etc/clickhouse-server/config.d/backup.xml'],
    ['deploy/platform/phase1/clickhouse/default-security.xml', '/etc/clickhouse-server/users.d/default-security.xml'],
    ['deploy/platform/phase1/clickhouse/resource-limits.xml', '/etc/clickhouse-server/config.d/resource-limits.xml'],
    ['deploy/platform/phase1/clickhouse/system-logs.xml', '/etc/clickhouse-server/config.d/system-logs.xml'],
    ['deploy/platform/phase1/clickhouse/query-log-profile.xml', '/etc/clickhouse-server/users.d/query-log-profile.xml'],
  ] as const;

  for (const filePath of [migrateScriptAbs, verifyScriptAbs, backupScriptAbs, ...xmlConfigs.map(([rel]) => resolve(root, rel))]) {
    if (!existsSync(filePath)) {
      throw new Error(`required ClickHouse migration or config artifact is missing: ${filePath}`);
    }
  }

  run(docker, ['version']);
  const imageProbe = execute(docker, ['image', 'inspect', clickhouseImage]);
  if (imageProbe.status !== 0) {
    run(docker, ['pull', clickhouseImage]);
  }

  try { run(docker, ['rm', '-f', clickhouseContainer]); } catch {}
  try { run(docker, ['network', 'rm', network]); } catch {}
  try { run(docker, ['volume', 'rm', '-f', dataVolume, backupVolume]); } catch {}

  run(docker, ['network', 'create', '--driver', 'bridge', network]);
  run(docker, ['volume', 'create', dataVolume]);
  run(docker, ['volume', 'create', backupVolume]);

  const serverArgs = [
    'run', '-d',
    '--name', clickhouseContainer,
    '--network', network,
    '--network-alias', clickhouseAlias,
    '-e', 'TZ=Asia/Shanghai',
    '-e', `CLICKHOUSE_SERVER_MEMORY_USAGE=${process.env.CLICKHOUSE_SERVER_MEMORY_USAGE ?? '2147483648'}`,
    '--mount', `type=volume,src=${dataVolume},dst=/var/lib/clickhouse`,
    '--mount', `type=volume,src=${backupVolume},dst=/var/lib/clickhouse/backups`,
  ];
  for (const [hostRel, containerDst] of xmlConfigs) {
    serverArgs.push('--mount', `type=bind,src=${resolve(root, hostRel)},dst=${containerDst},readonly`);
  }
  serverArgs.push(clickhouseImage);
  run(docker, serverArgs);
  await waitForClickHouse();

  // (a) Initial versioned migration via container loopback (127.0.0.1:9000) as default.
  run(docker, migratorArgs(migrateScriptAbs));

  const appliedCount = Number(
    clickhouseExec("SELECT count() FROM phase1_deployment.schema_migrations FINAL WHERE status = 'APPLIED' FORMAT TSVRaw"),
  );
  const incompleteCount = Number(
    clickhouseExec("SELECT count() FROM phase1_deployment.schema_migrations FINAL WHERE status != 'APPLIED' FORMAT TSVRaw"),
  );
  if (appliedCount < 23 || incompleteCount !== 0) {
    throw new Error(`unexpected ClickHouse migration tracking state: applied=${appliedCount} incomplete=${incompleteCount}`);
  }
  const recordedProductVersion = clickhouseExec(
    "SELECT product_version FROM phase1_deployment.product_schema FINAL WHERE product = 'hvac-web' FORMAT TSVRaw",
  );
  if (recordedProductVersion !== productVersion) {
    throw new Error(`ClickHouse product_schema version mismatch: expected=${productVersion} actual=${recordedProductVersion}`);
  }

  const initialSnapshot = captureSnapshot();
  assertExpectedSchemaObjects(initialSnapshot);

  // (b) Idempotent second run of run-clickhouse-migrations.sh.
  run(docker, migratorArgs(migrateScriptAbs));
  const rerunAppliedCount = Number(
    clickhouseExec("SELECT count() FROM phase1_deployment.schema_migrations FINAL WHERE status = 'APPLIED' FORMAT TSVRaw"),
  );
  if (rerunAppliedCount !== appliedCount) {
    throw new Error(`idempotent migration rerun changed applied count: before=${appliedCount} after=${rerunAppliedCount}`);
  }

  // (c) Higher-version APPLYING state blocks retry until operator recovery.
  const targetMigrationPath = clickhouseExec(
    'SELECT migration_path FROM phase1_deployment.schema_migrations FINAL ORDER BY migration_path LIMIT 1 FORMAT TSVRaw',
  );
  const writableColumns = readWritableMigrationColumns();
  const baseRow = readLatestMigrationRow(targetMigrationPath);
  const baseVersion = BigInt(String(baseRow.version ?? '0'));

  insertMigrationRow(writableColumns, { ...baseRow, status: 'APPLYING', version: (baseVersion + 1n).toString() });
  const incompleteFailure = runMustFail(
    docker,
    migratorArgs(migrateScriptAbs),
    'operator recovery is required before retry',
  );
  insertMigrationRow(writableColumns, { ...baseRow, status: 'APPLIED', version: (baseVersion + 2n).toString() });
  run(docker, migratorArgs(migrateScriptAbs));

  // (d) Tampered sha256 triggers migration drift detection until restored.
  const driftBaseRow = readLatestMigrationRow(targetMigrationPath);
  const driftBaseVersion = BigInt(String(driftBaseRow.version ?? '0'));
  const originalSha256 = String(driftBaseRow.sha256 ?? '');
  const tamperedSha256 = originalSha256 === '0'.repeat(64) ? '1'.repeat(64) : '0'.repeat(64);

  insertMigrationRow(writableColumns, {
    ...driftBaseRow,
    sha256: tamperedSha256,
    status: 'APPLIED',
    version: (driftBaseVersion + 1n).toString(),
  });
  const driftFailure = runMustFail(
    docker,
    migratorArgs(migrateScriptAbs),
    'ClickHouse migration drift detected',
  );
  insertMigrationRow(writableColumns, {
    ...driftBaseRow,
    sha256: originalSha256,
    status: 'APPLIED',
    version: (driftBaseVersion + 2n).toString(),
  });
  run(docker, migratorArgs(migrateScriptAbs));

  // (e) Peer container security boundary proof + phase1_schema_reader preflight checks.
  const peerDefaultRejected = runMustFail(
    docker,
    [
      'run', '--rm',
      '--network', network,
      '--entrypoint', 'clickhouse-client',
      clickhouseImage,
      '--host', clickhouseAlias,
      '--port', '9000',
      '--user', 'default',
      '--query', 'SELECT 1',
    ],
    /Authentication failed|not allowed|AUTHENTICATION_FAILED|ACCESS_DENIED|Code:\s*(?:516|497|192)/i,
  );
  run(docker, verifierArgs(verifyScriptAbs, productVersion));
  const incompatiblePreflightFailure = runMustFail(
    docker,
    verifierArgs(verifyScriptAbs, '0.0.0-incompatible'),
    /mismatch/i,
  );

  // (f) Deterministic sample rows honoring 001/002 non-null, UUID, SHA-256, and scope CHECK constraints.
  const sampleBaseMs = Math.floor((Date.now() - 3600_000) / 60_000) * 60_000;
  const t0 = formatUtcDateTime64(new Date(sampleBaseMs));
  const t1 = formatUtcDateTime64(new Date(sampleBaseMs + 15_000));
  const tStart = formatUtcDateTime64(new Date(sampleBaseMs - 900_000));
  const tenantId = '019a0000-1000-7000-8000-000000000001';
  const siteId = '019a0000-2000-7000-8000-000000000001';
  const deviceId = '019a0000-3000-7000-8000-000000000001';
  const pointId = '019a0000-4000-7000-8000-000000000001';
  const meterId = '019a0000-5000-7000-8000-000000000001';
  const meterBindingId = '019a0000-6000-7000-8000-000000000001';
  const topologyVersionId = '019a0000-7000-7000-8000-000000000001';
  const energyTypeId = '019a0000-8000-7000-8000-000000000001';

  clickhouseExec(`
    INSERT INTO telemetry_history.observations (
      observation_id, tenant_id, site_id, device_id, point_id, sensor_id,
      source_id, source_event_id, source_partition, source_offset, source_path,
      telemetry_key, point_type, point_revision, counter_decrease_mode, counter_rollover_modulus,
      value_type, unit, value_json, value_number, value_string, value_boolean,
      sampled_at, received_at, acceptance_status, quality, quality_reasons,
      payload_sha256, projected_at, history_sequence
    ) VALUES
      (
        toUUID('019a0000-0001-7000-8000-000000000001'), toUUID('${tenantId}'), toUUID('${siteId}'),
        toUUID('${deviceId}'), toUUID('${pointId}'), NULL,
        'restore-drill-gateway', toUUID('019a0000-9001-7000-8000-000000000001'), 'mqtt-live:0', 1, 'PUSH',
        'chw_supply_temp', 'TELEMETRY', 1, NULL, NULL,
        'NUMBER', 'Cel', NULL, 7.2, NULL, NULL,
        toDateTime64('${t0}', 3, 'UTC'), toDateTime64('${t0}', 3, 'UTC'), 'ACCEPTED', 'GOOD', [],
        repeat('a', 64), toDateTime64('${t0}', 3, 'UTC'), 1
      ),
      (
        toUUID('019a0000-0001-7000-8000-000000000002'), toUUID('${tenantId}'), toUUID('${siteId}'),
        toUUID('${deviceId}'), toUUID('${pointId}'), NULL,
        'restore-drill-gateway', toUUID('019a0000-9001-7000-8000-000000000002'), 'mqtt-live:0', 2, 'PUSH',
        'chw_supply_temp', 'TELEMETRY', 1, NULL, NULL,
        'NUMBER', 'Cel', NULL, 7.4, NULL, NULL,
        toDateTime64('${t1}', 3, 'UTC'), toDateTime64('${t1}', 3, 'UTC'), 'ACCEPTED', 'GOOD', [],
        repeat('b', 64), toDateTime64('${t1}', 3, 'UTC'), 2
      )
  `);

  clickhouseExec(`
    INSERT INTO analytics.energy_interval_facts (
      fact_id, tenant_id, site_id, meter_id, meter_binding_id, topology_version_id, binding_version,
      energy_type_id, meter_role, direction, accounting_scope, load_device_id, device_id, point_id,
      sensor_id, telemetry_key, energy_type, point_revision, unit, counter_decrease_mode,
      counter_rollover_modulus, period_start, period_end, energy_kwh, transition_type, quality,
      quality_reasons, source_previous_observation_id, source_current_observation_id,
      source_event_id, source_partition, source_offset, dataset_revision, data_watermark,
      projected_at, fact_revision, rebuild_run_id
    ) VALUES
      (
        toUUID('019a0000-0002-7000-8000-000000000001'), toUUID('${tenantId}'), toUUID('${siteId}'),
        toUUID('${meterId}'), toUUID('${meterBindingId}'), toUUID('${topologyVersionId}'), 1,
        toUUID('${energyTypeId}'), 'TOTAL_INCOMER', 'IMPORT', 'SITE_TOTAL', NULL, toUUID('${deviceId}'), toUUID('${pointId}'),
        NULL, 'active_energy_kwh', 'ELECTRICITY', 1, 'kWh', 'NONE',
        NULL, toDateTime64('${tStart}', 3, 'UTC'), toDateTime64('${t0}', 3, 'UTC'), 12.5, 'INCREASE', 'GOOD',
        [], toUUID('019a0000-0001-7000-8000-000000000001'), toUUID('019a0000-0001-7000-8000-000000000002'),
        toUUID('019a0000-9002-7000-8000-000000000001'), 'mqtt-live:0', 1, 1, toDateTime64('${t0}', 3, 'UTC'),
        toDateTime64('${t0}', 3, 'UTC'), 0, NULL
      ),
      (
        toUUID('019a0000-0002-7000-8000-000000000002'), toUUID('${tenantId}'), toUUID('${siteId}'),
        toUUID('${meterId}'), toUUID('${meterBindingId}'), toUUID('${topologyVersionId}'), 1,
        toUUID('${energyTypeId}'), 'TOTAL_INCOMER', 'IMPORT', 'SITE_TOTAL', NULL, toUUID('${deviceId}'), toUUID('${pointId}'),
        NULL, 'active_energy_kwh', 'ELECTRICITY', 1, 'kWh', 'NONE',
        NULL, toDateTime64('${t0}', 3, 'UTC'), toDateTime64('${t1}', 3, 'UTC'), 13.25, 'INCREASE', 'GOOD',
        [], toUUID('019a0000-0001-7000-8000-000000000002'), toUUID('019a0000-0001-7000-8000-000000000003'),
        toUUID('019a0000-9002-7000-8000-000000000002'), 'mqtt-live:0', 2, 1, toDateTime64('${t1}', 3, 'UTC'),
        toDateTime64('${t1}', 3, 'UTC'), 0, NULL
      )
  `);

  const before = captureSnapshot();
  if (before.observationCount !== 2 || before.energyIntervalFactCount !== 2) {
    throw new Error(`unexpected sample row counts before backup: obs=${before.observationCount} facts=${before.energyIntervalFactCount}`);
  }
  if (before.numericGauge1MinStatesCount < 1 || before.energyHourlyStatesCount < 1) {
    throw new Error(`materialized views did not populate target rollup tables: 1min=${before.numericGauge1MinStatesCount} hourly=${before.energyHourlyStatesCount}`);
  }

  // (g) Run deploy/platform/phase1/backup/clickhouse-backup.sh and verify non-empty backup directory.
  const backupStdout = run(docker, [
    'run', '--rm',
    '--network', `container:${clickhouseContainer}`,
    '--mount', `type=bind,src=${backupScriptAbs},dst=/usr/local/bin/clickhouse-backup,readonly`,
    '-e', 'CLICKHOUSE_HOST=127.0.0.1',
    '--entrypoint', '/bin/bash',
    clickhouseImage,
    '/usr/local/bin/clickhouse-backup',
  ]);
  const backupMatch = /ClickHouse backup completed:\s*(\S+)/u.exec(backupStdout);
  if (!backupMatch?.[1]) {
    throw new Error(`could not parse ClickHouse backup name from output: ${backupStdout}`);
  }
  const backupName = backupMatch[1];
  const backupSampleFile = run(docker, [
    'exec', clickhouseContainer,
    'sh', '-c', `find "/var/lib/clickhouse/backups/${backupName}" -type f | head -n 1`,
  ]);
  if (!backupSampleFile) {
    throw new Error(`ClickHouse backup directory is empty: /var/lib/clickhouse/backups/${backupName}`);
  }

  // (h) Destroy both databases and verify queries fail.
  clickhouseExec('DROP DATABASE telemetry_history SYNC');
  clickhouseExec('DROP DATABASE analytics SYNC');
  clickhouseExecMustFail('SELECT count() FROM telemetry_history.observations', /UNKNOWN_DATABASE|UNKNOWN_TABLE|Code:\s*(?:60|81)/i);
  clickhouseExecMustFail('SELECT count() FROM analytics.energy_interval_facts', /UNKNOWN_DATABASE|UNKNOWN_TABLE|Code:\s*(?:60|81)/i);

  // (i) Restore both databases from Disk('phase1_backups', '<backup_name>') and assert full parity.
  const restoreOutput = clickhouseExec(
    `RESTORE DATABASE telemetry_history, DATABASE analytics FROM Disk('phase1_backups', '${escapeSqlString(backupName)}')`,
  );
  if (!restoreOutput.includes('RESTORED')) {
    throw new Error(`unexpected ClickHouse RESTORE output: ${restoreOutput}`);
  }

  const after = captureSnapshot();
  const tablesPreserved = JSON.stringify(before.tables) === JSON.stringify(after.tables);
  const viewsPreserved = JSON.stringify(before.views) === JSON.stringify(after.views);
  const materializedViewsPreserved = JSON.stringify(before.materializedViews) === JSON.stringify(after.materializedViews);
  const observationCountPreserved = before.observationCount === after.observationCount;
  const energyFactCountPreserved = before.energyIntervalFactCount === after.energyIntervalFactCount;
  const observationsDigestPreserved = before.observationsDigest === after.observationsDigest;
  const energyFactsDigestPreserved = before.energyFactsDigest === after.energyFactsDigest;
  const rollupCountsPreserved =
    before.numericGauge1MinStatesCount === after.numericGauge1MinStatesCount
    && before.energyHourlyStatesCount === after.energyHourlyStatesCount;

  if (
    !tablesPreserved
    || !viewsPreserved
    || !materializedViewsPreserved
    || !observationCountPreserved
    || !energyFactCountPreserved
    || !observationsDigestPreserved
    || !energyFactsDigestPreserved
    || !rollupCountsPreserved
  ) {
    throw new Error(
      `ClickHouse restore mismatch: ${JSON.stringify({
        tablesPreserved,
        viewsPreserved,
        materializedViewsPreserved,
        observationCountPreserved,
        energyFactCountPreserved,
        observationsDigestPreserved,
        energyFactsDigestPreserved,
        rollupCountsPreserved,
        before,
        after,
      })}`,
    );
  }

  report.assertions = {
    appliedMigrationsCount: appliedCount,
    productVersionVerified: recordedProductVersion,
    idempotentRerunPassed: true,
    incompleteMigrationBlocksRetry: Boolean(incompleteFailure),
    digestDriftFailsClosed: Boolean(driftFailure),
    peerDefaultRejected: Boolean(peerDefaultRejected),
    schemaReaderPreflightPassed: true,
    incompatibleProductVersionRejected: Boolean(incompatiblePreflightFailure),
    backupName,
    backupCreatedNonEmpty: true,
    databasesDroppedBeforeRestore: true,
    tablesPreserved,
    viewsPreserved,
    materializedViewsPreserved,
    observationCountPreserved,
    energyFactCountPreserved,
    observationsDigestPreserved,
    energyFactsDigestPreserved,
    rollupCountsPreserved,
    tableCount: after.tables.length,
    viewCount: after.views.length,
    materializedViewCount: after.materializedViews.length,
  };
  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`Phase 1 ClickHouse migration and backup/restore passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  // (j) Always clean up isolated container, network, and volumes.
  try { execute(docker, ['rm', '-f', clickhouseContainer]); } catch {}
  try { execute(docker, ['network', 'rm', network]); } catch {}
  try { execute(docker, ['volume', 'rm', '-f', dataVolume, backupVolume]); } catch {}
}
