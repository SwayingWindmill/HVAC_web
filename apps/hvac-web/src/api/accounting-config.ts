import { z } from 'zod';
import {
  failureMessage,
  gatewayPath,
  gatewayRequest,
  GatewayRequestError,
  valueOf,
  type GatewayProblem,
} from './gateway-client.ts';

export const siteCalendarDayTypeSchema = z.enum(['HOLIDAY', 'SPECIAL_DAY', 'WEEKDAY', 'WEEKEND']);

export const siteCalendarHolidaySchema = z.object({
  id: z.string().min(1),
  tenantId: z.string().min(1),
  siteId: z.string().min(1),
  holidayDate: z.string().min(1),
  dayType: siteCalendarDayTypeSchema,
  label: z.string().nullish().transform((value) => value ?? ''),
  revision: z.number().int().nonnegative(),
  createdAt: z.string().min(1),
  updatedAt: z.string().min(1),
});

export const siteCalendarHolidayMutationSchema = siteCalendarHolidaySchema.extend({
  recalcJobId: z.string().nullish().transform((value) => value || undefined),
});

export const siteCalendarHolidayListSchema = z.object({
  items: z.array(siteCalendarHolidaySchema),
});

export const siteCalendarHolidayDeleteSchema = z.object({
  holidayDate: z.string().min(1),
  deleted: z.boolean(),
  recalcJobId: z.string().nullish().transform((value) => value || undefined),
});

export const ratioApplicationModeSchema = z.enum([
  'DEVICE_APPLIED',
  'EDGE_APPLIED',
  'PLATFORM_APPLIED',
]);

export const ratioReadingUnitSchema = z.enum(['Wh', 'kWh', 'MWh', 'GJ', 'MJ', 'RTh']);

export const meterRatioVersionStatusSchema = z.enum(['DRAFT', 'RELEASED', 'SUPERSEDED']);

export const meterRatioVersionSchema = z.object({
  id: z.string().min(1),
  tenantId: z.string().min(1),
  siteId: z.string().min(1),
  meterId: z.string().nullable().optional().transform((value) => value ?? null),
  meterBindingId: z.string().nullable().optional().transform((value) => value ?? null),
  pointId: z.string().nullable().optional().transform((value) => value ?? null),
  version: z.number().int().nullable().optional().transform((value) => value ?? null),
  ctRatio: z.number().positive(),
  ptRatio: z.number().positive(),
  meterMultiplier: z.number().positive(),
  cloudMultiplier: z.number().nonnegative(),
  ratioApplicationMode: ratioApplicationModeSchema,
  readingUnit: ratioReadingUnitSchema,
  effectiveFrom: z.string().min(1),
  effectiveTo: z.string().nullable().optional().transform((value) => value ?? null),
  status: meterRatioVersionStatusSchema,
  revision: z.number().int().nonnegative(),
  createdAt: z.string().min(1),
  updatedAt: z.string().min(1),
  recalcJobId: z.string().nullish().transform((value) => value || undefined),
});

export const meterRatioVersionListSchema = z.object({
  items: z.array(meterRatioVersionSchema),
});

export const counterAuditSummarySchema = z.object({
  totalSamples: z.number().int().nonnegative(),
  anomalyCount: z.number().int().nonnegative(),
  zeroDropCount: z.number().int().nonnegative(),
  spikeCount: z.number().int().nonnegative(),
  resetCount: z.number().int().nonnegative(),
  rolloverCount: z.number().int().nonnegative(),
  invalidDecreaseCount: z.number().int().nonnegative(),
});

export const counterAuditItemSchema = z.object({
  pointId: z.string().min(1),
  deviceId: z.string().nullish().transform((value) => value ?? ''),
  telemetryKey: z.string().nullish().transform((value) => value ?? ''),
  periodStart: z.string().nullish().transform((value) => value ?? ''),
  periodEnd: z.string().min(1),
  rawPrevious: z.number().nullable().optional().transform((value) => value ?? null),
  rawCurrent: z.number(),
  cleanedDeltaKWh: z.number().nullable().optional().transform((value) => value ?? null),
  transitionType: z.string().min(1),
  quality: z.string().min(1),
  qualityReasons: z.array(z.string()).nullish().transform((value) => value ?? []),
  unit: z.string().nullish().transform((value) => value || 'kWh'),
});

export const counterAnomalyAuditSchema = z.object({
  siteId: z.string().min(1),
  from: z.string().min(1),
  to: z.string().min(1),
  summary: counterAuditSummarySchema,
  items: z.array(counterAuditItemSchema),
  truncated: z.boolean().nullish().transform((value) => Boolean(value)),
});

export type SiteCalendarDayType = z.infer<typeof siteCalendarDayTypeSchema>;
export type SiteCalendarHoliday = z.infer<typeof siteCalendarHolidaySchema>;
export type SiteCalendarHolidayMutationResult = z.infer<typeof siteCalendarHolidayMutationSchema>;
export type SiteCalendarHolidayDeleteResult = z.infer<typeof siteCalendarHolidayDeleteSchema>;

export type RatioApplicationMode = z.infer<typeof ratioApplicationModeSchema>;
export type RatioReadingUnit = z.infer<typeof ratioReadingUnitSchema>;
export type MeterRatioVersionStatus = z.infer<typeof meterRatioVersionStatusSchema>;
export type MeterRatioVersion = z.infer<typeof meterRatioVersionSchema>;

export type CounterAuditSummary = z.infer<typeof counterAuditSummarySchema>;
export type CounterAuditItem = z.infer<typeof counterAuditItemSchema>;
export type CounterAnomalyAudit = z.infer<typeof counterAnomalyAuditSchema>;

export interface UpsertSiteHolidayInput {
  readonly date: string;
  readonly dayType: SiteCalendarDayType;
  readonly label: string;
  readonly idempotencyKey?: string;
}

export interface CreateMeterRatioVersionInput {
  readonly pointId: string;
  readonly meterBindingId?: string;
  readonly ctRatio: number;
  readonly ptRatio: number;
  readonly meterMultiplier?: number;
  readonly ratioApplicationMode: RatioApplicationMode;
  readonly readingUnit: RatioReadingUnit;
  readonly effectiveFrom: string;
  readonly effectiveTo?: string;
  readonly reason?: string;
  readonly idempotencyKey?: string;
}

export interface RetireMeterRatioVersionInput {
  readonly effectiveTo: string;
  readonly reason?: string;
  readonly idempotencyKey?: string;
}

export interface RequestOptions {
  readonly signal?: AbortSignal;
  readonly fetch?: typeof fetch;
}

const CONFIG_UNAVAILABLE_MESSAGE = '暂时无法连接计费日历与电表倍率配置服务，请稍后重试';
const AUDIT_UNAVAILABLE_MESSAGE = '暂时无法读取表计异常清洗记录，请稍后重试';
const RESOURCE_NOT_FOUND_MESSAGE = '未找到对应的站点或倍率版本记录，请刷新页面后重试';

function describeAccountingConfigRefusal(problem: GatewayProblem, fallback: string): string {
  const { status, code } = problem;
  if (code === 'ANALYTICS_ACCESS_DENIED') {
    return '当前账号没有查看该站点表计异常清洗记录的权限，请联系站点管理员';
  }
  if (
    status === 403 ||
    code === 'BILLING_MANAGE_FORBIDDEN' ||
    code === 'REGISTRY_WRITE_FORBIDDEN' ||
    code === 'FORBIDDEN'
  ) {
    return '当前账号没有维护站点计费日历与电表倍率配置的权限，请联系站点管理员';
  }
  if (status === 404 || code === 'RESOURCE_NOT_FOUND') {
    return RESOURCE_NOT_FOUND_MESSAGE;
  }
  if (status === 409) {
    if (code === 'METER_RATIO_OVERLAP') {
      return '该测点在所选生效时间段内已存在重叠的生效倍率版本，请先终止旧版本或调整生效起止时间';
    }
    if (code === 'REGISTRY_REVISION_CONFLICT') {
      return '该配置记录已被其他成员更新，请刷新页面核对最新内容后再提交';
    }
    if (code === 'REGISTRY_IDEMPOTENCY_CONFLICT') {
      return '请勿重复提交冲突的配置变更，请刷新页面后重试';
    }
    return '当前倍率版本或日历配置存在时间段或状态冲突，请刷新核对后再试';
  }
  if (
    status === 400 ||
    code === 'VALIDATION_ERROR' ||
    code === 'INVALID_ARGUMENT' ||
    code === 'REGISTRY_MUTATION_INVALID'
  ) {
    return '填写的日期、生效时间范围、变比数值或计量单位不符合校验要求，请核对后重试';
  }
  return fallback;
}

export function describeAccountingConfigError(error: unknown, fallback: string): string {
  if (error instanceof GatewayRequestError) {
    return failureMessage(error.failure, {
      unavailable: fallback,
      notPublished: RESOURCE_NOT_FOUND_MESSAGE,
      refused: (problem) => describeAccountingConfigRefusal(problem, fallback),
    });
  }
  return fallback;
}

export async function listSiteAccountingHolidays(
  siteId: string,
  options: RequestOptions & { readonly year?: number } = {},
): Promise<SiteCalendarHoliday[]> {
  const query = options.year !== undefined ? `?year=${encodeURIComponent(String(options.year))}` : '';
  const path = `${gatewayPath('/api/v1/sites/{siteId}/accounting/holidays', { siteId })}${query}`;
  const result = await gatewayRequest(path, {
    signal: options.signal,
    fetch: options.fetch,
    parse: (payload) => siteCalendarHolidayListSchema.parse(payload).items,
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function upsertSiteAccountingHoliday(
  siteId: string,
  csrfToken: string,
  input: UpsertSiteHolidayInput,
  options: RequestOptions = {},
): Promise<SiteCalendarHolidayMutationResult> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/holidays/{date}', {
    siteId,
    date: input.date,
  });
  const result = await gatewayRequest(path, {
    method: 'PUT',
    csrfToken,
    idempotencyKey: input.idempotencyKey ?? crypto.randomUUID(),
    signal: options.signal,
    fetch: options.fetch,
    body: {
      dayType: input.dayType,
      label: input.label,
    },
    parse: (payload) => siteCalendarHolidayMutationSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function deleteSiteAccountingHoliday(
  siteId: string,
  csrfToken: string,
  date: string,
  options: RequestOptions & { readonly idempotencyKey?: string } = {},
): Promise<SiteCalendarHolidayDeleteResult> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/holidays/{date}', {
    siteId,
    date,
  });
  const result = await gatewayRequest(path, {
    method: 'DELETE',
    csrfToken,
    idempotencyKey: options.idempotencyKey ?? crypto.randomUUID(),
    signal: options.signal,
    fetch: options.fetch,
    parse: (payload) => siteCalendarHolidayDeleteSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function listSiteMeterRatioVersions(
  siteId: string,
  options: RequestOptions & {
    readonly pointId?: string;
    readonly includeSuperseded?: boolean;
  } = {},
): Promise<MeterRatioVersion[]> {
  const params = new URLSearchParams();
  if (options.pointId) params.set('pointId', options.pointId);
  if (options.includeSuperseded !== undefined) {
    params.set('includeSuperseded', options.includeSuperseded ? 'true' : 'false');
  }
  const queryString = params.toString();
  const basePath = gatewayPath('/api/v1/sites/{siteId}/accounting/meter-ratios', { siteId });
  const path = queryString ? `${basePath}?${queryString}` : basePath;
  const result = await gatewayRequest(path, {
    signal: options.signal,
    fetch: options.fetch,
    parse: (payload) => meterRatioVersionListSchema.parse(payload).items,
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function createSiteMeterRatioVersion(
  siteId: string,
  csrfToken: string,
  input: CreateMeterRatioVersionInput,
  options: RequestOptions = {},
): Promise<MeterRatioVersion> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/meter-ratios', { siteId });
  const result = await gatewayRequest(path, {
    method: 'POST',
    csrfToken,
    idempotencyKey: input.idempotencyKey ?? crypto.randomUUID(),
    signal: options.signal,
    fetch: options.fetch,
    body: {
      pointId: input.pointId,
      ...(input.meterBindingId ? { meterBindingId: input.meterBindingId } : {}),
      ctRatio: input.ctRatio,
      ptRatio: input.ptRatio,
      ...(input.meterMultiplier !== undefined ? { meterMultiplier: input.meterMultiplier } : {}),
      ratioApplicationMode: input.ratioApplicationMode,
      readingUnit: input.readingUnit,
      effectiveFrom: input.effectiveFrom,
      ...(input.effectiveTo ? { effectiveTo: input.effectiveTo } : {}),
      ...(input.reason ? { reason: input.reason } : {}),
    },
    parse: (payload) => meterRatioVersionSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function retireSiteMeterRatioVersion(
  siteId: string,
  csrfToken: string,
  versionId: string,
  input: RetireMeterRatioVersionInput,
  options: RequestOptions = {},
): Promise<MeterRatioVersion> {
  const path = gatewayPath('/api/v1/sites/{siteId}/accounting/meter-ratios/{versionId}/retire', {
    siteId,
    versionId,
  });
  const result = await gatewayRequest(path, {
    method: 'POST',
    csrfToken,
    idempotencyKey: input.idempotencyKey ?? crypto.randomUUID(),
    signal: options.signal,
    fetch: options.fetch,
    body: {
      effectiveTo: input.effectiveTo,
      ...(input.reason ? { reason: input.reason } : {}),
    },
    parse: (payload) => meterRatioVersionSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: CONFIG_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, CONFIG_UNAVAILABLE_MESSAGE),
  });
}

export async function fetchSiteCounterAudit(
  siteId: string,
  options: RequestOptions & {
    readonly from: string;
    readonly to: string;
    readonly anomaliesOnly?: boolean;
    readonly limit?: number;
  },
): Promise<CounterAnomalyAudit> {
  const params = new URLSearchParams({
    from: options.from,
    to: options.to,
  });
  if (options.anomaliesOnly !== undefined) {
    params.set('anomaliesOnly', options.anomaliesOnly ? 'true' : 'false');
  }
  if (options.limit !== undefined) {
    params.set('limit', String(options.limit));
  }
  const path = `${gatewayPath('/api/v1/sites/{siteId}/accounting/counter-audit', { siteId })}?${params.toString()}`;
  const result = await gatewayRequest(path, {
    signal: options.signal,
    fetch: options.fetch,
    parse: (payload) => counterAnomalyAuditSchema.parse(payload),
  });
  return valueOf(result, {
    unavailable: AUDIT_UNAVAILABLE_MESSAGE,
    notPublished: RESOURCE_NOT_FOUND_MESSAGE,
    refused: (problem) => describeAccountingConfigRefusal(problem, AUDIT_UNAVAILABLE_MESSAGE),
  });
}
