import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { BigScreenPage } from '@/features/product/ProductPages';

export const Route = createFileRoute('/_app/sites/$siteId/bigscreen')({
  staticData: {
    title: '运行大屏',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: BigScreenRoute,
});

function BigScreenRoute() {
  const { site, principal } = Route.useRouteContext();
  return <Suspense fallback={<RouteLoading label="正在加载运行大屏" />}><BigScreenPage site={site} principal={principal} /></Suspense>;
}
