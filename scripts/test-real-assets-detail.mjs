import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isAssetsDetailHistoryState,
  parseAssetsDetailPath,
  assetsAssetPath,
  assetsDevicePath,
  assetsListPath,
  resolveAssetsDetail,
  writeAssetsClipboard,
} from '../apps/hvac-web/src/features/assets/detail.ts';

const siteId = '01900000-0001-7000-8000-000000000001';
const assetId = '01900000-0011-7000-8000-000000000011';
const otherAssetId = '01900000-0012-7000-8000-000000000012';
const deviceId = '01900000-0013-7000-8000-000000000013';
const otherDeviceId = '01900000-0014-7000-8000-000000000014';

const visibleAssetRow = { asset: { id: assetId } };
const visibleDeviceRow = { device: { id: deviceId } };

test('detail paths are typed for Asset and Device and reject the obsolete untyped route', () => {
  assert.equal(assetsListPath(siteId), `/sites/${siteId}/assets`);
  assert.equal(assetsAssetPath(siteId, assetId), `/sites/${siteId}/assets/asset/${assetId}`);
  assert.equal(assetsDevicePath(siteId, deviceId), `/sites/${siteId}/assets/device/${deviceId}`);
  assert.deepEqual(parseAssetsDetailPath(`/sites/${siteId}/assets`, siteId), { state: 'list' });
  assert.deepEqual(parseAssetsDetailPath(`/sites/${siteId}/assets/asset/${assetId}`, siteId), {
    state: 'detail', target: { kind: 'asset', id: assetId },
  });
  assert.deepEqual(parseAssetsDetailPath(`/sites/${siteId}/assets/device/${deviceId}`, siteId), {
    state: 'detail', target: { kind: 'device', id: deviceId },
  });
  assert.deepEqual(parseAssetsDetailPath(`/sites/${siteId}/assets/${assetId}`, siteId), { state: 'outside' });
  assert.deepEqual(parseAssetsDetailPath(`/sites/${siteId}/assets/device/${deviceId}/extra`, siteId), { state: 'outside' });
  assert.throws(() => assetsAssetPath(siteId, 'not-asset'), /Asset UUIDv7/);
  assert.throws(() => assetsDevicePath(siteId, 'not-device'), /Device UUIDv7/);
});

test('invalid, unknown and unauthorized typed selectors share one not-visible result', () => {
  assert.deepEqual(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], null), { state: 'closed' });
  assert.equal(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: assetId }).state, 'visible');
  assert.equal(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'device', id: deviceId }).state, 'visible');
  assert.deepEqual(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: 'not-asset' }), { state: 'not-visible' });
  assert.deepEqual(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'asset', id: otherAssetId }), { state: 'not-visible' });
  assert.deepEqual(resolveAssetsDetail([visibleAssetRow], [visibleDeviceRow], { kind: 'device', id: otherDeviceId }), { state: 'not-visible' });
});

test('history marker is scoped to the exact Site and Asset', () => {
  const state = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId, assetId };
  assert.equal(isAssetsDetailHistoryState(state, siteId, assetId), true);
  assert.equal(isAssetsDetailHistoryState(state, siteId, otherAssetId), false);
  assert.equal(isAssetsDetailHistoryState({ ...state, marker: 'other' }, siteId, assetId), false);
});

test('clipboard helper reports permission success and failure without throwing', async () => {
  const values = [];
  assert.equal(await writeAssetsClipboard(async (value) => { values.push(value); }, assetId), true);
  assert.deepEqual(values, [assetId]);
  assert.equal(await writeAssetsClipboard(async () => { throw new Error('denied'); }, assetId), false);
  assert.equal(await writeAssetsClipboard(undefined, assetId), false);
});
