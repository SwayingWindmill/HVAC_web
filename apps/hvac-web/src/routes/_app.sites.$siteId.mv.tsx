import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { VerificationWorkspace } from '@/features/verification/components/VerificationWorkspace';

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
  return (
    <Suspense fallback={<RouteLoading label="正在加载节能量验证" />}>
      <VerificationWorkspace />
    </Suspense>
  );
}
