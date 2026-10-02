import {
  knownMetric,
  type OverviewDashboardData,
  type OverviewPeriod,
} from "./overview-types";
// Explicit design fixture; never a fallback for failed production requests.
const profile = { daily: 19826, rate: 12.7, comfort: 96.8, coverage: 98.5 };
const periods = {
  today: { count: 12, days: 1, dates: "2026.09.30 · 00:00–24:00" },
  week: { count: 7, days: 7, dates: "2026.09.24–09.30" },
  month: { count: 30, days: 30, dates: "2026.09.01–09.30" },
  year: { count: 9, days: 273, dates: "2026.01.01–09.30" },
} as const;
export function createOverviewExample(
  scopeName: string,
  period: OverviewPeriod,
): OverviewDashboardData {
  const config = periods[period];
  const raw = Array.from({ length: config.count }, (_, i) => ({
    weight:
      period === "today"
        ? [
            0.55, 0.5, 0.48, 0.68, 1.18, 1.35, 1.42, 1.48, 1.25, 1.14, 0.9,
            0.65,
          ][i]
        : 1 + Math.sin(i * 0.8) * 0.13 + Math.cos(i * 0.35) * 0.07,
    rate: profile.rate + Math.sin(i * 0.6) * 2.2,
  }));
  const weightSum = raw.reduce((sum, p) => sum + p.weight, 0);
  const trendSeries = raw.map((p, i) => {
    const actual = Math.round(
      (profile.daily * config.days * p.weight) / weightSum,
    );
    const time =
      period === "today"
        ? String(i * 2).padStart(2, "0") + ":00"
        : period === "week"
          ? "09/" + (i + 24)
          : period === "month"
            ? "09/" + String(i + 1).padStart(2, "0")
            : i + 1 + "月";
    return { time, actual, baseline: Math.round(actual / (1 - p.rate / 100)) };
  });
  const actualKWh = trendSeries.reduce((sum, p) => sum + p.actual, 0);
  const baselineKWh = trendSeries.reduce((sum, p) => sum + p.baseline, 0);
  const savingsKWh = baselineKWh - actualKWh;
  return {
    mode: "example",
    scopeName,
    period,
    dateRange: config.dates,
    asOf: "2026-09-30T16:00:00+08:00",
    actualKWh: knownMetric(actualKWh),
    baselineKWh,
    savingsKWh: knownMetric(savingsKWh),
    savingsRate: knownMetric((savingsKWh / baselineKWh) * 100),
    savingsCny: knownMetric(Math.round(savingsKWh * 0.843)),
    baselineMethod: "天气与运营时长调整",
    trendUnit: "kWh",
    trendSeries,
    comfort: {
      rate: profile.comfort,
      coverage: profile.coverage,
      exceptions: [
        {
          location: "西塔 12F · 开放办公区",
          detail: "温度高于舒适上限 0.8°C · 待复核",
        },
        { location: "裙楼 3F · 会议区", detail: "CO₂ 超出设定范围 · 待复核" },
      ],
    },
    strategies: [
      {
        title: "冷冻水供水温度重设",
        status: "运行中",
        description: "冷站 · 7.0°C → 7.8°C",
        savingKWh: Math.round(savingsKWh * 0.34),
      },
      {
        title: "空气系统运行排程优化",
        status: "运行中",
        description: "西塔 AHU · 非营业时段缩短 45 分钟",
        savingKWh: Math.round(savingsKWh * 0.23),
      },
      {
        title: "冷却塔风机协同寻优",
        status: "运行中",
        description: "冷却系统 · 按室外湿球温度调整",
        savingKWh: Math.round(savingsKWh * 0.17),
      },
    ],
    opportunities: [
      {
        title: "缩短非营业时段运行",
        object: "西塔 12F · AHU-04",
        reason: "每日多运行 2.4 小时 · 优先核对排程",
        potential: "¥ 20,960 / 年",
        action: "评估排程",
      },
      {
        title: "改善低温差运行",
        object: "冷冻水系统 · 二次泵组",
        reason: "输配能耗偏高 · 需排查旁通与阀位",
        potential: "¥ 38,800 / 年",
        action: "查看证据",
      },
      {
        title: "优化冷却塔风机频率",
        object: "冷却循环 · CT-02",
        reason: "存在协同运行空间 · 需确认传感器质量",
        potential: "¥ 12,320 / 年",
        action: "查看证据",
      },
    ],
    attention: [
      {
        title: "冷机出水温度偏离设定值",
        location: "B2 冷冻机房 · CH-01 · 持续 10 分钟",
        severity: "warning",
        action: "查看告警",
      },
      {
        title: "排程调整方案等待审批",
        location: "西塔 12F · 责任人 李工 · 今日 17:30 前",
        severity: "info",
        action: "查看任务",
      },
    ],
    systems: [
      {
        name: "冷站",
        metric: "综合 COP",
        value: knownMetric(5.42),
        unit: "",
        reference: "设计基准 5.0",
      },
      {
        name: "输配",
        metric: "输送能效比",
        value: knownMetric(32.4),
        unit: "",
        reference: "设计基准 30.0",
      },
      {
        name: "空气系统",
        metric: "单位风量功率",
        value: knownMetric(0.42),
        unit: "W/(m³/h)",
        reference: "设计基准 0.50",
      },
    ],
  };
}
