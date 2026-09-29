import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsAssetPath,
  realAssetsDevicePath,
  realAssetsListPath,
  resolveRealAssetsDetail,
  writeRealAssetsClipboard,
} from '../apps/hvac-web/src/real/assets/detail.ts';

const siteId = '01900000-0001-7000-8000-000000000001';
const assetId = '01900000-0011-7000-8000-000000000011';
const otherAssetId = '01900000-0012-7000-8000-000000000012';
const deviceId = '01900000-0013-7000-8000-000000000013';
const otherDeviceId = '01900000-0014-7000-8000-000000000014';

const visibleAssetRow = { asset: { id: assetId } };
const visibleDeviceRow = { device: { id: deviceId } };

test('detail paths are typed for Asset and Device and reject the obsolete untyped route', () => {
  assert.equal(realAssetsListPath(siteId), `/sites/${siteId}/assets`);
  assert.equal(realAssetsAssetPath(siteId, assetId), `/sites/${siteId}/assets/asset/${assetId}`);
  assert.equal(realAssetsDevicePath(siteId, deviceId), `/sites/${siteId}/assets/device/${deviceId}`);
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets`, siteId), { state: 'list' });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/asset/${assetId}`, siteId), {
    state: 'detail', target: { kind: 'asset', id: assetId },
  });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/device/${deviceId}`, siteId), {
    state: 'detail', target: { kind: 'device', id: deviceId },
  });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${assetId}`, siteId), { state: 'outside' });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/device/${deviceId}/extra`, siteId), { state: 'outside' });
  assert.throws(() => realAssetsAssetPath(siteId, 'not-asset'), /Asset UUIDv7/);
  assert.throws(() => realAssetsDevicePath(siteId, 'not-device'), /Device UUIDv7/);
});

test('invalid, unknown and unauthorized typed selectors share one not-visible result', () => {
  assert.deepEqual(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], null), { state: 'closed' });
  assert.equal(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: assetId }).state, 'visible');
  assert.equal(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'device', id: deviceId }).state, 'visible');
  assert.deepEqual(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: 'not-asset' }), { state: 'not-visible' });
  assert.deepEqual(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: otherAssetId }), { state: 'not-visible' });
  assert.deepEqual(resolveRealAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'device', id: otherDeviceId }), { state: 'not-visible' });
});

test('history marker is scoped to the exact Site and Asset', () => {
  const state = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId, assetId };
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, assetId), true);
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, otherAssetId), false);
  assert.equal(isRealAssetsDetailHistoryState({ ...state, marker: 'other' }, siteId, assetId), false);
});

test('clipboard helper reports permission success and failure without throwing', async () => {
  const values = [];
  assert.equal(await writeRealAssetsClipboard(async (value) => { values.push(value); }, assetId), true);
  assert.deepEqual(values, [assetId]);
  assert.equal(await writeRealAssetsClipboard(async () => { throw new Error('denied'); }, assetId), false);
  assert.equal(await writeRealAssetsClipboard(undefined, assetId), false);
});
