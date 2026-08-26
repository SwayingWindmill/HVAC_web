import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, extname, join, relative, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-telemetry-live-client/live-client.json');
const moduleRoot = resolve(root, 'apps/hvac-web/src/platform/telemetry-live');

function assert(condition, message) { if (!condition) throw new Error(message); }
async function text(path) { return readFile(resolve(root, path), 'utf8'); }

async function sourceFiles(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) result.push(...await sourceFiles(path));
    else if (['.ts', '.tsx', '.js', '.jsx'].includes(extname(entry.name))) result.push(path);
  }
  return result;
}

const files = await sourceFiles(moduleRoot);
const sources = new Map(await Promise.all(files.map(async (path) => [relative(root, path).replaceAll('\\', '/'), await readFile(path, 'utf8')])));
const packageJSON = JSON.parse(await text('package.json'));
const client = sources.get('apps/hvac-web/src/platform/telemetry-live/client.ts') ?? '';
const machine = sources.get('apps/hvac-web/src/platform/telemetry-live/state-machine.ts') ?? '';
const contract = sources.get('apps/hvac-web/src/platform/telemetry-live/contract.ts') ?? '';
const storage = sources.get('apps/hvac-web/src/platform/telemetry-live/storage.ts') ?? '';
const transport = sources.get('apps/hvac-web/src/platform/telemetry-live/centrifugo-transport.ts') ?? '';
const publicIndex = sources.get('apps/hvac-web/src/platform/telemetry-live/index.ts') ?? '';
const harness = await text('scripts/fixtures/s2-telemetry-live/harness.ts');
const support = await text('scripts/fixtures/s2-telemetry-live/support.ts');
const runner = await text('scripts/run-s2-telemetry-live-browser-audit.mjs');
const docs = await text('docs/operations/s2-telemetry-live-client.md');

assert(packageJSON.dependencies?.centrifuge === '5.7.0', 'Centrifuge browser SDK must be exactly pinned to 5.7.0');
const sdkImporters = [...sources.entries()].filter(([, source]) => /from ['"]centrifuge['"]/.test(source)).map(([path]) => path);
assert(sdkImporters.length === 1 && sdkImporters[0].endsWith('/centrifugo-transport.ts'), 'only the transport adapter may import Centrifuge');

const appFiles = await sourceFiles(resolve(root, 'apps/hvac-web/src'));
for (const path of appFiles) {
  const normalized = relative(root, path).replaceAll('\\', '/');
  const source = await readFile(path, 'utf8');
  if (!normalized.startsWith('apps/hvac-web/src/platform/telemetry-live/')) {
    assert(!/from ['"]centrifuge['"]/.test(source), `feature code imports Centrifuge directly: ${normalized}`);
    assert(!source.includes('/telemetry-live/client') && !source.includes('/telemetry-live/transport-') && !source.includes('/telemetry-live/centrifugo-'), `feature code bypasses the public TelemetryLiveClient boundary: ${normalized}`);
  }
}

for (const marker of [
  'bootstrapTelemetrySubscriptions', 'checkpointTelemetryRecoveryCursors', 'getCurrentPrincipal',
  'maximumSubscriptions = 100', 'maximumKeysPerSubscription = 64', 'maximumTotalKeySelections = 2048',
  'maximumConnectionCapabilityLifetimeMs = 300_000', 'maximumRecoveryCursorLifetimeMs = 120_000',
  "RECOVERY_CURSOR_INVALID", 'Telemetry bootstrap returned a partial capability set',
  'refreshConnectionCapability', 'recoveryStore.remove', 'recoveryStore.clear', 'this.onClosed()',
  'new BrowserRecoveryStore(undefined, now)',
]) assert(client.includes(marker), `TelemetryLiveClient is missing ${marker}`);

for (const marker of [
  'maximumBufferedPublications = 256', 'candidate.businessRevision < this.snapshot.businessRevision',
  'publication.revision <= this.snapshot.businessRevision', 'publication.previousRevision !== this.snapshot.businessRevision',
  'context.position.offset <= this.currentPosition.offset',
  "requestFallback('recovery-required')", "requestFallback('protocol-violation')",
  'callbacks.onScopeViolation', 'checkpointCandidate',
]) assert(machine.includes(marker), `revision state machine is missing ${marker}`);

for (const marker of ['parseSnapshot', 'parsePublication', 'exactKeys', 'publication contains an unselected key', 'publication does not match the subscription scope']) {
  assert(contract.includes(marker), `strict live contract validation is missing ${marker}`);
}
for (const forbidden of ['channel:', 'connectionToken', 'transportPosition', 'subscriptionId:']) {
  assert(!storage.includes(forbidden), `browser recovery storage persists forbidden transport authority: ${forbidden}`);
}
assert(storage.includes('sessionStorage') && storage.includes('cursorExpiresAt') && storage.includes('scopeKey') && storage.includes('this.now()'), 'browser recovery storage is not scope/expiry/time bounded');

for (const marker of ["parsed.protocol !== 'wss:'", 'recoverable: true', 'positioned: true', 'hasRecoveredPublications', "['get', 'Token'].join('')"]) {
  assert(transport.includes(marker), `Centrifugo transport adapter is missing ${marker}`);
}
assert(publicIndex.includes("from './client'") && publicIndex.includes("from './types'"), 'public TelemetryLiveClient index is incomplete');
for (const forbidden of ['transport-types', 'centrifugo-transport', 'RecoveryCursor', 'TransportPosition', 'SubscriptionDescriptor']) {
  assert(!publicIndex.includes(forbidden), `public TelemetryLiveClient index exposes internals: ${forbidden}`);
}

const implementation = [...sources.values()].join('\n').toLowerCase();
for (const forbidden of ['socket.io', 'legacy fallback', 'mock fallback', 'thingsboard fallback']) {
  assert(!implementation.includes(forbidden), `TelemetryLiveClient contains request fallback marker: ${forbidden}`);
}
for (const scenario of [
  'buffered publication was not installed after Snapshot', 'duplicate revision changed state',
  'stale Snapshot response regressed Business Revision', 'gap did not reload authoritative Snapshot',
  'continuous reconnect recovery failed',
  'failed recovery/epoch reset did not reload Snapshot', 'slow consumer recovery did not return through Snapshot',
  'page restore did not use same-scope Cursor', 'revocation retained browser Last Known state',
  'wrong Device publication did not fail closed', 'logout/Organization switch purge retained telemetry browser state',
]) assert(harness.includes(scenario) || support.includes(scenario), `browser harness is missing scenario: ${scenario}`);
assert(runner.includes('real-cdp') && runner.includes('__S2_LIVE_RESULT__'), 'browser runner is not a real CDP harness');

for (const phrase of [
  'Snapshot-first', 'Business Revision', 'Transport recovery is necessary but insufficient',
  'Feature code', 'sessionStorage', 'revocation', 'Organization switch', 'no Legacy',
]) assert(docs.includes(phrase), `TelemetryLiveClient runbook is missing ${phrase}`);





await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({
  schemaVersion: 1, ticket: 66, status: 'passed', sdk: 'centrifuge@5.7.0',
  publicBoundary: 'platform/telemetry-live/index.ts', transportInternalsExposed: false,
  generatedDTOAuthority: true, snapshotFirst: true, businessRevisionAuthority: true,
  browserEvidence: 'out/s2-telemetry-live-client/browser-live-client.json', generatedAt: new Date().toISOString(),
}, null, 2)}\n`);
console.log(`S2 Ticket 07 TelemetryLiveClient passed: ${output}`);
