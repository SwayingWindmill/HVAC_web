import { readFile, stat } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const baseline = JSON.parse(await readFile(resolve(root, 'contracts/architecture/backend-architecture.v2.json'), 'utf8'));

function invariant(condition, message) {
  if (!condition) throw new Error(`Backend Architecture V2.1.2 check failed: ${message}`);
}

async function exists(path) {
  try {
    await stat(resolve(root, path));
    return true;
  } catch {
    return false;
  }
}

const expectedDomains = [
  'IAM', 'Tenant/Site', 'Space/Asset', 'Device/Product/Point', 'IoT Runtime', 'Telemetry',
  'Energy Topology', 'Metric', 'Energy', 'Tariff', 'Settlement', 'Alarm', 'Control', 'Config',
  'Notification', 'Forecast', 'Optimization', 'MLOps Metadata', 'Audit',
];
const allowedStatuses = new Set(['PASS', 'PARTIAL', 'MISSING']);

invariant(baseline.schemaVersion === 3, 'schemaVersion must be 3');
invariant(baseline.document?.documentId === 'SE-ARCH-004', 'documentId must be SE-ARCH-004');
invariant(baseline.document?.version === '2.1.2', 'document version must be V2.1.2');
invariant(baseline.document?.status === 'CURRENT / FROZEN CANDIDATE', 'document status drifted');
invariant(baseline.authorityOverrides?.tenantModel?.includes('Organization') && baseline.authorityOverrides?.tenantModel?.includes('Tenant'), 'Organization -> Tenant adjudication is missing');
invariant(baseline.serviceModel?.logicalDomainEqualsDeployableService === false, 'Logical Domain must not equal Deployable Service');
invariant(baseline.serviceModel?.servicePerTableAllowed === false, 'service-per-table must remain forbidden');
invariant(baseline.serviceModel?.kafkaRequiredInPhase1 === false, 'Kafka must remain optional in Phase 1');
invariant(JSON.stringify(baseline.logicalDomains.map(({ domain }) => domain)) === JSON.stringify(expectedDomains), '19-domain inventory drifted');
invariant(baseline.logicalDomains.every(({ alignment }) => alignment === 'ALIGNED' || alignment === 'PARTIAL'), 'logical domain alignment contains an invalid status');
invariant(baseline.conformance.length > 0 && baseline.conformance.every(({ status }) => allowedStatuses.has(status)), 'conformance status must be PASS/PARTIAL/MISSING');
invariant(baseline.acceptanceEligible === false, 'architecture checklist must not become a release/acceptance gate');

for (const domain of baseline.logicalDomains) {
  for (const implementation of domain.implementation) {
    invariant(await exists(implementation), `${domain.domain} implementation path is missing: ${implementation}`);
  }
}

const dataArchitecture = JSON.parse(await readFile(resolve(root, 'contracts/data/data-architecture.v2.json'), 'utf8'));
invariant(dataArchitecture.approvedRemovals?.includes('Organization'), 'Data Architecture V2 must keep Organization removed');
invariant(dataArchitecture.approvedRemovals?.includes('ThingsBoard'), 'Data Architecture V2 must keep ThingsBoard removed');

const publicOpenAPI = JSON.parse(await readFile(resolve(root, 'contracts/http/platform-gateway.openapi.yaml'), 'utf8'));
invariant(publicOpenAPI.openapi === '3.1.0', 'SE-API-001 machine contract must use OpenAPI 3.1');
invariant(publicOpenAPI['x-contract-id'] === 'SE-API-001' && publicOpenAPI.info?.version === '2.1.2', 'SE-API-001 machine contract identity/version drifted');
for (const contracted of baseline.api.contractedPaths) {
  const separator = contracted.indexOf(' ');
  const method = contracted.slice(0, separator).toLowerCase();
  const path = contracted.slice(separator + 1);
  invariant(publicOpenAPI.paths?.[path]?.[method], `SE-API-001 contracted route is missing: ${contracted}`);
}
invariant(publicOpenAPI.components?.responses?.V212Success && publicOpenAPI.components?.responses?.V212Error, 'SE-API-001 V2.1.2 response envelopes are missing');
invariant(publicOpenAPI.components?.schemas?.V212SuccessEnvelope && publicOpenAPI.components?.schemas?.V212ErrorEnvelope, 'SE-API-001 V2.1.2 envelope schemas are missing');
const contractOnlyGateway = await readFile(resolve(root, 'services/platform-gateway/internal/gateway/v212_contract_only.go'), 'utf8');
for (const route of [
  '/api/v1/sites',
  '/api/v1/sites/{siteId}/spaces/tree',
  '/api/v1/devices',
  '/api/v1/devices/{deviceId}/points',
  '/api/v1/telemetry/latest',
  '/api/v1/telemetry/history',
  '/api/v1/alarms',
  '/api/v1/alarms/{alarmId}',
  '/api/v1/alarms/{alarmId}/ack',
  '/api/v1/sites/{siteId}/forecast/load',
  '/api/v1/sites/{siteId}/forecast/pv',
  '/api/v1/optimization/runs',
  '/api/v1/optimization/runs/{runId}',
]) {
  invariant(contractOnlyGateway.includes(`template: "${route}"`), `SE-API-001 shape-pending route must be explicitly contract-only at runtime: ${route}`);
}
invariant(contractOnlyGateway.includes('CONTRACT_NOT_ACTIVE') && contractOnlyGateway.includes('writeV212Error'), 'SE-API-001 contract-only routes must return the frozen V2.1.2 error envelope');

const commandPublic = JSON.parse(await readFile(resolve(root, 'contracts/http/s3-command-public.openapi.json'), 'utf8'));
invariant(commandPublic.paths?.['/api/v1/commands/{commandId}/approve'], 'Command approve Contract URI must use /approve');
invariant(!commandPublic.paths?.['/api/v1/commands/{commandId}:approve'], 'legacy :approve Command URI must not remain contracted');
const alarmPublic = JSON.parse(await readFile(resolve(root, 'contracts/http/s4-alarm-public.openapi.json'), 'utf8'));
invariant(alarmPublic.paths?.['/api/v1/alarms']?.get, 'Alarm list Contract URI must be /api/v1/alarms');
invariant(alarmPublic.paths?.['/api/v1/alarms/{alarmId}']?.get, 'Alarm detail Contract URI must be /api/v1/alarms/{alarmId}');
invariant(alarmPublic.paths?.['/api/v1/alarms/{alarmId}/ack']?.post, 'Alarm ACK Contract URI must use /ack');
invariant(!Object.keys(alarmPublic.paths ?? {}).some((path) => path.includes('/sites/{siteId}/alarms') || path.includes(':acknowledge')), 'legacy Site-scoped/colon Alarm public URI must not remain contracted');
const operationsInternal = await readFile(resolve(root, 'contracts/http/operations-agent-internal.openapi.yaml'), 'utf8');
const telemetryQueryInternal = await readFile(resolve(root, 'contracts/http/telemetry-query-internal.openapi.yaml'), 'utf8');
invariant(!operationsInternal.includes('Organization') && !telemetryQueryInternal.includes('Organization'), 'HTTP machine contracts must be Tenant-only');

const commandGateway = await readFile(resolve(root, 'services/platform-gateway/internal/gateway/command.go'), 'utf8');
invariant(commandGateway.includes('strings.HasSuffix(raw, "/approve")'), 'Platform Gateway must route canonical Command /approve');
invariant(!commandGateway.includes('strings.HasSuffix(raw, ":approve")'), 'Platform Gateway production route must not parse legacy :approve');
const gatewayServer = await readFile(resolve(root, 'services/platform-gateway/internal/gateway/server.go'), 'utf8');
invariant(gatewayServer.includes('contractOnlyRoute.template != "/api/v1/sites" || request.Method == contractOnlyRoute.method'), 'SE-API-001 contract-only interception must preserve active GET /api/v1/sites while holding POST contract-only');
invariant(!gatewayServer.includes('header == "X-Organization-ID" && isVerifiedTelemetryWorkloadRequest(request)'), 'verified Telemetry workloads must not receive an X-Organization-ID compatibility exception');
invariant(gatewayServer.includes('"X-Operations-Registry-Asset-Grant"'), 'Platform Gateway must guard the canonical Operations Registry Asset grant header');
const operationsAgentHttp = await readFile(resolve(root, 'services/operations-agent-service/src/transport-http/internal/operations-agent-http.ts'), 'utf8');
invariant(operationsAgentHttp.includes("'X-Operations-Registry-Asset-Grant'"), 'Operations Agent must consume the canonical Registry Asset grant header');
invariant(!operationsAgentHttp.includes("'X-Operations-Registry-Equipment-Grant'"), 'Operations Agent must not consume the legacy Registry Equipment grant header');
const commandService = await readFile(resolve(root, 'services/command-service/pkg/commandservice/http.go'), 'utf8');
invariant(commandService.includes('strings.HasSuffix(raw, "/approve")'), 'Command Service must route canonical /approve');
invariant(!commandService.includes('strings.HasSuffix(raw, ":approve")'), 'Command Service production route must not parse legacy :approve');

const mqttConfig = await readFile(resolve(root, 'services/mqtt-telemetry-adapter/internal/adapter/config.go'), 'utf8');
for (const messageType of ['telemetry', 'state', 'event', 'heartbeat']) {
  invariant(mqttConfig.includes(`energy/v1/+/+/+/${messageType}`), `MQTT uplink subscription is missing ${messageType}`);
}
invariant(!mqttConfig.includes('energy/v1/+/+/+/#'), 'MQTT uplink adapter must not subscribe to command/reply through #');
const mqttProcessor = await readFile(resolve(root, 'services/mqtt-telemetry-adapter/internal/adapter/processor.go'), 'utf8');
invariant(mqttProcessor.includes('MessageTypeState') && mqttProcessor.includes('MessageTypeEvent') && mqttProcessor.includes('MessageTypeHeartbeat'), 'MQTT V2.1.2 message family is incomplete');
invariant(mqttProcessor.includes('SOURCE_ACTIVITY') && mqttProcessor.includes('negative report never directly forces Cloud OFFLINE'), 'Wire online must remain Presence evidence only');

const durableIntegration = await readFile(resolve(root, 'infra/s1-registry/postgres/init/009i-backend-integration-v2.sql'), 'utf8');
for (const token of ['domain_outbox_events', 'domain_event_deliveries', 'domain_consumer_inbox', 'cross_store_publications', 'publication_evidence']) {
  invariant(durableIntegration.includes(token), `durable/cross-store foundation missing ${token}`);
}
const metricEngine = await readFile(resolve(root, 'services/metric-engine-service/internal/metric/engine.go'), 'utf8');
invariant(metricEngine.includes('PERSISTING') || (await readFile(resolve(root, 'services/metric-engine-service/internal/metric/postgres.go'), 'utf8')).includes('PERSISTING'), 'Metric PERSISTING publication state is missing');
const forecastService = await readFile(resolve(root, 'services/forecast-service/internal/forecast/service.go'), 'utf8');
invariant(forecastService.includes('type ForecastEngine interface') && forecastService.includes('Reconcile('), 'Forecast cross-store reconciliation boundary is incomplete');
const optimizationService = await readFile(resolve(root, 'services/optimization-service/internal/optimization/service.go'), 'utf8');
for (const name of ['ProblemBuilder', 'ObjectiveBuilder', 'ConstraintBuilder', 'SolverAdapter']) {
  invariant(optimizationService.includes(`type ${name} interface`), `${name} interface is missing`);
}
invariant(optimizationService.includes('Reconcile('), 'Optimization Evaluation reconciliation boundary is incomplete');

const settlementService = await readFile(resolve(root, 'services/settlement-service/internal/settlement/clickhouse.go'), 'utf8');
invariant(settlementService.includes('analytics.metric_series'), 'Settlement must consume Metric Result from analytics.metric_series');
invariant(!settlementService.includes('energy_interval_facts'), 'Settlement must not recalculate standard metrics from energy_interval_facts');

const supportDomains = await readFile(resolve(root, 'infra/s1-registry/postgres/init/009j-operations-support-domains-v2.sql'), 'utf8');
invariant(supportDomains.includes('config_versions') && supportDomains.includes('config_desired_states') && supportDomains.includes('config_reported_states'), 'Config Domain boundary is incomplete');
invariant(supportDomains.includes('notification_user_states') && supportDomains.includes('Notification ACK') && supportDomains.includes('Alarm ACK'), 'Notification/Alarm ACK separation is missing');

const mlopsMetadata = await readFile(resolve(root, 'infra/s1-registry/postgres/init/009k-mlops-metadata-v2.sql'), 'utf8');
for (const token of ['mlops_artifacts', 'mlops_evaluations', 'mlops_approvals', 'mlops_deployment_metadata', 'mlops_drift_observations', 'mlops_rollback_records']) {
  invariant(mlopsMetadata.includes(token), `MLOps Metadata boundary missing ${token}`);
}

const phase1Migrations = JSON.parse(await readFile(resolve(root, 'deploy/platform/phase1/migrations/manifest.v1.json'), 'utf8'));
const phase1MigrationPaths = phase1Migrations.databases.flatMap(({ migrations }) => migrations);
invariant(phase1MigrationPaths.includes('infra/s1-registry/postgres/init/009d-data-governance-v2.sql'), 'Phase1 must deploy Data Governance before Object Storage governance');
invariant(phase1MigrationPaths.includes('infra/s1-registry/postgres/init/009g-object-storage-governance-v2.sql'), 'Phase1 must deploy Object Storage governance metadata');
const objectStorageContract = JSON.parse(await readFile(resolve(root, 'deploy/platform/phase1/object-storage.external.v1.json'), 'utf8'));
invariant(objectStorageContract.deploymentMode === 'EXTERNAL' && objectStorageContract.providerSelection === 'DEPLOYMENT_TIME', 'Object Storage must remain a deployment-selected external capability');
invariant(JSON.stringify(objectStorageContract.allowedProviderFamilies) === JSON.stringify(['S3_COMPATIBLE', 'AWS_S3', 'AZURE_BLOB', 'GCS']), 'Object Storage provider families drifted from the frozen governance model');
invariant(objectStorageContract.repositoryPolicy?.hardcodeProviderProduct === false && objectStorageContract.repositoryPolicy?.embedProviderCredentials === false, 'Repository must not select an Object Storage product or embed credentials');
invariant(objectStorageContract.repositoryPolicy?.productionProvisioningRequiredForPass === true, 'Object Storage must stay PARTIAL until production provisioning exists');

const compose = await readFile(resolve(root, 'deploy/platform/phase1/compose.yaml'), 'utf8');
invariant(compose.includes('postgres:') && compose.includes('clickhouse:') && compose.includes('redis:') && compose.includes('mqtt-broker:'), 'Phase1 core data/transport infrastructure is incomplete');
invariant(!compose.includes('\n  telemetry-history-projector:'), 'Phase1 must not deploy telemetry-history-projector as a standalone process');
invariant(!compose.includes('\n  analytics-read-model-projector:'), 'Phase1 must not deploy analytics-read-model-projector as a standalone process');
invariant(!compose.includes('\n  telemetry-runtime-service:'), 'Phase1 must not deploy telemetry-runtime-service under the pre-V2.1.2 deployable name');
invariant(compose.includes('\n  telemetry-worker:'), 'Phase1 must deploy the converged telemetry-worker process');
invariant(compose.includes('\n  metric-worker:'), 'Phase1 must deploy the canonical metric-worker process');
invariant(compose.includes('TELEMETRY_HISTORY_PROJECTION_ENABLED: "true"'), 'Phase1 telemetry-worker must own history projection in-process');
invariant(compose.includes('ANALYTICS_PROJECTION_ENABLED: "true"'), 'Phase1 telemetry-worker must own analytics projection in-process');
invariant(compose.includes('METRIC_WORKER_MODE: worker'), 'Phase1 metric-worker must run as a long-lived worker');
invariant(!compose.includes('\n  command-verifier:'), 'Phase1 must not deploy command-verifier as a standalone process');
invariant(!compose.includes('\n  command-dispatcher:'), 'Phase1 must not deploy command-dispatcher as a standalone process');
invariant(!compose.includes('\n  mqtt-telemetry-adapter:'), 'Phase1 must not deploy mqtt-telemetry-adapter as a standalone process');
invariant(compose.includes('\n  iot-service:'), 'Phase1 must deploy the converged iot-service process');
invariant(compose.includes('COMMAND_RUNTIME_IN_PROCESS_ENABLED: "true"'), 'Phase1 iot-service must own Command dispatch and verification in-process');
invariant(compose.includes('\n  energy-api:'), 'Phase1 must deploy the converged energy-api process');
invariant(!compose.includes('\n  platform-gateway:'), 'Phase1 must not deploy platform-gateway as a standalone process');
invariant(!compose.includes('\n  iam-service:'), 'Phase1 must not deploy iam-service as a standalone process');
invariant(!compose.includes('\n  platform-core-service:'), 'Phase1 must not deploy platform-core-service as a standalone process');
invariant(!compose.includes('\n  telemetry-query-service:'), 'Phase1 must not deploy telemetry-query-service as a standalone process');
invariant(!compose.includes('\n  alarm-service:'), 'Phase1 must not deploy alarm-service as a standalone process');
invariant(!compose.includes('\n  work-order-service:'), 'Phase1 must not deploy work-order-service as a standalone process');
invariant(!compose.includes('\n  command-service:'), 'Phase1 must not deploy command-service as a standalone process');
invariant(!compose.includes('\n  audit-ledger-service:'), 'Phase1 must not deploy audit-ledger-service as a standalone process');
invariant(compose.includes('ENERGY_API_IN_PROCESS_ENABLED: "true"'), 'Phase1 energy-api must own Gateway, IAM, Core, Telemetry Query, Audit, Alarm, Work Order and Command in-process');
const embeddedEnergy = await readFile(resolve(root, 'services/platform-gateway/cmd/platform-gateway/embedded_energy.go'), 'utf8');
invariant(embeddedEnergy.includes('sessionstore.OpenOutbox') && embeddedEnergy.includes('auditserver.OpenStore'), 'Phase1 Audit must use PostgreSQL Outbox -> Audit Ledger in-process');

const counts = baseline.conformance.reduce((acc, item) => {
  acc[item.status] = (acc[item.status] ?? 0) + 1;
  return acc;
}, {});
console.log(`Backend Architecture V2.1.2 baseline valid: domains=${baseline.logicalDomains.length}; checks=${baseline.conformance.length}; PASS=${counts.PASS ?? 0}; PARTIAL=${counts.PARTIAL ?? 0}; MISSING=${counts.MISSING ?? 0}; acceptanceEligible=${baseline.acceptanceEligible}`);
