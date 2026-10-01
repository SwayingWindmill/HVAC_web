import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { StrategyDetailWorkspace } from '@/features/strategies/StrategyDetailWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/strategies_/$strategyId')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '策略详情 / 仿真 / 审批',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: StrategyDetailRoute,
});

function StrategyDetailRoute() {
  const { site } = Route.useRouteContext();
  const { strategyId } = Route.useParams();

  return (
    <Suspense fallback={<RouteLoading label="正在加载策略工程仿真与安全审批详情" />}>
      <StrategyDetailWorkspace siteId={site.id} strategyId={strategyId} />
    </Suspense>
  );
}
