import { queryOptions, useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { listAlarms, type Alarm } from '@/features/alarms/alarm-api';
import { quarterHours } from '@/features/load/demand-model';
import { plantPowerPoint } from '@/features/load/query';
import { siteDayKey, siteMidnight, wallTime } from '@/features/energy-analysis/consumption/model';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { siteTariffQuery } from '@/features/tariff/query';
import { touEnergyCost } from '@/features/tariff/tariff-model';
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

const accept = { Accept: 'application/json, application/problem+json' };
const sitePath = (template: string, siteId: string) => template.replace('{siteId}', siteId);

/** A month's latest snapshot, or null when the month has none. */
export function monthlySnapshotQuery(siteId: string, month: string) {
  return queryOptions({
    queryKey: ['report-snapshot', siteId, month],
    queryFn: async ({ signal }): Promise<StoredReport | null> => {
      const response = await fetch(sitePath(intelligencePaths.siteMonthlyReport, siteId).replace('{month}', month), { credentials: 'same-origin', signal, headers: accept });
      if (response.status === 404) return null;
      if (!response.ok) throw new Error('报告存档暂不可用');
      return (await response.json()) as StoredReport;
    },
    retry: false,
  });
}

/** The latest snapshot of each archived month of a year. */
export function yearSnapshotsQuery(siteId: string, year: number) {
  return queryOptions({
    queryKey: ['report-snapshots', siteId, year],
    queryFn: async ({ signal }): Promise<StoredReport[]> => {
      const response = await fetch(`${sitePath(intelligencePaths.siteMonthlyReports, siteId)}?year=${year}`, { credentials: 'same-origin', signal, headers: accept });
      if (!response.ok) throw new Error('报告存档暂不可用');
      return ((await response.json()) as { items: StoredReport[] }).items;
    },
    retry: false,
  });
}

/** The month's electricity and cooling by day against the month before, with its hourly electricity. */
export function useMonthEnergy(month: string, span: ReportWindow, enabled = true) {
  const { site, read, summary } = useEnergyReader();
  return useQuery({
    queryKey: ['monthly-report', site.id, month, span.to.getTime(), 'energy'],
    enabled,
    queryFn: async ({ signal }) => {
      const [current, previous, hourly] = await Promise.all([
        summary('day', span.from, span.to, signal),
        summary('day', span.previousFrom, span.previousTo, signal),
        read('electricity', 'hour', span.from, span.to, signal),
      ]);
      return { current, previous, hourly: hourly.points };
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
 * The year's months on the live series: electricity and cooling by month, and each month's hourly
 * electricity priced at the Site's Tariff. Months with a snapshot read from it instead.
 */
export function useYearLive(year: number, now: Date) {
  const { site, read, summary } = useEnergyReader();
  const tariff = useSiteTariff();
  const from = siteMidnight(year, 1, 1, site.timezone);
  const end = siteMidnight(year + 1, 1, 1, site.timezone);
  const to = end.getTime() < now.getTime() ? end : now;
  return useQuery({
    queryKey: ['yearly-report', site.id, year, to.getTime(), tariff.data?.version ?? null],
    enabled: !tariff.isPending && from.getTime() < to.getTime(),
    queryFn: async ({ signal }): Promise<Map<string, LiveMonth>> => {
      const [months, hourly] = await Promise.all([summary('month', from, to, signal), read('electricity', 'hour', from, to, signal)]);
      const hoursByMonth = new Map<string, { periodStart: string; energyKWh: number }[]>();
      for (const point of hourly.points) {
        const month = siteDayKey(new Date(point.periodStart), site.timezone).slice(0, 7);
        hoursByMonth.set(month, [...(hoursByMonth.get(month) ?? []), point]);
      }
      const live = new Map<string, LiveMonth>();
      for (const row of months.rows) {
        const local = wallTime(new Date(row.periodStart), site.timezone);
        const month = `${local.year}-${String(local.month).padStart(2, '0')}`;
        const hours = hoursByMonth.get(month) ?? [];
        live.set(month, {
          electricityKWh: row.electricityKWh,
          coolingKWh: row.coolingKWh,
          energyCost: tariff.data && hours.length > 0 ? touEnergyCost(hours, tariff.data, site.timezone).total : null,
        });
      }
      return live;
    },
    retry: false,
  });
}
