import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, statSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { buildCertificationInventory } from './real-assets-certification-lib.mjs';
import { resolveLinuxBrowserExecutable } from './lib/browser-runtime.mjs';

const root = resolve(process.cwd());
const outputRoot = resolve(root, 'out/hvac-monitor-browser-audit');
const baseURL = process.env.HVAC_MONITOR_PREVIEW_URL ?? 'http://127.0.0.1:5175';
const tenantId = '01940000-0000-7000-8000-000000000001';
const siteId = process.env.HVAC_MONITOR_SITE_ID ?? '01940000-0001-7000-8000-000000000001';
const monitorURL = `${baseURL}/sites/${siteId}/monitor`;
const inventory = buildCertificationInventory({ tenantId, siteId, namespace: '01940000' });
const firstDeviceId = process.env.HVAC_MONITOR_DEVICE_ID ?? inventory.devices[0]?.id;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function visibleDom(raw) {
  return raw
    .replace(/<style[\s\S]*?<\/style>/gi, ' ')
    .replace(/<script[\s\S]*?<\/script>/gi, ' ');
}

function visibleText(raw) {
  return visibleDom(raw)
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim();
}

function countClass(raw, className) {
  const body = visibleDom(raw);
  return [...body.matchAll(/class="([^"]+)"/g)]
    .filter((match) => match[1].split(/\s+/).includes(className))
    .length;
}

function countLegacyAntClasses(raw) {
  return [...visibleDom(raw).matchAll(/class="([^"]+)"/g)]
    .filter((match) => match[1].split(/\s+/).some((className) => className.startsWith('ant-')))
    .length;
}

function pngDimensions(path) {
  const buffer = readFileSync(path);
  assert(buffer.subarray(1, 4).toString('ascii') === 'PNG', `${path} is not a PNG screenshot`);
  return { width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20) };
}

const browserPath = resolveLinuxBrowserExecutable();
assert(firstDeviceId, 'Certification fixture did not provide a device for Context Inspector visual audit.');

let browserRunSequence = 0;

function browserRuntimeArgs() {
  browserRunSequence += 1;
  const profile = `/tmp/hvac-monitor-audit-${process.pid}-${browserRunSequence}`;
  return [
    '--headless=new',
    '--disable-gpu',
    '--disable-extensions',
    '--disable-sync',
    '--disable-background-networking',
    '--no-first-run',
    '--no-default-browser-check',
    '--no-sandbox',
    `--user-data-dir=${profile}`,
  ];
}

function runBrowser(args, label, timeout = 12_000) {
  const result = spawnSync(browserPath, args, {
    cwd: root,
    encoding: 'utf8',
    timeout,
    maxBuffer: 40 * 1024 * 1024,
  });
  const timedOutAfterProducingEvidence = result.status === null && result.error?.code === 'ETIMEDOUT';
  assert(result.status === 0 || timedOutAfterProducingEvidence, `${label} failed with status ${result.status}: ${result.stderr}`);
  return result;
}

function dumpDom(url) {
  const result = runBrowser([
    ...browserRuntimeArgs(),
    '--window-size=1672,941',
    '--virtual-time-budget=5000',
    '--dump-dom',
    url,
  ], `dump DOM for ${url}`);
  assert(result.stdout.includes('</html>'), `dump DOM for ${url} did not produce a complete document`);
  return result.stdout;
}

function screenshot(url, filename) {
  const path = join(outputRoot, filename);
  runBrowser([
    ...browserRuntimeArgs(),
    '--hide-scrollbars',
    '--window-size=1672,941',
    '--virtual-time-budget=5000',
    `--screenshot=${path}`,
    url,
  ], `capture ${filename}`);
  assert(existsSync(path), `${filename} was not created`);
  const size = statSync(path).size;
  const dimensions = pngDimensions(path);
  assert(dimensions.width === 1672 && dimensions.height === 941, `${filename} viewport drifted to ${dimensions.width}x${dimensions.height}`);
  assert(size > 10_000, `${filename} is unexpectedly small (${size} bytes)`);
  return { filename, size, ...dimensions };
}

await mkdir(outputRoot, { recursive: true });
const report = {
  browserPath,
  preview: monitorURL,
  viewport: { width: 1672, height: 941 },
  assertions: [],
  screenshots: [],
  surfaces: {},
};

function pass(label) {
  report.assertions.push(label);
  console.log(`PASS ${label}`);
}

const topologyDom = dumpDom(monitorURL);
const topologyText = visibleText(topologyDom);
const topologyBody = visibleDom(topologyDom);
assert(topologyBody.includes('data-testid="hvac-control-workspace"'), 'HVAC shadcn workspace root was not rendered.');
assert(['系统拓扑', '异常定位', '能流证据', '关键实时证据', '当前告警', '继续调查'].every((label) => topologyText.includes(label)), 'HVAC workspace responsibilities are incomplete.');
assert(countClass(topologyDom, 'control-monitor-tab') === 3, 'HVAC workspace must render exactly three primary workspace tabs.');
assert(countClass(topologyDom, 'control-monitor-evidence-panel') === 3, 'HVAC workspace must render exactly three supporting evidence cards.');
assert(topologyBody.includes('data-testid="hvac-x6-topology"'), 'HVAC workspace did not render the shared X6 topology canvas.');
assert(['冷机组', '冷冻水泵', 'AHU', 'FCU', '冷却水泵', '冷却塔'].every((label) => topologyText.includes(label)), 'Engineering topology is missing one or more HVAC stages.');
assert(!topologyBody.includes('hvac-ref__kpis'), 'Superseded KPI band returned to the default monitor.');
assert(!topologyBody.includes('ant-pro-card'), 'Superseded ProCard geometry returned to the default monitor.');
assert(!topologyBody.includes('hvac-monitor__drawer'), 'Superseded device Drawer returned to the default monitor.');
assert(countLegacyAntClasses(topologyDom) === 0, 'Default monitor rendered legacy Ant DOM classes.');
for (const forbidden of ['traceId', 'schemaVersion', 'CERT-DEVICE', 'CONNECTED', '权威 Summary']) {
  assert(!topologyText.includes(forbidden), `Operator surface leaked internal term: ${forbidden}`);
}
report.surfaces.topology = {
  tabs: countClass(topologyDom, 'control-monitor-tab'),
  topologyNodes: countClass(topologyDom, 'hvac-x6-node'),
  evidencePanels: countClass(topologyDom, 'control-monitor-evidence-panel'),
};
report.screenshots.push(screenshot(monitorURL, '01-control-topology.png'));
pass('Shadcn HVAC topology workspace renders without legacy KPI/ProCard/Drawer UI');

const inspectorURL = `${monitorURL}?device=${encodeURIComponent(firstDeviceId)}`;
const inspectorDom = dumpDom(inspectorURL);
const inspectorText = visibleText(inspectorDom);
const inspectorBody = visibleDom(inspectorDom);
assert(inspectorBody.includes('control-monitor-detail-sheet'), 'Device Detail Sheet was not rendered for a selected device.');
assert(['运行', '连接', '数据新鲜度', '数据质量', '当前关键值', '活动告警', '打开完整详情'].every((label) => inspectorText.includes(label)), 'Device Detail Sheet is missing required business evidence.');
for (const rawEnum of [' FRESH ', ' GOOD ', ' DEGRADED ', ' NOT_APPLICABLE ', ' UNAVAILABLE ']) {
  assert(!` ${inspectorText} `.includes(rawEnum), `Context Inspector leaked raw enum ${rawEnum.trim()}.`);
}
assert(!inspectorBody.includes('ant-drawer-content'), 'Device Detail Sheet fell back to the legacy Drawer.');
report.surfaces.inspector = { deviceId: firstDeviceId, deviceName: inventory.devices[0]?.displayName ?? null };
report.screenshots.push(screenshot(inspectorURL, '02-device-detail-sheet.png'));
pass('Device Detail Sheet renders business labels without raw telemetry enums');

const anomalyURL = `${monitorURL}?view=anomaly`;
const anomalyDom = dumpDom(anomalyURL);
const anomalyText = visibleText(anomalyDom);
const anomalyBody = visibleDom(anomalyDom);
assert(anomalyBody.includes('data-view="anomaly"'), 'Anomaly workspace did not activate.');
assert(anomalyText.includes('异常对象与系统位置'), 'Anomaly workspace lost its spatial investigation purpose.');
assert(anomalyBody.includes('data-testid="hvac-x6-topology"'), 'Anomaly workspace did not preserve the shared X6 topology canvas.');
assert(['冷机组', '冷冻水泵', 'AHU', 'FCU', '冷却水泵', '冷却塔'].every((label) => anomalyText.includes(label)), 'Anomaly workspace lost one or more HVAC engineering stages.');
assert(anomalyBody.includes('control-monitor-evidence-dock'), 'Anomaly workspace lost the Evidence Dock.');
report.surfaces.anomaly = { topologyNodes: countClass(anomalyDom, 'hvac-x6-node') };
report.screenshots.push(screenshot(anomalyURL, '03-anomaly-canvas.png'));
pass('Anomaly view preserves spatial memory and shared evidence structure');

const energyURL = `${monitorURL}?view=energy`;
const energyDom = dumpDom(energyURL);
const energyText = visibleText(energyDom);
const energyBody = visibleDom(energyDom);
assert(energyBody.includes('data-view="energy"'), 'Energy evidence workspace did not activate.');
assert(countClass(energyDom, 'control-monitor-energy__node') === 4, 'Energy evidence chain must contain four measured/aggregated stages.');
assert(countClass(energyDom, 'control-monitor-energy__switch') === 1, 'Energy evidence dimension switch is missing.');
assert(['制冷', '电力', '水力'].every((label) => energyText.includes(label)), 'Energy evidence dimensions are incomplete.');
assert(energyText.includes('线宽不表达未经验证的流量或能量分配'), 'Energy evidence lost the anti-fabrication statement.');
assert(!energyBody.includes('hvac-monitor__energy-flow-visual'), 'Superseded standalone energy-flow visual returned.');
report.surfaces.energy = { evidenceNodes: countClass(energyDom, 'control-monitor-energy__node') };
report.screenshots.push(screenshot(energyURL, '04-energy-evidence.png'));
pass('Energy evidence renders measured facts without fabricated allocation semantics');

await writeFile(join(outputRoot, 'report.json'), JSON.stringify(report, null, 2));
console.log(`HVAC WSL visual audit passed with ${report.assertions.length} assertions.`);
console.log(`Evidence: ${outputRoot}`);
