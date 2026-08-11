import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

const baselinePath = 'deploy/platform/phase1/architecture-baseline.v1.json';
const matrixPath = 'deploy/platform/phase1/alignment-matrix.v1.json';
const overallPath = 'docs/architecture/phase1-overall-architecture.md';
const operationsPath = 'docs/operations/phase1-deployment-alignment.md';
const authorityPath = '架构规划/智慧能源系统部署与运维架构设计.md';

const [baseline, matrix, overall, operations, authority] = await Promise.all([
  readJSON(baselinePath),
  readJSON(matrixPath),
  readText(overallPath),
  readText(operationsPath),
  readText(authorityPath),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(authority.includes('第一阶段不建议直接 Kubernetes'), 'source deployment design no longer contains the Phase 1 Kubernetes boundary');
assert(authority.includes('Development') && authority.includes('Testing') && authority.includes('Staging') && authority.includes('Production'), 'source deployment design no longer defines four environments');
assert(authority.includes('Edge') && authority.includes('Central Platform') && authority.includes('Data Platform') && authority.includes('Monitoring Platform'), 'source deployment design no longer defines the four top-level deployment layers');

assert(baseline.schemaVersion === 1, 'Phase 1 architecture baseline schemaVersion must be 1');
assert(baseline.sourceOfTruth === authorityPath, 'Phase 1 architecture baseline must cite the deployment design as source of truth');
assert(baseline.deploymentModel?.host === 'linux-server', 'Phase 1 deployment host must be Linux Server');
assert(baseline.deploymentModel?.orchestration === 'docker-compose', 'Phase 1 canonical orchestrator must be Docker Compose');
assert(baseline.deploymentModel?.kubernetesRequired === false, 'Kubernetes must not be a Phase 1 requirement');

const expectedEnvironments = ['development', 'testing', 'staging', 'production'];
assert(JSON.stringify(baseline.environments) === JSON.stringify(expectedEnvironments), 'Phase 1 environments must be Development/Testing/Staging/Production in order');

for (const isolation of ['database', 'redis', 'mqtt', 'storage', 'secrets', 'domain', 'configuration']) {
  assert(baseline.environmentIsolation?.includes(isolation), `environment isolation is missing ${isolation}`);
}

for (const layer of ['edge', 'central-platform', 'data-platform', 'monitoring-platform']) {
  assert(baseline.logicalLayers?.includes(layer), `logical layer is missing ${layer}`);
}

assert(JSON.stringify(baseline.publicZone?.allowedPorts) === JSON.stringify([443, 8883]), 'Phase 1 public ports must be only 443 and 8883');
assert(baseline.publicZone?.databasePublicExposureAllowed === false, 'Phase 1 must forbid public database exposure');
assert(baseline.publicZone?.internalServicePublicExposureAllowed === false, 'Phase 1 must forbid public internal-service exposure');
assert(baseline.dataZone?.redis === 'rebuildable-cache-and-realtime-transport', 'Redis must remain a rebuildable cache/transport, not an authority');
assert(baseline.edgeZone?.cloudDirectOTAccessAllowed === false, 'Cloud direct OT access must remain forbidden');

for (const deferred of [
  'kubernetes-as-primary-orchestrator',
  'postgresql-replica-ha',
  'clickhouse-replica-ha',
  'mqtt-cluster',
  'kafka-or-redpanda-as-required-platform-backbone',
  'multi-region',
]) {
  assert(baseline.phase1DeferredCapabilities?.includes(deferred), `Phase 1 deferred capability is missing ${deferred}`);
}

const allowedStatuses = new Set(matrix.statuses ?? []);
assert(matrix.schemaVersion === 1, 'alignment matrix schemaVersion must be 1');
assert(matrix.sourceOfTruth === authorityPath, 'alignment matrix must cite the deployment design as source of truth');
assert(Array.isArray(matrix.items) && matrix.items.length >= 25, 'alignment matrix must cover at least 25 deployment concerns');
assert(new Set(matrix.items.map((item) => item.id)).size === matrix.items.length, 'alignment matrix IDs must be unique');
for (const item of matrix.items ?? []) {
  assert(allowedStatuses.has(item.status), `alignment matrix item ${item.id} has unsupported status ${item.status}`);
  assert(String(item.requirement ?? '').trim(), `alignment matrix item ${item.id} is missing requirement`);
  assert(String(item.action ?? '').trim(), `alignment matrix item ${item.id} is missing action`);
}

const byId = new Map((matrix.items ?? []).map((item) => [item.id, item]));
assert(byId.get('DEPLOY-PHASE1-001')?.status === 'SIMPLIFY', 'Phase 1 orchestration must explicitly simplify to Docker Compose');
assert(byId.get('DEPLOY-K8S-001')?.status === 'DEFER', 'Kubernetes must be explicitly deferred');
assert(byId.get('MQTT-HA-001')?.status === 'DEFER', 'MQTT cluster must be explicitly deferred');
assert(byId.get('POSTGRES-HA-001')?.status === 'DEFER', 'PostgreSQL HA must be explicitly deferred');
assert(byId.get('KAFKA-001')?.status === 'SIMPLIFY', 'Kafka/Redpanda must not be a Phase 1 platform requirement');
assert(byId.get('DOC-CONSISTENCY-001')?.status === 'KEEP', 'document-scope consistency must remain a canonical architecture control');
assert(byId.get('OPTIMIZATION-001')?.status === 'MISSING', 'Missing Optimization runtime must remain visible instead of being faked');

for (const marker of [
  'Linux Server(s)',
  'Docker Compose',
  'Cloud 不直接访问 PLC',
  'PostgreSQL',
  'ClickHouse',
  'Redis',
  'MQTT',
  'Development',
  'Testing',
  'Staging',
  'Production',
]) assert(overall.includes(marker), `overall architecture document is missing marker: ${marker}`);

assert(operations.includes('MISSING') && operations.includes('SIMPLIFY') && operations.includes('DEFER'), 'deployment alignment runbook must define gap handling states');

if (failures.length > 0) {
  console.error('Phase 1 architecture alignment check failed:\n' + failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

const statusCounts = Object.fromEntries([...allowedStatuses].map((status) => [status, matrix.items.filter((item) => item.status === status).length]));
console.log(`Phase 1 architecture alignment passed: items=${matrix.items.length}; ${Object.entries(statusCounts).map(([status, count]) => `${status}=${count}`).join(', ')}`);
