// One command for the local environment: `node scripts/local-environment.mjs up | down | reset | ps`.
// `reset` deletes this environment's containers, data volumes and the simulators' Edge state.
// `up` is idempotent: it migrates, bootstraps identity, seeds the central-plant simulator and
// starts every service from the current checkout.
import { randomBytes } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';

import { localContainer, localEnvFile, localProject, repoRoot, runtimeDir, runtimePath } from './lib/local-environment.mjs';

const launcher = path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs');

function run(args, options = {}) {
  const result = spawnSync(process.execPath, args, { cwd: repoRoot, stdio: options.input === undefined ? 'inherit' : ['pipe', 'pipe', 'inherit'], encoding: 'utf8', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${path.basename(args[0])} ${args.slice(1).join(' ')} failed with ${result.status}`);
  return result.stdout ?? '';
}
const compose = (...args) => run([launcher, ...args]);
const script = (name) => run([path.join(repoRoot, 'scripts', name)]);

function psqlScalar(database, sql) {
  const result = spawnSync('docker', ['exec', localContainer('postgres'), 'psql', '-U', 'postgres', '-d', database, '-Atqc', sql], { encoding: 'utf8' });
  if (result.status !== 0) throw new Error(`psql ${database} failed: ${result.stderr.trim()}`);
  return result.stdout.trim();
}

function writePrivate(file, content) {
  const temporary = `${file}.${process.pid}.tmp`;
  writeFileSync(temporary, content, { mode: 0o600 });
  chmodSync(temporary, 0o600);
  renameSync(temporary, file);
}

// Every database role in the reviewed credential template gets a local password; existing ones are kept.
function ensureRoleCredentials() {
  const template = readFileSync(path.join(repoRoot, 'deploy', 'platform', 'phase1', 'migrations', 'role-credentials.sql.example'), 'utf8');
  const file = runtimePath('db-role-credentials', 'roles.sql');
  const current = readFileSync(file, 'utf8');
  const roleOf = (line) => /^ALTER ROLE (\w+) WITH PASSWORD /.exec(line)?.[1];
  const present = new Set(current.split('\n').map(roleOf).filter(Boolean));
  const missing = template.split('\n').map(roleOf).filter((role) => role && !present.has(role));
  if (missing.length === 0) return;
  const additions = missing.map((role) => `ALTER ROLE ${role} WITH PASSWORD '${randomBytes(24).toString('hex')}';`).join('\n');
  writePrivate(file, `${current.trimEnd()}\n${additions}\n`);
}

// Data directories bind-mounted into services that run as 65532 must be writable by them.
function ensureServiceDataDirectory(...segments) {
  const directory = runtimePath(...segments);
  mkdirSync(directory, { recursive: true });
  const result = spawnSync('docker', ['run', '--rm', '--user', '0', '--entrypoint', 'chown', '-v', `${directory}:/data`, 'postgres:16.4-bookworm', '65532:65532', '/data'], { stdio: 'inherit' });
  if (result.status !== 0) throw new Error(`could not hand ${directory} to the service user`);
}

// The simulators are the Edge: their command ledger, measurement sequences and outbound
// spool belong to the platform state they talk to. Resetting the platform without them
// leaves the Edge rejecting fresh commands as stale and replaying old measurements.
function clearSimulatorState() {
  for (const name of ['eg8200', 'eg8200-b']) {
    const directory = runtimePath('data', name);
    if (!existsSync(directory)) continue;
    const result = spawnSync('docker', ['run', '--rm', '--user', '0', '--entrypoint', 'find', '-v', `${directory}:/data`, 'postgres:16.4-bookworm', '/data', '-mindepth', '1', '-delete'], { stdio: 'inherit' });
    if (result.status !== 0) throw new Error(`could not clear ${directory}`);
  }
}

function ensureKey(file, service) {
  if (!existsSync(runtimePath(...file))) compose('run', '--rm', service);
}

// The administrator is created once per database; its password is written only to the runtime directory.
function ensureAdministrator() {
  const bootstrap = JSON.parse(readFileSync(runtimePath('identity-user-bootstrap.json'), 'utf8'));
  const username = String(bootstrap.username).replaceAll("'", "''");
  if (psqlScalar('hvac_identity', `SELECT count(*) FROM identity.users WHERE username = '${username}'`) !== '0') return;
  const password = randomBytes(24).toString('base64url');
  const created = run([launcher, 'run', '--rm', '-T', 'identity-admin'], {
    input: '',
    env: {
      ...process.env,
      IDENTITY_ADMIN_OPERATION: 'create',
      IDENTITY_ADMIN_USERNAME: bootstrap.username,
      IDENTITY_ADMIN_DISPLAY_NAME: bootstrap.displayName,
      IDENTITY_ADMIN_EMAIL: bootstrap.email,
      IDENTITY_ADMIN_PASSWORD: password,
    },
  });
  const user = JSON.parse(created.trim().split('\n').at(-1));
  writePrivate(runtimePath('local-admin.credentials'), `username=${bootstrap.username}\npassword=${password}\n`);
  const reconcile = JSON.parse(readFileSync(runtimePath('identity-reconcile.json'), 'utf8'));
  writePrivate(runtimePath('identity-reconcile.json'), `${JSON.stringify({ ...reconcile, userId: user.id }, null, 2)}\n`);
}

function up() {
  if (!existsSync(localEnvFile)) throw new Error(`local env file is missing: ${localEnvFile}`);
  for (const required of ['db-role-credentials/roles.sql', 'internal-pki/ca.crt', 'identity-user-bootstrap.json', 'identity-reconcile.json']) {
    if (!existsSync(runtimePath(required))) throw new Error(`local runtime is missing ${required} under ${runtimeDir}`);
  }
  // The migrator and schema preflight must come from the checkout being deployed.
  compose('--profile', 'migration', 'run', '--rm', '--build', 'phase1-migrator');
  ensureKey(['identity', 'signing-key.pem'], 'identity-keygen');
  ensureKey(['identity', 'mfa-encryption.key'], 'identity-mfa-keygen');
  ensureKey(['iam', 'api-credential.pepper'], 'iam-api-credential-keygen');
  if (!existsSync(runtimePath('gateway-ca','connectivity.crt'))) {
    ensureServiceDataDirectory('gateway-ca');
    // The initializer needs a writable mount; the long-running service receives it read-only.
    compose('run','--rm','--no-deps','--build','-v',`${runtimePath('gateway-ca')}:/run/hvac/provisioning:rw`, '-e', 'GATEWAY_CA_DIR=/run/hvac/provisioning','connectivity','--initialize-gateway-ca');
  }
  ensureAdministrator();
  script('phase1-bootstrap-local-foundation.mjs');
  run([launcher, 'run', '--rm', '-T', 'identity-reconciler'], { input: readFileSync(runtimePath('identity-reconcile.json'), 'utf8') });
  compose('--source-deploy', '--integration', '--intelligence', 'up', '-d');
  ensureServiceDataDirectory('data', 'eg8200');
  ensureServiceDataDirectory('data', 'eg8200-b');
  script('phase1-central-plant-simulator.mjs');
  const env = readFileSync(localEnvFile, 'utf8');
  const origin = /^PUBLIC_ORIGIN=(.+)$/m.exec(env)?.[1]?.trim();
  console.log(`Local environment ${localProject} is up${origin ? ` at ${origin}` : ''}; administrator credentials: ${path.relative(repoRoot, runtimePath('local-admin.credentials'))}`);
}

const [command, ...rest] = process.argv.slice(2);
ensureRoleCredentials();
if (command === 'up') up();
else if (command === 'down') compose('--simulator-acceptance', '--intelligence', 'stop');
else if (command === 'reset') {
  compose('--simulator-acceptance', '--intelligence', 'down', '--volumes', '--remove-orphans');
  clearSimulatorState();
}
else if (command === 'ps') compose('--simulator-acceptance', '--intelligence', 'ps', ...rest);
else throw new Error('usage: node scripts/local-environment.mjs up | down | reset | ps');
