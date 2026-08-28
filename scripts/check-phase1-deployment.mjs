import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { parseRuntimeEnvironment, resolveDeploymentTier } from './phase1-deployment-tier.ts';
import { validateRecoveryAttainment } from './phase1-recovery-attainment.ts';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const paths = {
  baseline: 'deploy/platform/phase1/architecture-baseline.v1.json',
  matrix: 'deploy/platform/phase1/alignment-matrix.v1.json',
  compose: 'deploy/platform/phase1/compose.yaml',
  ownerSplitCompose: 'deploy/platform/phase1/owner-split.compose.yaml',
  nginx: 'deploy/platform/phase1/nginx/nginx.conf',
  webDockerfile: 'deploy/platform/phase1/nginx/Dockerfile',
  prometheus: 'deploy/platform/phase1/observability/prometheus.yaml',
  hostAlerts: 'infra/observability/alerts/host-resource.yaml',
  otel: 'deploy/platform/phase1/observability/otel-collector/full.yaml',
  otelLogs: 'deploy/platform/phase1/observability/otel-collector/logs.yaml',
  deploymentTiers: 'deploy/platform/phase1/deployment-tiers.v1.json',
  runtimeInventory: 'deploy/platform/phase1/runtime-inventory.v1.json',
  availabilityTier: 'deploy/platform/phase1/availability-tier.v1.json',
  s2ReleaseGates: 'deploy/s2/release-gates.v1.json',
  recoveryAttainment: 'deploy/platform/phase1/recovery/attainment.v1.json',
  loki: 'deploy/platform/phase1/observability/loki.yaml',
  tempo: 'deploy/platform/phase1/observability/tempo.yaml',
  grafanaDatasources: 'deploy/platform/phase1/observability/grafana/provisioning/datasources/datasources.yaml',
  grafanaDashboards: 'deploy/platform/phase1/observability/grafana/provisioning/dashboards/dashboards.yaml',
  postgresBackup: 'deploy/platform/phase1/backup/postgres-base-backup.sh',
  backupReadiness: 'deploy/platform/phase1/backup/verify-recovery-readiness.sh',
  clickhouseBackup: 'deploy/platform/phase1/backup/clickhouse-backup.sh',
  clickhouseBackupConfig: 'deploy/platform/phase1/backup/clickhouse-backup.xml',
  clickhouseResourceLimits: 'deploy/platform/phase1/clickhouse/resource-limits.xml',
  backupReadme: 'deploy/platform/phase1/backup/README.md',
  recoveryTargets: 'deploy/platform/phase1/recovery/recovery-targets.v1.json',
  recoveryReadme: 'deploy/platform/phase1/recovery/README.md',
  recoveryDrillTemplate: 'deploy/platform/phase1/recovery/drill-record.template.json',
  processFailureScenarios: 'deploy/platform/phase1/recovery/process-failure-scenarios.v1.json',
  gitignore: '.gitignore',
  phase1Databases: 'deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql',
  phase1Readme: 'deploy/platform/phase1/README.md',
  migrationManifest: 'deploy/platform/phase1/migrations/manifest.v1.json',
  migrationList: 'deploy/platform/phase1/migrations/migration-list.tsv',
  migrationDockerfile: 'deploy/platform/phase1/migrations/Dockerfile',
  migrationRunner: 'deploy/platform/phase1/migrations/run-phase1-migrations.sh',
  schemaPreflight: 'deploy/platform/phase1/migrations/verify-phase1-schema.sh',
  productRelease: 'deploy/platform/phase1/product-release.v1.json',
  limitPolicy: 'deploy/platform/phase1/limit-policy.v1.json',
  roleCredentialTemplate: 'deploy/platform/phase1/migrations/role-credentials.sql.example',
  packageJson: 'package.json',
  goWork: 'go.work',
  goServiceDockerfile: 'deploy/platform/phase1/images/go-service.Dockerfile',
  observabilityRuntime: 'libs/observability/runtime.go',
  gatewayMain: 'cmd/energy-api/main.go',
  embeddedEnergy: 'cmd/energy-api/embedded_energy.go',
  operationsGateway: 'cmd/energy-api/internal/gateway/operations_agent.go',
  identityMain: 'services/identity-service/cmd/identity-service/main.go',
  schedulerMain: 'cmd/scheduler/main.go',
  maintenanceMain: 'cmd/maintenance-worker/main.go',
  telemetryRuntimeMain: 'cmd/telemetry-worker/main.go',
  metricMain: 'cmd/metric-worker/main.go',
  iotRuntime: 'modules/iot/pkg/adapter/runtime.go',
  thingsboardSourceReview: 'docs/architecture/thingsboard-operations-platform-deployment-ha-observability-upgrade-adjudication.md',
};

const environmentFiles = {
  development: 'deploy/platform/phase1/environments/development.runtime.env.example',
  testing: 'deploy/platform/phase1/environments/testing.runtime.env.example',
  staging: 'deploy/platform/phase1/environments/staging.runtime.env.example',
  production: 'deploy/platform/phase1/environments/production.runtime.env.example',
};

const [baseline, matrix, compose, nginx, webDockerfile, prometheus, hostAlerts, otel, loki, tempo, grafanaDatasources, grafanaDashboards, postgresBackup, backupReadiness, clickhouseBackup, clickhouseBackupConfig, backupReadme, recoveryTargets, recoveryReadme, recoveryDrillTemplate, gitignore, phase1Databases, phase1Readme, migrationManifest, migrationList, migrationDockerfile, migrationRunner, schemaPreflight, productRelease, limitPolicy, roleCredentialTemplate, packageJson] = await Promise.all([
  readJSON(paths.baseline),
  readJSON(paths.matrix),
  read(paths.compose),
  read(paths.nginx),
  read(paths.webDockerfile),
  read(paths.prometheus),
  read(paths.hostAlerts),
  read(paths.otel),
  read(paths.loki),
  read(paths.tempo),
  read(paths.grafanaDatasources),
  read(paths.grafanaDashboards),
  read(paths.postgresBackup),
  read(paths.backupReadiness),
  read(paths.clickhouseBackup),
  read(paths.clickhouseBackupConfig),
  read(paths.backupReadme),
  readJSON(paths.recoveryTargets),
  read(paths.recoveryReadme),
  readJSON(paths.recoveryDrillTemplate),
  read(paths.gitignore),
  read(paths.phase1Databases),
  read(paths.phase1Readme),
  readJSON(paths.migrationManifest),
  read(paths.migrationList),
  read(paths.migrationDockerfile),
  read(paths.migrationRunner),
  read(paths.schemaPreflight),
  readJSON(paths.productRelease),
  readJSON(paths.limitPolicy),
  read(paths.roleCredentialTemplate),
  read(paths.packageJson),
]);
const envs = Object.fromEntries(await Promise.all(Object.entries(environmentFiles).map(async ([name, path]) => [name, await read(path)])));
const [otelLogs, deploymentTiers, runtimeInventory, availabilityTier, recoveryAttainment, s2ReleaseGates, clickhouseResourceLimits, processFailureScenarios, ownerSplitCompose, embeddedEnergy, thingsboardSourceReview] = await Promise.all([
  read(paths.otelLogs),
  readJSON(paths.deploymentTiers),
  readJSON(paths.runtimeInventory),
  readJSON(paths.availabilityTier),
  readJSON(paths.recoveryAttainment),
  readJSON(paths.s2ReleaseGates),
  read(paths.clickhouseResourceLimits),
  readJSON(paths.processFailureScenarios),
  read(paths.ownerSplitCompose),
  read(paths.embeddedEnergy),
  read(paths.thingsboardSourceReview),
]);
const runtimeSources = Object.fromEntries(await Promise.all([
  'observabilityRuntime', 'gatewayMain', 'operationsGateway', 'identityMain', 'schedulerMain', 'maintenanceMain', 'telemetryRuntimeMain', 'metricMain', 'iotRuntime',
].map(async (name) => [name, await read(paths[name])])));
const [goWork, goServiceDockerfile] = await Promise.all([
  read(paths.goWork),
  read(paths.goServiceDockerfile),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(baseline.deploymentModel?.orchestration === 'docker-compose', 'canonical deployment must remain Docker Compose');
assert(baseline.deploymentModel?.kubernetesRequired === false, 'Kubernetes must not become a Phase 1 requirement');
assert(baseline.deploymentModel?.singleServerRequired === true, 'Phase 1 must require the single-server deployment baseline');
assert(baseline.deploymentModel?.fewServersAllowed === false, 'multi-server deployment must remain outside the current Phase 1 baseline');
assert(baseline.delivery?.backwardCompatibleMigrationPreferred === false, 'Phase 1 must not prefer backward-compatible migration or compatibility layers');
assert(productRelease.schemaVersion === 1 && productRelease.product === 'hvac-web', 'Product Release contract must identify hvac-web');
assert(productRelease.schemaContract?.compatibilityMode === 'exact-product-and-manifest' && productRelease.schemaContract?.skipAllowed === false, 'Product Release must require exact non-skippable product/schema compatibility');
assert(productRelease.startup?.failureMode === 'fail-closed', 'Product Release startup preflight must fail closed');
assert(productRelease.platformPolicy?.limitPolicy === paths.limitPolicy, 'Product Release must pin the Phase 1 LimitPolicy artifact');
assert(limitPolicy.schemaVersion === 1 && limitPolicy.policyId === 'phase1-high-risk-limits' && Number.isInteger(limitPolicy.revision) && limitPolicy.revision > 0, 'LimitPolicy identity must be versioned');
assert(limitPolicy.limits?.operationsAgent?.backend === 'redis' && limitPolicy.limits?.operationsAgent?.failureMode === 'fail-closed' && limitPolicy.limits?.operationsAgent?.scope === 'session', 'Operations Agent limit must use the shared Redis fail-closed policy');
assert(limitPolicy.limits?.operationsAgent?.windowSeconds === 60 && limitPolicy.limits?.operationsAgent?.maxRequests === 30, 'Operations Agent limit bounds must match the Phase 1 policy');
assert(baseline.recovery?.sourceOfTruth === 'SE-OPS-009 V1.0 CURRENT CANDIDATE' && baseline.recovery?.rpoRtoTargetsDefined === true, 'Phase 1 baseline must freeze SE-OPS-009 recovery targets');
assert(baseline.recovery?.postgresRpoSeconds === 300 && baseline.recovery?.postgresRtoSeconds === 7200 && baseline.recovery?.controlRtoSeconds === 3600, 'Phase 1 baseline must preserve PostgreSQL/Control recovery objectives');
assert(baseline.recovery?.offServerBackupCopyRequired === true && baseline.recovery?.wholeServerFourHourTargetRequiresColdStandby === true, 'whole-server recovery target must preserve off-server backup and cold-standby prerequisites');
assert(baseline.deploymentTiers?.contract === paths.deploymentTiers && baseline.deploymentTiers?.profileSelectionRequired === true && baseline.deploymentTiers?.intelligenceServicesOptional === true, 'baseline must pin the tier contract and require explicit profile selection');
assert(baseline.runtimeInventory?.contract === paths.runtimeInventory && baseline.runtimeInventory?.canonicalCompose === paths.compose && baseline.runtimeInventory?.allComposeServicesClassified === true, 'baseline must pin the canonical runtime inventory and require complete Compose classification');
assert(JSON.stringify(baseline.runtimeInventory?.defaultBusinessDeployables) === JSON.stringify(['energy-api', 'telemetry-worker', 'metric-worker']), 'baseline must freeze the three default business deployables');
assert(JSON.stringify(baseline.runtimeInventory?.defaultSupportingWorkloads) === JSON.stringify(['scheduler', 'maintenance']), 'baseline must classify scheduler and maintenance as supporting workloads');
assert(JSON.stringify(baseline.runtimeInventory?.localStatefulInfrastructure) === JSON.stringify(['postgres', 'clickhouse', 'redis']), 'baseline must classify local PostgreSQL, ClickHouse and Redis separately from external state');
assert(JSON.stringify(baseline.runtimeInventory?.optionalIntegration) === JSON.stringify(['iot-service', 'mqtt-broker']), 'baseline must classify IoT/MQTT as optional integration');
assert(JSON.stringify(baseline.runtimeInventory?.optionalIntelligence) === JSON.stringify(['forecast-service', 'optimization-service', 'fdd-service']), 'baseline must classify Forecast/Optimization/FDD as optional intelligence');
assert(baseline.availabilityTier?.contract === paths.availabilityTier && baseline.availabilityTier?.currentTier === 'SINGLE_NODE_RECOVERABLE' && baseline.availabilityTier?.highAvailability === false && baseline.availabilityTier?.numericAvailabilitySloAllowed === false, 'baseline must pin SINGLE_NODE_RECOVERABLE and forbid unmeasured numeric SLO claims');
assert(baseline.recovery?.attainmentContract === paths.recoveryAttainment && baseline.recovery?.numericAvailabilitySloClaimForbiddenWithoutEvidence === true, 'baseline must pin the recovery attainment contract');
assert(productRelease.deploymentTiers === paths.deploymentTiers && productRelease.runtimeInventory === paths.runtimeInventory && productRelease.availabilityTier === paths.availabilityTier && productRelease.recoveryAttainment === paths.recoveryAttainment, 'Product Release must pin runtime inventory, tier, availability and recovery attainment contracts');
assert(deploymentTiers.schemaVersion === 1 && deploymentTiers.sourceOfTruth === 'SE-ARCH-DEPLOY-001 V1.0 CURRENT', 'deployment tier contract identity must be versioned and governed');
for (const tierId of ['demo', 'single-lite', 'single-full']) {
  const tier = (deploymentTiers.tiers ?? []).find((entry) => entry.id === tierId);
  assert(tier && Array.isArray(tier.profiles) && tier.profiles.length === 1, `deployment tier ${tierId} must declare exactly one observability profile`);
  try {
    resolveDeploymentTier({ contract: deploymentTiers, compose, tierId, environment: tierId === 'demo' ? 'testing' : 'production', additionalProfiles: ['local-postgres', 'local-clickhouse', 'local-redis'] });
  } catch (error) {
    assert(false, error.message);
  }
}
assert(JSON.stringify(deploymentTiers.profileDefinitions?.['local-postgres']?.services) === JSON.stringify(['postgres']) && JSON.stringify(deploymentTiers.profileDefinitions?.['local-clickhouse']?.services) === JSON.stringify(['clickhouse', 'cube']) && JSON.stringify(deploymentTiers.profileDefinitions?.['local-redis']?.services) === JSON.stringify(['redis']) && deploymentTiers.profileDefinitions?.integration?.optional === true && JSON.stringify(deploymentTiers.profileDefinitions?.integration?.services) === JSON.stringify(['iot-service', 'mqtt-broker']) && deploymentTiers.profileDefinitions?.intelligence?.optional === true && deploymentTiers.profileDefinitions?.['observability-core']?.services?.length === 3 && deploymentTiers.profileDefinitions?.['observability-full']?.services?.includes('tempo'), 'tier profiles must define local PostgreSQL, local ClickHouse, local Redis, optional integration, optional intelligence and three observability shapes');
assert(runtimeInventory.schemaVersion === 1 && runtimeInventory.sourceOfTruth === 'SE-ARCH-DEPLOY-001 V1.0 CURRENT' && runtimeInventory.canonicalCompose === paths.compose, 'runtime inventory identity must be versioned and point to the canonical Compose');
assert(JSON.stringify(runtimeInventory.classes?.defaultBusinessDeployables) === JSON.stringify(['energy-api', 'telemetry-worker', 'metric-worker']), 'runtime inventory must freeze the three default business deployables');
assert(JSON.stringify(runtimeInventory.classes?.defaultSupportingWorkloads) === JSON.stringify(['scheduler', 'maintenance']), 'runtime inventory must freeze scheduler and maintenance as supporting workloads');
assert(JSON.stringify(runtimeInventory.classes?.defaultIdentityInfrastructure) === JSON.stringify(['identity-service']), 'runtime inventory must keep identity-service outside business deployables');
assert(JSON.stringify(runtimeInventory.classes?.localStatefulInfrastructure) === JSON.stringify(['postgres', 'clickhouse', 'redis']), 'runtime inventory must freeze local PostgreSQL, ClickHouse and Redis as placement-specific services');
assert(JSON.stringify(runtimeInventory.classes?.optionalIntegration) === JSON.stringify(['iot-service', 'mqtt-broker']), 'runtime inventory must freeze optional IoT/MQTT integration');
assert(JSON.stringify(runtimeInventory.classes?.optionalIntelligence) === JSON.stringify(['forecast-service', 'optimization-service', 'fdd-service']), 'runtime inventory must freeze all optional intelligence services');
assert(runtimeInventory.semantics?.maintenanceDecision === 'KEEP_AS_SUPPORTING_WORKER' && runtimeInventory.semantics?.identityDecision === 'SEPARATE_INFRASTRUCTURE' && runtimeInventory.semantics?.integrationDecision === 'OPTIONAL_PROFILE' && runtimeInventory.semantics?.postgresPlacementDecision === 'LOCAL_OR_EXTERNAL' && runtimeInventory.semantics?.clickhousePlacementDecision === 'LOCAL_OR_EXTERNAL' && runtimeInventory.semantics?.redisPlacementDecision === 'LOCAL_OR_EXTERNAL' && runtimeInventory.semantics?.intelligenceDecision === 'OPTIONAL_PROFILE', 'runtime inventory decisions must preserve maintenance, identity, PostgreSQL/ClickHouse/Redis placement, integration and intelligence boundaries');
const canonicalSourceEntrypoints = {
  'energy-api': 'cmd/energy-api',
  'iot-service': 'cmd/iot-service',
  'telemetry-worker': 'cmd/telemetry-worker',
  'metric-worker': 'cmd/metric-worker',
  scheduler: 'cmd/scheduler',
  maintenance: 'cmd/maintenance-worker',
};
assert(JSON.stringify(runtimeInventory.sourceEntrypoints) === JSON.stringify(canonicalSourceEntrypoints), 'runtime inventory must freeze canonical cmd/* source entrypoints');
assert(goServiceDockerfile.includes('COPY cmd ./cmd'), 'canonical Go image build must copy root cmd/ entrypoints');
assert(clickhouseResourceLimits.includes('max_server_memory_usage') && clickhouseResourceLimits.includes('from_env="CLICKHOUSE_SERVER_MEMORY_USAGE"'), 'ClickHouse internal memory must be bound by the selected deployment tier');
assert(compose.includes('./clickhouse/resource-limits.xml:/etc/clickhouse-server/config.d/resource-limits.xml:ro'), 'Compose must mount the ClickHouse internal resource limit');
assert(availabilityTier.schemaVersion === 1 && availabilityTier.currentTier === 'SINGLE_NODE_RECOVERABLE', 'availability tier contract must fix the current tier');
const ownerSplitStage = availabilityTier.monolithToMultiInstancePath?.stages?.find((stage) => stage.id === 'stage-1');
assert(ownerSplitStage?.state === 'implemented-runtime-drill-required' && ownerSplitStage.exitEvidence?.includes('same-version-live-contract-drill'), 'Stage 1 must remain implemented but uncertified until the live same-version drill');
for (const owner of ['iam-owner', 'audit-owner', 'core-owner', 'telemetry-query-owner', 'command-owner', 'alarm-owner', 'work-order-owner']) {
  assert(ownerSplitCompose.includes(`  ${owner}:`), `owner-split Compose must define ${owner}`);
}
assert(compose.includes('dockerfile: deploy/platform/phase1/images/go-service.Dockerfile') && !compose.includes('dockerfile: deploy/s'), 'canonical Compose must build Go workloads from the canonical Phase 1 image definition, not historical deploy/s* paths');
assert(ownerSplitCompose.includes('dockerfile: deploy/platform/phase1/images/go-service.Dockerfile') && !ownerSplitCompose.includes('dockerfile: deploy/s'), 'owner-split overlay must reuse the canonical Phase 1 image definition, not historical deploy/s* paths');
assert(ownerSplitCompose.includes('ENERGY_API_EMBEDDED_OWNERS: notification') && !/kafka|zookeeper|redpanda/i.test(ownerSplitCompose), 'owner-split must retain only Notification in-process without adding a message backbone');
assert(embeddedEnergy.includes('parseEmbeddedOwners') && embeddedEnergy.includes('ENERGY_API_EMBEDDED_OWNERS'), 'energy-api must implement exact embedded Owner selection');
assert(thingsboardSourceReview.includes('## 18. Phase 1 deployment tiers / owner split 实施源码复核') && thingsboardSourceReview.includes('DefaultTbServiceInfoProvider.java') && thingsboardSourceReview.includes('DefaultInMemoryStorageTest.java'), 'deployment topology changes must retain the pinned ThingsBoard source/test review');
assert(
  s2ReleaseGates.slo?.snapshotHttp?.availabilityScope === 'snapshot-http-conditional-on-required-infrastructure'
    && JSON.stringify(s2ReleaseGates.slo?.snapshotHttp?.appliesToAvailabilityTiers) === JSON.stringify(['COMPONENT_REDUNDANT', 'HA'])
    && s2ReleaseGates.slo?.snapshotHttp?.wholePlatformClaimAllowed === false,
  'S2 Snapshot 99.9 objective must be scoped away from SINGLE_NODE_RECOVERABLE and whole-platform claims',
);
assert((availabilityTier.spofInventory ?? []).length >= 6 && availabilityTier.spofInventory.every((entry) => entry.spof === true && entry.component && Array.isArray(entry.upgradePath) && entry.upgradePath.length > 0), 'availability tier must enumerate SPOF impacts and upgrade costs');
assert(availabilityTier.monolithToMultiInstancePath?.reference?.tag === 'v4.3.1.1' && availabilityTier.monolithToMultiInstancePath?.reference?.commit === 'c2a52e46c44e308ddee430e7266b8e10eddde9c4', 'upgrade path reference must pin the reviewed ThingsBoard source');
try {
  validateRecoveryAttainment(recoveryAttainment);
} catch (error) {
  assert(false, error.message);
}
assert(
  processFailureScenarios.environment === 'staging'
    && processFailureScenarios.productionClaimAllowed === false
    && processFailureScenarios.containerOnlyEvidenceSufficient === false
    && processFailureScenarios.scenarios?.length === 9,
  'process failure drill must cover the staging single points without creating production DR evidence',
);
assert(compose.includes('x-phase1-deployment-tiers:') && compose.includes('availabilityContract: ./availability-tier.v1.json') && compose.includes('runtimeInventoryContract: ./runtime-inventory.v1.json'), 'Compose must expose tier, availability and runtime inventory contracts as static metadata');

const requiredServices = [
  'nginx',
  'energy-api',
  'identity-service',
  'scheduler',
  'maintenance',
  'iot-service',
  'telemetry-worker',
  'metric-worker',
  'postgres',
  'clickhouse',
  'cube',
  'redis',
  'mqtt-broker',
  'centrifugo',
  'otel-collector',
  'prometheus',
  'node-exporter',
  'loki',
  'tempo',
  'grafana',
];
const profileExpectations = {
  postgres: ['local-postgres'],
  clickhouse: ['local-clickhouse'],
  cube: ['local-clickhouse'],
  redis: ['local-redis'],
  'iot-service': ['integration'],
  'mqtt-broker': ['integration'],
  'otel-collector': ['observability-logs', 'observability-full'],
  prometheus: ['observability-core', 'observability-logs', 'observability-full'],
  'node-exporter': ['observability-core', 'observability-logs', 'observability-full'],
  loki: ['observability-logs', 'observability-full'],
  tempo: ['observability-full'],
  grafana: ['observability-core', 'observability-logs', 'observability-full'],
};
for (const service of requiredServices) assert(new RegExp(`^  ${service}:`, 'm').test(compose), `compose is missing required service ${service}`);
const serviceBlocksEarly = [...compose.matchAll(/^  ([a-z0-9-]+):\n([\s\S]*?)(?=^  [a-z0-9-]+:\n|^networks:|^volumes:)/gm)];
const composeServicesSection = compose.match(/^services:\n([\s\S]*?)^networks:/m)?.[1] ?? '';
const composeServiceNames = [...composeServicesSection.matchAll(/^  ([a-z0-9-]+):/gm)].map((match) => match[1]).sort();
const operatorOneShot = runtimeInventory.classes?.operatorOneShot ?? [];
const classifiedServiceNames = [
  ...(runtimeInventory.classes?.defaultPublic ?? []),
  ...(runtimeInventory.classes?.defaultBusinessDeployables ?? []),
  ...(runtimeInventory.classes?.defaultSupportingWorkloads ?? []),
  ...(runtimeInventory.classes?.defaultIdentityInfrastructure ?? []),
  ...(runtimeInventory.classes?.defaultDataAndRealtimeInfrastructure ?? []),
  ...(runtimeInventory.classes?.localStatefulInfrastructure ?? []),
  ...(runtimeInventory.classes?.localSemanticInfrastructure ?? []),
  ...(runtimeInventory.classes?.optionalIntegration ?? []),
  ...(runtimeInventory.classes?.profiledObservabilityInfrastructure ?? []),
  ...(runtimeInventory.classes?.optionalIntelligence ?? []),
  ...(runtimeInventory.classes?.requiredStartupOneShot ?? []),
  ...operatorOneShot.map((entry) => entry.service),
];
const duplicateClassifications = classifiedServiceNames.filter((service, index) => classifiedServiceNames.indexOf(service) !== index);
assert(duplicateClassifications.length === 0, `runtime inventory classifies services more than once: ${[...new Set(duplicateClassifications)].join(', ')}`);
assert(JSON.stringify([...classifiedServiceNames].sort()) === JSON.stringify(composeServiceNames), `runtime inventory must classify every canonical Compose service exactly once; compose=${composeServiceNames.join(', ')} inventory=${[...classifiedServiceNames].sort().join(', ')}`);
for (const [service, entrypoint] of Object.entries(canonicalSourceEntrypoints)) {
  const block = serviceBlocksEarly.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes(`SERVICE_PACKAGE: ./${entrypoint}`), `${service} must build from canonical source entrypoint ${entrypoint}`);
  assert(goWork.includes(`./${entrypoint}`), `go.work must register canonical source entrypoint ${entrypoint}`);
}
for (const service of [
  ...(runtimeInventory.classes?.defaultPublic ?? []),
  ...(runtimeInventory.classes?.defaultBusinessDeployables ?? []),
  ...(runtimeInventory.classes?.defaultSupportingWorkloads ?? []),
  ...(runtimeInventory.classes?.defaultIdentityInfrastructure ?? []),
  ...(runtimeInventory.classes?.defaultDataAndRealtimeInfrastructure ?? []),
]) {
  const block = serviceBlocksEarly.find(([, name]) => name === service)?.[2] ?? '';
  assert(!block.includes('profiles:'), `default runtime service ${service} must not require a Compose profile`);
}
for (const entry of operatorOneShot) {
  const block = serviceBlocksEarly.find(([, name]) => name === entry.service)?.[2] ?? '';
  assert(block.includes(`profiles: ["${entry.profile}"]`), `${entry.service} must remain an explicit one-shot ${entry.profile} profile action`);
}
const startupPreflightBlock = serviceBlocksEarly.find(([, name]) => name === 'phase1-schema-preflight')?.[2] ?? '';
assert(startupPreflightBlock.includes('restart: "no"') && !startupPreflightBlock.includes('profiles:'), 'phase1-schema-preflight must remain an automatic required one-shot startup gate');
assert(startupPreflightBlock.includes('PGHOST: ${PHASE1_POSTGRES_HOST:-postgres}') && startupPreflightBlock.includes('PGSSLMODE: ${PHASE1_POSTGRES_SSLMODE:-disable}') && startupPreflightBlock.includes('required: false'), 'schema preflight must support local or external PostgreSQL without hard-requiring the local container');
for (const [service, expectedProfiles] of Object.entries(profileExpectations)) {
  const block = serviceBlocksEarly.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes(`profiles: ["${expectedProfiles.join('", "')}"]`), `${service} must be gated to profiles ${expectedProfiles.join(', ')}`);
}
for (const service of ['forecast-service', 'optimization-service', 'fdd-service']) {
  const block = serviceBlocksEarly.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes('profiles: ["intelligence"]'), `${service} must be an explicit optional intelligence profile service`);
}
assert(!compose.includes('forecast-service:\n          condition: service_healthy') && !compose.includes('optimization-service:\n          condition: service_healthy') && !compose.includes('fdd-service:\n          condition: service_healthy'), 'energy-api must not hard-depend on optional intelligence services');
assert(/^  phase1-schema-preflight:/m.test(compose), 'Compose must include the one-shot Product/Schema preflight');
assert(compose.includes('PLATFORM_LIMIT_POLICY_FILE: /run/hvac/config/limit-policy.v1.json'), 'energy-api must receive the pinned LimitPolicy path');
assert(compose.includes('./limit-policy.v1.json:/run/hvac/config/limit-policy.v1.json:ro'), 'energy-api must mount LimitPolicy read-only');
assert(compose.includes('LIMIT_POLICY_REDIS_URL: ${LIMIT_POLICY_REDIS_URL:-redis://redis:6379/2}'), 'Operations limit authority must use shared Redis');

for (const network of ['application', 'mqtt', 'observability']) {
  assert(new RegExp(`^  ${network}:\\n    internal: true`, 'm').test(compose), `compose network ${network} must be internal`);
}
assert(/^  data:\n    internal: \$\{PHASE1_DATA_NETWORK_INTERNAL:-true\}/m.test(compose), 'data network must default to internal and open egress only through the placement-aware launcher');
assert(/^  public: \{\}/m.test(compose), 'compose must have an explicit public network');

const serviceBlocks = [...compose.matchAll(/^  ([a-z0-9-]+):\n([\s\S]*?)(?=^  [a-z0-9-]+:\n|^networks:|^volumes:)/gm)];
const identityServiceBlock = serviceBlocks.find(([, name]) => name === 'identity-service')?.[2] ?? '';
const identityAdminBlock = serviceBlocks.find(([, name]) => name === 'identity-admin')?.[2] ?? '';
const identityMFAKeygenBlock = serviceBlocks.find(([, name]) => name === 'identity-mfa-keygen')?.[2] ?? '';
const energyAPIBlock = serviceBlocks.find(([, name]) => name === 'energy-api')?.[2] ?? '';
const iamCredentialKeygenBlock = serviceBlocks.find(([, name]) => name === 'iam-api-credential-keygen')?.[2] ?? '';
assert(identityServiceBlock.includes('IDENTITY_MFA_ENCRYPTION_KEY_FILE: /run/hvac/identity/mfa-encryption.key') && identityServiceBlock.includes('${IDENTITY_RUNTIME_DIR:-./runtime/identity}:/run/hvac/identity:ro'), 'identity-service must use the dedicated MFA encryption key from a read-only runtime mount');
assert(identityAdminBlock.includes('IDENTITY_MFA_ENCRYPTION_KEY_FILE: /run/hvac/identity/mfa-encryption.key') && identityAdminBlock.includes('${IDENTITY_RUNTIME_DIR:-./runtime/identity}:/run/hvac/identity:ro'), 'identity-admin must use the dedicated MFA encryption key from a read-only runtime mount');
assert(identityMFAKeygenBlock.includes('IDENTITY_MFA_KEY_OUT: /run/hvac/identity/mfa-encryption.key') && identityMFAKeygenBlock.includes('${IDENTITY_RUNTIME_DIR:-./runtime/identity}:/run/hvac/identity'), 'Phase 1 must provide the explicit MFA key bootstrap tool');
assert(energyAPIBlock.includes('IAM_ADMIN_DATABASE_URL: ${IAM_ADMIN_DATABASE_URL:-}') && energyAPIBlock.includes('IAM_API_CREDENTIAL_PEPPER_FILE: /run/hvac/iam/api-credential.pepper') && energyAPIBlock.includes('${IAM_RUNTIME_DIR:-./runtime/iam}:/run/hvac/iam:ro'), 'energy-api must use the least-privilege IAM admin DSN and a read-only external API Credential pepper');
assert(iamCredentialKeygenBlock.includes('IAM_API_CREDENTIAL_PEPPER_OUT: /run/hvac/iam/api-credential.pepper') && iamCredentialKeygenBlock.includes('${IAM_RUNTIME_DIR:-./runtime/iam}:/run/hvac/iam'), 'Phase 1 must provide the explicit API Credential pepper bootstrap tool');
for (const service of ['energy-api', 'identity-service', 'scheduler', 'maintenance', 'telemetry-worker', 'metric-worker', 'iot-service']) {
  const block = serviceBlocks.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes('phase1-schema-preflight:') && block.includes('condition: service_completed_successfully'), `${service} must fail closed on Product/Schema preflight`);
}
const published = serviceBlocks.filter(([, , block]) => /^    ports:/m.test(block)).map(([, name]) => name).sort();
assert(JSON.stringify(published) === JSON.stringify(['mqtt-broker', 'nginx']), `only nginx and mqtt-broker may publish host ports, got: ${published.join(', ')}`);
assert(compose.includes('"${HTTPS_PORT:-443}:443"'), 'Nginx must publish HTTPS 443 by default');
assert(compose.includes('"${MQTT_TLS_PORT:-8883}:8883"'), 'MQTT must publish TLS 8883 by default');
assert(compose.includes('max-size: ${DOCKER_LOG_MAX_SIZE:-20m}') && compose.includes('max-file: "${DOCKER_LOG_MAX_FILES:-5}"'), 'canonical containers must use bounded Docker log rotation');
for (const marker of ['POSTGRES_DATA_DIR', 'CLICKHOUSE_DATA_DIR', 'REDIS_DATA_DIR', 'MQTT_DATA_DIR']) {
  assert(compose.includes(marker), `canonical Compose must expose configurable host data path ${marker}`);
}
for (const [service, port] of [['energy-api', '19080'], ['scheduler', '19092'], ['maintenance', '19093'], ['telemetry-worker', '19086'], ['metric-worker', '19090'], ['iot-service', '19094']]) {
  const block = serviceBlocks.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes(`/healthcheck", "http://127.0.0.1:${port}/health/ready`), `${service} must have a Compose readiness healthcheck`);
  assert(block.includes('cpus:') && block.includes('mem_limit:') && block.includes('mem_reservation:'), `${service} must have CPU/memory limits and reservations`);
}
assert(runtimeSources.observabilityRuntime.includes('SetDependencies') && runtimeSources.observabilityRuntime.includes('probeDependencies') && runtimeSources.observabilityRuntime.includes('Required bool') && runtimeSources.observabilityRuntime.includes('status.Required'), 'shared readiness runtime must evaluate live required dependency callbacks');
assert(runtimeSources.gatewayMain.includes('telemetry.SetDependencies(observability.Dependency{Name: "postgres-session"') && runtimeSources.gatewayMain.includes('Check: identity.ReadinessCheck'), 'energy-api readiness must track Identity Redis and Session persistence');
assert(runtimeSources.identityMain.includes('telemetry.SetDependencies(observability.Dependency{Name: "postgres"') && runtimeSources.identityMain.includes('Check: server.Ping'), 'identity-service readiness must track PostgreSQL');
assert(runtimeSources.schedulerMain.includes('telemetry.SetDependencies(observability.Dependency{Name: "postgres"') && runtimeSources.schedulerMain.includes('Check: store.Ping'), 'scheduler readiness must track PostgreSQL');
assert(runtimeSources.maintenanceMain.includes('telemetry.SetDependencies(observability.Dependency{Name: "postgres"') && runtimeSources.maintenanceMain.includes('Check: store.Ping'), 'maintenance supporting worker readiness must track PostgreSQL');
assert(runtimeSources.telemetryRuntimeMain.includes('observabilityRuntime.SetDependencies'), 'telemetry-worker readiness must track its required stores through live dependencies');
assert(runtimeSources.metricMain.includes('telemetry.SetDependencies(observability.Dependency{Name: "metric-runtime"') && runtimeSources.metricMain.includes('Check: runtime.Ping'), 'metric-worker readiness must track its required stores');
assert(runtimeSources.iotRuntime.includes('runtime.connected && runtime.subscribed && runtime.lastError == ""'), 'iot-service readiness must track live MQTT connection/subscription state');
assert(!runtimeSources.operationsGateway.includes('rateWindows') && !runtimeSources.operationsGateway.includes('RateLimitPerMinute') && runtimeSources.operationsGateway.includes('rateLimiter.Allow'), 'Operations Agent high-risk limit must not use an in-process authoritative map');

for (const dataService of ['postgres', 'clickhouse', 'redis']) {
  const block = serviceBlocks.find(([, name]) => name === dataService)?.[2] ?? '';
  assert(!/^    ports:/m.test(block), `${dataService} must not publish a host port`);
  assert(block.includes('networks: [data]'), `${dataService} must remain in the Data Zone network`);
}

for (const forbiddenService of ['redpanda-compat', 'outbox-relay-compat', 'platform-gateway', 'iam-service', 'platform-core-service', 'telemetry-query-service', 'audit-ledger-service', 'alarm-service', 'work-order-service', 'command-service', 'mqtt-telemetry-adapter', 'command-dispatcher', 'command-verifier', 'telemetry-history-projector', 'analytics-read-model-projector', 'telemetry-runtime-service']) {
  assert(!new RegExp(`^  ${forbiddenService}:`, 'm').test(compose), `canonical Compose must not deploy legacy/compat service ${forbiddenService}`);
}

assert(nginx.includes('listen 443 ssl;'), 'Nginx must terminate HTTPS on 443');
assert(!nginx.includes('listen 80'), 'Phase 1 Nginx must not expose a plaintext HTTP listener');
assert(nginx.includes('location /api/'), 'Nginx must proxy the API');
assert(nginx.includes('resolver 127.0.0.11'), 'Nginx must use Docker DNS for dynamic service discovery');
assert(nginx.includes('server energy-api:8080 resolve;'), 'Nginx gateway upstream must dynamically resolve energy-api');
assert(nginx.includes('proxy_pass http://gateway_backend;'), 'Nginx API upstream must use gateway_backend');
assert(nginx.includes('server identity-service:19095 resolve;'), 'Nginx identity upstream must dynamically resolve identity-service');
assert(nginx.includes('proxy_pass http://identity_backend/;'), 'Nginx /identity upstream must use identity_backend');
assert(nginx.includes('location /realtime/'), 'Nginx must proxy realtime WebSocket traffic through energy-api');
assert(!nginx.includes('location /connection/'), 'Nginx must not expose the internal Centrifugo connection path');
assert(!nginx.includes('proxy_pass http://centrifugo:8000;'), 'Nginx must not proxy browsers directly to Centrifugo');
assert(nginx.includes('try_files $uri $uri/ /index.html;'), 'Nginx must serve the React SPA fallback');
assert(webDockerfile.includes('npm run build:real'), 'production web image must build the Real artifact');
assert(!webDockerfile.includes('npm run dev'), 'production web image must never run a Vite development server');

const environmentObservability = {
  development: { tier: 'single-lite', profile: 'observability-logs', collectorRequired: true },
  testing: { tier: 'demo', profile: 'observability-core', collectorRequired: false },
  staging: { tier: 'single-lite', profile: 'observability-logs', collectorRequired: true },
  production: { tier: 'single-lite', profile: 'observability-logs', collectorRequired: true },
};
for (const [name, content] of Object.entries(envs)) {
  assert(content.includes(`HVAC_ENV=${name}`), `${name} environment contract must identify itself`);
  const obs = environmentObservability[name];
  const runtime = parseRuntimeEnvironment(content);
  assert(runtime.PHASE1_DEPLOYMENT_TIER === obs.tier, `${name} must select deployment tier ${obs.tier}`);
  assert(content.includes(`PHASE1_OBSERVABILITY_PROFILE=${obs.profile}`), `${name} must declare observability profile ${obs.profile}`);
  try {
    const resolved = resolveDeploymentTier({ contract: deploymentTiers, compose, tierId: runtime.PHASE1_DEPLOYMENT_TIER, environment: name });
    assert(resolved.profiles[0] === obs.profile, `${name} deployment tier and observability profile must agree`);
  } catch (error) {
    assert(false, error.message);
  }
  if (obs.collectorRequired) {
    assert(content.includes('OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318'), `${name} must send telemetry to the Phase 1 collector`);
  } else {
    assert(content.includes('OTEL_EXPORTER_OTLP_ENDPOINT=') && !content.includes('OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318'), `${name} must not export telemetry when the collector profile is not selected`);
  }
  assert(content.includes('MQTT_BROKER_URL=mqtts://mqtt-broker:8883'), `${name} must use MQTT TLS`);
  assert(content.includes('TELEMETRY_LATEST_CACHE_REDIS_URL=redis://redis:6379/0') && content.includes('TELEMETRY_LIMIT_POLICY_REDIS_URL=redis://redis:6379/2'), `${name} must keep telemetry projection and shared limit Redis contracts explicit`);
  assert(content.includes('HVAC_WEB_REALTIME_PROTOCOL=centrifugo-v1'), `${name} must use the supported realtime protocol contract`);
  assert(content.includes('/realtime/websocket'), `${name} realtime endpoint must enter through energy-api /realtime`);
  assert(content.includes('SCHEDULER_POSTGRES_DSN=[REDACTED_SECRET]'), `${name} must provide the Scheduler PostgreSQL authority contract`);
  assert(content.includes('SCHEDULER_SCAN_INTERVAL=2s') && content.includes('SCHEDULER_SCAN_BATCH=100'), `${name} must expose bounded Scheduler scan parameters`);
  assert(content.includes('METRIC_JOB_LEASE_DURATION=60s'), `${name} must configure Metric durable Job lease execution`);
  assert(!content.includes('METRIC_WORKER_BINDINGS_JSON=') && !content.includes('METRIC_WORKER_POLL_INTERVAL='), `${name} must not restore Metric self-scheduling authority`);
  assert(content.includes('[REDACTED_SECRET]'), `${name} example must redact runtime secrets`);
}
for (const name of ['development', 'testing', 'staging']) {
  assert(envs[name].includes('ALLOW_PRODUCTION_EGRESS=false'), `${name} must fail closed for production egress`);
  assert(!envs[name].includes('hvac/production'), `${name} must not use the production MQTT namespace`);
}
const releaseImageVariables = [
  'HVAC_WEB_IMAGE',
  'ENERGY_API_IMAGE',
  'SCHEDULER_IMAGE',
  'IOT_SERVICE_IMAGE',
  'TELEMETRY_WORKER_IMAGE',
  'METRIC_WORKER_IMAGE',
  'PHASE1_MIGRATOR_IMAGE',
];
for (const name of ['staging', 'production']) {
  for (const variable of releaseImageVariables) {
    const line = envs[name].split(/\r?\n/).find((value) => value.startsWith(`${variable}=`)) ?? '';
    assert(line.includes('@sha256:'), `${name} must bind ${variable} to an immutable digest`);
    assert(!line.includes(':0.1.0-dev'), `${name} must not use the local dev image for ${variable}`);
  }
}
for (const forbidden of ['localhost', '127.0.0.1', 'demo', 'fixture', 'oidc-test-provider']) {
  assert(!envs.production.toLowerCase().includes(forbidden), `production environment must not contain ${forbidden}`);
}
assert(envs.production.includes('PUBLIC_ORIGIN=https://app.energy.example.com'), 'production must have an explicit HTTPS public origin contract');
assert(envs.production.includes('MQTT_TOPIC_ROOT=hvac/production'), 'production must use its own MQTT namespace');

assert(prometheus.includes('energy-api:19080') && prometheus.includes('scheduler:19092') && prometheus.includes('telemetry-worker:19086') && prometheus.includes('metric-worker:19090') && prometheus.includes('iot-service:19094'), 'Prometheus must scrape the canonical Phase 1 Go processes including Scheduler coordination');
assert(prometheus.includes('node-exporter:9100'), 'Prometheus must scrape single-server host metrics');
assert(hostAlerts.includes('Phase1HostDiskUsageWarning') && hostAlerts.includes('> 80') && hostAlerts.includes('Phase1HostDiskUsageCritical') && hostAlerts.includes('> 90'), 'host disk alerts must enforce 80% warning and 90% critical thresholds');
assert(otel.includes('filelog/docker') && otel.includes('otlphttp/loki') && otel.includes('otlp/tempo'), 'observability-full OTel Collector must route logs and traces to central backends');
assert(otelLogs.includes('filelog/docker') && otelLogs.includes('otlphttp/loki') && !otelLogs.includes('otlp/tempo'), 'observability-logs OTel Collector must collect logs without requiring Tempo');
assert(!prometheus.includes('otel-collector:9464'), 'Prometheus must scrape Go diagnostics endpoints directly instead of depending on the collector');
assert(loki.includes('replication_factor: 1'), 'Phase 1 logging backend must stay single-instance rather than introducing a cluster');
assert(tempo.includes('backend: local'), 'Phase 1 trace backend must use local single-instance storage');
assert(grafanaDatasources.includes('Prometheus') && grafanaDatasources.includes('Loki') && grafanaDatasources.includes('Tempo'), 'Grafana must provision metrics, logs and traces datasources');
assert(grafanaDashboards.includes('/var/lib/grafana/dashboards'), 'Grafana must provision checked-in dashboards');

assert(compose.includes('archive_mode=on') && compose.includes('archive_command=test ! -f /var/lib/postgresql/wal-archive/%f'), 'PostgreSQL must continuously archive WAL');
assert(compose.includes('profiles: ["backup"]') && compose.includes('postgres-backup:') && compose.includes('clickhouse-backup:'), 'backup operations must be explicit non-default Compose profiles');
assert(postgresBackup.includes('pg_basebackup') && postgresBackup.includes('--wal-method=stream') && postgresBackup.includes('SHA256SUMS'), 'PostgreSQL base backup must include streamed WAL and checksums');
assert(postgresBackup.includes('sha256sum ./* > SHA256SUMS'), 'PostgreSQL base backup checksum manifest must use restore-host-verifiable relative paths');
assert(backupReadiness.includes('POSTGRES_RPO_TARGET_SECONDS:-300') && backupReadiness.includes('sha256sum --check SHA256SUMS'), 'backup readiness must verify the 5-minute WAL threshold and newest base-backup checksums');
assert(clickhouseBackup.includes('BACKUP DATABASE telemetry_history') && clickhouseBackup.includes("Disk('phase1_backups'"), 'ClickHouse must use native backup to the configured backup disk');
assert(clickhouseBackupConfig.includes('/var/lib/clickhouse/backups/'), 'ClickHouse backup disk must be explicitly configured');
assert(backupReadme.includes('Backup configuration and readiness checks are not RPO/RTO attestation'), 'backup documentation must not overclaim RPO/RTO attainment');
assert(recoveryTargets.sourceOfTruth === 'SE-OPS-009 V1.0 CURRENT CANDIDATE', 'recovery target contract must be governed by SE-OPS-009 V1');
assert(recoveryTargets.targets?.postgresqlBusinessSoT?.rpoSeconds === 300 && recoveryTargets.targets?.postgresqlBusinessSoT?.rtoSeconds === 7200, 'PostgreSQL recovery target must remain RPO<=5min/RTO<=2h');
assert(recoveryTargets.targets?.controlCommandAudit?.rpoSeconds === 300 && recoveryTargets.targets?.controlCommandAudit?.rtoSeconds === 3600, 'Control recovery target must remain RPO<=5min/RTO<=1h');
assert(recoveryTargets.targets?.telemetry?.cloudServiceRtoSeconds === 14400 && recoveryTargets.targets?.metricSeries?.rpoSeconds === 1800 && recoveryTargets.targets?.metricSeries?.rtoSeconds === 14400, 'Telemetry/Metric recovery targets must remain aligned to SE-OPS-009');
assert(recoveryTargets.failureScenarioRtoSeconds?.processOrContainerFailure === 300 && recoveryTargets.failureScenarioRtoSeconds?.databaseProcessFailure === 1800 && recoveryTargets.failureScenarioRtoSeconds?.serverOsRecoverableFailure === 3600 && recoveryTargets.failureScenarioRtoSeconds?.wholeServerReplacement === 14400, 'failure-scenario RTO targets must remain 5m/30m/1h/4h');
assert(recoveryTargets.backupPolicy?.offServerCopyRequired === true && recoveryTargets.productionAttestation?.timestampedRestoreDrillRequired === true && recoveryTargets.productionAttestation?.containerOnlyEvidenceSufficient === false, 'recovery contract must require off-server backup and real timestamped drill evidence');
assert(recoveryReadme.includes('RTO Start') && recoveryReadme.includes('RTO End') && recoveryReadme.includes('Control recovery') && recoveryReadme.includes('Scheduler and Outbox recovery') && recoveryReadme.includes('Edge Replay'), 'recovery runbook must preserve the frozen recovery semantics');
assert(recoveryDrillTemplate.prerequisites?.externalBackupVerified === false && recoveryDrillTemplate.controlRecovery?.blindRetryUsed === false, 'drill template must begin unverified and must prohibit blind Control retry');
assert(gitignore.includes('deploy/platform/phase1/environments/*.runtime.env'), 'real Phase 1 runtime env files must be ignored by Git');
assert(gitignore.includes('deploy/platform/phase1/runtime/'), 'Phase 1 runtime PKI/backup material must be ignored by Git');

for (const database of ['hvac_identity', 'hvac_s0', 'hvac_s1', 'hvac_s2', 'hvac_s3', 'hvac_s4', 'hvac_s5']) {
  assert(phase1Databases.includes(`CREATE DATABASE ${database}`), `Phase 1 PostgreSQL bootstrap must preserve existing database boundary ${database}`);
}
assert(compose.includes('POSTGRES_DB: ${POSTGRES_DB:-hvac_s0}'), 'Phase 1 PostgreSQL primary database must preserve the hvac_s0 boundary');
assert(!compose.includes('000-bootstrap-identities.sql') && !compose.includes('testdata/postgres/000_roles.sql'), 'Phase 1 Production topology must not mount local/test role bootstrap SQL');

const manifestEntries = (migrationManifest.databases ?? []).flatMap((database) => (database.migrations ?? []).map((migration) => `${database.name}|${migration}`));
const listEntries = migrationList.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
assert(migrationManifest.schemaVersion === 1, 'migration manifest schemaVersion must be 1');
assert(migrationManifest.policy?.fixturesAllowed === false && migrationManifest.policy?.testdataAllowed === false, 'production migration policy must forbid fixture/testdata sources');
assert(migrationManifest.policy?.localPasswordStatementsAllowed === false, 'production migration policy must forbid local password statements');
assert(manifestEntries.length === 76, `production migration allowlist must contain exactly 76 migrations, got ${manifestEntries.length}`);
assert(JSON.stringify(manifestEntries) === JSON.stringify(listEntries), 'migration-list.tsv must exactly match the JSON allowlist and order');
for (const entry of manifestEntries) {
  const [, sourcePath] = entry.split('|');
  assert(!/(fixture|testdata|bootstrap|legacy-migration)/i.test(sourcePath), `forbidden production migration source: ${sourcePath}`);
  assert(migrationDockerfile.includes(`COPY ${sourcePath} `), `migration image does not copy allowlisted source: ${sourcePath}`);
  const source = await read(sourcePath);
  assert(!/local-only|local-fixture-only|local-mutation-fixture-only/i.test(source), `production migration source contains local/test credential material: ${sourcePath}`);
}
assert(migrationDockerfile.includes("! grep -R -E 'local-only|fixture-only' /opt/hvac/repo"), 'migration image must fail if local/test credential markers survive the build');
assert(!migrationDockerfile.includes('sed -i'), 'migration image must not rewrite reviewed SQL bytes');
assert(!migrationRunner.includes('sanitize_sql'), 'migration runner must execute reviewed SQL without runtime sanitization');
assert(migrationRunner.includes('phase1_deployment.schema_migrations'), 'migration runner must record migration identity and status');
assert(migrationRunner.includes("'APPLYING'") && migrationRunner.includes("'APPLIED'") && migrationRunner.includes("'FAILED'"), 'migration runner must preserve explicit incomplete/failure states');
assert(migrationRunner.includes('phase1_deployment.product_schema'), 'migration runner must publish Product/Schema compatibility state only after migration completion');
assert(migrationRunner.includes('migration drift detected'), 'migration runner must fail closed on migration drift');
assert(schemaPreflight.includes('phase1_deployment.product_schema') && schemaPreflight.includes('schema manifest mismatch'), 'schema preflight must verify exact persisted Product/Schema state');
assert(!schemaPreflight.includes('SKIP_SCHEMA_VERSION_CHECK'), 'schema preflight must not expose a skip switch');
assert(compose.includes('phase1-migrator:') && compose.includes('profiles: ["migration"]'), 'production-safe migrator must be an explicit non-default migration profile');
assert(compose.includes('PHASE1_DB_ROLE_CREDENTIALS_FILE: /run/hvac/db-role-credentials/roles.sql'), 'migrator must consume runtime role credential material from a mounted file');
for (const role of migrationManifest.loginRoles ?? []) {
  assert(roleCredentialTemplate.includes(`ALTER ROLE ${role} WITH PASSWORD '[REDACTED_SECRET]';`), `role credential contract is missing ${role}`);
}
assert(!roleCredentialTemplate.includes('local-only') && !roleCredentialTemplate.includes('fixture-only'), 'role credential contract must not reuse historical local/test credentials');
assert(packageJson.includes('"deployment:phase1:migration:test": "node scripts/run-phase1-migration-integration.mjs"'), 'production migration integration must have a stable package entrypoint');
assert(packageJson.includes('"deployment:phase1:recovery:verify": "node scripts/verify-phase1-recovery-drill.mjs"'), 'recovery drill verifier must have a stable manual entrypoint');
assert(phase1Readme.includes('exact 76-file allowlist') && phase1Readme.includes('without runtime rewriting'), 'Phase 1 README must document the reviewed production-safe migration allowlist');
assert(phase1Readme.includes('Deployment tiers and observability profiles') && phase1Readme.includes('observability-core') && phase1Readme.includes('observability-logs') && phase1Readme.includes('observability-full') && phase1Readme.includes('intelligence'), 'Phase 1 README must document tier profiles');
assert(phase1Readme.includes('Availability and recovery evidence') && phase1Readme.includes('SINGLE_NODE_RECOVERABLE'), 'Phase 1 README must document the availability tier');

const byId = new Map((matrix.items ?? []).map((item) => [item.id, item]));
for (const id of ['DEPLOY-K8S-001', 'MQTT-HA-001', 'POSTGRES-HA-001', 'CLICKHOUSE-HA-001']) {
  assert(byId.get(id)?.status === 'DEFER', `${id} must remain deferred for Phase 1`);
}
assert(byId.get('KAFKA-001')?.status === 'REMOVE', 'Kafka/Redpanda must remain removed from the canonical Phase 1 Compose');
assert(byId.get('DEPLOY-MIGRATION-001')?.status === 'KEEP', 'production-safe unified migration must remain a canonical Phase 1 capability');
for (const id of ['REALTIME-001', 'HEALTH-001', 'RESOURCE-001', 'HOST-MONITOR-001', 'DATA-DIR-001', 'SCHEDULER-001']) {
  assert(byId.get(id)?.status === 'KEEP', `${id} must remain aligned to the single-server deployment baseline`);
}
assert(byId.get('RPO-RTO-001')?.status === 'KEEP', 'RPO/RTO target definition and recovery evidence mechanism must remain implemented');
assert(byId.get('OPTIMIZATION-001')?.status === 'DEFER', 'Optimization must remain optional/deferred until the deployment needs it');
for (const id of ['DEPLOY-TIER-001', 'OBS-PROFILE-001', 'AVAILABILITY-001', 'RECOVERY-ATTESTATION-001']) {
  assert(byId.get(id)?.status === 'KEEP', `${id} must remain a canonical deployment contract`);
}

if (failures.length > 0) {
  console.error('Phase 1 deployment check failed:\n' + failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`Phase 1 deployment check passed: services=${requiredServices.length}, publicPorts=443/8883, environments=${Object.keys(envs).length}, profiles=${Object.keys(profileExpectations).length + 1}`);
