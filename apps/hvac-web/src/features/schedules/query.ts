import { queryOptions } from '@tanstack/react-query';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type CreateOperatingExceptionRequest,
  type DecideOperatingExceptionRequest,
  type OperatingExceptionStatus,
  type OperatingScheduleMutation,
  type SiteTariff,
} from '@/api/generated/platformGateway.gen';
import {
  S2TelemetryClientError,
  createS2TelemetryClient,
  type DeviceHistoryAggregateBucket,
} from '@/api/generated/s2Telemetry.gen';
import { listWorkOrders, type WorkOrderList } from '@/api/work-orders';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import {
  deviationWorkOrderResourceId,
  type ScheduleDeviation,
  type SchedulePlantMonitorTarget,
} from './schedule-model';

const gatewayClient = createPlatformGatewayClient();
const telemetryClient = createS2TelemetryClient();

export const scheduleKeys = {
  schedule: (siteId: string) => ['operating-schedule', siteId] as const,
  exceptionsRoot: (siteId: string) => ['operating-exceptions', siteId] as const,
  exceptionsWindow: (siteId: string, from: string, to: string) =>
    ['operating-exceptions', siteId, 'window', from, to] as const,
  exceptionsList: (siteId: string, status?: OperatingExceptionStatus) =>
    ['operating-exceptions', siteId, 'list', status ?? 'ALL'] as const,
  tariff: (siteId: string) => ['site-tariff', siteId] as const,
  plantQuarterHour: (
    siteId: string,
    signature: string,
    fromMs: number,
    toMs: number,
    timezone: string,
  ) => ['schedule-plant-15m', siteId, signature, fromMs, toMs, timezone] as const,
};

export function siteOperatingScheduleQuery(siteId: string) {
  return queryOptions({
    queryKey: scheduleKeys.schedule(siteId),
    queryFn: ({ signal }) => gatewayClient.getSiteOperatingSchedule(siteId, { signal }),
    select: (response) => response.data,
    staleTime: 30_000,
  });
}

export function siteOperatingExceptionsWindowQuery(siteId: string, from: string, to: string) {
  return queryOptions({
    queryKey: scheduleKeys.exceptionsWindow(siteId, from, to),
    queryFn: ({ signal }) => gatewayClient.listSiteOperatingExceptions(siteId, { from, to }, { signal }),
    select: (response) => response.data.items,
    staleTime: 30_000,
  });
}

export function siteOperatingExceptionsListQuery(siteId: string, status?: OperatingExceptionStatus) {
  return queryOptions({
    queryKey: scheduleKeys.exceptionsList(siteId, status),
    queryFn: ({ signal }) =>
      gatewayClient.listSiteOperatingExceptions(siteId, status ? { status } : {}, { signal }),
    select: (response) => response.data.items,
    staleTime: 15_000,
  });
}

export function siteScheduleTariffQuery(siteId: string) {
  return queryOptions({
    queryKey: scheduleKeys.tariff(siteId),
    queryFn: async ({ signal }): Promise<SiteTariff | null> => {
      try {
        const response = await gatewayClient.getSiteTariff(siteId, { signal });
        return response.data;
      } catch (error) {
        if (error instanceof PlatformApiError && error.problem.status === 404) {
          return null;
        }
        throw error;
      }
    },
    staleTime: 5 * 60_000,
    retry: (failureCount, error) => {
      if (error instanceof PlatformApiError && (error.problem.status === 404 || error.problem.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}

export function schedulePlantAggregateQuery(input: {
  readonly siteId: string;
  readonly timezone: string;
  readonly csrfToken: string;
  readonly targets: readonly SchedulePlantMonitorTarget[];
  readonly fromMs: number;
  readonly toMs: number;
  readonly enabled: boolean;
}) {
  const signature = input.targets
    .map((target) => `${target.deviceId}:${target.keys.join(',')}`)
    .sort()
    .join('|');

  return queryOptions({
    queryKey: scheduleKeys.plantQuarterHour(
      input.siteId,
      signature,
      input.fromMs,
      input.toMs,
      input.timezone,
    ),
    enabled: input.enabled && input.targets.length > 0 && input.toMs > input.fromMs,
    queryFn: async ({ signal }): Promise<ReadonlyMap<string, readonly DeviceHistoryAggregateBucket[]>> => {
      const fromIso = new Date(input.fromMs).toISOString();
      const toIso = new Date(input.toMs).toISOString();
      const entries = await Promise.all(
        input.targets.map(async (target) => {
          const response = await telemetryClient.queryDeviceHistoryAggregate(
            {
              deviceId: target.deviceId,
              keys: [...target.keys],
              from: fromIso,
              to: toIso,
              granularity: 'QUARTER_HOUR',
              timezone: input.timezone,
              qualityPolicy: 'USABLE',
            },
            { csrfToken: input.csrfToken, signal },
          );
          return [target.deviceId, response.buckets] as const;
        }),
      );
      return new Map(entries);
    },
    staleTime: 60_000,
    placeholderData: (previous) => previous,
    retry: (failureCount, error) => {
      if (error instanceof S2TelemetryClientError && (error.problem.status === 401 || error.problem.status === 403)) {
        return false;
      }
      return failureCount < 1;
    },
  });
}

export function deviationWorkOrdersQuery(
  siteId: string,
  deviation: ScheduleDeviation | null,
  enabled: boolean,
) {
  const sourceRef = deviation ? deviationWorkOrderResourceId(siteId, deviation) : '';
  return queryOptions({
    queryKey: workOrderKeys.source(siteId, 'EXTERNAL', sourceRef),
    queryFn: ({ signal }): Promise<WorkOrderList> =>
      listWorkOrders({ sourceDomain: 'EXTERNAL', sourceRef, limit: 20 }, { siteId, signal }),
    enabled: enabled && Boolean(deviation),
    staleTime: 15_000,
  });
}

export async function saveOperatingSchedule(
  siteId: string,
  body: OperatingScheduleMutation,
  csrfToken: string,
) {
  const response = await gatewayClient.saveSiteOperatingSchedule(siteId, body, csrfToken);
  return response.data;
}

export async function createOperatingException(
  siteId: string,
  body: CreateOperatingExceptionRequest,
  csrfToken: string,
) {
  const response = await gatewayClient.createSiteOperatingException(siteId, body, csrfToken);
  return response.data;
}

export async function decideOperatingException(
  siteId: string,
  exceptionId: string,
  body: DecideOperatingExceptionRequest,
  csrfToken: string,
) {
  const response = await gatewayClient.decideSiteOperatingException(siteId, exceptionId, body, csrfToken);
  return response.data;
}

export function isRevisionConflictError(error: unknown): boolean {
  return (
    error instanceof PlatformApiError &&
    error.problem.status === 409 &&
    (error.problem.code.includes('REVISION') || error.problem.detail.toLowerCase().includes('revision'))
  );
}

export function scheduleErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof PlatformApiError) {
    const { status, code, detail } = error.problem;
    if (status === 403) {
      return '当前账号没有执行该运行计划操作的权限';
    }
    if (status === 409) {
      if (code.includes('REVISION') || detail.toLowerCase().includes('revision')) {
        return '周运行计划已被其他成员更新，请刷新最新版本后重新保存';
      }
      if (detail.includes('overlap') || code.includes('OVERLAP') || code.includes('CONFLICT')) {
        if (detail.includes('approved operating exception') || detail.includes('date range')) {
          return '该日期范围与已生效的运行例外重叠，请调整起止日期或先撤回已有例外';
        }
        if (detail.includes('slot')) {
          return '同一天的供冷时段存在重叠，请调整起止时间';
        }
      }
      if (detail.includes('only requester')) {
        return '仅申请人本人或站点管理员可以撤回该待审批例外';
      }
      return detail || '当前状态不允许此操作，请刷新后重试';
    }
    if (status === 400) {
      return detail || '填写的运行计划参数不符合校验规则，请检查起止时间与温度范围';
    }
    return detail || fallback;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}
