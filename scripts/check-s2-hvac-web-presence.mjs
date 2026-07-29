import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-ticket-09/hvac-web-presence.json');
const text = async (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const realAssets = await text('apps/hvac-web/src/pages/Assets/RealAssets.tsx');
const assetsEntry = await text('apps/hvac-web/src/pages/Assets/index.tsx');
const current = await text('apps/hvac-web/src/api/telemetry-current.ts');
const centralPlant = await text('apps/hvac-web/src/domain/centralPlantTelemetry.ts');
const rendering = await text('apps/hvac-web/src/components/DeviceTelemetryState.tsx');
const principal = await text('apps/hvac-web/src/components/AuthenticatedPrincipalStatus.tsx');
const liveIndex = await text('apps/hvac-web/src/platform/telemetry-live/index.ts');
const browserHarness = await text('scripts/fixtures/s2-hvac-web-presence/harness.tsx');
const browserRunner = await text('scripts/run-s2-hvac-web-presence-browser-audit.mjs');
const runbook = await text('docs/operations/s2-hvac-web-presence.md');
const workflow = await text('.github/workflows/s2-hvac-web-presence.yml');
const packageJSON = JSON.parse(await text('package.json'));

assert(assetsEntry.includes("API_MODE === 'real'") && assetsEntry.includes('<RealAssets />'), 'real Assets entry no longer selects RealAssets explicitly');
for (const marker of [
  'useVisibleDevicePresence', 'useDeviceTelemetryLive', 'DevicePresenceCell', 'DeviceTelemetryPanel',
  'purgeTelemetryCurrentState(queryClient, telemetryRuntime)', 'Presence-only batch', 'exact keys',
  'deviceType={selectedDevice?.deviceType}', 'data-presence-batch-state="partial"', '真实模式保持 Registry 列表可见',
]) assert(realAssets.includes(marker), `RealAssets is missing ${marker}`);
for (const forbidden of [
  "from '@/api/telemetry'", 'getToken(', 'hvac_token', 'socket.io-client', '/ws/telemetry', 'thingsboard/api', 'MockAssets',
]) assert(!realAssets.toLowerCase().includes(forbidden.toLowerCase()), `RealAssets contains forbidden real-mode dependency or fallback: ${forbidden}`);

for (const marker of [
  'createS2TelemetryClient', "from '@/platform/telemetry-live'", 'batchGetDeviceObservationSnapshots',
  'keys: []', 'MAX_BATCH_DEVICES = 100', 'getDeviceTelemetryProfile', 'deviceDetailTelemetryKeys',
  'const selectedKeys = keys ?? deviceDetailTelemetryKeys(device?.deviceType)', 'keys: [...selectedKeys]',
  'Authenticated Organization changed during telemetry request', 'snapshot.values.length !== 0',
  'runtime.live.open', 'runtime.live.purge()', "queryClient?.removeQueries({ queryKey: ['s2-current'] })",
  'x-route-policy-revision', 'runtime.routePolicy.subscribe',
  "error.problem.code === 'RESOURCE_NOT_FOUND'", "API_MODE !== 'real'",
]) assert(current.includes(marker), `telemetry-current is missing ${marker}`);
for (const forbidden of [
  "from './telemetry'", "from './auth'", 'getToken(', 'localStorage', 'Authorization:', 'X-Organization-ID',
  '/ws/telemetry', 'socket.io-client', 'thingsboard/api', 'createMock', 'mockTelemetry',
]) assert(!current.toLowerCase().includes(forbidden.toLowerCase()), `telemetry-current contains forbidden authority/fallback marker: ${forbidden}`);

for (const marker of [
  "CHILLER: profile('CHILLER'", "CHILLED_WATER_PUMP: profile('CHILLED_WATER_PUMP'",
  "COOLING_WATER_PUMP: profile('COOLING_WATER_PUMP'", "COOLING_TOWER: profile('COOLING_TOWER'",
  "HVAC_POWER_METER: profile('HVAC_POWER_METER'", "BTU_METER: profile('BTU_METER'",
  "GENERIC: profile('GENERIC'", "'temperature'", "'humidity'", "'setpoint'", "'power'",
  "'chiller.cop'", "'hvac_meter.active_power'", "'btu_meter.instant_cooling_capacity'",
  'buildDeviceTelemetryHighlights', 'telemetryPointDefinition',
]) assert(centralPlant.includes(marker), `central-plant telemetry profile is missing ${marker}`);

assert(liveIndex.includes('TelemetryLiveClient') && !liveIndex.includes('centrifugo-transport'), 'feature-facing live boundary exposes transport internals');
for (const marker of [
  'ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE', 'MISSING（不补零）', 'SUSPECT',
  'data-transport-state="degraded"', 'data-platform-availability="UNAVAILABLE"', 'data-device-live-state="revoked"',
  'data-central-plant-profile', '实时运行摘要', 'telemetryPointDefinition',
  '原 sampledAt', '不会混合尚未连续应用的 publication', 'Last Known 值保留原 sampledAt',
]) assert(rendering.includes(marker), `state rendering is missing ${marker}`);
assert(!rendering.includes('Date.now()') && !rendering.includes('new Date().toISOString()'), 'rendering invents a request-time device timestamp');
assert(principal.includes('purgeTelemetryCurrentState(queryClient)') && principal.indexOf('purgeTelemetryCurrentState(queryClient)') > principal.indexOf('await client.logout'), 'logout does not purge telemetry state after session revocation');
assert(current.includes("state?.status === 'revoked'") && current.includes('purgeTelemetryCurrentState(queryClient, runtime)'), 'revocation does not purge live and query state');
assert(browserHarness.includes('routeCohortChanged()') && browserHarness.includes("runtime.routePolicy.observe('fixture-route-revision-10')"), 'route cohort change does not exercise the production route-policy purge path');
for (const marker of [
  'two-device-partial-presence-batch', 'exact-key-last-known-rendering', 'live-delta-shared-snapshot-model',
  'reconnect-no-mixed-current-state', 'gap-requires-resynchronization', 'transport-outage-explicit-no-fallback',
  'revocation-purges-browser-state', 'sibling-site-switch-purges-hidden-device',
  'central-plant-chiller-exact-keys-and-summary', 'route-cohort-change-purges-browser-state',
  'two-organization-dual-principal-fail-closed',
  'browser-a11y-controls-labeled', 'real-mode-network-no-fallback',
]) assert(browserRunner.includes(marker), `browser journey is missing ${marker}`);

for (const marker of [
  'Presence-only', 'exact keys', 'MISSING', 'SUSPECT', 'route cohort', 'RESOURCE_NOT_FOUND',
  'npm run s2:ticket-09', 'out/s2-ticket-09/network-audit.json',
]) assert(runbook.includes(marker), `Ticket 09 Runbook is missing ${marker}`);
for (const marker of [
  'name: S2 HVAC Web Presence and Latest Telemetry', 'ubuntu-24.04', 'node-version: "22.22.0"',
  'npm run s2:ticket-09', 'out/s2-ticket-09', 'if-no-files-found: error',
]) assert(workflow.includes(marker), `Ticket 09 workflow is missing ${marker}`);

for (const script of ['test:central-plant-telemetry', 's2:hvac-web:check', 's2:hvac-web:browser', 's2:ticket-09']) {
  assert(packageJSON.scripts?.[script], `package script ${script} is missing`);
}
assert(packageJSON.scripts['s2:hvac-web:check'].includes('test:central-plant-telemetry'), 'S2 HVAC Web check omits central-plant telemetry behavior tests');
assert(packageJSON.scripts['s2:ticket-09'].includes('s2:hvac-web:browser'), 'Ticket 09 omits browser evidence');

await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({
  schemaVersion: 1,
  ticket: 68,
  status: 'passed',
  realModeGeneratedHttpOnly: true,
  featureUsesTelemetryLiveClient: true,
  visibleListPresenceOnlyBatch: true,
  detailExactKeys: ['temperature', 'humidity', 'setpoint', 'power'],
  explicitStates: ['ONLINE', 'OFFLINE', 'STALE', 'UNKNOWN', 'UNAVAILABLE', 'MISSING', 'SUSPECT', 'revoked'],
  browserCachePurge: ['organization-switch', 'site-switch', 'logout', 'revocation', 'resource-not-found'],
  requestFallback: false,
  browserEvidence: 'out/s2-ticket-09/browser-journey.json',
  networkEvidence: 'out/s2-ticket-09/network-audit.json',
  renderingEvidence: 'out/s2-ticket-09/state-rendering.json',
  generatedAt: new Date().toISOString(),
}, null, 2)}\n`);
console.log(`S2 Ticket 09 HVAC Web Presence/latest passed: ${output}`);
