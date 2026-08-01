export const centralPlantIdentity = Object.freeze({
  organizationId: '018f3e00-0000-7000-8000-000000000001',
  siteId: '018f3e00-1000-7000-8000-000000000001',
  principalId: '018f3e00-2000-7000-8000-000000000001',
  integrationInstanceId: '018f3e00-0000-7000-8000-000000000101',
});

export const centralPlantDevices = Object.freeze([
  { slug: 'chiller-01', name: 'CHILLER-01', type: 'CHILLER', platformDeviceId: '018f3e00-4000-7000-8000-000000000001' },
  { slug: 'chwp-01', name: 'CHWP-01', type: 'CHILLED_WATER_PUMP', platformDeviceId: '018f3e00-4000-7000-8000-000000000002' },
  { slug: 'cwp-01', name: 'CWP-01', type: 'COOLING_WATER_PUMP', platformDeviceId: '018f3e00-4000-7000-8000-000000000003' },
  { slug: 'ct-01', name: 'CT-01', type: 'COOLING_TOWER', platformDeviceId: '018f3e00-4000-7000-8000-000000000004' },
  { slug: 'hvac-meter', name: 'METER-HVAC-TOTAL', type: 'HVAC_POWER_METER', platformDeviceId: '018f3e00-4000-7000-8000-000000000005' },
  { slug: 'btu-meter', name: 'BTU-METER-01', type: 'BTU_METER', platformDeviceId: '018f3e00-4000-7000-8000-000000000006' },
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
