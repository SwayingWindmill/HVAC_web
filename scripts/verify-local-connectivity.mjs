// Manual acceptance at the production browser/API seam, never a fixture or CI gate.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { localSites, siteDeviceId, centralPlantDevices } from './central-plant-local-contract.mjs';
import { localAdministratorBrowser } from './lib/local-administrator-browser.mjs';

const origin = process.env.PLATFORM_PUBLIC_ORIGIN || 'https://localhost:8443';
const output = resolve('out/local-connectivity');
const { browser, context } = await localAdministratorBrowser(origin);
try {
  const snapshots = [];
  for (const site of localSites) {
    const deviceId = siteDeviceId(site, centralPlantDevices[0].platformDeviceId);
    const response = await context.request.get(`${origin}/api/v1/devices/${deviceId}/observation-snapshot?keys=power,run_state`);
    assert.equal(response.status(), 200, `${site.siteName}: operator cannot read the production Snapshot`);
    const snapshot = await response.json();
    assert.equal(snapshot.tenantId, site.tenantId);
    assert.equal(snapshot.siteId, site.siteId);
    assert.equal(snapshot.deviceId, deviceId);
    assert.equal(snapshot.displayState, 'ONLINE', `${site.siteName}: plant is not online`);
    for (const key of ['power', 'run_state']) {
      const value = snapshot.values.find((item) => item.key === key);
      assert.equal(value?.state, 'PRESENT', `${site.siteName}: ${key} missing`);
      assert.equal(value.freshness, 'FRESH', `${site.siteName}: ${key} stale`);
    }
    snapshots.push(snapshot);
  }
  await mkdir(output, { recursive: true });
  await writeFile(resolve(output, 'snapshots.json'), `${JSON.stringify(snapshots, null, 2)}\n`);
  console.log('Both Sites expose fresh, correctly scoped production Snapshots.');
} finally { await browser.close(); }
