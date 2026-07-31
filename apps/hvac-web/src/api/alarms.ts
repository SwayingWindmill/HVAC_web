import { z } from 'zod';
import {
  AlarmApiError,
  alarmListResponseSchema,
  alarmSchema,
  alarmSeveritySchema,
  alarmStatusSchema,
  alarmUUIDv7Schema,
  validateAlarmListScope,
  validateAlarmScope,
  type Alarm,
  type AlarmListResponse,
  type AlarmSeverity,
  type AlarmStatus,
} from './alarm-contract';
import { API_MODE } from './config';

export {
  AlarmApiError,
  alarmEvidenceReferenceSchema,
  alarmListResponseSchema,
  alarmSchema,
  alarmSeveritySchema,
  alarmSourceTypeSchema,
  alarmStatusSchema,
  alarmTransitionSchema,
  validateAlarmListScope,
  validateAlarmScope,
} from './alarm-contract';
export type {
  Alarm,
  AlarmEvidenceReference,
  AlarmListResponse,
  AlarmSeverity,
  AlarmSourceType,
  AlarmStatus,
  AlarmTransition,
} from './alarm-contract';

export const ALARM_PUBLIC_ROUTES_ENABLED = false as const;
export const ALARM_LOCAL_ROUTES_ENABLED = API_MODE === 'real'
  && import.meta.env.DEV
  && (import.meta.env.VITE_S4_LOCAL_ALARMS as string | undefined) === 'true';
export const ALARM_ROUTES_AVAILABLE = ALARM_PUBLIC_ROUTES_ENABLED || ALARM_LOCAL_ROUTES_ENABLED;

export interface AlarmListFilter {
  status?: AlarmStatus;
  severity?: AlarmSeverity;
  cursor?: string;
  limit?: number;
}

export interface ScopedAlarmRequestOptions {
  trustedOrganizationId: string;
  trustedSiteId: string;
  signal?: AbortSignal;
  fetchImplementation?: typeof fetch;
  baseUrl?: string;
}

const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

async function alarmRequest(
  path: string,
  schema: typeof alarmSchema | typeof alarmListResponseSchema,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm | AlarmListResponse> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}${path}`, {
    method: 'GET',
    credentials: 'same-origin',
    signal: options.signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = problemSchema.parse(payload);
    throw new AlarmApiError(
      response.status,
      problem.code ?? 'ALARM_UNAVAILABLE',
      problem.detail ?? problem.title ?? 'Alarm 服务暂时不可用。',
      problem.retryable ?? false,
    );
  }
  return schema.parse(payload);
}

export async function listScopedAlarms(
  filter: AlarmListFilter,
  options: ScopedAlarmRequestOptions,
): Promise<AlarmListResponse> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 读取路由已登记，但尚未启用生产流量。');
  }
  const siteId = alarmUUIDv7Schema.parse(options.trustedSiteId);
  const organizationId = alarmUUIDv7Schema.parse(options.trustedOrganizationId);
  const parameters = new URLSearchParams();
  if (filter.status) parameters.set('status', alarmStatusSchema.parse(filter.status));
  if (filter.severity) parameters.set('severity', alarmSeveritySchema.parse(filter.severity));
  if (filter.cursor) parameters.set('cursor', alarmUUIDv7Schema.parse(filter.cursor));
  const limit = filter.limit ?? 50;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
    throw new AlarmApiError(400, 'ALARM_FILTER_INVALID', 'Alarm 列表数量必须在 1 到 100 之间。');
  }
  parameters.set('limit', String(limit));
  const prefix = ALARM_LOCAL_ROUTES_ENABLED ? '/api/v1/local/sites' : '/api/v1/sites';
  const payload = await alarmRequest(
    `${prefix}/${encodeURIComponent(siteId)}/alarms?${parameters.toString()}`,
    alarmListResponseSchema,
    { ...options, trustedOrganizationId: organizationId, trustedSiteId: siteId },
  ) as AlarmListResponse;
  return validateAlarmListScope(payload, { trustedOrganizationId: organizationId, trustedSiteId: siteId });
}

export async function getScopedAlarm(
  alarmId: string,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 读取路由已登记，但尚未启用生产流量。');
  }
  const siteId = alarmUUIDv7Schema.parse(options.trustedSiteId);
  const organizationId = alarmUUIDv7Schema.parse(options.trustedOrganizationId);
  const validatedAlarmId = alarmUUIDv7Schema.parse(alarmId);
  const prefix = ALARM_LOCAL_ROUTES_ENABLED ? '/api/v1/local/sites' : '/api/v1/sites';
  const payload = await alarmRequest(
    `${prefix}/${encodeURIComponent(siteId)}/alarms/${encodeURIComponent(validatedAlarmId)}`,
    alarmSchema,
    { ...options, trustedOrganizationId: organizationId, trustedSiteId: siteId },
  ) as Alarm;
  return validateAlarmScope(payload, { trustedOrganizationId: organizationId, trustedSiteId: siteId });
}

export function alarmErrorMessage(error: unknown): string {
  if (error instanceof AlarmApiError) return error.message;
  if (error instanceof z.ZodError) return 'Alarm 服务返回了不符合权威契约的数据。';
  if (error instanceof Error) return error.message;
  return 'Alarm 服务暂时不可用。';
}
