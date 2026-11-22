import type { WorkOrder, WorkOrderPriority, WorkOrderStatus } from '@/api/work-orders';
import { StatusBadge, type StatusTone } from '@/components/status-badge';
import { cn } from '@/lib/utils';

export const PRIORITY_LABELS: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: '紧急',
  HIGH: '高',
  MEDIUM: '中',
  LOW: '低',
};

export const PRIORITY_CLASSES: Readonly<Record<WorkOrderPriority, string>> = {
  URGENT: 'bg-destructive-soft text-destructive-text',
  HIGH: 'bg-warning-soft text-severity-major',
  MEDIUM: 'bg-secondary text-secondary-foreground',
  LOW: 'border border-border text-muted-foreground',
};

export const STATUS_LABELS: Readonly<Record<WorkOrderStatus, string>> = {
  DRAFT: '草稿',
  OPEN: '待处理',
  IN_PROGRESS: '处理中',
  BLOCKED: '受阻',
  COMPLETED: '已完成',
  CANCELLED: '已取消',
};

const STATUS_TONE: Readonly<Record<WorkOrderStatus, StatusTone>> = {
  DRAFT: 'neutral',
  OPEN: 'neutral',
  IN_PROGRESS: 'info',
  BLOCKED: 'warning',
  COMPLETED: 'success',
  CANCELLED: 'neutral',
};

export function WorkOrderStatusBadge({ status }: { readonly status: WorkOrderStatus }) {
  return <StatusBadge tone={STATUS_TONE[status]} label={STATUS_LABELS[status]} />;
}

export const OPEN_STATUSES: readonly WorkOrderStatus[] = ['OPEN', 'IN_PROGRESS', 'BLOCKED'];

const PRIORITY_ORDER: readonly WorkOrderPriority[] = ['URGENT', 'HIGH', 'MEDIUM', 'LOW'];

export function PriorityBadge({ priority }: { readonly priority: WorkOrderPriority }) {
  return (
    <span className={cn('inline-flex h-5 items-center rounded-md px-[7px] text-[11px] font-medium', PRIORITY_CLASSES[priority])}>
      {PRIORITY_LABELS[priority]}
    </span>
  );
}

/** Most urgent first, then the newest. */
export function sortWorkOrdersForOperators(workOrders: readonly WorkOrder[]): WorkOrder[] {
  return [...workOrders].sort((left, right) => PRIORITY_ORDER.indexOf(left.priority) - PRIORITY_ORDER.indexOf(right.priority)
    || Date.parse(right.createdAt) - Date.parse(left.createdAt));
}
