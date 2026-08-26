import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const [plan, ownership, dataOwnership, migration, store, bootstrap, compose] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  read('modules/command/migrations/001_s3_command_runtime.sql'),
  read('modules/command/pkg/commandservice/postgres.go'),
  read('infra/command/postgres/init/000-bootstrap-identities.sql'),
  read('infra/command/compose.yaml'),
]);

const errors = [];
const assert = (condition, message) => {
  if (!condition) errors.push(message);
};

assert(plan.productionTrafficPercent === 0, 'S3 PostgreSQL authority must not enable production traffic');
assert(ownership.businessOwner === 'command-service', 'Command Service must remain the business owner');
assert(ownership.restrictedWorkers?.every((worker) => worker.directDatabaseAccess === false), 'Dispatcher direct database access must remain disabled');
assert(!(dataOwnership.databaseAccess ?? []).some((access) => access.service === 'command-dispatcher'), 'Data Ownership Registry grants Dispatcher direct database access');

for (const role of ['s3_command_migrator', 's3_command_runtime', 's3_command_dispatcher']) {
  assert(bootstrap.includes(`CREATE ROLE ${role} NOLOGIN`), `${role} must be a NOLOGIN role`);
  assert(bootstrap.includes('NOINHERIT NOBYPASSRLS'), 'S3 roles must remain NOINHERIT and NOBYPASSRLS');
}
assert(bootstrap.includes('GRANT s3_command_runtime TO s3_command_service'), 'Command Service activation membership is missing');
assert(!migration.includes('GRANT') || !migration.includes('TO s3_command_dispatcher'), 'Migration must not grant direct table access to Dispatcher');
assert(compose.includes('001_s3_command_runtime.sql'), 'S3 PostgreSQL compose must mount the canonical migration');

for (const table of ['device_control_state', 'command_intents', 'command_idempotency', 'command_authorization_snapshots', 'command_risk_snapshots', 'command_approval_snapshots', 'command_attempts', 'command_transitions', 'command_dispatch_outbox', 'command_audit_intents']) {
  assert(migration.includes(`ALTER TABLE command_runtime.${table} ENABLE ROW LEVEL SECURITY`), `${table} does not enable RLS`);
  assert(migration.includes(`ALTER TABLE command_runtime.${table} FORCE ROW LEVEL SECURITY`), `${table} does not force RLS`);
  assert(migration.includes(`${table}_runtime_org`), `${table} lacks a Tenant-scoped runtime policy`);
}
assert(migration.includes("current_setting('app.tenant_id', true)"), 'RLS does not bind to Tenant context');
assert(migration.includes('command_dispatch_outbox_ready_idx'), 'Dispatch Outbox ready index is missing');
assert(migration.includes("'DRAFT', 'CELSIUS', 16, 30, 3, 'LOW', 'NONE', 'PRE_SEND_ONLY', 'SYNTHETIC_ONLY'"), 'Synthetic Capability seed drifted');

assert(store.includes('pgx.Serializable'), 'Command submission must use a serializable transaction');
assert(store.includes("set_config('app.tenant_id'"), 'Command store does not set the Tenant RLS context');
for (const token of ['command_intents', 'command_idempotency', 'command_transitions', 'command_audit_intents', 'command_dispatch_outbox', 'device_control_state']) {
  assert(store.includes(`command_runtime.${token}`), `PostgreSQL Command transaction does not reference ${token}`);
}
assert(store.includes('replayIdempotentCommand'), 'Concurrent idempotency replay logic is missing');
assert(store.includes('isRetryablePostgresTransaction'), 'Serializable retry handling is missing');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 PostgreSQL authority checks passed: single writer, serializable atomic submission, forced RLS, Tenant context, Audit Intent and Dispatch Outbox.');
