import assert from 'node:assert/strict';
import test from 'node:test';
import { alarmDailyCounts, alarmsByDevice, meanTimeToClear } from '../apps/hvac-web/src/features/alarms/alarm-history.ts';
import { formatRelative } from '../apps/hvac-web/src/lib/operator-format.ts';

const now = Date.parse('2026-10-10T06:00:00Z'); // 14:00 in Asia/Shanghai
const alarm = (firstOccurredAt, raisedAt, overrides = {}) => ({
  alarmId: firstOccurredAt, firstOccurredAt, peakSeverity: 'CRITICAL', deviceId: 'chiller',
  timeline: [{ operation: 'PUBLISH', currentSeverity: raisedAt }],
  ...overrides,
});

test('alarms count on the Site-local day they were raised, at the severity they were raised', () => {
  const rows = alarmDailyCounts([
    alarm('2026-10-09T17:00:00Z', 'MAJOR'), // 10/10 01:00 local
    alarm('2026-10-09T15:00:00Z', 'MINOR'), // 10/9 23:00 local
    alarm('2026-10-09T15:30:00Z', 'MINOR'),
    alarm('2026-09-01T00:00:00Z', 'CRITICAL'), // outside the window
  ], 'Asia/Shanghai', 3, now);
  assert.deepEqual(rows.map((row) => [row.label, row.MAJOR, row.MINOR, row.CRITICAL]), [
    ['10/8', 0, 0, 0],
    ['10/9', 0, 2, 0],
    ['10/10', 1, 0, 0],
  ]);
});

test('devices rank by alarms raised; Site-level alarms belong to none', () => {
  assert.deepEqual(alarmsByDevice([
    alarm('a', 'MINOR', { deviceId: 'pump' }),
    alarm('b', 'MINOR', { deviceId: 'chiller' }),
    alarm('c', 'MINOR', { deviceId: 'chiller' }),
    alarm('d', 'MINOR', { deviceId: undefined }),
  ]), [{ deviceId: 'chiller', count: 2 }, { deviceId: 'pump', count: 1 }]);
});

test('relative times read the way operators scan them', () => {
  assert.equal(formatRelative('2026-10-10T05:59:40Z', now), '刚刚');
  assert.equal(formatRelative('2026-10-10T05:48:00Z', now), '12 分钟前');
  assert.equal(formatRelative('2026-10-10T03:00:00Z', now), '3 小时前');
  assert.equal(formatRelative('2026-10-08T05:00:00Z', now), '2 天前');
});

test('mean time to clear counts only alarms that have cleared', () => {
  assert.equal(meanTimeToClear([
    alarm('2026-10-10T01:00:00Z', 'MINOR', { condition: 'CLEARED', clearedAt: '2026-10-10T01:30:00Z' }),
    alarm('2026-10-10T02:00:00Z', 'MINOR', { condition: 'CLEARED', clearedAt: '2026-10-10T03:30:00Z' }),
    alarm('2026-10-10T04:00:00Z', 'MINOR', { condition: 'ACTIVE' }),
  ]), 60 * 60_000);
  assert.equal(meanTimeToClear([alarm('2026-10-10T04:00:00Z', 'MINOR', { condition: 'ACTIVE' })]), null);
});
