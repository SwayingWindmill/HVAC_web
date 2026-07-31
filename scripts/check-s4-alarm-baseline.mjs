import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const [openapi, routes, ownershipLock, model, serviceHTTP, migration, realAlarms, realApp] = await Promise.all([
  readJSON('contracts/http/s4-alarm-public.openapi.json'),
  readJSON('contracts/ownership/route-ownership.v1.json'),
  readJSON('contracts/ownership/ownership.v1.lock.json'),
  readText('libs/alarmmodel/model.go'),
  readText('services/alarm-service/pkg/alarmservice/http.go'),
  readText('services/alarm-service/migrations/001_s4_alarm_runtime.sql'),
  readText('apps/hvac-web/src/real/RealAlarms.tsx'),
  readText('apps/hvac-web/src/real/RealApp.tsx'),
]);

assert(openapi.info?.version === '0.1.0-contract-only', 'Alarm OpenAPI is not the S4 contract-only baseline');
const listPath = '/api/v1/sites/{siteId}/alarms';
const detailPath = '/api/v1/sites/{siteId}/alarms/{alarmId}';
for (const path of [listPath, detailPath]) {
  const operation = openapi.paths?.[path]?.get;
  assert(operation?.['x-owner'] === 'alarm-service', `Alarm owner is missing for ${path}`);
  assert(operation?.['x-public-ingress'] === 'platform-gateway', `Gateway ingress is missing for ${path}`);
  assert(operation?.['x-production-traffic-percent'] === 0, `Alarm production traffic is enabled for ${path}`);
  assert(operation?.['x-migration-phase'] === 'S4-R0-contract-only', `Alarm phase is not contract-only for ${path}`);
  for (const method of ['post', 'put', 'patch', 'delete']) {
    assert(openapi.paths?.[path]?.[method] === undefined, `Alarm write method ${method.toUpperCase()} is exposed at ${path}`);
  }
  const route = routes.routes?.find((entry) => entry.method === 'GET' && entry.path === path);
  assert(route?.owner === 'alarm-service', `Route Ownership Registry is missing ${path}`);
  assert(route?.rollout?.mode === 'disabled', `Alarm route ${path} is not disabled`);
  assert(route?.migrationPhase === 'S4-R0-contract-only', `Alarm route ${path} has the wrong phase`);
  assert(route?.shadowSideEffectPolicy === 'NONE' && route?.readOnlyFallback === false, `Alarm route ${path} permits fallback or side effects`);
  assert(route?.fallbackForbiddenResults?.includes('AUTHORIZATION_DENIED') && route?.fallbackForbiddenResults?.includes('RESOURCE_NOT_FOUND'), `Alarm route ${path} has incomplete non-leaking results`);
  assert(ownershipLock.routes?.[`GET ${path}`]?.owner === 'alarm-service', `Alarm ownership lock is missing ${path}`);
}

const alarmSchema = openapi.components?.schemas?.Alarm;
for (const field of ['sourceType', 'sourceReference', 'severity', 'status', 'occurrenceCount', 'evidence', 'transitions', 'version']) {
  assert(alarmSchema?.required?.includes(field), `Alarm contract does not require ${field}`);
}
assert(model.includes('SourceDeviceRule') && model.includes('SourceSiteRule') && model.includes('SourceExternal'), 'Alarm source ownership enum is incomplete');
assert(model.includes('alarm transition timeline is incomplete'), 'Alarm lifecycle validation is missing');
assert(serviceHTTP.includes('X-Alarm-Read-Context'), 'Alarm Service does not require a signed read context');
assert(serviceHTTP.includes('len(claims.Actions) != 1') && serviceHTTP.includes('claims.Actions[0] != action'), 'Alarm Service does not require one exact read action');
assert(serviceHTTP.includes('sameStringSet(claims.Scopes, expectedScopes)'), 'Alarm Service does not require exact read scope');
assert(!serviceHTTP.includes('/telemetry/'), 'Alarm Service derives Alarm state from Telemetry HTTP');
assert(migration.includes('ENABLE ROW LEVEL SECURITY') && migration.includes("current_setting('app.organization_id'"), 'Alarm Postgres Organization RLS is missing');
assert(migration.includes('GRANT SELECT') && !migration.includes('GRANT INSERT') && !migration.includes('GRANT UPDATE'), 'Alarm runtime is not read-only');
const disabledBoundaryIndex = realAlarms.indexOf('if (!ALARM_LOCAL_ROUTES_ENABLED)');
const localWorkbenchIndex = realAlarms.indexOf('function LocalAlarmWorkbench');
assert(disabledBoundaryIndex >= 0 && localWorkbenchIndex > disabledBoundaryIndex, 'Real Alarm production path does not fail closed before local hooks');
assert(!realAlarms.includes('useMutation'), 'Real Alarm first slice exposes a browser mutation seam');
assert(realAlarms.includes('不会把设备离线、Telemetry stale 或质量异常推导成 Alarm'), 'Real Alarm disabled surface does not state the non-inference boundary');
assert(realApp.includes("platformNavigation.filter((item) => item.id !== 'alarms')"), 'Site navigation still exposes the obsolete global Alarm placeholder');

console.log('S4 Alarm baseline passed: independent owner, disabled read routes, exact signed scope, durable lifecycle, production fail-closed UI and read-only RLS are present.');
