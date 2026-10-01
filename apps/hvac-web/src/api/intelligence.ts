import { z } from 'zod';

const uuidSchema = z.string().uuid();
const finiteNumber = z.number().finite();
const jsonObject = z.record(z.string(), z.unknown());

const forecastSnapshotSchema = z.object({
  snapshotId: uuidSchema,
  forecastJobId: uuidSchema,
  deploymentId: uuidSchema,
  modelVersionId: uuidSchema,
  inputSnapshotId: uuidSchema,
  subjectType: z.string(),
  subjectId: uuidSchema,
  target: z.enum(['SITE_LOAD', 'PV_GENERATION']),
  forecastOrigin: z.string().datetime(),
  windowStart: z.string().datetime(),
  windowEnd: z.string().datetime(),
  resultCount: z.number().int().positive(),
  quality: z.enum(['VALID', 'DEGRADED', 'FALLBACK']),
});

const forecastPointSchema = z.object({
  forecast_id: uuidSchema,
  tenant_id: uuidSchema,
  site_id: uuidSchema,
  target: z.enum(['SITE_LOAD', 'PV_GENERATION']),
  forecast_job_id: uuidSchema,
  forecast_snapshot_id: uuidSchema,
  deployment_id: uuidSchema,
  model_id: uuidSchema,
  model_version_id: uuidSchema,
  model_version: z.number().int().positive(),
  feature_set_version_id: uuidSchema,
  feature_set_version: z.number().int().positive(),
  input_snapshot_id: uuidSchema,
  topology_version_id: uuidSchema,
  forecast_origin: z.string().datetime(),
  forecast_for: z.string().datetime(),
  horizon_minutes: z.number().int().positive(),
  value: finiteNumber,
  unit: z.string().min(1),
  lower_bound: finiteNumber.nullable(),
  upper_bound: finiteNumber.nullable(),
  quantile: finiteNumber.nullable(),
  quality: z.enum(['VALID', 'DEGRADED', 'FALLBACK']),
  generated_at: z.string().datetime(),
});

export const publishedForecastSchema = z.object({
  snapshot: forecastSnapshotSchema,
  points: z.array(forecastPointSchema).min(1),
});
export type PublishedForecast = z.infer<typeof publishedForecastSchema>;

export const fddFindingSchema = z.object({
  id: uuidSchema,
  tenantId: uuidSchema,
  siteId: uuidSchema,
  assetId: uuidSchema,
  findingType: z.string().min(1),
  evaluationFrom: z.string().datetime(),
  evaluationTo: z.string().datetime(),
  evidenceIds: z.array(z.string().min(1)).min(1),
  modelDeploymentRevisionId: uuidSchema.optional().or(z.literal('')),
  ruleRevisionId: z.string().optional(),
  confidence: z.number().min(0).max(1),
  qualityBlocker: z.string().optional(),
  alarmId: uuidSchema.optional().or(z.literal('')),
  workOrderId: uuidSchema.optional().or(z.literal('')),
  createdAt: z.string().datetime(),
});
export type FDDFinding = z.infer<typeof fddFindingSchema>;
export type FDDFindingFilter = { alarmId?: string; workOrderId?: string; limit?: number };
export type FDDLinkInput = { alarmId: string; workOrderId: string };
export type FDDMutationOptions = { csrfToken: string; signal?: AbortSignal };

const revalidationSchema = z.object({
  snapshotId: z.string().min(1),
  accepted: z.boolean(),
  reasonCode: z.string(),
  validatedAt: z.string().datetime(),
  expiresAt: z.string().datetime(),
});

const recommendationSchema = z.object({
  id: uuidSchema,
  tenantId: uuidSchema,
  siteId: uuidSchema,
  inputSnapshotId: uuidSchema,
  deploymentRevisionId: uuidSchema,
  baseline: jsonObject,
  objective: jsonObject,
  constraints: z.array(jsonObject).min(1),
  candidate: jsonObject,
  expectedImpact: jsonObject,
  uncertainty: jsonObject,
  risk: jsonObject,
  rollbackPlan: jsonObject,
  verificationPlan: jsonObject,
  approval: z.enum(['DRAFT', 'APPROVED', 'REJECTED']),
  currentStateRevalidation: revalidationSchema.optional(),
  commandIntentId: uuidSchema.optional().or(z.literal('')),
  createdAt: z.string().datetime(),
});

export const publishedRecommendationSchema = z.object({
  runId: uuidSchema,
  runStatus: z.literal('PUBLISHED'),
  quality: z.string().min(1),
  recommendation: recommendationSchema,
});
export type PublishedRecommendation = z.infer<typeof publishedRecommendationSchema>;

const fddListSchema = z.object({ items: z.array(fddFindingSchema) });

class IntelligenceApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message);
  }
}

async function getJSON<T>(path: string, schema: z.ZodType<T>, signal?: AbortSignal): Promise<T | null> {
  const response = await fetch(path, {
    method: 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json, application/problem+json' },
    signal,
  });
  if (response.status === 404) return null;
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = z.object({ code: z.string().optional(), detail: z.string().optional(), title: z.string().optional() }).passthrough().safeParse(payload);
    throw new IntelligenceApiError(response.status, problem.success ? problem.data.code ?? 'INTELLIGENCE_UNAVAILABLE' : 'INTELLIGENCE_UNAVAILABLE', problem.success ? problem.data.detail ?? problem.data.title ?? 'Intelligence 服务暂时不可用。' : 'Intelligence 服务暂时不可用。');
  }
  return schema.parse(payload);
}

export function getSiteLoadForecast(siteId: string, signal?: AbortSignal) {
  return getJSON(`/api/v1/sites/${encodeURIComponent(siteId)}/forecast/load`, publishedForecastSchema, signal);
}

export function getSitePVForecast(siteId: string, signal?: AbortSignal) {
  return getJSON(`/api/v1/sites/${encodeURIComponent(siteId)}/forecast/pv`, publishedForecastSchema, signal);
}

export async function listSiteFDDFindings(siteId: string, signal?: AbortSignal, filter: FDDFindingFilter = {}): Promise<FDDFinding[]> {
  const query = new URLSearchParams();
  query.set('limit', String(filter.limit ?? 100));
  if (filter.alarmId) query.set('alarmId', filter.alarmId);
  if (filter.workOrderId) query.set('workOrderId', filter.workOrderId);
  const result = await getJSON(`/api/v1/sites/${encodeURIComponent(siteId)}/fdd/findings?${query.toString()}`, fddListSchema, signal);
  return result?.items ?? [];
}

export async function linkFDDFinding(siteId: string, findingId: string, input: FDDLinkInput, options: FDDMutationOptions): Promise<FDDFinding> {
  const response = await fetch(`/api/v1/sites/${encodeURIComponent(siteId)}/fdd/findings/${encodeURIComponent(findingId)}/links`, {
    method: 'PATCH',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json, application/problem+json',
      'Content-Type': 'application/json',
      'X-CSRF-Token': options.csrfToken,
    },
    body: JSON.stringify(input),
    signal: options.signal,
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = z.object({ code: z.string().optional(), detail: z.string().optional(), title: z.string().optional() }).passthrough().safeParse(payload);
    throw new IntelligenceApiError(response.status, problem.success ? problem.data.code ?? 'FDD_LINK_UNAVAILABLE' : 'FDD_LINK_UNAVAILABLE', problem.success ? problem.data.detail ?? problem.data.title ?? 'FDD 关联暂时不可用。' : 'FDD 关联暂时不可用。');
  }
  return fddFindingSchema.parse(payload);
}

export function getLatestOptimizationRecommendation(siteId: string, signal?: AbortSignal) {
  return getJSON(`/api/v1/sites/${encodeURIComponent(siteId)}/optimization/recommendations/latest`, publishedRecommendationSchema, signal);
}
