import { queryOptions } from '@tanstack/react-query';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { parseRecommendation, type OptimizationRecommendation, type PreparationOutcome, type PublishedRecommendation } from './optimization-model';

export class OptimizationRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

export const optimizationKeys = {
  latest: (siteId: string) => ['optimization', siteId, 'latest'] as const,
  preparation: (siteId: string) => ['optimization', siteId, 'preparation'] as const,
};

/** How the Site's hourly schedule last fired; null when the Site has no Optimization schedule that has fired. */
export function latestPreparationQuery(siteId: string) {
  return queryOptions({
    queryKey: optimizationKeys.preparation(siteId),
    queryFn: async ({ signal }): Promise<PreparationOutcome | null> => {
      const response = await fetch(intelligencePaths.latestOptimizationPreparation.replace('{siteId}', siteId), {
        credentials: 'same-origin', signal, headers: { Accept: 'application/json, application/problem+json' },
      });
      if (response.status === 404) return null;
      const payload = await response.json().catch(() => null);
      if (!response.ok) throw new OptimizationRequestError(response.status, payload?.detail ?? '优化服务暂时不可用');
      return { status: payload.status, reason: payload.reason ?? null, firedAt: payload.firedAt };
    },
    retry: false,
  });
}

/** The Site's latest published recommendation; null until the Optimization owner has published one. */
export function latestRecommendationQuery(siteId: string, waitingForRun: string | null) {
  return queryOptions({
    queryKey: optimizationKeys.latest(siteId),
    queryFn: async ({ signal }): Promise<OptimizationRecommendation | null> => {
      const response = await fetch(intelligencePaths.latestOptimizationRecommendation.replace('{siteId}', siteId), {
        credentials: 'same-origin', signal, headers: { Accept: 'application/json, application/problem+json' },
      });
      if (response.status === 404) return null;
      const payload = await response.json().catch(() => null);
      if (!response.ok) throw new OptimizationRequestError(response.status, payload?.detail ?? '优化服务暂时不可用');
      return parseRecommendation(payload as PublishedRecommendation);
    },
    // A requested run publishes within seconds; until it does the page asks again.
    refetchInterval: waitingForRun ? 3_000 : false,
    retry: false,
  });
}

/** Asks the Optimization owner for a new recommendation; it is computed and published asynchronously. */
export async function requestRecommendation(siteId: string, csrfToken: string): Promise<{ readonly runId: string }> {
  const response = await fetch(intelligencePaths.optimizationRuns, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, application/problem+json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify({ siteId }),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new OptimizationRequestError(response.status, runRefusal(payload));
  return { runId: payload.optimizationRunId as string };
}

function runRefusal(problem: { code?: string; retryable?: boolean } | null): string {
  // The owner declines for now (retryable) when an input it weighs has not been produced yet,
  // and for good when the Site's policy or deployment cannot be used.
  if (problem?.code === 'INTELLIGENCE_UPSTREAM_REJECTED') {
    return problem.retryable
      ? '暂时无法生成建议：最近一天的用电与电费、最新负荷预测或实时供水、室内温度尚未就绪'
      : '无法生成建议：站点的优化策略或模型部署不可用';
  }
  return '优化服务暂时不可用';
}
