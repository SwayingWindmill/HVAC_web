import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

type HttpMethod = 'get' | 'post' | 'put' | 'patch' | 'delete';
type OpenAPISchemaRef = { $ref?: string };
type OpenAPISchemaProperty = {
  type?: string;
  const?: unknown;
  enum?: unknown[];
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  uniqueItems?: boolean;
  maxItems?: number;
  items?: OpenAPISchemaRef;
  format?: string;
  'x-stable-codes'?: string[];
};
type OpenAPISchema = {
  type?: string;
  additionalProperties?: boolean;
  required?: string[];
  properties?: Record<string, OpenAPISchemaProperty>;
  enum?: unknown[];
  pattern?: string;
  minimum?: number;
};
type OpenAPIResponse = {
  content?: Record<string, { schema?: OpenAPISchemaRef }>;
  headers?: Record<string, { schema?: { format?: string } }>;
};
type OpenAPIOperation = {
  operationId?: string;
  'x-architecture-status'?: string;
  'x-shape-status'?: string;
  'x-shape-source'?: string;
  responses?: Record<string, OpenAPIResponse>;
  requestBody?: { content?: Record<string, { schema?: OpenAPISchemaRef }> };
};
type OpenAPISpec = {
  openapi?: string;
  paths?: Record<string, Partial<Record<HttpMethod, OpenAPIOperation>>>;
  components?: {
    schemas?: Record<string, OpenAPISchema>;
    responses?: Record<string, OpenAPIResponse>;
  };
};
type ToolingLock = {
  generator?: string;
  generatorVersion?: string;
  templates?: string[];
  outputs?: string[];
};
type OperationEntry = {
  path: string;
  method: HttpMethod;
  operation: OpenAPIOperation;
};

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

const normalizeLineEndings = (value: string): string => value.replace(/\r\n?/g, '\n');
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
const spec = JSON.parse(specText) as OpenAPISpec;
const toolingLock = JSON.parse(toolingLockText) as ToolingLock;
const digest = createHash('sha256').update(specText).digest('hex');

function invariant(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Invalid platform Gateway OpenAPI contract: ${message}`);
}

function exactMembers(actual: unknown, expected: readonly string[]): boolean {
  return Array.isArray(actual) && actual.length === expected.length && expected.every((member) => actual.includes(member));
}

function exactKeys(value: unknown, expected: readonly string[]): boolean {
  return value !== null && typeof value === 'object' && exactMembers(Object.keys(value), expected);
}

function operation(operationId: string): OperationEntry | null {
  for (const [path, pathItem] of Object.entries(spec.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete'] as const) {
      const candidate = pathItem[method];
      if (candidate?.operationId === operationId) return { path, method, operation: candidate };
    }
  }
  return null;
}

function schemaRef(operationValue: OpenAPIOperation, status: string, contentType = 'application/json'): string | undefined {
  return operationValue.responses?.[status]?.content?.[contentType]?.schema?.$ref;
}

invariant(toolingLock.generatorVersion === generatorVersion, 'tooling lock generator version does not match this generator');
invariant(toolingLock.generator === 'scripts/generate-platform-contracts.ts', 'tooling lock generator path is invalid');
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
const w4: ReadonlyArray<readonly [string, HttpMethod, string, string, string?]> = [
  ['listAlarmRuleDeadLetters', 'get', 'alarm-rules/dead-letters', 'AlarmRuleDeadLetterListEnvelope', '__ALARM_RULES_DEAD_LETTERS_PATH__'],
  ['replayAlarmRuleDeadLetter', 'post', 'alarm-rules/dead-letters/{outboxId}/replay', 'AlarmRuleDeadLetterReplayEnvelope', '__ALARM_RULES_DEAD_LETTER_REPLAY_PATH__'],
  ['putDevicePriorityLock', 'put', `${site}/devices/{deviceId}/priority-lock`, 'DevicePriorityLockState', '__DEVICE_PRIORITY_LOCK_PATH__'],
  ['deleteDevicePriorityLock', 'delete', `${site}/devices/{deviceId}/priority-lock`, 'DevicePriorityLockReleaseResponse'],
  ['listControlAlarmDeadLetters', 'get', `${site}/control-alarms/dead-letters`, 'ControlAlarmDeadLetterListEnvelope', '__CONTROL_ALARM_DEAD_LETTERS_PATH__'],
  ['replayControlAlarmDeadLetter', 'post', `${site}/control-alarms/dead-letters/{outboxId}/replay`, 'ControlAlarmDeadLetterReplayResponse', '__CONTROL_ALARM_DEAD_LETTER_REPLAY_PATH__'],
  ['listSiteCommandBatches', 'get', `${site}/command-batches`, 'CommandBatchListView'],
  ['getSiteCommandBatch', 'get', `${site}/command-batches/{batchId}`, 'CommandBatchView'],
  ['cancelSiteCommandBatch', 'post', `${site}/command-batches/{batchId}/cancel`, 'CommandBatchCancelResponse'],
  ['getSiteAlarmRouting', 'get', `${site}/alarm-routing`, 'SiteAlarmRoutingEnvelope', '__SITE_ALARM_ROUTING_PATH__'],
  ['reloadSiteAlarmRouting', 'post', `${site}/alarm-routing/reload`, 'SiteAlarmRoutingReloadEnvelope', '__SITE_ALARM_ROUTING_RELOAD_PATH__'],
  ['diffSiteAlarmRules', 'post', 'alarm-rules/diff', 'AlarmRuleDiffEnvelope', '__ALARM_RULES_DIFF_PATH__'],
  ['compileSiteAlarmRulesEdge', 'post', 'alarm-rules/compile-edge', 'EdgeRuleCompileEnvelope', '__ALARM_RULES_COMPILE_EDGE_PATH__'],
  ['publishSiteAlarmRules', 'put', 'alarm-rules', 'AlarmRulePublishEnvelope'],
  ['listSiteAlarmRuleRevisions', 'get', 'alarm-rules/revisions', 'AlarmRuleRevisionListEnvelope', '__ALARM_RULES_REVISIONS_PATH__'],
  ['rollbackSiteAlarmRules', 'post', 'alarm-rules/revisions/{revision}/rollback', 'AlarmRulePublishEnvelope', '__ALARM_RULES_ROLLBACK_PATH__'],
];
const expectedOperations: Record<string, readonly [HttpMethod, string]> = {
  ...Object.fromEntries(w4.map(([k, m, p]) => [k, [m, p] as const])),
  getGatewayCredential: ['get', `${gw}/credential`],
  listGatewayCredentials: ['get', `${gw}/credentials`],
  revokeGatewayCertificate: ['post', `${gw}/credentials/{credentialId}/revoke`],
  generateGatewayEnrollmentCode: ['post', `${gw}/enrollment-code`],
  revokeGatewayCredential: ['post', `${gw}/revoke`],
  rotateGatewayEnrollment: ['post', `${gw}/rotate`],
  issueGatewayFactoryReset: ['post', `${gw}/factory-reset`],
  listGatewayFactoryResets: ['get', `${gw}/factory-resets`],
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
  listGatewayWaveformRules: ['get', `${gw}/waveform-rules`],
  createGatewayWaveformRule: ['post', `${gw}/waveform-rules`],
  updateGatewayWaveformRule: ['put', `${gw}/waveform-rules/{ruleId}`],
  deleteGatewayWaveformRule: ['delete', `${gw}/waveform-rules/{ruleId}`],
  listGatewayWaveformCaptures: ['get', `${gw}/waveform-captures`],
  getGatewayWaveformCapture: ['get', `${gw}/waveform-captures/{captureId}`],
  triggerGatewayWaveformCapture: ['post', `${gw}/waveform-captures:trigger`],
  pullGatewayDiagnosticsLogs: ['post', `${gw}/diagnostics/logs:pull`],
  getGatewayHostMetrics: ['get', `${gw}/diagnostics/host-metrics`],
  listOTAPackages: ['get', 'ota/packages'],
  uploadOTAPackage: ['post', 'ota/packages'],
  listOTACampaigns: ['get', 'ota/campaigns'],
  createOTACampaign: ['post', 'ota/campaigns'],
  getOTACampaign: ['get', 'ota/campaigns/{campaignId}'],
  pauseOTACampaign: ['post', 'ota/campaigns/{campaignId}/pause'],
  resumeOTACampaign: ['post', 'ota/campaigns/{campaignId}/resume'],
  rollbackOTACampaign: ['post', 'ota/campaigns/{campaignId}/rollback'],
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
  transferSiteDevice: ['post', `${site}/devices/{deviceId}/transfer`],
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
  getSiteEdgeModelWorkspace: ['get', `${site}/edge-models`],
  createSiteEdgeModelArtifact: ['post', `${site}/edge-models/artifacts`],
  approveSiteEdgeModelArtifact: ['post', `${site}/edge-models/artifacts/{artifactId}/approve`],
  deploySiteEdgeModelArtifact: ['post', `${site}/edge-models/deployments`],
  rollbackSiteEdgeModelDeployment: ['post', `${site}/edge-models/deployments/{deploymentId}/rollback`],
  recordSiteEdgeModelEvaluation: ['post', `${site}/edge-models/evaluations`],
  createSiteCommandBatch: ['post', `${site}/command-batches`],
};
const operations: Record<string, OperationEntry> = {};
for (const [operationId, [method, subPath]] of Object.entries(expectedOperations)) {
  const value = operation(operationId);
  invariant(value?.method === method && value.path === `/api/v1/${subPath}`, `${operationId} method/path is unsupported by generator version 7.1`);
  operations[operationId] = value;
}

const expectedSuccessSchemas: Record<string, string> = {
  ...Object.fromEntries(w4.map(([k, , , s]) => [k, s])),
  getGatewayCredential: 'GatewayCredentialStatus',
  listGatewayCredentials: 'GatewayCredentialList',
  revokeGatewayCertificate: 'GatewayCredentialRecord',
  generateGatewayEnrollmentCode: 'GatewayEnrollmentCode',
  revokeGatewayCredential: 'GatewayCredentialStatus',
  rotateGatewayEnrollment: 'GatewayEnrollmentCode',
  issueGatewayFactoryReset: 'GatewayFactoryResetRecord',
  listGatewayFactoryResets: 'GatewayFactoryResetList',
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
  listGatewayWaveformRules: 'GatewayWaveformRuleList',
  updateGatewayWaveformRule: 'GatewayWaveformRule',
  deleteGatewayWaveformRule: 'GatewayWaveformRuleDeleteResult',
  listGatewayWaveformCaptures: 'GatewayWaveformCaptureList',
  getGatewayWaveformCapture: 'GatewayWaveformCapture',
  pullGatewayDiagnosticsLogs: 'GatewayLogBundleResult',
  getGatewayHostMetrics: 'GatewayHostMetricsSummary',
  listOTAPackages: 'OTAPackageList',
  listOTACampaigns: 'OTACampaignList',
  getOTACampaign: 'OTACampaignDetail',
  pauseOTACampaign: 'OTACampaignDetail',
  resumeOTACampaign: 'OTACampaignDetail',
  rollbackOTACampaign: 'OTACampaignDetail',
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
  getSiteEdgeModelWorkspace: 'EdgeModelWorkspace',
  approveSiteEdgeModelArtifact: 'EdgeModelApprovalView',
  deploySiteEdgeModelArtifact: 'EdgeModelDeploymentResult',
  rollbackSiteEdgeModelDeployment: 'EdgeModelRollbackResult',
};
for (const [operationId, schemaName] of Object.entries(expectedSuccessSchemas)) {
  invariant(schemaRef(operations[operationId].operation, '200') === `#/components/schemas/${schemaName}`, `${operationId} success schema is unsupported`);
}
invariant(schemaRef(operations.recalculateSiteAccounting.operation, '202') === '#/components/schemas/AccountingRecalcJob', 'recalculateSiteAccounting accepted schema is unsupported');
invariant(schemaRef(operations.createSiteCommandBatch.operation, '202') === '#/components/schemas/CommandBatchView', 'createSiteCommandBatch accepted schema is unsupported');
invariant(schemaRef(operations.createSiteEdgeModelArtifact.operation, '201') === '#/components/schemas/EdgeModelArtifactView', 'createSiteEdgeModelArtifact created schema is unsupported');
invariant(schemaRef(operations.recordSiteEdgeModelEvaluation.operation, '201') === '#/components/schemas/EdgeModelEvaluationResult', 'recordSiteEdgeModelEvaluation created schema is unsupported');
invariant(schemaRef(operations.createSiteMeterRatioVersion.operation, '201') === '#/components/schemas/MeterRatioVersion', 'createSiteMeterRatioVersion created schema is unsupported');
invariant(schemaRef(operations.replaceDeviceHardware.operation, '201') === '#/components/schemas/ReplaceDeviceHardwareResult', 'replaceDeviceHardware success schema is unsupported');
invariant(schemaRef(operations.transferSiteDevice.operation, '201') === '#/components/schemas/TransferDeviceResult', 'transferSiteDevice success schema is unsupported');
invariant(schemaRef(operations.uploadOTAPackage.operation, '201') === '#/components/schemas/OTAPackage', 'uploadOTAPackage created schema is unsupported');
invariant(schemaRef(operations.createOTACampaign.operation, '201') === '#/components/schemas/OTACampaignDetail', 'createOTACampaign created schema is unsupported');
invariant(schemaRef(operations.createGatewayWaveformRule.operation, '201') === '#/components/schemas/GatewayWaveformRule', 'createGatewayWaveformRule created schema is unsupported');
invariant(schemaRef(operations.triggerGatewayWaveformCapture.operation, '201') === '#/components/schemas/GatewayWaveformCapture', 'triggerGatewayWaveformCapture created schema is unsupported');
invariant(schemaRef(operations.getTemplateRevision.operation, '200') === './s05-registry-writer.openapi.json#/components/schemas/TemplateRevision', 'getTemplateRevision success schema is unsupported');
invariant(operations.logout.operation.responses?.['204'], 'logout must return 204');
invariant(operations.logout.operation.responses?.['204']?.headers?.Location?.schema?.format === 'uri', 'logout must publish the provider end-session Location header');
for (const operationId of ['listAlarmsV212', 'getAlarmV212', 'ackAlarmV212']) {
  invariant(operations[operationId].operation['x-architecture-status'] === 'ACTIVE' && operations[operationId].operation['x-shape-status'] === 'READY', `${operationId} must be active with a synchronized Alarm shape`);
  invariant(operations[operationId].operation['x-shape-source'] === 'contracts/http/s4-alarm-public.openapi.json', `${operationId} must use the S4 Alarm subordinate contract`);
}
invariant(schemaRef(operations.listAlarmsV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/CursorAlarmListResponse', 'Alarm list success schema is unsupported');
invariant(schemaRef(operations.getAlarmV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/AlarmEnvelope', 'Alarm detail success schema is unsupported');
invariant(schemaRef(operations.ackAlarmV212.operation, '200') === './s4-alarm-public.openapi.json#/components/schemas/AlarmEnvelope', 'Alarm ACK success schema is unsupported');
invariant(operations.ackAlarmV212.operation.requestBody?.content?.['application/json']?.schema?.$ref === './s4-alarm-public.openapi.json#/components/schemas/AckAlarmRequest', 'Alarm ACK request schema is unsupported');

const schemas = spec.components?.schemas ?? {};
const schemaRequirements: Record<string, readonly [readonly string[], (readonly string[])?]> = {
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
  Device: [['id', 'tenantId', 'siteId', 'code', 'displayName', 'deviceType', 'status', 'revision', 'createdAt', 'updatedAt'], ['externalId', 'protocolAddress', 'tags', 'connectivityMode']],
  ReplaceDeviceHardwareResult: [[
    'replacementId', 'tenantId', 'siteId', 'deviceId', 'revision', 'previousExternalId', 'newExternalId',
    'previousGatewayDeviceId', 'newGatewayDeviceId', 'previousProtocolAddress', 'newProtocolAddress',
    'reason', 'replacedBy', 'replacedAt', 'device', 'replayed',
  ]],
  TransferDeviceResult: [
    [
      'transferId', 'transferScope', 'sourceTenantId', 'sourceSiteId', 'sourceDeviceId', 'sourceDeviceRevision',
      'targetTenantId', 'targetSiteId', 'targetDeviceId', 'wipeRequired', 'reason', 'transferredBy', 'transferredAt',
      'sourceDevice', 'targetDevice', 'replayed',
    ],
    ['hardwareExternalId', 'oldCredentialsRevoked', 'factoryResetId', 'factoryResetStatus'],
  ],
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
  GatewayFactoryResetRequest: [['reason'], ['wipeCredentials', 'wipeConfigShadow', 'wipeOfflineBuffer', 'revokeCredentials']],
  GatewayFactoryResetRecord: [
    ['id', 'tenantId', 'gatewayId', 'wipeCredentials', 'wipeConfigShadow', 'wipeOfflineBuffer', 'reason', 'status', 'issuedBy', 'wipedArtifacts', 'createdAt', 'updatedAt'],
    ['transferId', 'dispatchedAt', 'acknowledgedAt', 'errorMessage'],
  ],
  GatewayFactoryResetList: [['items']],
  PointCopyTarget: [['deviceKey', 'pointCode']],
  GatewayPointMapping: [['sourceChannel', 'primaryDeviceKey', 'primaryPointCode'], ['copyTargets']],
  GatewaySamplingPolicy: [['deviceKey', 'sampleIntervalMs', 'publishIntervalMs'], ['pointCode']],
  ConcentratorUnitShadow: [[], ['params', 'eids']],
  ConfigShadowDriftDetail: [['section', 'target', 'reason', 'detail']],
  GatewayModbusRegisterMapping: [['pointCode', 'functionCode', 'address', 'registerCount', 'rawType', 'byteOrder', 'wordOrder', 'scale', 'offset', 'pollPriority'], ['semanticChannel', 'writeFunctionCode', 'bitIndex', 'enumMap']],
  GatewayBACnetObjectMapping: [['pointCode', 'objectType', 'objectInstance', 'pollPriority'], ['semanticChannel', 'safetyBoundary', 'propertyId', 'dataType', 'writable', 'covSubscribe', 'scale', 'offset', 'unit', 'enumMap']],
  GatewayBACnetDriverSpec: [['deviceInstance', 'objects'], ['networkNumber', 'covLifetimeSeconds', 'writePriority']],
  GatewayOPCUANodeMapping: [['pointCode', 'nodeId', 'pollPriority'], ['semanticChannel', 'safetyBoundary', 'namespaceIndex', 'dataType', 'writable', 'subscribe', 'scale', 'offset', 'unit']],
  GatewayOPCUADriverSpec: [['securityPolicy', 'securityMode', 'nodes'], ['authMode', 'username', 'credentialRef', 'subscriptionIntervalMs']],
  GatewayModbusDriverConfig: [['deviceKey', 'driverType', 'endpoint', 'timeoutMs', 'retries'], ['unitId', 'capabilityProfile', 'registers', 'bacnet', 'opcua']],
  GatewayAutonomyPolicy: [['enabled', 'offlineGraceSeconds', 'defaultChilledWaterSupplySetpointC', 'defaultPumpFrequencyHz', 'minPumpFrequencyHz', 'maxPumpFrequencyHz', 'minRunSeconds', 'minStopSeconds', 'snapshotIntervalSeconds']],
  GatewayDesiredConfig: [['desiredRevision', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'updatedBy', 'reason', 'createdAt', 'updatedAt'], ['driverConfigs', 'autonomyPolicy', 'edgeRuleBundle', 'edgeModels']],
  GatewayReportedConfig: [['reportedRevision', 'appliedStatus', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'driftDetails', 'reportedAt', 'updatedAt'], ['driverConfigs', 'autonomyPolicy', 'edgeRuleBundle', 'edgeModels', 'edgeModelStatuses', 'errorMessage']],
  GatewayConfigShadow: [['tenantId', 'gatewayId', 'syncStatus', 'drift'], ['desired', 'reported']],
  GatewayConfigShadowUpdateRequest: [['reason'], ['expectedDesiredRevision', 'pointMappings', 'samplingPolicies', 'concentratorConfigs', 'driverConfigs', 'autonomyPolicy', 'edgeRuleBundle', 'edgeModels']],
  EdgeModelHyperparams: [[], ['copCoefficients', 'chillerStartPowerKw', 'chillerStopPowerKw', 'supplyTempSetpointMinC', 'supplyTempSetpointMaxC', 'pumpFrequencyMinHz', 'pumpFrequencyMaxHz', 'towerFanSpeedMinPct', 'towerFanSpeedMaxPct', 'pidKp', 'pidKi', 'pidKd', 'stageUpLoadPct', 'stageDownLoadPct', 'fddDeltaTMinK', 'fddAnomalyZScore', 'targetPointCodes']],
  EdgeModelBundle: [['bundleId', 'domain', 'modelKey', 'version', 'rolloutMode', 'artifactSha256', 'maxShadowDeviation', 'hyperparams'], ['keyId', 'signature', 'updatedAt']],
  EdgeModelRuntimeStatus: [['modelKey', 'domain', 'shadowSampleCount', 'shadowMeanAbsDeviation', 'shadowMaxAbsDeviation', 'rollbackCount'], ['activeBundleId', 'activeVersion', 'shadowBundleId', 'shadowVersion', 'lastRollbackReason', 'lastEvaluatedAt']],
  EdgeModelArtifactView: [['id', 'versionId', 'domain', 'modelKey', 'version', 'name', 'objectUri', 'checksumSha256', 'maxShadowDeviation', 'hyperparams', 'signed', 'approvalStatus', 'createdAt'], ['description', 'gatewayId', 'latestApprovalReason', 'latestEvaluation']],
  EdgeModelApprovalView: [['id', 'versionId', 'domain', 'modelKey', 'version', 'decision', 'actorType', 'actorId', 'reason', 'policyRevision', 'decidedAt'], ['artifactId', 'evaluationId']],
  EdgeModelDeploymentView: [['id', 'versionId', 'domain', 'modelKey', 'version', 'approvalId', 'environment', 'status', 'servingRef', 'revision', 'effectiveFrom', 'hyperparams'], ['artifactId', 'gatewayId', 'effectiveTo']],
  EdgeModelEvaluationMetrics: [['modelKey', 'version', 'sampleCount', 'meanAbsDeviation', 'maxAbsDeviation', 'thresholdValue'], ['baselineCop', 'candidateCop', 'energySavingPct', 'notes']],
  EdgeModelEvaluationView: [['id', 'versionId', 'domain', 'modelKey', 'version', 'evaluationType', 'outcome', 'evaluator', 'metrics', 'evaluatedAt'], ['artifactId']],
  EdgeModelDriftEvidence: [['modelKey', 'version', 'sampleCount', 'meanAbsDeviation'], ['evaluationId', 'notes']],
  EdgeModelDriftView: [['id', 'deploymentId', 'metricCode', 'observedValue', 'threshold', 'driftStatus', 'evidence', 'observedAt']],
  EdgeModelRollbackView: [['id', 'deploymentId', 'fromVersionId', 'toVersionId', 'reason', 'actorType', 'actorId', 'status', 'requestedAt'], ['fromVersion', 'toVersion', 'appliedAt']],
  EdgeModelWorkspace: [['tenantId', 'siteId', 'generatedAt', 'artifacts', 'approvals', 'deployments', 'evaluations', 'driftObservations', 'rollbacks', 'desiredBundles']],
  CreateEdgeModelArtifactRequest: [['domain', 'modelKey', 'version', 'hyperparams'], ['name', 'description', 'maxShadowDeviation', 'artifactSha256', 'gatewayId']],
  ApproveEdgeModelRequest: [['decision', 'reason'], ['evaluationId', 'policyRevision']],
  DeployEdgeModelRequest: [['artifactId', 'environment'], ['gatewayId', 'reason']],
  EdgeModelDeploymentResult: [['deployment', 'desiredBundles'], ['gatewayId', 'shadowSyncStatus', 'shadowDesiredRevision', 'shadowSyncError']],
  RollbackEdgeModelRequest: [['reason'], ['targetArtifactId', 'gatewayId']],
  EdgeModelRollbackResult: [['rollback', 'desiredBundles'], ['restoredDeployment', 'gatewayId', 'shadowSyncStatus', 'shadowDesiredRevision', 'shadowSyncError']],
  RecordEdgeModelEvaluationRequest: [['artifactId', 'evaluationType', 'sampleCount', 'meanAbsDeviation', 'maxAbsDeviation'], ['deploymentId', 'baselineCop', 'candidateCop', 'energySavingPct', 'outcome', 'notes']],
  EdgeModelEvaluationResult: [['evaluation', 'breached'], ['driftObservation']],
  AlarmRuleGraphNode: [['ruleId', 'title', 'severity', 'hopDepth', 'dependsOnRuleIds', 'actionCount'], ['ruleKind', 'executionTarget', 'change']],
  EdgeRuleCompileResult: [['siteId', 'bundleRevision', 'edgeBundleDigest', 'compiledAt', 'edgeRuleCount', 'cloudOnlyCount', 'eligibility', 'bundle'], ['bundleDigest']],
  GatewayQuarantineRecord: [['id', 'gatewayId', 'topic', 'reasonCode', 'detail', 'payloadSha256', 'payloadBytes', 'payloadPreview', 'payloadEncoding', 'payloadTruncated', 'occurrenceCount', 'receivedAt', 'lastSeenAt']],
  GatewayQuarantineList: [['items']],
  GatewayCapturedPacket: [['sequence', 'capturedAt', 'direction', 'channel', 'topic', 'payloadBytes', 'payloadSha256', 'payloadPreview', 'payloadEncoding', 'payloadTruncated'], ['traceId']],
  GatewayPacketCaptureSnapshot: [['gatewayId', 'active', 'maxPackets', 'droppedCount', 'items'], ['startedAt', 'expiresAt', 'durationSeconds']],
  GatewayPacketCaptureStartRequest: [[], ['durationSeconds']],
  GatewayWaveformRule: [['id', 'tenantId', 'siteId', 'gatewayId', 'ruleName', 'enabled', 'deviceCode', 'pointCode', 'triggerType', 'thresholdValue', 'deltaWindowSeconds', 'preSeconds', 'postSeconds', 'sampleRateHz', 'pointCodes', 'cooldownSeconds', 'updatedBy', 'createdAt', 'updatedAt']],
  GatewayWaveformRuleList: [['gatewayId', 'items']],
  GatewayWaveformRuleUpsertRequest: [['ruleName', 'enabled', 'deviceCode', 'pointCode', 'triggerType', 'thresholdValue'], ['deltaWindowSeconds', 'preSeconds', 'postSeconds', 'sampleRateHz', 'pointCodes', 'cooldownSeconds']],
  GatewayWaveformRuleDeleteResult: [['deleted']],
  WaveformSampleRow: [['sampledAtMs', 'values'], ['flags']],
  GatewayWaveformCapture: [['captureId', 'tenantId', 'siteId', 'gatewayId', 'triggerSource', 'triggerReason', 'status', 'triggeredAt', 'windowStartAt', 'windowEndAt', 'sampleRateHz', 'sampleCount', 'samples', 'requestedBy', 'createdAt'], ['ruleId', 'errorMessage', 'completedAt']],
  GatewayWaveformCaptureList: [['gatewayId', 'items']],
  TriggerWaveformCaptureRequest: [[], ['ruleId', 'deviceCode', 'pointCode', 'pointCodes', 'preSeconds', 'postSeconds', 'sampleRateHz', 'triggerReason']],
  PullGatewayLogsRequest: [[], ['maxBytes', 'sinceMinutes']],
  GatewayLogBundleResult: [['requestId', 'gatewayId', 'status', 'content', 'returnedBytes', 'redactedCount', 'truncated', 'collectedAt'], ['errorMessage']],
  GatewayHostMetricsRecord: [['id', 'gatewayId', 'collectedAt', 'cpuPercent', 'memoryUsedPercent', 'diskUsedPercent', 'uptimeSeconds', 'rs485TxFrames', 'rs485RxErrors', 'rs485ErrorRatePpm', 'mqttRttMs', 'mqttTlsVersion', 'mqttReconnectCount', 'activeRingPoints', 'ringBufferBytes']],
  GatewayHostMetricsSummary: [['gatewayId', 'history'], ['latest']],
  OTAPackage: [['id', 'tenantId', 'component', 'version', 'targetOs', 'targetArch', 'hardwareModels', 'fileName', 'fileSizeBytes', 'sha256', 'signatureAlg', 'signingKeyId', 'signatureB64', 'releaseNotes', 'status', 'uploadedBy', 'createdAt', 'updatedAt'], ['minCompatibleVersion']],
  OTAPackageList: [['items']],
  OTADeviceTask: [['id', 'tenantId', 'campaignId', 'packageId', 'gatewayId', 'siteId', 'gatewayCode', 'gatewayName', 'gatewayOnline', 'wave', 'desiredAction', 'stage', 'progressPct', 'currentVersion', 'targetVersion', 'createdAt', 'updatedAt'], ['errorCode', 'errorMessage', 'dispatchedAt', 'lastReportedAt', 'finishedAt']],
  OTACampaignSummary: [['id', 'tenantId', 'name', 'packageId', 'packageVersion', 'component', 'targetOs', 'targetArch', 'targetSiteIds', 'targetTags', 'canaryPercent', 'failureRateThresholdPct', 'healthCheckTimeoutSec', 'autoPromoteRollout', 'status', 'reason', 'totalCount', 'pendingCount', 'inProgressCount', 'succeededCount', 'failedCount', 'rolledBackCount', 'cancelledCount', 'failureRatePct', 'createdBy', 'updatedBy', 'createdAt', 'updatedAt'], ['pauseReason', 'completedAt']],
  OTACampaignList: [['items']],
  OTACampaignDetail: [['id', 'tenantId', 'name', 'packageId', 'packageVersion', 'component', 'targetOs', 'targetArch', 'targetSiteIds', 'targetTags', 'canaryPercent', 'failureRateThresholdPct', 'healthCheckTimeoutSec', 'autoPromoteRollout', 'status', 'reason', 'totalCount', 'pendingCount', 'inProgressCount', 'succeededCount', 'failedCount', 'rolledBackCount', 'cancelledCount', 'failureRatePct', 'createdBy', 'updatedBy', 'createdAt', 'updatedAt', 'package', 'tasks'], ['pauseReason', 'completedAt']],
  OTACampaignCreateRequest: [['name', 'packageId', 'canaryPercent', 'failureRateThresholdPct', 'reason'], ['targetSiteIds', 'targetTags', 'healthCheckTimeoutSec', 'autoPromoteRollout']],
  OTACampaignActionRequest: [[], ['reason']],
  FieldError: [['field', 'message']],
  ProblemDetails: [['type', 'title', 'status', 'detail', 'instance', 'code', 'traceId', 'retryable'], ['reason', 'fieldErrors']],
  CommandBatchTargetInput: [['deviceId', 'pointId'], ['parameters']],
  CommandBatchCreateRequest: [['capability', 'targets'], ['parameters', 'deliveryMode', 'ttlSeconds', 'reason', 'verificationCode']],
  CommandBatchCancelRequest: [[], ['reason']],
  CommandBatchItemView: [['itemIndex', 'deviceId', 'pointId', 'admitted', 'status'], ['commandId', 'rejectionCode', 'rejectionReason', 'expiresAt', 'nextDispatchAfter']],
  CommandBatchView: [['id', 'tenantId', 'siteId', 'idempotencyKey', 'capability', 'commandSource', 'deliveryMode', 'targetCount', 'acceptedCount', 'rejectedCount', 'status', 'initiatedByPrincipalId', 'traceId', 'reason', 'createdAt', 'updatedAt', 'replayed', 'items'], ['ttlSeconds', 'expiresAt']],
  CommandBatchListView: [['items']],
  CommandBatchCancelResponse: [['batch', 'cancelledCommandIds']],
};
for (const [name, [required, optional = []]] of Object.entries(schemaRequirements)) {
  const properties = optional.length > 0 ? [...new Set([...required, ...optional])] : required;
  const schema = schemas[name];
  invariant(schema?.type === 'object' && schema.additionalProperties === false, `${name} must be a closed object schema`);
  invariant(exactMembers(schema.required, required), `${name} required fields are unsupported`);
  invariant(exactKeys(schema.properties, properties), `${name} properties are unsupported`);
}

invariant(schemas.BuildInfo.properties?.service?.const === 'platform-gateway', 'BuildInfo.service must be platform-gateway');
invariant(schemas.HealthResponse.properties?.status?.const === 'ok', 'HealthResponse.status must be ok');
invariant(schemas.PlatformStatusResponse.properties?.service?.const === 'platform-status', 'PlatformStatusResponse.service must be platform-status');
invariant(exactMembers(schemas.PlatformStatusResponse.properties?.implementation?.enum, ['go']), 'PlatformStatusResponse implementation must be Go-only');
invariant(exactMembers(schemas.PlatformStatusResponse.properties?.compatibilityMode?.enum, ['native']), 'PlatformStatusResponse compatibility mode must be native-only');
invariant(schemas.ServicePrincipal.properties?.service?.const === 'platform-gateway', 'ServicePrincipal.service must be platform-gateway');
invariant(schemas.PrincipalContext.properties?.audience?.const === 'iam-service', 'PrincipalContext.audience must be iam-service');
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
invariant(schemas.EffectiveAuthorization.properties?.capabilitySetVersion?.const === 14, 'EffectiveAuthorization capability set version must be 14');
invariant(schemas.EffectiveAuthorization.properties?.policyRevision?.minLength === 1 && schemas.EffectiveAuthorization.properties?.policyRevision?.maxLength === 128, 'EffectiveAuthorization policy revision bounds are unsupported');
invariant(schemas.EffectiveAuthorization.properties?.capabilities?.uniqueItems === true && schemas.EffectiveAuthorization.properties?.capabilities?.maxItems === schemas.Capability.enum?.length, 'EffectiveAuthorization capabilities must be unique and bounded');
invariant(schemas.EffectiveAuthorization.properties?.capabilities?.items?.$ref === '#/components/schemas/Capability', 'EffectiveAuthorization capabilities must use the public Capability vocabulary');
invariant(schemas.AuditRecord.properties?.schemaVersion?.const === 1, 'AuditRecord.schemaVersion must be 1');
invariant(schemas.AuditRecord.properties?.aggregateType?.const === 'bff-session', 'AuditRecord.aggregateType must be bff-session');
invariant(schemas.AuditRecord.properties?.executingService?.const === 'platform-gateway', 'AuditRecord.executingService must be platform-gateway');
invariant(schemas.UUIDv7?.pattern === '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$', 'UUIDv7 pattern is unsupported');
invariant(schemas.Revision?.minimum === 1, 'Revision minimum is unsupported');
invariant(schemas.Instant?.pattern === '^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}\\.\\d{3}Z$', 'Instant format must be RFC3339 UTC milliseconds');
invariant(schemas.OpaqueCursor?.pattern === '^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$', 'OpaqueCursor format is unsupported');
invariant(exactMembers(schemas.AssetRelationship.properties?.fromType?.enum, ['ASSET', 'DEVICE', 'SENSOR', 'POINT']), 'AssetRelationship.fromType vocabulary is stale');
invariant(exactMembers(schemas.AssetRelationship.properties?.toType?.enum, ['SITE', 'SPACE', 'ASSET', 'DEVICE', 'SENSOR', 'POINT']), 'AssetRelationship.toType vocabulary is stale');
invariant(exactMembers(schemas.ProblemDetails.properties?.code?.['x-stable-codes'], [
  'RESOURCE_NOT_FOUND',
  'CURSOR_INVALID',
  'REGISTRY_UNAVAILABLE',
  'REGISTRY_TIMEOUT',
  'MAPPING_INVALID',
  'MAPPING_QUARANTINED',
]), 'Registry Problem Details codes are unsupported');
invariant(exactMembers(schemas.ProblemDetails.properties?.reason?.enum, [
  'CODE_INVALID',
  'CODE_REPLAYED',
  'MFA_NOT_CONFIGURED',
  'MFA_LOCKED',
]), 'ProblemDetails.reason enum is unsupported');
invariant(schemas.ProblemDetails.properties?.code?.pattern === '^[A-Z][A-Z0-9_]+$', 'ProblemDetails.code pattern is unsupported');
invariant(schemas.ProblemDetails.properties?.traceId?.pattern === '^[a-f0-9]{32}$', 'ProblemDetails.traceId pattern is unsupported');
invariant(spec.components?.responses?.Problem?.content?.['application/problem+json']?.schema?.$ref === '#/components/schemas/ProblemDetails', 'public Problem response must use application/problem+json');

const banner = `Generator: platform-contracts@${generatorVersion}; Contract SHA-256: ${digest}`;
const pathOps: Record<string, string> = {
  __ALARMS_PATH__: 'listAlarmsV212',
  __ALARM_PATH__: 'getAlarmV212',
  __ALARM_ACK_PATH__: 'ackAlarmV212',
  __ALARM_ASSIGN_PATH__: 'assignAlarmV212',
  __ALARM_UNASSIGN_PATH__: 'unassignAlarmV212',
  __ALARM_SUPPRESS_PATH__: 'suppressAlarmV212',
  __ALARM_UNSUPPRESS_PATH__: 'unsuppressAlarmV212',
  __ALARM_EVALUATIONS_PATH__: 'listAlarmEvaluationsV212',
  __ALARM_RULES_PATH__: 'listSiteAlarmRules',
  __ALARM_RULES_SIMULATE_PATH__: 'simulateSiteAlarmRule',
  __COMMANDS_PATH__: 'listCommandsV212',
  __COMMAND_PATH__: 'getCommandV212',
  __COMMAND_APPROVE_PATH__: 'approveCommandV212',
  __COMMAND_RECONCILE_PATH__: 'reconcileCommandV212',
  __COMMAND_CANCEL_PATH__: 'cancelCommandV212',
  __SITE_LOAD_FORECAST_PATH__: 'getSiteLoadForecastV212',
  __SITE_FDD_FINDINGS_PATH__: 'listSiteFddFindingsV212',
  __SITE_FDD_LOW_DELTA_T_PATH__: 'evaluateSiteFddLowDeltaTV212',
  __SITE_FDD_LOW_DELTA_T_RULE_PATH__: 'getSiteFddLowDeltaTRuleV212',
  __SITE_SAVINGS_MEASURES_PATH__: 'listSiteSavingsMeasuresV212',
  __SITE_SAVINGS_MEASURE_PATH__: 'getSiteSavingsMeasureV212',
  __SITE_SAVINGS_IMPLEMENT_PATH__: 'implementSiteSavingsMeasureV212',
  __SITE_SAVINGS_REFIT_PATH__: 'refitSiteSavingsMeasureV212',
  __SITE_SAVINGS_END_PATH__: 'endSiteSavingsMeasureV212',
  __SITE_SAVINGS_SUMMARY_PATH__: 'getSiteSavingsSummaryV212',
  __SITE_MONTHLY_REPORTS_PATH__: 'listSiteMonthlyReportsV212',
  __SITE_MONTHLY_REPORT_PATH__: 'getSiteMonthlyReportV212',
  __OPTIMIZATION_RUNS_PATH__: 'createOptimizationRunV212',
  __LATEST_OPTIMIZATION_RECOMMENDATION_PATH__: 'getLatestSiteOptimizationRecommendationV212',
  __LATEST_OPTIMIZATION_PREPARATION_PATH__: 'getLatestSiteOptimizationPreparationV212',
  __LATEST_OPTIMIZATION_SHADOW_EVALUATION_PATH__: 'getLatestSiteOptimizationShadowEvaluationV212',
  __LATEST_OPTIMIZATION_STAGING_RECOMMENDATIONS_PATH__: 'getLatestSiteOptimizationStagingRecommendations',
  __SITE_OPERATING_SCHEDULE_PATH__: 'getSiteOperatingSchedule',
  __SITE_OPERATING_EXCEPTIONS_PATH__: 'listSiteOperatingExceptions',
  __SITE_OPERATING_EXCEPTION_DECIDE_PATH__: 'decideSiteOperatingException',
  __SITE_ELECTRICITY_BUDGETS_PATH__: 'listSiteMonthlyElectricityBudgets',
  __SITE_UTILITY_BILLS_PATH__: 'listSiteUtilityBills',
  __SITE_UTILITY_BILL_PATH__: 'updateSiteUtilityBill',
  __SITE_UTILITY_BILL_CONFIRM_PATH__: 'confirmSiteUtilityBill',
  __SITE_ACCOUNTING_RECALCULATE_PATH__: 'recalculateSiteAccounting',
  __SITE_ACCOUNTING_RECALC_JOBS_PATH__: 'listSiteAccountingRecalcJobs',
  __SITE_ACCOUNTING_HOLIDAYS_PATH__: 'listSiteCalendarHolidays',
  __SITE_ACCOUNTING_HOLIDAY_PATH__: 'upsertSiteCalendarHoliday',
  __SITE_ACCOUNTING_METER_RATIOS_PATH__: 'listSiteMeterRatioVersions',
  __SITE_ACCOUNTING_METER_RATIO_RETIRE_PATH__: 'retireSiteMeterRatioVersion',
  __SITE_ACCOUNTING_COUNTER_AUDIT_PATH__: 'getSiteCounterAudit',
  __SITE_DATA_GAPS_PATH__: 'listSiteDataGaps',
  __SITE_CONTROL_STATE_PATH__: 'getSiteControlState',
  __SITE_CONTROL_MODE_PATH__: 'updateSiteControlMode',
  __SITE_CONTROL_SHUTDOWN_PATH__: 'executeSiteControlShutdown',
  __ASSET_CONTROL_LOCK_PATH__: 'updateAssetControlLock',
  __SITE_AUTO_CONTROL_LEASE_PATH__: 'getSiteAutoControlLease',
  __SITE_AUTO_CONTROL_LEASE_REVOKE_PATH__: 'revokeSiteAutoControlLease',
  __SITE_OPTIMIZATION_EXECUTION_MONITORS_PATH__: 'listSiteOptimizationExecutionMonitors',
  __SITE_OPTIMIZATION_GOVERNANCE_SUMMARY_PATH__: 'getSiteOptimizationGovernanceSummary',
  __SITE_OPTIMIZATION_CANARY_BINDINGS_PATH__: 'listSiteOptimizationCanaryBindings',
  __SITE_OPTIMIZATION_CANARY_BINDING_PATH__: 'upsertSiteOptimizationCanaryBinding',
  __SITE_OPTIMIZATION_NEGATIVE_SAMPLES_PATH__: 'listSiteOptimizationNegativeSamples',
  __SITE_EDGE_MODELS_PATH__: 'getSiteEdgeModelWorkspace',
  __SITE_EDGE_MODEL_ARTIFACTS_PATH__: 'createSiteEdgeModelArtifact',
  __SITE_EDGE_MODEL_APPROVE_PATH__: 'approveSiteEdgeModelArtifact',
  __SITE_EDGE_MODEL_DEPLOYMENTS_PATH__: 'deploySiteEdgeModelArtifact',
  __SITE_EDGE_MODEL_ROLLBACK_PATH__: 'rollbackSiteEdgeModelDeployment',
  __SITE_EDGE_MODEL_EVALUATIONS_PATH__: 'recordSiteEdgeModelEvaluation',
  __HEALTH_PATH__: 'getHealth',
  __VERSION_PATH__: 'getVersion',
  __PLATFORM_STATUS_PATH__: 'getPlatformStatus',
  __LOGIN_PATH__: 'beginLogin',
  __CALLBACK_PATH__: 'completeLogin',
  __PRINCIPAL_PATH__: 'getCurrentPrincipal',
  __SWITCH_TENANT_PATH__: 'switchActiveTenant',
  __LOGOUT_PATH__: 'logout',
  __REVOKE_PATH__: 'revokeSession',
  __AUDIT_PATH__: 'getSessionAuditEvent',
  __AUDIT_SEARCH_PATH__: 'searchAudit',
  __ENERGY_SERIES_PATH__: 'queryEnergySeries',
  __SITES_PATH__: 'listSites',
  __SITE_PATH__: 'getSite',
  __ASSETS_PATH__: 'listAssets',
  __SITE_ASSET_PATH__: 'listSiteAssets',
  __ASSET_PATH__: 'getAsset',
  __DEVICES_PATH__: 'listDevices',
  __SITE_DEVICES_PATH__: 'listSiteDevices',
  __SITE_DEVICE_BINDINGS_PATH__: 'listSiteDeviceBindings',
  __SITE_ASSET_MODEL_PATH__: 'getSiteAssetModel',
  __SITE_TARIFF_PATH__: 'getSiteTariff',
  __DEVICE_PATH__: 'getDevice',
  __TEMPLATES_PATH__: 'listTemplates',
  __TEMPLATE_REVISION_PATH__: 'getTemplateRevision',
  __SITE_TEMPLATE_ASSIGNMENTS_PATH__: 'listSiteTemplateAssignments',
  __SITE_SPACE_CHILDREN_PATH__: 'listSiteSpaceChildren',
  __DEVICE_POINTS_PATH__: 'listDevicePoints',
  __SITE_SPACES_PATH__: 'createSiteSpace',
  __SITE_SPACE_PATH__: 'updateSiteSpace',
  __SITE_ASSET_DETAIL_PATH__: 'updateSiteAsset',
  __SITE_DEVICE_DETAIL_PATH__: 'updateSiteDevice',
  __SITE_DEVICE_REPLACE_HARDWARE_PATH__: 'replaceDeviceHardware',
  __SITE_SENSORS_PATH__: 'createSiteSensor',
  __SITE_SENSOR_PATH__: 'updateSiteSensor',
  __SITE_POINTS_PATH__: 'createSitePoint',
  __SITE_POINT_PATH__: 'updateSitePoint',
  __SITE_REBIND_PATH__: 'rebindRegistryResource',
  __TEMPLATE_REVISIONS_PATH__: 'releaseRegistryTemplateRevision',
  __TEMPLATE_ASSIGNMENTS_PATH__: 'assignRegistryTemplateRevision',
  __SITE_IMPORT_DRY_RUN_PATH__: 'planRegistryImport',
  __SITE_IMPORT_COMMIT_PATH__: 'commitRegistryImport',
  __SITE_RETIRE_PATH__: 'beginRegistryRetirement',
  __OTA_PACKAGES_PATH__: 'listOTAPackages',
  __OTA_CAMPAIGNS_PATH__: 'listOTACampaigns',
  __OTA_CAMPAIGN_PATH__: 'getOTACampaign',
  __OTA_CAMPAIGN_PAUSE_PATH__: 'pauseOTACampaign',
  __OTA_CAMPAIGN_RESUME_PATH__: 'resumeOTACampaign',
  __OTA_CAMPAIGN_ROLLBACK_PATH__: 'rollbackOTACampaign',
};
const replacements: Record<string, string> = {
  ...Object.fromEntries(w4.flatMap(([k, , , , r]) => (r ? [[r, operations[k].path]] : []))),
  ...Object.fromEntries(Object.entries(pathOps).map(([r, op]) => [r, operations[op].path])),
  __CONTRACT_BANNER__: banner,
};

function render(template: string): string {
  let output = template;
  for (const [placeholder, value] of Object.entries(replacements)) {
    if (output.includes(placeholder)) output = output.replaceAll(placeholder, value);
  }
  invariant(!/__[A-Z_]+__/.test(output), 'template contains an unresolved placeholder');
  return output;
}

function formatGo(source: string): string {
  const result = spawnSync(gofmtBinary, [], { input: source, encoding: 'utf8', windowsHide: true });
  invariant(!result.error, `gofmt could not start: ${result.error?.message ?? 'unknown error'}`);
  invariant(result.status === 0, `gofmt failed: ${result.stderr || `exit ${result.status}`}`);
  invariant(typeof result.stdout === 'string' && result.stdout.length > 0, 'gofmt returned empty output');
  return result.stdout;
}

async function emit(path: string, content: string): Promise<void> {
  let existing: string | null = null;
  try {
    existing = await readFile(path, 'utf8');
  } catch (error) {
    if (!(error instanceof Error && 'code' in error && error.code === 'ENOENT')) throw error;
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
