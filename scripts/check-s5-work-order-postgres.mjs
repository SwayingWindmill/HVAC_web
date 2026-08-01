import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };

const [routes, data, store, cursor, http, main, migration, roles, seed, compose, runner, workflow] = await Promise.all([
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readText('services/work-order-service/pkg/workorderservice/postgres.go'),
  readText('services/work-order-service/pkg/workorderservice/cursor.go'),
  readText('services/work-order-service/pkg/workorderservice/http.go'),
  readText('services/work-order-service/cmd/work-order-service/main.go'),
  readText('services/work-order-service/migrations/001_s5_work_order_runtime.sql'),
  readText('services/work-order-service/testdata/postgres/000_roles.sql'),
  readText('services/work-order-service/testdata/postgres/010_seed.sql'),
  readText('infra/s5-work-order/compose.yaml'),
  readText('scripts/run-s5-work-order-postgres-tests.mjs'),
  readText('.github/workflows/s5-work-order-postgres.yml'),
]);

const workOrderRoutes = routes.routes.filter((route) => route.path.includes('/work-orders'));
assert(workOrderRoutes.length === 2, 'S5 Work Order must expose exactly list and detail ownership entries');
for (const route of workOrderRoutes) {
  assert(route.method === 'GET', `S5 Work Order mutation route leaked: ${route.method} ${route.path}`);
  assert(route.owner === 'work-order-service' && route.publicIngress === 'platform-gateway', `S5 Work Order owner or ingress drifted: ${route.path}`);
  const contractOnly = route.revision === 1 && route.migrationPhase === 'S5-R0-contract-only' && route.rollout?.mode === 'disabled' && route.activationStatus === 'expand-baseline';
  const internalReadCanary = route.revision === 2 && route.migrationPhase === 'S5-R1-internal-read-only' && route.rollout?.mode === 'percentage' && route.rollout?.percentage === 1 && route.rollout?.cohortSalt === 's5-work-order-read-canary-v1' && route.activationStatus === 'internal-canary';
  assert(contractOnly || internalReadCanary, 'S5 Work Order route is outside the governed R0/R1 read states: ' + route.path);
  assert(route.readOnlyFallback === false && route.readFallbackOwner === undefined && route.rollout?.fallbackOwner === undefined, `S5 Work Order fallback was introduced: ${route.path}`);
}

assert(data.registryRevision >= 18, 'S5 Work Order service identity did not advance Data Ownership revision');

const identities = data.databaseIdentities ?? [];
const runtime = identities.find((entry) => entry.schema === 'work_order_runtime' && entry.runtimeRole === 's5_work_order_runtime');
const service = identities.find((entry) => entry.schema === 'work_order_runtime' && entry.runtimeRole === 's5_work_order_service');
assert(runtime?.migrationRole === 's5_work_order_migrator' && runtime?.accessMode === 'read' && runtime?.runtimeBypassRls === false && runtime?.activationRole === undefined, 'S5 Work Order runtime identity is invalid');
assert(service?.migrationRole === 's5_work_order_migrator' && service?.activationRole === 's5_work_order_runtime' && service?.accessMode === 'read' && service?.runtimeBypassRls === false, 'S5 Work Order service activation identity is invalid');

assert(store.includes('config.ConnConfig.User != "s5_work_order_service"'), 'Work Order Store does not require the service login identity');
assert(store.includes('SET ROLE s5_work_order_runtime'), 'Work Order Store does not explicitly activate the read-only runtime role');
assert(store.includes('pgx.TxOptions{AccessMode: pgx.ReadOnly}'), 'Work Order Store does not use read-only transactions');
assert(store.includes("set_config('app.organization_id'"), 'Work Order Store does not bind Organization RLS scope');
assert(store.includes('hydrateProjection') && store.includes('projection summaries do not converge'), 'Work Order Store does not reconstruct and verify authoritative projections');
assert(!store.includes('INSERT INTO') && !store.includes('UPDATE work_order_runtime') && !store.includes('DELETE FROM work_order_runtime'), 'Work Order Store contains authority writes');

for (const marker of ['crypto/hmac', 'sha256.New', 'OrganizationID', 'SiteID', 'Status', 'Priority', 'AssigneeID', 'UpdatedAt', 'WorkOrderID']) {
  assert(cursor.includes(marker), `Work Order opaque cursor lacks binding marker ${marker}`);
}
assert(cursor.includes('hmac.Equal') && cursor.includes('ErrInvalidCursor'), 'Work Order cursor verification is incomplete');

assert(http.includes('WorkOrderListAction') && http.includes('WorkOrderReadAction'), 'Work Order internal read actions are missing');
assert(http.includes('sameStringSet(claims.Scopes, expectedScopes)'), 'Work Order internal read context is not exact-scope');
assert(http.includes('request.Method != http.MethodGet'), 'Work Order internal boundary is not GET-only');
assert(!http.includes('MethodPost') && !http.includes('Idempotency-Key'), 'Work Order internal boundary contains lifecycle mutation behavior');
assert(main.includes('workloadtls.NewServerTLSConfig') && main.includes('peerSPIFFE(request) != gatewaySPIFFE'), 'Work Order service does not enforce Gateway mTLS identity');
assert(main.includes('WORK_ORDER_CURSOR_SECRET_FILE') && main.includes('WORK_ORDER_DATABASE_URL_FILE'), 'Work Order service does not load protected runtime references from files');

for (const table of ['work_order_current', 'work_order_source_reference', 'work_order_timeline', 'work_order_task', 'work_order_note', 'work_order_attachment_metadata', 'work_order_completion_evidence']) {
  assert(migration.includes(`work_order_runtime.${table}`), `Work Order migration lacks ${table}`);
}
assert(migration.includes('FORCE ROW LEVEL SECURITY') && migration.includes('GRANT SELECT ON ALL TABLES'), 'Work Order migration lacks FORCE RLS or read-only grants');
assert(!migration.includes('GRANT INSERT') && !migration.includes('GRANT UPDATE') && !migration.includes('GRANT DELETE'), 'Work Order runtime was granted mutation authority');
assert(roles.includes('s5_work_order_service LOGIN') && roles.includes('GRANT s5_work_order_runtime TO s5_work_order_service'), 'Work Order fixture lacks explicit service-to-runtime activation');
assert(seed.includes("'01920000-0000-7000-8000-000000000002'") && seed.includes('work_order_completion_evidence'), 'Work Order fixture lacks cross-Organization or completion evidence coverage');
assert(compose.includes('postgres:16.4-bookworm@sha256:') && compose.includes('S5_POSTGRES_HOST_PORT'), 'Work Order PostgreSQL fixture is not pinned or isolated');
const goWorkTriggers = (workflow.match(/^\s+- 'go\.work'$/gm) ?? []).length;
const goWorkSumTriggers = (workflow.match(/^\s+- 'go\.work\.sum'$/gm) ?? []).length;
assert(goWorkTriggers === 2 && goWorkSumTriggers === 2, 'Work Order PostgreSQL workflow must run when the root Go workspace changes');
for (const marker of ['explicitActivationRequired', 'organizationRls', 'readOnlyRuntime', 'goIntegrationTests', 'projectionConvergence']) {
  assert(runner.includes(marker), `Work Order PostgreSQL runner lacks assertion ${marker}`);
}

console.log('S5 Work Order PostgreSQL read baseline passed: exact internal GET authority, signed opaque pagination, read-only role activation, FORCE RLS and projection convergence are present.');
