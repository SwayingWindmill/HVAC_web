import { createHash } from 'node:crypto';
import { readFile, readdir } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const directoryArg = arg('directory');
if (!directoryArg) throw new Error('S4 Alarm read promotion verification requires --directory=<evidence-directory>');
const directory = resolve(root, directoryArg);
const envelope = JSON.parse(await readFile(resolve(root, 'deploy/s4/alarm-read-promotion-envelope.v1.json'), 'utf8'));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const allowTestFixture = process.env.S4_ALARM_ALLOW_TEST_FIXTURE === 'true';

const requiredFiles = envelope.requiredEvidence;
const actualFiles = await readdir(directory);
for (const name of requiredFiles) assert(actualFiles.includes(name), `S4 Alarm read promotion evidence is missing ${name}`);
const unexpectedEvidence = actualFiles.filter((name) => (name.endsWith('.json') || name === 'SHA256SUMS') && !requiredFiles.includes(name));
assert(unexpectedEvidence.length === 0, `S4 Alarm read promotion evidence contains unexpected files: ${unexpectedEvidence.join(', ')}`);

const checksumRaw = await readFile(resolve(directory, 'SHA256SUMS'), 'utf8');
const checksumEntries = checksumRaw.trim().split(/\r?\n/).filter(Boolean).map((line) => {
  const match = line.match(/^([a-f0-9]{64})  ([^/\\]+)$/);
  assert(match, `invalid S4 Alarm SHA256SUMS line: ${line}`);
  return { digest: match[1], name: match[2] };
});
const checksumNames = checksumEntries.map((entry) => entry.name);
const expectedChecksumNames = requiredFiles.filter((name) => name !== 'SHA256SUMS').sort();
assert(new Set(checksumNames).size === checksumNames.length, 'S4 Alarm SHA256SUMS contains duplicate subjects');
assert(JSON.stringify([...checksumNames].sort()) === JSON.stringify(expectedChecksumNames), 'S4 Alarm SHA256SUMS inventory does not match the promotion envelope');
for (const entry of checksumEntries) {
  const raw = await readFile(resolve(directory, entry.name));
  assert(sha256(raw) === entry.digest, `S4 Alarm promotion digest mismatch for ${entry.name}`);
}

const readJSON = async (name) => JSON.parse(await readFile(resolve(directory, name), 'utf8'));
const [source, slo, security, plan, rollback, approvals, certification, statement] = await Promise.all([
  readJSON('source-canary-report.json'),
  readJSON('slo-report.json'),
  readJSON('security-zero-report.json'),
  readJSON('route-promotion-plan.json'),
  readJSON('rollback-report.json'),
  readJSON('promotion-approvals.json'),
  readJSON('s4-alarm-read-promotion-attestation.json'),
  readJSON('s4-alarm-read-promotion.intoto.json'),
]);

assert(certification.issue === 187 && certification.status === 'passed', 'formal S4 Alarm promotion attestation is invalid');
const testFixture = certification.testFixture === true;
assert(testFixture ? allowTestFixture && certification.formalPromotionEligible === false : certification.formalPromotionEligible === true, 'formal S4 Alarm promotion attestation is not eligible');
assert(String(certification.repositorySha ?? '').trim() && source.repositorySha === certification.repositorySha, 'S4 Alarm source and certification repository SHAs do not match');
assert(certification.completedSourcePhase === envelope.routeGroup.source.phase && certification.sourceTrafficPercent === envelope.routeGroup.source.trafficPercent, 'S4 Alarm certification source phase drifted');
assert(certification.eligibleTargetPhase === envelope.routeGroup.target.phase && certification.eligibleTargetTrafficPercent === envelope.routeGroup.target.trafficPercent, 'S4 Alarm certification target phase drifted');
assert(certification.repositoryMutationPerformed === false, 'S4 Alarm certification mutated repository policy');
assert(certification.allSLOsPassed === true && certification.allZeroCountersPassed === true && certification.rollbackPassed === true, 'S4 Alarm certification summary contains a failed hard gate');
assert(certification.distinctApproverCount === envelope.approval.distinctApproversRequired, 'S4 Alarm certification approver count is invalid');

assert(source.status === 'passed' && source.formal === true && source.environment?.synthetic === false, 'S4 Alarm source canary report is not formal target-environment evidence');
assert((source.environment?.testFixture === true) === testFixture, 'S4 Alarm source and certification test-fixture classification do not match');
assert(!testFixture || allowTestFixture, 'repository test fixtures cannot verify as production promotion evidence');
assert(source.sourceCanary?.phase === envelope.routeGroup.source.phase && source.sourceCanary?.trafficPercent === 1, 'S4 Alarm source canary report phase is invalid');
const organizations = source.cohort?.organizations ?? [];
const sites = source.cohort?.sites ?? [];
assert(new Set(organizations).size >= envelope.sourceCanary.minimumOrganizationCount, 'S4 Alarm source canary report has too few Organizations');
assert(new Set(sites.map((site) => site.siteId)).size >= envelope.sourceCanary.minimumSiteCount, 'S4 Alarm source canary report has too few Sites');
const siteListRequests = sites.reduce((total, site) => total + Number(site.listRequests ?? Number.NaN), 0);
const siteDetailRequests = sites.reduce((total, site) => total + Number(site.detailRequests ?? Number.NaN), 0);
assert(Number.isFinite(siteListRequests) && Number.isFinite(siteDetailRequests), 'S4 Alarm source canary report has invalid Site-level request counts');
assert(siteListRequests === source.observedVolume?.listRequests && siteDetailRequests === source.observedVolume?.detailRequests, 'S4 Alarm Site-level request counts do not reconcile with totals');
assert(sites.every((site) => organizations.includes(site.organizationId) && Number(site.listRequests ?? 0) + Number(site.detailRequests ?? 0) > 0), 'S4 Alarm source canary report has an unbound or traffic-free Site');
assert(source.observedVolume?.listRequests >= envelope.sourceCanary.minimumListRequests && source.observedVolume?.detailRequests >= envelope.sourceCanary.minimumDetailRequests, 'S4 Alarm source canary report volume is below policy');
assert(source.observedVolume?.routeDecisionCount >= source.observedVolume.listRequests + source.observedVolume.detailRequests, 'S4 Alarm source canary report lacks route decisions');

assert(slo.status === 'passed' && slo.formal === true, 'S4 Alarm SLO report is not formal and passing');
assert(slo.observed?.availabilityFraction >= envelope.serviceLevelObjectives.availabilityFractionMinimum, 'S4 Alarm evidence availability is below policy');
assert(slo.observed?.p95Milliseconds <= envelope.serviceLevelObjectives.p95MillisecondsMaximum, 'S4 Alarm evidence p95 exceeds policy');
assert(slo.observed?.p99Milliseconds <= envelope.serviceLevelObjectives.p99MillisecondsMaximum, 'S4 Alarm evidence p99 exceeds policy');
assert(slo.observed?.serverErrorRateFraction <= envelope.serviceLevelObjectives.serverErrorRateFractionMaximum, 'S4 Alarm evidence 5xx rate exceeds policy');
for (const name of envelope.zeroCounters) assert(security.zeroCounters?.[name] === 0, `S4 Alarm zero counter ${name} is non-zero or missing`);

assert(plan.status === 'passed' && plan.currentRepositoryMutationPerformed === false, 'S4 Alarm route promotion plan mutated repository policy');
assert(plan.source?.phase === envelope.routeGroup.source.phase && plan.source?.trafficPercent === 1, 'S4 Alarm route plan source is invalid');
assert(plan.target?.phase === envelope.routeGroup.target.phase && plan.target?.trafficPercent === 5, 'S4 Alarm route plan target is invalid');
assert(plan.target?.routesPromotedTogether === true && plan.target?.fallbackOwner === '', 'S4 Alarm route plan splits the group or adds fallback');
assert(plan.target?.listRouteRevision === envelope.routeGroup.target.routeRevision && plan.target?.detailRouteRevision === envelope.routeGroup.target.routeRevision, 'S4 Alarm route plan revisions are invalid');

assert(rollback.status === 'passed' && rollback.passed === true, 'S4 Alarm rollback report did not pass');
assert(rollback.restoredPhase === envelope.routeGroup.rollback.phase && rollback.restoredTrafficPercent === 1, 'S4 Alarm rollback did not restore the source phase');
assert(rollback.futureReadsOnly === true && rollback.fallbackSelected === false, 'S4 Alarm rollback selected fallback or changed completed reads');
assert(rollback.decisionMinutes <= envelope.rollbackObjectives.maximumDecisionMinutes && rollback.routeRollbackMinutes <= envelope.rollbackObjectives.maximumRouteRollbackMinutes, 'S4 Alarm rollback exceeded its time objective');
assert(approvals.status === 'passed' && approvals.manualPromotion === true && approvals.approval?.manual === true, 'S4 Alarm promotion approval is not manual');
assert(new Set(approvals.approval?.approvers ?? []).size === envelope.approval.distinctApproversRequired, 'S4 Alarm promotion approval is not two-person');

assert(statement._type === 'https://in-toto.io/Statement/v1' && statement.predicateType === 'https://hvac.local/attestations/s4-alarm-read-promotion/v1', 'S4 Alarm in-toto statement type is invalid');
assert(statement.predicate?.repositorySha === certification.repositorySha && statement.predicate?.reviewerCanVerifyOffline === true, 'S4 Alarm in-toto predicate is not bound to the certified repository SHA');
assert((statement.predicate?.testFixture === true) === testFixture && statement.predicate?.formalPromotionEligible === certification.formalPromotionEligible, 'S4 Alarm in-toto fixture or eligibility classification drifted');
assert(statement.predicate?.repositoryMutationPerformed === false && statement.predicate?.eligibleTargetTrafficPercent === 5, 'S4 Alarm in-toto predicate overclaims repository mutation or target traffic');
const statementSubjects = new Map((statement.subject ?? []).map((subject) => [subject.name, subject.digest?.sha256]));
const expectedStatementSubjects = expectedChecksumNames.filter((name) => name !== 's4-alarm-read-promotion.intoto.json');
assert(statementSubjects.size === expectedStatementSubjects.length, 'S4 Alarm in-toto statement subject count is invalid');
for (const name of expectedStatementSubjects) {
  const checksum = checksumEntries.find((entry) => entry.name === name)?.digest;
  assert(statementSubjects.get(name) === checksum, `S4 Alarm in-toto subject digest mismatch for ${name}`);
}

console.log(`S4 Alarm read promotion evidence verified offline: ${basename(directory)} (${certification.repositorySha}).`);
