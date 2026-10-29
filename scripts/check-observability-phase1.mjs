import { readFile, readdir } from 'node:fs/promises';
import { extname, join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const read = async (path) => readFile(resolve(root, path), 'utf8');
const json = async (path) => JSON.parse(await read(path));

async function collectText(directory) {
  const absolute = resolve(root, directory);
  const entries = await readdir(absolute, { withFileTypes: true });
  const parts = [];
  for (const entry of entries) {
    const path = join(absolute, entry.name);
    if (entry.isDirectory()) {
      parts.push(await collectText(path));
      continue;
    }
    if (!['.go', '.mjs', '.json', '.yaml', '.yml', '.md'].includes(extname(entry.name))) continue;
    parts.push(await readFile(path, 'utf8'));
  }
  return parts.join('\n');
}

const catalog = await json('deploy/observability/phase1-metric-catalog.v1.json');
if (catalog.schemaVersion !== 1 || catalog.scope !== 'observability-phase1' || !Array.isArray(catalog.families) || catalog.families.length < 20) {
  throw new Error('Phase 1 observability metric catalog is incomplete');
}
const forbidden = new Set(catalog.forbiddenLabels ?? []);
const bounded = catalog.boundedValues ?? {};
const seen = new Set();
for (const family of catalog.families) {
  if (!/^hvac_(?:command|alarm|mqtt|edge)_[a-z0-9_]+$/.test(family.name)) throw new Error(`invalid metric name: ${family.name}`);
  if (seen.has(family.name)) throw new Error(`duplicate metric family: ${family.name}`);
  seen.add(family.name);
  if (!['counter', 'gauge', 'histogram'].includes(family.type)) throw new Error(`invalid metric type: ${family.name}`);
  if (family.type === 'counter' && !family.name.endsWith('_total')) throw new Error(`counter must use _total: ${family.name}`);
  if (family.type === 'histogram' && !family.name.endsWith('_seconds')) throw new Error(`histogram must use _seconds: ${family.name}`);
  if (!Number.isInteger(family.seriesBudget) || family.seriesBudget < 1 || family.seriesBudget > 512) throw new Error(`invalid series budget: ${family.name}`);
  for (const label of family.labels ?? []) {
    if (forbidden.has(label)) throw new Error(`forbidden high-cardinality label ${label} in ${family.name}`);
    if (!Array.isArray(bounded[label]) || bounded[label].length === 0) throw new Error(`label ${label} has no bounded value catalog`);
  }
}

const [commandSource, moduleSource, serviceSource] = await Promise.all([
  collectText('cmd'),
  collectText('modules'),
  collectText('services'),
]);
const edgeSource = await collectText('tools/eg8200-simulator');
const observabilitySource = await collectText('libs/observability');
const dashboardFiles = [
  'infra/observability/dashboards/iot-edge-phase1.json',
  'infra/observability/dashboards/control-safety-phase1.json',
  'infra/observability/dashboards/alarm-phase1.json',
  'infra/observability/dashboards/s2-telemetry.json',
];
let dashboardText = '';
for (const path of dashboardFiles) {
  const dashboard = await json(path);
  if (!dashboard.title || !Array.isArray(dashboard.panels) || dashboard.panels.length < 1) throw new Error(`invalid dashboard: ${path}`);
  dashboardText += JSON.stringify(dashboard);
}
const alertText = `${await read('infra/observability/alerts/domain-phase1.yaml')}\n${await read('infra/observability/alerts/s2-telemetry.yaml')}`;
const allImplementationText = `${commandSource}\n${moduleSource}\n${serviceSource}\n${edgeSource}\n${observabilitySource}\n${dashboardText}\n${alertText}`;
for (const family of catalog.families) {
  if (!allImplementationText.includes(family.name)) throw new Error(`metric catalog family is not referenced by implementation or operations assets: ${family.name}`);
}
for (const label of forbidden) {
  const prometheusLabelPattern = new RegExp(`${label}\\s*=`, 'i');
  if (prometheusLabelPattern.test(`${dashboardText}\n${alertText}`)) throw new Error(`operations query uses forbidden high-cardinality label: ${label}`);
}

for (const alert of [
  'EdgeMQTTPublisherDisconnected',
  'EdgeStoreForwardQueueHigh',
  'EdgeStoreForwardQueueRejected',
  'MQTTTelemetryAdapterDisconnected',
  'MQTTProcessingQueueHigh',
  'MQTTTelemetryDropped',
  'CommandVerificationFailureRateHigh',
  'CommandVerificationTimeoutRateHigh',
  'AlarmAPI5xxHigh',
  'S2DataGoodRateLow',
]) {
  if (!alertText.includes(`alert: ${alert}`)) throw new Error(`required Phase 1 alert is missing: ${alert}`);
}
// Every alert has a runbook link, and that link lands on a heading (GitHub anchors).
const anchorOf = (heading) => heading.trim().toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');
const alertFiles = (await readdir(resolve(root, 'infra/observability/alerts'))).filter((name) => name.endsWith('.yaml'));
const alertNames = [];
for (const name of alertFiles) {
  const alerts = await read(`infra/observability/alerts/${name}`);
  for (const block of alerts.split(/(?=      - alert: )/)) {
    const alertName = block.match(/- alert: (\S+)/)?.[1];
    if (!alertName) continue;
    alertNames.push(alertName);
    if (!/runbook:\s*\S+\.md#\S+/.test(block)) throw new Error(`${name}: ${alertName} is missing a runbook link`);
  }
  for (const [, document, anchor] of alerts.matchAll(/runbook:\s*(\S+\.md)#(\S+)/g)) {
    const headings = [...(await read(document)).matchAll(/^#{1,6}\s+(.+)$/gm)].map(([, heading]) => anchorOf(heading));
    if (!headings.includes(anchor)) throw new Error(`${name}: runbook link ${document}#${anchor} has no matching heading`);
  }
}
for (const alert of [
  'Phase1HostDiskUsageHigh',
  'Phase1HostDiskWillFillWithin24Hours',
  'Phase1HostMemoryAvailableLow',
  'Phase1HostSwapPressure',
  'Phase1ContainerRestartLoop',
  'Phase1ContainerUnhealthy',
  'Phase1BackupTooOld',
  'Phase1TlsCertificateExpiring',
  'Phase1PostgresDown',
  'Phase1ClickHouseDown',
  'Phase1RedisDown',
  'Phase1MqttDown',
  'Phase1PublicEndpointDown',
  'Phase1CoreProcessDown',
]) {
  if (!alertNames.includes(alert)) throw new Error(`required alert is missing: ${alert}`);
}
const alertmanager = await read('deploy/platform/phase1/observability/alertmanager.yaml');
if (!alertmanager.includes('receiver: none')) throw new Error('the committed Alertmanager config must keep the empty receiver');
const renderer = await read('deploy/platform/phase1/observability/render-alertmanager-config.sh');
if (!renderer.includes('wecom-webhook-url') || !renderer.includes('smtp-password') || !renderer.includes('cp "$template"')) {
  throw new Error('alertmanager renderer must support WeCom, SMTP, and the unconfigured template');
}

console.log(`Observability Phase 1 check passed: metricFamilies=${catalog.families.length}, dashboards=${dashboardFiles.length}`);
