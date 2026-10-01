import type { WorkOrderPriority, WorkOrderStatus } from '@/api/work-orders';

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
