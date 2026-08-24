import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

import { centralPlantIdentity } from './central-plant-local-contract.mjs';
import { parseRuntimeEnvironment, resolveDeploymentTier } from './phase1-deployment-tier.ts';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const phase1Dir = path.join(repoRoot, 'deploy', 'platform', 'phase1');
const runtimeEnv = process.env.PHASE1_ENV_FILE || path.join(phase1Dir, 'environments', 'development.runtime.env');
const roleCredentials = process.env.PHASE1_DB_ROLE_CREDENTIALS_SQL || path.join(phase1Dir, 'runtime', 'db-role-credentials', 'roles.sql');

const roleCredentialSql = readFileSync(roleCredentials, 'utf8');
const runtimeEnvText = readFileSync(runtimeEnv, 'utf8');
const runtimeValues = parseRuntimeEnvironment(runtimeEnvText);
const deploymentTierContract = JSON.parse(readFileSync(path.join(phase1Dir, 'deployment-tiers.v1.json'), 'utf8'));
const canonicalCompose = readFileSync(path.join(phase1Dir, 'compose.yaml'), 'utf8');
let deploymentTier = resolveDeploymentTier({
  contract: deploymentTierContract,
  compose: canonicalCompose,
  tierId: process.env.PHASE1_DEPLOYMENT_TIER || runtimeValues.PHASE1_DEPLOYMENT_TIER,
  environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
  runtimeEnvironment: { ...runtimeValues, ...process.env },
});
const args = process.argv.slice(2);
if (args.some((arg) => arg === '--profile' || arg.startsWith('--profile='))) {
  throw new Error('Compose profiles are selected only by PHASE1_DEPLOYMENT_TIER');
}
const ownerSplitIndex = args.indexOf('--owner-split');
const ownerSplit = ownerSplitIndex >= 0;
if (ownerSplit) {
  args.splice(ownerSplitIndex, 1);
  if (deploymentTier.tier.id !== 'single-full') {
    throw new Error('The owner-split topology requires PHASE1_DEPLOYMENT_TIER=single-full');
  }
  deploymentTier = resolveDeploymentTier({
    contract: deploymentTierContract,
    compose: canonicalCompose,
    tierId: deploymentTier.tier.id,
    environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
    runtimeEnvironment: { ...runtimeValues, ...process.env },
    additionalComposeDocuments: [readFileSync(path.join(phase1Dir, 'owner-split.compose.yaml'), 'utf8')],
    additionalProfiles: ['owner-split'],
  });
}

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
  COMPOSE_PROFILES: '',
  ...deploymentTier.environment,
  PHASE1_ENV_FILE: runtimeEnv,
  IDENTITY_DATABASE_URL: databaseUrl('identity_runtime', 'hvac_identity'),
  IDENTITY_ADMIN_DATABASE_URL: databaseUrl('identity_admin', 'hvac_identity'),
  IDENTITY_DIRECTORY_DATABASE_URL: databaseUrl('identity_directory_reader', 'hvac_identity'),
  IAM_RECONCILER_DATABASE_URL: databaseUrl('s1_iam_reconciler', 'hvac_s1'),
  CONNECTIVITY_DATABASE_URL: databaseUrl('connectivity_runtime', 'hvac_s1'),
  CONNECTIVITY_TENANT_ID: centralPlantIdentity.tenantId,
};

const simulatorAcceptanceIndex = args.indexOf('--simulator-acceptance');
const composeFiles = [
  path.join(phase1Dir, 'compose.yaml'),
  path.join(phase1Dir, 'wsl.override.yaml'),
];
if (ownerSplit) {
  composeFiles.push(path.join(phase1Dir, 'owner-split.compose.yaml'));
}
if (simulatorAcceptanceIndex >= 0) {
  args.splice(simulatorAcceptanceIndex, 1);
  composeFiles.push(path.join(repoRoot, 'deploy', 'acceptance', 'phase1-simulator.compose.yaml'));
}

const result = spawnSync('docker', [
  'compose',
  ...[...deploymentTier.profiles, ...(ownerSplit ? ['owner-split'] : [])].flatMap((profile) => ['--profile', profile]),
  '--env-file', runtimeEnv,
  ...composeFiles.flatMap((composeFile) => ['-f', composeFile]),
  ...args,
], {
  cwd: repoRoot,
  env,
  stdio: 'inherit',
});

if (result.error) {
  throw result.error;
}
process.exit(result.status ?? 1);
