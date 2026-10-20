import assert from 'node:assert/strict';
import test from 'node:test';
import { presentOverview } from '../miniprogram/lib/overview.ts';

const today = { electricityKWh: 1691, coolingKWh: 7657.3, cop: 4.53, costCNY: 1423.48, peakShare: 0.61 };
const change = { electricity: 0.156, cooling: 0.137, cop: -0.016, cost: 0.3 };

test('the overview leads with savings, then today against the same hours yesterday, then what needs handling', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: { cost: 8640, kWh: 7350, rate: 0.142 }, why: null, measures: [
      { id: 'm1', name: '冷冻水供水温度调高 1 °C', status: 'IMPLEMENTED', baseline: { trusted: true }, totals: { days: 20, avoidedKWh: 5020, avoidedCost: 5910 } },
    ] } },
    energy: { status: 'ok', today, change, yesterdayPeakShare: 0.39 },
    alarms: { status: 'ok', active: 5, complete: true },
  });
  assert.deepEqual(view.savings, {
    state: 'saved', amount: '¥8,640', detail: '少用电 7,350 kWh · 比不做措施少 14.2%',
    measures: [{ id: 'm1', name: '冷冻水供水温度调高 1 °C', amount: '¥5,910', detail: '本月统计 20 天 · 少用电 5,020 kWh' }],
  });
  assert.deepEqual(view.energy.metrics.map((each) => [each.label, each.value, each.unit, each.badge?.text, each.badge?.tone]), [
    ['空调用电', '1,691', 'kWh', '▲ 15.6%', 'bad'],
    ['电费（估算）', '¥1,423', '', '▲ 30.0%', 'bad'],
    ['供冷量', '7,657', 'kWh', '▲ 13.7%', 'neutral'],
    ['综合能效', '4.53', '', '▼ 1.6%', 'bad'],
  ]);
  assert.equal(view.energy.peak, '峰时段用电 今天 61% · 昨天 39%');
  assert.equal(view.alarms, '5');
});

test('nothing saved yet tells the operator what to do; a section that could not be read says so instead of showing zero', () => {
  const view = presentOverview({
    savings: { status: 'ok', card: { saved: null, why: 'NO_MEASURE', measures: [] } },
    energy: { status: 'error' },
    alarms: { status: 'ok', active: 100, complete: false },
  });
  assert.equal(view.savings.state, 'none');
  assert.equal(view.savings.amount, '—');
  assert.equal(view.savings.detail, '还没有在统计的节能措施。在网页「能源分析 → 节能项目」登记一项措施，实施满 24 小时后，第二天起这里显示省了多少钱、多少电。');
  assert.equal(view.energy.state, 'error');
  assert.equal(view.alarms, '100+');
});
