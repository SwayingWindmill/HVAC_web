import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const [collector, prometheus, alerts, runbook, dashboardText] = await Promise.all([
  read('infra/s0-durable/otel-collector-config.yaml'),
  read('infra/s0-durable/prometheus.yaml'),
  read('infra/s0-durable/observability/alerts/s0-platform.yaml'),
  read('docs/operations/s0-observability.md'),
  read('infra/s0-durable/observability/dashboards/s0-platform.json'),
]);

for (const marker of ['otlp:', 'memory_limiter:', 'batch:', 'traces:', 'metrics:', 'prometheus:']) {
  assert(collector.includes(marker), `Collector config is missing ${marker}`);
}
assert(prometheus.includes('/etc/prometheus/alerts/*.yaml'), 'Prometheus does not load S0 alert rules');
assert(prometheus.includes('host.docker.internal:19080'), 'Prometheus does not scrape Gateway diagnostics');
assert(prometheus.includes('host.docker.internal:19083'), 'Prometheus does not scrape IAM diagnostics');

const alertNames = [...alerts.matchAll(/^\s*- alert: (\S+)/gm)].map((match) => match[1]);
assert(alertNames.length >= 4, 'Expected at least four active S0 observability alerts');
for (const alertName of alertNames) {
  const start = alerts.indexOf(`- alert: ${alertName}`);
  const next = alerts.indexOf('- alert:', start + 1);
  const block = alerts.slice(start, next === -1 ? undefined : next);
  for (const field of ['severity:', 'primary_owner:', 'secondary_owner:', 'runbook:']) {
    assert(block.includes(field), `${alertName} is missing ${field}`);
  }
}

for (const heading of ['## Outbox stuck', '## Audit ingestion lag', '## Collector unavailable', '## Secret-absence verification']) {
  assert(runbook.includes(heading), `Runbook is missing ${heading}`);
}

const dashboard = JSON.parse(dashboardText);
const panelTitles = new Set((dashboard.panels ?? []).map((panel) => panel.title));
for (const title of ['Public API failures', 'Internal identity failures', 'Async Audit lag', 'Telemetry pressure']) {
  assert(panelTitles.has(title), `Dashboard is missing ${title}`);
}

console.log('S0 observability assets are internally consistent.');
