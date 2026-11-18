import assert from 'node:assert/strict';
import test from 'node:test';
import { deriveHourlyTodayStart, siteDay, siteDayBounds, siteWhen, todayStart } from '../miniprogram/lib/today.ts';

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

const hourBucket = (startIso: string, energyKWh = 100) => ({
  periodStart: startIso,
  periodEnd: new Date(Date.parse(startIso) + 3_600_000).toISOString(),
  energyKWh,
});

function withBrokenIntlTimeZone<T>(run: () => T): T {
  const original = Intl.DateTimeFormat;
  Intl.DateTimeFormat = new Proxy(original, {
    construct(target, args: [Intl.LocalesArgument?, Intl.DateTimeFormatOptions?]) {
      return new target('en-US', { ...args[1], timeZone: 'UTC' });
    },
    apply(target, _thisArg, args: [Intl.LocalesArgument?, Intl.DateTimeFormatOptions?]) {
      return target('en-US', { ...args[1], timeZone: 'UTC' });
    },
  });
  try {
    return run();
  } finally {
    Intl.DateTimeFormat = original;
  }
}

test('deriveHourlyTodayStart resolves Site midnight across midnight boundaries and returns null for empty sites', () => {
  const todayMidnight = '2026-10-19T16:00:00.000Z';
  const yesterdayHour = hourBucket('2026-10-19T02:00:00.000Z', 120);
  const todayHour = hourBucket('2026-10-20T05:00:00.000Z', 140);
  const staleHour = hourBucket('2026-10-18T15:00:00.000Z', 90);

  assert.equal(deriveHourlyTodayStart([yesterdayHour, todayHour], new Date('2026-10-20T06:30:00.000Z'), 'Asia/Shanghai'), todayMidnight);
  assert.equal(deriveHourlyTodayStart([yesterdayHour], new Date('2026-10-19T16:05:00.000Z'), 'Asia/Shanghai'), todayMidnight);
  assert.equal(deriveHourlyTodayStart([yesterdayHour], new Date('2026-10-19T16:00:00.000Z'), 'Asia/Shanghai'), todayMidnight);
  assert.equal(deriveHourlyTodayStart([yesterdayHour], new Date('2026-10-20T15:59:59.000Z'), 'Asia/Shanghai'), todayMidnight);
  assert.equal(deriveHourlyTodayStart([], new Date('2026-10-20T06:30:00.000Z'), 'Asia/Shanghai'), null);
  assert.equal(deriveHourlyTodayStart([staleHour], new Date('2026-10-20T06:30:00.000Z'), 'Asia/Shanghai'), null);
  assert.deepEqual(siteDayBounds(new Date('2026-10-20T20:00:00.000Z'), 'Asia/Kolkata'), {
    todayStart: '2026-10-20T18:30:00.000Z',
    yesterdayStart: '2026-10-19T18:30:00.000Z',
  });
});

test('siteDayBounds handles 23h spring-forward and 25h fall-back DST transitions', () => {
  assert.deepEqual(siteDayBounds(new Date('2026-03-09T12:00:00.000Z'), 'America/New_York'), {
    todayStart: '2026-03-09T04:00:00.000Z',
    yesterdayStart: '2026-03-08T05:00:00.000Z',
  });
  assert.deepEqual(siteDayBounds(new Date('2026-11-02T04:30:00.000Z'), 'America/New_York'), {
    todayStart: '2026-11-01T04:00:00.000Z',
    yesterdayStart: '2026-10-31T04:00:00.000Z',
  });
  assert.deepEqual(siteDayBounds(new Date('2026-11-02T12:00:00.000Z'), 'America/New_York'), {
    todayStart: '2026-11-02T05:00:00.000Z',
    yesterdayStart: '2026-11-01T04:00:00.000Z',
  });
});

test('deriveHourlyTodayStart keeps fixed-offset zones working and returns null for DST zones when Intl ignores timeZone', () => {
  withBrokenIntlTimeZone(() => {
    assert.equal(deriveHourlyTodayStart([hourBucket('2026-10-20T05:00:00.000Z')], new Date('2026-10-20T06:30:00.000Z'), 'Asia/Shanghai'), '2026-10-19T16:00:00.000Z');
    assert.equal(siteDayBounds(new Date('2026-11-02T12:00:00.000Z'), 'America/New_York'), null);
    assert.equal(deriveHourlyTodayStart([hourBucket('2026-11-02T10:00:00.000Z')], new Date('2026-11-02T12:00:00.000Z'), 'America/New_York'), null);
  });
});
