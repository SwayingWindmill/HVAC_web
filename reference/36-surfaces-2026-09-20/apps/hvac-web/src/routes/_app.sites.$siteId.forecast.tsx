import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { ForecastPage } from '@/features/product/ProductPages';

export const Route = createFileRoute('/_app/sites/$siteId/forecast')({
  staticData: {
    title: '预测与基线',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: ForecastRoute,
});

function ForecastRoute() {
  const { site, principal } = Route.useRouteContext();
  return <Suspense fallback={<RouteLoading label="正在加载工作台" />}><ForecastPage site={site} principal={principal} /></Suspense>;
}
