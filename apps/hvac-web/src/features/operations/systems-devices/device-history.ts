import { queryOptions } from '@tanstack/react-query';
import { createS2TelemetryClient, type DeviceHistoryAggregateBucket } from '@/api/generated/s2Telemetry.gen';

const client = createS2TelemetryClient();
const HOUR_MS = 3_600_000;

/** The history owner accepts at most eight keys per query. */
export const MAX_HISTORY_KEYS = 8;

export interface HourlyPoint {
  readonly periodStart: string;
  readonly average: number;
  readonly minimum: number;
  readonly maximum: number;
}

/**
 * Hourly gauge aggregates over the last 24 hours, including the current partial hour.
 * Like a ThingsBoard time-series widget on "last 24 hours, 1 hour AVG", buckets with no
 * samples are simply absent.
 */
export function deviceDayHistoryQuery(input: {
  readonly siteId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
  readonly timezone: string;
  readonly csrfToken: string;
}) {
  const nextHour = Math.floor(Date.now() / HOUR_MS) * HOUR_MS + HOUR_MS;
  return queryOptions({
    queryKey: ['device-history', input.siteId, input.deviceId, input.keys, nextHour],
    queryFn: async ({ signal }) => {
      const response = await client.queryDeviceHistoryAggregate({
        deviceId: input.deviceId,
        keys: [...input.keys],
        from: new Date(nextHour - 24 * HOUR_MS).toISOString(),
        to: new Date(nextHour).toISOString(),
        granularity: 'HOUR',
        timezone: input.timezone,
        qualityPolicy: 'USABLE',
      }, { csrfToken: input.csrfToken, signal });
      return groupByKey(response.buckets);
    },
    staleTime: 5 * 60_000,
  });
}

function groupByKey(buckets: readonly DeviceHistoryAggregateBucket[]): ReadonlyMap<string, readonly HourlyPoint[]> {
  const series = new Map<string, HourlyPoint[]>();
  for (const bucket of buckets) {
    if (!bucket.gauge) continue;
    const points = series.get(bucket.telemetryKey) ?? [];
    points.push({
      periodStart: bucket.periodStart,
      average: bucket.gauge.average,
      minimum: bucket.gauge.minimum,
      maximum: bucket.gauge.maximum,
    });
    series.set(bucket.telemetryKey, points);
  }
  return series;
}
