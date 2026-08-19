import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsAssetPath,
  realAssetsListPath,
  resolveRealAssetsDetail,
  writeRealAssetsClipboard,
} from '../apps/hvac-web/src/real/assets/detail.ts';

const siteId = '01900000-0001-7000-8000-000000000001';
const assetId = '01900000-0011-7000-8000-000000000011';
const otherAssetId = '01900000-0012-7000-8000-000000000012';

const visibleRow = {
  asset: { id: assetId },
};

test('detail paths are stable and parse list, detail and outside states', () => {
  assert.equal(realAssetsListPath(siteId), `/sites/${siteId}/assets`);
  assert.equal(realAssetsAssetPath(siteId, assetId), `/sites/${siteId}/assets/${assetId}`);
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets`, siteId), { state: 'list' });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${assetId}`, siteId), { state: 'detail', assetId });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${assetId}/extra`, siteId), { state: 'outside' });
  assert.throws(() => realAssetsAssetPath(siteId, 'not-asset'), /Asset UUIDv7/);
});

test('invalid, unknown and unauthorized Asset selectors share one not-visible result', () => {
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], null), { state: 'closed' });
  assert.equal(resolveRealAssetsDetail([visibleRow], assetId).state, 'visible');
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], 'not-asset'), { state: 'not-visible' });
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], otherAssetId), { state: 'not-visible' });
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
