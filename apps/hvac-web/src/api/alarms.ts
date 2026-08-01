import { z } from 'zod';
import {
  AlarmApiError,
  alarmListResponseSchema,
  alarmOperationSchema,
  alarmSchema,
  alarmSeveritySchema,
  alarmStatusSchema,
  alarmUUIDv7Schema,
  validateAlarmListScope,
  validateAlarmScope,
  type Alarm,
  type AlarmListResponse,
  type AlarmOperation,
  type AlarmSeverity,
  type AlarmStatus,
} from './alarm-contract';
import { API_MODE } from './config';

export {
  AlarmApiError,
  alarmEvidenceReferenceSchema,
  alarmListResponseSchema,
  alarmOperationSchema,
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
  AlarmOperation,
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
  csrfToken?: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
  fetchImplementation?: typeof fetch;
  baseUrl?: string;
}

export interface AlarmLifecycleInput {
  expectedVersion: number;
  reason: string;
}

export interface AlarmAssignInput extends AlarmLifecycleInput {
  assigneeId: string;
}

export interface AlarmSuppressInput extends AlarmLifecycleInput {
  suppressedUntil: string;
}

const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

const lifecycleInputSchema = z.object({
  expectedVersion: z.number().int().positive(),
  reason: z.string().trim().min(1).max(256),
}).strict();

const assignInputSchema = lifecycleInputSchema.extend({
  assigneeId: z.string().trim().min(1).max(256),
}).strict();

const suppressInputSchema = lifecycleInputSchema.extend({
  suppressedUntil: z.string().datetime({ offset: true }),
}).strict().superRefine((input, context) => {
  const duration = Date.parse(input.suppressedUntil) - Date.now();
  if (duration <= 0 || duration > 30 * 24 * 60 * 60 * 1000) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm suppression interval is invalid' });
  }
});

function alarmPrefix(): string {
  return ALARM_LOCAL_ROUTES_ENABLED ? '/api/v1/local/sites' : '/api/v1/sites';
}

async function alarmRequest<T>(
  path: string,
  schema: z.ZodType<T>,
  init: RequestInit,
  options: ScopedAlarmRequestOptions,
): Promise<T> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}${path}`, {
    ...init,
    credentials: 'same-origin',
    signal: options.signal ?? init.signal,
    headers: {
      Accept: 'application/json, application/problem+json',
      ...Object.fromEntries(new Headers(init.headers)),
    },
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

function validatedScope(options: ScopedAlarmRequestOptions): { organizationId: string; siteId: string } {
  return {
    organizationId: alarmUUIDv7Schema.parse(options.trustedOrganizationId),
    siteId: alarmUUIDv7Schema.parse(options.trustedSiteId),
  };
}

export async function listScopedAlarms(
  filter: AlarmListFilter,
  options: ScopedAlarmRequestOptions,
): Promise<AlarmListResponse> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 读取路由已登记，但尚未启用生产流量。');
  }
  const { organizationId, siteId } = validatedScope(options);
  const parameters = new URLSearchParams();
  if (filter.status) parameters.set('status', alarmStatusSchema.parse(filter.status));
  if (filter.severity) parameters.set('severity', alarmSeveritySchema.parse(filter.severity));
  if (filter.cursor) parameters.set('cursor', alarmUUIDv7Schema.parse(filter.cursor));
  const limit = filter.limit ?? 50;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
    throw new AlarmApiError(400, 'ALARM_FILTER_INVALID', 'Alarm 列表数量必须在 1 到 100 之间。');
  }
  parameters.set('limit', String(limit));
  const payload = await alarmRequest(
    `${alarmPrefix()}/${encodeURIComponent(siteId)}/alarms?${parameters.toString()}`,
    alarmListResponseSchema,
    { method: 'GET' },
    options,
  );
  return validateAlarmListScope(payload, { trustedOrganizationId: organizationId, trustedSiteId: siteId });
}

export async function getScopedAlarm(
  alarmId: string,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 读取路由已登记，但尚未启用生产流量。');
  }
  const { organizationId, siteId } = validatedScope(options);
  const validatedAlarmId = alarmUUIDv7Schema.parse(alarmId);
  const payload = await alarmRequest(
    `${alarmPrefix()}/${encodeURIComponent(siteId)}/alarms/${encodeURIComponent(validatedAlarmId)}`,
    alarmSchema,
    { method: 'GET' },
    options,
  );
  return validateAlarmScope(payload, { trustedOrganizationId: organizationId, trustedSiteId: siteId });
}

async function mutateScopedAlarm(
  alarmId: string,
  operation: Exclude<AlarmOperation, 'PUBLISH'>,
  input: AlarmLifecycleInput | AlarmAssignInput | AlarmSuppressInput,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 生命周期路由已登记，但尚未启用生产流量。');
  }
  const { organizationId, siteId } = validatedScope(options);
  const validatedAlarmId = alarmUUIDV7(alarmId);
  const validatedOperation = alarmOperationSchema.exclude(['PUBLISH']).parse(operation);
  if (!options.csrfToken) {
    throw new AlarmApiError(401, 'CSRF_REQUIRED', '认证会话没有提供 CSRF 能力。');
  }
  const idempotencyKey = options.idempotencyKey ?? `real-alarm-${crypto.randomUUID()}`;
  const payload = await alarmRequest(
    `${alarmPrefix()}/${encodeURIComponent(siteId)}/alarms/${encodeURIComponent(validatedAlarmId)}:${validatedOperation.toLowerCase()}`,
    alarmSchema,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': options.csrfToken,
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(input),
    },
    options,
  );
  return validateAlarmScope(payload, { trustedOrganizationId: organizationId, trustedSiteId: siteId });
}

function alarmUUIDV7(value: string): string {
  try {
    return alarmUUIDv7Schema.parse(value);
  } catch {
    throw new AlarmApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Alarm。');
  }
}

export function acknowledgeScopedAlarm(alarmId: string, input: AlarmLifecycleInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'ACKNOWLEDGE', lifecycleInputSchema.parse(input), options);
}

export function assignScopedAlarm(alarmId: string, input: AlarmAssignInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'ASSIGN', assignInputSchema.parse(input), options);
}

export function unassignScopedAlarm(alarmId: string, input: AlarmLifecycleInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'UNASSIGN', lifecycleInputSchema.parse(input), options);
}

export function suppressScopedAlarm(alarmId: string, input: AlarmSuppressInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'SUPPRESS', suppressInputSchema.parse(input), options);
}

export function unsuppressScopedAlarm(alarmId: string, input: AlarmLifecycleInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'UNSUPPRESS', lifecycleInputSchema.parse(input), options);
}

export function closeScopedAlarm(alarmId: string, input: AlarmLifecycleInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'CLOSE', lifecycleInputSchema.parse(input), options);
}

export function reopenScopedAlarm(alarmId: string, input: AlarmLifecycleInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  return mutateScopedAlarm(alarmId, 'REOPEN', lifecycleInputSchema.parse(input), options);
}

export function alarmErrorMessage(error: unknown): string {
  if (error instanceof AlarmApiError) return error.message;
  if (error instanceof z.ZodError) return 'Alarm 服务返回了不符合权威契约的数据。';
  if (error instanceof Error) return error.message;
  return 'Alarm 服务暂时不可用。';
}
