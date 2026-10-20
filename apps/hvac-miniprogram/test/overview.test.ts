import assert from 'node:assert/strict';
import test from 'node:test';
import { presentOverview } from '../miniprogram/lib/overview.ts';

const today = { electricityKWh: 1691, coolingKWh: 7657.32, cop: 4.528, costCNY: 1423.48 };
const change = { electricity: 0.156, cooling: 0.137, cop: -0.016, cost: 0.3 };
const recommendation = {
  createdAt: '2026-10-20T00:00:00Z',
  baseline: { supplyTempC: 7 }, candidate: { supplyTempC: 8 },
  expectedImpact: { energySavingKWhPerDay: 180.4, costSavingPerDay: 210.2 },
  risk: { level: 'LOW' as const, comfortMarginC: 1.2 },
};

test('the overview leads with savings and what more could be saved, then today as the web shows it, then what needs handling', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: { cost: 8640, kWh: 7350, rate: 0.142 }, why: null, measures: [
      { id: 'm1', name: '冷冻水供水温度调高 1 °C', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 20, avoidedKWh: 5020, avoidedCost: 5910 } },
    ] } },
    recommendation: { status: 'ok', latest: recommendation },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 5, complete: true },
  });
  assert.deepEqual(view.savings, {
    state: 'saved', amount: '¥8,640', detail: '少用电 7,350 kWh · 比不做措施少 14.2%',
    measures: [{ id: 'm1', name: '冷冻水供水温度调高 1 °C', amount: '¥5,910', detail: '本月统计 20 天 · 少用电 5,020 kWh' }],
  });
  assert.deepEqual(view.recommendation, {
    state: 'ready', title: '冷冻水供水 7.0 → 8.0 °C，预计每天再省 ¥210', detail: '少用电 180 kWh/天', risk: { text: '低风险', tone: 'good' },
  });
  // The web shows electricity and cooling to one decimal, efficiency to two, cost in whole yuan.
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value, each.unit, each.badge?.text, each.badge?.tone]), [
    ['空调用电', '1,691.0', 'kWh', '▲ 15.6%', 'bad'],
    ['电费（估算）', '¥1,423', '', '▲ 30.0%', 'bad'],
    ['供冷量', '7,657.3', 'kWh', '▲ 13.7%', 'neutral'],
    ['综合能效', '4.53', '', '▼ 1.6%', 'bad'],
  ]);
  assert.equal(view.alarms, '5');
});

test('missing data says so: no tariff hides the cost, no measure says what to do, an unreadable section says it could not be read', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today: { electricityKWh: null, coolingKWh: 12, cop: null, costCNY: null }, change: { electricity: null, cooling: null, cop: null, cost: null } },
    alarms: { status: 'ok', active: 100, complete: false },
  });
  assert.equal(view.savings.amount, '—');
  assert.equal(view.savings.detail, '还没有在统计的节能措施。在网页「能源分析 → 节能项目」登记一项措施，实施满 24 小时后，第二天起这里显示省了多少钱、多少电。');
  assert.equal(view.recommendation.state, 'none');
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value]), [['空调用电', '—'], ['供冷量', '12.0'], ['综合能效', '—']]);
  assert.equal(view.alarms, '100+');
  assert.equal(presentOverview({ savings: { status: 'error' }, recommendation: { status: 'error' }, energy: { status: 'error' }, alarms: { status: 'error' } }).energy.state, 'error');
});

test('a Site with no electricity readings says so, instead of reporting a failure', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'empty' },
    alarms: { status: 'ok', active: 0, complete: true },
  });
  assert.deepEqual(view.energy, { state: 'empty', metrics: [], note: '这个站点今天和昨天都没有用电读数。' });
});
