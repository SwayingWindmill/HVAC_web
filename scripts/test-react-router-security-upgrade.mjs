import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { extname, join, resolve } from 'node:path';
import test from 'node:test';

const root = resolve(process.cwd());
const requiredNodeVersion = '22.22.0';

async function collectFiles(directory, extensions) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await collectFiles(path, extensions));
    } else if (extensions.has(extname(entry.name))) {
      files.push(path);
    }
  }
  return files;
}

test('pins the patched React Router stack and its supported runtime', async () => {
  const packageJson = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
  assert.equal(packageJson.engines?.node, `>=${requiredNodeVersion}`);
  assert.equal(packageJson.dependencies?.react, '19.2.7');
  assert.equal(packageJson.dependencies?.['react-dom'], '19.2.7');
  assert.equal(packageJson.dependencies?.['react-router'], '8.3.0');
  assert.equal(packageJson.dependencies?.['react-router-dom'], undefined);
  assert.equal(packageJson.dependencies?.['@react-three/fiber'], '9.6.1');
  assert.equal(packageJson.dependencies?.['@react-three/drei'], '10.7.7');
  assert.equal(packageJson.devDependencies?.['@types/node'], '22.20.1');
  assert.equal(packageJson.devDependencies?.['@types/react'], '19.2.17');
  assert.equal(packageJson.devDependencies?.['@types/react-dom'], '19.2.3');
});

test('pins patched transitive security dependencies without an audit waiver', async () => {
  const packageJson = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
  assert.equal(packageJson.overrides?.['path-to-regexp'], '8.4.2');
  assert.equal(packageJson.overrides?.mermaid, '11.17.0');
  assert.equal(packageJson.overrides?.dompurify, '3.4.14');
  await assert.rejects(readFile(join(root, 'deploy/s0/security/dependency-audit-baseline.json'), 'utf8'), { code: 'ENOENT' });
});

test('contains no react-router-dom imports in application or test fixture source', async () => {
  const roots = [
    join(root, 'apps', 'hvac-web', 'src'),
    join(root, 'scripts', 'fixtures'),
  ];
  const offenders = [];
  for (const sourceRoot of roots) {
    for (const path of await collectFiles(sourceRoot, new Set(['.js', '.jsx', '.ts', '.tsx', '.mjs']))) {
      const source = await readFile(path, 'utf8');
      if (/from\s+['"]react-router-dom['"]|import\s+['"]react-router-dom['"]/.test(source)) {
        offenders.push(path.slice(root.length + 1).replaceAll('\\', '/'));
      }
    }
  }
  assert.deepEqual(offenders, []);
});

test('uses a React Router supported Node version in every root npm workflow', async () => {
  const workflowRoot = join(root, '.github', 'workflows');
  const offenders = [];
  for (const path of await collectFiles(workflowRoot, new Set(['.yml', '.yaml']))) {
    const source = await readFile(path, 'utf8');
    if (!/\bnpm (?:ci|install|run)\b/.test(source)) continue;
    const nodeVersions = [...source.matchAll(/node-version:\s*["']?([^"'\s]+)["']?/g)].map((match) => match[1]);
    if (nodeVersions.length === 0 || nodeVersions.some((version) => version !== requiredNodeVersion)) {
      offenders.push(path.slice(root.length + 1).replaceAll('\\', '/'));
    }
  }
  assert.deepEqual(offenders, []);
});

test('pins contract generation to the supported Node toolchain', async () => {
  const tooling = JSON.parse(await readFile(join(root, 'contracts', 'http', 'tooling.lock.json'), 'utf8'));
  assert.equal(tooling.node, requiredNodeVersion);
});

test('keeps the React Router security workflow trigger set precise and complete', async () => {
  const workflow = await readFile(join(root, '.github', 'workflows', 'security-79-react-router.yml'), 'utf8');
  assert.equal(workflow.includes('.github/workflows/**'), false);
  assert.equal(workflow.includes('scripts/check-s2-*.mjs'), false);

  const requiredPaths = [
    '.github/workflows/security-79-react-router.yml',
    'scripts/check-npm-production-audit.mjs',
    'scripts/check-production-licenses.mjs',
    'scripts/check-rms-real-build-graph.mjs',
    'scripts/check-rms-real-bundle.mjs',
    'scripts/check-s1-hvac-web-registry.mjs',
    'scripts/check-s2-hvac-web-presence.mjs',
    'scripts/check-s2-shadow-routing.mjs',
    'scripts/check-s2-telemetry-live-client.mjs',
    'scripts/generate-platform-contracts.mjs',
    'scripts/generate-s2-telemetry-contracts.mjs',
    'scripts/rms-certification-evidence-lib.mjs',
    'scripts/rms-real-build-audit-lib.mjs',
    'scripts/run-rms-authenticated-shell-browser-audit.mjs',
    'scripts/run-s2-hvac-web-presence-browser-audit.mjs',
    'scripts/test-central-plant-telemetry.mjs',
    'scripts/test-npm-production-audit-retry.mjs',
    'scripts/test-react-router-security-upgrade.mjs',
    'scripts/test-rms-authenticated-shell-policy.mjs',
    'scripts/test-rms-certification-evidence.mjs',
    'scripts/test-rms-feature-route-policy.mjs',
    'scripts/test-rms-protected-scope.mjs',
    'scripts/test-rms-realtime-status.mjs',
    'scripts/test-rms-shell-runtime.mjs',
    'scripts/test-rms-site-routing.mjs',
  ];
  for (const path of requiredPaths) {
    assert.equal(workflow.split(`\"${path}\"`).length - 1, 2, `${path} must be present in pull_request and push paths`);
  }
});
