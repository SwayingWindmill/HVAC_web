// Replays simulated history into the local stack through Telemetry Runtime's Historical Replay path,
// then backfills the Sites' hourly and daily Metrics over the window. Savings verification needs
// weeks of plant history before a measure; a laptop cannot run the simulator that long.
//
//   node scripts/local-history-replay.mjs --from 2026-09-15T00:00:00+08:00 --to 2026-10-06T00:00:00+08:00 \
//     [--commands local-energy-site=tools/eg8200-simulator/configs/replay-setpoint-raise.json] [--metrics-only]
//
// --metrics-only queues the Metric recalculation again without replaying.
//
// Replay only a window that ends before the Site's live history begins: replayed counters start
// fresh, so a window touching live data would put a counter jump into one hour.
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';

import { centralPlantDevices, localSites, siteDeviceId, sqlLiteral } from './central-plant-local-contract.mjs';
import { localContainer, repoRoot, runtimePath } from './lib/local-environment.mjs';

const { values } = parseArgs({
  options: {
    from: { type: 'string' },
    to: { type: 'string' },
    commands: { type: 'string', multiple: true, default: [] },
    'metrics-only': { type: 'boolean', default: false },
  },
});
const from = new Date(values.from ?? '');
const to = new Date(values.to ?? '');
if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || to <= from) throw new Error('--from and --to must be instants in order');
const commandsBySite = new Map(values.commands.map((entry) => {
  const [siteCode, file] = entry.split('=');
  return [siteCode, path.resolve(repoRoot, file)];
}));

const out = path.join(repoRoot, 'out', 'history-replay');
mkdirSync(out, { recursive: true });
const pki = runtimePath('internal-pki');
const identity = path.join(pki, 'historical-replay-runner');

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: repoRoot, stdio: 'inherit', ...options });
  if (result.status !== 0) throw new Error(`${command} ${args[0]} failed with ${result.status}`);
}

// The replay runner presents its own workload identity, issued by the local CA like every other.
function ensureReplayIdentity() {
  if (existsSync(path.join(identity, 'tls.crt'))) return;
  mkdirSync(identity, { recursive: true, mode: 0o750 });
  writeFileSync(path.join(identity, 'extensions.cnf'), [
    'basicConstraints=critical,CA:FALSE',
    'keyUsage=critical,digitalSignature',
    'extendedKeyUsage=clientAuth',
    'subjectAltName=URI:spiffe://hvac.local/historical-replay-runner',
    '',
  ].join('\n'), { mode: 0o640 });
  const file = (name) => path.join(identity, name);
  execFileSync('openssl', ['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', file('tls.key')]);
  execFileSync('chmod', ['640', file('tls.key')]);
  execFileSync('openssl', ['req', '-new', '-key', file('tls.key'), '-subj', '/CN=historical-replay-runner', '-out', file('request.csr')]);
  execFileSync('openssl', ['x509', '-req', '-in', file('request.csr'), '-CA', path.join(pki, 'ca.crt'), '-CAkey', path.join(pki, 'ca.key'),
    '-CAserial', path.join(pki, 'ca.srl'), '-days', '365', '-sha256', '-extfile', file('extensions.cnf'), '-out', file('tls.crt')], { stdio: 'ignore' });
  console.log('Issued the historical-replay-runner workload identity.');
}

// A deterministic UUIDv7 per Site and window, so a rerun replays into the same dataset.
function datasetId(site) {
  const stamp = from.getTime().toString(16).padStart(12, '0');
  return `${stamp.slice(0, 8)}-${stamp.slice(8, 12)}-7000-8000-${String(site.index + 1).padStart(12, '0')}`;
}

if (!values['metrics-only']) replay();

function replay() {
  ensureReplayIdentity();
  run(process.execPath, ['scripts/run-go.mjs', 'build', '-o', path.join(out, 'eg8200-history-replay'), './tools/eg8200-simulator/cmd/eg8200-history-replay'],
    { env: { ...process.env, CGO_ENABLED: '0', GOOS: 'linux' } });

  const points = ['METER-HVAC-TOTAL/hvac_meter.energy', 'BTU-METER-01/btu_meter.accumulated_cooling_energy'].join(',');
  for (const site of localSites) {
    const deviceIds = Object.fromEntries(centralPlantDevices.map((device) => [device.name, siteDeviceId(site, device.platformDeviceId)]));
    writeFileSync(path.join(out, `${site.siteCode}.device-ids.json`), `${JSON.stringify(deviceIds, null, 2)}\n`);
    const commands = commandsBySite.get(site.siteCode);
    const scenario = site.index === 0 ? 'scenario-daily-a.json' : 'scenario-daily-b.json';
    console.log(`Replaying ${site.siteName} from ${from.toISOString()} to ${to.toISOString()}${commands ? ` with ${path.basename(commands)}` : ''}`);
    run('docker', [
      'run', '--rm', '--network', 'hvac-local_application', '--user', `${process.getuid()}:${process.getgid()}`,
      '-v', `${out}:/replay:ro`, '-v', `${path.join(repoRoot, 'tools/eg8200-simulator/configs')}:/configs:ro`, '-v', `${pki}:/pki:ro`,
      ...(commands ? ['-v', `${commands}:/commands.json:ro`] : []),
      '-e', `EG8200_SCENARIO=/configs/${scenario}`,
      '--entrypoint', '/replay/eg8200-history-replay', 'hvac/eg8200-simulator:0.1.0-dev',
      '-plant-config', '/configs/central-plant.local.json', '-device-ids', `/replay/${site.siteCode}.device-ids.json`,
      '-telemetry-url', 'https://telemetry-runtime-service:8446', '-server-name', 'telemetry-runtime-service',
      '-tls-cert', '/pki/historical-replay-runner/tls.crt', '-tls-key', '/pki/historical-replay-runner/tls.key', '-server-ca', '/pki/ca.crt',
      '-dataset-id', datasetId(site), '-from', from.toISOString(), '-duration', `${(to.getTime() - from.getTime()) / 1000}s`,
      '-step', '1m', '-points', points, ...(commands ? ['-commands', '/commands.json'] : []),
    ]);
  }
}

// Each released hourly and daily Metric of each Site is recomputed over the window, one job per
// period. Replayed history is late data for those periods, so the jobs are recalculations, which
// metric-worker runs in parallel; backfills run one at a time and would take hours here.
const backfill = localSites.map((site) => `
WITH bindings AS (
  SELECT id, time_granularity FROM core_registry.metric_bindings
  WHERE site_id = ${sqlLiteral(site.siteId)} AND status = 'RELEASED' AND time_granularity IN ('HOUR', 'DAY')
), periods AS (
  SELECT bindings.id AS binding_id, period_start,
         period_start + CASE WHEN bindings.time_granularity = 'HOUR' THEN interval '1 hour' ELSE interval '1 day' END AS period_end,
         'replay-recalc-' || bindings.id || '-' || to_char(period_start AT TIME ZONE 'UTC', 'YYYYMMDDHH24') AS dedup
  FROM bindings CROSS JOIN LATERAL generate_series(
    CASE WHEN bindings.time_granularity = 'HOUR' THEN date_trunc('hour', ${sqlLiteral(from.toISOString())}::timestamptz)
         ELSE (date_trunc('day', ${sqlLiteral(from.toISOString())}::timestamptz AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai') END,
    ${sqlLiteral(to.toISOString())}::timestamptz - CASE WHEN bindings.time_granularity = 'HOUR' THEN interval '1 hour' ELSE interval '1 day' END,
    CASE WHEN bindings.time_granularity = 'HOUR' THEN interval '1 hour' ELSE interval '1 day' END) AS period_start
)
INSERT INTO core_registry.job_instances (job_id, trigger_type, job_type, tenant_id, site_id, scheduled_for, priority, dedup_key, payload, state, max_attempts, timeout_seconds, created_at, updated_at)
SELECT regexp_replace(lpad(to_hex((extract(epoch FROM clock_timestamp()) * 1000)::bigint), 12, '0') || '7' || substr(md5(dedup), 1, 3) || '8' || substr(md5(dedup), 4, 15),
         '^(.{8})(.{4})(.{4})(.{4})(.{12})$', '\\1-\\2-\\3-\\4-\\5')::uuid,
       'BACKFILL', 'METRIC_RECALC', ${sqlLiteral(site.tenantId)}, ${sqlLiteral(site.siteId)}, period_end, 20, dedup,
       jsonb_build_object('bindingId', binding_id, 'periodStart', period_start, 'periodEnd', period_end), 'READY', 3, 60, clock_timestamp(), clock_timestamp()
FROM periods
ON CONFLICT (dedup_key) DO NOTHING;`).join('\n');
run('docker', ['exec', '-i', localContainer('postgres'), 'psql', '-U', 'postgres', '-d', 'hvac_s1', '-v', 'ON_ERROR_STOP=1'], { input: `BEGIN;${backfill}\nCOMMIT;`, stdio: ['pipe', 'inherit', 'inherit'] });
console.log('Queued Metric recalculation over the replayed window.');
