import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const generatorVersion = '7.0.0';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = resolve(root, 'contracts/http/platform-gateway.openapi.yaml');
const toolingLockPath = resolve(root, 'contracts/http/tooling.lock.json');
const goTemplatePath = resolve(root, 'contracts/http/templates/platformapi.go.tmpl');
const tsTemplatePath = resolve(root, 'contracts/http/templates/platformGateway.ts.tmpl');
const goOutputPath = resolve(root, 'services/platform-gateway/pkg/platformapi/api.gen.go');
const tsOutputPath = resolve(root, 'apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const checkOnly = process.argv.includes('--check');
const windowsGofmtPath = 'C:\\Program Files\\Go\\bin\\gofmt.exe';
const gofmtBinary = process.env.GOFMT_BINARY ?? (process.platform === 'win32' && existsSync(windowsGofmtPath) ? windowsGofmtPath : 'gofmt');

const normalizeLineEndings = (value) => value.replace(/\r\n?/g, '\n');
const [specSource, toolingLockSource, goTemplateSource, tsTemplateSource] = await Promise.all([
  readFile(specPath, 'utf8'),
  readFile(toolingLockPath, 'utf8'),
  readFile(goTemplatePath, 'utf8'),
  readFile(tsTemplatePath, 'utf8'),
]);
const specText = normalizeLineEndings(specSource);
const toolingLockText = normalizeLineEndings(toolingLockSource);
const goTemplate = normalizeLineEndings(goTemplateSource);
const tsTemplate = normalizeLineEndings(tsTemplateSource);
const spec = JSON.parse(specText);
const toolingLock = JSON.parse(toolingLockText);
const digest = createHash('sha256').update(specText).digest('hex');

function invariant(condition, message) {
  if (!condition) throw new Error(`Invalid platform Gateway OpenAPI contract: ${message}`);
}

function exactMembers(actual, expected) {
  return Array.isArray(actual) && actual.length === expected.length && expected.every((member) => actual.includes(member));
}

function exactKeys(value, expected) {
  return value !== null && typeof value === 'object' && exactMembers(Object.keys(value), expected);
}

function operation(operationId) {
  for (const [path, pathItem] of Object.entries(spec.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      if (pathItem?.[method]?.operationId === operationId) return { path, method, operation: pathItem[method] };
    }
  }
  return null;
}

function schemaRef(operationValue, status, contentType = 'application/json') {
  return operationValue?.responses?.[status]?.content?.[contentType]?.schema?.$ref;
}

invariant(toolingLock.generatorVersion === generatorVersion, 'tooling lock generator version does not match this generator');
invariant(toolingLock.generator === 'scripts/generate-platform-contracts.mjs', 'tooling lock generator path is invalid');
invariant(exactMembers(toolingLock.templates, [
  'contracts/http/templates/platformapi.go.tmpl',
  'contracts/http/templates/platformGateway.ts.tmpl',
]), 'tooling lock templates are invalid');
invariant(exactMembers(toolingLock.outputs, [
  'services/platform-gateway/pkg/platformapi/api.gen.go',
  'apps/hvac-web/src/api/generated/platformGateway.gen.ts',
]), 'tooling lock outputs are invalid');
invariant(spec.openapi === '3.1.0', 'OpenAPI version must be 3.1.0');

const expectedOperations = {
  getHealth: ['get', '/api/v1/health'],
  getVersion: ['get', '/api/v1/version'],
  getPlatformStatus: ['get', '/api/v1/platform/status'],
  beginLogin: ['post', '/api/v1/auth/login'],
  completeLogin: ['get', '/api/v1/auth/callback'],
  getCurrentPrincipal: ['get', '/api/v1/principal'],
  logout: ['post', '/api/v1/auth/logout'],
  revokeSession: ['post', '/api/v1/auth/sessions/{sessionId}/revoke'],
  getSessionAuditEvent: ['get', '/api/v1/audit/session-events/{messageId}'],
  listSites: ['get', '/api/v1/sites'],
  getSite: ['get', '/api/v1/sites/{siteId}'],
  listSiteAsset: ['get', '/api/v1/sites/{siteId}/assets'],
  getAsset: ['get', '/api/v1/assets/{assetId}'],
  listSiteDevices: ['get', '/api/v1/sites/{siteId}/devices'],
  listSiteDeviceBindings: ['get', '/api/v1/sites/{siteId}/device-bindings'],
  getSiteAssetModel: ['get', '/api/v1/sites/{siteId}/asset-model'],
  getDevice: ['get', '/api/v1/devices/{deviceId}'],
  listAlarmsV212: ['get', '/api/v1/alarms'],
  getAlarmV212: ['get', '/api/v1/alarms/{alarmId}'],
  ackAlarmV212: ['post', '/api/v1/alarms/{alarmId}/ack'],
};
const operations = {};
for (const [operationId, [method, path]] of Object.entries(expectedOperations)) {
  const value = operation(operationId);
  invariant(value?.method === method && value?.path === path, `${operationId} method/path is unsupported by generator version 7`);
  operations[operationId] = value;
}

const expectedSuccessSchemas = {
  getHealth: 'HealthResponse',
  getVersion: 'BuildInfo',
  getPlatformStatus: 'PlatformStatusResponse',
  getCurrentPrincipal: 'CurrentPrincipalResponse',
  revokeSession: 'SessionRevocationResponse',
  getSessionAuditEvent: 'AuditRecord',
  listSites: 'SiteCollection',
  getSite: 'Site',
  listSiteAsset: 'AssetCollection',
  getAsset: 'Asset',
  listSiteDevices: 'DeviceCollection',
  listSiteDeviceBindings: 'DeviceBindingCollection',
  getSiteAssetModel: 'SiteAssetModel',
  getDevice: 'Device',
};
for (const [operationId, schemaName] of Object.entries(expectedSuccessSchemas)) {
  invariant(schemaRef(operations[operationId].operation, '200') === `#/components/schemas/${schemaName}`, `${operationId} success schema is unsupported`);
}
invariant(operations.logout.operation.responses?.['204'], 'logout must return 204');
invariant(operations.logout.operation.responses?.['204']?.headers?.Location?.schema?.format === 'uri', 'logout must publish the provider end-session Location header');
for (const operationId of ['listAlarmsV212', 'getAlarmV212', 'ackAlarmV212']) {
  invariant(operations[operationId].operation?.['x-architecture-status'] === 'ACTIVE' && operations[operationId].operation?.['x-shape-status'] === 'READY', `${operationId} must be active with a synchronized Alarm shape`);
  invariant(operations[operationId].operation?.['x-shape-source'] === 'contracts/http/s4-alarm-public.openapi.json', `${operationId} must use the S4 Alarm subordinate contract`);
}
invariant(schemaRef(operations.listAlarmsV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/CursorAlarmListResponse', 'Alarm list success schema is unsupported');
invariant(schemaRef(operations.getAlarmV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/AlarmEnvelope', 'Alarm detail success schema is unsupported');
invariant(schemaRef(operations.ackAlarmV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/AlarmEnvelope', 'Alarm ACK success schema is unsupported');
invariant(operations.ackAlarmV212.operation.requestBody?.content?.['application/json']?.schema?.$ref === './s4-alarm-public.openapi.json#/components/schemas/AckAlarmRequest', 'Alarm ACK request schema is unsupported');

const schemas = spec.components?.schemas ?? {};
const schemaRequirements = {
  BuildInfo: [['service', 'version', 'commit', 'builtAt'], ['service', 'version', 'commit', 'builtAt']],
  HealthResponse: [['status', 'service', 'checkedAt'], ['status', 'service', 'checkedAt', 'build']],
  PlatformStatusResponse: [['status', 'service', 'implementation', 'version', 'checkedAt', 'routePolicyRevision', 'routeRevision', 'compatibilityMode'], ['status', 'service', 'implementation', 'version', 'checkedAt', 'routePolicyRevision', 'routeRevision', 'compatibilityMode']],
  UserPrincipal: [['subject', 'issuer', 'displayName', 'email', 'roles'], ['subject', 'issuer', 'displayName', 'email', 'roles']],
  ServicePrincipal: [['service', 'spiffeId'], ['service', 'spiffeId']],
  PrincipalContext: [['initiatingPrincipal', 'executingServicePrincipal', 'tenantId', 'audience', 'policyRevision', 'delegationExpiresAt'], ['initiatingPrincipal', 'executingServicePrincipal', 'tenantId', 'audience', 'policyRevision', 'delegationExpiresAt']],
  EffectiveAuthorization: [['capabilitySetVersion', 'policyRevision', 'capabilities'], ['capabilitySetVersion', 'policyRevision', 'capabilities']],
  SessionView: [['id', 'expiresAt', 'idleTimeoutMs', 'csrfToken', 'revocationObjectiveMs', 'lastAuditMessageId'], ['id', 'expiresAt', 'idleTimeoutMs', 'csrfToken', 'revocationObjectiveMs', 'lastAuditMessageId']],
  CurrentPrincipalResponse: [['principal', 'context', 'authorization', 'session'], ['principal', 'context', 'authorization', 'session']],
  SessionRevocationResponse: [['sessionId', 'revokedAt', 'objectiveMs', 'auditMessageId'], ['sessionId', 'revokedAt', 'objectiveMs', 'auditMessageId']],
  AuditRecord: [[
    'ledgerSequence', 'messageId', 'schemaVersion', 'tenantId', 'aggregateType', 'aggregateId', 'aggregateVersion',
    'occurredAt', 'initiatingSubject', 'initiatingIssuer', 'executingService', 'executingSpiffeId',
    'action', 'result', 'policyRevision', 'correlationId', 'causationId', 'traceId', 'payloadSha256', 'previousRecordHash',
    'recordHash', 'recordedAt',
  ], [
    'ledgerSequence', 'messageId', 'schemaVersion', 'tenantId', 'aggregateType', 'aggregateId', 'aggregateVersion',
    'occurredAt', 'initiatingSubject', 'initiatingIssuer', 'executingService', 'executingSpiffeId',
    'action', 'result', 'policyRevision', 'correlationId', 'causationId', 'traceId', 'payloadSha256', 'previousRecordHash',
    'recordHash', 'recordedAt',
  ]],
  Site: [['id', 'tenantId', 'code', 'displayName', 'timezone', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'code', 'displayName', 'timezone', 'status', 'revision', 'createdAt', 'updatedAt']],
  Asset: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'assetType', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'code', 'displayName', 'assetType', 'status', 'revision', 'createdAt', 'updatedAt']],
  Device: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'deviceType', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'code', 'displayName', 'deviceType', 'status', 'revision', 'createdAt', 'updatedAt']],
  DeviceBinding: [['id', 'tenantId', 'siteId', 'deviceId', 'assetId', 'bindingRole', 'status', 'validFrom', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'deviceId', 'assetId', 'bindingRole', 'status', 'validFrom', 'validTo', 'revision', 'createdAt', 'updatedAt']],
  ExternalBinding: [['id', 'tenantId', 'siteId', 'integrationInstanceId', 'provider', 'externalEntityType', 'externalId', 'bindingStatus', 'validFrom', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'integrationInstanceId', 'provider', 'externalEntityType', 'externalId', 'bindingStatus', 'validFrom', 'validTo', 'revision', 'createdAt', 'updatedAt']],
  SiteCollection: [['items', 'nextCursor', 'hasMore'], ['items', 'nextCursor', 'hasMore']],
  AssetCollection: [['items', 'nextCursor', 'hasMore'], ['items', 'nextCursor', 'hasMore']],
  DeviceCollection: [['items', 'nextCursor', 'hasMore'], ['items', 'nextCursor', 'hasMore']],
  DeviceBindingCollection: [['items', 'nextCursor', 'hasMore'], ['items', 'nextCursor', 'hasMore']],
  Space: [['id', 'tenantId', 'siteId', 'parentSpaceId', 'code', 'displayName', 'spaceType', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'parentSpaceId', 'code', 'displayName', 'spaceType', 'status', 'revision', 'createdAt', 'updatedAt']],
  Sensor: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'sensorType', 'manufacturer', 'model', 'serialNumber', 'calibrationDueAt', 'metadata', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'code', 'displayName', 'sensorType', 'manufacturer', 'model', 'serialNumber', 'calibrationDueAt', 'metadata', 'status', 'revision', 'createdAt', 'updatedAt']],
  TelemetryPoint: [['id', 'tenantId', 'siteId', 'reportingDeviceId', 'sensorId', 'pointCode', 'sourceKey', 'displayName', 'pointType', 'valueType', 'unit', 'writable', 'sampleIntervalMs', 'publishIntervalMs', 'staleAfterMs', 'sourceMetadata', 'status', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'reportingDeviceId', 'sensorId', 'pointCode', 'sourceKey', 'displayName', 'pointType', 'valueType', 'unit', 'writable', 'sampleIntervalMs', 'publishIntervalMs', 'staleAfterMs', 'sourceMetadata', 'status', 'revision', 'createdAt', 'updatedAt']],
  AssetRelationship: [['id', 'tenantId', 'siteId', 'fromType', 'fromId', 'toType', 'toId', 'role', 'status', 'validFrom', 'validTo', 'revision', 'createdAt', 'updatedAt'], ['id', 'tenantId', 'siteId', 'fromType', 'fromId', 'toType', 'toId', 'role', 'status', 'validFrom', 'validTo', 'revision', 'createdAt', 'updatedAt']],
  AssetModelCounts: [['spaces', 'assets', 'deviceEndpoints', 'physicalSensors', 'points'], ['spaces', 'assets', 'deviceEndpoints', 'physicalSensors', 'points']],
  SiteAssetModel: [['schemaVersion', 'tenantId', 'siteId', 'spaces', 'assets', 'devices', 'sensors', 'telemetryPoints', 'relationships', 'counts'], ['schemaVersion', 'tenantId', 'siteId', 'spaces', 'assets', 'devices', 'sensors', 'telemetryPoints', 'relationships', 'counts']],
  FieldError: [['field', 'message'], ['field', 'message']],
  ProblemDetails: [['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable'], ['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable', 'fieldErrors']],
};
for (const [name, [required, properties]] of Object.entries(schemaRequirements)) {
  const schema = schemas[name];
  invariant(schema?.type === 'object' && schema.additionalProperties === false, `${name} must be a closed object schema`);
  invariant(exactMembers(schema.required, required), `${name} required fields are unsupported`);
  invariant(exactKeys(schema.properties, properties), `${name} properties are unsupported`);
}

invariant(schemas.BuildInfo.properties.service.const === 'platform-gateway', 'BuildInfo.service must be platform-gateway');
invariant(schemas.HealthResponse.properties.status.const === 'ok', 'HealthResponse.status must be ok');
invariant(schemas.PlatformStatusResponse.properties.service.const === 'platform-status', 'PlatformStatusResponse.service must be platform-status');
invariant(exactMembers(schemas.PlatformStatusResponse.properties.implementation.enum, ['go']), 'PlatformStatusResponse implementation must be Go-only');
invariant(exactMembers(schemas.PlatformStatusResponse.properties.compatibilityMode.enum, ['native']), 'PlatformStatusResponse compatibility mode must be native-only');
invariant(schemas.ServicePrincipal.properties.service.const === 'platform-gateway', 'ServicePrincipal.service must be platform-gateway');
invariant(schemas.PrincipalContext.properties.audience.const === 'iam-service', 'PrincipalContext.audience must be iam-service');
invariant(schemas.Capability?.type === 'string' && exactMembers(schemas.Capability.enum, [
  'site.list',
  'site.read',
  'equipment.list',
  'equipment.read',
  'device.list',
  'device.read',
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.history.read',
  'alarm.list',
  'alarm.read',
  'work-order.list',
  'work-order.read',
  'work-order.create',
  'work-order.assign',
  'work-order.lifecycle',
  'session.revoke',
  'audit.read',
  'iam.admin',
  'api-credential.manage',
]), 'Capability vocabulary is unsupported');
invariant(schemas.EffectiveAuthorization.properties.capabilitySetVersion.const === 8, 'EffectiveAuthorization capability set version must be 8');
invariant(schemas.EffectiveAuthorization.properties.policyRevision.minLength === 1 && schemas.EffectiveAuthorization.properties.policyRevision.maxLength === 128, 'EffectiveAuthorization policy revision bounds are unsupported');
invariant(schemas.EffectiveAuthorization.properties.capabilities.uniqueItems === true && schemas.EffectiveAuthorization.properties.capabilities.maxItems === 21, 'EffectiveAuthorization capabilities must be unique and bounded');
invariant(schemas.EffectiveAuthorization.properties.capabilities.items?.$ref === '#/components/schemas/Capability', 'EffectiveAuthorization capabilities must use the public Capability vocabulary');
invariant(schemas.AuditRecord.properties.schemaVersion.const === 1, 'AuditRecord.schemaVersion must be 1');
invariant(schemas.AuditRecord.properties.aggregateType.const === 'bff-session', 'AuditRecord.aggregateType must be bff-session');
invariant(schemas.AuditRecord.properties.executingService.const === 'platform-gateway', 'AuditRecord.executingService must be platform-gateway');
invariant(schemas.UUIDv7?.pattern === '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$', 'UUIDv7 pattern is unsupported');
invariant(schemas.Revision?.minimum === 1, 'Revision minimum is unsupported');
invariant(schemas.Instant?.pattern === '^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}\\.\\d{3}Z$', 'Instant format must be RFC3339 UTC milliseconds');
invariant(schemas.OpaqueCursor?.pattern === '^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$', 'OpaqueCursor format is unsupported');
invariant(exactMembers(schemas.AssetRelationship.properties.fromType.enum, ['ASSET', 'DEVICE', 'SENSOR', 'POINT']), 'AssetRelationship.fromType vocabulary is stale');
invariant(exactMembers(schemas.AssetRelationship.properties.toType.enum, ['SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT']), 'AssetRelationship.toType vocabulary is stale');
invariant(exactMembers(schemas.ProblemDetails.properties.code['x-stable-codes'], [
  'RESOURCE_NOT_FOUND',
  'CURSOR_INVALID',
  'REGISTRY_UNAVAILABLE',
  'REGISTRY_TIMEOUT',
  'MAPPING_INVALID',
  'MAPPING_QUARANTINED',
]), 'Registry Problem Details codes are unsupported');
invariant(schemas.ProblemDetails.properties.code.pattern === '^[A-Z][A-Z0-9_]+$', 'ProblemDetails.code pattern is unsupported');
invariant(schemas.ProblemDetails.properties.traceId.pattern === '^[a-f0-9]{32}$', 'ProblemDetails.traceId pattern is unsupported');
invariant(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'public Problem response must use application/problem+json');

const banner = `Generator: platform-contracts@${generatorVersion}; Contract SHA-256: ${digest}`;
const replacements = {
  __CONTRACT_BANNER__: banner,
  __HEALTH_PATH__: operations.getHealth.path,
  __VERSION_PATH__: operations.getVersion.path,
  __PLATFORM_STATUS_PATH__: operations.getPlatformStatus.path,
  __LOGIN_PATH__: operations.beginLogin.path,
  __CALLBACK_PATH__: operations.completeLogin.path,
  __PRINCIPAL_PATH__: operations.getCurrentPrincipal.path,
  __LOGOUT_PATH__: operations.logout.path,
  __REVOKE_PATH__: operations.revokeSession.path,
  __AUDIT_PATH__: operations.getSessionAuditEvent.path,
  __SITES_PATH__: operations.listSites.path,
  __SITE_PATH__: operations.getSite.path,
  __SITE_ASSET_PATH__: operations.listSiteAsset.path,
  __ASSET_PATH__: operations.getAsset.path,
  __SITE_DEVICES_PATH__: operations.listSiteDevices.path,
  __SITE_DEVICE_BINDINGS_PATH__: operations.listSiteDeviceBindings.path,
  __SITE_ASSET_MODEL_PATH__: operations.getSiteAssetModel.path,
  __DEVICE_PATH__: operations.getDevice.path,
  __ALARMS_PATH__: operations.listAlarmsV212.path,
  __ALARM_PATH__: operations.getAlarmV212.path,
  __ALARM_ACK_PATH__: operations.ackAlarmV212.path,
};

function render(template) {
  let output = template;
  for (const [placeholder, value] of Object.entries(replacements)) {
    if (output.includes(placeholder)) output = output.replaceAll(placeholder, value);
  }
  invariant(!/__[A-Z_]+__/.test(output), 'template contains an unresolved placeholder');
  return output;
}

function formatGo(source) {
  const result = spawnSync(gofmtBinary, [], { input: source, encoding: 'utf8', windowsHide: true });
  invariant(!result.error, `gofmt could not start: ${result.error?.message ?? 'unknown error'}`);
  invariant(result.status === 0, `gofmt failed: ${result.stderr || `exit ${result.status}`}`);
  invariant(typeof result.stdout === 'string' && result.stdout.length > 0, 'gofmt returned empty output');
  return result.stdout;
}

async function emit(path, content) {
  let existing = null;
  try {
    existing = await readFile(path, 'utf8');
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  if (checkOnly) {
    if (existing === null || normalizeLineEndings(existing) !== content) {
      process.stderr.write(`Generated contract drift: ${path}\n`);
      process.exitCode = 1;
    }
    return;
  }
  await mkdir(dirname(path), { recursive: true });
  if (existing !== content) await writeFile(path, content, 'utf8');
}

await Promise.all([
  emit(goOutputPath, formatGo(render(goTemplate))),
  emit(tsOutputPath, render(tsTemplate)),
]);
if (!checkOnly) process.stdout.write(`Generated platform contracts (${digest.slice(0, 12)}).\n`);
