import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  buildReadinessSummary,
  parseRecommendation,
  type PublishedRecommendation,
} from '../apps/hvac-web/src/features/optimization/optimization-model.ts';

const publishedFixture = JSON.parse(
  readFileSync(new URL('../apps/hvac-web/e2e/fixtures/optimization-latest.json', import.meta.url), 'utf8'),
) as PublishedRecommendation;

test('buildReadinessSummary reports 6/6 ready with remote setpoint dispatch when writable setpoint is active', () => {
  const recommendation = parseRecommendation(publishedFixture);
  assert.equal(recommendation.id, '01a12bdc-97d7-7e50-bc97-f14cb86800ba');
  assert.equal(recommendation.restoreSupplyTempC, 7.06);

  const summary = buildReadinessSummary({
    recommendation,
    preparation: { status: 'PREPARED', reason: null, firedAt: '2026-10-11T16:20:00Z' },
    hasWritableSetpoint: true,
  });

  assert.equal(summary.readyCount, 6);
  assert.equal(summary.totalCount, 6);
  assert.equal(summary.headline, '前置与安全条件：6/6 就绪 · 可远程下发设定点');
  assert.equal(summary.items.length, 6);
  assert.deepEqual(
    summary.items.map((item) => [item.key, item.status]),
    [
      ['supply_temp', 'ready'],
      ['zone_comfort', 'ready'],
      ['maintenance_locks', 'ready'],
      ['live_revalidation', 'ready'],
      ['forecast_and_tariff', 'ready'],
      ['execution_channel', 'ready'],
    ],
  );
});

test('buildReadinessSummary reports 5/6 ready with manual work-order channel when site has no writable setpoint', () => {
  const recommendation = parseRecommendation(publishedFixture);
  const summary = buildReadinessSummary({
    recommendation,
    preparation: null,
    hasWritableSetpoint: false,
  });

  assert.equal(summary.readyCount, 5);
  assert.equal(summary.totalCount, 6);
  assert.equal(summary.headline, '前置与安全条件：5/6 就绪 · 仅人工调温工单');
  const channel = summary.items.find((item) => item.key === 'execution_channel');
  assert.equal(channel?.status, 'manual');
  assert.match(channel?.detail ?? '', /人工调温工单/);
});

test('buildReadinessSummary surfaces exact missing preparation input without fabricating green checks', () => {
  const summary = buildReadinessSummary({
    recommendation: null,
    preparation: { status: 'FAILED', reason: 'ZONE_TEMPERATURE_MISSING', firedAt: '2026-10-30T04:20:00Z' },
    hasWritableSetpoint: false,
  });

  assert.equal(summary.readyCount, 0);
  assert.equal(summary.headline, '前置与安全条件：读不到可用的实时室内温度 · 仅人工调温工单');
  const comfort = summary.items.find((item) => item.key === 'zone_comfort');
  const live = summary.items.find((item) => item.key === 'live_revalidation');
  assert.equal(comfort?.status, 'blocked');
  assert.equal(live?.status, 'blocked');
  const maintenance = summary.items.find((item) => item.key === 'maintenance_locks');
  assert.equal(maintenance?.status, 'unknown');
});
