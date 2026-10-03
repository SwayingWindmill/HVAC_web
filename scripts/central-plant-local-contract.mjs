export const centralPlantIdentity = Object.freeze({
  tenantId: '018f3d00-0000-7000-8000-000000000001',
  siteId: '018f3e00-1000-7000-8000-000000000001',
  principalId: '018f3e00-2000-7000-8000-000000000001',
  gatewayDeviceId: '018f3e00-4000-7000-8000-000000000100',
});

export const centralPlantDevices = Object.freeze([
  { slug: 'chiller-01', name: 'CHILLER-01', type: 'CHILLER', platformDeviceId: '018f3e00-4000-7000-8000-000000000001' },
  { slug: 'chwp-01', name: 'CHWP-01', type: 'CHILLED_WATER_PUMP', platformDeviceId: '018f3e00-4000-7000-8000-000000000002' },
  { slug: 'cwp-01', name: 'CWP-01', type: 'COOLING_WATER_PUMP', platformDeviceId: '018f3e00-4000-7000-8000-000000000003' },
  { slug: 'ct-01', name: 'CT-01', type: 'COOLING_TOWER', platformDeviceId: '018f3e00-4000-7000-8000-000000000004' },
  { slug: 'hvac-meter', name: 'METER-HVAC-TOTAL', type: 'HVAC_POWER_METER', platformDeviceId: '018f3e00-4000-7000-8000-000000000005' },
  { slug: 'btu-meter', name: 'BTU-METER-01', type: 'BTU_METER', platformDeviceId: '018f3e00-4000-7000-8000-000000000006' },
  { slug: 'weather-station', name: 'WEATHER-STATION-01', type: 'WEATHER_STATION', platformDeviceId: '018f3e00-4000-7000-8000-000000000007' },
]);

// The local stack runs one EG8200 Gateway per Site, both in the local Tenant. Site A keeps
// the original local identities; every other Site derives its own from its index, so the
// same plant model can be registered again without colliding.
export const localSites = Object.freeze([
  Object.freeze({
    index: 0, tenantId: centralPlantIdentity.tenantId, siteId: centralPlantIdentity.siteId,
    siteCode: 'local-energy-site', siteName: '本地智慧能源站点',
    gatewayDeviceId: centralPlantIdentity.gatewayDeviceId, gatewayCode: 'eg8200-commercial-001', gatewayName: 'EG8200-COMMERCIAL-001',
    simulatorService: 'eg8200-simulator', simulatorConfig: 'eg8200-mqtt.json', simulatorQueue: 'eg8200',
  }),
  Object.freeze({
    index: 1, tenantId: centralPlantIdentity.tenantId, siteId: '018f3e00-1000-7000-8000-000000000002',
    siteCode: 'local-energy-site-b', siteName: '本地智慧能源二号站点',
    gatewayDeviceId: '018f3e00-4000-7000-8000-000000000200', gatewayCode: 'eg8200-commercial-002', gatewayName: 'EG8200-COMMERCIAL-002',
    simulatorService: 'eg8200-simulator-b', simulatorConfig: 'eg8200-mqtt-b.json', simulatorQueue: 'eg8200-b',
  }),
]);

// siteUUID is localUUID within a Site's namespace.
export function siteUUID(site, index) {
  return localUUID(index).replace(/^01910000-0000-/, `01910000-${site.index.toString(16).padStart(4, '0')}-`);
}

// siteDeviceId is a plant Device's platform id at a Site.
export function siteDeviceId(site, platformDeviceId) {
  return platformDeviceId.replace(/^018f3e00-4000-/, `018f3e00-${(0x4000 + site.index).toString(16)}-`);
}

export const analyticsActions = Object.freeze([
  'analytics.energy-series.read',
]);

export const telemetryActions = Object.freeze([
  'telemetry.snapshot.read',
  'telemetry.batch.read',
  'telemetry.subscribe',
  'telemetry.history.read',
  'telemetry.resubscribe',
  'telemetry.recovery.use',
  'telemetry.recovery.checkpoint',
]);

export function localUUID(index) {
  if (!Number.isSafeInteger(index) || index <= 0 || index > 0xffffffffffff) throw new Error('local UUID index is out of range');
  return `01910000-0000-7000-8000-${index.toString(16).padStart(12, '0')}`;
}

export function sqlLiteral(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}
