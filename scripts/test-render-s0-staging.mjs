import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const stagingRoot = resolve(root, 'deploy/s0/staging');

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) files.push(...await filesUnder(path));
    else if (entry.name.endsWith('.yaml') || entry.name.endsWith('.yml')) files.push(path);
  }
  return files;
}

const placeholders = new Set();
for (const path of await filesUnder(stagingRoot)) {
  const text = await readFile(path, 'utf8');
  for (const match of text.matchAll(/"\[([A-Z0-9_]+)\]"/g)) placeholders.add(match[1]);
}
const schema = JSON.parse(await readFile(resolve(stagingRoot, 'bindings.schema.json'), 'utf8'));
const schemaRequired = new Set(schema.required || []);
for (const name of placeholders) {
  if (!schemaRequired.has(name)) throw new Error(`bindings.schema.json is missing ${name}`);
}
for (const name of schemaRequired) {
  if (!placeholders.has(name)) throw new Error(`bindings.schema.json contains unused key ${name}`);
}

const digest = 'a'.repeat(64);
const bindings = {};
for (const name of placeholders) {
  if (name.startsWith('SIGNED_IMAGE_')) bindings[name] = `ghcr.io/test/hvac-s0/${name.toLowerCase()}@sha256:${digest}`;
  else if (name.startsWith('RUNTIME_BINDINGS_')) bindings[name] = [{ configMapRef: { name: 's0-runtime-config' } }];
  else if (name.endsWith('_MOUNTS') || name.endsWith('_VOLUMES')) bindings[name] = [];
  else if (name === 'WORKLOAD_CERT_PATH') bindings[name] = '/var/run/s0/pki/tls.crt';
  else if (name === 'WORKLOAD_KEY_PATH') bindings[name] = '/var/run/s0/pki/tls.key';
  else if (name === 'TRUST_BUNDLE_PATH') bindings[name] = '/var/run/s0/pki/ca.crt';
  else if (name === 'POSTGRES_HOST') bindings[name] = 'postgres.s0-staging.svc.cluster.local';
  else if (name.endsWith('_DATABASE_URL')) bindings[name] = 'postgres://runtime@postgres.s0-staging.svc.cluster.local/hvac_s0?sslmode=verify-full';
  else bindings[name] = 'test-binding';
}

const temporaryRoot = await mkdtemp(resolve(tmpdir(), 'hvac-s0-render-'));
const bindingsPath = resolve(temporaryRoot, 'bindings.json');
const outputPath = resolve(temporaryRoot, 'rendered');
try {
  await writeFile(bindingsPath, JSON.stringify(bindings));
  const result = spawnSync(process.execPath, [
    resolve(root, 'scripts/render-s0-staging.mjs'),
    `--bindings=${bindingsPath}`,
    `--output=${outputPath}`,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (result.error || result.status !== 0) {
    throw new Error(`staging renderer failed: ${result.error?.message ?? result.stderr ?? result.status}`);
  }
  for (const path of await filesUnder(outputPath)) {
    const text = await readFile(path, 'utf8');
    if (/\[[A-Z0-9_]+\]/.test(text)) throw new Error(`rendered placeholder remains in ${path}`);
    if (text.includes(':release-placeholder')) throw new Error(`mutable release tag remains in ${path}`);
  }
  const receipt = JSON.parse(await readFile(resolve(outputPath, 'render-receipt.json'), 'utf8'));
  if (!Array.isArray(receipt.bindings) || receipt.bindings.length !== placeholders.size) {
    throw new Error('render receipt does not account for every binding');
  }
  if (receipt.redactedEvidenceMode !== false) throw new Error('normal staging render unexpectedly enabled redacted evidence mode');

  const redactedMarker = ['[', 'REDACTED_', 'SECRET', ']'].join('');
  const redactedBindings = { ...bindings };
  for (const name of ['SESSION_TOKEN_KEY', 'MIGRATOR_DATABASE_PASSWORD']) redactedBindings[name] = redactedMarker;
  const redactedBindingsPath = resolve(temporaryRoot, 'redacted-bindings.json');
  const strictOutputPath = resolve(temporaryRoot, 'strict-redacted-render');
  const evidenceOutputPath = resolve(temporaryRoot, 'evidence-redacted-render');
  await writeFile(redactedBindingsPath, JSON.stringify(redactedBindings));

  const strictResult = spawnSync(process.execPath, [
    resolve(root, 'scripts/render-s0-staging.mjs'),
    `--bindings=${redactedBindingsPath}`,
    `--output=${strictOutputPath}`,
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (!strictResult.error && strictResult.status === 0) throw new Error('normal staging renderer accepted a redacted evidence marker');

  const evidenceResult = spawnSync(process.execPath, [
    resolve(root, 'scripts/render-s0-staging.mjs'),
    `--bindings=${redactedBindingsPath}`,
    `--output=${evidenceOutputPath}`,
    '--allow-redacted-evidence',
  ], { cwd: root, encoding: 'utf8', windowsHide: true });
  if (evidenceResult.error || evidenceResult.status !== 0) {
    throw new Error(`redacted evidence renderer failed: ${evidenceResult.error?.message ?? evidenceResult.stderr ?? evidenceResult.status}`);
  }
  for (const path of await filesUnder(evidenceOutputPath)) {
    const text = await readFile(path, 'utf8');
    const unresolved = text.replaceAll(redactedMarker, '');
    if (/\[[A-Z0-9_]+\]/.test(unresolved)) throw new Error(`unexpected placeholder remains in evidence render ${path}`);
  }
  const evidenceReceipt = JSON.parse(await readFile(resolve(evidenceOutputPath, 'render-receipt.json'), 'utf8'));
  if (evidenceReceipt.redactedEvidenceMode !== true) throw new Error('evidence render receipt did not record redacted evidence mode');

  console.log(`S0 staging renderer test passed with ${placeholders.size} private bindings and strict evidence redaction.`);
} finally {
  await rm(temporaryRoot, { recursive: true, force: true });
}
