import { readDashboardOverview } from "@/api/dashboard-overview";
import { createOverviewExample } from "./overview-example";
import {
  OVERVIEW_PERIODS,
  type OverviewDashboardData,
  type OverviewPeriod,
} from "./overview-types";
export const isOverviewExample =
  __HVAC_WEB_FRONTEND_REVIEW__ || import.meta.env.DEV;
export class OverviewUnsupportedError extends Error {}
export async function readEnergyOverview(
  scopeId: string,
  scopeName: string,
  period: OverviewPeriod,
  signal?: AbortSignal,
): Promise<OverviewDashboardData> {
  if (isOverviewExample)
    return createOverviewExample(scopeId, scopeName, period);
  // Current owner contract has no historical aggregation parameter.
  if (!scopeId.startsWith("site:"))
    throw new OverviewUnsupportedError(
      "集团与区域节能汇总尚未接入，请选择单个站点。",
    );
  if (period !== "today")
    throw new OverviewUnsupportedError(
      OVERVIEW_PERIODS[period] + "节能汇总尚未接入，请查看今日概况。",
    );
  const facts = await readDashboardOverview(scopeId.slice(5), signal);
  const energy = facts.savingsPerformance;
  return {
    mode: "live",
    scopeName,
    period,
    dateRange: "今日 · 平台当前概况",
    asOf: facts.asOf,
    actualKWh: energy.actualEnergyKWh,
    baselineKWh: energy.baselineEnergyKWh,
    savingsKWh: energy.savingEnergyKWh,
    savingsRate: energy.savingRatePercent,
    savingsCny: energy.savingCostCny,
    baselineMethod: null,
    trendUnit: "kW",
    trendSeries: facts.loadTrend.map((point) => ({
      time: new Date(point.at).toLocaleTimeString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour: "2-digit",
        minute: "2-digit",
      }),
      actual: point.actual,
      baseline: point.baseline ?? null,
    })),
    comfort: {
      rate: facts.kpis.comfortRatePercent,
      coverage: null,
      exceptions: [],
    },
    strategies: facts.strategies.map((strategy) => ({
      title: strategy.title,
      status:
        strategy.status === "RUNNING"
          ? "运行中"
          : strategy.status === "STOPPED"
            ? "已暂停"
            : "待审批",
      description: "平台策略概况 · 收益待验证",
      savingKWh: strategy.savingKWh,
    })),
    opportunities: facts.opportunities.map((opportunity) => ({
      title: opportunity.title,
      object: "站点节能机会",
      reason: "查看专业工作区中的分析证据",
      potential:
        opportunity.savingKWhPerDay === null
          ? "潜力待评估"
          : opportunity.savingKWhPerDay.toLocaleString("zh-CN") + " kWh/日",
      action: "查看机会",
    })),
    attention: facts.priorityAlarms.map((alarm) => ({
      title: alarm.title,
      location: alarm.locationLabel + " · " + alarm.deviceLabel,
      severity:
        alarm.severity === "CRITICAL" || alarm.severity === "MAJOR"
          ? "risk"
          : "warning",
      action: "查看告警",
    })),
    systems: [
      {
        name: "冷站",
        metric: "综合 COP",
        value: facts.kpis.averageCop,
        unit: "",
        reference: "基准未提供",
      },
    ],
  };
}
