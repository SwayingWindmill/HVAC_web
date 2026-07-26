import { createHash } from 'node:crypto';
import { readFile, readdir } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const directoryArg = arg('directory');
if (!directoryArg) throw new Error('S3 certification verification requires --directory=<evidence-directory>');
const directory = resolve(root, directoryArg);
const envelope = JSON.parse(await readFile(resolve(root, 'deploy/s3/certification-envelope.v1.json'), 'utf8'));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = (value) => createHash('sha256').update(value).digest('hex');

const requiredFiles = envelope.requiredEvidence;
const actualFiles = await readdir(directory);
for (const name of requiredFiles) assert(actualFiles.includes(name), `S3 certification evidence is missing ${name}`);
const unexpectedJSON = actualFiles.filter((name) => (name.endsWith('.json') || name === 'SHA256SUMS') && !requiredFiles.includes(name));
assert(unexpectedJSON.length === 0, `S3 certification evidence contains unexpected files: ${unexpectedJSON.join(', ')}`);

const checksumRaw = await readFile(resolve(directory, 'SHA256SUMS'), 'utf8');
const checksumEntries = checksumRaw.trim().split(/\r?\n/).filter(Boolean).map((line) => {
  const match = line.match(/^([a-f0-9]{64})  ([^/\\]+)$/);
  assert(match, `invalid SHA256SUMS line: ${line}`);
  return { digest: match[1], name: match[2] };
});
const checksumNames = checksumEntries.map((entry) => entry.name);
const expectedChecksumNames = requiredFiles.filter((name) => name !== 'SHA256SUMS').sort();
assert(new Set(checksumNames).size === checksumNames.length, 'SHA256SUMS contains duplicate subjects');
assert(JSON.stringify([...checksumNames].sort()) === JSON.stringify(expectedChecksumNames), 'SHA256SUMS subject inventory does not match the certification envelope');
for (const entry of checksumEntries) {
  const raw = await readFile(resolve(directory, entry.name));
  assert(sha256(raw) === entry.digest, `S3 certification digest mismatch for ${entry.name}`);
}

const readJSON = async (name) => JSON.parse(await readFile(resolve(directory, name), 'utf8'));
const [
  capacity,
  failures,
  fence,
  canary,
  approvals,
  security,
  rollback,
  certification,
  statement,
] = await Promise.all([
  readJSON('capacity-report.json'),
  readJSON('failure-injection-report.json'),
  readJSON('command-fence-report.json'),
  readJSON('internal-canary-report.json'),
  readJSON('promotion-approvals.json'),
  readJSON('security-zero-report.json'),
  readJSON('rollback-report.json'),
  readJSON('s3-certification-attestation.json'),
  readJSON('s3-certification.intoto.json'),
]);

assert(certification.ticket === 'S3-09' && certification.status === 'passed' && certification.formalCertificationEligible === true, 'formal S3 certification attestation is not eligible');
assert(certification.productionTrafficPercent === 0 && certification.publicRoutesEnabled === false, 'formal S3 certification evidence enabled public production traffic');
assert(String(certification.repositorySha ?? '').trim() && capacity.repositorySha === certification.repositorySha, 'capacity and certification repository SHAs do not match');
assert(capacity.status === 'passed' && capacity.formal === true, 'capacity report is not formal and passing');
assert(failures.status === 'passed' && failures.formal === true && failures.scenarios?.length === envelope.requiredCrashPoints.length, 'failure-injection report is incomplete');
assert(new Set(failures.scenarios.map((entry) => entry.name)).size === envelope.requiredCrashPoints.length, 'failure-injection report contains duplicate scenarios');
for (const name of envelope.requiredCrashPoints) {
  const scenario = failures.scenarios.find((entry) => entry.name === name);
  assert(scenario?.passed === true && String(scenario.evidence ?? '').trim(), `failure-injection evidence is missing for ${name}`);
}
assert(fence.status === 'passed' && fence.oldFenceExecutions === 0 && fence.duplicateDeviceSideEffects === 0 && fence.blindRetriesAfterRequestCommitted === 0, 'Command Fence report violates a zero invariant');
assert(canary.status === 'passed' && canary.formal === true && canary.phase === envelope.internalCanary.phase, 'internal Device canary report is not formal and passing');
assert(canary.organizationCount === 1 && canary.siteCount === 1 && canary.deviceCount === 1, 'internal Device canary is not exactly one Organization/Site/Device');
assert(canary.publicRoutesEnabled === false && canary.productionTrafficPercent === 0, 'internal Device canary enabled public production traffic');
assert(canary.commandCount === canary.verifiedCommandCount && canary.outcomeUnknownCount === 0 && canary.automaticReissuesAfterRequestCommitted === 0, 'internal Device canary contains an unverified, uncertain or reissued Command');
assert(/^(secret|workload):\/\//.test(String(canary.credentialReference ?? '')), 'internal Device canary contains a non-opaque credential reference');
assert(approvals.status === 'passed' && approvals.manualPromotion === true && approvals.approval?.manual === true && approvals.approval?.primaryOwner !== approvals.approval?.secondaryOwner, 'promotion approvals are incomplete or not separated');
assert(rollback.status === 'passed' && rollback.futureCommandsOnly === true && rollback.acceptedCommandsRemainWithOriginalOwner === true, 'rollback evidence violates accepted-command ownership');
for (const name of envelope.zeroCounters) assert(security.zeroCounters?.[name] === 0, `security-zero report counter ${name} is non-zero or missing`);

assert(statement._type === 'https://in-toto.io/Statement/v1' && statement.predicateType === 'https://hvac.local/attestations/s3-command-certification/v1', 'S3 in-toto statement type is invalid');
assert(statement.predicate?.repositorySha === certification.repositorySha && statement.predicate?.reviewerCanVerifyOffline === true, 'S3 in-toto predicate is not bound to the certified repository SHA');
const statementSubjects = new Map((statement.subject ?? []).map((subject) => [subject.name, subject.digest?.sha256]));
const expectedStatementSubjects = expectedChecksumNames.filter((name) => name !== 's3-certification.intoto.json');
assert(statementSubjects.size === expectedStatementSubjects.length, 'S3 in-toto statement subject count is invalid');
for (const name of expectedStatementSubjects) {
  const checksum = checksumEntries.find((entry) => entry.name === name)?.digest;
  assert(statementSubjects.get(name) === checksum, `S3 in-toto subject digest mismatch for ${name}`);
}

console.log(`S3 Command formal certification evidence verified offline: ${basename(directory)} (${certification.repositorySha}).`);
