import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { RulesWorkspace } from '@/features/rules/RulesWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/rules')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '规则与通知',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-rules',
      label: '规则与通知',
      group: 'system',
      order: 80,
      siteLeaf: 'rules',
    },
  },
  component: RulesRoute,
});

function RulesRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载检测规则与通知策略工作台" />}>
      <RulesWorkspace siteId={site.id} />
    </Suspense>
  );
}
