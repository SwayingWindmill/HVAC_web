import { createHash } from 'node:crypto';

export const OPERATIONS_AGENT_SAFETY_CERTIFICATION_VERSION = 'operations-agent-safety-certification/v1';
export const OPERATIONS_AGENT_SAFETY_CERTIFICATION_NAME = 'MAP_5_5_NEGATIVE_AUTHORIZATION_AND_RECOVERY';

export const OPERATIONS_AGENT_SAFETY_INVARIANTS = Object.freeze([
  'tenantIsolation',
  'idempotency',
  'restartSafety',
  'boundedFailureOutcomes',
]);

export const OPERATIONS_AGENT_SAFETY_GATES = Object.freeze([
  'gateway-contract',
  'gateway-negative',
  'agent-service',
  'operations-postgres',
  'durable-postgres',
  'workspace-unit',
  'workspace-browser',
]);

const evidence = (gate, marker, sourceArtifact = null) => Object.freeze({ gate, marker, sourceArtifact });
const invariant = (status, evidenceReferences = []) => Object.freeze({ status, evidence: Object.freeze(evidenceReferences) });

export const OPERATIONS_AGENT_SAFETY_SCENARIOS = Object.freeze([
  Object.freeze({
    id: 'authorization-negative-complete-boundary',
    category: 'AUTHORIZATION_NEGATIVE',
    description: 'Wrong tenant or Site, revoked or expired grants, presenter mismatch and Owner-grant mismatch remain nondiscoverable.',
    requirements: Object.freeze([
      evidence('gateway-negative', 'TestOperationsGatewayEnforcesSessionCSRFScopeAndServiceDelegation'),
      evidence('gateway-negative', 'TestOperationsGatewayDenialIsNondiscoverableAndRateLimited'),
      evidence('gateway-negative', 'TestOperationsToolAuthorizationFailsClosedBeforeIAM'),
      evidence('gateway-negative', 'TestRegistryGrantFailsClosedForBoundaryAndFreshnessChanges/wrong_presenter'),
      evidence('gateway-negative', 'TestRegistryGrantFailsClosedForBoundaryAndFreshnessChanges/expired'),
      evidence('gateway-negative', 'TestRegistryGrantFailsClosedForBoundaryAndFreshnessChanges/stale_policy'),
      evidence('gateway-negative', 'TestRegistryGrantFailsClosedForBoundaryAndFreshnessChanges/revoked'),
      evidence('gateway-negative', 'TestHandlerRejectsExpiredDelegation'),
      evidence('gateway-negative', 'TestHandlerFailsClosedForScopeMismatchAndForgedHeaders/scope_changed_after_grant'),
      evidence('agent-service', 'internal HTTP authorization denial is nondiscoverable and malformed requests fail before use cases'),
      evidence('agent-service', 'Owner readers reject timeout, malformed JSON, missing authorization and cross-Scope identities'),
      evidence('workspace-unit', 'scoped Operations API accepts the authorized stream and rejects a mismatched Organization'),
      evidence('workspace-browser', 'nondiscoverable-stable-no-retry', 'out/operations-reconnect-certification/browser-evidence.json'),
    ]),
    invariants: Object.freeze({
      tenantIsolation: invariant('PROVEN', ['wrong-organization-site', 'nondiscoverable-public-result']),
      idempotency: invariant('NOT_APPLICABLE'),
      restartSafety: invariant('NOT_APPLICABLE'),
      boundedFailureOutcomes: invariant('PROVEN', ['typed-denial', 'no-owner-read-after-denial']),
    }),
  }),
  Object.freeze({
    id: 'retry-exactly-once-durable-outcomes',
    category: 'RETRY_AND_DUPLICATE_DELIVERY',
    description: 'Ambiguous responses and repeated public, AG-UI, Operator Input, business-effect and Audit delivery attempts stay exactly once.',
    requirements: Object.freeze([
      evidence('agent-service', 'business effects are serialized and exact retries do not save twice'),
      evidence('agent-service', 'internal HTTP accepts one bounded Operator Input and exact retry is inert'),
      evidence('operations-postgres', 'PostgreSQL Audit outbox deduplicates content and survives lease, retry, and delivery'),
      evidence('operations-postgres', 'PostgreSQL commits Operator Input atomically and exact retry survives restart'),
      evidence('workspace-unit', 'Headless Operations agent reconnects after interruption without duplicating durable Tool records'),
      evidence('workspace-browser', 'retryable-interruption-visible-and-recovered', 'out/operations-reconnect-certification/browser-evidence.json'),
      evidence('workspace-browser', 'operator-input-exact-retry-same-run', 'out/operations-reconnect-certification/browser-evidence.json'),
    ]),
    invariants: Object.freeze({
      tenantIsolation: invariant('NOT_APPLICABLE'),
      idempotency: invariant('PROVEN', ['operator-input', 'business-effect', 'audit-outbox', 'ag-ui-event']),
      restartSafety: invariant('PROVEN', ['exact-retry-after-restart']),
      boundedFailureOutcomes: invariant('PROVEN', ['ambiguous-response-retry', 'no-duplicate-records']),
    }),
  }),
  Object.freeze({
    id: 'restart-authoritative-state-recovery',
    category: 'PROCESS_AND_CHECKPOINT_RECOVERY',
    description: 'Checkpoint recovery or loss and process restart before or after commit rebuild from authoritative durable state.',
    requirements: Object.freeze([
      evidence('operations-postgres', 'PostgreSQL restart resumes the LangGraph Runtime at the next logical Step'),
      evidence('operations-postgres', 'PostgreSQL keeps Operations facts authoritative and Checkpoints independently disposable'),
      evidence('operations-postgres', 'PostgreSQL atomically persists typed records across restart, retry, and rollback'),
      evidence('operations-postgres', 'PostgreSQL resumes a checkpointed night-energy Run without duplicate business effects'),
      evidence('agent-service', 'a saved Runtime Checkpoint without receipts recovers by replaying only the fixed Registry reads'),
      evidence('workspace-browser', 'cancel-terminal-reload-and-route-leave-purge', 'out/operations-reconnect-certification/browser-evidence.json'),
    ]),
    invariants: Object.freeze({
      tenantIsolation: invariant('NOT_APPLICABLE'),
      idempotency: invariant('PROVEN', ['post-commit-restart', 'checkpoint-replay']),
      restartSafety: invariant('PROVEN', ['checkpoint-present', 'checkpoint-lost', 'pre-commit', 'post-commit', 'terminal-reload']),
      boundedFailureOutcomes: invariant('PROVEN', ['rollback-without-partial-records']),
    }),
  }),
  Object.freeze({
    id: 'concurrency-single-writer-authority',
    category: 'CONCURRENT_MUTATION',
    description: 'Stale Revision, stale or expired Lease, repository CAS conflict and simultaneous mutation preserve one writer and one outcome.',
    requirements: Object.freeze([
      evidence('agent-service', 'stale revisions, mismatched leases, and expired leases are rejected independently'),
      evidence('agent-service', 'the Fake Repository exposes deterministic transaction conflicts through the Coordinator'),
      evidence('agent-service', 'business effects are serialized and exact retries do not save twice'),
      evidence('agent-service', 'simultaneous Operator Input acceptance commits one authoritative outcome'),
      evidence('operations-postgres', 'PostgreSQL Run budget survives restart, ignores Checkpoint deletion, and serializes concurrency'),
    ]),
    invariants: Object.freeze({
      tenantIsolation: invariant('NOT_APPLICABLE'),
      idempotency: invariant('PROVEN', ['single-writer', 'stable-operation-identity']),
      restartSafety: invariant('PROVEN', ['durable-cas', 'monotonic-revision']),
      boundedFailureOutcomes: invariant('PROVEN', ['typed-stale-revision', 'typed-stale-lease', 'typed-conflict']),
    }),
  }),
  Object.freeze({
    id: 'stream-recovery-authoritative-rebuild',
    category: 'STREAM_RECOVERY',
    description: 'Valid suffix replay and invalid recovery positions always rebuild from committed state without duplicate durable projections.',
    requirements: Object.freeze([
      evidence('agent-service', 'valid recovery positions replay only the committed missing suffix after a fresh snapshot'),
      evidence('agent-service', 'unknown expired and conflicting recovery positions fall back to a full authoritative snapshot'),
      evidence('workspace-unit', 'Operations recovery positions persist only opaque scoped cursors in session storage'),
      evidence('workspace-unit', 'Headless Operations agent restores a scoped cursor on the first request after reload'),
      evidence('workspace-unit', 'Headless Operations agent does not retry a nondiscoverable Investigation'),
      evidence('workspace-browser', 'stored-last-event-id-survives-page-reload-and-retry', 'out/operations-reconnect-certification/browser-evidence.json'),
      evidence('workspace-browser', 'typed-provenance-and-site-only-finding', 'out/operations-reconnect-certification/browser-evidence.json'),
    ]),
    invariants: Object.freeze({
      tenantIsolation: invariant('PROVEN', ['organization-site-investigation-scoped-cursor', 'protected-site-purge']),
      idempotency: invariant('PROVEN', ['event-identity-deduplication', 'authoritative-snapshot-replacement']),
      restartSafety: invariant('PROVEN', ['page-reload', 'reconnect', 'terminal-reload']),
      boundedFailureOutcomes: invariant('PROVEN', ['unknown', 'future', 'expired', 'out-of-range', 'partial-tool']),
    }),
  }),
]);

const stableValue = (value) => {
  if (Array.isArray(value)) return value.map(stableValue);
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableValue(value[key])]));
  }
  return value;
};
const stableJson = (value) => JSON.stringify(stableValue(value));
export const sha256Hex = (value) => createHash('sha256').update(value).digest('hex');

const markerPresent = (gateResult, requirement, browserEvidence) => {
  if (!gateResult?.passed) return false;
  if (requirement.sourceArtifact?.endsWith('browser-evidence.json')) {
    return browserEvidence?.passed === true
      && Array.isArray(browserEvidence.assertions)
      && browserEvidence.assertions.includes(requirement.marker);
  }
  return `${gateResult.stdout ?? ''}\n${gateResult.stderr ?? ''}`.includes(requirement.marker);
};

export function buildOperationsAgentSafetyCertificationReport({
  repositorySha,
  startedAt,
  completedAt,
  gateResults,
  browserEvidence,
}) {
  const scenarios = OPERATIONS_AGENT_SAFETY_SCENARIOS.map((scenario) => {
    const evidenceResults = scenario.requirements.map((requirement) => ({
      ...requirement,
      present: markerPresent(gateResults[requirement.gate], requirement, browserEvidence),
    }));
    return {
      id: scenario.id,
      category: scenario.category,
      description: scenario.description,
      passed: evidenceResults.every(({ present }) => present),
      invariants: scenario.invariants,
      evidence: evidenceResults,
    };
  });
  const gates = OPERATIONS_AGENT_SAFETY_GATES.map((id) => {
    const gate = gateResults[id];
    return {
      id,
      passed: gate?.passed === true,
      command: gate?.command ?? null,
      durationMs: gate?.durationMs ?? null,
      exitCode: gate?.exitCode ?? null,
      signal: gate?.signal ?? null,
      stdoutPath: gate?.stdoutPath ?? null,
      stderrPath: gate?.stderrPath ?? null,
      stdoutSha256: gate?.stdoutSha256 ?? null,
      stderrSha256: gate?.stderrSha256 ?? null,
    };
  });
  const invariantCoverage = Object.fromEntries(OPERATIONS_AGENT_SAFETY_INVARIANTS.map((name) => [
    name,
    scenarios.filter((scenario) => scenario.invariants[name]?.status === 'PROVEN').map(({ id }) => id),
  ]));
  const passed = gates.every((gate) => gate.passed)
    && scenarios.every((scenario) => scenario.passed)
    && Object.values(invariantCoverage).every((scenarioIds) => scenarioIds.length > 0);
  return {
    schemaVersion: 1,
    certificationVersion: OPERATIONS_AGENT_SAFETY_CERTIFICATION_VERSION,
    certification: OPERATIONS_AGENT_SAFETY_CERTIFICATION_NAME,
    repositorySha,
    startedAt,
    completedAt,
    passed,
    formalProductionClaim: false,
    productionTrafficPercent: 0,
    authoritativeStateOwner: 'operations-agent-service-postgresql',
    gates,
    invariantCoverage,
    scenarios,
    browserEvidence: {
      schemaVersion: browserEvidence?.schemaVersion ?? null,
      passed: browserEvidence?.passed === true,
      assertions: Array.isArray(browserEvidence?.assertions) ? browserEvidence.assertions : [],
      digest: browserEvidence ? sha256Hex(stableJson(browserEvidence)) : null,
    },
    checksumManifest: 'SHA256SUMS',
  };
}

export function validateOperationsAgentSafetyCertificationReport(report) {
  const failures = [];
  if (report?.schemaVersion !== 1) failures.push('schemaVersion must be 1');
  if (report?.certificationVersion !== OPERATIONS_AGENT_SAFETY_CERTIFICATION_VERSION) failures.push('certificationVersion is invalid');
  if (report?.certification !== OPERATIONS_AGENT_SAFETY_CERTIFICATION_NAME) failures.push('certification name is invalid');
  if (!/^[0-9a-f]{40}$/u.test(report?.repositorySha ?? '')) failures.push('repositorySha must be a lowercase 40-character SHA');
  if (report?.formalProductionClaim !== false || report?.productionTrafficPercent !== 0) failures.push('certification must not claim or enable production traffic');
  if (report?.authoritativeStateOwner !== 'operations-agent-service-postgresql') failures.push('authoritative state owner is invalid');
  const reportGates = Array.isArray(report?.gates) ? report.gates : [];
  const gateIds = new Set(reportGates.map(({ id }) => id));
  if (reportGates.length !== OPERATIONS_AGENT_SAFETY_GATES.length
    || gateIds.size !== OPERATIONS_AGENT_SAFETY_GATES.length
    || reportGates.some(({ id }, index) => id !== OPERATIONS_AGENT_SAFETY_GATES[index])) {
    failures.push('gate list must exactly match the versioned certification contract');
  }
  for (const id of OPERATIONS_AGENT_SAFETY_GATES) {
    const gate = reportGates.find((candidate) => candidate.id === id);
    if (!gateIds.has(id) || gate?.passed !== true) failures.push(`gate ${id} is missing or failed`);
    if (!/^[0-9a-f]{64}$/u.test(gate?.stdoutSha256 ?? '')) failures.push(`gate ${id} stdout digest is invalid`);
    if (!/^[0-9a-f]{64}$/u.test(gate?.stderrSha256 ?? '')) failures.push(`gate ${id} stderr digest is invalid`);
  }
  const reportScenarios = Array.isArray(report?.scenarios) ? report.scenarios : [];
  const scenarioIds = new Set(reportScenarios.map(({ id }) => id));
  if (reportScenarios.length !== OPERATIONS_AGENT_SAFETY_SCENARIOS.length
    || scenarioIds.size !== OPERATIONS_AGENT_SAFETY_SCENARIOS.length
    || reportScenarios.some(({ id }, index) => id !== OPERATIONS_AGENT_SAFETY_SCENARIOS[index].id)) {
    failures.push('scenario list must exactly match the versioned certification contract');
  }
  for (const definition of OPERATIONS_AGENT_SAFETY_SCENARIOS) {
    const scenario = report?.scenarios?.find((candidate) => candidate.id === definition.id);
    if (!scenarioIds.has(definition.id) || scenario?.passed !== true) failures.push(`scenario ${definition.id} is missing or failed`);
    for (const invariantName of OPERATIONS_AGENT_SAFETY_INVARIANTS) {
      const invariantEvidence = scenario?.invariants?.[invariantName];
      if (!['PROVEN', 'NOT_APPLICABLE'].includes(invariantEvidence?.status)) failures.push(`scenario ${definition.id} lacks ${invariantName} status`);
      if (!Array.isArray(invariantEvidence?.evidence)) failures.push(`scenario ${definition.id} lacks ${invariantName} evidence`);
      if (invariantEvidence?.status === 'PROVEN' && invariantEvidence.evidence.length === 0) failures.push(`scenario ${definition.id} has empty ${invariantName} evidence`);
    }
    if (!Array.isArray(scenario?.evidence) || scenario.evidence.some(({ present }) => present !== true)) failures.push(`scenario ${definition.id} has missing evidence markers`);
  }
  for (const invariantName of OPERATIONS_AGENT_SAFETY_INVARIANTS) {
    if (!Array.isArray(report?.invariantCoverage?.[invariantName]) || report.invariantCoverage[invariantName].length === 0) {
      failures.push(`invariant ${invariantName} is not covered`);
    }
  }
  if (report?.browserEvidence?.passed !== true) failures.push('browser evidence did not pass');
  if (report?.passed !== true) failures.push('report passed must be true');
  return { valid: failures.length === 0, failures };
}
