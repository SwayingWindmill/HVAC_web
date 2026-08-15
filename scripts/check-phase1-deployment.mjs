import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await read(path));

const paths = {
  baseline: 'deploy/platform/phase1/architecture-baseline.v1.json',
  matrix: 'deploy/platform/phase1/alignment-matrix.v1.json',
  compose: 'deploy/platform/phase1/compose.yaml',
  nginx: 'deploy/platform/phase1/nginx/nginx.conf',
  webDockerfile: 'deploy/platform/phase1/nginx/Dockerfile',
  prometheus: 'deploy/platform/phase1/observability/prometheus.yaml',
  hostAlerts: 'infra/s0-durable/observability/alerts/host-resource.yaml',
  otel: 'deploy/platform/phase1/observability/otel-collector.yaml',
  loki: 'deploy/platform/phase1/observability/loki.yaml',
  tempo: 'deploy/platform/phase1/observability/tempo.yaml',
  grafanaDatasources: 'deploy/platform/phase1/observability/grafana/provisioning/datasources/datasources.yaml',
  grafanaDashboards: 'deploy/platform/phase1/observability/grafana/provisioning/dashboards/dashboards.yaml',
  postgresBackup: 'deploy/platform/phase1/backup/postgres-base-backup.sh',
  backupReadiness: 'deploy/platform/phase1/backup/verify-recovery-readiness.sh',
  clickhouseBackup: 'deploy/platform/phase1/backup/clickhouse-backup.sh',
  clickhouseBackupConfig: 'deploy/platform/phase1/backup/clickhouse-backup.xml',
  backupReadme: 'deploy/platform/phase1/backup/README.md',
  recoveryTargets: 'deploy/platform/phase1/recovery/recovery-targets.v1.json',
  recoveryReadme: 'deploy/platform/phase1/recovery/README.md',
  recoveryDrillTemplate: 'deploy/platform/phase1/recovery/drill-record.template.json',
  gitignore: '.gitignore',
  phase1Databases: 'deploy/platform/phase1/postgres/init/001-create-phase1-databases.sql',
  phase1Readme: 'deploy/platform/phase1/README.md',
  migrationManifest: 'deploy/platform/phase1/migrations/manifest.v1.json',
  migrationList: 'deploy/platform/phase1/migrations/migration-list.tsv',
  migrationDockerfile: 'deploy/platform/phase1/migrations/Dockerfile',
  migrationRunner: 'deploy/platform/phase1/migrations/run-phase1-migrations.sh',
  roleCredentialTemplate: 'deploy/platform/phase1/migrations/role-credentials.sql.example',
  packageJson: 'package.json',
};

const environmentFiles = {
  development: 'deploy/platform/phase1/environments/development.runtime.env.example',
  testing: 'deploy/platform/phase1/environments/testing.runtime.env.example',
  staging: 'deploy/platform/phase1/environments/staging.runtime.env.example',
  production: 'deploy/platform/phase1/environments/production.runtime.env.example',
};

const [baseline, matrix, compose, nginx, webDockerfile, prometheus, hostAlerts, otel, loki, tempo, grafanaDatasources, grafanaDashboards, postgresBackup, backupReadiness, clickhouseBackup, clickhouseBackupConfig, backupReadme, recoveryTargets, recoveryReadme, recoveryDrillTemplate, gitignore, phase1Databases, phase1Readme, migrationManifest, migrationList, migrationDockerfile, migrationRunner, roleCredentialTemplate, packageJson] = await Promise.all([
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
  read(paths.roleCredentialTemplate),
  read(paths.packageJson),
]);
const envs = Object.fromEntries(await Promise.all(Object.entries(environmentFiles).map(async ([name, path]) => [name, await read(path)])));

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(baseline.deploymentModel?.orchestration === 'docker-compose', 'canonical deployment must remain Docker Compose');
assert(baseline.deploymentModel?.kubernetesRequired === false, 'Kubernetes must not become a Phase 1 requirement');
assert(baseline.deploymentModel?.singleServerRequired === true, 'Phase 1 must require the single-server deployment baseline');
assert(baseline.deploymentModel?.fewServersAllowed === false, 'multi-server deployment must remain outside the current Phase 1 baseline');
assert(baseline.recovery?.sourceOfTruth === 'SE-OPS-009 V1.0 CURRENT CANDIDATE' && baseline.recovery?.rpoRtoTargetsDefined === true, 'Phase 1 baseline must freeze SE-OPS-009 recovery targets');
assert(baseline.recovery?.postgresRpoSeconds === 300 && baseline.recovery?.postgresRtoSeconds === 7200 && baseline.recovery?.controlRtoSeconds === 3600, 'Phase 1 baseline must preserve PostgreSQL/Control recovery objectives');
assert(baseline.recovery?.offServerBackupCopyRequired === true && baseline.recovery?.wholeServerFourHourTargetRequiresColdStandby === true, 'whole-server recovery target must preserve off-server backup and cold-standby prerequisites');

const requiredServices = [
  'nginx',
  'energy-api',
  'scheduler',
  'iot-service',
  'telemetry-worker',
  'metric-worker',
  'postgres',
  'clickhouse',
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
for (const service of requiredServices) assert(new RegExp(`^  ${service}:`, 'm').test(compose), `compose is missing required service ${service}`);

for (const network of ['application', 'data', 'mqtt', 'observability']) {
  assert(new RegExp(`^  ${network}:\\n    internal: true`, 'm').test(compose), `compose network ${network} must be internal`);
}
assert(/^  public: \{\}/m.test(compose), 'compose must have an explicit public network');

const serviceBlocks = [...compose.matchAll(/^  ([a-z0-9-]+):\n([\s\S]*?)(?=^  [a-z0-9-]+:\n|^networks:|^volumes:)/gm)];
const published = serviceBlocks.filter(([, , block]) => /^    ports:/m.test(block)).map(([, name]) => name).sort();
assert(JSON.stringify(published) === JSON.stringify(['mqtt-broker', 'nginx']), `only nginx and mqtt-broker may publish host ports, got: ${published.join(', ')}`);
assert(compose.includes('"${HTTPS_PORT:-443}:443"'), 'Nginx must publish HTTPS 443 by default');
assert(compose.includes('"${MQTT_TLS_PORT:-8883}:8883"'), 'MQTT must publish TLS 8883 by default');
assert(compose.includes('max-size: ${DOCKER_LOG_MAX_SIZE:-20m}') && compose.includes('max-file: "${DOCKER_LOG_MAX_FILES:-5}"'), 'canonical containers must use bounded Docker log rotation');
for (const marker of ['POSTGRES_DATA_DIR', 'CLICKHOUSE_DATA_DIR', 'REDIS_DATA_DIR', 'MQTT_DATA_DIR']) {
  assert(compose.includes(marker), `canonical Compose must expose configurable host data path ${marker}`);
}
for (const [service, port] of [['energy-api', '19080'], ['scheduler', '19092'], ['telemetry-worker', '19086'], ['metric-worker', '19090'], ['iot-service', '19094']]) {
  const block = serviceBlocks.find(([, name]) => name === service)?.[2] ?? '';
  assert(block.includes(`/healthcheck", "http://127.0.0.1:${port}/health/ready`), `${service} must have a Compose readiness healthcheck`);
  assert(block.includes('cpus:') && block.includes('mem_limit:') && block.includes('mem_reservation:'), `${service} must have CPU/memory limits and reservations`);
}

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
assert(nginx.includes('proxy_pass http://energy-api:8080;'), 'Nginx API upstream must be energy-api');
assert(nginx.includes('location /realtime/'), 'Nginx must proxy realtime WebSocket traffic through energy-api');
assert(!nginx.includes('location /connection/'), 'Nginx must not expose the internal Centrifugo connection path');
assert(!nginx.includes('proxy_pass http://centrifugo:8000;'), 'Nginx must not proxy browsers directly to Centrifugo');
assert(nginx.includes('try_files $uri $uri/ /index.html;'), 'Nginx must serve the React SPA fallback');
assert(webDockerfile.includes('npm run build:real'), 'production web image must build the Real artifact');
assert(!webDockerfile.includes('npm run dev'), 'production web image must never run a Vite development server');

for (const [name, content] of Object.entries(envs)) {
  assert(content.includes(`HVAC_ENV=${name}`), `${name} environment contract must identify itself`);
  assert(content.includes('OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318'), `${name} must send telemetry to the Phase 1 collector`);
  assert(content.includes('MQTT_BROKER_URL=mqtts://mqtt-broker:8883'), `${name} must use MQTT TLS`);
  assert(content.includes('TELEMETRY_LATEST_CACHE_REDIS_URL=redis://redis:6379/0'), `${name} must use the canonical Redis service for latest telemetry`);
  assert(content.includes('HVAC_WEB_REALTIME_PROTOCOL=centrifugo-v1'), `${name} must use the supported realtime protocol contract`);
  assert(content.includes('/realtime/websocket'), `${name} realtime endpoint must enter through energy-api /realtime`);
  assert(content.includes('SCHEDULER_POSTGRES_DSN=[REDACTED_SECRET]'), `${name} must provide the Scheduler PostgreSQL authority contract`);
  assert(content.includes('SCHEDULER_SCAN_INTERVAL=2s') && content.includes('SCHEDULER_SCAN_BATCH=100'), `${name} must expose bounded Scheduler scan parameters`);
  assert(content.includes('METRIC_WORKER_ID=metric-worker-1') && content.includes('METRIC_JOB_LEASE_DURATION=60s'), `${name} must configure Metric durable Job claim/lease execution`);
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
assert(otel.includes('filelog/docker') && otel.includes('otlphttp/loki') && otel.includes('otlp/tempo'), 'OTel Collector must route logs and traces to central backends');
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

for (const database of ['hvac_s0', 'hvac_s1', 'hvac_s2', 'hvac_s3', 'hvac_s4', 'hvac_s5']) {
  assert(phase1Databases.includes(`CREATE DATABASE ${database}`), `Phase 1 PostgreSQL bootstrap must preserve existing database boundary ${database}`);
}
assert(compose.includes('POSTGRES_DB: ${POSTGRES_DB:-hvac_s0}'), 'Phase 1 PostgreSQL primary database must preserve the hvac_s0 boundary');
assert(!compose.includes('000-bootstrap-identities.sql') && !compose.includes('testdata/postgres/000_roles.sql'), 'Phase 1 Production topology must not mount local/test role bootstrap SQL');

const manifestEntries = (migrationManifest.databases ?? []).flatMap((database) => (database.migrations ?? []).map((migration) => `${database.name}|${migration}`));
const listEntries = migrationList.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
assert(migrationManifest.schemaVersion === 1, 'migration manifest schemaVersion must be 1');
assert(migrationManifest.policy?.fixturesAllowed === false && migrationManifest.policy?.testdataAllowed === false, 'production migration policy must forbid fixture/testdata sources');
assert(migrationManifest.policy?.localPasswordStatementsAllowed === false, 'production migration policy must forbid local password statements');
assert(manifestEntries.length === 41, `production migration allowlist must contain exactly 41 migrations, got ${manifestEntries.length}`);
assert(JSON.stringify(manifestEntries) === JSON.stringify(listEntries), 'migration-list.tsv must exactly match the JSON allowlist and order');
for (const entry of manifestEntries) {
  const [, sourcePath] = entry.split('|');
  assert(!/(fixture|testdata|bootstrap|legacy-migration)/i.test(sourcePath), `forbidden production migration source: ${sourcePath}`);
  assert(migrationDockerfile.includes(`COPY ${sourcePath} `), `migration image does not copy allowlisted source: ${sourcePath}`);
}
assert(migrationDockerfile.includes("! grep -R -E 'local-only|fixture-only' /opt/hvac/repo"), 'migration image must fail if local/test credential markers survive the build');
assert(migrationRunner.includes('phase1_deployment.schema_migrations'), 'migration runner must record applied migration hashes');
assert(migrationRunner.includes('migration drift detected'), 'migration runner must fail closed on migration drift');
assert(migrationRunner.includes('006-s2-telemetry-authorization.sql') && migrationRunner.includes('007-s4-alarm-authorization.sql') && migrationRunner.includes('008-s5-work-order-authorization.sql'), 'migration runner must explicitly remove environment seed blocks from mixed authorization sources');
assert(compose.includes('phase1-migrator:') && compose.includes('profiles: ["migration"]'), 'production-safe migrator must be an explicit non-default migration profile');
assert(compose.includes('PHASE1_DB_ROLE_CREDENTIALS_FILE: /run/hvac/db-role-credentials/roles.sql'), 'migrator must consume runtime role credential material from a mounted file');
for (const role of migrationManifest.loginRoles ?? []) {
  assert(roleCredentialTemplate.includes(`ALTER ROLE ${role} WITH PASSWORD '[REDACTED_SECRET]';`), `role credential contract is missing ${role}`);
}
assert(!roleCredentialTemplate.includes('local-only') && !roleCredentialTemplate.includes('fixture-only'), 'role credential contract must not reuse historical local/test credentials');
assert(packageJson.includes('"deployment:phase1:migration:test": "node scripts/run-phase1-migration-integration.mjs"'), 'production migration integration must have a stable package entrypoint');
assert(packageJson.includes('"deployment:phase1:recovery:verify": "node scripts/verify-phase1-recovery-drill.mjs"'), 'recovery drill verifier must have a stable manual entrypoint');
assert(phase1Readme.includes('exact 41-file allowlist'), 'Phase 1 README must document the production-safe migration allowlist');

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

if (failures.length > 0) {
  console.error('Phase 1 deployment check failed:\n' + failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

console.log(`Phase 1 deployment check passed: services=${requiredServices.length}, publicPorts=443/8883, environments=${Object.keys(envs).length}`);
