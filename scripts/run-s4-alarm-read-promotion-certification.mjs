import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const profile = arg('profile', process.env.S4_ALARM_READ_PROMOTION_PROFILE ?? 'preflight');
const outputDir = resolve(root, arg('output-dir', profile === 'formal' ? 'out/s4-alarm-read-promotion' : 'out/s4-alarm-read-promotion-preflight'));
const expectedRepositorySha = process.env.GITHUB_SHA ?? execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const readJSON = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const parseTime = (value, label) => {
  const timestamp = Date.parse(value);
  assert(Number.isFinite(timestamp), `${label} is not an ISO date-time`);
  return timestamp;
};
const finite = (value, label) => {
  assert(Number.isFinite(value), `${label} is missing or not finite`);
  return value;
};
const uniqueStrings = (values, label) => {
  assert(Array.isArray(values), `${label} must be an array`);
  const normalized = values.map((value) => String(value ?? '').trim());
  assert(normalized.every(Boolean), `${label} contains an empty value`);
  assert(new Set(normalized).size === normalized.length, `${label} contains duplicates`);
  return normalized;
};

assert(['preflight', 'formal'].includes(profile), `unsupported S4 Alarm read promotion profile: ${profile}`);
const [envelope, registry] = await Promise.all([
  readJSON('deploy/s4/alarm-read-promotion-envelope.v1.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
]);

assert(envelope.schemaVersion === 1 && envelope.issue === 187, 'S4 Alarm read promotion envelope is invalid');
assert(envelope.status === 'formal-promotion-pending' && envelope.formalPromotionRequired === true, 'S4 Alarm read promotion must remain formally gated');
assert(envelope.repositoryMutationByCertification === false, 'certification may not mutate the route registry');
assert(new Set(envelope.zeroCounters ?? []).size === envelope.zeroCounters?.length, 'S4 Alarm promotion zero counters must be unique');
assert(new Set(envelope.requiredEvidence ?? []).size === envelope.requiredEvidence?.length, 'S4 Alarm promotion evidence names must be unique');

const routeGroup = envelope.routeGroup;
const sourcePolicy = routeGroup.source;
const targetPolicy = routeGroup.target;
const rollbackPolicy = routeGroup.rollback;
const routeKeys = routeGroup.routes;
assert(JSON.stringify(routeKeys) === JSON.stringify([
  'GET /api/v1/sites/{siteId}/alarms',
  'GET /api/v1/sites/{siteId}/alarms/{alarmId}',
]), 'S4 Alarm read promotion route inventory drifted');
assert(sourcePolicy.phase === 'S4-R1-internal-read-only' && sourcePolicy.trafficPercent === 1 && sourcePolicy.routeRevision === 2, 'S4 Alarm source policy drifted');
assert(targetPolicy.phase === 'S4-R2-site-canary' && targetPolicy.trafficPercent === 5 && targetPolicy.routeRevision === 3, 'S4 Alarm target policy drifted');
assert(rollbackPolicy.phase === sourcePolicy.phase && rollbackPolicy.trafficPercent === 1 && rollbackPolicy.routeRevision === 4, 'S4 Alarm rollback policy drifted');
assert(targetPolicy.routeRevision === sourcePolicy.routeRevision + 1 && rollbackPolicy.routeRevision === targetPolicy.routeRevision + 1, 'S4 Alarm route revisions are not adjacent');

const registeredReadRoutes = routeKeys.map((key) => {
  const separator = key.indexOf(' ');
  const method = key.slice(0, separator);
  const path = key.slice(separator + 1);
  const route = registry.routes.find((candidate) => candidate.method === method && candidate.path === path);
  assert(route, `S4 Alarm route is missing: ${key}`);
  return route;
});
for (const route of registeredReadRoutes) {
  assert(route.owner === 'alarm-service' && route.publicIngress === 'platform-gateway', `${route.method} ${route.path} escaped Alarm/Gateway ownership`);
  assert(route.revision === sourcePolicy.routeRevision, `${route.method} ${route.path} source revision drifted`);
  assert(route.migrationPhase === sourcePolicy.phase && route.activationStatus === sourcePolicy.activationStatus, `${route.method} ${route.path} source phase drifted`);
  assert(route.rollout?.mode === 'percentage' && route.rollout.percentage === sourcePolicy.trafficPercent, `${route.method} ${route.path} is not the reviewed 1% canary`);
  assert(route.rollout.cohortSalt === routeGroup.cohortSalt && route.cohortGroup === routeGroup.cohortGroup, `${route.method} ${route.path} cohort binding drifted`);
  assert(!route.rollout.fallbackOwner && route.readOnlyFallback === false && route.readFallbackOwner === undefined, `${route.method} ${route.path} gained a fallback`);
}
const lifecycleRoutes = registry.routes.filter((route) => route.owner === 'alarm-service' && route.method === 'POST');
assert(lifecycleRoutes.length === 7, `expected seven Alarm lifecycle routes; found ${lifecycleRoutes.length}`);
for (const route of lifecycleRoutes) {
  assert(route.rollout?.mode === 'disabled' && route.migrationPhase === 'S4-R0-contract-only', `${route.method} ${route.path} escaped the 0% lifecycle boundary`);
}

await mkdir(outputDir, { recursive: true });
if (profile === 'preflight') {
  const report = {
    schemaVersion: 1,
    issue: 187,
    status: 'passed',
    profile,
    certificationLevel: 'repository-policy-and-formal-evidence-preflight',
    formalPromotionEligible: false,
    repositorySha: expectedRepositorySha,
    repositoryRegistryRevision: registry.registryRevision,
    repositoryPhase: sourcePolicy.phase,
    repositoryTrafficPercent: sourcePolicy.trafficPercent,
    targetPhase: targetPolicy.phase,
    targetTrafficPercent: targetPolicy.trafficPercent,
    repositoryMutationPerformed: false,
    statement: 'Preflight only. The repository remains at the reviewed 1% Alarm read canary. This report does not claim elapsed target-environment hold time, production SLO compliance, rollback execution, manual approval, or eligibility to change route traffic.',
    requiredFormalEvidence: envelope.requiredEvidence,
    checks: [
      'list-and-detail-remain-one-cohort-at-one-percent',
      'both-read-routes-remain-alarm-owned-and-gateway-ingressed',
      'no-read-fallback-owner-exists',
      'all-seven-lifecycle-routes-remain-disabled',
      'formal-attestation-must-bind-repository-sha-and-workflow-run',
      'formal-attestation-must-prove-twenty-four-hour-source-hold',
      'formal-attestation-must-prove-volume-cohort-and-slo-thresholds',
      'formal-attestation-must-prove-all-zero-security-counters',
      'formal-attestation-must-prove-adjacent-five-percent-plan',
      'formal-attestation-must-prove-rollback-and-two-distinct-approvers'
    ]
  };
  await writeFile(resolve(outputDir, 'promotion-preflight-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(`S4 Alarm read promotion preflight passed: ${outputDir}`);
  process.exit(0);
}

const attestationPath = arg('attestation');
assert(attestationPath, 'formal S4 Alarm read promotion requires --attestation=<json>');
const attestationRaw = await readFile(resolve(root, attestationPath));
const attestation = JSON.parse(attestationRaw.toString('utf8'));
assert(attestation.schemaVersion === 1, 'formal S4 Alarm attestation schema version is unsupported');
assert(attestation.repositorySha === expectedRepositorySha, `formal S4 Alarm attestation repository SHA ${attestation.repositorySha} does not match ${expectedRepositorySha}`);
assert(String(attestation.workflowRunId ?? '').trim(), 'formal S4 Alarm attestation lacks workflowRunId');
const environment = attestation.environment ?? {};
assert(environment.synthetic === false, 'synthetic evidence cannot certify an Alarm read promotion');
const testFixture = environment.testFixture === true;
const allowTestFixture = process.env.S4_ALARM_ALLOW_TEST_FIXTURE === 'true';
assert(!testFixture || allowTestFixture, 'repository test fixtures cannot certify an Alarm read promotion');
assert(['production', 'production-like'].includes(environment.classification), 'formal S4 Alarm environment classification is invalid');
for (const field of ['name', 'region', 'observabilitySource']) assert(String(environment[field] ?? '').trim(), `formal S4 Alarm environment.${field} is required`);

const source = attestation.sourceCanary ?? {};
assert(source.phase === sourcePolicy.phase && source.trafficPercent === sourcePolicy.trafficPercent, 'formal source canary phase or traffic is invalid');
assert(source.activationStatus === sourcePolicy.activationStatus, 'formal source canary activation status is invalid');
assert(source.listRouteRevision === sourcePolicy.routeRevision && source.detailRouteRevision === sourcePolicy.routeRevision, 'formal source route revisions are invalid');
assert(source.cohortGroup === routeGroup.cohortGroup && source.cohortSalt === routeGroup.cohortSalt, 'formal source cohort binding is invalid');
assert(finite(source.registryRevision, 'sourceCanary.registryRevision') >= registry.registryRevision, 'formal source registry revision predates the repository baseline');
const sourceStartedAt = parseTime(source.startedAt, 'sourceCanary.startedAt');
const sourceEndedAt = parseTime(source.endedAt, 'sourceCanary.endedAt');
const maximumObservedTime = Date.now() + 5 * 60 * 1000;
assert(sourceEndedAt >= sourceStartedAt, 'formal source canary time window is reversed');
assert(sourceEndedAt <= maximumObservedTime, 'formal source canary completion is in the future');
const elapsedHoldMinutes = Math.floor((sourceEndedAt - sourceStartedAt) / 60000);
assert(elapsedHoldMinutes >= envelope.sourceCanary.minimumHoldMinutes, 'formal source canary is shorter than 24 hours');
assert(finite(source.holdMinutes, 'sourceCanary.holdMinutes') >= envelope.sourceCanary.minimumHoldMinutes && source.holdMinutes <= elapsedHoldMinutes + 1, 'formal source holdMinutes is inconsistent with timestamps');

const cohort = attestation.cohort ?? {};
const organizations = uniqueStrings(cohort.organizations, 'cohort.organizations');
assert(organizations.length >= envelope.sourceCanary.minimumOrganizationCount, 'formal source cohort has too few Organizations');
assert(Array.isArray(cohort.sites), 'cohort.sites must be an array');
const siteIds = new Set();
const organizationsWithTraffic = new Set();
let siteListRequests = 0;
let siteDetailRequests = 0;
for (const site of cohort.sites) {
  const organizationId = String(site?.organizationId ?? '').trim();
  const siteId = String(site?.siteId ?? '').trim();
  assert(organizationId && siteId, 'cohort.sites contains an incomplete Organization/Site binding');
  assert(organizations.includes(organizationId), `cohort Site ${siteId} references an unlisted Organization`);
  assert(!siteIds.has(siteId), `cohort Site ${siteId} is duplicated`);
  const siteList = finite(site.listRequests, `cohort.sites.${siteId}.listRequests`);
  const siteDetail = finite(site.detailRequests, `cohort.sites.${siteId}.detailRequests`);
  assert(siteList >= 0 && siteDetail >= 0 && siteList + siteDetail > 0, `cohort Site ${siteId} has no observed Alarm reads`);
  siteListRequests += siteList;
  siteDetailRequests += siteDetail;
  siteIds.add(siteId);
  organizationsWithTraffic.add(organizationId);
}
assert(siteIds.size >= envelope.sourceCanary.minimumSiteCount, 'formal source cohort has too few Sites');
assert(organizationsWithTraffic.size === organizations.length, 'formal source cohort lists an Organization with no observed Site traffic');

const observed = attestation.observed ?? {};
const listRequests = finite(observed.listRequests, 'observed.listRequests');
const detailRequests = finite(observed.detailRequests, 'observed.detailRequests');
assert(siteListRequests === listRequests && siteDetailRequests === detailRequests, 'formal Site-level Alarm read volume does not reconcile with observed totals');
assert(listRequests >= envelope.sourceCanary.minimumListRequests, 'formal source canary has too few list requests');
assert(detailRequests >= envelope.sourceCanary.minimumDetailRequests, 'formal source canary has too few detail requests');
const totalRequests = listRequests + detailRequests;
assert(finite(observed.routeDecisionCount, 'observed.routeDecisionCount') >= totalRequests, 'formal source canary lacks a route decision for every read');
const slo = envelope.serviceLevelObjectives;
assert(finite(observed.availabilityFraction, 'observed.availabilityFraction') >= slo.availabilityFractionMinimum, 'Alarm read availability is below the promotion SLO');
assert(finite(observed.p95Milliseconds, 'observed.p95Milliseconds') <= slo.p95MillisecondsMaximum, 'Alarm read p95 exceeds the promotion SLO');
assert(finite(observed.p99Milliseconds, 'observed.p99Milliseconds') <= slo.p99MillisecondsMaximum, 'Alarm read p99 exceeds the promotion SLO');
assert(finite(observed.serverErrorRateFraction, 'observed.serverErrorRateFraction') <= slo.serverErrorRateFractionMaximum, 'Alarm read 5xx rate exceeds the promotion SLO');
for (const name of envelope.zeroCounters) assert(attestation.zeroCounters?.[name] === 0, `zero-tolerance counter ${name} is non-zero or missing`);

const target = attestation.targetPlan ?? {};
assert(target.phase === targetPolicy.phase && target.activationStatus === targetPolicy.activationStatus && target.trafficPercent === targetPolicy.trafficPercent, 'formal target plan is not the reviewed 5% site canary');
assert(target.listRouteRevision === targetPolicy.routeRevision && target.detailRouteRevision === targetPolicy.routeRevision, 'formal target route revisions are not the reviewed adjacent revision');
assert(target.registryRevision === source.registryRevision + 1, 'formal target registry revision is not adjacent');
assert(target.cohortGroup === routeGroup.cohortGroup && target.cohortSalt === routeGroup.cohortSalt, 'formal target cohort does not preserve the source cohort identity');
assert(target.routesPromotedTogether === true && target.fallbackOwner === '', 'formal target plan splits the route group or adds fallback');

const rollback = attestation.rollback ?? {};
assert(rollback.passed === true, 'formal Alarm read rollback drill did not pass');
assert(rollback.restoredPhase === rollbackPolicy.phase && rollback.restoredActivationStatus === rollbackPolicy.activationStatus && rollback.restoredTrafficPercent === rollbackPolicy.trafficPercent, 'formal rollback did not restore the 1% source phase');
assert(rollback.listRouteRevision === rollbackPolicy.routeRevision && rollback.detailRouteRevision === rollbackPolicy.routeRevision, 'formal rollback route revisions are invalid');
assert(rollback.registryRevision === target.registryRevision + 1, 'formal rollback registry revision is not adjacent');
assert(rollback.futureReadsOnly === envelope.rollbackObjectives.futureReadsOnly && rollback.fallbackSelected === false, 'formal rollback changed completed reads or selected a fallback owner');
assert(finite(rollback.decisionMinutes, 'rollback.decisionMinutes') <= envelope.rollbackObjectives.maximumDecisionMinutes, 'formal rollback decision exceeded five minutes');
assert(finite(rollback.routeRollbackMinutes, 'rollback.routeRollbackMinutes') <= envelope.rollbackObjectives.maximumRouteRollbackMinutes, 'formal route rollback exceeded fifteen minutes');
const rollbackCompletedAt = parseTime(rollback.completedAt, 'rollback.completedAt');
assert(rollbackCompletedAt >= sourceEndedAt, 'formal rollback drill predates source canary completion');
assert(rollbackCompletedAt <= maximumObservedTime, 'formal rollback completion is in the future');

const approval = attestation.approval ?? {};
assert(approval.manual === envelope.approval.manual, 'formal promotion approval is not manual');
const approvers = uniqueStrings(approval.approvers, 'approval.approvers');
assert(approvers.length === envelope.approval.distinctApproversRequired, 'formal promotion requires exactly two distinct approvers');
assert(String(approval.statement ?? '').trim(), 'formal promotion approval lacks a statement');
const approvedAt = parseTime(approval.approvedAt, 'approval.approvedAt');
assert(approvedAt >= Math.max(sourceEndedAt, rollbackCompletedAt), 'formal approval predates hold completion or rollback');
assert(approvedAt <= maximumObservedTime, 'formal approval is in the future');

const evidence = new Map();
const writeEvidence = async (name, value) => {
  const raw = Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
  await writeFile(resolve(outputDir, name), raw);
  evidence.set(name, sha256(raw));
};
await writeEvidence('source-canary-report.json', {
  schemaVersion: 1,
  status: 'passed',
  formal: true,
  repositorySha: expectedRepositorySha,
  environment,
  sourceCanary: source,
  cohort,
  observedVolume: { listRequests, detailRequests, routeDecisionCount: observed.routeDecisionCount }
});
await writeEvidence('slo-report.json', {
  schemaVersion: 1,
  status: 'passed',
  formal: true,
  objectives: slo,
  observed: {
    availabilityFraction: observed.availabilityFraction,
    p95Milliseconds: observed.p95Milliseconds,
    p99Milliseconds: observed.p99Milliseconds,
    serverErrorRateFraction: observed.serverErrorRateFraction
  }
});
await writeEvidence('security-zero-report.json', { schemaVersion: 1, status: 'passed', zeroCounters: attestation.zeroCounters });
await writeEvidence('route-promotion-plan.json', {
  schemaVersion: 1,
  status: 'passed',
  currentRepositoryMutationPerformed: false,
  source: {
    phase: source.phase,
    trafficPercent: source.trafficPercent,
    registryRevision: source.registryRevision,
    routeRevision: source.listRouteRevision
  },
  target
});
await writeEvidence('rollback-report.json', { schemaVersion: 1, status: 'passed', ...rollback });
await writeEvidence('promotion-approvals.json', { schemaVersion: 1, status: 'passed', manualPromotion: true, approval });
const certification = {
  schemaVersion: 1,
  issue: 187,
  status: 'passed',
  formalPromotionEligible: !testFixture,
  testFixture,
  repositorySha: expectedRepositorySha,
  workflowRunId: attestation.workflowRunId,
  sourceAttestationSha256: sha256(attestationRaw),
  completedSourcePhase: source.phase,
  sourceTrafficPercent: source.trafficPercent,
  eligibleTargetPhase: target.phase,
  eligibleTargetTrafficPercent: target.trafficPercent,
  repositoryMutationPerformed: false,
  allSLOsPassed: true,
  allZeroCountersPassed: true,
  rollbackPassed: true,
  distinctApproverCount: approvers.length,
  completedAt: approval.approvedAt
};
await writeEvidence('s4-alarm-read-promotion-attestation.json', certification);
const statementSubjects = [...evidence].map(([name, digest]) => ({ name, digest: { sha256: digest } }));
const statement = {
  _type: 'https://in-toto.io/Statement/v1',
  subject: statementSubjects,
  predicateType: 'https://hvac.local/attestations/s4-alarm-read-promotion/v1',
  predicate: {
    issue: 187,
    repositorySha: expectedRepositorySha,
    sourcePhase: source.phase,
    eligibleTargetPhase: target.phase,
    eligibleTargetTrafficPercent: target.trafficPercent,
    sourceHoldAndVolumePassed: true,
    serviceLevelObjectivesPassed: true,
    zeroSecurityCountersPassed: true,
    adjacentRoutePlanPassed: true,
    rollbackObjectivesPassed: true,
    twoPersonApprovalPassed: true,
    formalPromotionEligible: !testFixture,
    testFixture,
    repositoryMutationPerformed: false,
    reviewerCanVerifyOffline: true
  }
};
await writeEvidence('s4-alarm-read-promotion.intoto.json', statement);
const checksumLines = [...evidence].sort(([a], [b]) => a.localeCompare(b)).map(([name, digest]) => `${digest}  ${basename(name)}`);
await writeFile(resolve(outputDir, 'SHA256SUMS'), `${checksumLines.join('\n')}\n`);
console.log(`S4 Alarm read promotion formal certification passed: ${outputDir}`);
