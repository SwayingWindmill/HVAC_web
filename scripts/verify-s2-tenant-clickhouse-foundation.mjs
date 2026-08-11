import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(process.cwd());
const postgresBin = '/usr/lib/postgresql/16/bin';
const initdb = join(postgresBin, 'initdb');
const postgres = join(postgresBin, 'postgres');
const pgIsReady = join(postgresBin, 'pg_isready');
const psql = join(postgresBin, 'psql');
const goBinary = process.env.GO_BINARY ?? '/home/haozhang/.local/share/go1.25.12/golang.org/toolchain@v0.0.1-go1.25.12.linux-amd64/bin/go';
const initDirectory = resolve(root, 'infra/s2-telemetry/postgres/init');
const clickHouseHistoryPath = resolve(root, 'infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql');
const clickHouseEnergyPath = resolve(root, 'infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: 'utf8',
    stdio: options.capture ? 'pipe' : 'inherit',
    ...options,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed: ${result.error?.message ?? result.stderr?.trim() ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

async function availablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('temporary PostgreSQL port was not allocated');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

function sql(port, database, statement) {
  return run(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', statement], { capture: true });
}

function sqlFailure(port, database, statement) {
  const result = spawnSync(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atqc', statement], {
    cwd: root,
    encoding: 'utf8',
  });
  if (!result.error && result.status === 0) throw new Error(`SQL unexpectedly succeeded: ${statement}`);
  return String(result.stderr ?? '').trim();
}

async function waitReady(port, postgresProcess) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (postgresProcess.exitCode !== null || postgresProcess.signalCode !== null) throw new Error('temporary PostgreSQL exited before readiness');
    const ready = spawnSync(pgIsReady, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', 'postgres'], { stdio: 'ignore' });
    if (ready.status === 0) return;
    await pause(100);
  }
  throw new Error('temporary PostgreSQL did not become ready');
}

function expect(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
}

function requireText(text, marker, label) {
  if (!text.includes(marker)) throw new Error(`${label} is missing ${marker}`);
}

const dataDirectory = await mkdtemp(join(tmpdir(), 'hvac-s2-tenant-'));
const port = await availablePort();
let postgresProcess;

try {
  run(initdb, ['-D', dataDirectory, '-A', 'trust', '-U', 'postgres', '--encoding=UTF8', '--no-locale'], { capture: true });
  postgresProcess = spawn(postgres, ['-D', dataDirectory, '-h', '127.0.0.1', '-p', String(port), '-k', dataDirectory], {
    cwd: root,
    stdio: 'ignore',
  });
  await waitReady(port, postgresProcess);
  sql(port, 'postgres', 'CREATE DATABASE hvac_s2');

  const migrations = (await readdir(initDirectory))
    .filter((name) => name.endsWith('.sql'))
    .sort((left, right) => left.localeCompare(right, 'en'));
  for (const migration of migrations) {
    run(psql, ['-h', '127.0.0.1', '-p', String(port), '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-f', resolve(initDirectory, migration)], { capture: true });
  }

  expect(
    sql(port, 'hvac_s2', "SELECT count(*) FROM telemetry_runtime.registry_device_bindings WHERE tenant_id IS NOT NULL"),
    '4',
    'Tenant-scoped registry device bindings',
  );
  expect(
    sql(port, 'hvac_s2', "SELECT count(*) FROM telemetry_runtime.registry_device_bindings WHERE tenant_id = '018f2d00-0000-7000-8000-000000000002'::uuid"),
    '1',
    'Tenant B registry binding isolation fixture',
  );
  expect(
    sql(port, 'hvac_s2', 'SELECT count(*) FROM telemetry_runtime.iam_scope_projections WHERE tenant_id IS NOT NULL'),
    '4',
    'Tenant-scoped IAM projections',
  );
  expect(
    sql(port, 'hvac_s2', 'SELECT count(*) FROM telemetry_runtime.registry_point_bindings WHERE tenant_id IS NOT NULL AND point_id IS NOT NULL'),
    '4',
    'Tenant-scoped Point projections',
  );
  expect(
    sql(port, 'hvac_s2', "SELECT count(*) FROM telemetry_runtime.source_observations WHERE acceptance_status = 'ACCEPTED' AND tenant_id IS NOT NULL AND point_id IS NOT NULL"),
    '1',
    'Accepted observation Tenant and Point identity',
  );

  const mappedWithoutTenant = sqlFailure(port, 'hvac_s2', `
    INSERT INTO telemetry_runtime.source_observations (
      observation_id, tenant_id, integration_instance_id, source_event_id, source_partition,
      source_offset, source_path, device_id, point_id, sensor_id, telemetry_key, value, value_type, unit,
      sampled_at, received_at, acceptance_status, quality, quality_reasons, payload_sha256, created_at
    ) VALUES (
      '018f2e00-8100-7000-8000-000000000099', NULL,
      '018f2e00-6000-7000-8000-000000000001', '018f2e00-8000-7000-8000-000000000099',
      'tenant-negative', 99, 'WEBHOOK', '018f2e00-3000-7000-8000-000000000001',
      '018f2e00-3100-7000-8000-000000000001', '018f2e00-3200-7000-8000-000000000001',
      'zone.temperature', '24.0'::jsonb, 'NUMBER', 'Cel', now(), now(), 'ACCEPTED', 'GOOD', '{}',
      'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', now()
    );
  `);
  if (!mappedWithoutTenant.includes('source_observations_mapped_tenant_check')) {
    throw new Error(`mapped observation without Tenant was not rejected by the expected invariant: ${mappedWithoutTenant}`);
  }

  const historyDDL = await readFile(clickHouseHistoryPath, 'utf8');
  for (const marker of [
    'tenant_id Nullable(UUID)',
    'CONSTRAINT accepted_tenant_scope',
    "ifNull(tenant_id, toUUID('00000000-0000-0000-0000-000000000000'))",
    'tenant_id UUID',
    'point_id Nullable(UUID)',
    'sensor_id Nullable(UUID)',
    'ORDER BY (tenant_id, owning_organization_id, site_id, point_id, sensor_id, device_id, telemetry_key, unit, hour)',
    'AND tenant_id IS NOT NULL',
    'AND point_id IS NOT NULL',
  ]) requireText(historyDDL, marker, 'ClickHouse telemetry history DDL');

  const energyDDL = await readFile(clickHouseEnergyPath, 'utf8');
  requireText(energyDDL, 'tenant_id UUID', 'ClickHouse energy fact DDL');
  requireText(energyDDL, 'point_id UUID', 'ClickHouse energy fact DDL');
  requireText(energyDDL, 'sensor_id Nullable(UUID)', 'ClickHouse energy fact DDL');
  requireText(energyDDL, 'tenant_id,\n  organization_id,\n  site_id,\n  point_id,', 'ClickHouse energy fact sort key');

  const runtimeURL = `postgres://s2_telemetry_service@127.0.0.1:${port}/hvac_s2?sslmode=disable`;
  const adminURL = `postgres://postgres@127.0.0.1:${port}/hvac_s2?sslmode=disable`;
  run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/telemetry-runtime-service',
    'test', '-count=1', '-run', 'TestPostgresIngestEndToEnd', '-v', './internal/telemetry/...',
  ], {
    capture: true,
    env: {
      ...process.env,
      GO_BINARY: goBinary,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
    },
  });

  console.log(`S2 Tenant/Point/ClickHouse foundation verification passed on temporary PostgreSQL 16 (${migrations.length} PostgreSQL migrations + runtime ingest integration).`);
} finally {
  if (postgresProcess && postgresProcess.exitCode === null && postgresProcess.signalCode === null) {
    postgresProcess.kill('SIGTERM');
    await Promise.race([once(postgresProcess, 'exit'), pause(3000)]).catch(() => {});
  }
  await rm(dataDirectory, { recursive: true, force: true });
}
