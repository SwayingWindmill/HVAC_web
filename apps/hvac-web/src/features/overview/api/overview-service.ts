import {
  createPlatformGatewayClient,
  type DashboardMetric,
  type PresentationState,
  type SiteDashboardSummary,
} from "@/api/generated/platformGateway.gen";
import { createOverviewExample } from "./overview-example";
import {
  knownMetric,
  OVERVIEW_PERIODS,
  type OverviewAttention,
  type OverviewDashboardData,
  type OverviewMetric,
  type OverviewPeriod,
} from "./overview-types";
export const isOverviewExample =
  __HVAC_WEB_FRONTEND_REVIEW__;
export class OverviewUnsupportedError extends Error {}
const client = createPlatformGatewayClient();
const ABSENCE: Record<PresentationState, string> = {
  READY: "暂无数据",
  ATTENTION: "暂无数据",
  PARTIAL: "暂无数据",
  STALE: "暂无数据",
  SUSPECT: "暂无数据",
  NO_DATA: "暂无数据",
  UNAVAILABLE: "暂不可用",
  NOT_AUTHORIZED: "无权查看",
  NOT_INTEGRATED: "未接入",
};
const NOT_INTEGRATED: OverviewMetric = { value: null, absence: "未接入" };
function figure(value: number | null, state: PresentationState): OverviewMetric {
  return value === null ? { value: null, absence: ABSENCE[state] } : knownMetric(value);
}
const metric = (source: DashboardMetric) => figure(source.value, source.state);
const SEVERITY_LABEL = {
  CRITICAL: "紧急",
  MAJOR: "重要",
  MINOR: "次要",
  WARNING: "警告",
  INFO: "提示",
} as const;
function openAlarmAttention(summary: SiteDashboardSummary): OverviewAttention[] {
  const alarms = summary.fastMetrics.openAlarms;
  if (!alarms.activeCount || !alarms.highestSeverity) return [];
  const severity = alarms.highestSeverity;
  return [
    {
      title: alarms.activeCount + " 条未结告警",
      location: "最高级别 · " + SEVERITY_LABEL[severity],
      severity:
        severity === "CRITICAL" || severity === "MAJOR"
          ? "risk"
          : severity === "INFO"
            ? "info"
            : "warning",
      action: "查看告警",
    },
  ];
}
export async function readEnergyOverview(
  scopeId: string,
  scopeName: string,
  period: OverviewPeriod,
  signal?: AbortSignal,
): Promise<OverviewDashboardData> {
  if (isOverviewExample)
    return createOverviewExample(scopeName, period);
  // The Site summary covers the Site-local day only.
  if (!scopeId.startsWith("site:"))
    throw new OverviewUnsupportedError(
      "集团与区域节能汇总尚未接入，请选择单个站点。",
    );
  if (period !== "today")
    throw new OverviewUnsupportedError(
      OVERVIEW_PERIODS[period] + "节能汇总尚未接入，请查看今日概况。",
    );
  const { data: summary } = await client.getSiteDashboardSummary(
    scopeId.slice(5),
    { signal },
  );
  const { slowMetrics, devicePopulation } = summary;
  return {
    mode: "live",
    scopeName,
    period,
    dateRange: "今日 · 站点当地时间",
    asOf: summary.asOf,
    actualKWh: metric(slowMetrics.siteLocalDayEnergy),
    baselineKWh: null,
    savingsKWh: metric(slowMetrics.baselineSavings),
    savingsRate: NOT_INTEGRATED,
    savingsCny: NOT_INTEGRATED,
    baselineMethod: null,
    trendUnit: "kW",
    trendSeries: [],
    comfort: { rate: null, coverage: null, exceptions: [] },
    strategies: [],
    opportunities: [],
    attention: openAlarmAttention(summary),
    systems: [
      {
        name: "冷站",
        metric: "综合 COP",
        value: metric(slowMetrics.cop),
        unit: "",
        reference: "基准未提供",
      },
      {
        name: "设备",
        metric: "在线率",
        value: figure(devicePopulation.availabilityPercent, devicePopulation.state),
        unit: "%",
        reference:
          devicePopulation.online + " / " + devicePopulation.registered + " 台在线",
      },
    ],
  };
}
