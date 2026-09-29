import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { FddPage } from '@/features/product/ProductPages';

export const Route = createFileRoute('/_app/sites/$siteId/fdd')({
  staticData: {
    title: '故障检测',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: FddRoute,
});

function FddRoute() {
  const { site, principal } = Route.useRouteContext();
  return <Suspense fallback={<RouteLoading label="正在加载故障诊断" />}><FddPage site={site} principal={principal} /></Suspense>;
}
