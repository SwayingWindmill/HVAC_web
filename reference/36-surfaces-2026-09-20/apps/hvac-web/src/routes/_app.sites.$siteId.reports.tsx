import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { ReportsHub } from '@/features/reports/ReportsHub';

export const Route = createFileRoute('/_app/sites/$siteId/reports')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '报告中心',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-reports',
      label: '报告中心',
      group: 'management',
      order: 45,
      siteLeaf: 'reports',
    },
  },
  component: ReportsRoute,
});

function ReportsRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载报告中心与合规分发工作台" />}>
      <ReportsHub siteId={site.id} />
    </Suspense>
  );
}
