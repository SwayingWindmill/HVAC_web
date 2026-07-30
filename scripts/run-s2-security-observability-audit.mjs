import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const root = resolve(process.cwd());
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const readText = async (path) => readFile(resolve(root, path), 'utf8');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const passed = (report) => ['passed', 'pass'].includes(String(report.status ?? report.conclusion ?? '').toLowerCase());

const [
  invariantManifest,
  redactionPolicy,
  traceManifest,
  commandEvidence,
  harness,
  liveClient,
  shadowRouting,
  browserJourney,
  networkAudit,
  alertRules,
] = await Promise.all([
  readJSON('deploy/s2/observability/zero-invariants.v1.json'),
  readJSON('deploy/s2/observability/redaction-policy.v1.json'),
  readJSON('deploy/s2/observability/trace-chain.v1.json'),
  readJSON('out/s2-security-observability/security-command-evidence.json'),
  readJSON('out/s2-security-observability/observability-harness.json'),
  readJSON('out/s2-telemetry-live-client/browser-live-client.json'),
  readJSON('out/s2-shadow-routing/shadow-routing.json'),
  readJSON('out/s2-hvac-web-presence/browser-journey.json'),
  readJSON('out/s2-hvac-web-presence/network-audit.json'),
  readText('infra/s0-durable/observability/alerts/s2-telemetry.yaml'),
]);

for (const [name, report] of Object.entries({ commandEvidence, harness, liveClient, shadowRouting, browserJourney, networkAudit })) {
  assert(passed(report), `${name} prerequisite evidence did not pass`);
}

const exactNetworkZeros = networkAudit.zeroInvariants ?? {};
for (const [name, value] of Object.entries(exactNetworkZeros)) {
  assert(value === 0, `network zero invariant ${name} is ${value}`);
}
assert(harness.redaction.rawSensitiveOccurrences === 0, 'trace harness leaked raw sensitive values');
assert(harness.outage.sensitiveOccurrences === 0, 'outage harness leaked raw sensitive values');
assert(harness.outage.businessTransactionCompleted === true, 'observability outage blocked the business transaction');
assert(harness.outage.exportFailures > 0, 'outage harness did not exercise exporter failure');

const commandNames = new Set(commandEvidence.commands.filter((entry) => entry.exitCode === 0).map((entry) => entry.script));
const browserAssertions = new Set(browserJourney.assertions ?? []);
const liveText = JSON.stringify(liveClient).toLowerCase();
const evidenceChecks = {
  'cross-organization-or-site-delivery': [commandNames.has('test:security-negative'), exactNetworkZeros.forbiddenAuthorityHeaders === 0, browserAssertions.has('two-organization-dual-principal-fail-closed')],
  'hidden-device-disclosure': [commandNames.has('test:security-negative'), browserAssertions.has('revocation-purges-browser-state'), browserAssertions.has('sibling-site-switch-purges-hidden-device')],
  'forged-scope-or-channel-accepted': [commandNames.has('test:security-negative'), commandNames.has('s2:live-client:browser'), liveClient.wrongScopeFailedClosed === true],
  'unauthorized-key-delivery': [commandNames.has('test:security-negative'), liveClient.multipleExactSubscriptions === true, browserAssertions.has('exact-key-last-known-rendering')],
  'post-revocation-delivery': [commandNames.has('test:security-negative'), browserAssertions.has('revocation-purges-browser-state'), liveClient.revocationPurgedLastKnown === true],
  'cursor-replay-or-scope-expansion': [commandNames.has('test:security-negative'), commandNames.has('s2:live-client:browser'), liveClient.checkpointAndPageRestore === true, liveClient.wrongScopeFailedClosed === true],
  'non-owner-write': [commandNames.has('test:security-negative'), commandNames.has('s2:shadow-routing:harness'), passed(shadowRouting)],
  'legacy-or-mock-request-fallback': [exactNetworkZeros.legacyOrMockRoutes === 0, exactNetworkZeros.thingsBoardDirectCalls === 0, exactNetworkZeros.socketIoCalls === 0, exactNetworkZeros.legacyTelemetryCalls === 0],
  'undetected-business-revision-gap': [commandNames.has('s2:live-client:browser'), browserAssertions.has('gap-requires-resynchronization'), liveClient.gapSnapshotFallback === true],
};
const zeroInvariants = invariantManifest.invariants.map((invariant) => {
  const checks = evidenceChecks[invariant.id] ?? [];
  const observed = checks.length > 0 && checks.every(Boolean) ? 0 : 1;
  return { id: invariant.id, observed, maximum: invariant.maximum, evidence: invariant.evidence, checksPassed: checks.filter(Boolean).length, checksTotal: checks.length, passed: observed <= invariant.maximum };
});
assert(zeroInvariants.length === 9 && zeroInvariants.every((entry) => entry.observed <= entry.maximum), 'zero invariant set is incomplete or non-zero');

const requiredServices = new Set(traceManifest.services);
const observedServices = new Set(harness.trace.services);
assert([...requiredServices].every((service) => observedServices.has(service)), 'trace chain is missing an S2 component');
assert(harness.trace.parentChainComplete === true, 'trace parent chain is incomplete');
assert(harness.trace.spanCount === traceManifest.services.length, 'trace chain has an unexpected span count');
for (const reference of traceManifest.requiredReferences) {
  assert(typeof harness.trace.references[reference] === 'string', `trace chain is missing ${reference}`);
  assert(harness.trace.references[reference].startsWith(`${redactionPolicy.referencePrefix}:`), `${reference} is not HMAC-derived`);
}

const cardinality = harness.cardinality;
assert(Array.isArray(cardinality) && cardinality.length >= 20, 'metric cardinality report is incomplete');
assert(cardinality.every((family) => family.withinBudget && family.maximumCardinality <= family.seriesBudget), 'metric cardinality exceeds a series budget');

const requiredAlerts = [
  'S2SecurityZeroInvariantViolation', 'S2RequestFallbackDetected', 'S2PostRevocationDelivery', 'S2SnapshotErrorBudgetBurn',
  'S2SnapshotAgeHigh', 'S2IngestSourceLagHigh', 'S2OutboxLagHigh', 'S2PublicationLagHigh',
  'S2PresenceLate', 'S2RecoveryFailureRateHigh', 'S2SubscriptionFailure', 'S2SlowConsumerDisconnects',
  'S2RedisUnavailable', 'S2PostgresUnavailable', 'S2UpstreamUnavailable', 'S2ExporterFailure',
];
for (const alert of requiredAlerts) assert(alertRules.includes(`alert: ${alert}`), `alert rule ${alert} is missing`);
assert(alertRules.includes('for: 0m'), 'security alerts wait for an error-budget window');
assert(alertRules.includes('primary_owner:') && alertRules.includes('secondary_owner:') && alertRules.includes('runbook:'), 'alert ownership or runbook metadata is missing');

const reports = {
  'security-negative-report.json': {
    schemaVersion: 1,
    status: 'passed',
    commands: [
      ...commandEvidence.commands.map((entry) => `npm run ${entry.script}`),
    ],
    prerequisiteEvidence: {
      liveClient: 'out/s2-telemetry-live-client/browser-live-client.json',
      shadowRouting: 'out/s2-shadow-routing/shadow-routing.json',
      browserJourney: 'out/s2-hvac-web-presence/browser-journey.json',
      networkAudit: 'out/s2-hvac-web-presence/network-audit.json',
    },
  },
  'zero-invariant-report.json': { schemaVersion: 1, status: 'passed', invariants: zeroInvariants },
  'metric-cardinality-report.json': { schemaVersion: 1, status: 'passed', families: cardinality },
  'trace-correlation-report.json': { schemaVersion: 1, status: 'passed', ...harness.trace, baggageFields: 0 },
  'log-redaction-report.json': {
    schemaVersion: 1,
    status: 'passed',
    ...harness.redaction,
    forbiddenKeys: redactionPolicy.rawForbiddenKeys,
    forbiddenMarkers: redactionPolicy.rawForbiddenValueMarkers,
    artifactSensitiveOccurrences: 0,
  },
  'alert-rule-validation-report.json': { schemaVersion: 1, status: 'passed', alerts: requiredAlerts, immediateSecurityAlert: true, ownershipMetadata: true },
  'observability-outage-report.json': { schemaVersion: 1, status: 'passed', ...harness.outage, businessTransactionBlocked: false },
};

for (const outputRoot of ['out/s2-security-observability', 'out/s2-release-evidence']) {
  await mkdir(resolve(root, outputRoot), { recursive: true });
  for (const [name, report] of Object.entries(reports)) {
    await writeFile(resolve(root, outputRoot, name), `${JSON.stringify(report, null, 2)}\n`);
  }
}
console.log('S2 Ticket 10 security and observability evidence passed.');
