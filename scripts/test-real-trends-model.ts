import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildTimeSlots,
  fillGaugeSlots,
  fillScalarSlots,
} from '../apps/hvac-web/src/features/operations/trends/timeline.ts';

test('buildTimeSlots generates contiguous slots for MINUTE, FIVE_MINUTE, QUARTER_HOUR, HOUR and site-local DAY', () => {
  const minuteSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:02:00Z'),
    toMs: Date.parse('2026-10-11T01:05:00Z'),
    granularity: 'MINUTE',
    timeZone: 'Asia/Shanghai',
  });
  assert.deepEqual(
    minuteSlots.map((slot) => new Date(slot.startMs).toISOString()),
    [
      '2026-10-11T01:02:00.000Z',
      '2026-10-11T01:03:00.000Z',
      '2026-10-11T01:04:00.000Z',
    ],
  );

  const fiveMinSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:00:00Z'),
    toMs: Date.parse('2026-10-11T01:15:00Z'),
    granularity: 'FIVE_MINUTE',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(fiveMinSlots.length, 3);
  assert.equal(fiveMinSlots[1].startMs - fiveMinSlots[0].startMs, 5 * 60_000);

  const quarterSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T01:00:00Z'),
    toMs: Date.parse('2026-10-11T02:00:00Z'),
    granularity: 'QUARTER_HOUR',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(quarterSlots.length, 4);

  const hourSlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T00:00:00Z'),
    toMs: Date.parse('2026-10-11T03:00:00Z'),
    granularity: 'HOUR',
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(hourSlots.length, 3);

  // Asia/Shanghai midnight is 16:00Z of the previous UTC day.
  const daySlots = buildTimeSlots({
    fromMs: Date.parse('2026-10-09T16:00:00Z'),
    toMs: Date.parse('2026-10-11T16:00:00Z'),
    granularity: 'DAY',
    timeZone: 'Asia/Shanghai',
  });
  assert.deepEqual(
    daySlots.map((slot) => [new Date(slot.startMs).toISOString(), new Date(slot.endMs).toISOString()]),
    [
      ['2026-10-09T16:00:00.000Z', '2026-10-10T16:00:00.000Z'],
      ['2026-10-10T16:00:00.000Z', '2026-10-11T16:00:00.000Z'],
    ],
  );
});

test('fillGaugeSlots and fillScalarSlots leave null in missing outage buckets instead of bridging or zero-filling', () => {
  const slots = buildTimeSlots({
    fromMs: Date.parse('2026-10-11T10:00:00Z'),
    toMs: Date.parse('2026-10-11T14:00:00Z'),
    granularity: 'HOUR',
    timeZone: 'Asia/Shanghai',
  });

  const gauge = fillGaugeSlots(slots, [
    { periodStart: '2026-10-11T10:00:00Z', average: 182.4, minimum: 170.0, maximum: 195.2 },
    { periodStart: '2026-10-11T13:00:00Z', average: 190.1, minimum: 185.0, maximum: 198.0 },
  ]);

  assert.deepEqual(
    gauge.map((row) => ({
      at: new Date(row.startMs).toISOString(),
      average: row.average,
      range: row.minimum !== null && row.maximum !== null ? [row.minimum, row.maximum] : null,
    })),
    [
      { at: '2026-10-11T10:00:00.000Z', average: 182.4, range: [170.0, 195.2] },
      { at: '2026-10-11T11:00:00.000Z', average: null, range: null },
      { at: '2026-10-11T12:00:00.000Z', average: null, range: null },
      { at: '2026-10-11T13:00:00.000Z', average: 190.1, range: [185.0, 198.0] },
    ],
  );

  const scalar = fillScalarSlots(slots, [
    { periodStart: '2026-10-11T10:00:00Z', value: 4.85 },
    { periodStart: '2026-10-11T13:00:00Z', value: 5.12 },
  ]);
  assert.deepEqual(scalar.map((row) => row.value), [4.85, null, null, 5.12]);
});
