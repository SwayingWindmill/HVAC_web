import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';

const root = resolve(process.cwd());
const runner = resolve(root, 'scripts/run-s4-alarm-read-promotion-certification.mjs');
const verifier = resolve(root, 'scripts/verify-s4-alarm-read-promotion-certification.mjs');
const templatePath = resolve(root, 'scripts/fixtures/s4-alarm-read-promotion/formal-attestation.template.json');
const routeRegistry = JSON.parse(await readFile(resolve(root, 'contracts/ownership/route-ownership.v1.json'), 'utf8'));
const repositorySha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const run = (script, args, options = {}) => spawnSync(process.execPath, [script, ...args], {
  cwd: root,
  encoding: 'utf8',
  env: { ...process.env, GITHUB_SHA: repositorySha, S4_ALARM_ALLOW_TEST_FIXTURE: 'true', ...options.env },
});
const withTemp = async (callback) => {
  const directory = await mkdtemp(join(tmpdir(), 's4-alarm-promotion-'));
  try {
    return await callback(directory);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
};
const materializeAttestation = async (directory, mutate = (value) => value) => {
  const template = JSON.parse(await readFile(templatePath, 'utf8'));
  template.repositorySha = repositorySha;
  template.sourceCanary.registryRevision = routeRegistry.registryRevision;
  template.targetPlan.registryRevision = routeRegistry.registryRevision + 1;
  template.rollback.registryRevision = routeRegistry.registryRevision + 2;
  const attestation = mutate(template) ?? template;
  const path = join(directory, 'attestation.json');
  await writeFile(path, `${JSON.stringify(attestation, null, 2)}\n`);
  return path;
};

await test('preflight proves repository Alarm reads remain at 1% and writes remain disabled', async () => {
  await withTemp(async (directory) => {
    const result = run(runner, ['--profile=preflight', `--output-dir=${directory}`]);
    assert.equal(result.status, 0, result.stderr || result.stdout);
    const report = JSON.parse(await readFile(join(directory, 'promotion-preflight-report.json'), 'utf8'));
    assert.equal(report.status, 'passed');
    assert.equal(report.formalPromotionEligible, false);
    assert.equal(report.repositoryPhase, 'S4-R1-internal-read-only');
    assert.equal(report.repositoryTrafficPercent, 1);
    assert.equal(report.targetPhase, 'S4-R2-site-canary');
    assert.equal(report.targetTrafficPercent, 5);
    assert.equal(report.repositoryMutationPerformed, false);
  });
});

await test('complete formal evidence produces an offline-verifiable promotion bundle', async () => {
  await withTemp(async (directory) => {
    const attestationPath = await materializeAttestation(directory);
    const evidenceDir = join(directory, 'evidence');
    const certification = run(runner, ['--profile=formal', `--attestation=${attestationPath}`, `--output-dir=${evidenceDir}`]);
    assert.equal(certification.status, 0, certification.stderr || certification.stdout);
    const verification = run(verifier, [`--directory=${evidenceDir}`]);
    assert.equal(verification.status, 0, verification.stderr || verification.stdout);
    const summary = JSON.parse(await readFile(join(evidenceDir, 's4-alarm-read-promotion-attestation.json'), 'utf8'));
    assert.equal(summary.formalPromotionEligible, false);
    assert.equal(summary.testFixture, true);
    assert.equal(summary.repositoryMutationPerformed, false);
    assert.equal(summary.eligibleTargetTrafficPercent, 5);
    assert.equal(summary.distinctApproverCount, 2);
  });
});

await test('test fixtures, synthetic, incomplete and tampered evidence fail closed', async () => {
  await withTemp(async (directory) => {
    const fixturePath = await materializeAttestation(directory);
    const fixtureRejected = run(runner, ['--profile=formal', `--attestation=${fixturePath}`, `--output-dir=${join(directory, 'fixture-rejected')}`], {
      env: { S4_ALARM_ALLOW_TEST_FIXTURE: '' },
    });
    assert.notEqual(fixtureRejected.status, 0);
    assert.match(fixtureRejected.stderr, /test fixtures cannot certify/i);

    const syntheticPath = await materializeAttestation(directory, (value) => {
      value.environment.synthetic = true;
      return value;
    });
    const synthetic = run(runner, ['--profile=formal', `--attestation=${syntheticPath}`, `--output-dir=${join(directory, 'synthetic')}`]);
    assert.notEqual(synthetic.status, 0);
    assert.match(synthetic.stderr, /synthetic evidence cannot certify/i);

    const incompletePath = await materializeAttestation(directory, (value) => {
      delete value.zeroCounters.responseScopeMismatches;
      return value;
    });
    const incomplete = run(runner, ['--profile=formal', `--attestation=${incompletePath}`, `--output-dir=${join(directory, 'incomplete')}`]);
    assert.notEqual(incomplete.status, 0);
    assert.match(incomplete.stderr, /responseScopeMismatches/);

    const concentratedPath = await materializeAttestation(directory, (value) => {
      value.cohort.sites[2].listRequests = 0;
      value.cohort.sites[2].detailRequests = 0;
      value.cohort.sites[0].listRequests += 350;
      value.cohort.sites[0].detailRequests += 70;
      return value;
    });
    const concentrated = run(runner, ['--profile=formal', `--attestation=${concentratedPath}`, `--output-dir=${join(directory, 'concentrated')}`]);
    assert.notEqual(concentrated.status, 0);
    assert.match(concentrated.stderr, /has no observed Alarm reads/);

    const futurePath = await materializeAttestation(directory, (value) => {
      value.sourceCanary.startedAt = '2099-01-01T00:00:00.000Z';
      value.sourceCanary.endedAt = '2099-01-02T00:00:00.000Z';
      value.rollback.completedAt = '2099-01-02T00:10:00.000Z';
      value.approval.approvedAt = '2099-01-02T00:15:00.000Z';
      return value;
    });
    const future = run(runner, ['--profile=formal', `--attestation=${futurePath}`, `--output-dir=${join(directory, 'future')}`]);
    assert.notEqual(future.status, 0);
    assert.match(future.stderr, /completion is in the future/);

    const validPath = await materializeAttestation(directory);
    const evidenceDir = join(directory, 'tampered');
    const valid = run(runner, ['--profile=formal', `--attestation=${validPath}`, `--output-dir=${evidenceDir}`]);
    assert.equal(valid.status, 0, valid.stderr || valid.stdout);
    const sloPath = join(evidenceDir, 'slo-report.json');
    const slo = JSON.parse(await readFile(sloPath, 'utf8'));
    slo.observed.p95Milliseconds = 999;
    await writeFile(sloPath, `${JSON.stringify(slo, null, 2)}\n`);
    const tampered = run(verifier, [`--directory=${evidenceDir}`]);
    assert.notEqual(tampered.status, 0);
    assert.match(tampered.stderr, /digest mismatch/i);
  });
});
