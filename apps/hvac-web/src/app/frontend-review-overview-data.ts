import type { SiteDashboardSummary } from '@/api/generated/platformGateway.gen';
import type { DashboardOverview } from '@/api/dashboard-overview';

const tenantId = '01940000-0000-7000-8000-000000000001';
const generatedAt = '2026-09-21T06:30:00.000Z';

const metric = (value: number, unit: string, source: string) => ({
  state: 'READY' as const,
  value,
  unit,
  source,
  dataWatermark: generatedAt,
  aggregateWatermark: generatedAt,
  reason: null,
});

export function createFrontendReviewDashboardSummary(siteId: string): SiteDashboardSummary {
  return {
    schemaVersion: 1,
    tenantId,
    siteId,
    siteTimezone: 'Asia/Shanghai',
    asOf: generatedAt,
    generatedAt,
    dataWatermark: generatedAt,
    aggregateWatermark: generatedAt,
    completeness: 'READY',
    quality: 'READY',
    reasons: [],
    devicePopulation: {
      state: 'READY',
      registered: 200,
      applicable: 200,
      observable: 195,
      online: 185,
      offline: 5,
      stale: 5,
      unknown: 5,
      unavailable: 0,
      denominatorPolicy: 'APPLICABLE_WITH_KNOWN_PRESENCE',
      denominator: 200,
      availabilityPercent: 92.5,
      evaluatedAt: generatedAt,
    },
    slowMetrics: {
      siteLocalDayEnergy: metric(18420, 'kWh', 'frontend-review'),
      cost: metric(14260, 'CNY', 'frontend-review'),
      baselineSavings: metric(13.4, '%', 'frontend-review'),
      cop: metric(5.72, 'COP', 'frontend-review'),
    },
    fastMetrics: {
      currentPower: metric(486, 'kW', 'frontend-review'),
      openAlarms: {
        state: 'READY',
        activeCount: 3,
        highestSeverity: 'MAJOR',
        watermark: generatedAt,
        reason: null,
      },
    },
  };
}

const trend = [
  ['2026-09-20T23:00:00.000Z', 395, 445],
  ['2026-09-21T00:00:00.000Z', 412, 452],
  ['2026-09-21T01:00:00.000Z', 438, 466],
  ['2026-09-21T02:00:00.000Z', 465, 492],
  ['2026-09-21T03:00:00.000Z', 488, 515],
  ['2026-09-21T04:00:00.000Z', 472, 506],
  ['2026-09-21T05:00:00.000Z', 451, 493],
  ['2026-09-21T06:00:00.000Z', 428, 480],
] as const;

export function createFrontendReviewDashboardOverview(siteId: string): DashboardOverview {
  return {
    schemaVersion: 1,
    siteId,
    asOf: generatedAt,
    weather: { temperatureC: 30.2, condition: '多云' },
    kpis: {
      coolingTodayRT: 3260,
      coolingComparePercent: -4.8,
      totalLoadKW: 486,
      loadComparePercent: -7.4,
      averageCop: 5.72,
      copComparePercent: 6.1,
      comfortRatePercent: 96.8,
      comfortComparePercent: 1.4,
      savingEnergyKWh: 2860,
      savingEnergyComparePercent: 12.6,
      savingCostCny: 2210,
      savingCostComparePercent: 12.6,
      carbonReductionTco2e: 1.63,
      carbonComparePercent: 12.6,
    },
    topology: [
      { key: 'chiller', label: '冷水机组', running: 2, total: 3, powerKW: 450 },
      { key: 'chwp', label: '冷冻水泵', running: 2, total: 3, powerKW: 87 },
      { key: 'cwp', label: '冷却水泵', running: 2, total: 3, powerKW: 74 },
      { key: 'ct', label: '冷却塔', running: 2, total: 2, powerKW: 42 },
      { key: 'ahu', label: '空调末端', running: 36, total: 42, powerKW: 128 },
    ],
    loadTrend: trend.map(([at, actual, baseline]) => ({ at, actual, baseline })),
    loadSummary: { currentKW: 486, baselineKW: 548, savingKW: 62, savingRatePercent: 11.3 },
    coolingTrend: trend.map(([at, actual, baseline]) => ({ at, actual: actual * 6.5, baseline: baseline * 6.5 })),
    coolingSummary: { currentRT: 3260, baselineRT: 3480, savingRT: 220, savingRatePercent: 6.3 },
    temperatureTrend: trend.map(([at], index) => ({
      at,
      supply: 6.9 + index * 0.03,
      return: 11.8 + index * 0.05,
      setpoint: 7,
    })),
    waterTemperatures: { supplyC: 7.1, returnC: 12.2, setpointC: 7 },
    coolingWaterTemperatureTrend: trend.map(([at], index) => ({
      at,
      supply: 29.2 + index * 0.08,
      return: 34.1 + index * 0.06,
      setpoint: 29,
    })),
    coolingWaterTemperatures: { supplyC: 29.6, returnC: 34.5, setpointC: 29 },
    alarmSeverity: { critical: 0, major: 1, warning: 2 },
    priorityAlarms: [
      {
        severity: 'MAJOR',
        title: '2# 冷水机组冷凝压力偏高',
        locationLabel: '中央冷站 B1 机房',
        deviceLabel: 'CH-02',
        occurredAt: '2026-09-21T05:18:00.000Z',
      },
      {
        severity: 'WARNING',
        title: '冷冻水供回水温差偏低',
        locationLabel: '冷冻水主管',
        deviceLabel: 'CHW-MAIN',
        occurredAt: '2026-09-21T04:42:00.000Z',
      },
    ],
    deviceStatus: {
      total: 200,
      running: 185,
      runningPercent: 92.5,
      stopped: 10,
      stoppedPercent: 5,
      fault: 3,
      faultPercent: 1.5,
      offline: 2,
      offlinePercent: 1,
    },
    energyBreakdown: [
      { key: 'chiller', label: '冷水机组', energyKWh: 10240, percent: 55.6, costCny: 7936, costPercent: 55.6 },
      { key: 'pumps', label: '输配水泵', energyKWh: 3680, percent: 20.0, costCny: 2852, costPercent: 20.0 },
      { key: 'towers', label: '冷却塔', energyKWh: 1475, percent: 8.0, costCny: 1143, costPercent: 8.0 },
      { key: 'terminals', label: '空调末端', energyKWh: 3025, percent: 16.4, costCny: 2344, costPercent: 16.4 },
    ],
    savingsPerformance: {
      actualEnergyKWh: 18420,
      baselineEnergyKWh: 21280,
      savingEnergyKWh: 2860,
      savingRatePercent: 13.4,
      savingCostCny: 2210,
      carbonReductionTco2e: 1.63,
      actualTrend: trend.map(([at, actual]) => ({ at, actual: actual * 4.7 })),
    },
    opportunities: [
      { rank: 1, title: '冷冻水供水温度动态重置', priority: 'HIGH', savingKWhPerDay: 520 },
      { rank: 2, title: '冷却塔逼近度优化', priority: 'MEDIUM', savingKWhPerDay: 310 },
      { rank: 3, title: '低负荷水泵群控优化', priority: 'MEDIUM', savingKWhPerDay: 265 },
    ],
    strategies: [
      { title: '冷冻水温度重置策略', status: 'RUNNING', savingKWh: 1240 },
      { title: '主机台数优化策略', status: 'RUNNING', savingKWh: 980 },
      { title: '需量响应策略', status: 'PENDING', savingKWh: 0 },
    ],
  };
}
