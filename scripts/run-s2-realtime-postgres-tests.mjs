import { spawnSync } from 'node:child_process';

const result = spawnSync(process.execPath, ['scripts/run-s2-telemetry-runtime-postgres-tests.mjs'], {
  cwd: process.cwd(),
  env: {
    ...process.env,
    S2_RUNTIME_TEST_PATTERN: 'TestPostgresRealtime',
    S2_RUNTIME_TICKET_NUMBER: '65',
    S2_RUNTIME_REPORT_PATH: 'out/s2-realtime-backend/realtime-postgres.json',
  },
  encoding: 'utf8',
  windowsHide: true,
});

if (result.stdout) process.stdout.write(result.stdout);
if (result.stderr) process.stderr.write(result.stderr);
if (result.error || result.status !== 0) {
  throw result.error ?? new Error(`S2 realtime PostgreSQL evidence failed with status ${result.status}`);
}
