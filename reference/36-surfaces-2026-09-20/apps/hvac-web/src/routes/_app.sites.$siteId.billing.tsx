import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { BillingCostDashboard } from '@/features/billing/BillingCostDashboard';

export const Route = createFileRoute('/_app/sites/$siteId/billing')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '账单与成本',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: BillingRoute,
});

function BillingRoute() {
  const { site, principal } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载账单与成本" />}>
      <BillingCostDashboard site={site} principal={principal} />
    </Suspense>
  );
}
