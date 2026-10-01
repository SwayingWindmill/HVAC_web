import type {
  ConsumptionAnalyticsData,
  DailyConsumptionRecord,
  ConsumptionFilterParams,
} from "./consumption-types";
export const CONSUMPTION_EXAMPLE_TARIFFS = [
  {
    period: "尖峰",
    periodType: "SHARP",
    rate: 1.485,
    key: "sharpKWh",
    hoursDesc: "11:00–13:00 / 18:00–20:00",
    color: "#2563eb",
  },
  {
    period: "高峰",
    periodType: "PEAK",
    rate: 1.12,
    key: "peakKWh",
    hoursDesc: "08:00–11:00 / 13:00–18:00",
    color: "#60a5fa",
  },
  {
    period: "平段",
    periodType: "FLAT",
    rate: 0.76,
    key: "flatKWh",
    hoursDesc: "07:00–08:00 / 20:00–23:00",
    color: "#10b981",
  },
  {
    period: "低谷",
    periodType: "VALLEY",
    rate: 0.38,
    key: "valleyKWh",
    hoursDesc: "23:00–07:00",
    color: "#a7f3d0",
  },
] as const;
const periods = {
  "current-month": ["2026-09-01", 30, "2026年9月"],
  "last-month": ["2026-08-01", 31, "2026年8月"],
  quarter: ["2026-07-01", 92, "2026年第三季度"],
  year: ["2026-01-01", 273, "2026年迄今"],
} as const;
export function createConsumptionExample(
  period: NonNullable<ConsumptionFilterParams["period"]>,
  scopeId: string,
): ConsumptionAnalyticsData {
  const [start, count, label] = periods[period];
  const scale =
    scopeId === "portfolio"
      ? 6.4
      : scopeId === "group:east"
        ? 3.2
        : scopeId === "group:south"
          ? 2.5
          : scopeId === "site:site-02"
            ? 0.68
            : scopeId === "site:site-03"
              ? 0.51
              : 1;
  const dailyRecords: DailyConsumptionRecord[] = Array.from(
    { length: count },
    (_, index) => {
      const date = new Date(start + "T00:00:00Z");
      date.setUTCDate(date.getUTCDate() + index);
      const temp = 28 + Math.sin(index / 4) * 3;
      const totalKWh = Math.round(
        (date.getUTCDay() === 0 || date.getUTCDay() === 6 ? 5200 : 7200) *
          scale *
          (1 + Math.sin(index / 5) * 0.12),
      );
      const sharpKWh = Math.round(totalKWh * 0.16),
        peakKWh = Math.round(totalKWh * 0.32),
        flatKWh = Math.round(totalKWh * 0.21),
        valleyKWh = totalKWh - sharpKWh - peakKWh - flatKWh;
      const buckets = { sharpKWh, peakKWh, flatKWh, valleyKWh };
      const totalCostCNY =
        CONSUMPTION_EXAMPLE_TARIFFS.reduce(
          (total, item) =>
            total + Math.round(buckets[item.key] * item.rate * 100),
          0,
        ) / 100;
      const baselineKWh = Math.round(totalKWh * 1.08);
      return {
        id: date.toISOString().slice(0, 10),
        date: date.toISOString().slice(0, 10),
        dayOfWeek: ["周日", "周一", "周二", "周三", "周四", "周五", "周六"][
          date.getUTCDay()
        ],
        totalKWh,
        sharpKWh,
        peakKWh,
        flatKWh,
        valleyKWh,
        totalCostCNY,
        averageRate: totalCostCNY / totalKWh,
        baselineKWh,
        varianceRate: ((totalKWh - baselineKWh) / baselineKWh) * 100,
        outdoorTempAvg: Number(temp.toFixed(1)),
        coolingDegreeDays: temp - 18,
        peakPowerKW: Math.round(totalKWh / 12),
        peakTime: "14:30",
      };
    },
  );
  const sum = (
    key:
      | "totalKWh"
      | "totalCostCNY"
      | "baselineKWh"
      | "sharpKWh"
      | "peakKWh"
      | "flatKWh"
      | "valleyKWh",
  ) => dailyRecords.reduce((total, record) => total + record[key], 0);
  const energy = sum("totalKWh"),
    cost = sum("totalCostCNY");
  const touBreakdown = CONSUMPTION_EXAMPLE_TARIFFS.map((item) => {
    const energyKWh = sum(item.key);
    const costCNY =
      dailyRecords.reduce(
        (total, record) =>
          total + Math.round(record[item.key] * item.rate * 100),
        0,
      ) / 100;
    return {
      ...item,
      energyKWh,
      costCNY,
      energyShare: (energyKWh / energy) * 100,
      costShare: (costCNY / cost) * 100,
    };
  });
  const subsystemCosts = [
    { name: "冷水机组", share: 0.57 },
    { name: "输配水泵", share: 0.217 },
    { name: "空调末端", share: 0.144 },
    { name: "冷却塔", share: 0.069 },
  ].map((item, index) => ({
    id: String(index),
    name: item.name,
    category: item.name,
    costCNY: cost * item.share,
    energyKWh: energy * item.share,
    sharePercent: item.share * 100,
    momPercent: 0,
    color: "#2563eb",
  }));
  return {
    summary: {
      totalEnergyKWh: energy,
      totalCostCNY: cost,
      averageUnitCost: cost / energy,
      peakTariffShare: touBreakdown[0].costShare + touBreakdown[1].costShare,
      baselineVariancePercent:
        ((energy - sum("baselineKWh")) / sum("baselineKWh")) * 100,
      energyMomPercent: 0,
      costMomPercent: 0,
      energySavedKWh: sum("baselineKWh") - energy,
      costSavedCNY: 0,
      carbonEmissionsTons: energy * 0.00057,
      carbonSavedTons: 0,
    },
    dailyRecords,
    touBreakdown,
    subsystemCosts,
    periodRange: {
      start,
      end: dailyRecords[dailyRecords.length - 1].date,
      label,
    },
  };
}
