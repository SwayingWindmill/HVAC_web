import { queryOptions, useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { gatewayPath, gatewayRequest, publishedValueOf, valueOf } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { listAlarms, type Alarm } from '@/features/alarms/alarm-api';
import { quarterHours } from '@/features/load/demand-model';
import { plantPowerPoint } from '@/features/load/query';
import { siteMidnight, wallTime } from '@/features/energy-analysis/consumption/model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { siteTariffQuery } from '@/features/tariff/query';
import { sumCosts } from '@/features/tariff/tariff-model';
import { listView } from '@/features/work-orders/work-order-queries';
import type { LiveMonth, ReportWindow, StoredReport } from './report-model';

const telemetry = createS2TelemetryClient();
const siteRoute = getRouteApi('/_app/_site');
/** The history owner returns at most 1000 buckets: about ten days of quarter-hours. */
const CHUNK_MS = 10 * 86_400_000;
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

/** A month's latest snapshot, or null when the month has none. */
export function monthlySnapshotQuery(siteId: string, month: string) {
  return queryOptions({
    queryKey: ['report-snapshot', siteId, month],
    queryFn: async ({ signal }): Promise<StoredReport | null> => publishedValueOf(
      await gatewayRequest<StoredReport>(gatewayPath(intelligencePaths.siteMonthlyReport, { siteId, month }), { signal }),
      ARCHIVE_UNAVAILABLE,
    ),
    retry: false,
  });
}

/** The latest snapshot of each archived month of a year. */
export function yearSnapshotsQuery(siteId: string, year: number) {
  return queryOptions({
    queryKey: ['report-snapshots', siteId, year],
    queryFn: async ({ signal }): Promise<StoredReport[]> => valueOf(
      await gatewayRequest(`${gatewayPath(intelligencePaths.siteMonthlyReports, { siteId })}?year=${year}`, {
        signal, parse: (payload) => (payload as { items: StoredReport[] }).items,
      }),
      ARCHIVE_UNAVAILABLE,
    ),
    retry: false,
  });
}

/** The month's electricity, its cost and cooling by day, against the month before. */
export function useMonthEnergy(month: string, span: ReportWindow, enabled = true) {
  const { site, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['monthly-report', site.id, month, span.to.getTime(), 'energy'],
    enabled,
    queryFn: async ({ signal }) => {
      const [current, previous] = await Promise.all([
        summary('day', span.from, span.to, signal),
        summary('day', span.previousFrom, span.previousTo, signal),
      ]);
      return { current, previous };
    },
    retry: false,
  });
}

/** The plant's quarter-hour demand over the month, from the HVAC total meter's power. */
export function useMonthDemand(month: string, span: ReportWindow) {
  const { site, principal } = siteRoute.useRouteContext();
  const { registry } = useRealtimePlant();
  const meter = registry.data ? plantPowerPoint(registry.data.assetModel) : null;
  const demand = useQuery({
    queryKey: ['monthly-report', site.id, month, span.to.getTime(), 'demand', meter?.deviceId],
    enabled: meter !== null,
    queryFn: async ({ signal }) => {
      const chunks: { from: Date; to: Date }[] = [];
      for (let start = span.from.getTime(); start < span.to.getTime(); start += CHUNK_MS) {
        chunks.push({ from: new Date(start), to: new Date(Math.min(start + CHUNK_MS, span.to.getTime())) });
      }
      const responses = await Promise.all(chunks.map((chunk) => telemetry.queryDeviceHistoryAggregate({
        deviceId: meter!.deviceId,
        keys: [meter!.pointCode],
        from: chunk.from.toISOString(),
        to: chunk.to.toISOString(),
        granularity: 'QUARTER_HOUR',
        timezone: site.timezone,
        qualityPolicy: 'USABLE',
      }, { csrfToken: principal.session.csrfToken, signal })));
      return quarterHours(responses.flatMap((response) => response.buckets), span.to);
    },
    retry: false,
  });
  return { meter, demand };
}

/** Alarms and work orders, which only their owners keep: never in a snapshot. */
export function useMonthOperations(month: string, span: ReportWindow) {
  const { site } = siteRoute.useRouteContext();
  const key = ['monthly-report', site.id, month, span.to.getTime()];
  const alarms = useQuery({ queryKey: [...key, 'alarms'], queryFn: ({ signal }) => alarmsSince(site.id, span.from, signal), retry: false });
  const workOrders = useQuery({ queryKey: [...key, 'work-orders'], queryFn: ({ signal }) => listView(site.id, [], undefined, signal), retry: false });
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
  const to = end.getTime() < now.getTime() ? end : now;
  return useQuery({
    queryKey: ['yearly-report', site.id, year, to.getTime()],
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
          coolingKWh: row.coolingKWh,
          energyCost: row.electricityKWh === null || cost.unpricedKWh > 0 ? null : cost.total,
        });
      }
      return live;
    },
    retry: false,
  });
}
