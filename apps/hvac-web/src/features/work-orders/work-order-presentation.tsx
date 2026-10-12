import type { WorkOrder, WorkOrderPriority, WorkOrderStatus } from '@/api/work-orders';
import { Status, StatusIndicator } from '@/components/ui/status';
import { cn } from '@/lib/utils';

export const PRIORITY_LABELS: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: '紧急',
  HIGH: '高',
  MEDIUM: '中',
  LOW: '低',
};

export const PRIORITY_CLASSES: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: 'bg-red-500/10 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  HIGH: 'bg-orange-500/10 text-orange-700 dark:bg-orange-500/15 dark:text-orange-300',
  MEDIUM: 'bg-sky-500/10 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  LOW: 'bg-muted text-muted-foreground',
};

export const STATUS_LABELS: Readonly<Record<WorkOrderStatus, string>> = {
  DRAFT: '草稿',
  OPEN: '待处理',
  IN_PROGRESS: '处理中',
  BLOCKED: '受阻',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
};

const STATUS_TONE: Readonly<Record<WorkOrderStatus, 'default' | 'info' | 'warning' | 'success'>> = {
  DRAFT: 'default',
  OPEN: 'default',
  IN_PROGRESS: 'info',
  BLOCKED: 'warning',
  COMPLETED: 'success',
  CANCELLED: 'default',
};

export function WorkOrderStatusBadge({ status }: { readonly status: WorkOrderStatus }) {
  return (
    <Status variant={STATUS_TONE[status]}>
      <StatusIndicator />
      {STATUS_LABELS[status]}
    </Status>
  );
}

export const OPEN_STATUSES: readonly WorkOrderStatus[] = ['OPEN', 'IN_PROGRESS', 'BLOCKED'];

const PRIORITY_ORDER: readonly WorkOrderPriority[] = ['URGENT', 'HIGH', 'MEDIUM', 'LOW'];

export function PriorityBadge({ priority }: { readonly priority: WorkOrderPriority }) {
  return (
    <span className={cn('inline-flex h-5.5 items-center rounded-md px-1.5 text-xs font-medium', PRIORITY_CLASSES[priority])}>
      {PRIORITY_LABELS[priority]}
    </span>
  );
}

/** Most urgent first, then the newest. */
export function sortWorkOrdersForOperators(workOrders: readonly WorkOrder[]): WorkOrder[] {
  return [...workOrders].sort((left, right) => PRIORITY_ORDER.indexOf(left.priority) - PRIORITY_ORDER.indexOf(right.priority)
    || Date.parse(right.createdAt) - Date.parse(left.createdAt));
}
