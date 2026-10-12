import { queryOptions } from '@tanstack/react-query';
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
  SAVINGS_REPORTING_OVERLAP: '同一站点同一时间只能有一项措施在核算，请先结束正在核算的措施',
  SAVINGS_BASELINE_LOCKED: '已发布核算结果，基线已冻结，不能重新拟合',
  SAVINGS_MEASURE_STATE_CONFLICT: '措施当前状态不允许此操作，请刷新后重试',
  SAVINGS_REQUEST_INVALID: '填写的内容不完整或超出范围，请检查后重试',
  SAVINGS_MANAGE_FORBIDDEN: '你没有管理本站节能项目的权限',
};

const accept = { Accept: 'application/json, application/problem+json' };
const path = (template: string, siteId: string, measureId = '') => template.replace('{siteId}', siteId).replace('{measureId}', measureId);

async function read<T>(url: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(url, { credentials: 'same-origin', signal, headers: accept });
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new SavingsRequestError(response.status, REFUSALS[payload?.code] ?? '节能核算服务暂时不可用');
  return payload as T;
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
  const response = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { ...accept, 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify(body),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new SavingsRequestError(response.status, REFUSALS[payload?.code] ?? '节能核算服务暂时不可用，请稍后重试');
  return payload as Measure;
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
