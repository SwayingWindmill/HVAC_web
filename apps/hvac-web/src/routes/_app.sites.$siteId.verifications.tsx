import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { VerificationsWorkspace } from '@/features/verifications/VerificationsWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/verifications')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '功能验证与持续调试',
    scope: 'site',
    requiredCapabilities: ['site.read'],
  },
  component: VerificationsRoute,
});

function VerificationsRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载功能验证与持续调试" />}>
      <VerificationsWorkspace siteId={site.id} />
    </Suspense>
  );
}
