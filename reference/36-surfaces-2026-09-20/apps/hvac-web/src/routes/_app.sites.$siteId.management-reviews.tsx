import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { ManagementReviewWorkspace } from '@/features/management-reviews/ManagementReviewWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/management-reviews')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '管理评审',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-management-reviews',
      label: '管理评审',
      group: 'management',
      order: 70,
      siteLeaf: 'management-reviews',
    },
  },
  component: ManagementReviewsRoute,
});

function ManagementReviewsRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载 ISO 50001 管理评审决策工作台" />}>
      <ManagementReviewWorkspace siteId={site.id} />
    </Suspense>
  );
}
