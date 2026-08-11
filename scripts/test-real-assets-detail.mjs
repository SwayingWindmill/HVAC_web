import assert from 'node:assert/strict';
import test from 'node:test';
import {
  REAL_ASSETS_DETAIL_HISTORY_MARKER,
  isRealAssetsDetailHistoryState,
  parseRealAssetsDetailPath,
  realAssetsEquipmentPath,
  realAssetsListPath,
  resolveRealAssetsDetail,
  writeRealAssetsClipboard,
} from '../apps/hvac-web/src/real/assets/detail.ts';

const siteId = '01900000-0001-7000-8000-000000000001';
const equipmentId = '01900000-0011-7000-8000-000000000011';
const otherEquipmentId = '01900000-0012-7000-8000-000000000012';

const visibleRow = {
  equipment: { id: equipmentId },
};

test('detail paths are stable and parse list, detail and outside states', () => {
  assert.equal(realAssetsListPath(siteId), `/sites/${siteId}/assets`);
  assert.equal(realAssetsEquipmentPath(siteId, equipmentId), `/sites/${siteId}/assets/${equipmentId}`);
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets`, siteId), { state: 'list' });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${equipmentId}`, siteId), { state: 'detail', equipmentId });
  assert.deepEqual(parseRealAssetsDetailPath(`/sites/${siteId}/assets/${equipmentId}/extra`, siteId), { state: 'outside' });
  assert.throws(() => realAssetsEquipmentPath(siteId, 'not-equipment'), /Equipment UUIDv7/);
});

test('invalid, unknown and unauthorized Equipment selectors share one not-visible result', () => {
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], null), { state: 'closed' });
  assert.equal(resolveRealAssetsDetail([visibleRow], equipmentId).state, 'visible');
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], 'not-equipment'), { state: 'not-visible' });
  assert.deepEqual(resolveRealAssetsDetail([visibleRow], otherEquipmentId), { state: 'not-visible' });
});

test('history marker is scoped to the exact Site and Equipment', () => {
  const state = { marker: REAL_ASSETS_DETAIL_HISTORY_MARKER, siteId, equipmentId };
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, equipmentId), true);
  assert.equal(isRealAssetsDetailHistoryState(state, siteId, otherEquipmentId), false);
  assert.equal(isRealAssetsDetailHistoryState({ ...state, marker: 'other' }, siteId, equipmentId), false);
});

test('clipboard helper reports permission success and failure without throwing', async () => {
  const values = [];
  assert.equal(await writeRealAssetsClipboard(async (value) => { values.push(value); }, equipmentId), true);
  assert.deepEqual(values, [equipmentId]);
  assert.equal(await writeRealAssetsClipboard(async () => { throw new Error('denied'); }, equipmentId), false);
  assert.equal(await writeRealAssetsClipboard(undefined, equipmentId), false);
});
