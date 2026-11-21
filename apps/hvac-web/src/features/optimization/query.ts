import { queryOptions } from '@tanstack/react-query';
import { z } from 'zod';
import { gatewayPath, gatewayRequest, publishedValueOf, valueOf, type GatewayProblem } from '@/api/gateway-client';
import {
  intelligencePaths,
  optimizationCanaryBindingDeleteResultSchema,
  optimizationCanaryBindingListSchema,
  optimizationCanaryBindingRecordSchema,
  optimizationCopCellSchema,
  optimizationNegativeSamplePageSchema,
  optimizationPreparationSchema,
  stagingEvaluationSchema,
  type OptimizationCanaryBindingDeleteResult,
  type OptimizationCanaryBindingList,
  type OptimizationCanaryBindingRecord,
  type OptimizationNegativeSampleFilterKind,
  type OptimizationNegativeSampleItem,
  type OptimizationNegativeSamplePage,
  type StagingEvaluation,
  type UpsertOptimizationCanaryBindingRequest,
} from '@/api/generated/platformGateway.gen';
import { listWorkOrders, type WorkOrderList } from '@/api/work-orders';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import {
  optimizationRecommendationSourceRef,
  parseOptimizationGovernanceSummary,
  parseRecommendation,
  type OptimizationGovernanceSummary,
  type OptimizationRecommendation,
  type PreparationOutcome,
  type PublishedOptimizationGovernanceSummary,
  type PublishedRecommendation,
  type PublishedShadowEvaluation,
} from './optimization-model';

const opt = <S extends z.ZodTypeAny>(schema: S) => schema.nullish().transform((value) => value ?? undefined);

const riskLevelSchema = z.enum(['LOW', 'MEDIUM', 'HIGH']);

const optimizationAuxiliaryTargetSchema = z.object({
  loopKey: z.string().min(1),
  capability: z.string().min(1),
  targetValue: z.number().finite(),
  unit: z.string(),
  rationale: opt(z.string()),
});

const publishedRecommendationSchema: z.ZodType<PublishedRecommendation> = z.object({
  runId: z.string(),
  trigger: z.enum(['MANUAL', 'SCHEDULE']),
  responseModel: z.object({
    dailyEnergyDeltaKWhPerSupplyTempC: z.number(),
    zoneTempDeltaCPerSupplyTempC: z.number(),
  }),
  recommendation: z.object({
    id: opt(z.string()),
    createdAt: z.string(),
    baseline: z.object({
      supplyTempC: z.number(),
      zoneTempC: z.number(),
      dailyEnergyKWh: z.number(),
      dailyCost: z.number(),
    }),
    candidate: z.object({
      supplyTempC: z.number(),
      projectedZoneTempC: z.number(),
      dailyEnergyKWh: z.number(),
      dailyCost: z.number(),
    }),
    auxiliaryTargets: opt(z.array(optimizationAuxiliaryTargetSchema)),
    expectedImpact: z.object({
      energySavingKWhPerDay: z.number(),
      costSavingPerDay: z.number(),
    }),
    constraints: z.array(z.object({
      kind: z.string(),
      minC: opt(z.number()),
      maxC: opt(z.number()),
      maxStepC: opt(z.number()),
    })),
    uncertainty: z.object({
      energyP90KWh: z.number(),
      zoneTempP90C: z.number(),
    }),
    risk: z.object({
      level: riskLevelSchema,
      comfortMarginC: z.number(),
    }),
    rollbackPlan: opt(z.object({
      restoreSupplyTempC: opt(z.number()),
    })),
    verificationPlan: z.object({
      windowMinutes: z.number(),
    }),
  }),
});

const publishedShadowEvaluationSchema: z.ZodType<PublishedShadowEvaluation> = z.object({
  evaluationId: z.string(),
  runId: z.string(),
  publishedRecommendationId: z.string(),
  generatedAt: z.string(),
  solver: z.string(),
  mode: z.string(),
  outcome: z.enum(['FEASIBLE', 'INFEASIBLE', 'NO_USABLE_MODEL', 'HISTORY_UNAVAILABLE', 'INVALID_REQUEST']),
  reason: opt(z.string()),
  cvRmse: z.number().nullable(),
  condenserReset: opt(z.object({
    status: opt(z.string()),
    reason: opt(z.string()),
  })),
  energyResponse: opt(z.object({
    source: opt(z.string()),
    dailyEnergyDeltaKWhPerSupplyTempC: z.number(),
    referenceChillerEnergyKWh: z.number(),
    referenceSamples: z.number(),
    referenceHours: z.number(),
    replacesPolicyValue: z.number(),
  })),
  zoneResponse: opt(z.object({
    source: opt(z.string()),
    zoneTempDeltaCPerSupplyTempC: z.number(),
    zoneTempUncertaintyP90C: z.number(),
  })),
  fit: opt(z.object({
    history: z.object({
      from: z.string(),
      to: z.string(),
      bucketMinutes: z.number(),
      chillers: z.number(),
      runningBuckets: z.number(),
      setAside: z.record(z.string(), z.number()),
      loadAllocation: opt(z.record(z.string(), z.number())),
    }),
    chillers: z.array(z.object({
      deviceId: z.string(),
      status: z.enum(['FITTED', 'INSUFFICIENT_DATA', 'REJECTED']),
      reason: opt(z.string()),
      samples: z.number(),
      capacityReference: opt(z.object({ kW: z.number(), source: z.string() })),
      coefficients: opt(z.object({
        intercept: z.number(),
        loadRatio: z.number(),
        loadRatioSquared: z.number(),
        chilledWaterSupplyTempC: z.number(),
        condenserReturnTempC: opt(z.number()),
      })),
      cvRmsePower: opt(z.number()),
      cvRmseCOP: opt(z.number()),
      meanCOP: opt(z.number()),
      chilledWaterSupplyRangeC: opt(z.array(z.number())),
    })),
    cvRmsePower: z.number().nullable(),
  })),
  candidates: opt(z.array(z.object({
    supplyTempC: z.number(),
    projectedZoneTempC: z.number(),
    chillerEnergyDeltaKWh: z.number(),
    dailyEnergyKWh: z.number(),
    dailyCost: z.number(),
    extrapolated: z.boolean(),
    feasible: z.boolean(),
    reason: opt(z.string()),
  }))),
  summary: opt(z.object({
    supplyTempC: z.number(),
    projectedZoneTempC: z.number(),
    dailyEnergyKWh: z.number(),
    dailyCost: z.number(),
    energySavingKWhPerDay: z.number(),
    costSavingPerDay: z.number(),
    energyP90KWh: z.number(),
    zoneTempP90C: z.number(),
    comfortMarginC: z.number(),
    riskLevel: riskLevelSchema,
    extrapolated: z.boolean(),
    savingWithinEnergyUncertainty: z.boolean(),
  })),
});

const optimizationRunResponseSchema = z.object({ optimizationRunId: z.string() });

const publishedGovernanceSummarySchema: z.ZodType<PublishedOptimizationGovernanceSummary, unknown> = z.object({
  tenantId: z.string(),
  siteId: z.string(),
  windowDays: z.number(),
  generatedAt: z.string(),
  activeSolver: z.object({
    servingRef: z.string(),
    source: z.enum(['DEFAULT', 'DEPLOYMENT_BINDING', 'SITE_LOOP_CANARY']),
    loopKey: z.string(),
    deploymentRevisionId: z.string().nullish().transform((value) => value ?? null),
  }),
  tickCounts: z.object({
    aiClosedLoop: z.number(),
    aiShadow: z.number(),
    ruleBaseline: z.number(),
    manualOverride: z.number(),
  }),
  copComparison: z.object({
    matchedBinCount: z.number(),
    matchedAiSamples: z.number(),
    matchedBaselineSamples: z.number(),
    weightedBaselineCOP: z.number().nullish().transform((value) => value ?? null),
    weightedAiCOP: z.number().nullish().transform((value) => value ?? null),
    weightedCopUpliftPct: z.number().nullish().transform((value) => value ?? null),
    totalEstimatedSavedKWh: z.number(),
    cells: z.array(optimizationCopCellSchema).nullish().transform((value) => value ?? []),
  }),
  flywheel: z.object({
    totalSamples: z.number(),
    guardrailClamped: z.number(),
    guardrailVetoed: z.number(),
    manualOverride: z.number(),
    monitorTripped: z.number(),
    trackingDeviation: z.number(),
  }),
});

export const optimizationKeys = {
  latest: (siteId: string) => ['optimization', siteId, 'latest'] as const,
  preparation: (siteId: string) => ['optimization', siteId, 'preparation'] as const,
  shadow: (siteId: string, runId: string | null) => ['optimization', siteId, 'shadow', runId] as const,
  governanceSummary: (siteId: string) => ['optimization', siteId, 'governance-summary'] as const,
  canaryBindings: (siteId: string) => ['optimization', siteId, 'canary-bindings'] as const,
  negativeSamples: (
    siteId: string,
    params: { windowDays?: number; sampleKind?: OptimizationNegativeSampleFilterKind; limit?: number; offset?: number },
  ) => ['optimization', siteId, 'negative-samples', params] as const,
  staging: (siteId: string) => ['optimization', siteId, 'staging'] as const,
};

const UNAVAILABLE = '优化服务暂时不可用';

/** How the Site's hourly schedule last fired and why supply optimization is ready or blocked. */
export function latestPreparationQuery(siteId: string) {
  return queryOptions({
    queryKey: optimizationKeys.preparation(siteId),
    queryFn: async ({ signal }): Promise<PreparationOutcome | null> => publishedValueOf(
      await gatewayRequest(gatewayPath(intelligencePaths.latestOptimizationPreparation, { siteId }), {
        signal,
        parse: (payload) => optimizationPreparationSchema.parse(payload),
      }),
      { unavailable: UNAVAILABLE },
    ),
    retry: false,
  });
}

/** The Site's latest published recommendation; null until the Optimization owner has published one. */
export function latestRecommendationQuery(siteId: string, waitingForRun: string | null) {
  return queryOptions({
    queryKey: optimizationKeys.latest(siteId),
    queryFn: async ({ signal }): Promise<OptimizationRecommendation | null> => publishedValueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.latestOptimizationRecommendation, { siteId }),
        { signal, parse: (payload) => parseRecommendation(publishedRecommendationSchema.parse(payload)) },
      ),
      { unavailable: UNAVAILABLE },
    ),
    // A requested run publishes within seconds; until it does the page asks again.
    refetchInterval: waitingForRun ? 3_000 : false,
    retry: false,
  });
}

/** Work orders originating from a specific optimization recommendation, used for manual dispatch deduplication. */
export function recommendationWorkOrdersQuery(siteId: string, recommendationId: string | null, enabled: boolean) {
  const sourceRef = recommendationId ? optimizationRecommendationSourceRef(recommendationId) : '';
  return queryOptions({
    queryKey: workOrderKeys.source(siteId, 'EXTERNAL', sourceRef),
    queryFn: ({ signal }): Promise<WorkOrderList> =>
      listWorkOrders({ sourceDomain: 'EXTERNAL', sourceRef, limit: 20 }, { siteId, signal }),
    enabled: enabled && Boolean(recommendationId),
  });
}

/** The Site's latest read-only shadow evaluation from the Chiller COP Model; fetched lazily on the analysis tab. */
export function latestShadowEvaluationQuery(siteId: string, runId: string | null, enabled: boolean) {
  return queryOptions({
    queryKey: optimizationKeys.shadow(siteId, runId),
    queryFn: async ({ signal }): Promise<PublishedShadowEvaluation | null> => publishedValueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.latestOptimizationShadowEvaluation, { siteId }),
        { signal, parse: (payload) => publishedShadowEvaluationSchema.parse(payload) },
      ),
      { unavailable: UNAVAILABLE },
    ),
    enabled,
    retry: false,
  });
}

/** 14-day solver routing, weather/load-normalized COP M&V comparison, and guardrail negative-sample flywheel summary. */
export function siteOptimizationGovernanceSummaryQuery(siteId: string, enabled: boolean) {
  return queryOptions({
    queryKey: optimizationKeys.governanceSummary(siteId),
    queryFn: async ({ signal }): Promise<OptimizationGovernanceSummary | null> => publishedValueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.siteOptimizationGovernanceSummary, { siteId }),
        {
          signal,
          parse: (payload) => parseOptimizationGovernanceSummary(publishedGovernanceSummarySchema.parse(payload)),
        },
      ),
      { unavailable: UNAVAILABLE },
    ),
    enabled: Boolean(siteId) && enabled,
    retry: false,
  });
}

function canaryRefusal(problem: GatewayProblem): string {
  switch (problem.code) {
    case 'OPTIMIZATION_SOLVER_HOST_NOT_ALLOWED':
      return '外部优化服务地址不在允许名单内，请联系平台管理员在服务配置中添加白名单主机';
    case 'CONTROL_EXIT_FORBIDDEN':
      return '你没有管理优化策略灰度路由的权限';
    case 'OPTIMIZATION_CANARY_REQUEST_INVALID':
      return '回路标识或求解服务地址格式无效，请检查后重试';
    case 'RESOURCE_NOT_FOUND':
      return '当前站点尚未配置该回路的灰度路由绑定';
    default:
      return UNAVAILABLE;
  }
}

export function siteOptimizationCanaryBindingsQuery(siteId: string, enabled: boolean) {
  return queryOptions({
    queryKey: optimizationKeys.canaryBindings(siteId),
    queryFn: async ({ signal }): Promise<OptimizationCanaryBindingList> => valueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.siteOptimizationCanaryBindings, { siteId }),
        {
          signal,
          parse: (payload) => optimizationCanaryBindingListSchema.parse(payload),
        },
      ),
      { unavailable: UNAVAILABLE, refused: canaryRefusal },
    ),
    enabled: Boolean(siteId) && enabled,
    retry: false,
  });
}

export async function upsertSiteOptimizationCanaryBinding(
  siteId: string,
  loopKey: string,
  body: UpsertOptimizationCanaryBindingRequest,
  csrfToken: string,
): Promise<OptimizationCanaryBindingRecord> {
  const result = await gatewayRequest(
    gatewayPath(intelligencePaths.siteOptimizationCanaryBinding, { siteId, loopKey }),
    {
      method: 'PUT',
      csrfToken,
      body,
      parse: (payload) => optimizationCanaryBindingRecordSchema.parse(payload),
    },
  );
  return valueOf(result, { unavailable: UNAVAILABLE, refused: canaryRefusal });
}

export async function deleteSiteOptimizationCanaryBinding(
  siteId: string,
  loopKey: string,
  csrfToken: string,
): Promise<OptimizationCanaryBindingDeleteResult> {
  const result = await gatewayRequest(
    gatewayPath(intelligencePaths.siteOptimizationCanaryBinding, { siteId, loopKey }),
    {
      method: 'DELETE',
      csrfToken,
      parse: (payload) => optimizationCanaryBindingDeleteResultSchema.parse(payload),
    },
  );
  return valueOf(result, { unavailable: UNAVAILABLE, refused: canaryRefusal });
}

export interface NegativeSamplesQueryParams {
  readonly windowDays?: number;
  readonly sampleKind?: OptimizationNegativeSampleFilterKind;
  readonly limit?: number;
  readonly offset?: number;
}

function buildNegativeSamplesPath(siteId: string, params: NegativeSamplesQueryParams): string {
  const base = gatewayPath(intelligencePaths.siteOptimizationNegativeSamples, { siteId });
  const search = new URLSearchParams();
  if (params.windowDays !== undefined) search.set('windowDays', String(params.windowDays));
  if (params.sampleKind !== undefined) search.set('sampleKind', params.sampleKind);
  if (params.limit !== undefined) search.set('limit', String(params.limit));
  if (params.offset !== undefined) search.set('offset', String(params.offset));
  return search.size > 0 ? `${base}?${search.toString()}` : base;
}

export async function fetchSiteOptimizationNegativeSamples(
  siteId: string,
  params: NegativeSamplesQueryParams,
  signal?: AbortSignal,
): Promise<OptimizationNegativeSamplePage> {
  const result = await gatewayRequest(
    buildNegativeSamplesPath(siteId, params),
    {
      signal,
      parse: (payload) => optimizationNegativeSamplePageSchema.parse(payload),
    },
  );
  return valueOf(result, { unavailable: UNAVAILABLE, refused: canaryRefusal });
}

export function siteOptimizationNegativeSamplesQuery(
  siteId: string,
  params: NegativeSamplesQueryParams,
  enabled: boolean,
) {
  return queryOptions({
    queryKey: optimizationKeys.negativeSamples(siteId, params),
    queryFn: ({ signal }) => fetchSiteOptimizationNegativeSamples(siteId, params, signal),
    enabled: Boolean(siteId) && enabled,
    retry: false,
  });
}

export type { OptimizationNegativeSampleItem };

/** Asks the Optimization owner for a new recommendation; it is computed and published asynchronously. */
export async function requestRecommendation(siteId: string, csrfToken: string): Promise<{ readonly runId: string }> {
  const result = await gatewayRequest(
    intelligencePaths.optimizationRuns,
    {
      method: 'POST', csrfToken, body: { siteId },
      parse: (payload) => ({ runId: optimizationRunResponseSchema.parse(payload).optimizationRunId }),
    },
  );
  return valueOf(result, { unavailable: UNAVAILABLE, refused: runRefusal });
}

function runRefusal(problem: GatewayProblem): string {
  // The owner declines for now (retryable) when an input it weighs has not been produced yet,
  // and for good when the Site's policy or deployment cannot be used.
  if (problem.code === 'INTELLIGENCE_UPSTREAM_REJECTED') {
    return problem.retryable
      ? '暂时无法生成建议：最近一天的用电与电费、最新负荷预测或实时供水、室内温度尚未就绪'
      : '无法生成建议：站点的优化策略或模型部署不可用';
  }
  return UNAVAILABLE;
}

/** The latest stage-down and idle-auxiliary evaluation; null until the service has evaluated this Site. */
export function latestStagingQuery(siteId: string) {
  return queryOptions({
    queryKey: optimizationKeys.staging(siteId),
    queryFn: async ({ signal }): Promise<StagingEvaluation | null> => publishedValueOf(
      await gatewayRequest(
        gatewayPath(intelligencePaths.latestOptimizationStagingRecommendations, { siteId }),
        { signal, parse: (payload) => stagingEvaluationSchema.parse(payload) },
      ),
      { unavailable: UNAVAILABLE },
    ),
    refetchInterval: 60_000,
    retry: false,
  });
}
