import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const [openapi, routes, data, model, store, postgresMutation, http, serviceMain, gateway, gatewayMutation, iam, auth, migration, roles, compose, browser, workflow] = await Promise.all([
  readJSON('contracts/http/s5-work-order-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readText('libs/workordermodel/model.go'),
  readText('services/work-order-service/pkg/workorderservice/store.go'),
  readText('services/work-order-service/pkg/workorderservice/postgres_mutation.go'),
  readText('services/work-order-service/pkg/workorderservice/http.go'),
  readText('services/work-order-service/cmd/work-order-service/main.go'),
  readText('services/platform-gateway/internal/gateway/work_order.go'),
  readText('services/platform-gateway/internal/gateway/work_order_mutation.go'),
  readText('services/iam-service/internal/iam/work_order_authorization.go'),
  readText('libs/workorderauth/authorization.go'),
  readText('services/work-order-service/migrations/002_s5_work_order_create_assignment.sql'),
  readText('services/work-order-service/testdata/postgres/000_roles.sql'),
  readText('infra/s5-work-order/compose.yaml'),
  readText('scripts/run-s5-work-order-create-assign-browser-audit.mjs'),
  readText('.github/workflows/s5-work-order-create-assign.yml'),
]);
const paths = openapi.paths ?? {};
assert(!JSON.stringify(openapi).includes('"":'), 'public Work Order OpenAPI contains an empty schema key');
assert(paths['/api/v1/sites/{siteId}/work-orders']?.post?.['x-iam-action'] === 'work-order:create', 'public Work Order create contract is missing');
assert(paths['/api/v1/sites/{siteId}/work-orders/{workOrderId}:assign']?.post?.['x-iam-action'] === 'work-order:assign', 'public Work Order assignment contract is missing');
for (const forbidden of [':start', ':block', ':resume', ':complete', ':cancel', ':reopen', '/tasks', '/notes', '/attachments']) assert(!Object.keys(paths).some((path) => path.includes(forbidden)), `unreviewed public Work Order route exists: ${forbidden}`);
const writes = routes.routes.filter((route) => route.owner === 'work-order-service' && route.method === 'POST');
assert(writes.length === 2, 'Work Order must expose exactly create and assign POST routes');
assert(writes.every((route) => route.activationStatus === 'internal-canary' && route.rollout?.mode === 'percentage' && route.rollout?.percentage === 1 && route.rollout?.fallbackOwner === undefined && route.shadowSideEffectPolicy === 'NONE' && route.migrationPhase === 'S5-R1-internal-create-assign'), 'Work Order write cohort is not a no-fallback/no-shadow 1% canary');
assert(new Set(writes.map((route) => route.cohortGroup)).size === 1 && new Set(writes.map((route) => route.rollout.cohortSalt)).size === 1, 'Work Order write routes are not cohort-grouped');
for (const marker of ['work-order-idempotency', 'work-order-mutation-audit']) assert(data.resources.some((resource) => resource.name === marker && resource.writer === 'work-order-service'), `data ownership is missing ${marker}`);
assert(data.databaseIdentities.some((identity) => identity.runtimeRole === 's5_work_order_mutation_service' && identity.activationRole === 's5_work_order_writer' && identity.accessMode === 'write'), 'data ownership lacks isolated Work Order mutation identity');
for (const marker of ['ErrVersionConflict', 'ErrInvalidCreate', 'ErrInvalidAssignment', 'MaximumScheduleHorizon', 'ApplyAssignment']) assert(model.includes(marker), `Work Order model is missing ${marker}`);
for (const marker of ['ErrIdempotencyConflict', 'Create(', 'Assign(', 'createMutationDigest', 'assignmentMutationDigest']) assert(store.includes(marker), `Work Order Store is missing ${marker}`);
for (const marker of ['work_order_idempotency', 'work_order_mutation_audit', 'pg_advisory_xact_lock', 'mutationPool']) assert(postgresMutation.includes(marker), `Work Order PostgreSQL mutation owner is missing ${marker}`);
for (const marker of ['WorkOrderWriteContextHeader', 'Idempotency-Key', 'ExpectedVersion', 'work-order:create', 'work-order:assign']) assert(http.includes(marker), `Work Order internal mutation boundary is missing ${marker}`);
for (const marker of ['X-CSRF-Token', 'workOrderWriteContextHeader', 'Idempotency-Key', 'ExpectedVersion', 'signWorkOrderWriteContext']) assert(gatewayMutation.includes(marker), `Gateway Work Order mutation boundary is missing ${marker}`);
assert(gateway.includes('publicWorkOrderAssignment') && !gateway.includes('work-order:complete'), 'Gateway route matcher leaked lifecycle authority');
assert(iam.includes('ReasonDenyExplicit') && auth.includes('ActionCreate') && auth.includes('ActionAssign'), 'IAM Work Order create/assign deny-wins contract is incomplete');
assert(migration.includes('GRANT UPDATE (assignee_id, team_id, version, updated_at)') && migration.includes('FORCE ROW LEVEL SECURITY') && !migration.includes('GRANT DELETE') && !migration.includes('GRANT ALL'), 'Work Order writer SQL authority is too broad');
assert(roles.includes('s5_work_order_mutation_service LOGIN') && serviceMain.includes('WORK_ORDER_MUTATION_DATABASE_URL_FILE') && serviceMain.includes('OpenPostgresStoreWithMutations') && compose.includes('002_s5_work_order_create_assignment.sql'), 'isolated Work Order mutation login wiring is missing');
for (const marker of ['authorized-create-assign', 'exact-idempotent-retry', 'stale-version-conflict', 'authorization-denial-no-data', 'non-selected-session-route-absence', 'cross-site-nondiscovery', 'session-loss-purge', 'unreviewed-lifecycle-absence', 'public-gateway-mutation-only']) assert(browser.includes(marker), `Work Order browser certification is missing ${marker}`);
assert(workflow.includes('npm run s5:work-order:create-assign') && workflow.includes('npm run contracts:check'), 'P4 workflow omits required gates');
for (const forbidden of ["- 'package.json'", "- 'package-lock.json'", "- 'go.work'", "- 'go.work.sum'"]) assert(!workflow.includes(forbidden), `Work Order workflow directly watches broad root manifest ${forbidden}`);
console.log('S5 Work Order governed create/assignment assets passed.');
