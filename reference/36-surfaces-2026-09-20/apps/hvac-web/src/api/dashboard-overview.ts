import { z } from 'zod';

const trendPointSchema = z.object({
  at: z.string().datetime(),
  actual: z.number().nullable(),
  baseline: z.number().nullable().optional(),
  forecast: z.number().nullable().optional(),
}).strict();

const temperaturePointSchema = z.object({
  at: z.string().datetime(),
  supply: z.number().nullable(),
  return: z.number().nullable(),
  setpoint: z.number().nullable().optional(),
}).strict();

const topologyNodeSchema = z.object({
  key: z.string().min(1),
  label: z.string().min(1),
  running: z.number().int().nonnegative().nullable(),
  total: z.number().int().nonnegative().nullable(),
  powerKW: z.number().nonnegative().nullable(),
}).strict();

const categorySchema = z.object({
  key: z.string().min(1),
  label: z.string().min(1),
  energyKWh: z.number().nonnegative().nullable(),
  percent: z.number().min(0).max(100).nullable(),
  costCny: z.number().nonnegative().nullable().optional(),
  costPercent: z.number().min(0).max(100).nullable().optional(),
}).strict();

const opportunitySchema = z.object({
  rank: z.number().int().positive(),
  title: z.string().min(1),
  priority: z.enum(['HIGH', 'MEDIUM', 'LOW']),
  savingKWhPerDay: z.number().nonnegative().nullable(),
}).strict();

const strategySchema = z.object({
  title: z.string().min(1),
  status: z.enum(['RUNNING', 'STOPPED', 'PENDING']),
  savingKWh: z.number().nonnegative().nullable(),
}).strict();

const alarmSeveritySummarySchema = z.object({
  critical: z.number().int().nonnegative(),
  major: z.number().int().nonnegative(),
  warning: z.number().int().nonnegative(),
}).strict();

const priorityAlarmSchema = z.object({
  severity: z.enum(['CRITICAL', 'MAJOR', 'WARNING']),
  title: z.string().min(1),
  locationLabel: z.string().min(1),
  deviceLabel: z.string().min(1),
  occurredAt: z.string().datetime(),
}).strict();

const deviceStatusSchema = z.object({
  total: z.number().int().nonnegative(),
  running: z.number().int().nonnegative(),
  runningPercent: z.number().min(0).max(100).nullable(),
  stopped: z.number().int().nonnegative(),
  stoppedPercent: z.number().min(0).max(100).nullable(),
  fault: z.number().int().nonnegative(),
  faultPercent: z.number().min(0).max(100).nullable(),
  offline: z.number().int().nonnegative(),
  offlinePercent: z.number().min(0).max(100).nullable(),
}).strict();

export const dashboardOverviewSchema = z.object({
  schemaVersion: z.literal(1),
  siteId: z.string(),
  asOf: z.string().datetime(),
  weather: z.object({
    temperatureC: z.number().nullable(),
    condition: z.string().min(1).nullable(),
  }).strict(),
  kpis: z.object({
    coolingTodayRT: z.number().nonnegative().nullable(),
    coolingComparePercent: z.number().nullable(),
    totalLoadKW: z.number().nonnegative().nullable(),
    loadComparePercent: z.number().nullable(),
    averageCop: z.number().nonnegative().nullable(),
    copComparePercent: z.number().nullable(),
    comfortRatePercent: z.number().min(0).max(100).nullable(),
    comfortComparePercent: z.number().nullable(),
    savingEnergyKWh: z.number().nonnegative().nullable(),
    savingEnergyComparePercent: z.number().nullable(),
    savingCostCny: z.number().nonnegative().nullable(),
    savingCostComparePercent: z.number().nullable(),
    carbonReductionTco2e: z.number().nonnegative().nullable(),
    carbonComparePercent: z.number().nullable(),
  }).strict(),
  topology: z.array(topologyNodeSchema),
  loadTrend: z.array(trendPointSchema),
  loadSummary: z.object({
    currentKW: z.number().nonnegative().nullable(),
    baselineKW: z.number().nonnegative().nullable(),
    savingKW: z.number().nonnegative().nullable(),
    savingRatePercent: z.number().nullable(),
  }).strict(),
  coolingTrend: z.array(trendPointSchema).optional(),
  coolingSummary: z.object({
    currentRT: z.number().nonnegative().nullable(),
    baselineRT: z.number().nonnegative().nullable(),
    savingRT: z.number().nonnegative().nullable(),
    savingRatePercent: z.number().nullable(),
  }).strict().optional(),
  temperatureTrend: z.array(temperaturePointSchema),
  waterTemperatures: z.object({
    supplyC: z.number().nullable(),
    returnC: z.number().nullable(),
    setpointC: z.number().nullable(),
  }).strict(),
  coolingWaterTemperatureTrend: z.array(temperaturePointSchema).optional(),
  coolingWaterTemperatures: z.object({
    supplyC: z.number().nullable(),
    returnC: z.number().nullable(),
    setpointC: z.number().nullable(),
  }).strict().optional(),
  alarmSeverity: alarmSeveritySummarySchema,
  priorityAlarms: z.array(priorityAlarmSchema).max(3),
  deviceStatus: deviceStatusSchema,
  energyBreakdown: z.array(categorySchema),
  savingsPerformance: z.object({
    actualEnergyKWh: z.number().nonnegative().nullable(),
    baselineEnergyKWh: z.number().nonnegative().nullable(),
    savingEnergyKWh: z.number().nonnegative().nullable(),
    savingRatePercent: z.number().nullable(),
    savingCostCny: z.number().nonnegative().nullable(),
    carbonReductionTco2e: z.number().nonnegative().nullable(),
    actualTrend: z.array(trendPointSchema),
  }).strict(),
  opportunities: z.array(opportunitySchema).max(5),
  strategies: z.array(strategySchema).max(5),
}).strict();

export type DashboardOverview = z.infer<typeof dashboardOverviewSchema>;

export class DashboardOverviewError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'DashboardOverviewError';
  }
}

export async function readDashboardOverview(siteId: string, signal?: AbortSignal): Promise<DashboardOverview> {
  const response = await fetch(`/api/v1/sites/${encodeURIComponent(siteId)}/dashboard-overview`, {
    method: 'GET',
    credentials: 'same-origin',
    signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) throw new DashboardOverviewError(response.status, 'Dashboard overview projection is unavailable.');
  return dashboardOverviewSchema.parse(payload);
}
