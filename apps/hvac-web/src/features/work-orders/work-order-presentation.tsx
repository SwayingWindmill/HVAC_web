import type { WorkOrder, WorkOrderPriority, WorkOrderStatus } from '@/api/work-orders';
import { cn } from '@/lib/utils';

export const PRIORITY_LABELS: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: '紧急',
  HIGH: '高',
  MEDIUM: '中',
  LOW: '低',
};

export const PRIORITY_CLASSES: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/50 dark:text-red-300',
  HIGH: 'border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-900 dark:bg-orange-950/50 dark:text-orange-300',
  MEDIUM: 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900 dark:bg-sky-950/50 dark:text-sky-300',
  LOW: 'border-border bg-muted text-muted-foreground',
};

export const STATUS_LABELS: Readonly<Record<WorkOrderStatus, string>> = {
  DRAFT: '草稿',
  OPEN: '待处理',
  IN_PROGRESS: '处理中',
  BLOCKED: '受阻',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
};

export const OPEN_STATUSES: readonly WorkOrderStatus[] = ['OPEN', 'IN_PROGRESS', 'BLOCKED'];

const PRIORITY_ORDER: readonly WorkOrderPriority[] = ['URGENT', 'HIGH', 'MEDIUM', 'LOW'];

export function PriorityBadge({ priority }: { readonly priority: WorkOrderPriority }) {
  return (
    <span className={cn('inline-flex items-center rounded-md border px-1.5 py-0.5 text-xs font-medium', PRIORITY_CLASSES[priority])}>
      {PRIORITY_LABELS[priority]}
    </span>
  );
}

/** Most urgent first, then the newest. */
export function sortWorkOrdersForOperators(workOrders: readonly WorkOrder[]): WorkOrder[] {
  return [...workOrders].sort((left, right) => PRIORITY_ORDER.indexOf(left.priority) - PRIORITY_ORDER.indexOf(right.priority)
    || Date.parse(right.createdAt) - Date.parse(left.createdAt));
}
