import { spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile } from 'node:fs/promises';
import { createServer as createTCPServer } from 'node:net';
import { dirname, resolve } from 'node:path';

import { runDockerCompose } from './lib/docker-cli.mjs';

const root = resolve(process.cwd());
const composePath = resolve(root, 'infra/telemetry/compose.yaml');
const projectName = `hvac-s25-redis-${process.pid}`;
const redisContainerName = `${projectName}-latest-redis-1`;
const reportPath = resolve(root, 'out/s25-release-cutover/redis-rebuild.json');
const redisImage = 'redis:7.4.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952';

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', windowsHide: true, ...options });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr?.trim() || result.stdout?.trim() || String(result.status);
    throw new Error(`${command} ${args.join(' ')} failed: ${detail}`);
  }
  return String(result.stdout ?? '').trim();
}

async function availablePort() {
  const server = createTCPServer();
  server.listen({ host: '127.0.0.1', port: 0, exclusive: true });
  await once(server, 'listening');
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('Redis test port allocator failed');
  await new Promise((resolveClose, rejectClose) => server.close((error) => error ? rejectClose(error) : resolveClose()));
  return address.port;
}

const port = await availablePort();
const env = { ...process.env, S2_LATEST_REDIS_HOST_PORT: String(port) };
const compose = (args) => runDockerCompose(run, ['-p', projectName, '-f', composePath, ...args], { env });
const report = { schemaVersion: 1, status: 'failed', redisImage, redisAuthority: false, rebuildSource: 'postgresql-business-state-machine-snapshot' };

try {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
  compose(['up', '-d', 'latest-redis']);
  let ready = false;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const probe = spawnSync('docker', ['exec', redisContainerName, 'redis-cli', 'ping'], { cwd: root, encoding: 'utf8', windowsHide: true });
    if (!probe.error && probe.status === 0 && String(probe.stdout).trim() === 'PONG') {
      ready = true;
      break;
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 250));
  }
  if (!ready) throw new Error('Redis Latest acceptance container did not become ready');

  const output = run(process.execPath, [
    'scripts/run-isolated-go.mjs',
    '--module=modules/telemetry',
    'test', '-count=1', '-run', 'Test(RedisLatestCacheNeverRegressesBusinessRevision|RebuildLatestCacheRestoresBusinessSnapshots)', '-v', './internal/telemetry/...',
  ], { env: { ...process.env, S2_TELEMETRY_LATEST_REDIS_TEST_URL: `redis://127.0.0.1:${port}/0` } });

  report.status = 'passed';
  report.assertions = {
    redisBusinessRevisionCAS: true,
    tenantDeviceSiteIndex: true,
    rebuildFromAuthoritativeSnapshot: true,
    redisSnapshotIsBusinessAuthority: false,
  };
  report.testOutput = output;
  await mkdir(dirname(reportPath), { recursive: true });
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S25 Redis Latest rebuild acceptance passed: ${reportPath}`);
} finally {
  try { compose(['down', '--volumes', '--remove-orphans']); } catch {}
}
