import type { WorkOrder, WorkOrderList, WorkOrderListFilter } from '@/api/work-orders';

const tenantId = '01940000-0000-7000-8000-000000000001';
const siteId = '01940000-0001-7000-8000-000000000001';

export const frontendReviewWorkOrders: readonly WorkOrder[] = [
  {
    schemaVersion: 1,
    workOrderId: 'WO-202609-101',
    tenantId,
    siteId,
    title: '检查 CH-02 冷凝压力偏高',
    description: '核对冷却水温度、冷凝器压差与导叶开度，确认高压侧异常是否持续。',
    priority: 'URGENT',
    status: 'IN_PROGRESS',
    sourceReferences: [{ domain: 'MANUAL', resourceId: 'review:ch02-condenser', relationship: 'ORIGIN' }],
    assigneeId: '张工',
    teamId: '冷站运维组',
    scheduledStart: '2026-09-21T05:30:00.000Z',
    dueAt: '2026-09-21T09:30:00.000Z',
    tasks: { total: 5, completed: 2, blocked: 0 },
    noteCount: 4,
    attachmentCount: 2,
    completionEvidence: [],
    timeline: [],
    version: 3,
    createdAt: '2026-09-21T05:20:00.000Z',
    updatedAt: '2026-09-21T06:18:00.000Z',
  },
  {
    schemaVersion: 1,
    workOrderId: 'WO-202609-098',
    tenantId,
    siteId,
    title: '排查冷冻水供回水温差偏低',
    description: '检查旁通阀、末端二通阀及水泵频率，定位小温差大流量原因。',
    priority: 'HIGH',
    status: 'OPEN',
    sourceReferences: [{ domain: 'MANUAL', resourceId: 'review:chw-delta-t', relationship: 'ORIGIN' }],
    teamId: '水系统运维组',
    dueAt: '2026-09-21T12:00:00.000Z',
    tasks: { total: 4, completed: 0, blocked: 0 },
    noteCount: 2,
    attachmentCount: 1,
    completionEvidence: [],
    timeline: [],
    version: 1,
    createdAt: '2026-09-21T04:50:00.000Z',
    updatedAt: '2026-09-21T05:06:00.000Z',
  },
  {
    schemaVersion: 1,
    workOrderId: 'WO-202609-091',
    tenantId,
    siteId,
    title: '冷却塔 CT-01 振动复核',
    description: '复核风机振动、皮带张力与轴承温升，记录复测数据。',
    priority: 'MEDIUM',
    status: 'BLOCKED',
    sourceReferences: [{ domain: 'MANUAL', resourceId: 'review:ct01-vibration', relationship: 'ORIGIN' }],
    assigneeId: '李工',
    teamId: '机电维护组',
    scheduledStart: '2026-09-20T23:00:00.000Z',
    dueAt: '2026-09-21T14:00:00.000Z',
    tasks: { total: 3, completed: 1, blocked: 1 },
    noteCount: 5,
    attachmentCount: 3,
    completionEvidence: [],
    timeline: [],
    version: 4,
    createdAt: '2026-09-20T12:10:00.000Z',
    updatedAt: '2026-09-21T03:25:00.000Z',
  },
  {
    schemaVersion: 1,
    workOrderId: 'WO-202609-084',
    tenantId,
    siteId,
    title: '校验 3# 冷冻水泵压差传感器',
    description: '执行零点检查和便携表比对，确认传感器漂移幅度。',
    priority: 'MEDIUM',
    status: 'COMPLETED',
    sourceReferences: [{ domain: 'MANUAL', resourceId: 'review:dp-sensor', relationship: 'ORIGIN' }],
    assigneeId: '王工',
    teamId: '自控组',
    scheduledStart: '2026-09-20T02:00:00.000Z',
    dueAt: '2026-09-20T06:00:00.000Z',
    tasks: { total: 4, completed: 4, blocked: 0 },
    noteCount: 3,
    attachmentCount: 4,
    completionEvidence: [
      { kind: 'measurement', reference: 'review://evidence/dp-sensor-084', capturedAt: '2026-09-20T05:42:00.000Z' },
    ],
    timeline: [],
    version: 5,
    createdAt: '2026-09-19T14:30:00.000Z',
    updatedAt: '2026-09-20T05:50:00.000Z',
  },
  {
    schemaVersion: 1,
    workOrderId: 'WO-202609-079',
    tenantId,
    siteId,
    title: '检查 AHU-12 过滤器压差',
    description: '核实过滤器压差与风量，判断是否达到更换条件。',
    priority: 'LOW',
    status: 'OPEN',
    sourceReferences: [{ domain: 'MANUAL', resourceId: 'review:ahu12-filter', relationship: 'ORIGIN' }],
    teamId: '末端运维组',
    dueAt: '2026-09-22T04:00:00.000Z',
    tasks: { total: 2, completed: 0, blocked: 0 },
    noteCount: 1,
    attachmentCount: 0,
    completionEvidence: [],
    timeline: [],
    version: 1,
    createdAt: '2026-09-20T08:15:00.000Z',
    updatedAt: '2026-09-20T08:15:00.000Z',
  },
];

export function listFrontendReviewWorkOrders(filter: WorkOrderListFilter): WorkOrderList {
  const items = frontendReviewWorkOrders.filter((item) => {
    if (filter.status && item.status !== filter.status) return false;
    if (filter.priority && item.priority !== filter.priority) return false;
    if (filter.assigneeId && item.assigneeId !== filter.assigneeId) return false;
    return true;
  });
  const limit = filter.limit ?? 50;
  return {
    schemaVersion: 1,
    items: items.slice(0, limit),
    nextCursor: null,
    hasMore: false,
  };
}

export function getFrontendReviewWorkOrder(workOrderId: string): WorkOrder {
  const workOrder = frontendReviewWorkOrders.find((item) => item.workOrderId === workOrderId);
  if (!workOrder) throw new Error('Frontend review work order not found.');
  return workOrder;
}
