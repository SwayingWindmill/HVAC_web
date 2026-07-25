import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const output = resolve(root, 'out/s2-ticket-10/static-assets.json');
const text = async (path) => readFile(resolve(root, path), 'utf8');
const json = async (path) => JSON.parse(await text(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [catalog, invariants, redaction, trace, securityGo, metricsGo, harness, alerts, dashboard, negativeRunner, audit, runbook, workflow, packageJSON] = await Promise.all([
  json('deploy/s2/observability/metric-catalog.v1.json'),
  json('deploy/s2/observability/zero-invariants.v1.json'),
  json('deploy/s2/observability/redaction-policy.v1.json'),
  json('deploy/s2/observability/trace-chain.v1.json'),
  text('libs/observability/s2_security.go'),
  text('libs/observability/metrics.go'),
  text('libs/observability/cmd/s2-ticket-10-harness/main.go'),
  text('infra/s0-durable/observability/alerts/s2-telemetry.yaml'),
  text('infra/s0-durable/observability/dashboards/s2-telemetry.json'),
  text('scripts/run-s2-security-negative.mjs'),
  text('scripts/run-s2-security-observability-audit.mjs'),
  text('docs/operations/s2-security-observability.md'),
  text('.github/workflows/s2-security-observability.yml'),
  json('package.json'),
]);

assert(catalog.schemaVersion === 1 && catalog.namespace === 'hvac_s2', 'metric catalog version or namespace drifted');
assert(catalog.families.length >= 20, 'required S2 metric families are incomplete');
const names = new Set(catalog.families.map((family) => family.name));
for (const marker of ['ingest', 'source_lag', 'presence', 'snapshot', 'publication', 'recovery', 'subscription', 'revocation', 'outbox', 'quarantine', 'upstream', 'exporter']) {
  assert([...names].some((name) => name.includes(marker)), `metric family ${marker} is missing`);
}
for (const family of catalog.families) {
  assert(family.seriesBudget > 0, `metric ${family.name} has no series budget`);
  assert(family.labels.every((label) => catalog.labelPolicy.allowed.includes(label)), `metric ${family.name} uses a non-allowlisted label`);
  assert(family.labels.every((label) => !catalog.labelPolicy.forbidden.includes(label)), `metric ${family.name} uses a forbidden label`);
}
for (const forbidden of ['organization_id', 'site_id', 'device_id', 'subscription_id', 'recovery_cursor', 'business_revision', 'telemetry_key', 'value', 'token', 'channel']) {
  assert(catalog.labelPolicy.forbidden.includes(forbidden), `metric label denylist is missing ${forbidden}`);
}
assert(invariants.invariants.length === 9 && invariants.invariants.every((entry) => entry.maximum === 0), 'zero invariant manifest is incomplete');
assert(redaction.minimumKeyBytes >= 32 && redaction.rawForbiddenKeys.length >= 10, 'redaction policy is weak');
assert(trace.services.length === 7 && trace.rawIdentifiersAllowed === false && trace.baggageAllowed === false, 'trace chain policy drifted');
for (const marker of ['NewReferenceHasher', 'ValidateS2MetricCatalog', 'ValidateOperationalRecord', 'HMACOperationalReferences']) {
  assert(securityGo.includes(marker), `observability security library is missing ${marker}`);
}
for (const marker of ['device_id', 'subscription_id', 'recovery_cursor', 'business_revision', 'telemetry_key', 'channel', 'csrf']) {
  assert(metricsGo.includes(marker), `global metric label denylist is missing ${marker}`);
}
for (const marker of ['platform-gateway', 'iam-service', 'telemetry-runtime-service', 'outbox-relay', 'centrifugo-api', 'telemetry-live-client', 'audit-ledger-service', 'NewAsyncExporter']) {
  assert(harness.includes(marker), `observability harness is missing ${marker}`);
}
for (const marker of ['S2SecurityZeroInvariantViolation', 'S2RequestFallbackDetected', 'for: 0m', 'primary_owner:', 'secondary_owner:', 'runbook:', 'S2ExporterFailure']) {
  assert(alerts.includes(marker), `S2 alert rules are missing ${marker}`);
}
assert(dashboard.includes('S2 Telemetry Security and Reliability') && dashboard.includes('hvac_s2_'), 'S2 dashboard is incomplete');
for (const marker of ['test:security-negative', 's2:live-client:browser', 's2:shadow-routing:harness', 's2:hvac-web:browser', 'security-command-evidence.json']) {
  assert(negativeRunner.includes(marker), `security-negative runner is missing ${marker}`);
}
for (const report of ['security-negative-report.json', 'zero-invariant-report.json', 'metric-cardinality-report.json', 'trace-correlation-report.json', 'log-redaction-report.json', 'alert-rule-validation-report.json', 'observability-outage-report.json']) {
  assert(audit.includes(report), `audit runner does not generate ${report}`);
}
for (const marker of ['HMAC', '低基数', 'collector', 'security zero invariant', 'npm run s2:ticket-10']) {
  assert(runbook.toLowerCase().includes(marker.toLowerCase()), `Runbook is missing ${marker}`);
}
for (const marker of ['npm run s2:ticket-10', 'out/s2-ticket-10', 'if-no-files-found: error', 'go-version: "1.25.12"', 'prom/prometheus:v2.54.1', 'promtool check rules']) {
  assert(workflow.includes(marker), `Ticket 10 workflow is missing ${marker}`);
}
for (const script of ['s2:security-observability:check', 's2:security-negative', 's2:observability:harness', 's2:ticket-10']) {
  assert(packageJSON.scripts?.[script], `package script ${script} is missing`);
}

await mkdir(dirname(output), { recursive: true });
await writeFile(output, `${JSON.stringify({ schemaVersion: 1, ticket: 69, status: 'passed', metricFamilies: catalog.families.length, zeroInvariants: invariants.invariants.length, traceServices: trace.services.length }, null, 2)}\n`);
console.log(`S2 Ticket 10 static assets passed: ${output}`);
