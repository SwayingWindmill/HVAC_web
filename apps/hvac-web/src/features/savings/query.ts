import { queryOptions } from '@tanstack/react-query';
import { failureStatus, gatewayPath, gatewayRequest, ROUTE_MISSING_MESSAGE } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import type { Measure, MeasureKind, SavingsSummary } from './savings-model';

export class SavingsRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

export const savingsKeys = {
  all: (siteId: string) => ['savings', siteId] as const,
  measures: (siteId: string) => ['savings', siteId, 'measures'] as const,
  measure: (siteId: string, measureId: string) => ['savings', siteId, 'measures', measureId] as const,
  summary: (siteId: string, from: string, to: string) => ['savings', siteId, 'summary', from, to] as const,
};

// The owner's refusals, in the words of the operator who has to act on them.
const REFUSALS: Record<string, string> = {
  SAVINGS_REPORTING_OVERLAP: '同一块电表同一时间只能统计一项措施；用冷站总电表统计的措施，也不能和单台设备的措施同时统计，请先结束正在统计的措施',
  SAVINGS_BOUNDARY_UNMETERED: '这台设备没有逐时用电数据，不能作为计量电表',
  SAVINGS_BASELINE_LOCKED: '已发布统计结果，对比基线已固定，不能重新计算',
  SAVINGS_MEASURE_STATE_CONFLICT: '措施当前状态不允许此操作，请刷新后重试',
  SAVINGS_REQUEST_INVALID: '填写的内容不完整或超出范围，请检查后重试',
  SAVINGS_MANAGE_FORBIDDEN: '你没有管理本站节能项目的权限',
};

const path = (template: string, siteId: string, measureId?: string) =>
  gatewayPath(template, measureId === undefined ? { siteId } : { siteId, measureId });

function unwrapSavingsResult<T>(
  result: Awaited<ReturnType<typeof gatewayRequest<T>>>,
  fallback: string,
): T {
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new SavingsRequestError(result.status, ROUTE_MISSING_MESSAGE);
  const code = result.kind === 'unavailable' ? undefined : result.problem.code;
  throw new SavingsRequestError(failureStatus(result), (code && REFUSALS[code]) ?? fallback);
}

async function read<T>(url: string, signal: AbortSignal): Promise<T> {
  return unwrapSavingsResult(await gatewayRequest<T>(url, { signal }), '节能统计服务暂时不可用');
}

export function measuresQuery(siteId: string) {
  return queryOptions({
    queryKey: savingsKeys.measures(siteId),
    queryFn: ({ signal }) => read<{ items: Measure[] }>(path(intelligencePaths.siteSavingsMeasures, siteId), signal).then((payload) => payload.items),
    retry: false,
  });
}

/** One measure with its baseline checks and every verified day. */
export function measureQuery(siteId: string, measureId: string) {
  return queryOptions({
    queryKey: savingsKeys.measure(siteId, measureId),
    queryFn: ({ signal }) => read<Measure>(path(intelligencePaths.siteSavingsMeasure, siteId, measureId), signal),
    retry: false,
  });
}

/** Verified savings on the Site between two Site-local dates, inclusive. */
export function summaryQuery(siteId: string, from: string, to: string) {
  return queryOptions({
    queryKey: savingsKeys.summary(siteId, from, to),
    queryFn: ({ signal }) => read<SavingsSummary>(`${path(intelligencePaths.siteSavingsSummary, siteId)}?from=${from}&to=${to}`, signal),
    retry: false,
  });
}

async function write(url: string, csrfToken: string, body: unknown): Promise<Measure> {
  return unwrapSavingsResult(
    await gatewayRequest<Measure>(url, { method: 'POST', csrfToken, body }),
    '节能统计服务暂时不可用，请稍后重试',
  );
}

export interface MeasureDraft {
  readonly name: string;
  readonly kind: MeasureKind;
  readonly description: string;
  readonly plannedAt: string;
  readonly investmentCny?: number;
  readonly expectedAnnualKWh?: number;
  readonly evidence: string;
  readonly evidenceCommandId?: string;
  readonly boundaryDeviceId?: string;
}

export const registerMeasure = (siteId: string, csrfToken: string, draft: MeasureDraft) =>
  write(path(intelligencePaths.siteSavingsMeasures, siteId), csrfToken, draft);

/** Implementation freezes the baseline from the fourteen days before it. */
export const implementMeasure = (siteId: string, measureId: string, csrfToken: string, implementedAt: string) =>
  write(path(intelligencePaths.siteSavingsImplement, siteId, measureId), csrfToken, { implementedAt });

export const refitMeasure = (siteId: string, measureId: string, csrfToken: string, from: string, to: string) =>
  write(path(intelligencePaths.siteSavingsRefit, siteId, measureId), csrfToken, { from, to });

export const endMeasure = (siteId: string, measureId: string, csrfToken: string, endedAt: string) =>
  write(path(intelligencePaths.siteSavingsEnd, siteId, measureId), csrfToken, { endedAt });
