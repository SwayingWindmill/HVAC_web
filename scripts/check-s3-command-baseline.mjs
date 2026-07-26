import { readFile } from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readText = (relativePath) => readFile(path.join(root, relativePath), 'utf8');
const readJSON = async (relativePath) => JSON.parse(await readText(relativePath));

const [plan, ownership, openapi, routeRegistry, dataRegistry, migration, serviceSource, connectorSource, dispatcherSource, adr] = await Promise.all([
  readJSON('deploy/s3/implementation-plan.v1.json'),
  readJSON('contracts/ownership/s3-command-ownership.v1.json'),
  readJSON('contracts/http/s3-command-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/data-ownership.v1.json'),
  readText('services/command-service/migrations/001_s3_command_runtime.sql'),
  readText('services/command-service/pkg/commandservice/service.go'),
  readText('services/thingsboard-connector-control/pkg/controlconnector/synthetic.go'),
  readText('services/command-dispatcher/pkg/commanddispatcher/dispatcher.go'),
  readText('docs/adr/0006-s3-command-intent-attempt-safety.md'),
]);

const errors = [];
const requireCondition = (condition, message) => {
  if (!condition) errors.push(message);
};

requireCondition(plan.schemaVersion === 1 && plan.slice === 'S3', 'S3 implementation plan identity is invalid');
requireCondition(plan.productionTrafficPercent === 0, 'S3 baseline must have zero production traffic');
requireCondition(plan.firstTracerBullet?.capability === 'SET_TEMPERATURE_SETPOINT', 'first Capability must be SET_TEMPERATURE_SETPOINT');
requireCondition(plan.firstTracerBullet?.connector === 'SYNTHETIC_ONLY', 'first Connector must be Synthetic-only');
requireCondition(plan.firstTracerBullet?.publicRoutesEnabled === false, 'S3 public routes must remain disabled');
requireCondition((plan.tickets ?? []).length === 9, 'S3 implementation plan must contain nine ordered tickets');
requireCondition((plan.tickets ?? []).filter((ticket) => (ticket.blockedBy ?? []).length === 0).map((ticket) => ticket.id).join('|') === 'S3-01', 'S3-01 must be the only initial frontier');

requireCondition(ownership.businessOwner === 'command-service', 'Command business owner must be command-service');
requireCondition(ownership.publicIngress === 'platform-gateway', 'Gateway must remain public ingress');
requireCondition(ownership.authoritativeStore?.schema === 'command_runtime', 'command_runtime must be the authoritative Schema');
requireCondition(ownership.authoritativeStore?.runtimeBypassRls === false, 'Command runtime must not bypass RLS');
requireCondition(ownership.firstCapability?.retryPolicy === 'PRE_SEND_ONLY', 'initial retry policy must be PRE_SEND_ONLY');
requireCondition(ownership.currentStateRequirements?.historicalTelemetryAllowed === false, 'historical telemetry must not satisfy control validation');
requireCondition((ownership.forbiddenFlows ?? []).includes('request-committed-auto-retry'), 'REQUEST_COMMITTED auto-retry prohibition is missing');

const createOperation = openapi.paths?.['/api/v1/commands']?.post;
const getOperation = openapi.paths?.['/api/v1/commands/{commandId}']?.get;
requireCondition(createOperation?.['x-production-traffic-percent'] === 0, 'create Command operation must carry zero traffic');
requireCondition(getOperation?.['x-production-traffic-percent'] === 0, 'get Command operation must carry zero traffic');
requireCondition(createOperation?.responses?.['202'], 'create Command must return 202 Accepted');
requireCondition(createOperation?.parameters?.some((parameter) => parameter.name === 'Idempotency-Key'), 'Idempotency-Key header is required');
for (const forbidden of ['organizationId', 'siteId', 'principalId', 'providerMethod', 'providerParams', 'thingsBoardDeviceId', 'executionFence']) {
  requireCondition(createOperation?.['x-client-forbidden-fields']?.includes(forbidden), `browser forbidden field is missing: ${forbidden}`);
}

const commandRouteKeys = new Set(['POST /api/v1/commands', 'GET /api/v1/commands/{commandId}']);
for (const route of routeRegistry.routes ?? []) {
  const key = `${route.method} ${route.path}`;
  if (!commandRouteKeys.has(key)) continue;
  requireCondition(route.owner === 'command-service', `${key}: owner must be command-service`);
  requireCondition(route.publicIngress === 'platform-gateway', `${key}: public ingress must be Gateway`);
  requireCondition(route.rollout?.mode === 'disabled', `${key}: route must remain disabled`);
  requireCondition(route.shadowSideEffectPolicy === 'SYNTHETIC_ONLY', `${key}: only Synthetic execution is permitted`);
}
for (const key of commandRouteKeys) {
  requireCondition((routeRegistry.routes ?? []).some((route) => `${route.method} ${route.path}` === key), `${key}: route ownership entry is missing`);
}

const resources = new Map((dataRegistry.resources ?? []).map((resource) => [`${resource.kind}:${resource.name}`, resource]));
requireCondition(resources.get('schema:command_runtime')?.writer === 'command-service', 'command_runtime writer must be command-service');
requireCondition(resources.get('outbox:command-dispatch-outbox')?.relay === 'command-dispatcher', 'command dispatch outbox relay must be command-dispatcher');
requireCondition(!(dataRegistry.databaseAccess ?? []).some((access) => access.service === 'command-dispatcher'), 'S3-02 must not grant Dispatcher direct command database access');

for (const requiredTable of ['capability_profiles', 'command_intents', 'command_authorization_snapshots', 'command_risk_snapshots', 'command_approval_snapshots', 'command_attempts', 'command_transitions', 'command_idempotency', 'device_control_state', 'command_dispatch_outbox', 'command_audit_intents']) {
  requireCondition(migration.includes(`command_runtime.${requiredTable}`), `migration is missing ${requiredTable}`);
}
for (const requiredRlsTable of ['command_intents', 'command_authorization_snapshots', 'command_risk_snapshots', 'command_approval_snapshots', 'command_attempts', 'command_transitions', 'command_idempotency', 'device_control_state', 'command_dispatch_outbox', 'command_audit_intents']) {
  requireCondition(migration.includes(`ALTER TABLE command_runtime.${requiredRlsTable} ENABLE ROW LEVEL SECURITY`), `RLS is not enabled for ${requiredRlsTable}`);
}

for (const token of ['ErrIdempotencyConflict', 'ErrCurrentStateUnsafe', 'IntentOutcomeUnknown', 'ConnectorPreSendRejected', 'ExecutionFence']) {
  requireCondition(serviceSource.includes(token) || connectorSource.includes(token), `executable safety token is missing: ${token}`);
}
requireCondition(connectorSource.includes('ErrOldFence'), 'Synthetic Connector must reject an old fence');
requireCondition(dispatcherSource.includes('PrepareDispatch') && dispatcherSource.includes('ResolveDispatch'), 'Dispatcher must bracket Connector execution with persisted preparation and resolution');
requireCondition(adr.includes('historical telemetry -> control precondition'), 'ADR must forbid historical telemetry control validation');

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join('\n'));
  process.exit(1);
}

console.log('S3 Command baseline passed: ownership, disabled routes, canonical Capability, Synthetic execution, idempotency, fence and OUTCOME_UNKNOWN invariants are present.');
