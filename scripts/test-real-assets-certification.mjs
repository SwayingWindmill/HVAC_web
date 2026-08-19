import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_CERTIFICATION_FIXTURE_REVISION,
  REAL_ASSETS_CERTIFICATION_SCENARIOS,
  buildCertificationInventory,
  validateRealAssetsCertificationEvidence,
} from './real-assets-certification-lib.mjs';

const tenantId = '01940000-0000-7000-8000-000000000001';
const siteId = '01940000-0001-7000-8000-000000000001';

function validEvidence() {
  return {
    schemaVersion: 1,
    passed: true,
    fixture: {
      revision: REAL_ASSETS_CERTIFICATION_FIXTURE_REVISION,
      deviceCount: 200,
      scenarioCounts: Object.fromEntries(REAL_ASSETS_CERTIFICATION_SCENARIOS.map((scenario) => [scenario, 25])),
    },
    network: {
      registryRequestCount: 3,
      snapshotBatchRequestCount: 2,
      snapshotBatchSizes: [100, 100],
      perDeviceCurrentRequestCount: 0,
    },
    subscriptions: { maximumActive: 1, afterClose: 0, afterScopePurge: 0 },
    scope: { oldScopeLeakDetected: false },
    accessibility: {
      keyboardFlowPassed: true,
      focusRestored: true,
      nonColorSemantics: true,
      reducedMotionPassed: true,
    },
    responsive: {
      desktop: { horizontalOverflow: false },
      tablet: { horizontalOverflow: false },
      mobile: { horizontalOverflow: false, drawerFullWidth: true },
    },
    errors: { console: 0, network: 0 },
    bundle: { historyLazyBoundary: true, nonAssetsAvoidedHistoryChunk: true },
  };
}

test('builds the frozen 200 Device certification inventory with all required states', () => {
  const inventory = buildCertificationInventory({ tenantId, siteId });
  assert.equal(inventory.devices.length, 200);
  assert.equal(inventory.assets.length, 20);
  assert.equal(inventory.bindings.length, 200);
  assert.equal(inventory.unboundDeviceIds.length, 5);
  assert.equal(inventory.ambiguousDeviceIds.length, 5);
  assert.deepEqual(inventory.scenarioCounts, Object.fromEntries(REAL_ASSETS_CERTIFICATION_SCENARIOS.map((scenario) => [scenario, 25])));
  assert.equal(new Set(inventory.devices.map((device) => device.id)).size, 200);
  assert.equal(new Set(inventory.bindings.map((binding) => binding.id)).size, 200);
});

test('accepts complete structured certification evidence', () => {
  assert.deepEqual(validateRealAssetsCertificationEvidence(validEvidence()), { passed: true, errors: [] });
});

test('rejects request storms, scope leaks and accessibility regressions', () => {
  const evidence = validEvidence();
  evidence.network.snapshotBatchSizes = [99, 101];
  evidence.network.perDeviceCurrentRequestCount = 1;
  evidence.subscriptions.maximumActive = 2;
  evidence.scope.oldScopeLeakDetected = true;
  evidence.accessibility.focusRestored = false;
  evidence.responsive.mobile.horizontalOverflow = true;
  evidence.errors.console = 1;
  const result = validateRealAssetsCertificationEvidence(evidence);
  assert.equal(result.passed, false);
  assert.match(result.errors.join('\n'), /100\/100/);
  assert.match(result.errors.join('\n'), /per-Device/);
  assert.match(result.errors.join('\n'), /more than one/);
  assert.match(result.errors.join('\n'), /old scope/);
  assert.match(result.errors.join('\n'), /focus restoration/);
  assert.match(result.errors.join('\n'), /mobile viewport/);
  assert.match(result.errors.join('\n'), /console or network/);
});
