import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/s2-telemetry/compose.yaml');
const projectName = `hvac-s2-ingest-${process.pid}`;
const containerName = `${projectName}-postgres-1`;
const reportPath = resolve(root, process.env.S2_INGEST_REPORT_PATH ?? 'out/s2-telemetry-ingest/telemetry-ingest-postgres.json');
const pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
async function findAvailablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('S2 telemetry ingest PostgreSQL port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const postgresHostPort = await findAvailablePort();
const composeEnvironment = { ...process.env, S2_POSTGRES_HOST_PORT: String(postgresHostPort) };
const runtimeURL = `postgres://s2_telemetry_service:s2-telemetry-runtime-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;
const adminURL = `postgres://postgres:postgres-local-only@127.0.0.1:${postgresHostPort}/hvac_s2?sslmode=disable`;

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

function compose(args) {
  return runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env: composeEnvironment });
}

function psql(sql) {
  return run('docker', ['exec', containerName, 'psql', '-U', 'postgres', '-d', 'hvac_s2', '-v', 'ON_ERROR_STOP=1', '-Atqc', sql]);
}

async function waitForPostgres() {
  let stableStart = '';
  let stableChecks = 0;
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try {
      const state = psql("SELECT pg_postmaster_start_time()::text || '|' || (to_regclass('telemetry_runtime.source_delivery_evidence') IS NOT NULL)::text || '|' || EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='telemetry_runtime' AND table_name='source_observations' AND column_name='source_path')::text || '|' || (to_regclass('telemetry_runtime.telemetry_history_outbox') IS NOT NULL)::text");
      const [startedAt, evidenceReady, sourcePathReady, historyOutboxReady] = state.split('|');
      if (evidenceReady === 'true' && sourcePathReady === 'true' && historyOutboxReady === 'true') {
        if (startedAt === stableStart) stableChecks += 1;
        else {
          stableStart = startedAt;
          stableChecks = 1;
        }
        if (stableChecks >= 4) return;
      } else stableChecks = 0;
    } catch {
      stableChecks = 0;
    }
    await pause(250);
  }
  let logs = '';
  try { logs = compose(['logs', '--no-color', 'postgres']); } catch (error) { logs = String(error); }
  throw new Error(`S2 telemetry ingest PostgreSQL fixture did not initialize\n${logs}`);
}

const report = {
  schemaVersion: 1,
  ticket: 63,
  status: 'failed',
  startedAt: new Date().toISOString(),
  postgresImage: 'postgres:16.4-bookworm@sha256:e62fbf9d3e2b49816a32c400ed2dba83e3b361e6833e624024309c35d334b412',
  assertions: {},
};

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'postgres']);
  await waitForPostgres();

  report.assertions.goIntegration = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=services/telemetry-runtime-service',
    'test', '-count=1', '-run', 'TestPostgresIngestEndToEnd', '-v', './internal/telemetry/...',
  ], {
    env: {
      ...process.env,
      S2_TELEMETRY_TEST_DATABASE_URL: runtimeURL,
      S2_TELEMETRY_ADMIN_DATABASE_URL: adminURL,
    },
  });

  report.assertions.authoritativeState = psql(`
    SELECT s.business_revision::text || '|'
      || (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o WHERE o.device_id = s.device_id AND o.subscription_id IS NULL)::text || '|'
      || (SELECT source_offset FROM telemetry_runtime.source_positions WHERE integration_instance_id = '018f2e00-6000-7000-8000-000000000001'::uuid AND source_partition = 'tb-ticket-04-a')::text || '|'
      || (SELECT value::text FROM telemetry_runtime.latest_accepted_telemetry WHERE device_id = s.device_id AND telemetry_key = 'zone.temperature')
    FROM telemetry_runtime.device_observation_snapshots s
    WHERE s.device_id = '018f2e00-3000-7000-8000-000000000001'::uuid
  `);
  if (report.assertions.authoritativeState !== '8|8|6|26.0') {
    throw new Error(`unexpected ingest authoritative state ${report.assertions.authoritativeState}`);
  }
  report.assertions.deliveryEvidence = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE quality_reason = 'REPLAYED')::text || '|'
      || count(*) FILTER (WHERE quality_reason = 'OUT_OF_ORDER')::text
    FROM telemetry_runtime.source_delivery_evidence
    WHERE source_partition LIKE 'tb-ticket-04%'
  `);
  if (report.assertions.deliveryEvidence !== '4|1|1') {
    throw new Error(`unexpected delivery evidence ${report.assertions.deliveryEvidence}`);
  }
  report.assertions.rejectedAndQuarantined = psql(`
    SELECT count(*) FILTER (WHERE acceptance_status = 'REJECTED')::text || '|'
      || count(*) FILTER (WHERE acceptance_status = 'QUARANTINED')::text || '|'
      || count(*) FILTER (WHERE acceptance_status <> 'ACCEPTED' AND value IS NOT NULL)::text || '|'
      || count(*) FILTER (
        WHERE acceptance_status = 'REJECTED'
          AND quality_reasons = ARRAY['CLOCK_AHEAD']::text[]
          AND sampled_at > received_at + interval '24 hours'
      )::text
    FROM telemetry_runtime.source_observations
    WHERE source_partition LIKE 'tb-ticket-04%'
  `);
  if (report.assertions.rejectedAndQuarantined !== '3|1|0|1') {
    throw new Error(`unexpected rejected/quarantine evidence ${report.assertions.rejectedAndQuarantined}`);
  }
  report.assertions.historyOutbox = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE delivery_state = 'PENDING')::text || '|'
      || count(*) FILTER (WHERE payload ->> 'acceptance_status' <> 'ACCEPTED'
                           AND COALESCE(payload ->> 'value_json', payload ->> 'value_number', payload ->> 'value_string', payload ->> 'value_boolean') IS NOT NULL)::text || '|'
      || count(*) FILTER (WHERE payload ->> 'owning_organization_id' IS NOT NULL
                           AND payload ->> 'site_id' IS NOT NULL
                           AND payload ->> 'device_id' IS NOT NULL)::text
    FROM telemetry_runtime.telemetry_history_outbox
    WHERE payload ->> 'source_partition' LIKE 'tb-ticket-04%'
  `);
  if (report.assertions.historyOutbox !== '9|9|0|8') {
    throw new Error(`unexpected history outbox state ${report.assertions.historyOutbox}`);
  }
  report.assertions.coverageRecovery = psql(`
    SELECT available::text || '|' || source_revision::text || '|' || continuous_since::text
    FROM telemetry_runtime.observation_coverage
    WHERE device_id = '018f2e00-3000-7000-8000-000000000001'::uuid
  `);
  if (report.assertions.coverageRecovery !== 'true|3|2026-07-24 00:22:00+00') {
    throw new Error(`unexpected coverage recovery ${report.assertions.coverageRecovery}`);
  }
  report.assertions.coverageQuarantine = psql(`
    SELECT count(*)::text || '|'
      || count(*) FILTER (WHERE device_id IS NULL AND telemetry_key IS NULL)::text
    FROM telemetry_runtime.ingest_quarantine
    WHERE external_id = 'tb-device-coverage-missing'
      AND evidence ->> 'kind' = 'OBSERVATION_COVERAGE_REPORT'
  `);
  if (report.assertions.coverageQuarantine !== '1|1') {
    throw new Error(`unexpected coverage quarantine ${report.assertions.coverageQuarantine}`);
  }
  report.assertions.relayRetry = psql(`
    SELECT attempts::text || '|' || last_error_code || '|'
      || (SELECT business_revision FROM telemetry_runtime.device_observation_snapshots WHERE device_id = o.device_id)::text
    FROM telemetry_runtime.telemetry_publication_outbox o
    WHERE device_id = '018f2e00-3000-7000-8000-000000000001'::uuid
      AND business_revision = 8 AND subscription_id IS NULL
  `);
  if (report.assertions.relayRetry !== '2|CENTRIFUGO_UNAVAILABLE|8') {
    throw new Error(`unexpected relay retry state ${report.assertions.relayRetry}`);
  }
  report.assertions.twoOrganizationIsolation = psql(`
    SELECT snapshot ->> 'owningOrganizationId'
    FROM telemetry_runtime.device_observation_snapshots
    WHERE device_id = '018f2e00-3000-7000-8000-000000000003'::uuid
  `);
  if (report.assertions.twoOrganizationIsolation !== '018f2e00-0000-7000-8000-000000000002') {
    throw new Error(`unexpected Organization B snapshot ${report.assertions.twoOrganizationIsolation}`);
  }

  run('docker', ['restart', containerName]);
  await waitForPostgres();
  report.assertions.restartDurability = psql(`
    SELECT s.business_revision::text || '|'
      || (SELECT source_offset FROM telemetry_runtime.source_positions WHERE integration_instance_id = '018f2e00-6000-7000-8000-000000000001'::uuid AND source_partition = 'tb-ticket-04-a')::text || '|'
      || (SELECT count(*) FROM telemetry_runtime.telemetry_publication_outbox o WHERE o.device_id = s.device_id AND o.subscription_id IS NULL)::text
    FROM telemetry_runtime.device_observation_snapshots s
    WHERE s.device_id = '018f2e00-3000-7000-8000-000000000001'::uuid
  `);
  if (report.assertions.restartDurability !== '8|6|8') {
    throw new Error(`unexpected post-restart state ${report.assertions.restartDurability}`);
  }

  report.status = 'passed';
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S2 telemetry ingest PostgreSQL evidence passed: ${reportPath}`);
} catch (error) {
  report.error = error instanceof Error ? error.message : String(error);
  report.finishedAt = new Date().toISOString();
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  throw error;
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
