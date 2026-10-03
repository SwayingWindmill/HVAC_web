import { spawnSync } from 'node:child_process';
import { loadAcceptanceRig } from './lib/acceptance-rig.mjs';
import { localContainer } from './lib/local-environment.mjs';

// Drift gate for the live acceptance rig. Criterion "the acceptance profile is explicit
// and repeatable" only holds if the running stack still matches the reviewed profile;
// a partially applied rig is what previously produced a stack that looked live while
// every input was reported STALE, or a session that expired mid-run.
const root = process.cwd();
const profile = await loadAcceptanceRig(root);
const postgresContainer = process.env.PHASE1_POSTGRES_CONTAINER || localContainer('postgres');
const brokerContainer = process.env.PHASE1_MQTT_CONTAINER || localContainer('mqtt-broker');

const failures = [];
const notes = [];

function invariant(condition, message) {
  if (!condition) failures.push(message);
}

function psql(database, sql) {
  const result = spawnSync('docker', ['exec', postgresContainer, 'psql', '-U', 'postgres', '-d', database, '-tAc', sql], {
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error(`psql against ${database} failed: ${result.stderr.trim()}`);
  return result.stdout.trim();
}

function psqlRows(database, sql) {
  return psql(database, sql).split('\n').filter((line) => line.length > 0);
}

// 1. Registry publishes the cadence the rig profile declares.
const registryRows = psqlRows(
  'hvac_s1',
  `SELECT point_code || '|' || publish_interval_ms || '|' || stale_after_ms
     FROM core_registry.telemetry_points
     WHERE status = 'ACTIVE' AND point_type <> 'COMMAND'
       AND reporting_device_id IN (
         SELECT id FROM core_registry.devices WHERE display_name LIKE ANY (ARRAY[${profile.requiredDevices
           .map((device) => `'${device}%'`)
           .join(',')}])
       )`,
);
invariant(registryRows.length > 0, 'no ACTIVE Registry points found for the required Devices');
for (const row of registryRows) {
  const [pointCode, publishMs, staleMs] = row.split('|');
  invariant(
    Number(publishMs) === profile.registry.publishIntervalMs,
    `Registry ${pointCode} publish interval is ${publishMs}ms, rig expects ${profile.registry.publishIntervalMs}ms`,
  );
  invariant(
    Number(staleMs) === profile.registry.staleAfterMs,
    `Registry ${pointCode} stale_after is ${staleMs}ms, rig expects ${profile.registry.staleAfterMs}ms`,
  );
}
notes.push(`registry: ${registryRows.length} required-device points at ${profile.registry.publishIntervalMs}ms/${profile.registry.staleAfterMs}ms`);

// 2. Runtime freshness policies agree with the rig, so inputs are not labelled STALE
//    between two published samples.
const freshnessRows = psqlRows(
  'hvac_s2',
  `SELECT telemetry_key || '|' || fresh_within_seconds || '|' || coalesce(expected_sample_interval_seconds, 0)
     FROM telemetry_runtime.freshness_policies WHERE configured`,
);
invariant(freshnessRows.length > 0, 'no configured runtime freshness policies found');
for (const row of freshnessRows) {
  const [key, fresh, expected] = row.split('|');
  invariant(
    Number(fresh) >= Number(expected),
    `freshness policy ${key} window ${fresh}s is shorter than its ${expected}s sample interval`,
  );
}
// The rig window must be the tightest configured policy, i.e. it really applies to the
// Devices the acceptance evaluates; slower background Devices carry a wider window.
const windows = freshnessRows.map((row) => Number(row.split('|')[1]));
const intervals = freshnessRows.map((row) => Number(row.split('|')[2]));
invariant(
  Math.min(...windows) === profile.runtimeFreshness.freshWithinSeconds,
  `tightest freshness window is ${Math.min(...windows)}s, rig expects ${profile.runtimeFreshness.freshWithinSeconds}s`,
);
invariant(
  Math.min(...intervals) === profile.runtimeFreshness.expectedSampleIntervalSeconds,
  `shortest expected sample interval is ${Math.min(...intervals)}s, rig expects ${profile.runtimeFreshness.expectedSampleIntervalSeconds}s`,
);
notes.push(
  `runtime freshness: ${freshnessRows.length} policies, ${profile.runtimeFreshness.freshWithinSeconds}s/${profile.runtimeFreshness.expectedSampleIntervalSeconds}s for the chain Devices`,
);

// 3. Every Gateway credential must outlast the acceptance window: a Gateway whose
//    credential lapses mid-run has all of its uplink quarantined.
const credentialRows = psqlRows(
  'hvac_s1',
  `SELECT gateway_id::text || '|' || round(extract(epoch FROM (valid_until - now())) / 3600)::text
     FROM connectivity.gateway_credentials WHERE status = 'ACTIVE'`,
);
invariant(credentialRows.length > 0, 'expected at least one ACTIVE Gateway credential');
for (const row of credentialRows) {
  const [gatewayId, remainingHours] = row.split('|');
  invariant(Number(remainingHours) > 24, `Gateway ${gatewayId} credential expires in ${remainingHours}h, inside the acceptance window`);
  notes.push(`Gateway ${gatewayId} credential: ACTIVE for another ${remainingHours}h`);
}

// 4. The broker must not be replaying a persisted backlog: a saturated queue is what put
//    business time 38 minutes behind wall clock.
if (profile.broker.resetSubscriberQueue) {
  const listing = spawnSync(
    'docker',
    ['exec', brokerContainer, 'sh', '-c', 'stat -c %s /mosquitto/data/mosquitto.db 2>/dev/null || echo 0'],
    { encoding: 'utf8' },
  );
  invariant(listing.status === 0, 'could not inspect the MQTT broker data directory');
  const queuedBytes = Number(listing.stdout.trim() || '0');
  invariant(
    Number.isFinite(queuedBytes) && queuedBytes < 1024 * 1024,
    `MQTT broker persistence file is ${listing.stdout.trim()} bytes; the rig requires an empty subscriber queue before a run`,
  );
  notes.push(`broker queue: ${queuedBytes} bytes persisted`);
}

// 5. The simulator configuration the stack mounts must match the rig profile.
const plantCheck = spawnSync(process.execPath, ['scripts/generate-acceptance-plant-config.mjs', '--check'], {
  cwd: root,
  encoding: 'utf8',
});
invariant(plantCheck.status === 0, `acceptance plant config does not match the rig: ${plantCheck.stderr.trim() || plantCheck.stdout.trim()}`);
if (plantCheck.status === 0) notes.push(plantCheck.stdout.trim());

for (const note of notes) console.log(`  ${note}`);
if (failures.length > 0) {
  for (const failure of failures) console.error(`acceptance rig drift: ${failure}`);
  process.exit(1);
}
console.log(`Acceptance rig "${profile.profile}" is applied consistently.`);
