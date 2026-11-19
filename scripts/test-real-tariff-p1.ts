import assert from 'node:assert/strict';
import test from 'node:test';
import { PERIOD_COLOR } from '../apps/hvac-web/src/features/tariff/period-presentation.ts';
import {
  daySchedule,
  periodAt,
  PERIOD_LABEL,
  PERIOD_ORDER,
  periodRanges,
  sumCosts,
  type PeriodCode,
  type SiteTariffPeriod,
  type SiteTariffWithCalendar,
} from '../apps/hvac-web/src/features/tariff/tariff-model.ts';

const TIMEZONE = 'Asia/Shanghai';
const SITE_ID = '01990000-5000-7000-8000-000000000001';

function buildFiveTierSlices(dayType: SiteTariffPeriod['dayType'], rateShift = 0): SiteTariffPeriod[] {
  return [
    {
      periodCode: 'DEEP_VALLEY',
      dayType,
      startMinute: 0,
      endMinute: 240,
      energyRatePerKWh: 0.18 + rateShift,
      demandRatePerKW: 0,
    },
    {
      periodCode: 'VALLEY',
      dayType,
      startMinute: 240,
      endMinute: 480,
      energyRatePerKWh: 0.32 + rateShift,
      demandRatePerKW: 0,
    },
    {
      periodCode: 'FLAT',
      dayType,
      startMinute: 480,
      endMinute: 660,
      energyRatePerKWh: 0.72 + rateShift,
      demandRatePerKW: 0,
    },
    {
      periodCode: 'PEAK',
      dayType,
      startMinute: 660,
      endMinute: 840,
      energyRatePerKWh: 1.15 + rateShift,
      demandRatePerKW: 38,
    },
    {
      periodCode: 'SUPER_PEAK',
      dayType,
      startMinute: 840,
      endMinute: 960,
      energyRatePerKWh: 1.48 + rateShift,
      demandRatePerKW: 0,
    },
    {
      periodCode: 'FLAT',
      dayType,
      startMinute: 960,
      endMinute: 1440,
      energyRatePerKWh: 0.72 + rateShift,
      demandRatePerKW: 0,
    },
  ];
}

function makeTariff(
  periods: readonly SiteTariffPeriod[],
  holidayDates?: Readonly<Record<string, string>>,
): SiteTariffWithCalendar {
  return {
    siteId: SITE_ID,
    displayName: '五段分时电价（含深谷）',
    version: 2,
    effectiveFrom: '2026-01-01T00:00:00.000Z',
    timezone: TIMEZONE,
    currency: 'CNY',
    periods,
    ...(holidayDates ? { holidayDates } : {}),
  };
}

test('5-tier TOU defines DEEP_VALLEY presentation, label, and hottest-first order', () => {
  assert.deepEqual(PERIOD_ORDER, ['SUPER_PEAK', 'PEAK', 'FLAT', 'VALLEY', 'DEEP_VALLEY']);
  assert.equal(PERIOD_LABEL.DEEP_VALLEY, '深谷');
  assert.equal(PERIOD_COLOR.DEEP_VALLEY, 'var(--information)');

  const expectedCodes: readonly PeriodCode[] = ['SUPER_PEAK', 'PEAK', 'FLAT', 'VALLEY', 'DEEP_VALLEY'];
  for (const code of expectedCodes) {
    assert.ok(PERIOD_LABEL[code].length > 0);
    assert.ok(PERIOD_COLOR[code].startsWith('var(--'));
  }
});

test('sumCosts orders 5-tier TOU breakdown including DEEP_VALLEY hottest-first', () => {
  const result = sumCosts([
    {
      electricityKWh: 1_500,
      cost: {
        amountCNY: 1_016,
        unpricedKWh: 50,
        periods: [
          { periodCode: 'DEEP_VALLEY', energyKWh: 300, amountCNY: 54 },
          { periodCode: 'FLAT', energyKWh: 400, amountCNY: 288 },
          { periodCode: 'SUPER_PEAK', energyKWh: 100, amountCNY: 148 },
          { periodCode: 'VALLEY', energyKWh: 350, amountCNY: 112 },
          { periodCode: 'PEAK', energyKWh: 300, amountCNY: 414 },
        ],
      },
    },
  ]);

  assert.equal(result.energyKWh, 1_500);
  assert.equal(result.total, 1_016);
  assert.equal(result.unpricedKWh, 50);
  assert.deepEqual(
    result.byPeriod.map((entry) => [entry.code, entry.energyKWh, entry.cost]),
    [
      ['SUPER_PEAK', 100, 148],
      ['PEAK', 300, 414],
      ['FLAT', 400, 288],
      ['VALLEY', 350, 112],
      ['DEEP_VALLEY', 300, 54],
    ],
  );
});

test('daySchedule and periodAt resolve DEEP_VALLEY and holidayDates calendar with explicit fallbacks', () => {
  // 2026-10-01 is Thursday (HOLIDAY), 2026-10-10 is Saturday (makeup WEEKDAY), 2026-10-12 is Monday (SPECIAL_DAY)
  const holidayDates: Record<string, string> = {
    '2026-10-01': 'HOLIDAY',
    '2026-10-10': 'WEEKDAY',
    '2026-10-12': 'SPECIAL_DAY',
  };

  // 1. Tariff with explicit HOLIDAY + SPECIAL_DAY slices uses them directly
  const fullTariff = makeTariff(
    [
      ...buildFiveTierSlices('WEEKDAY', 0),
      ...buildFiveTierSlices('WEEKEND', -0.05),
      ...buildFiveTierSlices('HOLIDAY', -0.08),
      ...buildFiveTierSlices('SPECIAL_DAY', 0.1),
    ],
    holidayDates,
  );

  // 02:00 local on 2026-10-01 -> DEEP_VALLEY with HOLIDAY rate (0.18 - 0.08 = 0.10)
  const oct1At02 = new Date('2026-09-30T18:00:00.000Z');
  const holidayPeriod = periodAt(fullTariff, oct1At02, TIMEZONE);
  assert.equal(holidayPeriod?.periodCode, 'DEEP_VALLEY');
  assert.equal(holidayPeriod?.dayType, 'HOLIDAY');
  assert.ok(holidayPeriod && Math.abs(holidayPeriod.energyRatePerKWh - 0.1) < 1e-6);

  // 11:30 local on 2026-10-12 -> PEAK with SPECIAL_DAY rate (1.15 + 0.10 = 1.25)
  const oct12At1130 = new Date('2026-10-12T03:30:00.000Z');
  const specialPeriod = periodAt(fullTariff, oct12At1130, TIMEZONE);
  assert.equal(specialPeriod?.periodCode, 'PEAK');
  assert.equal(specialPeriod?.dayType, 'SPECIAL_DAY');
  assert.ok(specialPeriod && Math.abs(specialPeriod.energyRatePerKWh - 1.25) < 1e-6);

  // Saturday 2026-10-10 overridden as makeup WEEKDAY uses WEEKDAY schedule
  const oct10At02 = new Date('2026-10-09T18:00:00.000Z');
  const makeupPeriod = periodAt(fullTariff, oct10At02, TIMEZONE);
  assert.equal(makeupPeriod?.dayType, 'WEEKDAY');
  assert.equal(makeupPeriod?.periodCode, 'DEEP_VALLEY');
  assert.equal(periodRanges(fullTariff, oct10At02, TIMEZONE).get('DEEP_VALLEY'), '00:00–04:00');

  // 2. Tariff without HOLIDAY/SPECIAL_DAY slices falls back to WEEKEND first
  const weekdayWeekendTariff = makeTariff(
    [...buildFiveTierSlices('WEEKDAY'), ...buildFiveTierSlices('WEEKEND')],
    holidayDates,
  );
  const holidayFallbackToWeekend = daySchedule(weekdayWeekendTariff, oct1At02, TIMEZONE);
  assert.ok(holidayFallbackToWeekend.length > 0);
  assert.equal(holidayFallbackToWeekend[0]?.dayType, 'WEEKEND');

  const specialFallbackToWeekend = daySchedule(weekdayWeekendTariff, oct12At1130, TIMEZONE);
  assert.ok(specialFallbackToWeekend.length > 0);
  assert.equal(specialFallbackToWeekend[0]?.dayType, 'WEEKEND');

  // 3. Tariff with only WEEKDAY slices falls back from HOLIDAY/SPECIAL_DAY -> WEEKEND (empty) -> WEEKDAY
  const weekdayOnlyTariff = makeTariff(buildFiveTierSlices('WEEKDAY'), holidayDates);
  const holidayFallbackToWeekday = daySchedule(weekdayOnlyTariff, oct1At02, TIMEZONE);
  assert.ok(holidayFallbackToWeekday.length > 0);
  assert.equal(holidayFallbackToWeekday[0]?.dayType, 'WEEKDAY');
});
