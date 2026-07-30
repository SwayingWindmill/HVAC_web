import { z } from 'zod';

export const energyGranularitySchema = z.enum(['hour', 'day', 'month']);
export const energyQualityPolicySchema = z.enum(['VALID_ONLY', 'VALID_AND_SUSPECT']);

export const energySeriesQuerySchema = z.object({
  organizationId: z.string().uuid(),
  siteId: z.string().uuid(),
  energyType: z.literal('electricity'),
  granularity: energyGranularitySchema,
  timezone: z.string().min(3).max(128),
  from: z.string().datetime(),
  to: z.string().datetime(),
  qualityPolicy: energyQualityPolicySchema,
}).strict().superRefine((value, context) => {
  const from = Date.parse(value.from);
  const to = Date.parse(value.to);
  if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ['to'], message: 'to must be after from' });
  } else if (to - from > 366 * 24 * 60 * 60 * 1000) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ['to'], message: 'range must not exceed 366 days' });
  }
});

export const energySeriesPointSchema = z.object({
  periodStart: z.string().datetime(),
  periodEnd: z.string().datetime(),
  energyKWh: z.number().nonnegative(),
}).strict();

export const qualitySummarySchema = z.object({
  valid: z.number().int().nonnegative(),
  suspect: z.number().int().nonnegative(),
  invalid: z.number().int().nonnegative(),
}).strict();

export const energySeriesMetadataSchema = z.object({
  requestedGranularity: energyGranularitySchema,
  actualGranularity: energyGranularitySchema,
  dataWatermark: z.string().datetime().optional(),
  aggregateWatermark: z.string().datetime().optional(),
  datasetRevision: z.string().min(1),
  partial: z.boolean(),
  qualitySummary: qualitySummarySchema,
}).strict();

export const energySeriesResponseSchema = z.object({
  schemaVersion: z.literal(1),
  points: z.array(energySeriesPointSchema).max(10_000),
  metadata: energySeriesMetadataSchema,
}).strict();

export const energyProblemDetailsSchema = z.object({
  type: z.string(),
  title: z.string(),
  status: z.number().int().min(400).max(599),
  code: z.string(),
  detail: z.string(),
  retryable: z.boolean(),
  traceId: z.string().optional(),
  instance: z.string().optional(),
}).passthrough();

export type EnergyGranularity = z.infer<typeof energyGranularitySchema>;
export type EnergyQualityPolicy = z.infer<typeof energyQualityPolicySchema>;
export type EnergySeriesQuery = z.infer<typeof energySeriesQuerySchema>;
export type EnergySeriesPoint = z.infer<typeof energySeriesPointSchema>;
export type EnergySeriesResponse = z.infer<typeof energySeriesResponseSchema>;
export type EnergyProblemDetails = z.infer<typeof energyProblemDetailsSchema>;

export class EnergyAnalyticsRequestError extends Error {
  readonly problem: EnergyProblemDetails;

  constructor(problem: EnergyProblemDetails) {
    super(problem.detail);
    this.name = 'EnergyAnalyticsRequestError';
    this.problem = problem;
  }
}

export class EnergyAnalyticsInvalidResponseError extends Error {
  constructor() {
    super('Energy analytics returned an unsupported response.');
    this.name = 'EnergyAnalyticsInvalidResponseError';
  }
}

export interface EnergyAnalyticsRequestOptions {
  csrfToken: string;
  trustedOrganizationId: string;
  signal?: AbortSignal;
  fetchImplementation?: typeof fetch;
  baseUrl?: string;
}

export async function queryEnergySeries(
  input: EnergySeriesQuery,
  options: EnergyAnalyticsRequestOptions,
): Promise<EnergySeriesResponse> {
  const query = energySeriesQuerySchema.parse(input);
  if (query.organizationId !== options.trustedOrganizationId) {
    throw new Error('Energy query Organization does not match the authenticated Principal.');
  }
  if (!options.csrfToken) throw new Error('Authenticated Session omitted CSRF capability.');

  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}/api/v1/analytics/energy-series`, {
    method: 'POST',
    credentials: 'same-origin',
    signal: options.signal,
    headers: {
      Accept: 'application/json, application/problem+json',
      'Content-Type': 'application/json',
      'X-CSRF-Token': options.csrfToken,
    },
    body: JSON.stringify(query),
  });

  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new EnergyAnalyticsInvalidResponseError();
  }
  if (!response.ok) {
    const parsed = energyProblemDetailsSchema.safeParse(payload);
    if (!parsed.success) throw new EnergyAnalyticsInvalidResponseError();
    throw new EnergyAnalyticsRequestError(parsed.data);
  }
  const parsed = energySeriesResponseSchema.safeParse(payload);
  if (!parsed.success) throw new EnergyAnalyticsInvalidResponseError();
  return parsed.data;
}

export function energySeriesQueryKey(query: EnergySeriesQuery) {
  return [
    'energy-series',
    query.organizationId,
    query.siteId,
    query.energyType,
    query.from,
    query.to,
    query.granularity,
    query.timezone,
    query.qualityPolicy,
  ] as const;
}

export function energySeriesRevisionKey(query: EnergySeriesQuery, datasetRevision: string) {
  return [...energySeriesQueryKey(query), datasetRevision] as const;
}

export type EnergyTrendDatum = readonly [timestamp: number, energyKWh: number | null];

const granularityMilliseconds: Record<EnergyGranularity, number> = {
  hour: 60 * 60 * 1000,
  day: 24 * 60 * 60 * 1000,
  month: 28 * 24 * 60 * 60 * 1000,
};

export function buildEnergyTrendData(
  points: readonly EnergySeriesPoint[],
  _granularity: EnergyGranularity,
): EnergyTrendDatum[] {
  const sorted = [...points].sort((left, right) => Date.parse(left.periodStart) - Date.parse(right.periodStart));
  const result: EnergyTrendDatum[] = [];
  sorted.forEach((point, index) => {
    const start = Date.parse(point.periodStart);
    const previous = sorted[index - 1];
    if (previous) {
      const previousEnd = Date.parse(previous.periodEnd);
      if (start > previousEnd) result.push([previousEnd, null]);
    }
    result.push([start, point.energyKWh]);
  });
  return result;
}

export function energyTotal(points: readonly EnergySeriesPoint[]): number | null {
  if (points.length === 0) return null;
  return points.reduce((total, point) => total + point.energyKWh, 0);
}

export function hasStaleWatermark(response: EnergySeriesResponse, query: EnergySeriesQuery): boolean {
  const watermark = response.metadata.aggregateWatermark ?? response.metadata.dataWatermark;
  if (!watermark) return true;
  const tolerance = granularityMilliseconds[response.metadata.actualGranularity] * 2;
  return Date.parse(query.to) - Date.parse(watermark) > tolerance;
}

export interface EnergyFailureView {
  kind: 'aborted' | 'unauthorized' | 'forbidden' | 'invalid-query' | 'upstream' | 'invalid-response' | 'unexpected';
  title: string;
  detail: string;
  retryable: boolean;
  traceId?: string;
}

export function classifyEnergyAnalyticsFailure(error: unknown): EnergyFailureView {
  if (error instanceof DOMException && error.name === 'AbortError') {
    return { kind: 'aborted', title: '请求已取消', detail: '查询条件已变化，旧请求不会覆盖当前 Site 数据。', retryable: false };
  }
  if (error instanceof EnergyAnalyticsInvalidResponseError) {
    return { kind: 'invalid-response', title: '数据响应无效', detail: 'Gateway 返回了无法验证的能源数据，页面未采用该结果。', retryable: true };
  }
  if (error instanceof EnergyAnalyticsRequestError) {
    const { status, detail, retryable, traceId } = error.problem;
    if (status === 401) return { kind: 'unauthorized', title: '会话已失效', detail: '请重新登录后再读取能源数据。', retryable: false, traceId };
    if (status === 403) return { kind: 'forbidden', title: '无权读取能源数据', detail: '当前 Principal 无权访问此 Site 的能源分析。', retryable: false, traceId };
    if (status === 422) return { kind: 'invalid-query', title: '查询条件无效', detail, retryable: false, traceId };
    if (status === 502 || status === 503 || status === 504) {
      return { kind: 'upstream', title: '能源分析暂不可用', detail: '权威分析服务暂时无法完成查询，请稍后重试。', retryable, traceId };
    }
    return { kind: 'unexpected', title: '能源查询失败', detail, retryable, traceId };
  }
  return {
    kind: 'unexpected',
    title: '能源查询失败',
    detail: error instanceof Error ? error.message : '发生未知错误。',
    retryable: true,
  };
}
