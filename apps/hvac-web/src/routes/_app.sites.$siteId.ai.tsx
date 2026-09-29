import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { siteRoute } from '@/app/router-paths';
import { AiLanding } from '@/features/product/ProductPages';

export const Route = createFileRoute('/_app/sites/$siteId/ai')({
  staticData: {
    title: 'AI 运维助手',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: AiRoute,
});

function AiRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载智能助手" />}>
      <AiLanding site={site} principal={principal} operationsPath={siteRoute(site, 'operations')} />
    </Suspense>
  );
}
