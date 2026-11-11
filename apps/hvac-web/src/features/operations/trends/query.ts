import { queryOptions } from '@tanstack/react-query';
import { createPlatformGatewayClient } from '@/api/generated/platformGateway.gen';
import {
  S2TelemetryClientError,
  createS2TelemetryClient,
  type DeviceHistoryAggregateBucket,
} from '@/api/generated/s2Telemetry.gen';
import { AlarmRequestError, listAlarms, type Alarm } from '@/features/alarms/alarm-api';
import { CommandRequestError, listCommands, type Command } from '@/features/control/command-api';
import type { TrendGranularity } from './timeline';
import { groupTrendDeviceRequests, type TrendPointItem } from './trends-model';

const gatewayClient = createPlatformGatewayClient();
const telemetryClient = createS2TelemetryClient();

export function siteAssetModelQuery(siteId: string) {
  return queryOptions({
    queryKey: ['site-asset-model', siteId],
    queryFn: ({ signal }) => gatewayClient.getSiteAssetModel(siteId, { signal }),
    select: (response) => response.data,
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

const MAX_ALARM_OVERLAY_PAGES = 5;

export function trendOverlappingAlarmsQuery(input: {
  readonly siteId: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly enabled: boolean;
}) {
  const activeFrom = new Date(input.fromMs).toISOString();
  const activeTo = new Date(input.toMs).toISOString();
  return queryOptions({
    queryKey: ['trends-alarms-overlay', input.siteId, activeFrom, activeTo] as const,
    enabled: input.enabled && input.toMs > input.fromMs,
    queryFn: async ({ signal }): Promise<readonly Alarm[]> => {
      const items: Alarm[] = [];
      let cursor: string | undefined;
      for (let page = 0; page < MAX_ALARM_OVERLAY_PAGES; page += 1) {
        const result = await listAlarms(input.siteId, { activeFrom, activeTo }, cursor, signal);
        items.push(...result.items);
        if (!result.hasMore || !result.nextCursor) break;
        cursor = result.nextCursor;
      }
      return items;
    },
    staleTime: 30_000,
    retry: (failureCount, error) => {
      if (error instanceof AlarmRequestError && (error.status === 401 || error.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}

export function trendCommandsQuery(input: {
  readonly siteId: string;
  readonly enabled: boolean;
}) {
  return queryOptions({
    queryKey: ['trends-commands-overlay', input.siteId] as const,
    enabled: input.enabled,
    queryFn: ({ signal }): Promise<readonly Command[]> => listCommands(input.siteId, signal),
    staleTime: 30_000,
    retry: (failureCount, error) => {
      if (error instanceof CommandRequestError && (error.status === 401 || error.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}
