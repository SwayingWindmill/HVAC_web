import assert from 'node:assert/strict';
import test from 'node:test';
import { forecastChartRows, forecastSummary, nextDay, parseLoadForecast } from '../apps/hvac-web/src/features/forecast/forecast-model.ts';

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

test('a published forecast reads as quarter-hour load with its band', () => {
  const forecast = parseLoadForecast(response([point(15, 200), point(30, 240)]));
  assert.equal(forecast.origin, origin);
  assert.equal(forecast.quality, 'VALID');
  assert.deepEqual(forecast.points.map((item) => [item.at, item.loadKW, item.lowerKW, item.upperKW]), [
    ['2026-10-11T04:22:00.000Z', 200, 190, 210],
    ['2026-10-11T04:37:00.000Z', 240, 230, 250],
  ]);
});

test('the summary names the peak and the energy the forecast adds up to', () => {
  const forecast = parseLoadForecast(response([point(15, 200), point(30, 240), point(45, 120)]));
  const summary = forecastSummary(forecast);
  assert.deepEqual(summary.peak, { at: '2026-10-11T04:37:00.000Z', loadKW: 240 });
  assert.equal(summary.trough.loadKW, 120);
  // Each quarter-hour point holds its load for 15 minutes.
  assert.equal(summary.energyKWh, (200 + 240 + 120) / 4);
});

test('actual hours and forecast quarter-hours share one time axis, and the band is drawn only where the model gives one', () => {
  const forecast = parseLoadForecast(response([point(15, 200), point(30, 240, 0, 'FALLBACK')]));
  const actual = [{ periodStart: '2026-10-11T03:00:00.000Z', electricityKWh: 180 }, { periodStart: '2026-10-11T04:00:00.000Z', electricityKWh: null }];
  assert.deepEqual(forecastChartRows(actual, forecast), [
    { at: Date.parse('2026-10-11T03:00:00.000Z'), actualKW: 180, forecastKW: null, band: null },
    // An hour without a reading stays as a gap in the actual line.
    { at: Date.parse('2026-10-11T04:00:00.000Z'), actualKW: null, forecastKW: null, band: null },
    { at: Date.parse('2026-10-11T04:22:00.000Z'), actualKW: null, forecastKW: 200, band: [190, 210] },
    { at: Date.parse('2026-10-11T04:37:00.000Z'), actualKW: null, forecastKW: 240, band: null },
  ]);
});

test('the forecast reads hour by hour in Site-local time, with the peak hour marked', async () => {
  const { hourlyForecast } = await import('../apps/hvac-web/src/features/forecast/forecast-model.ts');
  // 04:22Z and 04:37Z fall in the 12:00 hour in Shanghai, 04:52Z too; 05:07Z starts 13:00.
  const forecast = parseLoadForecast(response([point(15, 200), point(30, 240), point(45, 220), point(60, 100, 0, 'FALLBACK')]));
  assert.deepEqual(hourlyForecast(forecast, 'Asia/Shanghai'), [
    { label: '12:00', start: '2026-10-11T04:22:00.000Z', loadKW: 220, lowerKW: 210, upperKW: 230, peak: true },
    { label: '13:00', start: '2026-10-11T05:07:00.000Z', loadKW: 100, lowerKW: null, upperKW: null, peak: false },
  ]);
});

test('the page reads the next 24 hours of a forecast that reaches further ahead', () => {
  const forecast = parseLoadForecast(response([point(15, 200), point(24 * 60, 210), point(24 * 60 + 15, 220), point(48 * 60, 230)]));
  const day = nextDay(forecast);
  assert.deepEqual(day.points.map((item) => item.loadKW), [200, 210]);
  assert.equal(day.origin, forecast.origin);
});
