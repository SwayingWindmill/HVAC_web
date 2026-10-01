export type WorkOrderPriority = 'LOW' | 'MEDIUM' | 'HIGH' | 'URGENT';

export type WorkOrderStatus = 'OPEN' | 'IN_PROGRESS' | 'BLOCKED' | 'COMPLETED' | 'CANCELLED';

export type WorkOrderCategory = 'CORRECTIVE' | 'PREVENTIVE' | 'OPTIMIZATION' | 'VERIFICATION';

export interface ChecklistTask {
  id: string;
  title: string;
  requirement: string;
  completed: boolean;
  operator?: string;
  completedAt?: string;
}

export interface RequiredPart {
  id: string;
  code: string;
  name: string;
  specification: string;
  quantity: number;
  unit: string;
  inStock: boolean;
}

export interface WorkOrderTimelineEntry {
  id: string;
  time: string;
  action: string;
  operator: string;
  detail: string;
  type: 'CREATE' | 'ASSIGN' | 'START' | 'BLOCK' | 'RESUME' | 'COMPLETE' | 'VERIFY';
}

export interface WorkOrderItem {
  id: string;
  code: string; // e.g. WO-202609-101
  title: string;
  description: string;
  priority: WorkOrderPriority;
  status: WorkOrderStatus;
  category: WorkOrderCategory;

  // Source Reference
  sourceAlarmCode?: string;
  sourceFinding?: string;

  // Object & Location
  deviceId: string;
  deviceName: string;
  subsystem: string;
  location: string;

  // Assignment & Team
  assigneeName?: string;
  assigneeRole?: string;
  teamName: string;

  // Schedule & SLA
  scheduledStart: string;
  dueAt: string;
  durationHoursEstimated: number;
  durationHoursActual?: number;
  isOverdue?: boolean;

  // Tasks & Checklist
  tasksTotal: number;
  tasksCompleted: number;
  blockReason?: string;
  checklist: ChecklistTask[];
  parts: RequiredPart[];
  timeline: WorkOrderTimelineEntry[];

  createdAt: string;
  updatedAt: string;
}

export interface WorkOrdersSummary {
  openCount: number;
  inProgressCount: number;
  blockedCount: number;
  completedCount: number;
  urgentCount: number;
  unassignedCount: number;
  averageExecutionHours: number;
  weeklyClosedCount: number;
  slaComplianceRate: number; // e.g. 94.2%
}

export interface WorkOrdersFilterParams {
  search?: string;
  priority?: string;
  status?: string;
  category?: string;
  team?: string;
}
