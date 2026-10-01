import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { MeteringSemanticModelWorkspace } from '@/features/model/MeteringSemanticModelWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/model')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '计量与语义模型',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-model',
      label: '计量与语义模型',
      group: 'system',
      order: 75,
      siteLeaf: 'model',
    },
  },
  component: ModelRoute,
});

function ModelRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载计量与语义模型" />}>
      <MeteringSemanticModelWorkspace siteId={site.id} />
    </Suspense>
  );
}
