// What the 总览 page shows, from what its owners returned: savings first (what customers care about
// most), then today against the same hours yesterday, then what needs handling (#664).

import { changeBadge, grouped, type ChangeBadge } from './format';
import type { NoSavingsReason, SavingsCard } from './savings';
import type { SoFar } from './today';

type Loaded<T> = ({ readonly status: 'ok' } & T) | { readonly status: 'error' };

export interface OverviewInputs {
  readonly savings: Loaded<{ readonly card: SavingsCard }>;
  readonly energy: Loaded<{
    readonly today: SoFar;
    readonly change: { readonly electricity: number | null; readonly cooling: number | null; readonly cop: number | null; readonly cost: number | null };
    readonly yesterdayPeakShare: number | null;
  }>;
  readonly alarms: Loaded<{ readonly active: number; readonly complete: boolean }>;
}

export interface Metric {
  readonly label: string;
  readonly value: string;
  readonly unit: string;
  readonly badge: ChangeBadge | null;
}

const NO_SAVINGS: Readonly<Record<NoSavingsReason, string>> = {
  NO_MEASURE: '还没有在统计的节能措施。在网页「能源分析 → 节能项目」登记一项措施，实施满 24 小时后，第二天起这里显示省了多少钱、多少电。',
  BASELINE_UNTRUSTED: '节能措施的对比基线不可靠，暂时不统计。请在网页「节能项目」里查看原因。',
  FIRST_DAY_PENDING: '节能措施实施满 24 小时后，第二天起这里显示省了多少钱、多少电。',
};

const money = (value: number) => `¥${grouped(value)}`;
const percent = (share: number | null) => (share === null ? '—' : `${Math.round(share * 100)}%`);

function presentSavings(input: OverviewInputs['savings']) {
  if (input.status === 'error') return { state: 'error' as const, amount: '—', detail: '节能统计暂时读不到，下拉刷新再试。', measures: [] };
  const { saved, why, measures } = input.card;
  const rows = measures.filter((measure) => measure.totals.days > 0).map((measure) => ({
    id: measure.id,
    name: measure.name,
    amount: money(measure.totals.avoidedCost),
    detail: `本月统计 ${measure.totals.days} 天 · 少用电 ${grouped(measure.totals.avoidedKWh)} kWh`,
  }));
  if (saved === null) return { state: 'none' as const, amount: '—', detail: NO_SAVINGS[why ?? 'NO_MEASURE'], measures: rows };
  const rate = saved.rate === null ? '' : ` · 比不做措施少 ${grouped(saved.rate * 100, 1)}%`;
  return { state: 'saved' as const, amount: money(saved.cost), detail: `少用电 ${grouped(saved.kWh)} kWh${rate}`, measures: rows };
}

function presentEnergy(input: OverviewInputs['energy']) {
  if (input.status === 'error') return { state: 'error' as const, metrics: [] as Metric[], peak: '' };
  const { today, change, yesterdayPeakShare } = input;
  const metrics: Metric[] = [
    { label: '空调用电', value: grouped(today.electricityKWh), unit: 'kWh', badge: changeBadge(change.electricity, 'less-is-better') },
    { label: '电费（估算）', value: money(today.costCNY), unit: '', badge: changeBadge(change.cost, 'less-is-better') },
    { label: '供冷量', value: grouped(today.coolingKWh), unit: 'kWh', badge: changeBadge(change.cooling, 'neutral') },
    { label: '综合能效', value: grouped(today.cop, 2), unit: '', badge: changeBadge(change.cop, 'more-is-better') },
  ];
  return { state: 'ok' as const, metrics, peak: `峰时段用电 今天 ${percent(today.peakShare)} · 昨天 ${percent(yesterdayPeakShare)}` };
}

export function presentOverview(inputs: OverviewInputs) {
  return {
    savings: presentSavings(inputs.savings),
    energy: presentEnergy(inputs.energy),
    alarms: inputs.alarms.status === 'error' ? '—' : `${inputs.alarms.active}${inputs.alarms.complete ? '' : '+'}`,
  };
}
