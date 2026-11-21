import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const generatorVersion = '7.1.0';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specPath = resolve(root, 'contracts/http/platform-gateway.openapi.yaml');
const toolingLockPath = resolve(root, 'contracts/http/tooling.lock.json');
const goTemplatePath = resolve(root, 'contracts/http/templates/platformapi.go.tmpl');
const tsTemplatePath = resolve(root, 'contracts/http/templates/platformGateway.ts.tmpl');
const goOutputPath = resolve(root, 'cmd/energy-api/internal/platformapi/api.gen.go');
const tsOutputPath = resolve(root, 'apps/hvac-web/src/api/generated/platformGateway.gen.ts');
const checkOnly = process.argv.includes('--check');
const gofmtBinary = process.env.GOFMT_BINARY ?? 'gofmt';

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
  'cmd/energy-api/internal/platformapi/api.gen.go',
  'apps/hvac-web/src/api/generated/platformGateway.gen.ts',
]), 'tooling lock outputs are invalid');
invariant(spec.openapi === '3.1.0', 'OpenAPI version must be 3.1.0');

const gw = 'gateways/{gatewayId}';
const conc = `${gw}/concentrators/{unitId}`;
const site = 'sites/{siteId}';
const sav = `${site}/savings/measures`;
const opt = `${site}/optimization`;
const w4 = [
  ['listAlarmRuleDeadLetters', 'get', 'alarm-rules/dead-letters', 'AlarmRuleDeadLetterListEnvelope', '__ALARM_RULES_DEAD_LETTERS_PATH__'],
  ['replayAlarmRuleDeadLetter', 'post', 'alarm-rules/dead-letters/{outboxId}/replay', 'AlarmRuleDeadLetterReplayEnvelope', '__ALARM_RULES_DEAD_LETTER_REPLAY_PATH__'],
  ['putDevicePriorityLock', 'put', `${site}/devices/{deviceId}/priority-lock`, 'DevicePriorityLockState', '__DEVICE_PRIORITY_LOCK_PATH__'],
  ['deleteDevicePriorityLock', 'delete', `${site}/devices/{deviceId}/priority-lock`, 'DevicePriorityLockReleaseResponse'],
  ['listControlAlarmDeadLetters', 'get', `${site}/control-alarms/dead-letters`, 'ControlAlarmDeadLetterListEnvelope', '__CONTROL_ALARM_DEAD_LETTERS_PATH__'],
  ['replayControlAlarmDeadLetter', 'post', `${site}/control-alarms/dead-letters/{outboxId}/replay`, 'ControlAlarmDeadLetterReplayResponse', '__CONTROL_ALARM_DEAD_LETTER_REPLAY_PATH__'],
  ['getSiteAlarmRouting', 'get', `${site}/alarm-routing`, 'SiteAlarmRoutingEnvelope', '__SITE_ALARM_ROUTING_PATH__'],
  ['reloadSiteAlarmRouting', 'post', `${site}/alarm-routing/reload`, 'SiteAlarmRoutingReloadEnvelope', '__SITE_ALARM_ROUTING_RELOAD_PATH__'],
];
const expectedOperations = {
  ...Object.fromEntries(w4.map(([k, m, p]) => [k, [m, p]])),
  getGatewayCredential: ['get', `${gw}/credential`],
  listGatewayCredentials: ['get', `${gw}/credentials`],
  revokeGatewayCertificate: ['post', `${gw}/credentials/{credentialId}/revoke`],
  generateGatewayEnrollmentCode: ['post', `${gw}/enrollment-code`],
  revokeGatewayCredential: ['post', `${gw}/revoke`],
  rotateGatewayEnrollment: ['post', `${gw}/rotate`],
  getGatewayConcentratorStatus: ['get', `${conc}/status`],
  updateGatewayConcentratorParams: ['put', `${conc}/params`],
  getGatewayConcentratorEids: ['get', `${conc}/eids`],
  updateGatewayConcentratorEids: ['put', `${conc}/eids`],
  getGatewayConfigShadow: ['get', `${gw}/config-shadow`],
  updateGatewayConfigShadow: ['put', `${gw}/config-shadow`],
  listGatewayQuarantine: ['get', `${gw}/quarantine`],
  getGatewayPacketCapture: ['get', `${gw}/packet-capture`],
  startGatewayPacketCapture: ['post', `${gw}/packet-capture/start`],
  stopGatewayPacketCapture: ['post', `${gw}/packet-capture/stop`],
  getHealth: ['get', 'health'],
  getVersion: ['get', 'version'],
  getPlatformStatus: ['get', 'platform/status'],
  beginLogin: ['post', 'auth/login'],
  completeLogin: ['get', 'auth/callback'],
  getCurrentPrincipal: ['get', 'principal'],
  switchActiveTenant: ['post', 'auth/session/tenant'],
  logout: ['post', 'auth/logout'],
  revokeSession: ['post', 'auth/sessions/{sessionId}/revoke'],
  getSessionAuditEvent: ['get', 'audit/session-events/{messageId}'],
  searchAudit: ['post', 'audit/search'],
  queryEnergySeries: ['post', 'analytics/energy-series'],
  listSites: ['get', 'sites'],
  getSite: ['get', site],
  listAssets: ['get', 'assets'],
  listSiteAssets: ['get', `${site}/assets`],
  getAsset: ['get', 'assets/{assetId}'],
  listDevices: ['get', 'devices'],
  listSiteDevices: ['get', `${site}/devices`],
  listSiteDeviceBindings: ['get', `${site}/device-bindings`],
  getSiteAssetModel: ['get', `${site}/asset-model`],
  getSiteTariff: ['get', `${site}/tariff`],
  getDevice: ['get', 'devices/{deviceId}'],
  listTemplates: ['get', 'templates'],
  getTemplateRevision: ['get', 'templates/revisions/{revisionId}'],
  listSiteTemplateAssignments: ['get', `${site}/template-assignments`],
  listSiteSpaceChildren: ['get', `${site}/spaces/tree`],
  listDevicePoints: ['get', 'devices/{deviceId}/points'],
  createSite: ['post', 'sites'],
  updateSite: ['patch', site],
  createSiteSpace: ['post', `${site}/spaces`],
  updateSiteSpace: ['patch', `${site}/spaces/{spaceId}`],
  createSiteAsset: ['post', `${site}/assets`],
  updateSiteAsset: ['patch', `${site}/assets/{assetId}`],
  createSiteDevice: ['post', `${site}/devices`],
  updateSiteDevice: ['patch', `${site}/devices/{deviceId}`],
  replaceDeviceHardware: ['post', `${site}/devices/{deviceId}/replace-hardware`],
  createSiteSensor: ['post', `${site}/sensors`],
  updateSiteSensor: ['patch', `${site}/sensors/{sensorId}`],
  createSitePoint: ['post', `${site}/points`],
  updateSitePoint: ['patch', `${site}/points/{pointId}`],
  rebindRegistryResource: ['post', `${site}/bindings/rebind`],
  releaseRegistryTemplateRevision: ['post', 'templates/revisions'],
  assignRegistryTemplateRevision: ['post', 'templates/assignments'],
  planRegistryImport: ['post', `${site}/imports/dry-run`],
  commitRegistryImport: ['post', `${site}/imports/commit`],
  beginRegistryRetirement: ['post', `${site}/retire`],
  listAlarmsV212: ['get', 'alarms'],
  getAlarmV212: ['get', 'alarms/{alarmId}'],
  ackAlarmV212: ['post', 'alarms/{alarmId}/ack'],
  assignAlarmV212: ['post', 'alarms/{alarmId}/assign'],
  unassignAlarmV212: ['post', 'alarms/{alarmId}/unassign'],
  suppressAlarmV212: ['post', 'alarms/{alarmId}/suppress'],
  unsuppressAlarmV212: ['post', 'alarms/{alarmId}/unsuppress'],
  listAlarmEvaluationsV212: ['get', 'alarms/{alarmId}/evaluations'],
  listSiteAlarmRules: ['get', 'alarm-rules'],
  simulateSiteAlarmRule: ['post', 'alarm-rules/simulate'],
  listCommandsV212: ['get', 'commands'],
  getCommandV212: ['get', 'commands/{commandId}'],
  approveCommandV212: ['post', 'commands/{commandId}/approve'],
  reconcileCommandV212: ['post', 'commands/{commandId}/reconcile'],
  cancelCommandV212: ['post', 'commands/{commandId}/cancel'],
  getSiteLoadForecastV212: ['get', `${site}/forecast/load`],
  listSiteFddFindingsV212: ['get', `${site}/fdd/findings`],
  evaluateSiteFddLowDeltaTV212: ['post', `${site}/fdd/evaluate/low-delta-t`],
  getSiteFddLowDeltaTRuleV212: ['get', `${site}/fdd/rules/low-delta-t`],
  listSiteSavingsMeasuresV212: ['get', sav],
  registerSiteSavingsMeasureV212: ['post', sav],
  getSiteSavingsMeasureV212: ['get', `${sav}/{measureId}`],
  implementSiteSavingsMeasureV212: ['post', `${sav}/{measureId}/implement`],
  refitSiteSavingsMeasureV212: ['post', `${sav}/{measureId}/refit`],
  endSiteSavingsMeasureV212: ['post', `${sav}/{measureId}/end`],
  getSiteSavingsSummaryV212: ['get', `${site}/savings/summary`],
  listSiteMonthlyReportsV212: ['get', `${site}/reports/monthly`],
  getSiteMonthlyReportV212: ['get', `${site}/reports/monthly/{month}`],
  createOptimizationRunV212: ['post', 'optimization/runs'],
  getLatestSiteOptimizationRecommendationV212: ['get', `${opt}/recommendations/latest`],
  getLatestSiteOptimizationPreparationV212: ['get', `${opt}/preparations/latest`],
  getLatestSiteOptimizationShadowEvaluationV212: ['get', `${opt}/shadow-evaluations/latest`],
  getLatestSiteOptimizationStagingRecommendations: ['get', `${opt}/staging-recommendations/latest`],
  getSiteOperatingSchedule: ['get', `${site}/operating-schedule`],
  saveSiteOperatingSchedule: ['put', `${site}/operating-schedule`],
  listSiteOperatingExceptions: ['get', `${site}/operating-exceptions`],
  createSiteOperatingException: ['post', `${site}/operating-exceptions`],
  decideSiteOperatingException: ['post', `${site}/operating-exceptions/{exceptionId}/decide`],
  listSiteMonthlyElectricityBudgets: ['get', `${site}/electricity-budgets`],
  saveSiteMonthlyElectricityBudget: ['put', `${site}/electricity-budgets`],
  listSiteUtilityBills: ['get', `${site}/utility-bills`],
  createSiteUtilityBill: ['post', `${site}/utility-bills`],
  updateSiteUtilityBill: ['put', `${site}/utility-bills/{billId}`],
  confirmSiteUtilityBill: ['post', `${site}/utility-bills/{billId}/confirm`],
  recalculateSiteAccounting: ['post', `${site}/accounting/recalculate`],
  listSiteAccountingRecalcJobs: ['get', `${site}/accounting/recalculate-jobs`],
  listSiteCalendarHolidays: ['get', `${site}/accounting/holidays`],
  upsertSiteCalendarHoliday: ['put', `${site}/accounting/holidays/{date}`],
  deleteSiteCalendarHoliday: ['delete', `${site}/accounting/holidays/{date}`],
  listSiteMeterRatioVersions: ['get', `${site}/accounting/meter-ratios`],
  createSiteMeterRatioVersion: ['post', `${site}/accounting/meter-ratios`],
  retireSiteMeterRatioVersion: ['post', `${site}/accounting/meter-ratios/{versionId}/retire`],
  getSiteCounterAudit: ['get', `${site}/accounting/counter-audit`],
  listSiteDataGaps: ['get', `${site}/data-gaps`],
  getSiteControlState: ['get', `${site}/control-state`],
  updateSiteControlMode: ['put', `${site}/control-mode`],
  executeSiteControlShutdown: ['post', `${site}/control-shutdown`],
  updateAssetControlLock: ['put', 'assets/{assetId}/control-lock'],
  getSiteAutoControlLease: ['get', `${site}/optimization/auto-control-lease`],
  armSiteAutoControlLease: ['put', `${site}/optimization/auto-control-lease`],
  revokeSiteAutoControlLease: ['post', `${site}/optimization/auto-control-lease/revoke`],
  listSiteOptimizationExecutionMonitors: ['get', `${site}/optimization/execution-monitors`],
  getSiteOptimizationGovernanceSummary: ['get', `${opt}/governance-summary`],
  listSiteOptimizationCanaryBindings: ['get', `${opt}/canary-bindings`],
  upsertSiteOptimizationCanaryBinding: ['put', `${opt}/canary-bindings/{loopKey}`],
  deleteSiteOptimizationCanaryBinding: ['delete', `${opt}/canary-bindings/{loopKey}`],
  listSiteOptimizationNegativeSamples: ['get', `${opt}/negative-samples`],
};
const operations = {};
for (const [operationId, [method, subPath]] of Object.entries(expectedOperations)) {
  const value = operation(operationId);
  invariant(value?.method === method && value?.path === `/api/v1/${subPath}`, `${operationId} method/path is unsupported by generator version 7.1`);
  operations[operationId] = value;
}

const expectedSuccessSchemas = {
  ...Object.fromEntries(w4.map(([k, , , s]) => [k, s])),
  getGatewayCredential: 'GatewayCredentialStatus',
  listGatewayCredentials: 'GatewayCredentialList',
  revokeGatewayCertificate: 'GatewayCredentialRecord',
  generateGatewayEnrollmentCode: 'GatewayEnrollmentCode',
  revokeGatewayCredential: 'GatewayCredentialStatus',
  rotateGatewayEnrollment: 'GatewayEnrollmentCode',
  getGatewayConcentratorStatus: 'ConcentratorStatus',
  updateGatewayConcentratorParams: 'ConcentratorParamsWriteResult',
  getGatewayConcentratorEids: 'ConcentratorEidTable',
  updateGatewayConcentratorEids: 'ConcentratorEidWriteResult',
  getGatewayConfigShadow: 'GatewayConfigShadow',
  updateGatewayConfigShadow: 'GatewayConfigShadow',
  listGatewayQuarantine: 'GatewayQuarantineList',
  getGatewayPacketCapture: 'GatewayPacketCaptureSnapshot',
  startGatewayPacketCapture: 'GatewayPacketCaptureSnapshot',
  stopGatewayPacketCapture: 'GatewayPacketCaptureSnapshot',
  getHealth: 'HealthResponse',
  getVersion: 'BuildInfo',
  getPlatformStatus: 'PlatformStatusResponse',
  getCurrentPrincipal: 'CurrentPrincipalResponse',
  switchActiveTenant: 'CurrentPrincipalResponse',
  revokeSession: 'SessionRevocationResponse',
  getSessionAuditEvent: 'AuditRecord',
  searchAudit: 'AuditSearchResult',
  queryEnergySeries: 'EnergySeriesResponse',
  listSites: 'SiteCollection',
  getSite: 'Site',
  listAssets: 'AssetCollection',
  listSiteAssets: 'AssetCollection',
  getAsset: 'Asset',
  listDevices: 'DeviceCollection',
  listSiteDevices: 'DeviceCollection',
  listSiteDeviceBindings: 'DeviceBindingCollection',
  getSiteAssetModel: 'SiteAssetModel',
  getSiteTariff: 'SiteTariff',
  getDevice: 'Device',
  listTemplates: 'TemplateCollection',
  listSiteTemplateAssignments: 'TemplateAssignmentCollection',
  listSiteAlarmRules: 'AlarmRuleList',
  simulateSiteAlarmRule: 'AlarmRuleSimulationEnvelope',
  getSiteOperatingSchedule: 'OperatingSchedule',
  saveSiteOperatingSchedule: 'OperatingSchedule',
  listSiteOperatingExceptions: 'OperatingExceptionList',
  decideSiteOperatingException: 'OperatingException',
  listSiteMonthlyElectricityBudgets: 'MonthlyElectricityBudgetList',
  saveSiteMonthlyElectricityBudget: 'MonthlyElectricityBudget',
  listSiteUtilityBills: 'UtilityBillList',
  updateSiteUtilityBill: 'UtilityBill',
  confirmSiteUtilityBill: 'UtilityBill',
  listSiteAccountingRecalcJobs: 'AccountingRecalcJobList',
  listSiteCalendarHolidays: 'SiteCalendarHolidayList',
  upsertSiteCalendarHoliday: 'SiteCalendarHoliday',
  deleteSiteCalendarHoliday: 'SiteCalendarHolidayDeleteResult',
  listSiteMeterRatioVersions: 'MeterRatioVersionList',
  retireSiteMeterRatioVersion: 'MeterRatioVersion',
  getSiteCounterAudit: 'CounterAuditResponse',
  listSiteDataGaps: 'SiteDataGapList',
  getSiteControlState: 'SiteControlState',
  updateSiteControlMode: 'SiteControlModeMutationResponse',
  executeSiteControlShutdown: 'SiteControlShutdownResponse',
  updateAssetControlLock: 'AssetControlLockMutationResponse',
  getLatestSiteOptimizationPreparationV212: 'OptimizationPreparation',
  getLatestSiteOptimizationStagingRecommendations: 'StagingEvaluation',
  getSiteAutoControlLease: 'SiteAutoControlLeaseState',
  armSiteAutoControlLease: 'SiteAutoControlLeaseState',
  revokeSiteAutoControlLease: 'SiteAutoControlLeaseState',
  listSiteOptimizationExecutionMonitors: 'OptimizationExecutionMonitorList',
  getSiteOptimizationGovernanceSummary: 'OptimizationGovernanceSummary',
  listSiteOptimizationCanaryBindings: 'OptimizationCanaryBindingList',
  upsertSiteOptimizationCanaryBinding: 'OptimizationCanaryBindingRecord',
  deleteSiteOptimizationCanaryBinding: 'OptimizationCanaryBindingDeleteResult',
  listSiteOptimizationNegativeSamples: 'OptimizationNegativeSamplePage',
};
for (const [operationId, schemaName] of Object.entries(expectedSuccessSchemas)) {
  invariant(schemaRef(operations[operationId].operation, '200') === `#/components/schemas/${schemaName}`, `${operationId} success schema is unsupported`);
}
invariant(schemaRef(operations.recalculateSiteAccounting.operation, '202') === '#/components/schemas/AccountingRecalcJob', 'recalculateSiteAccounting accepted schema is unsupported');
invariant(schemaRef(operations.createSiteMeterRatioVersion.operation, '201') === '#/components/schemas/MeterRatioVersion', 'createSiteMeterRatioVersion created schema is unsupported');
invariant(schemaRef(operations.replaceDeviceHardware.operation, '201') === '#/components/schemas/ReplaceDeviceHardwareResult', 'replaceDeviceHardware success schema is unsupported');
invariant(schemaRef(operations.getTemplateRevision.operation, '200') === './s05-registry-writer.openapi.json#/components/schemas/TemplateRevision', 'getTemplateRevision success schema is unsupported');
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
  BuildInfo: [['service', 'version', 'commit', 'builtAt']],
  HealthResponse: [['status', 'service', 'checkedAt'], ['build']],
  PlatformStatusResponse: [['status', 'service', 'implementation', 'version', 'checkedAt', 'routePolicyRevision', 'routeRevision', 'compatibilityMode']],
  UserPrincipal: [['subject', 'issuer', 'displayName', 'email', 'roles']],
  ServicePrincipal: [['service', 'spiffeId']],
  PrincipalContext: [['initiatingPrincipal', 'executingServicePrincipal', 'tenantId', 'audience', 'policyRevision', 'delegationExpiresAt']],
  EffectiveAuthorization: [['capabilitySetVersion', 'policyRevision', 'capabilities']],
  SessionView: [['id', 'expiresAt', 'idleTimeoutMs', 'csrfToken', 'revocationObjectiveMs', 'lastAuditMessageId']],
  TenantMembershipSummary: [['tenantId', 'code', 'displayName']],
  SwitchTenantRequest: [['tenantId']],
  CurrentPrincipalResponse: [['principalId', 'principal', 'context', 'authorization', 'session'], ['availableTenants']],
  SessionRevocationResponse: [['sessionId', 'revokedAt', 'objectiveMs', 'auditMessageId']],
  AuditRecord: [[
    'ledgerSequence', 'messageId', 'schemaVersion', 'tenantId', 'aggregateType', 'aggregateId', 'aggregateVersion',
    'occurredAt', 'initiatingSubject', 'initiatingIssuer', 'executingService', 'executingSpiffeId',
    'action', 'result', 'policyRevision', 'correlationId', 'causationId', 'traceId', 'payloadSha256', 'previousRecordHash',
    'recordHash', 'recordedAt',
  ]],
  Site: [['id', 'tenantId', 'code', 'displayName', 'timezone', 'status', 'revision', 'createdAt', 'updatedAt', 'floorAreaM2', 'climateZone']],
  Asset: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'assetType', 'status', 'revision', 'createdAt', 'updatedAt'], ['ratedPower', 'ratedCapacity', 'tags', 'waterLoop', 'branchCode']],
  Device: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'deviceType', 'status', 'revision', 'createdAt', 'updatedAt'], ['externalId', 'protocolAddress', 'tags']],
  ReplaceDeviceHardwareResult: [[
    'replacementId', 'tenantId', 'siteId', 'deviceId', 'revision', 'previousExternalId', 'newExternalId',
    'previousGatewayDeviceId', 'newGatewayDeviceId', 'previousProtocolAddress', 'newProtocolAddress',
    'reason', 'replacedBy', 'replacedAt', 'device', 'replayed',
  ]],
  Template: [['id', 'tenantId', 'templateKey', 'templateKind', 'createdAt'], ['latestRevisionId', 'latestRevisionNumber', 'releasedAt']],
  TemplateCollection: [['items', 'nextCursor', 'hasMore']],
  TemplateAssignmentCollection: [['items', 'nextCursor', 'hasMore']],
  DeviceBinding: [['id', 'tenantId', 'siteId', 'deviceId', 'assetId', 'bindingRole', 'status', 'validFrom', 'revision', 'createdAt', 'updatedAt'], ['validTo']],
  ExternalBinding: [['id', 'tenantId', 'siteId', 'integrationInstanceId', 'provider', 'externalEntityType', 'externalId', 'bindingStatus', 'validFrom', 'revision', 'createdAt', 'updatedAt'], ['validTo']],
  SiteCollection: [['items', 'nextCursor', 'hasMore']],
  AssetCollection: [['items', 'nextCursor', 'hasMore']],
  DeviceCollection: [['items', 'nextCursor', 'hasMore']],
  DeviceBindingCollection: [['items', 'nextCursor', 'hasMore']],
  Space: [['id', 'tenantId', 'siteId', 'parentSpaceId', 'code', 'displayName', 'spaceType', 'status', 'revision', 'createdAt', 'updatedAt']],
  Sensor: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'sensorType', 'manufacturer', 'model', 'serialNumber', 'calibrationDueAt', 'metadata', 'status', 'revision', 'createdAt', 'updatedAt']],
  TelemetryPoint: [['id', 'tenantId', 'siteId', 'reportingDeviceId', 'sensorId', 'pointCode', 'sourceKey', 'displayName', 'pointType', 'valueType', 'unit', 'writable', 'sampleIntervalMs', 'publishIntervalMs', 'staleAfterMs', 'counterDecreaseMode', 'counterRolloverModulus', 'sourceMetadata', 'status', 'revision', 'createdAt', 'updatedAt']],
  AssetRelationship: [['id', 'tenantId', 'siteId', 'fromType', 'fromId', 'toType', 'toId', 'role', 'status', 'validFrom', 'validTo', 'revision', 'createdAt', 'updatedAt']],
  AssetModelCounts: [['spaces', 'assets', 'deviceEndpoints', 'physicalSensors', 'points']],
  SiteAssetModel: [['schemaVersion', 'tenantId', 'siteId', 'spaces', 'assets', 'devices', 'sensors', 'telemetryPoints', 'relationships', 'counts']],
  SiteTariff: [['siteId', 'displayName', 'version', 'effectiveFrom', 'timezone', 'currency', 'periods'], ['holidayDates']],
  TariffPeriod: [['periodCode', 'dayType', 'startMinute', 'endMinute', 'energyRatePerKWh', 'demandRatePerKW']],
  AccountingRecalculateRequest: [['fromDate', 'toDate', 'reason'], ['granularity']],
  AccountingRecalcJob: [[
    'id', 'tenantId', 'siteId', 'granularity', 'windowStart', 'windowEnd',
    'status', 'availableAt', 'requestedBy', 'requestedByDisplay', 'reason',
    'source', 'attemptCount', 'lastError', 'createdAt', 'updatedAt', 'finishedAt',
  ]],
  AccountingRecalcJobList: [['items']],
  SiteCalendarHoliday: [['id', 'tenantId', 'siteId', 'holidayDate', 'dayType', 'label', 'revision', 'createdAt', 'updatedAt']],
  SiteCalendarHolidayList: [['items']],
  SiteCalendarHolidayUpsertRequest: [['dayType'], ['label', 'expectedRevision']],
  SiteCalendarHolidayDeleteResult: [['deleted', 'siteId', 'holidayDate']],
  MeterRatioVersion: [
    ['id', 'tenantId', 'siteId', 'ctRatio', 'ptRatio', 'meterMultiplier', 'ratioApplicationMode', 'readingUnit', 'effectiveFrom', 'effectiveTo', 'status', 'revision', 'createdAt', 'updatedAt'],
    ['meterId', 'meterBindingId', 'pointId', 'version', 'cloudMultiplier'],
  ],
  MeterRatioVersionList: [['items']],
  MeterRatioVersionCreateRequest: [
    ['ctRatio', 'ptRatio', 'meterMultiplier', 'ratioApplicationMode', 'readingUnit', 'effectiveFrom'],
    ['meterId', 'meterBindingId', 'pointId', 'effectiveTo', 'reason'],
  ],
  MeterRatioVersionRetireRequest: [[], ['effectiveTo', 'expectedRevision', 'reason']],
  CounterAuditSummary: [['totalSamples', 'anomalyCount', 'zeroDropCount', 'spikeCount', 'resetCount', 'rolloverCount', 'invalidDecreaseCount']],
  CounterAuditItem: [['pointId', 'deviceId', 'telemetryKey', 'periodStart', 'periodEnd', 'rawPrevious', 'rawCurrent', 'cleanedDeltaKWh', 'transitionType', 'quality', 'qualityReasons', 'unit']],
  CounterAuditResponse: [['siteId', 'from', 'to', 'summary', 'items', 'truncated']],
  SiteDataGap: [['alarmId', 'deviceId', 'deviceName', 'pointId', 'pointCode', 'pointName', 'since', 'affectedFigures', 'excludesDeviceFromTotal']],
  SiteDataGapList: [['items']],
  AssetControlLockState: [['siteId', 'assetId', 'lockMode', 'reason', 'updatedBy', 'updatedAt', 'revision'], ['id', 'tenantId', 'updatedByName', 'boundDeviceIds']],
  ActiveMaintenanceWindow: [['id', 'title', 'startDate', 'endDate', 'reason']],
  SiteControlState: [['tenantId', 'siteId', 'controlMode', 'reason', 'updatedBy', 'updatedAt', 'revision', 'assetLocks'], ['id', 'updatedByName', 'devicePriorityLocks', 'activeMaintenanceWindow']],
  SiteControlModeMutation: [['controlMode'], ['reason', 'expectedRevision', 'meta']],
  SiteControlModeMutationResponse: [['tenantId', 'siteId', 'controlMode', 'reason', 'updatedBy', 'updatedAt', 'revision', 'assetLocks', 'cancelledCommandIds'], ['id', 'updatedByName', 'devicePriorityLocks', 'activeMaintenanceWindow']],
  AssetControlLockMutation: [['lockMode'], ['reason', 'expectedRevision', 'meta']],
  AssetControlLockMutationResponse: [['siteId', 'assetId', 'lockMode', 'reason', 'updatedBy', 'updatedAt', 'revision', 'cancelledCommandIds'], ['id', 'tenantId', 'updatedByName', 'boundDeviceIds']],
  SiteControlShutdownRequest: [['mode', 'reason', 'verificationCode'], ['unitCode', 'confirmEmergencyDoRelease', 'expectedRevision']],
  SiteControlShutdownResponse: [['siteControlState', 'mode', 'cancelledCommandIds', 'submittedCommandIds', 'deferredHoldingAssetIds']],
  StagingDiagnostic: [['scope', 'status', 'reason', 'detail'], ['assetId', 'assetCode']],
  StagingRecommendationDraft: [
    ['id', 'kind', 'confirmationMode', 'requiresSupervisorReview', 'requiresTotp', 'suggestedCapability', 'targetAssetId', 'targetAssetType', 'predictedPowerSavingKW', 'estimatedHourlySavingKWh', 'estimatedShiftSavingKWh', 'shiftHours', 'rationale', 'preconditions'],
    ['suggestedCommandPointId', 'targetAssetCode', 'targetAssetName', 'powerSavingBasis', 'currentTotalPowerKW', 'projectedTotalPowerKW', 'currentCoolingLoadKW', 'remainingCapacityKW', 'headroomMarginFraction', 'copBefore', 'projectedCopAfter', 'continuousIdleSeconds', 'continuousIdleMinutes', 'servedChillerIds', 'interlockSafetyStatement', 'extrapolated'],
  ],
  StagingEvaluation: [['tenantId', 'siteId', 'evaluatedAt', 'confirmationMode', 'recommendations', 'diagnostics'], ['siteControlMode']],
  OptimizationPreparationResponseCoefficient: [['status', 'calibratedAt']],
  OptimizationPreparationTenantStrategy: [['status']],
  OptimizationPreparation: [['trigger', 'status', 'reason', 'firedAt', 'completedAt', 'responseCoefficient', 'tenantStrategy']],
  AutoControlLease: [['id', 'tenantId', 'siteId', 'status', 'armedBy', 'armedAt', 'expiresAt', 'allowedCapabilities', 'maxSetpointStepC', 'supplyTempMinC', 'supplyTempMaxC', 'zoneTempMaxC', 'minRunningChillers', 'maxCommandsPerHour', 'revision', 'createdAt', 'updatedAt'], ['deadbandC', 'manualBackoffUntil', 'manualBackoffReason', 'tripReason', 'executionMode', 'pumpFreqMinHz', 'pumpFreqMaxHz', 'maxFreqStepHz', 'freqDeadbandHz', 'fanSpeedMinPct', 'fanSpeedMaxPct', 'maxFanStepPct', 'fanDeadbandPct']],
  SiteAutoControlLeaseState: [['tenantId', 'siteId', 'confirmationMode', 'activeLease', 'latestLease']],
  ArmAutoControlLeaseRequest: [['allowedCapabilities', 'supplyTempMinC', 'supplyTempMaxC', 'reason', 'verificationCode'], ['expiresInHours', 'expiresAt', 'maxSetpointStepC', 'deadbandC', 'zoneTempMaxC', 'minRunningChillers', 'maxCommandsPerHour', 'executionMode', 'pumpFreqMinHz', 'pumpFreqMaxHz', 'maxFreqStepHz', 'freqDeadbandHz', 'fanSpeedMinPct', 'fanSpeedMaxPct', 'maxFanStepPct', 'fanDeadbandPct', 'expectedRevision']],
  RevokeAutoControlLeaseRequest: [['reason'], ['expectedRevision']],
  OptimizationExecutionMonitor: [
    ['id', 'tenantId', 'siteId', 'leaseId', 'recommendationId', 'commandIntentId', 'deviceId', 'targetCapability', 'zoneTempMaxC', 'supplyTempMaxC', 'startedAt', 'windowEndsAt', 'status', 'createdAt', 'updatedAt'],
    ['loopKey', 'targetUnit', 'baselineSupplyTempC', 'appliedSupplyTempC', 'restoreSupplyTempC', 'supplyTempMinC', 'rawRecommendedValue', 'clampedValue', 'guardrailVerdict', 'guardrailTrace', 'executionMode', 'decisionId', 'traceId', 'lastCheckedAt', 'telemetryUnhealthySince', 'tripReason', 'rollbackCommandIntentId'],
  ],
  OptimizationExecutionMonitorList: [['tenantId', 'siteId', 'items']],
  OptimizationActiveSolver: [['servingRef', 'source', 'loopKey', 'deploymentRevisionId']],
  OptimizationTickCounts: [['aiClosedLoop', 'aiShadow', 'ruleBaseline', 'manualOverride']],
  OptimizationCopComparison: [['matchedBinCount', 'matchedAiSamples', 'matchedBaselineSamples', 'weightedBaselineCOP', 'weightedAiCOP', 'weightedCopUpliftPct', 'totalEstimatedSavedKWh', 'cells']],
  OptimizationFlywheelSummary: [['totalSamples', 'guardrailClamped', 'guardrailVetoed', 'manualOverride', 'monitorTripped', 'trackingDeviation']],
  OptimizationGovernanceSummary: [['tenantId', 'siteId', 'windowDays', 'generatedAt', 'activeSolver', 'tickCounts', 'copComparison', 'flywheel']],
  OptimizationCanaryBindingRecord: [['id', 'tenantId', 'siteId', 'loopKey', 'servingRef', 'status', 'createdAt', 'updatedAt']],
  OptimizationCanaryBindingList: [['tenantId', 'siteId', 'allowedSolverHosts', 'activeSolver', 'items']],
  UpsertOptimizationCanaryBindingRequest: [['servingRef', 'status']],
  OptimizationCanaryBindingDeleteResult: [['deleted', 'siteId', 'loopKey']],
  OptimizationGuardrailStageResult: [['stage', 'action', 'before', 'after'], ['reason']],
  OptimizationFeatureSnapshot: [['runningChillers', 'cabinetLocalManual', 'deviceFaultActive', 'leaseDeadbandC', 'supplyTempMinC', 'supplyTempMaxC'], ['supplyTempC', 'zoneMaxTempC', 'outdoorWetBulbC', 'coolingLoadKW', 'chillerPowerKW', 'observedCOP']],
  OptimizationNegativeSampleItem: [['id', 'decisionId', 'recommendationId', 'monitorId', 'traceId', 'sampleKind', 'reasonCode', 'solverServingRef', 'rawRecommendedValue', 'appliedOrClampedValue', 'occurredAt', 'guardrailTrace', 'featureSnapshot']],
  OptimizationNegativeSamplePage: [['tenantId', 'siteId', 'windowDays', 'sampleKind', 'total', 'limit', 'offset', 'hasMore', 'items']],
  GatewayCredentialRecord: [['id', 'gatewayId', 'fingerprintSha256', 'status', 'validFrom', 'validUntil', 'createdAt'], ['serialNumber', 'revokedAt', 'revocationReason']],
  GatewayCredentialList: [['items']],
  GatewayCertificateRevokeRequest: [[], ['reason']],
  PointCopyTarget: [['deviceKey', 'pointCode']],
  GatewayPointMapping: [['sourceChannel', 'primaryDeviceKey', 'primaryPointCode'], ['copyTargets']],
  GatewaySamplingPolicy: [['deviceKey', 'sampleIntervalMs', 'publishIntervalMs'], ['pointCode']],
  ConcentratorUnitShadow: [[], ['params', 'eids']],
  ConfigShadowDriftDetail: [['section', 'target', 'reason', 'detail']],
  GatewayModbusRegisterMapping: [['pointCode', 'functionCode', 'address', 'registerCount', 'rawType', 'byteOrder', 'wordOrder', 'scale', 'offset', 'pollPriority'], ['semanticChannel', 'writeFunctionCode', 'bitIndex', 'enumMap']],
  GatewayModbusDriverConfig: [['deviceKey', 'driverType', 'endpoint', 'unitId', 'timeoutMs', 'retries', 'registers'], ['capabilityProfile']],
  GatewayAutonomyPolicy: [['enabled', 'offlineGraceSeconds', 'defaultChilledWaterSupplySetpointC', 'defaultPumpFrequencyHz', 'minPumpFrequencyHz', 'maxPumpFrequencyHz', 'minRunSeconds', 'minStopSeconds', 'snapshotIntervalSeconds']],
  GatewayDesiredConfig: [['desiredRevision', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'updatedBy', 'reason', 'createdAt', 'updatedAt'], ['driverConfigs', 'autonomyPolicy']],
  GatewayReportedConfig: [['reportedRevision', 'appliedStatus', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'driftDetails', 'reportedAt', 'updatedAt'], ['driverConfigs', 'autonomyPolicy', 'errorMessage']],
  GatewayConfigShadow: [['tenantId', 'gatewayId', 'syncStatus', 'drift'], ['desired', 'reported']],
  GatewayConfigShadowUpdateRequest: [['reason'], ['expectedDesiredRevision', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'driverConfigs', 'autonomyPolicy']],
  GatewayQuarantineRecord: [['id', 'gatewayId', 'topic', 'reasonCode', 'detail', 'payloadSha256', 'payloadBytes', 'payloadPreview', 'payloadEncoding', 'payloadTruncated', 'occurrenceCount', 'receivedAt', 'lastSeenAt']],
  GatewayQuarantineList: [['items']],
  GatewayCapturedPacket: [['sequence', 'capturedAt', 'direction', 'channel', 'topic', 'payloadBytes', 'payloadSha256', 'payloadPreview', 'payloadEncoding', 'payloadTruncated'], ['traceId']],
  GatewayPacketCaptureSnapshot: [['gatewayId', 'active', 'maxPackets', 'droppedCount', 'items'], ['startedAt', 'expiresAt', 'durationSeconds']],
  GatewayPacketCaptureStartRequest: [[], ['durationSeconds']],
  FieldError: [['field', 'message']],
  ProblemDetails: [['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable'], ['reason', 'fieldErrors']],
};
for (const [name, [required, optional = []]] of Object.entries(schemaRequirements)) {
  const properties = optional.length > 0 ? [...new Set([...required, ...optional])] : required;
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
  'asset.list',
  'asset.read',
  'device.list',
  'device.read',
  'site.write',
  'space.write',
  'asset.write',
  'device.write',
  'sensor.write',
  'point.write',
  'binding.write',
  'template.manage',
  'registry.import',
  'registry.retire',
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.history.read',
  'alarm.list',
  'alarm.read',
  'alarm.assign',
  'work-order.list',
  'work-order.read',
  'work-order.create',
  'work-order.assign',
  'work-order.lifecycle',
  'session.revoke',
  'audit.read',
  'iam.admin',
  'savings.manage',
  'schedule.request',
  'schedule.manage',
  'billing.manage',
]), 'Capability vocabulary is unsupported');
invariant(schemas.EffectiveAuthorization.properties.capabilitySetVersion.const === 14, 'EffectiveAuthorization capability set version must be 14');
invariant(schemas.EffectiveAuthorization.properties.policyRevision.minLength === 1 && schemas.EffectiveAuthorization.properties.policyRevision.maxLength === 128, 'EffectiveAuthorization policy revision bounds are unsupported');
invariant(schemas.EffectiveAuthorization.properties.capabilities.uniqueItems === true && schemas.EffectiveAuthorization.properties.capabilities.maxItems === schemas.Capability.enum.length, 'EffectiveAuthorization capabilities must be unique and bounded');
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
invariant(exactMembers(schemas.ProblemDetails.properties.reason?.enum, [
  'CODE_INVALID',
  'CODE_REPLAYED',
  'MFA_NOT_CONFIGURED',
  'MFA_LOCKED',
]), 'ProblemDetails.reason enum is unsupported');
invariant(schemas.ProblemDetails.properties.code.pattern === '^[A-Z][A-Z0-9_]+$', 'ProblemDetails.code pattern is unsupported');
invariant(schemas.ProblemDetails.properties.traceId.pattern === '^[a-f0-9]{32}$', 'ProblemDetails.traceId pattern is unsupported');
invariant(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'public Problem response must use application/problem+json');

const banner = `Generator: platform-contracts@${generatorVersion}; Contract SHA-256: ${digest}`;
const replacements = {
  ...Object.fromEntries(w4.flatMap(([k, , , , r]) => (r ? [[r, operations[k].path]] : []))),
  __CONTRACT_BANNER__: banner,
  __HEALTH_PATH__: operations.getHealth.path,
  __VERSION_PATH__: operations.getVersion.path,
  __PLATFORM_STATUS_PATH__: operations.getPlatformStatus.path,
  __LOGIN_PATH__: operations.beginLogin.path,
  __CALLBACK_PATH__: operations.completeLogin.path,
  __PRINCIPAL_PATH__: operations.getCurrentPrincipal.path,
  __SWITCH_TENANT_PATH__: operations.switchActiveTenant.path,
  __LOGOUT_PATH__: operations.logout.path,
  __REVOKE_PATH__: operations.revokeSession.path,
  __AUDIT_PATH__: operations.getSessionAuditEvent.path,
  __AUDIT_SEARCH_PATH__: operations.searchAudit.path,
  __ENERGY_SERIES_PATH__: operations.queryEnergySeries.path,
  __SITES_PATH__: operations.listSites.path,
  __SITE_PATH__: operations.getSite.path,
  __ASSETS_PATH__: operations.listAssets.path,
  __SITE_ASSET_PATH__: operations.listSiteAssets.path,
  __ASSET_PATH__: operations.getAsset.path,
  __DEVICES_PATH__: operations.listDevices.path,
  __SITE_DEVICES_PATH__: operations.listSiteDevices.path,
  __SITE_DEVICE_BINDINGS_PATH__: operations.listSiteDeviceBindings.path,
  __SITE_ASSET_MODEL_PATH__: operations.getSiteAssetModel.path,
  __SITE_TARIFF_PATH__: operations.getSiteTariff.path,
  __DEVICE_PATH__: operations.getDevice.path,
  __TEMPLATES_PATH__: operations.listTemplates.path,
  __TEMPLATE_REVISION_PATH__: operations.getTemplateRevision.path,
  __SITE_TEMPLATE_ASSIGNMENTS_PATH__: operations.listSiteTemplateAssignments.path,
  __SITE_SPACE_CHILDREN_PATH__: operations.listSiteSpaceChildren.path,
  __DEVICE_POINTS_PATH__: operations.listDevicePoints.path,
  __SITE_SPACES_PATH__: operations.createSiteSpace.path,
  __SITE_SPACE_PATH__: operations.updateSiteSpace.path,
  __SITE_ASSET_DETAIL_PATH__: operations.updateSiteAsset.path,
  __SITE_DEVICE_DETAIL_PATH__: operations.updateSiteDevice.path,
  __SITE_DEVICE_REPLACE_HARDWARE_PATH__: operations.replaceDeviceHardware.path,
  __SITE_SENSORS_PATH__: operations.createSiteSensor.path,
  __SITE_SENSOR_PATH__: operations.updateSiteSensor.path,
  __SITE_POINTS_PATH__: operations.createSitePoint.path,
  __SITE_POINT_PATH__: operations.updateSitePoint.path,
  __SITE_REBIND_PATH__: operations.rebindRegistryResource.path,
  __TEMPLATE_REVISIONS_PATH__: operations.releaseRegistryTemplateRevision.path,
  __TEMPLATE_ASSIGNMENTS_PATH__: operations.assignRegistryTemplateRevision.path,
  __SITE_IMPORT_DRY_RUN_PATH__: operations.planRegistryImport.path,
  __SITE_IMPORT_COMMIT_PATH__: operations.commitRegistryImport.path,
  __SITE_RETIRE_PATH__: operations.beginRegistryRetirement.path,
  __ALARMS_PATH__: operations.listAlarmsV212.path,
  __ALARM_PATH__: operations.getAlarmV212.path,
  __ALARM_ACK_PATH__: operations.ackAlarmV212.path,
  __ALARM_ASSIGN_PATH__: operations.assignAlarmV212.path,
  __ALARM_UNASSIGN_PATH__: operations.unassignAlarmV212.path,
  __ALARM_SUPPRESS_PATH__: operations.suppressAlarmV212.path,
  __ALARM_UNSUPPRESS_PATH__: operations.unsuppressAlarmV212.path,
  __ALARM_EVALUATIONS_PATH__: operations.listAlarmEvaluationsV212.path,
  __ALARM_RULES_PATH__: operations.listSiteAlarmRules.path,
  __ALARM_RULES_SIMULATE_PATH__: operations.simulateSiteAlarmRule.path,
  __COMMANDS_PATH__: operations.listCommandsV212.path,
  __COMMAND_PATH__: operations.getCommandV212.path,
  __COMMAND_APPROVE_PATH__: operations.approveCommandV212.path,
  __COMMAND_RECONCILE_PATH__: operations.reconcileCommandV212.path,
  __COMMAND_CANCEL_PATH__: operations.cancelCommandV212.path,
  __SITE_LOAD_FORECAST_PATH__: operations.getSiteLoadForecastV212.path,
  __SITE_FDD_FINDINGS_PATH__: operations.listSiteFddFindingsV212.path,
  __SITE_FDD_LOW_DELTA_T_PATH__: operations.evaluateSiteFddLowDeltaTV212.path,
  __SITE_FDD_LOW_DELTA_T_RULE_PATH__: operations.getSiteFddLowDeltaTRuleV212.path,
  __SITE_SAVINGS_MEASURES_PATH__: operations.listSiteSavingsMeasuresV212.path,
  __SITE_SAVINGS_MEASURE_PATH__: operations.getSiteSavingsMeasureV212.path,
  __SITE_SAVINGS_IMPLEMENT_PATH__: operations.implementSiteSavingsMeasureV212.path,
  __SITE_SAVINGS_REFIT_PATH__: operations.refitSiteSavingsMeasureV212.path,
  __SITE_SAVINGS_END_PATH__: operations.endSiteSavingsMeasureV212.path,
  __SITE_SAVINGS_SUMMARY_PATH__: operations.getSiteSavingsSummaryV212.path,
  __SITE_MONTHLY_REPORTS_PATH__: operations.listSiteMonthlyReportsV212.path,
  __SITE_MONTHLY_REPORT_PATH__: operations.getSiteMonthlyReportV212.path,
  __OPTIMIZATION_RUNS_PATH__: operations.createOptimizationRunV212.path,
  __LATEST_OPTIMIZATION_RECOMMENDATION_PATH__: operations.getLatestSiteOptimizationRecommendationV212.path,
  __LATEST_OPTIMIZATION_PREPARATION_PATH__: operations.getLatestSiteOptimizationPreparationV212.path,
  __LATEST_OPTIMIZATION_SHADOW_EVALUATION_PATH__: operations.getLatestSiteOptimizationShadowEvaluationV212.path,
  __LATEST_OPTIMIZATION_STAGING_RECOMMENDATIONS_PATH__: operations.getLatestSiteOptimizationStagingRecommendations.path,
  __SITE_OPERATING_SCHEDULE_PATH__: operations.getSiteOperatingSchedule.path,
  __SITE_OPERATING_EXCEPTIONS_PATH__: operations.listSiteOperatingExceptions.path,
  __SITE_OPERATING_EXCEPTION_DECIDE_PATH__: operations.decideSiteOperatingException.path,
  __SITE_ELECTRICITY_BUDGETS_PATH__: operations.listSiteMonthlyElectricityBudgets.path,
  __SITE_UTILITY_BILLS_PATH__: operations.listSiteUtilityBills.path,
  __SITE_UTILITY_BILL_PATH__: operations.updateSiteUtilityBill.path,
  __SITE_UTILITY_BILL_CONFIRM_PATH__: operations.confirmSiteUtilityBill.path,
  __SITE_ACCOUNTING_RECALCULATE_PATH__: operations.recalculateSiteAccounting.path,
  __SITE_ACCOUNTING_RECALC_JOBS_PATH__: operations.listSiteAccountingRecalcJobs.path,
  __SITE_ACCOUNTING_HOLIDAYS_PATH__: operations.listSiteCalendarHolidays.path,
  __SITE_ACCOUNTING_HOLIDAY_PATH__: operations.upsertSiteCalendarHoliday.path,
  __SITE_ACCOUNTING_METER_RATIOS_PATH__: operations.listSiteMeterRatioVersions.path,
  __SITE_ACCOUNTING_METER_RATIO_RETIRE_PATH__: operations.retireSiteMeterRatioVersion.path,
  __SITE_ACCOUNTING_COUNTER_AUDIT_PATH__: operations.getSiteCounterAudit.path,
  __SITE_DATA_GAPS_PATH__: operations.listSiteDataGaps.path,
  __SITE_CONTROL_STATE_PATH__: operations.getSiteControlState.path,
  __SITE_CONTROL_MODE_PATH__: operations.updateSiteControlMode.path,
  __SITE_CONTROL_SHUTDOWN_PATH__: operations.executeSiteControlShutdown.path,
  __ASSET_CONTROL_LOCK_PATH__: operations.updateAssetControlLock.path,
  __SITE_AUTO_CONTROL_LEASE_PATH__: operations.getSiteAutoControlLease.path,
  __SITE_AUTO_CONTROL_LEASE_REVOKE_PATH__: operations.revokeSiteAutoControlLease.path,
  __SITE_OPTIMIZATION_EXECUTION_MONITORS_PATH__: operations.listSiteOptimizationExecutionMonitors.path,
  __SITE_OPTIMIZATION_GOVERNANCE_SUMMARY_PATH__: operations.getSiteOptimizationGovernanceSummary.path,
  __SITE_OPTIMIZATION_CANARY_BINDINGS_PATH__: operations.listSiteOptimizationCanaryBindings.path,
  __SITE_OPTIMIZATION_CANARY_BINDING_PATH__: operations.upsertSiteOptimizationCanaryBinding.path,
  __SITE_OPTIMIZATION_NEGATIVE_SAMPLES_PATH__: operations.listSiteOptimizationNegativeSamples.path,
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
