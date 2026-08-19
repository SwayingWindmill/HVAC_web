export const REAL_ASSETS_CERTIFICATION_SCHEMA_VERSION = 1;
export const REAL_ASSETS_CERTIFICATION_FIXTURE_REVISION = 'real-assets-200-devices:v1';
export const REAL_ASSETS_CERTIFICATION_DEVICE_COUNT = 200;
export const REAL_ASSETS_CERTIFICATION_ASSET_COUNT = 20;
export const REAL_ASSETS_CERTIFICATION_SCENARIOS = Object.freeze([
  'normal',
  'offline',
  'stale',
  'suspect',
  'invalid',
  'never-observed',
  'unknown-device-type',
  'valid-zero',
]);

export function certificationId(kind, index, namespace = '01940000') {
  const kindPart = Number(kind).toString(16).padStart(4, '0');
  const indexPart = Number(index).toString(16);
  return `${namespace}-${kindPart}-7${indexPart.padStart(3, '0')}-8000-${indexPart.padStart(12, '0')}`;
}

export function scenarioForIndex(index) {
  if (!Number.isInteger(index) || index < 1) throw new Error('certification index must be a positive integer');
  return REAL_ASSETS_CERTIFICATION_SCENARIOS[(index - 1) % REAL_ASSETS_CERTIFICATION_SCENARIOS.length];
}

export function buildCertificationInventory({ tenantId, siteId, count = REAL_ASSETS_CERTIFICATION_DEVICE_COUNT, namespace = '01940000' }) {
  if (count !== REAL_ASSETS_CERTIFICATION_DEVICE_COUNT) throw new Error('Real Assets certification fixture is frozen at 200 Devices');
  const now = '2026-08-01T00:00:00.000Z';
  const assets = Array.from({ length: REAL_ASSETS_CERTIFICATION_ASSET_COUNT }, (_, offset) => {
    const index = offset + 1;
    return {
      id: certificationId(0x10, index, namespace),
      tenantId: tenantId,
      siteId,
      code: `PLANT-EQ-${String(index).padStart(2, '0')}`,
      displayName: `Plant Asset ${String(index).padStart(2, '0')}`,
      assetType: index % 2 === 0 ? 'CHILLER' : 'PUMP_GROUP',
      status: 'ACTIVE',
      revision: index,
      createdAt: now,
      updatedAt: now,
    };
  });
  const devices = Array.from({ length: count }, (_, offset) => {
    const index = offset + 1;
    const scenario = scenarioForIndex(index);
    return {
      id: certificationId(0x20, index, namespace),
      tenantId: tenantId,
      siteId,
      code: `CERT-DEVICE-${String(index).padStart(3, '0')}`,
      displayName: `Certification Device ${String(index).padStart(3, '0')} · ${scenario}`,
      deviceType: scenario === 'unknown-device-type' ? 'VENDOR_UNKNOWN_CONTROLLER' : 'CHILLER',
      status: index % 47 === 0 ? 'INACTIVE' : 'ACTIVE',
      revision: 1000 + index,
      createdAt: now,
      updatedAt: now,
      certificationScenario: scenario,
    };
  });
  const bindings = [];
  for (let index = 1; index <= 190; index += 1) {
    const assetIndex = Math.floor((index - 1) / 10) + 1;
    bindings.push({
      id: certificationId(0x30, index, namespace),
      tenantId: tenantId,
      siteId,
      deviceId: devices[index - 1].id,
      assetId: assets[assetIndex - 1].id,
      bindingRole: 'PRIMARY_CONTROLLER',
      status: 'ACTIVE',
      validFrom: now,
      validTo: null,
      revision: index,
      createdAt: now,
      updatedAt: now,
    });
  }
  for (let index = 196; index <= 200; index += 1) {
    for (let target = 1; target <= 2; target += 1) {
      const bindingIndex = 190 + ((index - 196) * 2) + target;
      bindings.push({
        id: certificationId(0x30, bindingIndex, namespace),
        tenantId: tenantId,
        siteId,
        deviceId: devices[index - 1].id,
        assetId: assets[target - 1].id,
        bindingRole: target === 1 ? 'PRIMARY_CONTROLLER' : 'SECONDARY_CONTROLLER',
        status: 'ACTIVE',
        validFrom: now,
        validTo: null,
        revision: bindingIndex,
        createdAt: now,
        updatedAt: now,
      });
    }
  }
  const scenarioCounts = Object.fromEntries(REAL_ASSETS_CERTIFICATION_SCENARIOS.map((scenario) => [scenario, 0]));
  for (const device of devices) scenarioCounts[device.certificationScenario] += 1;
  return Object.freeze({
    assets: Object.freeze(assets),
    devices: Object.freeze(devices),
    bindings: Object.freeze(bindings),
    scenarioCounts: Object.freeze(scenarioCounts),
    unboundDeviceIds: Object.freeze(devices.slice(190, 195).map((device) => device.id)),
    ambiguousDeviceIds: Object.freeze(devices.slice(195, 200).map((device) => device.id)),
  });
}

export function validateRealAssetsCertificationEvidence(evidence) {
  const errors = [];
  if (evidence?.schemaVersion !== REAL_ASSETS_CERTIFICATION_SCHEMA_VERSION) errors.push('unsupported schemaVersion');
  if (evidence?.fixture?.revision !== REAL_ASSETS_CERTIFICATION_FIXTURE_REVISION) errors.push('fixture revision drifted');
  if (evidence?.fixture?.deviceCount !== REAL_ASSETS_CERTIFICATION_DEVICE_COUNT) errors.push('fixture did not contain 200 Devices');
  for (const scenario of REAL_ASSETS_CERTIFICATION_SCENARIOS) {
    if ((evidence?.fixture?.scenarioCounts?.[scenario] ?? 0) < 1) errors.push(`fixture omitted ${scenario}`);
  }
  if (evidence?.network?.registryRequestCount !== 3) errors.push('initial Registry request count was not exactly three');
  if (evidence?.network?.snapshotBatchRequestCount !== 2) errors.push('initial Snapshot batch request count was not exactly two');
  if (JSON.stringify(evidence?.network?.snapshotBatchSizes) !== JSON.stringify([100, 100])) errors.push('Snapshot batch sizes were not 100/100');
  if ((evidence?.network?.perDeviceCurrentRequestCount ?? 1) !== 0) errors.push('per-Device current requests were observed');
  if ((evidence?.subscriptions?.maximumActive ?? 0) !== 1) errors.push('more than one exact realtime subscription was active');
  if ((evidence?.subscriptions?.afterClose ?? -1) !== 0) errors.push('realtime subscription did not return to zero after close');
  if ((evidence?.subscriptions?.afterScopePurge ?? -1) !== 0) errors.push('realtime subscription survived scope purge');
  if (evidence?.scope?.oldScopeLeakDetected !== false) errors.push('old scope data leaked');
  if (evidence?.accessibility?.keyboardFlowPassed !== true) errors.push('keyboard-only flow did not pass');
  if (evidence?.accessibility?.focusRestored !== true) errors.push('focus restoration did not pass');
  if (evidence?.accessibility?.nonColorSemantics !== true) errors.push('non-color semantics were not observed');
  if (evidence?.accessibility?.reducedMotionPassed !== true) errors.push('reduced-motion audit did not pass');
  for (const viewport of ['desktop', 'tablet', 'mobile']) {
    if (evidence?.responsive?.[viewport]?.horizontalOverflow !== false) errors.push(`${viewport} viewport overflowed horizontally`);
  }
  if (evidence?.responsive?.mobile?.drawerFullWidth !== true) errors.push('mobile drawer was not full width');
  if ((evidence?.errors?.console ?? 1) !== 0 || (evidence?.errors?.network ?? 1) !== 0) errors.push('unexpected browser console or network errors were observed');
  if (evidence?.bundle?.historyLazyBoundary !== true || evidence?.bundle?.nonAssetsAvoidedHistoryChunk !== true) errors.push('history/ECharts lazy boundary evidence failed');
  if (evidence?.passed !== true) errors.push('evidence is not marked passed');
  return Object.freeze({ passed: errors.length === 0, errors: Object.freeze(errors) });
}
