import { listWorkOrders, type WorkOrderStatus } from '@/api/work-orders';

export const workOrderKeys = {
  all: (siteId: string) => ['work-orders', siteId] as const,
  view: (siteId: string, statuses: readonly WorkOrderStatus[], assigneeId?: string) =>
    ['work-orders', siteId, 'view', statuses.join(','), assigneeId ?? ''] as const,
  source: (siteId: string, domain: string, resourceId: string) => ['work-orders', siteId, 'source', domain, resourceId] as const,
};

// The owner filters by one status at a time; a view is the union of its statuses.
export async function listView(siteId: string, statuses: readonly WorkOrderStatus[], assigneeId: string | undefined, signal: AbortSignal) {
  const filters = statuses.length > 0 ? statuses.map((status) => ({ status })) : [{}];
  const pages = await Promise.all(filters.map((filter) =>
    listWorkOrders({ ...filter, assigneeId, limit: 100 }, { siteId, signal })));
  return pages.flatMap((page) => page.items);
}
