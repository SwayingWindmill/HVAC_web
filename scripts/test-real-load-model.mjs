import assert from 'node:assert/strict';
import test from 'node:test';
import { demandSummary, demandWindows, quarterHours } from '../apps/hvac-web/src/features/load/demand-model.ts';
import { forecastBetween, forecastPeak, hourlyForecast, parseLoadForecast } from '../apps/hvac-web/src/features/load/forecast-model.ts';
import { expensiveSpans, forecastEnergy, loadCurveRows, loadDays, meteredHour, nearestQuarter } from '../apps/hvac-web/src/features/load/load-model.ts';

const zone = 'Asia/Shanghai';
const bucket = (start, average, minutes = 15) => ({
  telemetryKey: 'active_power', pointId: 'p', pointRevision: 1, pointType: 'TELEMETRY', unit: 'kW',
  periodStart: start, periodEnd: new Date(Date.parse(start) + minutes * 60_000).toISOString(),
  quality: {}, completeness: 1, gauge: { average, minimum: average, maximum: average, first: average, last: average, sampleCount: 90 },
});

const origin = '2026-10-11T04:07:00Z';
const point = (minutes, value, band = 10, quality = 'VALID') => ({
  forecast_for: new Date(Date.parse(origin) + minutes * 60_000).toISOString(),
  value, lower_bound: quality === 'VALID' ? value - band : null, upper_bound: quality === 'VALID' ? value + band : null,
  unit: 'kW', quality,
});
const response = (points) => ({
  snapshot: { forecastOrigin: origin, quality: points[0]?.quality ?? 'VALID', resultCount: points.length },
  points,
});

// Peak 08:00–11:00 Site time, the only period with a demand rate.
const slices = [['VALLEY', 0, 360], ['FLAT', 360, 480], ['PEAK', 480, 660], ['FLAT', 660, 1320], ['VALLEY', 1320, 1440]];
const rates = { PEAK: [1.2, 38], FLAT: [0.72, 0], VALLEY: [0.32, 0] };
const tariff = {
  siteId: '01990000-5000-7000-8000-000000000001', displayName: '两部制分时电价', version: 1, effectiveFrom: '2026-01-01T00:00:00.000Z',
  timezone: zone, currency: 'CNY',
  periods: ['WEEKDAY', 'WEEKEND'].flatMap((dayType) => slices.map(([periodCode, startMinute, endMinute]) => ({
    periodCode, dayType, startMinute, endMinute, energyRatePerKWh: rates[periodCode][0], demandRatePerKW: rates[periodCode][1],
  }))),
};

test('the month and yesterday are read in chunks the history owner accepts', () => {
  // 22:00 on 11 October, Site time.
  const windows = demandWindows(new Date('2026-10-11T14:00:00Z'), zone);
  assert.deepEqual(windows.map((window) => [window.from.toISOString(), window.to.toISOString()]), [
    ['2026-09-30T16:00:00.000Z', '2026-10-10T16:00:00.000Z'],
    ['2026-10-10T16:00:00.000Z', '2026-10-11T14:00:00.000Z'],
  ]);
  // On the first of the month yesterday still has to be read for the comparison.
  assert.equal(demandWindows(new Date('2026-10-01T02:00:00Z'), zone)[0].from.toISOString(), '2026-09-29T16:00:00.000Z');
});

test('the running quarter-hour is shown but never counted as a peak', () => {
  const now = new Date('2026-10-11T14:05:00Z');
  const hours = quarterHours([
    bucket('2026-10-11T13:45:00Z', 200),
    { ...bucket('2026-10-11T14:00:00Z', 900), periodEnd: now.toISOString() },
  ], now);
  assert.deepEqual(hours.map((hour) => hour.complete), [true, false]);
  const summary = demandSummary(hours, now, zone);
  assert.equal(summary.current.demandKW, 900);
  assert.deepEqual(summary.today, { at: '2026-10-11T13:45:00.000Z', demandKW: 200 });
});

test('peaks are read per Site day, and the load factor is today\'s average over its peak', () => {
  const now = new Date('2026-10-11T14:00:00Z');
  const hours = quarterHours([
    bucket('2026-10-09T20:00:00Z', 150), // 10 Oct, Site time
    bucket('2026-10-10T03:00:00Z', 260),
    bucket('2026-10-10T16:00:00Z', 100), // 11 Oct
    bucket('2026-10-11T04:00:00Z', 300),
  ], now);
  const summary = demandSummary(hours, now, zone);
  assert.deepEqual(summary.yesterday, { at: '2026-10-10T03:00:00.000Z', demandKW: 260 });
  assert.deepEqual(summary.month, { at: '2026-10-11T04:00:00.000Z', demandKW: 300 });
  assert.equal(summary.todayLoadFactor, 200 / 300);
  assert.deepEqual(summary.dailyPeaks.map((day) => [day.day, day.peak?.demandKW]), [['2026-10-10', 260], ['2026-10-11', 300]]);
});

test('a published forecast reads as quarter-hour load with its band, highest point first found', () => {
  const forecast = parseLoadForecast(response([point(30, 240), point(15, 200), point(45, 240, 0, 'FALLBACK')]));
  assert.equal(forecast.origin, origin);
  assert.deepEqual(forecast.points.map((item) => [item.at, item.loadKW, item.lowerKW, item.upperKW]), [
    ['2026-10-11T04:22:00.000Z', 200, 190, 210],
    ['2026-10-11T04:37:00.000Z', 240, 230, 250],
    ['2026-10-11T04:52:00.000Z', 240, null, null],
  ]);
  assert.deepEqual(forecastPeak(forecast.points), { at: '2026-10-11T04:37:00.000Z', loadKW: 240 });
  assert.equal(forecastPeak([]), null);
  assert.deepEqual(forecastBetween(forecast, Date.parse('2026-10-11T04:30:00Z'), Date.parse('2026-10-11T04:52:00Z')).map((item) => item.loadKW), [240]);
});

test('the forecast reads hour by hour in Site-local time, with a band only where every point has one', () => {
  // 04:22Z, 04:37Z and 04:52Z fall in the 12:00 hour in Shanghai; 05:07Z starts 13:00.
  const forecast = parseLoadForecast(response([point(15, 200), point(30, 240), point(45, 220), point(60, 100, 0, 'FALLBACK')]));
  assert.deepEqual(hourlyForecast(forecast.points, zone), [
    { label: '12:00', start: '2026-10-11T04:22:00.000Z', loadKW: 220, lowerKW: 210, upperKW: 230 },
    { label: '13:00', start: '2026-10-11T05:07:00.000Z', loadKW: 100, lowerKW: null, upperKW: null },
  ]);
});

test('forecast instants are read at the nearest quarter-hour', () => {
  assert.equal(new Date(nearestQuarter('2026-10-12T00:07:13Z')).toISOString(), '2026-10-12T00:00:00.000Z');
  assert.equal(new Date(nearestQuarter('2026-10-12T00:08:00Z')).toISOString(), '2026-10-12T00:15:00.000Z');
});

test('today and tomorrow share one quarter-hour grid: metered load, yesterday under today, and the forecast with its band', () => {
  // 12:00 on 11 October, Site time.
  const now = new Date('2026-10-11T04:00:00Z');
  const days = loadDays(now, zone);
  assert.deepEqual([days.yesterday, days.today, days.tomorrow, days.end].map((at) => new Date(at).toISOString()), [
    '2026-10-09T16:00:00.000Z', '2026-10-10T16:00:00.000Z', '2026-10-11T16:00:00.000Z', '2026-10-12T16:00:00.000Z',
  ]);
  const quarters = quarterHours([bucket('2026-10-09T16:15:00Z', 150), bucket('2026-10-10T16:15:00Z', 180)], now);
  const ahead = [{ at: '2026-10-11T16:30:00.000Z', loadKW: 210, lowerKW: 200, upperKW: 220, quality: 'VALID' }];
  const rows = loadCurveRows(quarters, ahead, days);
  assert.equal(rows.length, 192);
  assert.deepEqual(rows[1], { at: Date.parse('2026-10-10T16:15:00Z'), actualKW: 180, previousKW: 150, forecastKW: null, band: null });
  // Tomorrow has no yesterday line: today's own curve is beside it.
  assert.deepEqual(rows[98], { at: Date.parse('2026-10-11T16:30:00Z'), actualKW: null, previousKW: null, forecastKW: 210, band: [200, 220] });
});

test('a metered hour averages its complete quarter-hours only', () => {
  const now = new Date('2026-10-11T04:40:00Z');
  const quarters = quarterHours([
    bucket('2026-10-11T04:00:00Z', 100), bucket('2026-10-11T04:15:00Z', 200),
    { ...bucket('2026-10-11T04:30:00Z', 900), periodEnd: now.toISOString() },
  ], now);
  assert.equal(meteredHour(quarters, Date.parse('2026-10-11T04:00:00Z')), 150);
  assert.equal(meteredHour(quarters, Date.parse('2026-10-11T05:00:00Z')), null);
});

test('peak-priced hours are shaded on both days, and the forecast says how much of its energy falls in them', () => {
  const days = loadDays(new Date('2026-10-11T04:00:00Z'), zone);
  assert.deepEqual(expensiveSpans(tariff, days, zone).map((span) => [new Date(span.from).toISOString(), new Date(span.to).toISOString(), span.code]), [
    ['2026-10-11T00:00:00.000Z', '2026-10-11T03:00:00.000Z', 'PEAK'],
    ['2026-10-12T00:00:00.000Z', '2026-10-12T03:00:00.000Z', 'PEAK'],
  ]);
  // 09:00 Site time is peak, 12:00 flat; each quarter-hour point holds its load for 15 minutes.
  const points = [
    { at: '2026-10-12T01:00:00.000Z', loadKW: 300, lowerKW: null, upperKW: null, quality: 'VALID' },
    { at: '2026-10-12T04:00:00.000Z', loadKW: 100, lowerKW: null, upperKW: null, quality: 'VALID' },
  ];
  assert.deepEqual(forecastEnergy(points, tariff, zone), { energyKWh: 100, expensiveShare: 0.75 });
  assert.deepEqual(forecastEnergy(points, undefined, zone), { energyKWh: 100, expensiveShare: null });
});
