import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { ExecutionsLedger } from '@/features/executions/ExecutionsLedger';

export const Route = createFileRoute('/_app/sites/$siteId/executions')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '执行记录',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: ExecutionsRoute,
});

function ExecutionsRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载执行记录" />}>
      <ExecutionsLedger siteId={site.id} />
    </Suspense>
  );
}
