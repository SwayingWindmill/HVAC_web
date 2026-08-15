import { z } from 'zod';
import {
  AlarmApiError,
  alarmCursorSchema,
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
import { alarmPaths } from './generated/platformGateway.gen';

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

export const ALARM_PUBLIC_ROUTES_ENABLED = API_MODE === 'real';
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
  trustedTenantId: string;
  trustedSiteId: string;
  csrfToken?: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
  fetchImplementation?: typeof fetch;
  baseUrl?: string;
}

export interface AlarmAcknowledgeInput {
  comment?: string;
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
const publicErrorEnvelopeSchema = z.object({
  error: z.object({
    code: z.string().min(1),
    message: z.string().min(1),
    details: z.unknown(),
  }).strict(),
  meta: z.object({ requestId: z.string().min(1) }).passthrough(),
}).strict();
const publicAlarmEnvelopeSchema = z.object({
  data: alarmSchema,
  meta: z.object({ requestId: z.string().min(1) }).passthrough(),
}).strict();
const publicAlarmListEnvelopeSchema = z.object({
  data: z.array(alarmSchema).max(200),
  meta: z.object({
    requestId: z.string().min(1),
    limit: z.number().int().min(1).max(200),
    nextCursor: alarmCursorSchema.nullable(),
    hasMore: z.boolean(),
  }).passthrough(),
}).strict();
const acknowledgeInputSchema = z.object({
  comment: z.string().trim().max(1000).optional(),
}).strict();

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

function localAlarmPrefix(): string {
  return '/api/v1/local/sites';
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
    const publicEnvelope = publicErrorEnvelopeSchema.safeParse(payload);
    if (publicEnvelope.success) {
      throw new AlarmApiError(
        response.status,
        publicEnvelope.data.error.code,
        publicEnvelope.data.error.message,
        Boolean((publicEnvelope.data.error.details as { retryable?: boolean } | null)?.retryable),
      );
    }
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

function validatedScope(options: ScopedAlarmRequestOptions): { tenantId: string; siteId: string } {
  return {
    tenantId: alarmUUIDv7Schema.parse(options.trustedTenantId),
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
  const { tenantId, siteId } = validatedScope(options);
  const parameters = new URLSearchParams();
  parameters.set('siteId', siteId);
  if (filter.status) parameters.set('status', alarmStatusSchema.parse(filter.status));
  if (filter.severity) parameters.set('severity', alarmSeveritySchema.parse(filter.severity));
  if (filter.cursor) parameters.set('cursor', alarmCursorSchema.parse(filter.cursor));
  const limit = filter.limit ?? 50;
  if (!Number.isInteger(limit) || limit < 1 || limit > 200) {
    throw new AlarmApiError(400, 'INVALID_ARGUMENT', 'Alarm 列表数量必须在 1 到 200 之间。');
  }
  parameters.set('limit', String(limit));
  const payload = await alarmRequest(
    `${alarmPaths.list}?${parameters.toString()}`,
    publicAlarmListEnvelopeSchema,
    { method: 'GET' },
    options,
  );
  const response = alarmListResponseSchema.parse({
    schemaVersion: 1,
    items: payload.data,
    nextCursor: payload.meta.nextCursor,
    hasMore: payload.meta.hasMore,
  });
  return validateAlarmListScope(response, { trustedTenantId: tenantId, trustedSiteId: siteId });
}

export async function getScopedAlarm(
  alarmId: string,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm> {
  if (!ALARM_ROUTES_AVAILABLE) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm 读取路由已登记，但尚未启用生产流量。');
  }
  const { tenantId, siteId } = validatedScope(options);
  const validatedAlarmId = alarmUUIDv7Schema.parse(alarmId);
  const payload = await alarmRequest(
    alarmPaths.detail.replace('{alarmId}', encodeURIComponent(validatedAlarmId)),
    publicAlarmEnvelopeSchema,
    { method: 'GET' },
    options,
  );
  return validateAlarmScope(payload.data, { trustedTenantId: tenantId, trustedSiteId: siteId });
}

async function mutateScopedAlarm(
  alarmId: string,
  operation: Exclude<AlarmOperation, 'PUBLISH' | 'ACKNOWLEDGE'>,
  input: AlarmLifecycleInput | AlarmAssignInput | AlarmSuppressInput,
  options: ScopedAlarmRequestOptions,
): Promise<Alarm> {
  if (!ALARM_LOCAL_ROUTES_ENABLED) {
    throw new AlarmApiError(503, 'ALARM_LIFECYCLE_DISABLED', 'Alarm 生命周期写入仅在本地认证工作台启用，生产流量保持 0%。');
  }
  const { tenantId, siteId } = validatedScope(options);
  const validatedAlarmId = alarmUUIDV7(alarmId);
  const validatedOperation = alarmOperationSchema.exclude(['PUBLISH', 'ACKNOWLEDGE']).parse(operation);
  if (!options.csrfToken) {
    throw new AlarmApiError(401, 'CSRF_REQUIRED', '认证会话没有提供 CSRF 能力。');
  }
  const idempotencyKey = options.idempotencyKey ?? `real-alarm-${crypto.randomUUID()}`;
  const payload = await alarmRequest(
    `${localAlarmPrefix()}/${encodeURIComponent(siteId)}/alarms/${encodeURIComponent(validatedAlarmId)}:${validatedOperation.toLowerCase()}`,
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
  return validateAlarmScope(payload, { trustedTenantId: tenantId, trustedSiteId: siteId });
}

function alarmUUIDV7(value: string): string {
  try {
    return alarmUUIDv7Schema.parse(value);
  } catch {
    throw new AlarmApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Alarm。');
  }
}

export async function acknowledgeScopedAlarm(alarmId: string, input: AlarmAcknowledgeInput, options: ScopedAlarmRequestOptions): Promise<Alarm> {
  if (!ALARM_PUBLIC_ROUTES_ENABLED) {
    throw new AlarmApiError(503, 'ALARM_ROUTE_DISABLED', 'Alarm ACK 路由尚未启用。');
  }
  const { tenantId, siteId } = validatedScope(options);
  const validatedAlarmId = alarmUUIDV7(alarmId);
  const body = acknowledgeInputSchema.parse(input);
  const headers = new Headers({ 'Content-Type': 'application/json' });
  if (options.csrfToken) headers.set('X-CSRF-Token', options.csrfToken);
  if (options.idempotencyKey) headers.set('Idempotency-Key', options.idempotencyKey);
  const payload = await alarmRequest(
    alarmPaths.acknowledge.replace('{alarmId}', encodeURIComponent(validatedAlarmId)),
    publicAlarmEnvelopeSchema,
    { method: 'POST', headers, body: JSON.stringify(body) },
    options,
  );
  return validateAlarmScope(payload.data, { trustedTenantId: tenantId, trustedSiteId: siteId });
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
