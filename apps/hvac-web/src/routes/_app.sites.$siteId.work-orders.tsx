import { Suspense, useCallback } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { zodValidator } from '@tanstack/zod-adapter';
import { z } from 'zod';
import { RouteLoading } from '@/app/RouteLoading';
import { requireCapabilities } from '@/app/route-access';
import type { ProtectedScopeResource } from '@/app/protected-scope';
import { WorkOrders } from '@/features/work-orders/WorkOrders';

const workOrderSearchSchema = z.object({
  workOrder: z.string().optional(),
  sourceAlarm: z.string().optional(),
  device: z.string().optional(),
  source: z.string().optional(),
  filters: z.string().optional(),
  joinOperator: z.enum(['and', 'or']).optional(),
});

export const Route = createFileRoute('/_app/sites/$siteId/work-orders')({
  validateSearch: zodValidator(workOrderSearchSchema),
  beforeLoad: ({ context }) => {
    requireCapabilities(context.runtime, ['work-order.list']);
  },
  staticData: {
    title: '工单',
    scope: 'site',
    requiredCapabilities: ['site.read', 'work-order.list'],
    navigation: { id: 'site-work-orders', label: '工单', group: 'management', order: 50, siteLeaf: 'work-orders' },
  },
  component: WorkOrdersRoute,
});

function WorkOrdersRoute() {
  const { site, principal, runtime } = Route.useRouteContext();
  const registerProtectedResource = useCallback(
    (resource: ProtectedScopeResource) => runtime.registerProtectedResource(resource),
    [runtime],
  );
  return (
    <Suspense fallback={<RouteLoading label="正在加载工单" />}>
      <WorkOrders
        site={site}
        principal={principal}
        registerProtectedResource={registerProtectedResource}
      />
    </Suspense>
  );
}
