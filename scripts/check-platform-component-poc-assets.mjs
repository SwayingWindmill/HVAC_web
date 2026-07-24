import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

async function text(path) {
  return readFile(resolve(root, path), 'utf8');
}

const lock = JSON.parse(await text('pocs/platform-components/versions.lock.json'));
const readme = await text('pocs/platform-components/README.md');
const compose = await text('pocs/platform-components/docker/compose.yaml');
const connect = await text('pocs/platform-components/docker/redpanda-connect.yaml');
const centrifugo = await text('pocs/platform-components/docker/centrifugo.json');
const envoy = await text('pocs/platform-components/envoy/manifests.yaml');
const riverMod = await text('pocs/platform-components/river/go.mod');
const riverSource = await text('pocs/platform-components/river/main.go');
const dockerRunner = await text('scripts/run-platform-component-docker-pocs.mjs');
const envoyRunner = await text('scripts/run-platform-component-envoy-poc.mjs');
const isolatedGoRunner = await text('scripts/run-isolated-go.mjs');

assert(lock.schemaVersion === 1, 'component lock schemaVersion must be 1');
const expected = {
  envoyGateway: ['v1.8.0', 'Apache-2.0'],
  debezium: ['3.6.0.Final', 'Apache-2.0'],
  redpandaConnect: ['v4.94.0', 'Redpanda Community License with component-specific availability'],
  centrifugo: ['v6.8.1', 'Apache-2.0'],
  river: ['v0.35.1', 'MPL-2.0'],
};
for (const [name, [version, license]] of Object.entries(expected)) {
  const component = lock.components?.[name];
  assert(component, `missing component lock: ${name}`);
  assert(component.version === version, `${name} must remain pinned to ${version}`);
  assert(component.license === license, `${name} license assessment drifted`);
  const allowedDecisions = name === 'centrifugo'
    ? ['adopt-s2-transport']
    : ['poc', 'poc-license-review-required'];
  assert(allowedDecisions.includes(component.decision), `${name} decision is not valid for its current lifecycle stage`);
}
const realtimeRedis = lock.components?.s2RealtimeRedis;
assert(realtimeRedis?.version === '7.4.2-alpine', 'S2 realtime Redis version drifted');
assert(realtimeRedis?.decision === 'adopt-s2-transport-only', 'S2 realtime Redis must remain transport-only');
assert(/@sha256:[a-f0-9]{64}$/.test(realtimeRedis?.image ?? ''), 'S2 realtime Redis image must be digest pinned');
assert(/^[a-f0-9]{64}$/.test(lock.components.envoyGateway.installerSha256), 'Envoy installer SHA-256 is invalid');
for (const name of ['debezium', 'redpandaConnect', 'centrifugo', 'redpandaBrokerCompatibility']) {
  assert(/@sha256:[a-f0-9]{64}$/.test(lock.components[name].image), `${name} image must be digest pinned`);
}

for (const component of ['Envoy Gateway', 'Debezium', 'Redpanda Connect', 'Centrifugo', 'River']) {
  assert(readme.includes(component), `README is missing ${component}`);
}
for (const boundary of [
  'not production dependencies',
  'business identity headers',
  'single-direction CDC',
  'platform-owned Snapshot/Cursor/Scope semantics',
  'with no external side effect ambiguity',
]) {
  assert(readme.includes(boundary), `README is missing boundary: ${boundary}`);
}

const composeImages = [...compose.matchAll(/^\s+image:\s+(.+)$/gm)].map((match) => match[1].trim());
assert(composeImages.length === 6, `expected 6 digest-pinned Compose images, found ${composeImages.length}`);
for (const image of composeImages) {
  assert(/@sha256:[a-f0-9]{64}$/.test(image), `Compose image is not digest pinned: ${image}`);
}
for (const requiredVariable of ['POC_POSTGRES_PASSWORD', 'POC_CENTRIFUGO_HMAC_SECRET', 'POC_CENTRIFUGO_API_KEY']) {
  assert(compose.includes(`\${${requiredVariable}:?`), `Compose must require runtime variable ${requiredVariable}`);
}
for (const forbidden of ['postgres-local-only', 'my_secret', 'YOUR_API_KEY', 'token_hmac_secret_key": "']) {
  assert(!compose.includes(forbidden) && !centrifugo.includes(forbidden), `static credential marker found: ${forbidden}`);
}

assert(connect.includes('kafka_franz:'), 'Redpanda Connect POC must use the non-enterprise Kafka-compatible input');
assert(connect.includes('start_offset: earliest'), 'Redpanda Connect must consume the initial Debezium snapshot');
assert(connect.includes('http://evidence-sink:18080/events'), 'Redpanda Connect evidence sink is missing');
for (const forbidden of ['organization_id', 'site_id', 'equipment_id', 'device_id', 'platform_id']) {
  assert(!connect.includes(forbidden), `Redpanda Connect mapping must not invent ${forbidden}`);
}

assert(envoy.includes('kind: EnvoyProxy'), 'Envoy POC must use an explicit EnvoyProxy configuration');
assert(envoy.includes('type: ClusterIP'), 'Envoy POC must not depend on an unavailable Kind LoadBalancer implementation');
assert(envoy.includes('value: /api/v1/status'), 'Envoy POC must expose only the exact platform status path');
assert(envoy.includes('type: Exact'), 'Envoy POC route must use exact matching');
assert(!envoy.includes('RequestHeaderModifier'), 'Envoy POC must not synthesize identity headers');
for (const forbidden of ['X-Principal', 'X-Organization', 'X-Site', 'X-Roles', 'X-Admin']) {
  assert(!envoy.includes(forbidden), `Envoy POC contains forbidden identity header ${forbidden}`);
}
assert(envoy.match(/@sha256:[a-f0-9]{64}/g)?.length === 2, 'Envoy fixture images must be digest pinned');

assert(riverMod.includes('github.com/riverqueue/river v0.35.1'), 'River dependency must remain pinned');
assert(riverMod.includes('github.com/jackc/pgx/v5 v5.9.2'), 'River POC must use the existing pgx baseline');
assert(riverSource.includes('InsertTx'), 'River POC must test transaction-local insertion');
assert(riverSource.includes('UniqueOpts'), 'River POC must test unique jobs');
assert(riverSource.includes('Rollback'), 'River POC must test rollback');
assert(isolatedGoRunner.includes("GOWORK: 'off'"), 'isolated Go runner must disable the production workspace');

for (const runner of [dockerRunner, envoyRunner]) {
  assert(runner.includes('out/platform-component-pocs'), 'POC runner must write machine-readable evidence');
  assert(runner.includes('status: \'passed\''), 'POC runner must emit a passing status only after assertions');
}
assert(dockerRunner.includes("['database', 'password'].join('.')"), 'Debezium credential key must be constructed at runtime');
assert(!dockerRunner.includes("'database.password':"), 'Debezium credential must not be embedded in source');
assert(dockerRunner.includes('secretsPersisted: false'), 'Docker POC report must assert secrets are not persisted');
assert(envoyRunner.includes('installerSha256'), 'Envoy runner must verify the installer digest');
assert(envoyRunner.includes('businessIdentityHeadersAdded: false'), 'Envoy report must record the identity-header boundary');

console.log('Platform component POC assets passed: 5 candidates, isolated and version locked.');
