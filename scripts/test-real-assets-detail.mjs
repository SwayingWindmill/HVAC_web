import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsDevicePath,
  realAssetsListPath,
  resolveRealAssetsDetail,
  writeRealAssetsClipboard,
} from '../apps/hvac-web/src/real/assets/detail.ts';

const siteId = '01900000-0001-7000-8000-000000000001';
const deviceId = '01900000-0011-7000-8000-000000000011';
const otherDeviceId = '01900000-0012-7000-8000-000000000012';

const visibleRow = {
  device: { id: deviceId },
};

test('detail paths are stable and parse list, detail and outside states', () => {
  assert.equal(realAssetsListPath(siteId), `/sites/${siteId}/assets`);
  assert.equal(realAssetsDevicePath(siteId, deviceId), `/sites/${siteId}/assets/${deviceId}`);
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets`, siteId), { state: 'list' });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${deviceId}`, siteId), { state: 'detail', deviceId });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${deviceId}/extra`, siteId), { state: 'outside' });
  assert.throws(() => realAssetsDevicePath(siteId, 'not-a-device'), /Device UUIDv7/);
});

test('invalid, unknown and unauthorized Device selectors share one not-visible result', () => {
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], null), { state: 'closed' });
  assert.equal(resolveRealAssetsDetail([visibleRow], deviceId).state, 'visible');
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], 'not-a-device'), { state: 'not-visible' });
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], otherDeviceId), { state: 'not-visible' });
});

test('history marker is scoped to the exact Site and Device', () => {
  const state = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId, deviceId };
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, deviceId), true);
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, otherDeviceId), false);
  assert.equal(isRealAssetsDetailHistoryState({ ...state, marker: 'other' }, siteId, deviceId), false);
});

test('clipboard helper reports permission success and failure without throwing', async () => {
  const values = [];
  assert.equal(await writeRealAssetsClipboard(async (value) => { values.push(value); }, deviceId), true);
  assert.deepEqual(values, [deviceId]);
  assert.equal(await writeRealAssetsClipboard(async () => { throw new Error('denied'); }, deviceId), false);
  assert.equal(await writeRealAssetsClipboard(undefined, deviceId), false);
});
