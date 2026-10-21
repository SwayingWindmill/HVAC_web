import assert from 'node:assert/strict';
import test from 'node:test';
import { todayStart } from '../miniprogram/lib/today.ts';

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
