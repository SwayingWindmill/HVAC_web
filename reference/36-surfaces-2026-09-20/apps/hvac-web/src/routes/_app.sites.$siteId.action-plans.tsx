import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { ActionPlansWorkspace } from '@/features/action-plans/ActionPlansWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/action-plans')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '目标与行动计划',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-action-plans',
      label: '目标与计划',
      group: 'management',
      order: 30,
      siteLeaf: 'action-plans',
    },
  },
  component: ActionPlansRoute,
});

function ActionPlansRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载目标与行动计划工作台" />}>
      <ActionPlansWorkspace siteId={site.id} />
    </Suspense>
  );
}
