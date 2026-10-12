import { useQuery } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { createS2TelemetryClient } from '@/api/generated/s2Telemetry.gen';
import { listAlarms, type Alarm } from '@/features/alarms/alarm-api';
import { quarterHours } from '@/features/demand/demand-model';
import { plantPowerPoint } from '@/features/demand/query';
import { siteFindingsQuery } from '@/features/diagnostics/query';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { useRealtimePlant } from '@/features/operations/realtime/use-realtime-plant';
import { siteTariffQuery } from '@/features/tariff/query';
import { listView } from '@/features/work-orders/work-order-queries';
import type { ReportWindow } from './report-model';

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

/** Everything a month's report reads, each from the owner of that record. */
export function useMonthlyReport(month: string, span: ReportWindow) {
  const { site, principal } = siteRoute.useRouteContext();
  const { read, summary } = useEnergyReader();
  const { registry } = useRealtimePlant();
  const meter = registry.data ? plantPowerPoint(registry.data.assetModel) : null;
  const key = ['monthly-report', site.id, month, span.to.getTime()];

  const energy = useQuery({
    queryKey: [...key, 'energy'],
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
  const demand = useQuery({
    queryKey: [...key, 'demand', meter?.deviceId],
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
  const alarms = useQuery({ queryKey: [...key, 'alarms'], queryFn: ({ signal }) => alarmsSince(site.id, span.from, signal), retry: false });
  const workOrders = useQuery({ queryKey: [...key, 'work-orders'], queryFn: ({ signal }) => listView(site.id, [], undefined, signal), retry: false });
  const findings = useQuery(siteFindingsQuery(site.id));
  const tariff = useQuery(siteTariffQuery(site.id));
  return { site, meter, registry, energy, demand, alarms, workOrders, findings, tariff };
}
