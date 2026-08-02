import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { validateOperationsAgentSafetyCertificationReport } from './operations-agent-safety-certification.v1.mjs';

const directoryArgument = process.argv.find((argument) => argument.startsWith('--directory='));
const directory = resolve(directoryArgument?.slice('--directory='.length) || 'out/operations-agent-safety-certification');
const reportPath = join(directory, 'certification.json');
const checksumPath = join(directory, 'SHA256SUMS');
const failures = [];

if (!existsSync(reportPath)) failures.push('certification.json is missing');
if (!existsSync(checksumPath)) failures.push('SHA256SUMS is missing');

let report = null;
if (failures.length === 0) {
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch (error) {
    failures.push(`certification.json is invalid: ${error instanceof Error ? error.message : String(error)}`);
  }
}

if (report) {
  const validation = validateOperationsAgentSafetyCertificationReport(report);
  failures.push(...validation.failures);
  if (report.checksumManifest !== 'SHA256SUMS') failures.push('checksum manifest reference is invalid');
  const supportingArtifacts = Array.isArray(report.supportingArtifacts) ? report.supportingArtifacts : [];
  const expectedArtifacts = new Set(supportingArtifacts);
  if (expectedArtifacts.size !== supportingArtifacts.length) failures.push('supporting artifact list contains duplicates');
  for (const artifact of expectedArtifacts) {
    if (!/^(?:logs|supporting)\/[A-Za-z0-9._-]+$/u.test(artifact)) {
      failures.push(`supporting artifact path is invalid: ${artifact}`);
    }
  }
  const checksumEntries = new Map();
  for (const line of readFileSync(checksumPath, 'utf8').trim().split(/\r?\n/u)) {
    const match = /^([0-9a-f]{64})  (.+)$/u.exec(line);
    if (!match) {
      failures.push(`invalid checksum line: ${line}`);
      continue;
    }
    checksumEntries.set(match[2], match[1]);
  }
  const gateDigests = new Map((report.gates ?? []).flatMap((gate) => [
    [gate.stdoutPath, gate.stdoutSha256],
    [gate.stderrPath, gate.stderrSha256],
  ]));
  for (const artifact of expectedArtifacts) {
    const path = join(directory, artifact);
    if (!existsSync(path)) {
      failures.push(`supporting artifact is missing: ${artifact}`);
      continue;
    }
    const expected = checksumEntries.get(artifact);
    const actual = createHash('sha256').update(readFileSync(path)).digest('hex');
    if (!expected || expected !== actual) failures.push(`checksum mismatch: ${artifact}`);
    const gateDigest = gateDigests.get(artifact);
    if (gateDigest && gateDigest !== actual) failures.push(`report gate digest mismatch: ${artifact}`);
    if (artifact === 'supporting/postgres-persistence.json'
      && report.postgresEvidence?.digest !== actual) {
      failures.push('PostgreSQL evidence digest does not match the supporting artifact');
    }
  }
  for (const artifact of checksumEntries.keys()) {
    if (!expectedArtifacts.has(artifact)) failures.push(`checksum manifest contains undeclared artifact: ${artifact}`);
  }
}

if (failures.length > 0) {
  console.error(failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}
console.log(`Operations Agent Map 5.5 certification evidence verified: ${directory}`);
