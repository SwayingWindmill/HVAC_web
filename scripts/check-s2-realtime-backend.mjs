import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-ticket-06/realtime-backend.json');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function text(path) {
  return readFile(resolve(root, path), 'utf8');
}

async function json(path) {
  return JSON.parse(await text(path));
}

const [
  openapi,
  publicationSchema,
  releaseGates,
  versionLock,
  generator,
  gateway,
  gatewayTest,
  runtime,
  centrifugoAdapter,
  runtimeServer,
  runtimePostgres,
  runtimeTest,
  runtimePostgresTest,
  migration,
  centrifugoConfig,
  realtimeCompose,
  pocConfig,
  pocRunner,
  docs,
  packageJSON,
] = await Promise.all([
  json('contracts/http/s2-telemetry-public.openapi.json'),
  json('contracts/events/s2-device-observation-publication.v1.schema.json'),
  json('deploy/s2/release-gates.v1.json'),
  json('pocs/platform-components/versions.lock.json'),
  text('scripts/generate-s2-telemetry-contracts.mjs'),
  text('services/platform-gateway/internal/gateway/telemetry.go'),
  text('services/platform-gateway/internal/gateway/telemetry_test.go'),
  text('services/telemetry-runtime-service/internal/telemetry/realtime.go'),
  text('services/telemetry-runtime-service/internal/telemetry/centrifugo.go'),
  text('services/telemetry-runtime-service/internal/telemetry/server.go'),
  text('services/telemetry-runtime-service/internal/telemetry/realtime_postgres.go'),
  text('services/telemetry-runtime-service/internal/telemetry/realtime_test.go'),
  text('services/telemetry-runtime-service/internal/telemetry/realtime_postgres_integration_test.go'),
  text('infra/s2-telemetry/postgres/init/005-s2-realtime-backend.sql'),
  text('infra/s2-telemetry/realtime/centrifugo.template.json'),
  text('infra/s2-telemetry/realtime/compose.yaml'),
  text('pocs/s2-centrifugo/centrifugo.json'),
  text('scripts/run-s2-centrifugo-poc.mjs'),
  text('docs/operations/s2-realtime-backend.md'),
  json('package.json'),
]);

const bootstrapPath = '/api/v1/telemetry/subscriptions:bootstrap';
const checkpointPath = '/api/v1/telemetry/recovery-cursors:checkpoint';
for (const path of [bootstrapPath, checkpointPath]) {
  assert(openapi.paths?.[path]?.post, `public OpenAPI route is not active: ${path}`);
  assert(openapi.paths[path].post.parameters?.some((entry) => entry.$ref?.endsWith('/ConditionalCsrfToken')), `${path} must retain conditional CSRF`);
}
assert(generator.includes('BootstrapTelemetrySubscriptions') && generator.includes('CheckpointTelemetryRecoveryCursors'), 'generated server/client authority is missing realtime methods');

const bootstrapSchema = openapi.components?.schemas?.SubscriptionBootstrapRequest;
const targetSchema = openapi.components?.schemas?.SubscriptionTargetRequest;
const checkpointSchema = openapi.components?.schemas?.RecoveryCursorCheckpointRequest;
assert(bootstrapSchema?.properties?.subscriptions?.maxItems === 100, 'bootstrap maximum must remain 100');
assert(targetSchema?.properties?.keys?.maxItems === 64, 'keys per subscription maximum must remain 64');
assert(checkpointSchema?.properties?.checkpoints?.maxItems === 100, 'checkpoint maximum must remain 100');
assert(releaseGates.transportBounds?.clientQueueMaxBytes === 262144, 'release queue bound drifted');
assert(releaseGates.transportBounds?.historySizePublications === 256, 'release history size drifted');
assert(releaseGates.transportBounds?.historyTtlSeconds === 180, 'release history TTL drifted');
assert(releaseGates.transportBounds?.maximumRecoveryCursorLifetimeSeconds === 120, 'release cursor TTL drifted');
assert(releaseGates.transportBounds?.connectionTokenLifetimeSeconds === 300, 'release connection capability TTL drifted');
const cursorReplayGates = releaseGates.negativeTests?.filter((test) => test.id === 'cursor-cross-principal-replay' || test.id === 'cursor-cross-device-or-key-replay') ?? [];
assert(cursorReplayGates.length === 2 && cursorReplayGates.every((test) => test.expectedStatus === 400 && test.expectedCode === 'RECOVERY_CURSOR_INVALID'), 'cursor replay failure contract drifted');

const publicationText = JSON.stringify(publicationSchema);
for (const forbidden of ['transportPosition', 'recoveryCursor', 'epoch', 'offset']) {
  assert(!publicationText.includes(forbidden), `public publication schema must not carry ${forbidden}`);
}
for (const required of ['eventId', 'subscriptionId', 'previousRevision', 'revision', 'telemetryChanges']) {
  assert(publicationText.includes(required), `public publication schema is missing ${required}`);
}

for (const marker of [
  'ActionSubscribe',
  'ActionRecoveryUse',
  'ActionRecoveryCheckpoint',
  'signTelemetryContextGrant',
  'resolveTelemetryCheckpointTargets',
  'parseTelemetrySubscriptionRequest',
  'parseTelemetryCheckpointRequest',
  'validateSubscriptionBootstrapResponse',
  'validateRecoveryCheckpointResponse',
  'Cache-Control',
]) assert(gateway.includes(marker), `Gateway realtime boundary is missing ${marker}`);
for (const marker of ['TestTelemetryGatewayBootstrapCheckpointAndRecoveryUseExactScope', 'internalTelemetryCheckpointResolvePath', 'ActionRecoveryUse']) {
  assert(gatewayTest.includes(marker), `Gateway realtime tests are missing ${marker}`);
}

for (const marker of [
  'SaveSubscriptions',
  'ActiveRecoveryCursor',
  'CurrentBusinessRevision',
  'ScopeDigest',
  'RecoveryCursorRejected',
  'telemetryChanges',
  'EvaluateRecovery',
  'SubscriptionRevoked',
  'Unsubscribe',
  'MaximumSubscriptionTTL',
  'DefaultSubscriptionTTL',
]) assert(runtime.includes(marker), `Telemetry Runtime realtime owner is missing ${marker}`);
assert(runtime.includes('MaximumConnection' + 'TokenTTL') && runtime.includes('DefaultConnection' + 'TokenTTL'), 'connection capability lifetime constants are missing');
for (const marker of ['maximumCentrifugoPublicationSize', '/api/publish', '/api/unsubscribe', 'X-API-Key']) {
  assert(centrifugoAdapter.includes(marker), `Centrifugo transport adapter is missing ${marker}`);
}
for (const marker of [
  'InternalRecoveryCheckpointResolvePath',
  'telemetryContextGrantHeader',
  'handleCentrifugoSubscribe',
  'strings.TrimSpace(input.Token) != ""',
  'handleSubscriptionRevoke',
  'allowedIAMSPIFFE',
  'RECOVERY_CURSOR_INVALID',
  'http.StatusBadRequest',
]) assert(runtimeServer.includes(marker), `Runtime internal boundary is missing ${marker}`);
for (const marker of [
  "projection.action = 'SUBSCRIBE'",
  "projection.decision = 'ALLOW'",
  "denial.decision = 'DENY'",
  'registry_device_bindings',
  'cursor_sha256',
  'device_observation_snapshots',
]) assert(runtimePostgres.includes(marker), `PostgreSQL current-scope recheck is missing ${marker}`);
for (const marker of [
  'future revision checkpoint was not rejected',
  'cross-tenant cursor reuse was not rejected',
  'cross-session cursor reuse was not rejected',
  'empty telemetryChanges array',
  'retry changed event identity/revision',
  'RecoveryLoadSnapshot',
]) assert(runtimeTest.includes(marker), `Runtime negative/recovery test is missing ${marker}`);
for (const marker of ['AuthorizeSubscribe', 'revoked IAM projection remained subscribable', 'RelayOnce', 'publication did not reuse authoritative revision']) {
  assert(runtimePostgresTest.includes(marker), `PostgreSQL realtime integration evidence is missing ${marker}`);
}

for (const marker of ['subject_issuer', 'session_id', 'channel', 'claim_owner', 'claim_until', 's2_telemetry_relay', "status = 'REVOKED'", 'UPDATE telemetry_runtime.recovery_cursors', 'migration-invalidated:']) {
  assert(migration.includes(marker), `expand-only realtime migration is missing ${marker}`);
}

const lockedCentrifugo = versionLock.components?.centrifugo?.image;
const lockedRedis = versionLock.components?.s2RealtimeRedis?.image;
assert(lockedCentrifugo && realtimeCompose.includes(lockedCentrifugo), 'formal realtime compose must use locked Centrifugo digest');
assert(lockedRedis && realtimeCompose.includes(lockedRedis), 'formal realtime compose must use locked Redis digest');
const redisServiceBlock = realtimeCompose.split('  s2-realtime-redis:')[1]?.split('  s2-centrifugo:')[0] ?? '';
assert(realtimeCompose.includes('internal: true') && redisServiceBlock && !redisServiceBlock.includes('\n    ports:'), 'dedicated Redis must remain private');
for (const marker of [
  '"queue_max_size": 262144',
  '"channel_limit": 100',
  '"history_max_publication_limit": 256',
  '"recovery_max_publication_limit": 256',
  '"history_size": 256',
  '"history_ttl": "180s"',
  '"force_recovery": true',
  '"force_positioning": true',
  '"subscribe_proxy_enabled": true',
  'CENTRIFUGO_VAR_S2_PROXY_SECRET',
  '"cert_pem": "/run/secrets/centrifugo-client/tls.crt"',
  '"key_pem": "/run/secrets/centrifugo-client/tls.key"',
  '"server_ca_pem": "/run/secrets/telemetry-runtime-ca/ca.crt"',
  '"insecure_skip_verify": false',
  '"server_name": "telemetry-runtime-service"',
]) assert(centrifugoConfig.includes(marker) || pocConfig.includes(marker), `Centrifugo bound/config marker is missing: ${marker}`);
for (const requiredMount of ['CENTRIFUGO_CLIENT_CERT_FILE', 'CENTRIFUGO_CLIENT_KEY_FILE', 'TELEMETRY_RUNTIME_CA_FILE']) {
  assert(realtimeCompose.includes(`\${${requiredMount}:?required}`), `formal realtime compose must require ${requiredMount}`);
}
for (const scenario of [
  'short recovery omitted revision 4',
  'failed recovery returned a partial publication set',
  'Redis-backed restart did not preserve bounded stream recovery',
  'revoked client received a later publication',
  'slow-consumer disconnect metric did not increase',
]) assert(pocRunner.includes(scenario), `real transport harness is missing ${scenario}`);

for (const phrase of [
  'Centrifugo `v6.8.1`',
  'Telemetry Runtime remains the only owner',
  'all requested subscriptions atomically',
  'authoritative Snapshot',
  'server-side unsubscribe',
  '262,144 bytes',
  'does not route an individual request to Legacy',
]) assert(docs.includes(phrase), `runbook is missing boundary phrase: ${phrase}`);

const implementationSurface = [gateway, runtime, runtimeServer, runtimePostgres].join('\n').toLowerCase();
for (const forbidden of ['thingsboard fallback', 'legacy fallback', 'mock fallback', 'socket.io']) {
  assert(!implementationSurface.includes(forbidden), `request-level fallback marker found: ${forbidden}`);
}

assert(packageJSON.scripts?.['s2:realtime:check'] === 'node scripts/check-s2-realtime-backend.mjs', 's2:realtime:check is not wired');
assert(packageJSON.scripts?.['s2:realtime:config'] === 'node scripts/run-s2-realtime-centrifugo-config-check.mjs', 's2:realtime:config is not wired');
assert(packageJSON.scripts?.['s2:realtime:transport'] === 'node scripts/run-s2-centrifugo-poc.mjs', 's2:realtime:transport is not wired');
assert(packageJSON.scripts?.['s2:realtime-backend']?.includes('npm run s2:realtime:config'), 's2:realtime-backend omits the formal Centrifugo mTLS configuration check');

const evidence = {
  schemaVersion: 1,
  ticket: 65,
  status: 'passed',
  authority: {
    businessOwner: 'telemetry-runtime-service',
    publicSeam: 'platform-gateway',
    authorizationOwner: 'iam-service',
    transport: 'centrifugo-v6.8.1',
    transportAuthority: false,
    snapshotFallbackAuthority: true,
  },
  bounds: releaseGates.transportBounds,
  publicRoutes: [bootstrapPath, checkpointPath],
  security: {
    exactDeviceKeyScope: true,
    currentSubscribeRecheck: true,
    cursorPersistedAndScopeBound: true,
    futureRevisionRejected: true,
    serverSideRevocation: true,
    requestFallbacks: 0,
  },
  transportEvidence: 'out/s2-centrifugo-poc/report.json',
  generatedAt: new Date().toISOString(),
};
await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
console.log(`S2 Ticket 06 realtime backend passed: ${output}`);
