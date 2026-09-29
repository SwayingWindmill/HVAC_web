import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { PortfolioOverviewWorkspace } from '@/features/portfolio/PortfolioOverviewWorkspace';

export const Route = createFileRoute('/_app/portfolio/overview')({
  staticData: {
    title: '企业总览',
    scope: 'platform',
    navigation: {
      id: 'portfolio-overview',
      label: '企业总览',
      group: 'management',
      order: 5,
    },
  },
  component: PortfolioOverviewRoute,
});

function PortfolioOverviewRoute() {
  return (
    <Suspense fallback={<RouteLoading label="正在加载企业总览与跨站点治理大盘" />}>
      <PortfolioOverviewWorkspace />
    </Suspense>
  );
}
