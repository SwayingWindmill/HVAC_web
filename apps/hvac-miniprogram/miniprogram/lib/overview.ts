// What the 总览 page shows, from what its owners returned: savings and what more could be saved
// first (what customers care about most), then today against the same hours yesterday as the web
// overview shows it, then what needs handling (#664). Savings stay on top even before there are
// any: the card says why there are none yet, and the recommendation below it the same.

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
  /** Its lucide icon in assets/icon. */
  readonly icon: string;
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

const RISK: Readonly<Record<Recommendation['risk']['level'], ChangeBadge>> = {
  LOW: { text: '低风险', tone: 'good' },
  MEDIUM: { text: '中风险', tone: 'neutral' },
  HIGH: { text: '高风险', tone: 'bad' },
};

const money = (value: number) => `¥${grouped(value)}`;

const SET_UP_SAVINGS = '在电脑网页「能源分析 → 节能项目」登记措施后，这里显示省了多少。';
const SET_UP_RECOMMENDATION = '在电脑网页「能源分析 → 优化建议」生成后，这里显示还能省多少。';

/** No amount yet: a dash, why, and where on the web to set it up when that is what is missing. */
const noSavings = (detail: string, hint: string) => ({ amount: '—', detail, measures: [], hint });

function presentSavings(input: OverviewInputs['savings']) {
  if (input.status === 'error') return noSavings(UNREADABLE, '');
  const { saved, why, measures } = input.card;
  if (saved === null) return noSavings(NO_SAVINGS[why ?? 'NO_MEASURE'], why === 'FIRST_DAY_PENDING' ? '' : SET_UP_SAVINGS);
  const rows = measures.filter((measure) => measure.totals.days > 0).map((measure) => ({
    id: measure.id,
    name: measure.name,
    amount: money(measure.totals.avoidedCost),
    detail: `本月统计 ${measure.totals.days} 天 · 少用电 ${grouped(measure.totals.avoidedKWh)} kWh`,
  }));
  const rate = saved.rate === null ? '' : ` · 比不做措施少 ${grouped(saved.rate * 100, 1)}%`;
  return { amount: money(saved.cost), detail: `少用电 ${grouped(saved.kWh)} kWh${rate}`, measures: rows, hint: '' };
}

function presentRecommendation(input: OverviewInputs['recommendation']) {
  if (input.status === 'error') return { title: UNREADABLE, detail: '', risk: null };
  const latest = input.latest;
  if (latest === null) return { title: '还没有优化建议', detail: SET_UP_RECOMMENDATION, risk: null };
  const from = grouped(latest.baseline.supplyTempC, 1);
  // The same threshold as the web: a change under 0.05 °C keeps the supply temperature.
  const action = Math.abs(latest.candidate.supplyTempC - latest.baseline.supplyTempC) >= 0.05
    ? `冷冻水供水 ${from} → ${grouped(latest.candidate.supplyTempC, 1)} °C`
    : `保持供水 ${from} °C`;
  return {
    title: `${action}，预计每天再省 ${money(latest.expectedImpact.costSavingPerDay)}`,
    detail: `少用电 ${grouped(latest.expectedImpact.energySavingKWhPerDay)} kWh/天`,
    risk: RISK[latest.risk.level] as ChangeBadge | null,
  };
}

/** `happening` shows the red alarm icon; unreadable alarms do not. */
function presentAlarms(input: OverviewInputs['alarms']) {
  if (input.status === 'error') return { count: '—', waiting: '', happening: false };
  const count = `${input.active}${input.complete ? '' : '+'}`;
  const happening = input.active > 0;
  if (!happening) return { count, waiting: '', happening };
  // With only the first page read, the rest may be waiting: say only what is known.
  if (input.waiting === 0) return { count, waiting: input.complete ? '都有人确认了' : '', happening };
  return { count, waiting: `${input.complete ? '' : '至少 '}${input.waiting} 条没人确认`, happening };
}

function presentEnergy(input: OverviewInputs['energy']) {
  if (input.status === 'error') return { state: 'error' as const, metrics: [] as Metric[], note: '今天的用能暂时读不到，下拉刷新再试。' };
  if (input.status === 'empty') return { state: 'empty' as const, metrics: [] as Metric[], note: '这个站点今天和昨天都没有用电读数。' };
  const { today, change } = input;
  const metrics: Metric[] = [
    { label: '空调用电', icon: 'zap', value: grouped(today.electricityKWh, 1), unit: today.electricityKWh === null ? '' : 'kWh', badge: changeBadge(change.electricity, 'less-is-better') },
    // A Site without a tariff has no cost to show, as on the web.
    ...(today.costCNY === null ? [] : [{ label: '电费（估算）', icon: 'receipt', value: money(today.costCNY), unit: '', badge: changeBadge(change.cost, 'less-is-better') }]),
    { label: '供冷量', icon: 'snowflake', value: grouped(today.coolingKWh, 1), unit: today.coolingKWh === null ? '' : 'kWh', badge: changeBadge(change.cooling, 'neutral') },
    { label: '综合能效', icon: 'gauge', value: grouped(today.cop, 2), unit: '', badge: changeBadge(change.cop, 'more-is-better') },
  ];
  return { state: 'ok' as const, metrics, note: '' };
}

export function presentOverview(inputs: OverviewInputs) {
  return {
    savings: presentSavings(inputs.savings),
    recommendation: presentRecommendation(inputs.recommendation),
    energy: presentEnergy(inputs.energy),
    alarms: presentAlarms(inputs.alarms),
  };
}
