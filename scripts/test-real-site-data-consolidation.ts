import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import type { SiteAssetModel } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import { siteAssetModelQueryKey } from '../apps/hvac-web/src/api/site-asset-model.ts';
import {
  assetsCurrentStateQueryKey,
  assetsProtectedQueryRoot,
  assetsRegistryQueryKey,
} from '../apps/hvac-web/src/features/assets/data.ts';
import { plantMachinesFromAssetModel } from '../apps/hvac-web/src/features/breakdown/plant-machines.ts';
import { createRealtimeDemandController } from '../apps/hvac-web/src/features/operations/realtime/realtime-demand.ts';

test('assetsProtectedQueryRoot is an exact prefix of both asset-model and current-state keys', () => {
  const root = assetsProtectedQueryRoot(7, 'tenant-1', 'site-1');
  const registryKey = assetsRegistryQueryKey(7, 'tenant-1', 'site-1', 2);
  const currentKey = assetsCurrentStateQueryKey(7, 'tenant-1', 'site-1', [], [], 2);

  assert.deepEqual(root, ['real-assets', 7, 'tenant-1', 'site-1']);
  assert.deepEqual(registryKey.slice(0, root.length), root);
  assert.deepEqual(currentKey.slice(0, root.length), root);
});

test('siteAssetModelQueryKey produces one canonical key per site or NONE fallback', () => {
  assert.deepEqual(siteAssetModelQueryKey('site-abc'), ['site-asset-model', 'site-abc']);
  assert.deepEqual(siteAssetModelQueryKey(null), ['site-asset-model', 'NONE']);
});

test('createRealtimeDemandController handles activation, idempotent release, grace handoff, and reset', () => {
  const transitions: boolean[] = [];
  const scheduled = new Map<number, () => void>();
  let nextHandle = 1;

  const controller = createRealtimeDemandController<number>({
    graceMs: 250,
    onChange: (active) => transitions.push(active),
    setTimer: (callback) => {
      const handle = nextHandle;
      nextHandle += 1;
      scheduled.set(handle, callback);
      return handle;
    },
    clearTimer: (handle) => {
      scheduled.delete(handle);
    },
  });

  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);

  // 0 -> 1 activates immediately.
  const releaseFirst = controller.acquire();
  assert.equal(controller.isActive(), true);
  assert.equal(controller.count(), 1);
  assert.deepEqual(transitions, [true]);

  // Duplicate release is idempotent; calling twice keeps count at 0 without multiple timers.
  releaseFirst();
  releaseFirst();
  assert.equal(controller.count(), 0);
  assert.equal(controller.isActive(), true);
  assert.equal(scheduled.size, 1);

  // Handoff 1 -> 0 -> 1 inside the grace period cancels deactivation without flicker.
  const releaseSecond = controller.acquire();
  assert.equal(scheduled.size, 0);
  assert.equal(controller.isActive(), true);
  assert.equal(controller.count(), 1);
  assert.deepEqual(transitions, [true]);

  // Dropping to 0 and letting the grace timer fire deactivates cleanly.
  releaseSecond();
  assert.equal(scheduled.size, 1);
  const [[onlyHandle, onlyCallback]] = [...scheduled.entries()];
  scheduled.delete(onlyHandle);
  onlyCallback();
  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);
  assert.deepEqual(transitions, [true, false]);

  // Re-acquire, start a pending grace deactivation, and verify reset() cancels it and deactivates immediately.
  const releaseThird = controller.acquire();
  assert.equal(controller.isActive(), true);
  releaseThird();
  assert.equal(scheduled.size, 1);
  controller.reset();
  assert.equal(controller.isActive(), false);
  assert.equal(controller.count(), 0);
  assert.equal(scheduled.size, 0);
  assert.deepEqual(transitions, [true, false, true, false]);
});

test('plantMachinesFromAssetModel derives plant machines from E2E asset-model fixture without snapshots', () => {
  const assetModel = JSON.parse(
    readFileSync(new URL('../apps/hvac-web/e2e/fixtures/asset-model.json', import.meta.url), 'utf8'),
  ) as SiteAssetModel;
  const machines = plantMachinesFromAssetModel(assetModel);
  assert.ok(machines.length > 0);
  assert.ok(machines.some((machine) => machine.category === 'CHILLER'));
  assert.ok(machines.every((machine) => Boolean(machine.deviceId && machine.name)));
});
