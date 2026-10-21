import assert from 'node:assert/strict';
import test from 'node:test';
import { siteDay, siteWhen, todayStart } from '../miniprogram/lib/today.ts';

// Site-local day buckets as the Energy analytics owner returns them for the last 48 hours (UTC+8).
const day = (start: string, end: string, energyKWh: number) => ({ periodStart: start, periodEnd: end, energyKWh });
const days = [
  day('2026-10-17T16:00:00Z', '2026-10-18T16:00:00Z', 900),
  day('2026-10-18T16:00:00Z', '2026-10-19T16:00:00Z', 1634),
  day('2026-10-19T16:00:00Z', '2026-10-20T16:00:00Z', 820),
];

test('today starts where the day bucket holding now starts, without any time zone arithmetic', () => {
  assert.equal(todayStart(days, new Date('2026-10-20T06:30:00Z')), '2026-10-19T16:00:00Z');
});

test('just after midnight, before the owner has a bucket for today, today starts where yesterday ended', () => {
  assert.equal(todayStart(days.slice(0, 2), new Date('2026-10-19T16:05:00Z')), '2026-10-19T16:00:00Z');
  assert.equal(todayStart([], new Date('2026-10-19T16:05:00Z')), null);
});

test('a moment reads as the Site\'s own date and clock, east or west of Greenwich', () => {
  // Shanghai: 1 October began at 16:00 UTC on 30 September.
  assert.equal(siteWhen('2026-09-29T08:26:00Z', '2026-09-30T16:00:00Z'), '9/29 16:26');
  assert.equal(siteWhen('2026-09-30T23:59:00Z', '2026-09-30T16:00:00Z'), '今天 07:59');
  assert.deepEqual(siteDay('2026-09-30T15:59:00Z', '2026-09-30T16:00:00Z'), { day: '昨天', time: '23:59' });
  // New York in October (UTC−4): 1 October began at 04:00 UTC.
  assert.equal(siteWhen('2026-10-01T02:30:00Z', '2026-10-01T04:00:00Z'), '昨天 22:30');
  assert.deepEqual(siteDay('2026-09-28T12:00:00Z', '2026-10-01T04:00:00Z'), { day: '9 月 28 日', time: '08:00' });
});
