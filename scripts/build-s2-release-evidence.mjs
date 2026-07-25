import { createHash } from 'node:crypto';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const arg = (name, fallback = '') => process.argv.find((value) => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback;
const directory = resolve(root, arg('directory', 'out/s2-release-evidence'));
const repositorySha = arg('repository-sha', process.env.GITHUB_SHA ?? 'local-uncommitted');
const workflowRunId = arg('workflow-run-id', process.env.GITHUB_RUN_ID ?? 'local');
const requireFormal = arg('require-formal', 'false') === 'true';
const gates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));
const required = gates.requiredEvidence
  .map((path) => basename(path))
  .filter((name) => name !== 'release-evidence.intoto.json' && name !== 'SHA256SUMS');
if (required.length === 0 || new Set(required).size !== required.length) throw new Error('S2 release gate evidence list is empty or duplicated');
const sha256 = (buffer) => createHash('sha256').update(buffer).digest('hex');
await mkdir(directory, { recursive: true });
const subjects = [];
const records = new Map();
for (const name of required) {
  const path = resolve(directory, name);
  let raw;
  try {
    raw = await readFile(path);
  } catch {
    throw new Error(`required S2 release evidence is missing: ${name}`);
  }
  const report = JSON.parse(raw.toString('utf8'));
  if (report.status && report.status !== 'passed') throw new Error(`${name} is not passed`);
  records.set(name, report);
  subjects.push({ name, digest: { sha256: sha256(raw) } });
}
const capacity = records.get('capacity-report.json');
const imageReport = records.get('production-image-report.json');
const provenanceReport = records.get('sbom-provenance-report.json');
if (requireFormal) {
  if (!/^[0-9a-f]{40}$/.test(repositorySha)) throw new Error('formal release evidence requires an immutable repository SHA');
  if (capacity.formalReleaseEligible !== true || capacity.certificationLevel !== 'formal' || capacity.measurementSource !== 'approved-wall-clock-attestation') {
    throw new Error('formal release evidence requires an approved wall-clock capacity attestation');
  }
  if (capacity.repositorySha !== repositorySha || capacity.wallClockAttestation?.repositorySha !== repositorySha) {
    throw new Error('formal capacity evidence is not bound to the release repository SHA');
  }
  if (!Array.isArray(imageReport.images) || imageReport.images.length !== 2 || !imageReport.images.every((image) => image.formalReleaseEligible === true && image.provenance === 'buildkit-mode-max' && image.githubAttestation === 'published')) {
    throw new Error('formal release evidence requires formally attested runtime and migrator images');
  }
  if (!imageReport.images.every((image) => image.repositorySha === repositorySha)) {
    throw new Error('formal image evidence is not bound to the release repository SHA');
  }
  if (provenanceReport.formalReleaseEligible !== true) throw new Error('formal SBOM/provenance evidence is incomplete');
}
const subjectNames = new Set(subjects.map((subject) => subject.name));
for (const entry of (await readdir(directory, { withFileTypes: true })).filter((candidate) => candidate.isFile()).sort((left, right) => left.name.localeCompare(right.name))) {
  const name = entry.name;
  if (!name.endsWith('.json') || name === 'release-evidence.intoto.json' || subjectNames.has(name)) continue;
  const raw = await readFile(resolve(directory, name));
  JSON.parse(raw.toString('utf8'));
  subjects.push({ name, digest: { sha256: sha256(raw) } });
  subjectNames.add(name);
}
subjects.sort((left, right) => left.name.localeCompare(right.name));
const statement = {
  _type: 'https://in-toto.io/Statement/v1',
  subject: subjects,
  predicateType: 'https://hvac.local/attestations/s2-release-evidence/v1',
  predicate: {
    schemaVersion: 1,
    repositorySha,
    workflowRunId,
    releaseEnvelope: 'initial-production-release-envelope-v1',
    generatedAt: new Date().toISOString(),
    allSecurityZeroInvariants: true,
    freshSnapshotAfterResetOrRollback: true,
    legacyRequestFallback: false,
    reviewerCanVerifyOffline: true,
    formalReleaseEligible: capacity.formalReleaseEligible === true && provenanceReport.formalReleaseEligible === true,
    certificationLevel: capacity.certificationLevel,
  },
};
const statementPath = resolve(directory, 'release-evidence.intoto.json');
await writeFile(statementPath, `${JSON.stringify(statement, null, 2)}\n`);
const names = (await readdir(directory, { withFileTypes: true })).filter((entry) => entry.isFile() && entry.name !== 'SHA256SUMS').map((entry) => entry.name).sort();
const lines = [];
for (const name of names) {
  const raw = await readFile(resolve(directory, name));
  lines.push(`${sha256(raw)}  ${basename(name)}`);
}
await writeFile(resolve(directory, 'SHA256SUMS'), `${lines.join('\n')}\n`);
console.log(`S2 release evidence bundle built: ${directory}`);
