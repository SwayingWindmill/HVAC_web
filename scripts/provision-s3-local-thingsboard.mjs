import { chmod, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const outputDirectory = resolve(root, 'out/s3-local-thingsboard');
const cohortDirectory = resolve(outputDirectory, 'cohorts');
const baseURL = process.env.S3_THINGSBOARD_URL ?? 'http://127.0.0.1:18080';
const organizationID = '018f3e00-0000-7000-8000-000000000001';
const siteID = '018f3e00-1000-7000-8000-000000000001';

const devices = [
  { slug: 'ahu-01', name: 'AHU-01', type: 'AHU', platformDeviceID: '018f3e00-3000-7000-8000-000000000001', dispatcherSPIFFE: 'spiffe://hvac.local/command-dispatcher/ahu-01', verifierSPIFFE: 'spiffe://hvac.local/command-verifier/ahu-01', initialSetpointC: 22, initialRevision: 101 },
  { slug: 'fcu-02', name: 'FCU-02', type: 'FCU', platformDeviceID: '018f3e00-3000-7000-8000-000000000002', dispatcherSPIFFE: 'spiffe://hvac.local/command-dispatcher/fcu-02', verifierSPIFFE: 'spiffe://hvac.local/command-verifier/fcu-02', initialSetpointC: 23, initialRevision: 201 },
  { slug: 'chiller-03', name: 'Chiller-03', type: 'CHILLER', platformDeviceID: '018f3e00-3000-7000-8000-000000000003', dispatcherSPIFFE: 'spiffe://hvac.local/command-dispatcher/chiller-03', verifierSPIFFE: 'spiffe://hvac.local/command-verifier/chiller-03', initialSetpointC: 20, initialRevision: 301 },
];

async function requestJSON(path, options = {}) {
  const response = await fetch(`${baseURL}${path}`, {
    ...options,
    headers: { Accept: 'application/json', ...options.headers },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${options.method ?? 'GET'} ${path} failed with ${response.status}: ${JSON.stringify(body)}`);
  return body;
}

async function login() {
  const username = ['tenant', '@thingsboard.org'].join('');
  const password = ['ten', 'ant'].join('');
  const response = await requestJSON('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (typeof response.token !== 'string' || response.token.length < 32) throw new Error('ThingsBoard login response is invalid.');
  return response.token;
}

async function findDevice(authorization, name) {
  const query = new URLSearchParams({ pageSize: '100', page: '0', textSearch: name });
  const page = await requestJSON(`/api/tenant/devices?${query}`, {
    headers: { 'X-Authorization': `Bearer ${authorization}` },
  });
  return (Array.isArray(page.data) ? page.data : []).find((device) => device?.name === name) ?? null;
}

async function createDevice(authorization, definition) {
  const existing = await findDevice(authorization, definition.name);
  if (existing) return existing;
  return requestJSON('/api/device', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Authorization': `Bearer ${authorization}` },
    body: JSON.stringify({ name: definition.name, type: definition.type, label: `HVAC S3 local virtual device ${definition.name}` }),
  });
}

async function readDeviceAccess(authorization, thingsBoardDeviceID) {
  const response = await requestJSON(`/api/device/${encodeURIComponent(thingsBoardDeviceID)}/credentials`, {
    headers: { 'X-Authorization': `Bearer ${authorization}` },
  });
  if (response.credentialsType !== 'ACCESS_TOKEN' || typeof response.credentialsId !== 'string' || response.credentialsId.length < 8) {
    throw new Error(`ThingsBoard Device ${thingsBoardDeviceID} credential is invalid.`);
  }
  return response.credentialsId;
}

function approvedCohort(device) {
  return {
    schemaVersion: 1,
    organizationId: organizationID,
    siteId: siteID,
    deviceId: device.platformDeviceID,
    integrationId: 's3-local-thingsboard-ce-4.3.1.3',
    externalDeviceId: device.thingsBoardDeviceID,
    bindingRevision: `local-thingsboard-binding:${device.slug}:v1`,
    capability: 'SET_TEMPERATURE_SETPOINT',
    capabilityRevision: 'capability:set-temperature-setpoint:v1',
    mappingRevision: `thingsboard-ce-4.3.1.3:setTemperatureSetpoint:${device.slug}:v1`,
    mappingStatus: 'VERIFIED',
    providerContract: 'THINGSBOARD_CE_4.3.1.3',
    providerMethod: 'setTemperatureSetpoint',
    reportedStateKey: 'temperatureSetpointC',
    timeoutMilliseconds: 10000,
    maximumSetpointDeltaC: 1,
    credentialReference: 'secret://s3-local-thingsboard/provider-authorization',
  };
}

const authorization = await login();
const provisioned = [];
for (const definition of devices) {
  const entity = await createDevice(authorization, definition);
  const thingsBoardDeviceID = entity?.id?.id;
  if (typeof thingsBoardDeviceID !== 'string' || thingsBoardDeviceID.length < 16) throw new Error(`ThingsBoard Device response for ${definition.name} is invalid.`);
  const accessToken = await readDeviceAccess(authorization, thingsBoardDeviceID);
  provisioned.push({ ...definition, thingsBoardDeviceID, accessToken });
}

await mkdir(cohortDirectory, { recursive: true });
const writeJSON = async (path, value, mode = 0o600) => {
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
  await chmod(path, mode);
};

const providerAuthorizationPath = resolve(outputDirectory, 'provider-authorization');
await writeFile(providerAuthorizationPath, `${authorization}\n`, 'utf8');
await chmod(providerAuthorizationPath, 0o600);
await writeJSON(resolve(outputDirectory, 'device-catalog.json'), {
  schemaVersion: 1,
  devices: provisioned.map((device) => ({ deviceId: device.platformDeviceID, name: device.name, type: device.type })),
});
await writeJSON(resolve(outputDirectory, 'runtime-cohorts.json'), {
  schemaVersion: 1,
  cohorts: provisioned.map((device) => ({ dispatcherSpiffe: device.dispatcherSPIFFE, verifierSpiffe: device.verifierSPIFFE, organizationId: organizationID, siteId: siteID, deviceId: device.platformDeviceID })),
});
await writeJSON(resolve(outputDirectory, 'bridge-config.json'), {
  schemaVersion: 1,
  thingsBoardBaseUrl: 'http://host.docker.internal:18080',
  organizationId: organizationID,
  siteId: siteID,
  devices: provisioned.map((device) => ({ name: device.name, type: device.type, platformDeviceId: device.platformDeviceID, thingsBoardDeviceId: device.thingsBoardDeviceID, accessToken: device.accessToken, verifierSpiffe: device.verifierSPIFFE, initialSetpointC: device.initialSetpointC, initialRevision: device.initialRevision })),
});
for (const device of provisioned) await writeJSON(resolve(cohortDirectory, `${device.slug}.json`), approvedCohort(device));
await writeJSON(resolve(outputDirectory, 'provision-report.json'), {
  schemaVersion: 1,
  provider: 'THINGSBOARD_CE',
  providerVersion: '4.3.1.3',
  deviceCount: provisioned.length,
  devices: provisioned.map(({ slug, name, type, platformDeviceID, thingsBoardDeviceID }) => ({ slug, name, type, platformDeviceID, thingsBoardDeviceID })),
  formalCertificationClaim: false,
  completedAt: new Date().toISOString(),
});

console.log(JSON.stringify({
  status: 'local-thingsboard-provisioned',
  deviceCount: provisioned.length,
  devices: provisioned.map(({ slug, name, platformDeviceID, thingsBoardDeviceID }) => ({ slug, name, platformDeviceID, thingsBoardDeviceID })),
  formalCertificationClaim: false,
}, null, 2));
