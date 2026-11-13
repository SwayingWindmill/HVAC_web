import { queryOptions } from '@tanstack/react-query';
import { z } from 'zod';
import { gatewayPath, gatewayRequest, publishedValueOf, valueOf, type GatewayProblem } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { listWorkOrders, type WorkOrderList } from '@/api/work-orders';
import { workOrderKeys } from '@/features/work-orders/work-order-queries';
import {
  optimizationRecommendationSourceRef,
  parseRecommendation,
  type OptimizationRecommendation,
  type PreparationOutcome,
  type PublishedRecommendation,
  type PublishedShadowEvaluation,
} from './optimization-model';

const opt = <S extends z.ZodTypeAny>(schema: S) => schema.nullish().transform((value) => value ?? undefined);

const preparationSchema = z.object({
  status: z.enum(['PREPARED', 'FAILED', 'PENDING']),
  reason: z.string().nullable().optional(),
  firedAt: z.string(),
});

const riskLevelSchema = z.enum(['LOW', 'MEDIUM', 'HIGH']);

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

export const optimizationKeys = {
  latest: (siteId: string) => ['optimization', siteId, 'latest'] as const,
  preparation: (siteId: string) => ['optimization', siteId, 'preparation'] as const,
  shadow: (siteId: string, runId: string | null) => ['optimization', siteId, 'shadow', runId] as const,
};

const UNAVAILABLE = '优化服务暂时不可用';

/** How the Site's hourly schedule last fired; null when the Site has no Optimization schedule that has fired. */
export function latestPreparationQuery(siteId: string) {
  return queryOptions({
    queryKey: optimizationKeys.preparation(siteId),
    queryFn: async ({ signal }): Promise<PreparationOutcome | null> => publishedValueOf(
      await gatewayRequest(gatewayPath(intelligencePaths.latestOptimizationPreparation, { siteId }), {
        signal,
        parse: (payload) => {
          const outcome = preparationSchema.parse(payload);
          return { status: outcome.status, reason: outcome.reason ?? null, firedAt: outcome.firedAt };
        },
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
