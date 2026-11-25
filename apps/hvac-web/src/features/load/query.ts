import { queryOptions, useQueries } from '@tanstack/react-query';
import { useMemo } from 'react';
import { z } from 'zod';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE } from '@/api/gateway-client';
import { intelligencePaths, type SiteAssetModel } from '@/api/generated/platformGateway.gen';
import { createS2TelemetryClient, type DeviceHistoryAggregateBucket } from '@/api/generated/s2Telemetry.gen';
import { alignDemandEndToQuarter, demandChunkWindows, quarterHours, type QuarterHour } from './demand-model';
import { parseLoadForecast, type LoadForecast, type PublishedForecast } from './forecast-model';

const forecastQualitySchema = z.enum(['VALID', 'FALLBACK']);

const publishedForecastSchema: z.ZodType<PublishedForecast> = z.object({
  snapshot: z.object({
    forecastOrigin: z.string(),
    quality: forecastQualitySchema,
  }),
  points: z.array(z.object({
    forecast_for: z.string(),
    value: z.number(),
    lower_bound: z.number().nullable(),
    upper_bound: z.number().nullable(),
    quality: forecastQualitySchema,
  })),
});

const client = createS2TelemetryClient();

/** The plant's total active power point: the HVAC meter's reading demand is measured on. */
export function plantPowerPoint(model: Pick<SiteAssetModel, 'telemetryPoints'>): { deviceId: string; pointCode: string } | null {
  const point = model.telemetryPoints.find((candidate) => candidate.sourceKey === 'hvac_meter.active_power' && candidate.status === 'ACTIVE');
  return point ? { deviceId: point.reportingDeviceId, pointCode: point.pointCode } : null;
}

/**
 * Quarter-hour demand over `[from, to)`, split into 10-day owner chunks.
 * Completed chunks carry immutable cache keys and long `staleTime`; the live trailing chunk aligns its end
 * to the enclosing quarter-hour for rollup hits while computing bucket completion against real `now`.
 */
export function useDemandChunks(input: {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  /** Null until the Registry names the meter; nothing is read before then. */
  readonly point: { deviceId: string; pointCode: string } | null;
  readonly from: Date;
  readonly to: Date;
  readonly now: Date;
  readonly refetchIntervalMs?: number | false;
}) {
  const { point } = input;
  const chunks = useMemo(
    () => (point === null ? [] : demandChunkWindows(input.from, input.to, input.now.getTime())),
    [input.from, input.now, input.to, point],
  );
  const queries = useQueries({
    queries: chunks.map((chunk) => queryOptions({
      queryKey: [
        'demand-chunk',
        input.siteId,
        point?.deviceId ?? 'NONE',
        point?.pointCode ?? 'NONE',
        chunk.from.toISOString(),
        chunk.closed ? chunk.to.toISOString() : 'live',
      ] as const,
      enabled: point !== null,
      queryFn: async ({ signal }): Promise<readonly DeviceHistoryAggregateBucket[]> => {
        const toDate = chunk.closed ? chunk.to : alignDemandEndToQuarter(new Date(Math.max(chunk.to.getTime(), Date.now())));
        const response = await client.queryDeviceHistoryAggregate({
          deviceId: point!.deviceId,
          keys: [point!.pointCode],
          from: chunk.from.toISOString(),
          to: toDate.toISOString(),
          granularity: 'QUARTER_HOUR',
          timezone: input.timezone,
          qualityPolicy: 'USABLE',
        }, { csrfToken: input.csrfToken, signal });
        return response.buckets;
      },
      staleTime: chunk.closed ? 30 * 60_000 : 60_000,
      refetchInterval: chunk.closed ? false : (input.refetchIntervalMs ?? false),
      placeholderData: chunk.closed ? undefined : ((previous, previousQuery) =>
        previousQuery?.queryKey[1] === input.siteId ? previous : undefined),
    })),
  });

  const buckets = useMemo(() => {
    if (point === null || chunks.length === 0) return undefined;
    if (queries.some((q) => q.data === undefined)) return undefined;
    return queries.flatMap((q) => q.data ?? []);
  }, [chunks.length, point, queries]);

  const data = useMemo<QuarterHour[] | undefined>(
    () => (buckets ? quarterHours(buckets, input.now) : undefined),
    [buckets, input.now],
  );

  const isError = queries.some((q) => q.isError);
  const isPending = point === null || (queries.some((q) => q.isPending) && data === undefined);
  const refetch = () => Promise.all(queries.map((q) => q.refetch()));

  return useMemo(() => ({
    data,
    isPending,
    isError,
    refetch,
  }), [data, isError, isPending, queries]);
}

export class ForecastRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

/** The latest published Site load forecast; null until the Forecast owner has published one. */
export function siteLoadForecastQuery(siteId: string) {
  return queryOptions({
    queryKey: ['forecast', siteId, 'site-load'],
    queryFn: async ({ signal }): Promise<LoadForecast | null> => {
      const result = await gatewayRequest(
        gatewayPath(intelligencePaths.siteLoadForecast, { siteId }),
        {
          signal,
          parse: (payload) => parseLoadForecast(publishedForecastSchema.parse(payload)),
        },
      );
      if (result.kind === 'ok') return result.value;
      if (result.kind === 'notPublished') return null;
      throw new ForecastRequestError(
        failureStatus(result),
        result.kind === 'routeMissing'
          ? ROUTE_MISSING_MESSAGE
          : (result.kind === 'refused' ? (result.problem.detail ?? '预测服务暂时不可用') : '预测服务暂时不可用'),
      );
    },
    // A new forecast is published every hour; five minutes keeps the page close to it.
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}
