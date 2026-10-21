// What the 总览 page shows, from what its owners returned: savings and what more could be saved
// first (what customers care about most), then today against the same hours yesterday as the web
// overview shows it, then what needs handling (#664).

import { changeBadge, grouped, type ChangeBadge } from './format';
import type { NoSavingsReason, SavingsCard } from './savings';
import type { EnergySummary } from './today';

type Loaded<T> = ({ readonly status: 'ok' } & T) | { readonly status: 'error' };

/** The optimization owner's latest recommendation, as published. */
export interface Recommendation {
  readonly createdAt: string;
  readonly baseline: { readonly supplyTempC: number };
  readonly candidate: { readonly supplyTempC: number };
  readonly expectedImpact: { readonly energySavingKWhPerDay: number; readonly costSavingPerDay: number };
  readonly risk: { readonly level: 'LOW' | 'MEDIUM' | 'HIGH'; readonly comfortMarginC: number };
}

export interface OverviewInputs {
  readonly savings: Loaded<{ readonly card: SavingsCard }>;
  readonly recommendation: Loaded<{ readonly latest: Recommendation | null }>;
  /** `empty`: the Site has no electricity reading today or yesterday, so there is no day to compare. */
  readonly energy: Loaded<{
    readonly today: EnergySummary;
    readonly change: { readonly electricity: number | null; readonly cooling: number | null; readonly cop: number | null; readonly cost: number | null };
  }> | { readonly status: 'empty' };
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

const RISK: Readonly<Record<Recommendation['risk']['level'], ChangeBadge>> = {
  LOW: { text: '低风险', tone: 'good' },
  MEDIUM: { text: '中风险', tone: 'neutral' },
  HIGH: { text: '高风险', tone: 'bad' },
};

const money = (value: number) => `¥${grouped(value)}`;

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

function presentRecommendation(input: OverviewInputs['recommendation']) {
  if (input.status === 'error') return { state: 'error' as const, title: '优化建议暂时读不到。', detail: '', risk: null };
  const latest = input.latest;
  if (latest === null) return { state: 'none' as const, title: '还没有优化建议。', detail: '在网页「能源分析 → 优化建议」生成后，这里显示建议怎么调、预计每天能省多少。', risk: null };
  const from = grouped(latest.baseline.supplyTempC, 1);
  // The same threshold as the web: a change under 0.05 °C keeps the supply temperature.
  const action = Math.abs(latest.candidate.supplyTempC - latest.baseline.supplyTempC) >= 0.05
    ? `冷冻水供水 ${from} → ${grouped(latest.candidate.supplyTempC, 1)} °C`
    : `保持供水 ${from} °C`;
  return {
    state: 'ready' as const,
    title: `${action}，预计每天再省 ${money(latest.expectedImpact.costSavingPerDay)}`,
    detail: `少用电 ${grouped(latest.expectedImpact.energySavingKWhPerDay)} kWh/天`,
    risk: RISK[latest.risk.level],
  };
}

function presentEnergy(input: OverviewInputs['energy']) {
  if (input.status === 'error') return { state: 'error' as const, metrics: [] as Metric[], note: '今天的用能暂时读不到，下拉刷新再试。' };
  if (input.status === 'empty') return { state: 'empty' as const, metrics: [] as Metric[], note: '这个站点今天和昨天都没有用电读数。' };
  const { today, change } = input;
  const metrics: Metric[] = [
    { label: '空调用电', value: grouped(today.electricityKWh, 1), unit: today.electricityKWh === null ? '' : 'kWh', badge: changeBadge(change.electricity, 'less-is-better') },
    // A Site without a tariff has no cost to show, as on the web.
    ...(today.costCNY === null ? [] : [{ label: '电费（估算）', value: money(today.costCNY), unit: '', badge: changeBadge(change.cost, 'less-is-better') }]),
    { label: '供冷量', value: grouped(today.coolingKWh, 1), unit: today.coolingKWh === null ? '' : 'kWh', badge: changeBadge(change.cooling, 'neutral') },
    { label: '综合能效', value: grouped(today.cop, 2), unit: '', badge: changeBadge(change.cop, 'more-is-better') },
  ];
  return { state: 'ok' as const, metrics, note: '' };
}

export function presentOverview(inputs: OverviewInputs) {
  return {
    savings: presentSavings(inputs.savings),
    recommendation: presentRecommendation(inputs.recommendation),
    energy: presentEnergy(inputs.energy),
    alarms: inputs.alarms.status === 'error' ? '—' : `${inputs.alarms.active}${inputs.alarms.complete ? '' : '+'}`,
  };
}
