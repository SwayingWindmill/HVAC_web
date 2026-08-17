import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const readText = (path) => readFile(resolve(root, path), 'utf8');
const readJSON = async (path) => JSON.parse(await readText(path));

const baselinePath = 'deploy/platform/phase1/architecture-baseline.v1.json';
const matrixPath = 'deploy/platform/phase1/alignment-matrix.v1.json';
const overallPath = 'docs/architecture/phase1-overall-architecture.md';
const operationsPath = 'docs/operations/phase1-deployment-alignment.md';
const edgeControlPlanePath = 'contracts/architecture/edge-control-plane.v1.json';
const authorityId = 'SE-ARCH-DEPLOY-001 V1.0 CURRENT';

const [baseline, matrix, overall, operations, edgeControlPlane] = await Promise.all([
  readJSON(baselinePath),
  readJSON(matrixPath),
  readText(overallPath),
  readText(operationsPath),
  readJSON(edgeControlPlanePath),
]);

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

assert(baseline.schemaVersion === 1, 'Phase 1 architecture baseline schemaVersion must be 1');
assert(baseline.sourceOfTruth === authorityId, 'Phase 1 architecture baseline must cite SE-ARCH-DEPLOY-001 V1.0 CURRENT');
assert(baseline.deploymentModel?.host === 'single-linux-server', 'Phase 1 deployment host must be one Linux Server');
assert(baseline.deploymentModel?.orchestration === 'docker-compose', 'Phase 1 canonical orchestrator must be Docker Compose');
assert(baseline.deploymentModel?.kubernetesRequired === false, 'Kubernetes must not be a Phase 1 requirement');
assert(baseline.deploymentModel?.singleServerRequired === true && baseline.deploymentModel?.fewServersAllowed === false, 'Phase 1 must remain a single-server baseline');

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
assert(baseline.edgeZone?.controlPlaneContract === edgeControlPlanePath, 'Phase 1 Edge zone must link the HVAC Edge Control Plane contract');
assert(baseline.edgeZone?.hardRealtime === false && baseline.edgeZone?.hardRealtimeOwner === 'plc-device-protection-safety-hardware', 'Edge soft-real-time / PLC hard-real-time safety boundary drifted');
assert(baseline.edgeZone?.cloudCommandSemantics === 'governed-leased-intent', 'Cloud commands must remain governed leased Edge intents');
for (const required of ['channel-runtime', 'process-image', 'input-process-output-cycle', 'controller-runtime', 'scheduler-control-arbiter', 'capability-profile-registry', 'device-driver-abstraction', 'protocol-bridge-abstraction', 'edge-manifest', 'edge-timedata', 'leased-cloud-command-intents']) {
  assert(baseline.edgeZone?.requiredProperties?.includes(required), `Phase 1 Edge foundation is missing ${required}`);
}

assert(edgeControlPlane.schemaVersion === 1 && edgeControlPlane.status === 'CURRENT TARGET', 'Edge Control Plane contract identity/status drifted');
assert(edgeControlPlane.decisions?.length === 50, 'OpenEMS/HVAC adjudication must contain exactly 50 decisions');
assert(new Set(edgeControlPlane.decisions.map(({ id }) => id)).size === 50, 'Edge Control Plane decision IDs must be unique');
const edgeVerdicts = new Set(['OPENEMS', 'HVAC', 'MERGE', 'REJECT']);
assert(edgeControlPlane.decisions.every(({ verdict }) => edgeVerdicts.has(verdict)), 'Edge Control Plane contains an unsupported verdict');
assert(edgeControlPlane.decisions.filter(({ verdict }) => verdict === 'OPENEMS').length === 29, 'OpenEMS adjudication count drifted');
assert(edgeControlPlane.decisions.filter(({ verdict }) => verdict === 'HVAC').length === 8, 'HVAC adjudication count drifted');
assert(edgeControlPlane.decisions.filter(({ verdict }) => verdict === 'MERGE').length === 12, 'MERGE adjudication count drifted');
assert(edgeControlPlane.decisions.filter(({ verdict }) => verdict === 'REJECT').length === 1, 'REJECT adjudication count drifted');

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
assert(matrix.sourceOfTruth === authorityId, 'alignment matrix must cite SE-ARCH-DEPLOY-001 V1.0 CURRENT');
assert(Array.isArray(matrix.items) && matrix.items.length >= 25, 'alignment matrix must cover at least 25 deployment concerns');
assert(new Set(matrix.items.map((item) => item.id)).size === matrix.items.length, 'alignment matrix IDs must be unique');
for (const item of matrix.items ?? []) {
  assert(allowedStatuses.has(item.status), `alignment matrix item ${item.id} has unsupported status ${item.status}`);
  assert(String(item.requirement ?? '').trim(), `alignment matrix item ${item.id} is missing requirement`);
  assert(String(item.action ?? '').trim(), `alignment matrix item ${item.id} is missing action`);
}

const byId = new Map((matrix.items ?? []).map((item) => [item.id, item]));
assert(byId.get('DEPLOY-PHASE1-001')?.status === 'KEEP', 'single-server Docker Compose must remain the canonical Phase 1 deployment');
assert(byId.get('DEPLOY-K8S-001')?.status === 'DEFER', 'Kubernetes must be explicitly deferred');
assert(byId.get('MQTT-HA-001')?.status === 'DEFER', 'MQTT cluster must be explicitly deferred');
assert(byId.get('POSTGRES-HA-001')?.status === 'DEFER', 'PostgreSQL HA must be explicitly deferred');
assert(byId.get('KAFKA-001')?.status === 'REMOVE', 'Kafka/Redpanda must stay out of the canonical Phase 1 deployment');
assert(byId.get('DOC-CONSISTENCY-001')?.status === 'KEEP', 'document-scope consistency must remain a canonical architecture control');
assert(byId.get('OPTIMIZATION-001')?.status === 'DEFER', 'Optimization must remain optional until the deployment needs it');
assert(byId.get('SCHEDULER-001')?.status === 'KEEP', 'unified Scheduler Coordination must remain implemented through the durable Job contract');
assert(byId.get('RPO-RTO-001')?.status === 'KEEP', 'RPO/RTO objectives and the recovery evidence mechanism must remain implemented');
assert(byId.get('EDGE-CONTROL-PLANE-001')?.status === 'MISSING', 'HVAC Edge Control Plane must remain an explicit gap until Process Image/Cycle/Controller/Scheduler are implemented');
assert(byId.get('EDGE-CAPABILITY-001')?.status === 'MISSING', 'Capability Profile / Driver / Bridge foundation must remain an explicit gap until implemented');
assert(byId.get('EDGE-TIMEDATA-001')?.status === 'MISSING', 'Edge Timedata must remain an explicit gap until local latest/history and priority resend are implemented');

for (const marker of [
  '1 Linux Server',
  'Docker Compose',
  'Realtime Module',
  'Cloud 不直接访问 PLC',
  'PostgreSQL',
  'ClickHouse',
  'Redis',
  'MQTT',
  'HVAC Edge Control Plane',
  'Process Image',
  'Capability Profile',
  'Edge Timedata',
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
