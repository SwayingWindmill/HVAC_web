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
const args = process.argv.slice(2);
if (args.some((arg, index) =>
  (arg === '--profile' && args[index + 1] === 'intelligence') ||
  arg === '--profile=intelligence')) {
  throw new Error('Use --intelligence so the selected deployment tier is capacity-validated before Compose starts the intelligence services');
}

function takeFlag(flag) {
  const index = args.indexOf(flag);
  if (index < 0) return false;
  args.splice(index, 1);
  return true;
}

const ownerSplit = takeFlag('--owner-split');
const simulatorAcceptance = takeFlag('--simulator-acceptance');
const atv630ProtocolAcceptance = takeFlag('--atv630-protocol-acceptance');
const sourceDeploy = takeFlag('--source-deploy');
const integration = takeFlag('--integration') || simulatorAcceptance || atv630ProtocolAcceptance;
const intelligence = takeFlag('--intelligence');
const sourceRevision = sourceDeploy
  ? spawnSync('git', ['rev-parse', '--short=12', 'HEAD'], { cwd: repoRoot, encoding: 'utf8' }).stdout.trim()
  : '';
if (sourceDeploy && !sourceRevision) {
  throw new Error('Source deploy requires a Git revision');
}
const postgresMode = process.env.PHASE1_POSTGRES_MODE || runtimeValues.PHASE1_POSTGRES_MODE || 'local';
if (postgresMode !== 'local' && postgresMode !== 'external') {
  throw new Error(`Unknown PHASE1_POSTGRES_MODE: ${postgresMode}`);
}
const clickhouseMode = process.env.PHASE1_CLICKHOUSE_MODE || runtimeValues.PHASE1_CLICKHOUSE_MODE || 'local';
if (clickhouseMode !== 'local' && clickhouseMode !== 'external') {
  throw new Error(`Unknown PHASE1_CLICKHOUSE_MODE: ${clickhouseMode}`);
}
const redisMode = process.env.PHASE1_REDIS_MODE || runtimeValues.PHASE1_REDIS_MODE || 'local';
if (redisMode !== 'local' && redisMode !== 'external') {
  throw new Error(`Unknown PHASE1_REDIS_MODE: ${redisMode}`);
}
const localPostgres = postgresMode === 'local';
const localClickHouse = clickhouseMode === 'local';
const localRedis = redisMode === 'local';
const externalState = !localPostgres || !localClickHouse || !localRedis;
const tierId = process.env.PHASE1_DEPLOYMENT_TIER || runtimeValues.PHASE1_DEPLOYMENT_TIER;
if (ownerSplit && tierId !== 'single-full') {
  throw new Error('The owner-split runtime mode requires PHASE1_DEPLOYMENT_TIER=single-full');
}

const ownerSplitCompose = ownerSplit
  ? readFileSync(path.join(phase1Dir, 'owner-split.compose.yaml'), 'utf8')
  : null;
const runtimeProfiles = [
  ...(localPostgres ? ['local-postgres'] : []),
  ...(localClickHouse ? ['local-clickhouse'] : []),
  ...(localRedis ? ['local-redis'] : []),
  ...(integration ? ['integration'] : []),
  ...(intelligence ? ['intelligence'] : []),
  ...(ownerSplit ? ['owner-split'] : []),
];
const deploymentTier = resolveDeploymentTier({
  contract: deploymentTierContract,
  compose: canonicalCompose,
  tierId,
  environment: process.env.HVAC_ENV || runtimeValues.HVAC_ENV,
  runtimeEnvironment: { ...runtimeValues, ...process.env },
  additionalComposeDocuments: ownerSplitCompose ? [ownerSplitCompose] : [],
  additionalProfiles: runtimeProfiles,
});

function rolePassword(role) {
  const escapedRole = role.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = roleCredentialSql.match(new RegExp(`^ALTER ROLE ${escapedRole} WITH PASSWORD '([^']+)';$`, 'm'));
  if (!match) {
    throw new Error(`Missing runtime credential for database role: ${role}`);
  }
  return match[1];
}

const postgresHost = process.env.PHASE1_POSTGRES_HOST || runtimeValues.PHASE1_POSTGRES_HOST || 'postgres';
const postgresPort = process.env.PHASE1_POSTGRES_PORT || runtimeValues.PHASE1_POSTGRES_PORT || '5432';
const postgresSslMode = process.env.PHASE1_POSTGRES_SSLMODE || runtimeValues.PHASE1_POSTGRES_SSLMODE || 'disable';

function databaseUrl(role, database) {
  const url = new URL('postgres://postgres');
  url.username = role;
  url.password = rolePassword(role);
  url.hostname = postgresHost;
  url.port = postgresPort;
  url.pathname = `/${database}`;
  url.searchParams.set('sslmode', postgresSslMode);
  return url.toString();
}

const env = {
  ...process.env,
  COMPOSE_PROFILES: '',
  ...deploymentTier.environment,
  PHASE1_DATA_NETWORK_INTERNAL: externalState ? 'false' : 'true',
  PHASE1_OBSERVABILITY_CONFIG: deploymentTier.profiles[0].replace(/^observability-/, ''),
  PHASE1_ENV_FILE: runtimeEnv,
  ...(sourceDeploy ? { HVAC_WEB_BUILD_ID: sourceRevision } : {}),
  IDENTITY_DATABASE_URL: databaseUrl('identity_runtime', 'hvac_identity'),
  IDENTITY_ADMIN_DATABASE_URL: databaseUrl('identity_admin', 'hvac_identity'),
  IDENTITY_DIRECTORY_DATABASE_URL: databaseUrl('identity_directory_reader', 'hvac_identity'),
  IAM_RECONCILER_DATABASE_URL: databaseUrl('s1_iam_reconciler', 'hvac_s1'),
  CONNECTIVITY_DATABASE_URL: databaseUrl('connectivity_runtime', 'hvac_s1'),
  CONNECTIVITY_TENANT_ID: centralPlantIdentity.tenantId,
  ...(atv630ProtocolAcceptance ? {
    ATV630_SOURCE_ROOT: repoRoot,
    ATV630_PLANT_CONFIG_PATH: path.join(repoRoot, 'tools', 'eg8200-simulator', 'configs', 'central-plant.local.json'),
    ATV630_REGISTRY_DATABASE_URL: databaseUrl('s1_core_service', 'hvac_s1'),
    ATV630_CONNECTIVITY_DATABASE_URL: databaseUrl('connectivity_runtime', 'hvac_s1'),
    ATV630_TEMPLATE_TENANT_ID: centralPlantIdentity.tenantId,
    ATV630_TEMPLATE_RELEASE_PRINCIPAL_ID: centralPlantIdentity.principalId,
    ATV630_INTEGRATION_INSTANCE_ID: centralPlantIdentity.integrationInstanceId,
    ATV630_CONNECTIVITY_CREDENTIAL_REF_ID: '01910000-0000-7000-8000-810000000002',
    ATV630_GATEWAY_EXTERNAL_ID: 'EG8200-COMMERCIAL-001',
    PHASE1_RUNTIME_CONFIG_DIR: process.env.PHASE1_RUNTIME_CONFIG_DIR || path.join(phase1Dir, 'runtime', 'config'),
    INTERNAL_PKI_DIR: process.env.INTERNAL_PKI_DIR || path.join(phase1Dir, 'runtime', 'internal-pki'),
    ATV630_EDGE_QUEUE_DIR: process.env.ATV630_EDGE_QUEUE_DIR || path.join(phase1Dir, 'runtime', 'data', 'atv630-edge'),
  } : {}),
  ...(intelligence ? {
    FORECAST_POSTGRES_DSN: databaseUrl('forecast_runtime', 'hvac_s1'),
    OPTIMIZATION_POSTGRES_DSN: databaseUrl('optimization_runtime', 'hvac_s1'),
    FDD_DATABASE_URL: databaseUrl('fdd_runtime', 'hvac_s1'),
  } : {}),
};

const composeFiles = [
  path.join(phase1Dir, 'compose.yaml'),
  path.join(phase1Dir, 'wsl.override.yaml'),
];
if (ownerSplit) {
  composeFiles.push(path.join(phase1Dir, 'owner-split.compose.yaml'));
}
if (simulatorAcceptance || atv630ProtocolAcceptance) {
  composeFiles.push(path.join(repoRoot, 'deploy', 'acceptance', 'phase1-simulator.compose.yaml'));
}

const composeBaseArgs = [
  'compose',
  '--project-name', 'hvac-phase1-local',
  ...[
    ...deploymentTier.profiles,
    ...runtimeProfiles,
    ...(simulatorAcceptance ? ['simulator-acceptance'] : []),
    ...(atv630ProtocolAcceptance ? ['atv630-protocol-acceptance'] : []),
  ].flatMap((profile) => ['--profile', profile]),
  '--env-file', runtimeEnv,
  ...composeFiles.flatMap((composeFile) => ['-f', composeFile]),
];

function runCompose(commandArgs) {
  const result = spawnSync('docker', [...composeBaseArgs, ...commandArgs], {
    cwd: repoRoot,
    env,
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

const runtimeServices = [
  'nginx',
  'energy-api',
  'identity-service',
  'telemetry-worker',
  'metric-worker',
  'scheduler',
  'maintenance',
  ...(integration ? ['iot-service'] : []),
  ...(intelligence ? ['forecast-service', 'optimization-service', 'fdd-service'] : []),
];

if (sourceDeploy) {
  const upIndex = args.indexOf('up');
  if (upIndex < 0) {
    throw new Error('--source-deploy is only valid with docker compose up');
  }
  const buildArgs = [
    ['GO_BUILD_IMAGE', process.env.PHASE1_GO_BUILD_IMAGE],
    ['GO_RUNTIME_IMAGE', process.env.PHASE1_GO_RUNTIME_IMAGE],
    ['GO_PROXY', process.env.PHASE1_GO_PROXY],
    ['WEB_BUILD_IMAGE', process.env.PHASE1_WEB_BUILD_IMAGE],
    ['NGINX_RUNTIME_IMAGE', process.env.PHASE1_NGINX_RUNTIME_IMAGE],
    ['NPM_REGISTRY', process.env.PHASE1_NPM_REGISTRY],
  ].flatMap(([name, value]) => value ? ['--build-arg', `${name}=${value}`] : []);
  const buildStatus = runCompose(['build', ...buildArgs, ...runtimeServices]);
  if (buildStatus !== 0) process.exit(buildStatus);

  process.exit(runCompose(args));
}

process.exit(runCompose(args));
