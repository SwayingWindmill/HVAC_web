import { queryOptions } from '@tanstack/react-query';
import { gatewayPath, gatewayRequest, publishedValueOf, valueOf, type GatewayProblem } from '@/api/gateway-client';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { parseRecommendation, type OptimizationRecommendation, type PublishedRecommendation } from './optimization-model';

export const optimizationKeys = {
  latest: (siteId: string) => ['optimization', siteId, 'latest'] as const,
};

const UNAVAILABLE = '优化服务暂时不可用';

/** The Site's latest published recommendation; null until the Optimization owner has published one. */
export function latestRecommendationQuery(siteId: string, waitingForRun: string | null) {
  return queryOptions({
    queryKey: optimizationKeys.latest(siteId),
    queryFn: async ({ signal }): Promise<OptimizationRecommendation | null> => publishedValueOf(
      await gatewayRequest(gatewayPath(intelligencePaths.latestOptimizationRecommendation, { siteId }), {
        signal, parse: (payload) => parseRecommendation(payload as PublishedRecommendation),
      }),
      { unavailable: UNAVAILABLE },
    ),
    // A requested run publishes within seconds; until it does the page asks again.
    refetchInterval: waitingForRun ? 3_000 : false,
    retry: false,
  });
}

/** Asks the Optimization owner for a new recommendation; it is computed and published asynchronously. */
export async function requestRecommendation(siteId: string, csrfToken: string): Promise<{ readonly runId: string }> {
  const result = await gatewayRequest(intelligencePaths.optimizationRuns, {
    method: 'POST', csrfToken, body: { siteId },
    parse: (payload) => ({ runId: (payload as { optimizationRunId: string }).optimizationRunId }),
  });
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
