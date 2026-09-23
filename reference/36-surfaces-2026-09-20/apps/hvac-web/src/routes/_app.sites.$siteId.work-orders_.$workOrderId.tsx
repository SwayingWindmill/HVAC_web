import { Suspense } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import { WorkOrderDetailWorkspace } from '@/features/work-orders/WorkOrderDetailWorkspace';

export const Route = createFileRoute('/_app/sites/$siteId/work-orders_/$workOrderId')({
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['work-order.list', 'work-order.read']);
  },
  staticData: {
    title: '工单详情',
    scope: 'site',
    requiredCapabilities: ['site.read', 'work-order.list', 'work-order.read'],
  },
  component: WorkOrderDetailRoute,
});

function WorkOrderDetailRoute() {
  const { site } = Route.useRouteContext();
  const { workOrderId } = Route.useParams();

  return (
    <Suspense fallback={<RouteLoading label="正在加载工单技术排查与证据详情" />}>
      <WorkOrderDetailWorkspace siteId={site.id} workOrderId={workOrderId} />
    </Suspense>
  );
}
