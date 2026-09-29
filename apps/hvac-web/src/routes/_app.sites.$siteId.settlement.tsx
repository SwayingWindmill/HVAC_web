import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { SettlementPage } from '@/features/product/ProductPages';

export const Route = createFileRoute('/_app/sites/$siteId/settlement')({
  staticData: {
    title: '结算与对账',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: SettlementRoute,
});

function SettlementRoute() {
  const { site, principal } = Route.useRouteContext();
  return <Suspense fallback={<RouteLoading label="正在加载结算" />}><SettlementPage site={site} principal={principal} /></Suspense>;
}
