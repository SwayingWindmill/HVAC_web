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
    alarms: { status: 'ok', active: 5, complete: true, waiting: 3 },
  });
  assert.deepEqual(view.savings, {
    amount: '¥8,640', detail: '少用电 7,350 kWh · 比不做措施少 14.2%',
    measures: [{ id: 'm1', name: '冷冻水供水温度调高 1 °C', amount: '¥5,910', detail: '本月统计 20 天 · 少用电 5,020 kWh' }],
  });
  assert.deepEqual(view.recommendation, {
    title: '冷冻水供水 7.0 → 8.0 °C，预计每天再省 ¥210', detail: '少用电 180 kWh/天', risk: { text: '低风险', tone: 'good' },
  });
  // The web shows electricity and cooling to one decimal, efficiency to two, cost in whole yuan.
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value, each.unit, each.badge?.text, each.badge?.tone]), [
    ['空调用电', '1,691.0', 'kWh', '▲ 15.6%', 'bad'],
    ['电费（估算）', '¥1,423', '', '▲ 30.0%', 'bad'],
    ['供冷量', '7,657.3', 'kWh', '▲ 13.7%', 'neutral'],
    ['综合能效', '4.53', '', '▼ 1.6%', 'bad'],
  ]);
  assert.equal(view.notYet, null);
  // The same count as the red dot on 通知: the alarms happening that no one has acknowledged.
  assert.deepEqual(view.alarms, { count: '5', waiting: '3 条没人确认' });
});

// Regression (#677, 2026-10-21): a Site with neither savings nor a recommendation opened on two
// large cards of explanation, pushing today's figures off the first screen.
test('savings and a recommendation not there yet share one small card below the figures, a line each', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 0, complete: true, waiting: 0 },
  });
  assert.equal(view.savings, null);
  assert.equal(view.recommendation, null);
  assert.deepEqual(view.notYet, {
    rows: [{ label: '本月节省', text: '还没有在统计的节能措施' }, { label: '还能再省', text: '还没有优化建议' }],
    hint: '在电脑网页「能源分析」里登记节能措施、生成优化建议后，这里显示能省多少。',
  });
  assert.deepEqual(view.alarms, { count: '0', waiting: '' });

  const waiting = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'FIRST_DAY_PENDING', measures: [] } },
    recommendation: { status: 'ok', latest: recommendation },
    energy: { status: 'ok', today, change },
    alarms: { status: 'ok', active: 2, complete: true, waiting: 0 },
  });
  assert.equal(waiting.recommendation?.title, '冷冻水供水 7.0 → 8.0 °C，预计每天再省 ¥210');
  assert.deepEqual(waiting.notYet, { rows: [{ label: '本月节省', text: '措施实施满 24 小时后，第二天开始算' }], hint: '' });
  assert.deepEqual(waiting.alarms, { count: '2', waiting: '都有人确认了' });

  const unreadable = presentOverview({ savings: { status: 'error' }, recommendation: { status: 'error' }, energy: { status: 'error' }, alarms: { status: 'error' } });
  assert.deepEqual(unreadable.notYet, {
    rows: [{ label: '本月节省', text: '暂时读不到，下拉刷新再试' }, { label: '还能再省', text: '暂时读不到，下拉刷新再试' }],
    hint: '',
  });
  assert.deepEqual(unreadable.alarms, { count: '—', waiting: '' });
});

test('missing data says so: no tariff hides the cost, an unreadable section says it could not be read', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'ok', today: { electricityKWh: null, coolingKWh: 12, cop: null, costCNY: null }, change: { electricity: null, cooling: null, cop: null, cost: null } },
    alarms: { status: 'ok', active: 100, complete: false, waiting: 40 },
  });
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value]), [['空调用电', '—'], ['供冷量', '12.0'], ['综合能效', '—']]);
  // Only the first page of alarms was read.
  assert.deepEqual(view.alarms, { count: '100+', waiting: '至少 40 条没人确认' });
  assert.equal(presentOverview({ savings: { status: 'error' }, recommendation: { status: 'error' }, energy: { status: 'error' }, alarms: { status: 'error' } }).energy.state, 'error');
});

test('a Site with no electricity readings says so, instead of reporting a failure', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    recommendation: { status: 'ok', latest: null },
    energy: { status: 'empty' },
    alarms: { status: 'ok', active: 0, complete: true, waiting: 0 },
  });
  assert.deepEqual(view.energy, { state: 'empty', metrics: [], note: '这个站点今天和昨天都没有用电读数。' });
});
