import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const modulePath = resolve(root, 'pocs/s1-sqlc');
const runner = resolve(root, 'scripts/run-isolated-go.mjs');
const nodeBinary = process.execPath;

function run(args) {
  const result = spawnSync(nodeBinary, [runner, '--module=pocs/s1-sqlc', ...args], {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
    stdio: 'pipe',
  });
  if (result.error || result.status !== 0) {
    throw new Error(`isolated Go command failed: ${args.join(' ')}\n${result.error?.message ?? result.stderr ?? result.status}`);
  }
  return String(result.stdout ?? '').trim();
}

async function generatedDigest() {
  const directory = resolve(modulePath, 'generated');
  const files = (await readdir(directory)).sort();
  const hash = createHash('sha256');
  for (const file of files) {
    hash.update(file);
    hash.update(await readFile(resolve(directory, file)));
  }
  return hash.digest('hex');
}

const before = await generatedDigest();
run(['run', 'github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1', 'generate', '-f', 'sqlc.yaml']);
const after = await generatedDigest();
if (before !== after) throw new Error(`sqlc generation drifted: ${before} -> ${after}`);
run(['mod', 'verify']);
run(['test', './...']);
run(['run', 'golang.org/x/vuln/cmd/govulncheck@v1.1.4', './...']);
console.log(`S1 sqlc POC passed: v1.31.1, deterministic digest ${after.slice(0, 16)}, generated package compiles and has no known Go vulnerabilities.`);
