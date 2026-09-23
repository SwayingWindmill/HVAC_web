import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { MeasurementVerificationWorkspace } from '@/features/mv/MeasurementVerificationWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/mv')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '节能量验证 (M&V)',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: MVRoute,
});

function MVRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载节能量验证" />}>
      <MeasurementVerificationWorkspace siteId={site.id} />
    </Suspense>
  );
}
