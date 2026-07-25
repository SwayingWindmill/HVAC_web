import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const directory = resolve(root, process.argv.find((value) => value.startsWith('--directory='))?.slice(12) ?? 'out/s2-completion-evidence');
const plan = JSON.parse(await readFile(resolve(root, 'deploy/s2/cutover-plan.v1.json'), 'utf8'));
const checksumText = await readFile(resolve(directory, 'SHA256SUMS'), 'utf8');
const checksums = new Map();
for (const line of checksumText.trim().split(/\r?\n/).filter(Boolean)) {
  const match = /^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$/.exec(line);
  if (!match) throw new Error(`invalid S2 completion SHA256SUMS line: ${line}`);
  if (checksums.has(match[2])) throw new Error(`duplicate S2 completion checksum: ${match[2]}`);
  const raw = await readFile(resolve(directory, match[2]));
  const actual = createHash('sha256').update(raw).digest('hex');
  if (actual !== match[1]) throw new Error(`S2 completion digest mismatch: ${match[2]}`);
  checksums.set(match[2], actual);
}
for (const name of plan.requiredEvidence) {
  if (name !== 'SHA256SUMS' && !checksums.has(name)) throw new Error(`required S2 completion evidence is missing: ${name}`);
}
const completion = JSON.parse(await readFile(resolve(directory, 's2-completion-attestation.json'), 'utf8'));
if (completion.status !== 'passed' || completion.formalCompletionEligible !== true || completion.certificationLevel !== 'formal-production-cutover' || completion.completedPhase !== 'R8-legacy-current-state-retired') {
  throw new Error('S2 completion attestation is not formal and passed');
}
if (completion.backendRepositorySha !== plan.retirement.backendCommitSha || completion.allSecurityZeroInvariants !== true || completion.allCompletionRisksZero !== true || completion.legacyCurrentStateTrafficRemaining !== 0 || completion.historicalTimeseriesRetained !== true) {
  throw new Error('S2 completion attestation does not preserve final invariants');
}
const statement = JSON.parse(await readFile(resolve(directory, 's2-completion.intoto.json'), 'utf8'));
if (statement._type !== 'https://in-toto.io/Statement/v1' || statement.predicateType !== 'https://hvac.local/attestations/s2-completion/v1' || statement.predicate?.reviewerCanVerifyOffline !== true) {
  throw new Error('S2 completion in-toto statement is invalid');
}
if (!Array.isArray(statement.subject) || statement.predicate.backendRepositorySha !== plan.retirement.backendCommitSha || statement.predicate.completedPhase !== 'R8-legacy-current-state-retired' || statement.predicate.sevenDayZeroLegacyTrafficVerified !== true || statement.predicate.legacyCurrentStateCodeAndNetworkSealed !== true) {
  throw new Error('S2 completion statement is missing retirement invariants');
}
const seen = new Set();
for (const subject of statement.subject) {
  if (!/^[A-Za-z0-9._-]+$/.test(subject.name) || seen.has(subject.name)) throw new Error('S2 completion subject is invalid or duplicated');
  const expected = subject.digest?.sha256;
  const raw = await readFile(resolve(directory, subject.name));
  const actual = createHash('sha256').update(raw).digest('hex');
  if (actual !== expected || checksums.get(subject.name) !== expected) throw new Error(`S2 completion subject digest mismatch: ${subject.name}`);
  seen.add(subject.name);
}
for (const name of plan.requiredEvidence) {
  if (name !== 'SHA256SUMS' && name !== 's2-completion.intoto.json' && !seen.has(name)) throw new Error(`S2 completion statement is missing subject: ${name}`);
}
if (!checksums.has('s2-completion.intoto.json')) throw new Error('S2 completion statement is missing from SHA256SUMS');
console.log(`S2 completion evidence verified offline: ${directory}`);
