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
const compose = await text('pocs/s2-centrifugo/compose.yaml');
const config = await text('pocs/s2-centrifugo/centrifugo.json');
const owner = await text('pocs/s2-centrifugo/owner.mjs');
const runner = await text('scripts/run-s2-centrifugo-poc.mjs');
const readme = await text('pocs/s2-centrifugo/README.md');
const evaluation = await text('docs/research/s2-realtime-transport-evaluation.md');
const packageJSON = JSON.parse(await text('package.json'));

const locked = lock.components?.centrifugo;
const lockedRedis = lock.components?.s2RealtimeRedis;
assert(locked?.version === 'v6.8.1', 'S2 Centrifugo POC must use locked v6.8.1');
assert(locked?.license === 'Apache-2.0', 'S2 Centrifugo license conclusion must remain Apache-2.0');
assert(
  locked?.image === 'centrifugo/centrifugo:v6.8.1@sha256:b20c036ed17c7151c6acc176b34870ac37f4eb84618de7cc6459094744dd42aa',
  'S2 Centrifugo image must match the locked digest',
);

for (const image of [...compose.matchAll(/^\s+image:\s+(.+)$/gm)].map((match) => match[1].trim())) {
  assert(/@sha256:[a-f0-9]{64}$/.test(image), `POC image is not digest pinned: ${image}`);
}
assert(compose.includes(locked.image), 'Compose does not use the locked Centrifugo image');
assert(lockedRedis?.image === 'redis:7.4.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952', 'S2 realtime Redis image must match the locked digest');
assert(compose.includes(lockedRedis.image), 'Compose does not use the dedicated locked Redis image');
assert(config.includes('"type": "redis"') && config.includes('redis://redis:6379/0'), 'Centrifugo must use the dedicated Redis engine');
for (const variable of ['POC_CENTRIFUGO_HMAC_SECRET', 'POC_CENTRIFUGO_API_KEY']) {
  assert(compose.includes(`\${${variable}:?`), `Compose must require runtime variable ${variable}`);
}

assert(config.includes('"subscribe_proxy_enabled": true'), 'subscribe proxy must authorize every client subscription');
assert(config.includes('"subscribe_proxy_name": "s2-subscribe"'), 'subscribe proxy name is missing');
assert(!config.includes('allow_subscribe_for_client'), 'client-controlled subscription permission must not be enabled');
assert(config.includes('"history_size": 256'), 'bounded history size must remain 256');
assert(config.includes('"history_ttl": "180s"'), 'bounded history TTL must remain 180 seconds');
assert(config.includes('"queue_max_size": 262144'), 'slow-consumer queue limit must match the S2 release envelope');
assert(config.includes('"history_max_publication_limit": 256'), 'history response limit must remain 256');
assert(config.includes('"recovery_max_publication_limit": 256'), 'recovery response limit must remain 256');
assert(config.includes('"force_recovery": true'), 'transport recovery must remain enabled');
assert(config.includes('"force_positioning": true'), 'transport positioning must remain enabled');
assert(config.includes('"prometheus"'), 'Prometheus metrics must remain enabled');

for (const marker of [
  "['s2-user', new Set([primaryChannel, loadChannel])]",
  "if (!allowed)",
  "REVISION_NOT_MONOTONIC",
  "permissions.get(user)?.delete(channel)",
  "snapshot-captured",
]) {
  assert(owner.includes(marker), `owner fixture is missing boundary marker: ${marker}`);
}
assert(!owner.includes('org-b:site-b'), 'owner ACL fixture must not pre-authorize the denied Site');

for (const scenario of [
  'forged connection token was accepted',
  'cross-Site subscription was not denied',
  'duplicate business revision was not ignored',
  'short recovery omitted revision 4',
  'revoked client received a later publication',
  'failed recovery returned a partial publication set',
  'Redis-backed restart did not preserve bounded stream recovery',
  'fanout revision ${fanoutRevision}',
  'slow-consumer disconnect metric did not increase',
]) {
  assert(runner.includes(scenario), `runner is missing required scenario: ${scenario}`);
}
assert(runner.includes('signature[0] ^= 0x01'), 'forged-token scenario must flip decoded signature bytes, not base64 padding bits');
assert(runner.includes("decision: 'adopt-with-bounded-responsibility'"), 'runner must emit the bounded adoption decision');
assert(runner.includes('productionScaleCertified: false'), 'bounded fan-out must not be represented as production certification');
assert(runner.includes('secretsPersisted: false'), 'runner must assert runtime secrets are not persisted');
assert(runner.includes("closeCode: slowClose.code"), 'runner must record slow-consumer close evidence');
assert(runner.includes("serverUnsubscribeMetrics: false"), 'runner must retain the v6.8.1 unsubscribe-metric limitation');

for (const phrase of [
  'throwaway integration experiment',
  'platform owner',
  'Centrifugo transport',
  'not a production scale certification',
  'npm run s2:centrifugo:poc',
]) {
  assert(readme.includes(phrase), `README is missing boundary phrase: ${phrase}`);
}

for (const phrase of [
  'Adopt with bounded responsibility',
  'Snapshot authority',
  'business Revision',
  'subscribe proxy',
  'close code `3008`',
  'production scale is not certified',
  'Apache-2.0',
  'rollback',
]) {
  assert(evaluation.includes(phrase), `evaluation is missing required decision evidence: ${phrase}`);
}

assert(packageJSON.scripts?.['s2:centrifugo:check'] === 'node scripts/check-s2-centrifugo-poc-assets.mjs', 'static check script is not wired');
assert(packageJSON.scripts?.['s2:centrifugo:poc'] === 'node scripts/run-s2-centrifugo-poc.mjs', 'executable POC script is not wired');

for (const forbidden of [
  'YOUR_API_KEY',
  'my_secret',
  'token_hmac_secret_key": "',
  'api_key": "',
]) {
  assert(!compose.includes(forbidden) && !config.includes(forbidden) && !runner.includes(forbidden), `static credential marker found: ${forbidden}`);
}

console.log('S2 Centrifugo POC assets passed: exact authorization, bounded recovery and transport-only responsibility.');
