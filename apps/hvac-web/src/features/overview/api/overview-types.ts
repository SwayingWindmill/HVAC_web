export const OVERVIEW_PERIODS = {
  today: "今日",
  week: "近7天",
  month: "本月",
  year: "本年",
} as const;
export type OverviewPeriod = keyof typeof OVERVIEW_PERIODS;
/** A figure the owner either provides or says why it cannot. */
export interface OverviewMetric {
  readonly value: number | null;
  /** Shown in place of the number when there is none, e.g. 未接入. */
  readonly absence: string | null;
}
export const knownMetric = (value: number): OverviewMetric => ({
  value,
  absence: null,
});
export interface EnergyTrendPoint {
  readonly time: string;
  readonly actual: number | null;
  readonly baseline: number | null;
}
export interface OverviewStrategy {
  readonly title: string;
  readonly status: "运行中" | "已暂停" | "待审批";
  readonly description: string;
  readonly savingKWh: number | null;
}
export interface OverviewOpportunity {
  readonly title: string;
  readonly object: string;
  readonly reason: string;
  readonly potential: string;
  readonly action: string;
}
export interface OverviewAttention {
  readonly title: string;
  readonly location: string;
  readonly severity: "risk" | "warning" | "info";
  readonly action: string;
}
export interface OverviewDashboardData {
  readonly mode: "example" | "live";
  readonly scopeName: string;
  readonly period: OverviewPeriod;
  readonly dateRange: string;
  readonly asOf: string;
  readonly actualKWh: OverviewMetric;
  readonly baselineKWh: number | null;
  readonly savingsKWh: OverviewMetric;
  readonly savingsRate: OverviewMetric;
  readonly savingsCny: OverviewMetric;
  readonly baselineMethod: string | null;
  readonly trendUnit: "kW" | "kWh";
  readonly trendSeries: readonly EnergyTrendPoint[];
  readonly comfort: {
    readonly rate: number | null;
    readonly coverage: number | null;
    readonly exceptions: readonly {
      readonly location: string;
      readonly detail: string;
    }[];
  };
  readonly strategies: readonly OverviewStrategy[];
  readonly opportunities: readonly OverviewOpportunity[];
  readonly attention: readonly OverviewAttention[];
  readonly systems: readonly {
    readonly name: string;
    readonly metric: string;
    readonly value: OverviewMetric;
    readonly unit: string;
    readonly reference: string;
  }[];
}
export function formatOverviewNumber(value: number | null, digits = 1) {
  return value === null
    ? "—"
    : value.toLocaleString("zh-CN", {
        maximumFractionDigits: digits,
        minimumFractionDigits: digits,
      });
}
