import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { DataQualityWorkspace } from '@/features/data-quality/DataQualityWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/data-quality')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['site.read']);
  },
  staticData: {
    title: '数据质量',
    scope: 'site',
    requiredCapabilities: ['site.read'],
    navigation: {
      id: 'site-data-quality',
      label: '数据质量',
      group: 'system',
      order: 70,
      siteLeaf: 'data-quality',
    },
  },
  component: DataQualityRoute,
});

function DataQualityRoute() {
  const { site } = Route.useRouteContext();
  return (
    <Suspense fallback={<RouteLoading label="正在加载数据质量与遥测可观测性工作台" />}>
      <DataQualityWorkspace siteId={site.id} />
    </Suspense>
  );
}
