import { queryOptions } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import {
  S2TelemetryClientError,
  createS2TelemetryClient,
  type DeviceHistoryAggregateBucket,
} from '@/api/generated/s2Telemetry.gen';
import type { TrendGranularity } from './timeline';
import { groupTrendDeviceRequests, type TrendPointItem } from './trends-model';

const gatewayClient = createPlatformGatewayClient();
const telemetryClient = createS2TelemetryClient();

export function siteAssetModelQuery(siteId: string) {
  return queryOptions({
    queryKey: ['site-asset-model', siteId],
    queryFn: async ({ signal }) => {
      const response = await gatewayClient.getSiteAssetModel(siteId, { signal });
      return response.data;
    },
    staleTime: 5 * 60_000,
  });
}

export class TrendHistoryPermissionError extends Error {
  readonly status: number;

  constructor(status: number, detail: string) {
    super(detail);
    this.name = 'TrendHistoryPermissionError';
    this.status = status;
  }
}

export function trendAggregateWindowQuery(input: {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly points: readonly TrendPointItem[];
  readonly fromMs: number;
  readonly toMs: number;
  readonly granularity: TrendGranularity;
  readonly enabled?: boolean;
}) {
  const requests = groupTrendDeviceRequests(input.points);
  const requestSignature = requests
    .map((req) => `${req.deviceId}:${req.keys.join(',')}`)
    .sort()
    .join('|');

  return queryOptions({
    queryKey: [
      'trends-aggregate',
      input.siteId,
      requestSignature,
      input.fromMs,
      input.toMs,
      input.granularity,
      input.timezone,
    ],
    enabled: (input.enabled ?? true) && requests.length > 0 && input.toMs > input.fromMs,
    queryFn: async ({ signal }): Promise<ReadonlyMap<string, readonly DeviceHistoryAggregateBucket[]>> => {
      const fromIso = new Date(input.fromMs).toISOString();
      const toIso = new Date(input.toMs).toISOString();
      const entries = await Promise.all(
        requests.map(async (req) => {
          try {
            const response = await telemetryClient.queryDeviceHistoryAggregate(
              {
                deviceId: req.deviceId,
                keys: [...req.keys],
                from: fromIso,
                to: toIso,
                granularity: input.granularity,
                timezone: input.timezone,
                qualityPolicy: 'USABLE',
              },
              { csrfToken: input.csrfToken, signal },
            );
            return [req.deviceId, response.buckets] as const;
          } catch (error) {
            if (error instanceof S2TelemetryClientError && (error.problem.status === 401 || error.problem.status === 403)) {
              throw new TrendHistoryPermissionError(error.problem.status, error.problem.detail);
            }
            throw error;
          }
        }),
      );
      return new Map(entries);
    },
    staleTime: 30_000,
    placeholderData: (previous) => previous,
    retry: (failureCount, error) => {
      if (error instanceof TrendHistoryPermissionError) return false;
      return failureCount < 1;
    },
  });
}
