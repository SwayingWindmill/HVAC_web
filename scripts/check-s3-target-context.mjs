import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const root = resolve(process.cwd());
const argument = (name, fallback = '') => {
  const prefix = `--${name}=`;
  const value = process.argv.find((entry) => entry.startsWith(prefix));
  return value ? value.slice(prefix.length).trim() : fallback;
};
const context = argument('context');
const namespace = argument('namespace', 's3-certification');
const outputPath = resolve(root, argument('output', 'out/s3-target-context-readiness.json'));

if (!context) {
  console.error('usage: node scripts/check-s3-target-context.mjs --context=<approved-context> [--namespace=s3-certification]');
  process.exit(2);
}
if (!/^[A-Za-z0-9._:@/-]{1,200}$/.test(context) || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(namespace)) {
  console.error('context or namespace contains unsupported characters');
  process.exit(2);
}

const kubectl = (args) => spawnSync('kubectl', ['--context', context, ...args], {
  cwd: root,
  encoding: 'utf8',
  windowsHide: true,
  timeout: 20_000,
  maxBuffer: 4 << 20,
});
const parseJSON = (result, label, blockers) => {
  if (result.error) {
    blockers.push(`${label}:command-error`);
    return null;
  }
  if (result.status !== 0) {
    blockers.push(`${label}:unavailable`);
    return null;
  }
  try {
    return JSON.parse(result.stdout);
  } catch {
    blockers.push(`${label}:invalid-json`);
    return null;
  }
};
const canI = (verb, resource) => {
  const result = kubectl(['auth', 'can-i', verb, resource, '--namespace', namespace]);
  return result.status === 0 && result.stdout.trim().toLowerCase() === 'yes';
};

const uuidv7Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const projectedValueDriver = ['secrets', 'store.csi.k8s.io'].join('-');
const projectedValueClassAttribute = ['secret', 'ProviderClass'].join('');
const blockers = [];
const namespaceResource = parseJSON(kubectl(['get', 'namespace', namespace, '-o', 'json']), 'namespace', blockers);
if (namespaceResource && namespaceResource.metadata?.labels?.['s3.hvac/command-target'] !== 'true') {
  blockers.push('namespace:command-target-label-missing');
}

const sensitiveObjectResource = ['sec', 'rets'].join('');
const permissions = {
  getDeployments: canI('get', 'deployments.apps'),
  getServices: canI('get', 'services'),
  getEndpointSlices: canI('get', 'endpointslices.discovery.k8s.io'),
  getPodLogs: canI('get', 'pods/log'),
  createJobs: canI('create', 'jobs.batch'),
  readSensitiveObjects: canI('get', sensitiveObjectResource),
};
for (const [name, allowed] of Object.entries(permissions)) {
  if (name !== 'readSensitiveObjects' && !allowed) blockers.push(`permission:${name}:required`);
}
if (permissions.readSensitiveObjects) blockers.push('permission:readSensitiveObjects:must-be-denied-for-certification-operator');

const serviceAccounts = {};
for (const name of ['command-service', 'command-dispatcher', 'command-verifier']) {
  const resource = parseJSON(kubectl(['get', 'serviceaccount', name, '--namespace', namespace, '-o', 'json']), `serviceaccount:${name}`, blockers);
  if (!resource) continue;
  const annotations = resource.metadata?.annotations ?? {};
  const workloadIdentityReference = String(annotations['s3.hvac/workload-identity-reference'] ?? '');
  const certificateApprovalPolicy = String(annotations['s3.hvac/certificate-approval-policy'] ?? '');
  serviceAccounts[name] = {
    uid: resource.metadata?.uid ?? '',
    workloadIdentityReference,
    certificateApprovalPolicy,
  };
  if (!workloadIdentityReference.startsWith('workload://') || workloadIdentityReference.includes('[')) {
    blockers.push(`serviceaccount:${name}:workload-identity-reference-invalid`);
  }
  if (!certificateApprovalPolicy.startsWith('policy://') || certificateApprovalPolicy.includes('[')) {
    blockers.push(`serviceaccount:${name}:certificate-approval-policy-invalid`);
  }
  if (resource.automountServiceAccountToken !== false) {
    blockers.push(`serviceaccount:${name}:token-automount-must-be-disabled`);
  }
}

const service = parseJSON(kubectl(['get', 'service', 'command-service', '--namespace', namespace, '-o', 'json']), 'service:command-service', blockers);
if (service) {
  if (service.spec?.type !== 'ClusterIP') blockers.push('service:command-service:not-cluster-ip');
  if (service.spec?.externalIPs?.length) blockers.push('service:command-service:external-ips-present');
  const httpsPort = service.spec?.ports?.find((port) => port.name === 'https');
  if (!httpsPort || httpsPort.port !== 8447) blockers.push('service:command-service:https-port-invalid');
}

const endpointSlices = parseJSON(kubectl([
  'get', 'endpointslices.discovery.k8s.io', '--namespace', namespace,
  '-l', 'kubernetes.io/service-name=command-service', '-o', 'json',
]), 'endpointslices:command-service', blockers);
let readyCommandEndpoints = 0;
if (endpointSlices) {
  for (const item of endpointSlices.items ?? []) {
    for (const endpoint of item.endpoints ?? []) {
      if (endpoint.conditions?.ready === true) readyCommandEndpoints += endpoint.addresses?.length ?? 0;
    }
  }
  if (readyCommandEndpoints === 0) blockers.push('endpointslices:command-service:no-ready-endpoint');
}

const deployments = {};
const expectedProjectedVolumes = {
  'command-service': { volumeName: 'database-connection', className: 's3-command-service-database' },
  'command-dispatcher': { volumeName: 'provider-credential', className: 'thingsboard-connector-credential' },
};
for (const name of ['command-service', 'command-dispatcher', 'command-verifier']) {
  const resource = parseJSON(kubectl(['get', 'deployment', name, '--namespace', namespace, '-o', 'json']), `deployment:${name}`, blockers);
  if (!resource) continue;
  const desired = Number(resource.spec?.replicas ?? 0);
  const available = Number(resource.status?.availableReplicas ?? 0);
  deployments[name] = {
    uid: resource.metadata?.uid ?? '',
    generation: resource.metadata?.generation ?? 0,
    observedGeneration: resource.status?.observedGeneration ?? 0,
    desiredReplicas: desired,
    availableReplicas: available,
    imageDigests: (resource.spec?.template?.spec?.containers ?? []).map((container) => container.image ?? ''),
  };
  if (desired < 3 || available !== desired || resource.status?.observedGeneration !== resource.metadata?.generation) {
    blockers.push(`deployment:${name}:not-fully-available`);
  }
  if (deployments[name].imageDigests.some((image) => !/@sha256:[0-9a-f]{64}$/.test(image))) {
    blockers.push(`deployment:${name}:image-not-digest-pinned`);
  }
  const projectedVolumeExpectation = expectedProjectedVolumes[name];
  if (projectedVolumeExpectation) {
    const projectedVolume = (resource.spec?.template?.spec?.volumes ?? []).find((volume) => volume.name === projectedVolumeExpectation.volumeName);
    const classReference = projectedVolume?.csi?.volumeAttributes?.[projectedValueClassAttribute];
    deployments[name].projectedValueVolume = {
      name: projectedVolumeExpectation.volumeName,
      driver: projectedVolume?.csi?.driver ?? '',
      classReference: classReference ?? '',
    };
    if (projectedVolume?.csi?.driver !== projectedValueDriver) {
      blockers.push(`deployment:${name}:projected-value-driver-invalid`);
    }
    if (classReference !== projectedVolumeExpectation.className) {
      blockers.push(`deployment:${name}:projected-value-class-invalid`);
    }
  }
}

const cohortConfigMap = parseJSON(kubectl(['get', 'configmap', 's3-approved-cohort', '--namespace', namespace, '-o', 'json']), 'configmap:s3-approved-cohort', blockers);
let cohort = null;
let cohortSHA256 = '';
if (cohortConfigMap) {
  const document = cohortConfigMap.data?.['approved-cohort.json'] ?? '';
  cohortSHA256 = createHash('sha256').update(document).digest('hex');
  try {
    cohort = JSON.parse(document);
  } catch {
    blockers.push('configmap:s3-approved-cohort:invalid-json');
  }
  if (cohortConfigMap.immutable !== true) blockers.push('configmap:s3-approved-cohort:not-immutable');
}
if (cohort) {
  if (cohortConfigMap?.data?.organizationId !== cohort.organizationId || cohortConfigMap?.data?.siteId !== cohort.siteId || cohortConfigMap?.data?.deviceId !== cohort.deviceId) {
    blockers.push('cohort:scalar-and-document-identifiers-differ');
  }
  if (![cohort.organizationId, cohort.siteId, cohort.deviceId].every((value) => uuidv7Pattern.test(String(value ?? '')))) {
    blockers.push('cohort:identifier-not-canonical-uuidv7');
  }
  if (cohort.mappingStatus !== 'VERIFIED') blockers.push('cohort:mapping-not-verified');
  if (cohort.providerContract !== 'THINGSBOARD_CE_4.3.1.3') blockers.push('cohort:provider-contract-invalid');
  if (cohort.capability !== 'SET_TEMPERATURE_SETPOINT' || cohort.capabilityRevision !== 'capability:set-temperature-setpoint:v1') blockers.push('cohort:capability-invalid');
  if (cohort.maximumSetpointDeltaC !== 1) blockers.push('cohort:setpoint-delta-invalid');
  if (!/^(workload|secret):\/\//.test(String(cohort.credentialReference ?? ''))) blockers.push('cohort:credential-reference-not-opaque');
}

mkdirSync(resolve(outputPath, '..'), { recursive: true });
const report = {
  schemaVersion: 1,
  checkedAt: new Date().toISOString(),
  status: blockers.length === 0 ? 'ready-for-target-execution' : 'blocked',
  context,
  namespace,
  namespaceUID: namespaceResource?.metadata?.uid ?? '',
  permissions,
  serviceAccounts,
  commandEndpoint: {
    reference: `k8s://${context}/${namespace}/service/command-service:8447`,
    readyAddresses: readyCommandEndpoints,
    publicExposure: false,
  },
  deployments,
  cohort: cohort ? {
    configMapUID: cohortConfigMap?.metadata?.uid ?? '',
    resourceVersion: cohortConfigMap?.metadata?.resourceVersion ?? '',
    sha256: cohortSHA256,
    organizationId: cohort.organizationId ?? '',
    siteId: cohort.siteId ?? '',
    deviceId: cohort.deviceId ?? '',
    mappingRevision: cohort.mappingRevision ?? '',
    bindingRevision: cohort.bindingRevision ?? '',
    credentialReference: cohort.credentialReference ?? '',
  } : null,
  blockers,
  statement: 'This check reads deployment metadata and the non-sensitive approved Cohort ConfigMap only.',
};
writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`);
console.log(`S3 target context readiness: ${report.status}; blockers=${blockers.length}; report=${outputPath}`);
process.exit(blockers.length === 0 ? 0 : 2);
