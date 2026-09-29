import { existsSync, readFileSync, renameSync, unlinkSync, writeFileSync, chmodSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

import { parseRuntimeEnvironment } from './phase1-deployment-tier.ts';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const phase1Dir = path.join(repoRoot, 'deploy', 'platform', 'phase1');
const runtimeEnv = path.resolve(
  process.env.PHASE1_ENV_FILE || path.join(phase1Dir, 'environments', 'development.runtime.env'),
);
const bootstrapFile = path.resolve(
  process.env.PHASE1_IDENTITY_BOOTSTRAP_FILE || path.join(phase1Dir, 'runtime', 'identity-user-bootstrap.json'),
);
const credentialsFile = path.resolve(
  process.env.PHASE1_LOCAL_ADMIN_CREDENTIALS_FILE || path.join(phase1Dir, 'runtime', 'local-admin.credentials'),
);

if (!existsSync(runtimeEnv)) throw new Error(`Phase 1 runtime environment file not found: ${runtimeEnv}`);
if (!existsSync(bootstrapFile)) throw new Error(`Identity bootstrap file not found: ${bootstrapFile}`);

const runtimeValues = parseRuntimeEnvironment(readFileSync(runtimeEnv, 'utf8'));
const environment = (process.env.HVAC_ENV || runtimeValues.HVAC_ENV || '').trim();
if (!['development', 'testing'].includes(environment)) {
  throw new Error(`Local Identity password recovery is allowed only for development/testing, got HVAC_ENV=${environment || '<unset>'}`);
}

const bootstrap = JSON.parse(readFileSync(bootstrapFile, 'utf8'));
const username = String(bootstrap.username || '').trim();
if (!username) throw new Error(`Identity bootstrap username is missing: ${bootstrapFile}`);

const launcher = path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs');
const result = spawnSync(process.execPath, [launcher, 'run', '--rm', 'identity-admin'], {
  cwd: repoRoot,
  env: {
    ...process.env,
    PHASE1_ENV_FILE: runtimeEnv,
    IDENTITY_ADMIN_OPERATION: 'reset-password-random',
    IDENTITY_ADMIN_USERNAME: username,
  },
  encoding: 'utf8',
  maxBuffer: 1024 * 1024,
});
if (result.error) throw result.error;
if (result.status !== 0) {
  const stderr = String(result.stderr || '')
    .split(/\r?\n/)
    .filter((line) => !/password/i.test(line))
    .join('\n')
    .trim();
  throw new Error(`Identity password reset failed${stderr ? `: ${stderr}` : ''}`);
}

const password = String(result.stdout || '')
  .split(/\r?\n/)
  .map((line) => line.trim())
  .findLast((line) => /^[A-Za-z0-9_-]{32}$/.test(line));
if (!password) throw new Error('Identity password reset did not return the expected random credential');

const temporary = `${credentialsFile}.${process.pid}.tmp`;
try {
  writeFileSync(temporary, `username=${username}\npassword=${password}\n`, { encoding: 'utf8', mode: 0o600 });
  chmodSync(temporary, 0o600);
  renameSync(temporary, credentialsFile);
} finally {
  if (existsSync(temporary)) unlinkSync(temporary);
}

console.log(`Local/test Identity password reset; canonical credentials updated: ${path.relative(repoRoot, credentialsFile)}`);
