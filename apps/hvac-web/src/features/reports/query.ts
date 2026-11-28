import { queryOptions, useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { z } from 'zod';
import { gatewayPath, gatewayRequest, publishedValueOf, valueOf } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { listAlarms, type Alarm } from '@/features/alarms/alarm-api';
import { plantPowerPoint, useDemandChunks } from '@/features/load/query';
import { energyRetryDelay, retryEnergyQuery, siteDayKey } from '@/features/energy-analysis/consumption/model';
import { siteMidnight, wallTime } from '@/lib/site-time';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { siteTariffQuery } from '@/api/site-tariff';
import { sumCosts } from '@/features/tariff/tariff-model';
import { listView } from '@/features/work-orders/work-order-queries';
import type { LiveMonth, ReportWindow, StoredReport } from './report-model';

const siteRoute = getRouteApi('/_app/_site');
const ALARM_PAGES = 10;

/** Alarms raised from `from` on, walking the newest-first pages until one reaches past it. */
async function alarmsSince(siteId: string, from: Date, signal: AbortSignal): Promise<{ alarms: Alarm[]; complete: boolean }> {
  const alarms: Alarm[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < ALARM_PAGES; page += 1) {
    const result = await listAlarms(siteId, {}, cursor, signal);
    alarms.push(...result.items);
    if (!result.hasMore || result.items.some((alarm) => Date.parse(alarm.firstOccurredAt) < from.getTime())) return { alarms, complete: true };
    cursor = result.nextCursor ?? undefined;
  }
  return { alarms, complete: false };
}

const ARCHIVE_UNAVAILABLE = { unavailable: '报告存档暂不可用' };

const reportLayeredEfficiencySchema = z.object({
  plantCOP: z.number().nullish(),
  chillerCOP: z.number().nullish(),
  chilledPumpWTF: z.number().nullish(),
  coolingPumpWTF: z.number().nullish(),
  towerPowerShare: z.number().nullish(),
});

const reportMeterBalanceSchema = z.object({
  status: z.string().nullish(),
  siteKWh: z.number().nullish(),
  submeterSumKWh: z.number().nullish(),
  diffKWh: z.number().nullish(),
  imbalanceRatio: z.number().nullish(),
});

const reportConditionAnchorsSchema = z.object({
  meanWetBulbC: z.number().nullish(),
  meanDryBulbC: z.number().nullish(),
  meanLoadRatio: z.number().nullish(),
  aiClosedLoopShare: z.number().nullish(),
});

const storedReportSchema: z.ZodType<StoredReport> = z.object({
  id: z.string(),
  month: z.string(),
  version: z.number(),
  generatedAt: z.string(),
  report: z.object({
    schemaVersion: z.literal(1),
    month: z.string(),
    timezone: z.string(),
    daysInMonth: z.number(),
    daysWithData: z.number(),
    electricityKWh: z.number(),
    crossGapElectricityKWh: z.number().nullish().transform((value) => value ?? undefined),
    coolingKWh: z.number(),
    cop: z.number().nullable(),
    energyCostCNY: z.number().nullable(),
    energyCostStatus: z.enum(['PRICED', 'NO_TARIFF', 'UNPRICED_HOURS']),
    tariffPeriods: z.array(z.object({
      code: z.string(),
      electricityKWh: z.number(),
      costCNY: z.number(),
    })),
    days: z.array(z.object({
      date: z.string(),
      electricityKWh: z.number(),
      coolingKWh: z.number(),
      cop: z.number().nullable(),
      energyCostCNY: z.number().nullable(),
    })),
    findings: z.object({
      count: z.number(),
      byType: z.array(z.object({
        findingType: z.string(),
        count: z.number(),
      })),
    }),
    savings: z.object({
      days: z.number(),
      adjustedBaselineKWh: z.number(),
      avoidedKWh: z.number(),
      avoidedCost: z.number(),
      unpricedHours: z.number(),
      plantKWh: z.number(),
    }),
    layeredEfficiency: reportLayeredEfficiencySchema.nullish().transform((value) => value ?? undefined),
    meterBalance: reportMeterBalanceSchema.nullish().transform((value) => value ?? undefined),
    conditionAnchors: reportConditionAnchorsSchema.nullish().transform((value) => value ?? undefined),
  }),
});

const yearSnapshotsResponseSchema = z.object({ items: z.array(storedReportSchema) });

/** A month's latest snapshot, or null when the month has none. */
export function monthlySnapshotQuery(siteId: string, month: string) {
  return queryOptions({
    queryKey: ['report-snapshot', siteId, month],
    queryFn: async ({ signal }): Promise<StoredReport | null> => publishedValueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.siteMonthlyReport, { siteId, month }),
        { signal, parse: (payload) => storedReportSchema.parse(payload) },
      ),
      ARCHIVE_UNAVAILABLE,
    ),
    retry: false,
    staleTime: 5 * 60_000,
  });
}

/** The latest snapshot of each archived month of a year. */
export function yearSnapshotsQuery(siteId: string, year: number) {
  return queryOptions({
    queryKey: ['report-snapshots', siteId, year],
    queryFn: async ({ signal }): Promise<StoredReport[]> => valueOf(
      await gatewayRequest(
        `${gatewayPath(intelligencePaths.siteMonthlyReports, { siteId })}?year=${year}`,
        {
          signal,
          parse: (payload) => yearSnapshotsResponseSchema.parse(payload).items,
        },
      ),
      ARCHIVE_UNAVAILABLE,
    ),
    retry: false,
    staleTime: 5 * 60_000,
  });
}

/** The month's electricity, its cost and cooling by day, against the month before. */
export function useMonthEnergy(month: string, span: ReportWindow, enabled = true) {
  const { site, summary } = useEnergyReader();
  const stateKey = span.complete ? 'closed' : siteDayKey(span.to, site.timezone);
  return useQuery({
    queryKey: ['monthly-report', site.id, month, stateKey, 'energy'],
    enabled,
    queryFn: async ({ signal }) => {
      const [current, previous] = await Promise.all([
        summary('day', span.from, span.to, signal),
        summary('day', span.previousFrom, span.previousTo, signal),
      ]);
      return { current, previous };
    },
    staleTime: span.complete ? 30 * 60_000 : 60_000,
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === site.id ? previousData : undefined,
    retry: retryEnergyQuery,
    retryDelay: energyRetryDelay,
  });
}

/** The plant's quarter-hour demand over the month, from the HVAC total meter's power. */
export function useMonthDemand(month: string, span: ReportWindow) {
  void month;
  const { site, principal } = siteRoute.useRouteContext();
  const assetModel = useQuery(siteAssetModelQuery(site.id));
  const meter = assetModel.data ? plantPowerPoint(assetModel.data) : null;
  const demand = useDemandChunks({
    siteId: site.id,
    timezone: site.timezone,
    csrfToken: principal.session.csrfToken,
    point: meter,
    from: span.from,
    to: span.to,
    now: span.to,
  });
  return { meter, demand };
}

/** Alarms and work orders, which only their owners keep: never in a snapshot. */
export function useMonthOperations(month: string, span: ReportWindow) {
  const { site } = siteRoute.useRouteContext();
  const stateKey = span.complete ? 'closed' : siteDayKey(span.to, site.timezone);
  const key = ['monthly-report', site.id, month, stateKey];
  const staleTime = span.complete ? 30 * 60_000 : 60_000;
  const alarms = useQuery({ queryKey: [...key, 'alarms'], queryFn: ({ signal }) => alarmsSince(site.id, span.from, signal), staleTime, retry: false });
  const workOrders = useQuery({ queryKey: [...key, 'work-orders'], queryFn: ({ signal }) => listView(site.id, [], undefined, signal), staleTime, retry: false });
  return { alarms, workOrders };
}

export function useSiteTariff() {
  const { site } = siteRoute.useRouteContext();
  return useQuery(siteTariffQuery(site.id));
}

/**
 * The year's months on the live series: electricity, its cost and cooling by month. Months with a
 * snapshot read from it instead.
 */
export function useYearLive(year: number, now: Date) {
  const { site, summary } = useEnergyReader();
  const from = siteMidnight(year, 1, 1, site.timezone);
  const end = siteMidnight(year + 1, 1, 1, site.timezone);
  const complete = end.getTime() <= now.getTime();
  const to = complete ? end : now;
  const stateKey = complete ? 'closed' : siteDayKey(to, site.timezone);
  return useQuery({
    queryKey: ['yearly-report', site.id, year, stateKey],
    enabled: from.getTime() < to.getTime(),
    queryFn: async ({ signal }): Promise<Map<string, LiveMonth>> => {
      const months = await summary('month', from, to, signal);
      const live = new Map<string, LiveMonth>();
      for (const row of months.rows) {
        const local = wallTime(new Date(row.periodStart), site.timezone);
        const month = `${local.year}-${String(local.month).padStart(2, '0')}`;
        // As an archived month: electricity no Tariff period covers leaves the month without a cost.
        const cost = sumCosts([row]);
        live.set(month, {
          electricityKWh: row.electricityKWh,
          crossGapElectricityKWh: row.crossGapElectricityKWh,
          coolingKWh: row.coolingKWh,
          energyCost: row.electricityKWh === null || cost.unpricedKWh > 0 ? null : cost.total,
        });
      }
      return live;
    },
    staleTime: complete ? 30 * 60_000 : 60_000,
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === site.id ? previousData : undefined,
    retry: retryEnergyQuery,
    retryDelay: energyRetryDelay,
  });
}
