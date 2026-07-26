import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { basename, resolve } from 'node:path';

const root = resolve(process.cwd());
const directory = resolve(root, process.argv.find((value) => value.startsWith('--directory='))?.slice(12) ?? 'out/s2-release-evidence');
const gates = JSON.parse(await readFile(resolve(root, 'deploy/s2/release-gates.v1.json'), 'utf8'));
const required = gates.requiredEvidence.map((path) => basename(path));
if (required.length === 0 || new Set(required).size !== required.length) throw new Error('S2 release gate evidence list is empty or duplicated');
const checksumText = await readFile(resolve(directory, 'SHA256SUMS'), 'utf8');
const entries = checksumText.trim().split(/\r?\n/).filter(Boolean);
const checksums = new Map();
for (const line of entries) {
  const match = /^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$/.exec(line);
  if (!match) throw new Error(`invalid SHA256SUMS line: ${line}`);
  if (checksums.has(match[2])) throw new Error(`duplicate SHA256SUMS entry: ${match[2]}`);
  const raw = await readFile(resolve(directory, match[2]));
  const actual = createHash('sha256').update(raw).digest('hex');
  if (actual !== match[1]) throw new Error(`S2 release evidence digest mismatch: ${match[2]}`);
  checksums.set(match[2], actual);
}
for (const name of required) {
  if (name !== 'SHA256SUMS' && !checksums.has(name)) throw new Error(`required S2 release evidence is missing from SHA256SUMS: ${name}`);
}
const statement = JSON.parse(await readFile(resolve(directory, 'release-evidence.intoto.json'), 'utf8'));
if (statement._type !== 'https://in-toto.io/Statement/v1' || statement.predicate?.reviewerCanVerifyOffline !== true) {
  throw new Error('S2 in-toto statement is invalid');
}
if (!Array.isArray(statement.subject) || statement.predicate.allSecurityZeroInvariants !== true || statement.predicate.legacyRequestFallback !== false) {
  throw new Error('S2 in-toto statement does not preserve release invariants');
}
const seenSubjects = new Set();
for (const subject of statement.subject) {
  if (!/^[A-Za-z0-9._-]+$/.test(subject.name) || seenSubjects.has(subject.name)) throw new Error('S2 in-toto subject name is invalid or duplicated');
  const expected = subject.digest?.sha256;
  const raw = await readFile(resolve(directory, subject.name));
  const actual = createHash('sha256').update(raw).digest('hex');
  if (actual !== expected || checksums.get(subject.name) !== expected) throw new Error(`S2 in-toto subject digest mismatch: ${subject.name}`);
  seenSubjects.add(subject.name);
}
for (const name of required) {
  if (name !== 'SHA256SUMS' && name !== 'release-evidence.intoto.json' && !seenSubjects.has(name)) {
    throw new Error(`required S2 release evidence is missing from the in-toto statement: ${name}`);
  }
}
if (!checksums.has('release-evidence.intoto.json')) throw new Error('S2 statement is missing from SHA256SUMS');
console.log(`S2 release evidence verified offline: ${directory}`);
