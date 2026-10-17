import assert from 'node:assert/strict';
import test from 'node:test';
import { daySchedule, demandCharges } from '../apps/hvac-web/src/features/tariff/tariff-model.ts';

const zone = 'Asia/Shanghai';
const rates = { PEAK: [1.2, 38], FLAT: [0.72, 0], VALLEY: [0.32, 0] };
const slices = [['VALLEY', 0, 360], ['FLAT', 360, 480], ['PEAK', 480, 660], ['FLAT', 660, 1320], ['VALLEY', 1320, 1440]];
const tariff = {
  siteId: '01990000-5000-7000-8000-000000000001', displayName: '两部制分时电价', version: 1, effectiveFrom: '2026-01-01T00:00:00.000Z',
  timezone: zone, currency: 'CNY',
  periods: ['WEEKDAY', 'WEEKEND'].flatMap((dayType) => slices.map(([periodCode, startMinute, endMinute]) => ({
    periodCode, dayType, startMinute, endMinute, energyRatePerKWh: rates[periodCode][0], demandRatePerKW: rates[periodCode][1],
  }))),
};
// Site-local hour h of 12 October 2026, a Monday.
const at = (hour, minute = 0) => new Date(Date.UTC(2026, 9, 11, 16 + hour, minute)).toISOString();

test('a day is priced by its own day type, in clock order', () => {
  const weekendOnly = { ...tariff, periods: tariff.periods.filter((period) => period.dayType === 'WEEKEND') };
  assert.deepEqual(daySchedule(tariff, new Date(at(10)), zone).map((period) => period.periodCode), ['VALLEY', 'FLAT', 'PEAK', 'FLAT', 'VALLEY']);
  // Monday has no weekday periods in this tariff, Sunday (the day before) does.
  assert.deepEqual(daySchedule(weekendOnly, new Date(at(10)), zone), []);
  assert.equal(daySchedule(weekendOnly, new Date(at(-14)), zone).length, 5);
});

test('demand is charged per period on its highest complete quarter-hour since the cycle began', () => {
  const quarter = (hour, minute, demandKW, complete = true) => ({ start: at(hour, minute), demandKW, complete });
  const charges = demandCharges([
    quarter(3, 0, 900), // valley carries no demand rate
    quarter(9, 15, 400),
    quarter(10, 30, 520),
    quarter(10, 45, 700, false), // still running
    quarter(-30, 0, 990), // before the cycle
  ], tariff, zone, new Date(at(0)));
  assert.equal(charges.length, 1);
  const [peak] = charges;
  assert.equal(peak.code, 'PEAK');
  assert.equal(peak.ratePerKW, 38);
  assert.deepEqual(peak.peak, { at: at(10, 30), demandKW: 520 });
  assert.equal(peak.charge, 520 * 38);
});

test('a period with a demand rate but no complete quarter-hour yet has no charge', () => {
  assert.deepEqual(demandCharges([], tariff, zone, new Date(at(0))), [{ code: 'PEAK', ratePerKW: 38, peak: null, charge: null }]);
});
