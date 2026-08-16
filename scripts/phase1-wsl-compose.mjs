import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const phase1Dir = path.join(repoRoot, 'deploy', 'platform', 'phase1');
const runtimeEnv = process.env.PHASE1_ENV_FILE || path.join(phase1Dir, 'environments', 'development.runtime.env');
const roleCredentials = process.env.PHASE1_DB_ROLE_CREDENTIALS_SQL || path.join(phase1Dir, 'runtime', 'db-role-credentials', 'roles.sql');

const roleCredentialSql = readFileSync(roleCredentials, 'utf8');

function rolePassword(role) {
  const escapedRole = role.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = roleCredentialSql.match(new RegExp(`^ALTER ROLE ${escapedRole} WITH PASSWORD '([^']+)';$`, 'm'));
  if (!match) {
    throw new Error(`Missing runtime credential for database role: ${role}`);
  }
  return match[1];
}

function databaseUrl(role, database) {
  const url = new URL('postgres://postgres');
  url.username = role;
  url.password = rolePassword(role);
  url.hostname = 'postgres';
  url.port = '5432';
  url.pathname = `/${database}`;
  url.searchParams.set('sslmode', 'disable');
  return url.toString();
}

const env = {
  ...process.env,
  PHASE1_ENV_FILE: runtimeEnv,
  IDENTITY_DATABASE_URL: databaseUrl('identity_runtime', 'hvac_identity'),
  IDENTITY_ADMIN_DATABASE_URL: databaseUrl('identity_admin', 'hvac_identity'),
  IDENTITY_DIRECTORY_DATABASE_URL: databaseUrl('identity_directory_reader', 'hvac_identity'),
  IAM_RECONCILER_DATABASE_URL: databaseUrl('s1_iam_reconciler', 'hvac_s1'),
};

const result = spawnSync('docker', [
  'compose',
  '--env-file', runtimeEnv,
  '-f', path.join(phase1Dir, 'compose.yaml'),
  '-f', path.join(phase1Dir, 'wsl.override.yaml'),
  ...process.argv.slice(2),
], {
  cwd: repoRoot,
  env,
  stdio: 'inherit',
});

if (result.error) {
  throw result.error;
}
process.exit(result.status ?? 1);
