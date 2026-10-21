// What the 总览 page shows, from what its owners returned: savings and what more could be saved
// first (what customers care about most), then today against the same hours yesterday as the web
// overview shows it, then what needs handling (#664). Savings or a recommendation not there yet
// take one line each in a small card below the figures instead of a large card on top (#677).

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
  /** `waiting`: the alarms happening that no one has acknowledged, the red dot on 通知. */
  readonly alarms: Loaded<{ readonly active: number; readonly complete: boolean; readonly waiting: number }>;
}

export interface Metric {
  readonly label: string;
  readonly value: string;
  readonly unit: string;
  readonly badge: ChangeBadge | null;
}

const NO_SAVINGS: Readonly<Record<NoSavingsReason, string>> = {
  NO_MEASURE: '还没有在统计的节能措施',
  BASELINE_UNTRUSTED: '对比基线不可靠，暂时不统计',
  FIRST_DAY_PENDING: '措施实施满 24 小时后，第二天开始算',
};
const UNREADABLE = '暂时读不到，下拉刷新再试';
const SET_UP_ON_THE_WEB = '在电脑网页「能源分析」里登记节能措施、生成优化建议后，这里显示能省多少。';

/** A line in the small card for what is not there yet; `onTheWeb` when the web is where to set it up. */
interface NotYetRow {
  readonly label: string;
  readonly text: string;
  readonly onTheWeb: boolean;
}

const RISK: Readonly<Record<Recommendation['risk']['level'], ChangeBadge>> = {
  LOW: { text: '低风险', tone: 'good' },
  MEDIUM: { text: '中风险', tone: 'neutral' },
  HIGH: { text: '高风险', tone: 'bad' },
};

const money = (value: number) => `¥${grouped(value)}`;

function presentSavings(input: OverviewInputs['savings']) {
  if (input.status === 'error') return { notYet: { label: '本月节省', text: UNREADABLE, onTheWeb: false } };
  const { saved, why, measures } = input.card;
  if (saved === null) return { notYet: { label: '本月节省', text: NO_SAVINGS[why ?? 'NO_MEASURE'], onTheWeb: why !== 'FIRST_DAY_PENDING' } };
  const rows = measures.filter((measure) => measure.totals.days > 0).map((measure) => ({
    id: measure.id,
    name: measure.name,
    amount: money(measure.totals.avoidedCost),
    detail: `本月统计 ${measure.totals.days} 天 · 少用电 ${grouped(measure.totals.avoidedKWh)} kWh`,
  }));
  const rate = saved.rate === null ? '' : ` · 比不做措施少 ${grouped(saved.rate * 100, 1)}%`;
  return { card: { amount: money(saved.cost), detail: `少用电 ${grouped(saved.kWh)} kWh${rate}`, measures: rows } };
}

function presentRecommendation(input: OverviewInputs['recommendation']) {
  if (input.status === 'error') return { notYet: { label: '还能再省', text: UNREADABLE, onTheWeb: false } };
  const latest = input.latest;
  if (latest === null) return { notYet: { label: '还能再省', text: '还没有优化建议', onTheWeb: true } };
  const from = grouped(latest.baseline.supplyTempC, 1);
  // The same threshold as the web: a change under 0.05 °C keeps the supply temperature.
  const action = Math.abs(latest.candidate.supplyTempC - latest.baseline.supplyTempC) >= 0.05
    ? `冷冻水供水 ${from} → ${grouped(latest.candidate.supplyTempC, 1)} °C`
    : `保持供水 ${from} °C`;
  return {
    card: {
      title: `${action}，预计每天再省 ${money(latest.expectedImpact.costSavingPerDay)}`,
      detail: `少用电 ${grouped(latest.expectedImpact.energySavingKWhPerDay)} kWh/天`,
      risk: RISK[latest.risk.level],
    },
  };
}

function presentAlarms(input: OverviewInputs['alarms']) {
  if (input.status === 'error') return { count: '—', waiting: '' };
  const count = `${input.active}${input.complete ? '' : '+'}`;
  if (input.active === 0) return { count, waiting: '' };
  if (input.waiting === 0) return { count, waiting: '都有人确认了' };
  return { count, waiting: `${input.complete ? '' : '至少 '}${input.waiting} 条没人确认` };
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
  const savings = presentSavings(inputs.savings);
  const recommendation = presentRecommendation(inputs.recommendation);
  const notYet = [savings.notYet, recommendation.notYet].filter((row): row is NotYetRow => row !== undefined);
  return {
    savings: savings.card ?? null,
    recommendation: recommendation.card ?? null,
    notYet: notYet.length === 0 ? null : {
      rows: notYet.map(({ label, text }) => ({ label, text })),
      hint: notYet.some((row) => row.onTheWeb) ? SET_UP_ON_THE_WEB : '',
    },
    energy: presentEnergy(inputs.energy),
    alarms: presentAlarms(inputs.alarms),
  };
}
