import { z } from 'zod';
import { gatewayRequest, ROUTE_MISSING_MESSAGE, type GatewayFailure, type GatewayRequest } from './gateway-client.ts';

export const workOrderPrioritySchema = z.enum(['LOW', 'MEDIUM', 'HIGH', 'URGENT']);
export const workOrderStatusSchema = z.enum(['DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED', 'COMPLETED', 'CANCELLED']);
export const workOrderSourceDomainSchema = z.enum(['MANUAL', 'ALARM', 'ASSET', 'EQUIPMENT', 'INVESTIGATION', 'EXTERNAL']);
export const workOrderSourceRelationshipSchema = z.enum(['ORIGIN', 'RELATED']);
export const workOrderOperationSchema = z.enum(['CREATE', 'OPEN', 'ASSIGN', 'UNASSIGN', 'SCHEDULE', 'START', 'BLOCK', 'RESUME', 'COMPLETE', 'CANCEL', 'REOPEN']);

const sourceReferenceSchema = z.object({
  domain: workOrderSourceDomainSchema,
  resourceId: z.string().min(1).max(512),
  relationship: workOrderSourceRelationshipSchema,
}).strict();

const evidenceReferenceSchema = z.object({
  kind: z.string().min(1).max(128),
  reference: z.string().min(1).max(1024),
  capturedAt: z.string().min(1),
}).strict();

const timelineEventSchema = z.object({
  operation: workOrderOperationSchema,
  fromStatus: workOrderStatusSchema.optional(),
  toStatus: workOrderStatusSchema,
  reason: z.string(),
  actorType: z.string(),
  actorId: z.string(),
  assigneeId: z.string().optional(),
  teamId: z.string().optional(),
  policyRevision: z.string().optional(),
  correlationId: z.string().optional(),
  occurredAt: z.string(),
  version: z.number().int().positive(),
}).strict();

export const workOrderSchema = z.object({
  schemaVersion: z.literal(1),
  workOrderId: z.string(),
  tenantId: z.string(),
  siteId: z.string(),
  title: z.string(),
  description: z.string(),
  priority: workOrderPrioritySchema,
  status: workOrderStatusSchema,
  sourceReferences: z.array(sourceReferenceSchema),
  assigneeId: z.string().optional(),
  teamId: z.string().optional(),
  scheduledStart: z.string().optional(),
  dueAt: z.string().optional(),
  tasks: z.object({ total: z.number().int().nonnegative(), completed: z.number().int().nonnegative(), blocked: z.number().int().nonnegative() }).strict(),
  noteCount: z.number().int().nonnegative(),
  attachmentCount: z.number().int().nonnegative(),
  completionEvidence: z.array(evidenceReferenceSchema),
  timeline: z.array(timelineEventSchema),
  version: z.number().int().positive(),
  createdAt: z.string(),
  updatedAt: z.string(),
}).strict();

const workOrderListSchema = z.object({
  schemaVersion: z.literal(1),
  items: z.array(workOrderSchema),
  nextCursor: z.string().nullable(),
  hasMore: z.boolean(),
}).strict();

export type WorkOrder = z.infer<typeof workOrderSchema>;
export type WorkOrderPriority = z.infer<typeof workOrderPrioritySchema>;
export type WorkOrderStatus = z.infer<typeof workOrderStatusSchema>;
export type WorkOrderOperation = z.infer<typeof workOrderOperationSchema>;
export type WorkOrderSourceDomain = z.infer<typeof workOrderSourceDomainSchema>;
export type WorkOrderSourceReference = z.infer<typeof sourceReferenceSchema>;
export type WorkOrderEvidenceReference = z.infer<typeof evidenceReferenceSchema>;
export type WorkOrderList = z.infer<typeof workOrderListSchema>;

export class WorkOrderApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(status: number, code: string, message: string, retryable = false) {
    super(message);
    this.name = 'WorkOrderApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export type WorkOrderListFilter = {
  status?: WorkOrderStatus;
  priority?: WorkOrderPriority;
  assigneeId?: string;
  sourceDomain?: WorkOrderSourceDomain;
  sourceRef?: string;
  cursor?: string;
  limit?: number;
};

export type WorkOrderRequestOptions = {
  siteId: string;
  csrfToken?: string;
  idempotencyKey?: string;
  signal?: AbortSignal;
};

export type CreateWorkOrderInput = {
  title: string;
  description: string;
  priority: WorkOrderPriority;
  sourceReferences: WorkOrderSourceReference[];
  assigneeId: string | null;
  teamId: string | null;
  scheduledStart: string | null;
  dueAt: string | null;
};

export type AssignWorkOrderInput = {
  expectedVersion: number;
  assigneeId: string | null;
  teamId: string | null;
  reason: string;
};

export type WorkOrderLifecycleInput = {
  expectedVersion: number;
  reason: string;
  scheduledStart?: string | null;
  dueAt?: string | null;
  completionEvidence?: WorkOrderEvidenceReference[];
};

const HAS_CHINESE_RE = /[\u4e00-\u9fff]/;

const WORK_ORDER_CODE_MESSAGES: Readonly<Record<string, string>> = {
  AUTHENTICATION_REQUIRED: '登录已失效，请重新登录。',
  SESSION_INVALID: '登录已失效，请重新登录。',
  CSRF_REQUIRED: '安全校验未通过，请刷新页面后再试。',
  CSRF_INVALID: '安全校验未通过，请刷新页面后再试。',
  FORBIDDEN: '当前账号没有这项工单操作权限，请联系管理员开通。',
  WORK_ORDER_NOT_FOUND: '找不到这条工单，可能已被更新或删除。',
  RESOURCE_NOT_FOUND: '找不到这条工单，可能已被更新或删除。',
  WORK_ORDER_VERSION_CONFLICT: '这条工单刚刚被其他人更新了，请刷新后再操作。',
  CONFLICT: '这条工单刚刚有变动，请刷新后再试。',
  WORK_ORDER_TASKS_INCOMPLETE: '还有未完成的现场检查项，请完成全部检查后再结单。',
  WORK_ORDER_INVALID: '填写的内容不完整或不符合要求，请检查后再试。',
  VALIDATION_FAILED: '填写的内容不完整或不符合要求，请检查后再试。',
};

function translateWorkOrderError(status: number, code: string, detail?: string): string {
  if (detail && HAS_CHINESE_RE.test(detail)) return detail;
  if (WORK_ORDER_CODE_MESSAGES[code]) return WORK_ORDER_CODE_MESSAGES[code];
  if (status === 401) return '登录已失效，请重新登录。';
  if (status === 403) return '当前账号没有这项工单操作权限，请联系管理员开通。';
  if (status === 404) return '找不到这条工单，请刷新列表后再试。';
  if (status === 409) return '这条工单刚刚被其他人更新了，请刷新后再试。';
  if (status === 422 || status === 400) return '填写的内容不符合要求，请检查后再试。';
  if (status === 429) return '操作过于频繁，请稍后再试。';
  return '工单服务暂时不可用，请稍后再试。';
}

function toWorkOrderError(failure: GatewayFailure): WorkOrderApiError {
  if (failure.kind === 'routeMissing') {
    return new WorkOrderApiError(
      failure.status,
      'ROUTE_NOT_FOUND',
      ROUTE_MISSING_MESSAGE,
      false,
    );
  }
  if (failure.kind === 'unavailable') {
    const status = failure.status ?? 503;
    const code = failure.problem?.code ?? 'WORK_ORDER_UNAVAILABLE';
    const message = translateWorkOrderError(status, code, failure.problem?.detail);
    return new WorkOrderApiError(status, code, message, failure.problem?.retryable ?? false);
  }
  const code = failure.problem.code ?? (failure.kind === 'notPublished' ? 'RESOURCE_NOT_FOUND' : 'WORK_ORDER_UNAVAILABLE');
  const message = translateWorkOrderError(failure.problem.status, code, failure.problem.detail);
  return new WorkOrderApiError(failure.problem.status, code, message, failure.problem.retryable ?? false);
}

async function sendWorkOrderRequest<T>(path: string, request: GatewayRequest<T>): Promise<T> {
  const result = await gatewayRequest(path, request);
  if (result.kind === 'ok') return result.value;
  throw toWorkOrderError(result);
}

function workOrderMutationRequest(
  body: unknown,
  options: WorkOrderRequestOptions,
): GatewayRequest<WorkOrder> {
  return {
    method: 'POST',
    csrfToken: options.csrfToken ?? '',
    idempotencyKey: options.idempotencyKey ?? `work-order-${crypto.randomUUID()}`,
    body,
    signal: options.signal,
    parse: (payload) => workOrderSchema.parse(payload),
  };
}

export function listWorkOrders(filter: WorkOrderListFilter, options: WorkOrderRequestOptions): Promise<WorkOrderList> {
  const query = new URLSearchParams();
  if (filter.status) query.set('status', filter.status);
  if (filter.priority) query.set('priority', filter.priority);
  if (filter.assigneeId) query.set('assigneeId', filter.assigneeId);
  if (filter.sourceDomain) query.set('sourceDomain', filter.sourceDomain);
  if (filter.sourceRef) query.set('sourceRef', filter.sourceRef);
  if (filter.cursor) query.set('cursor', filter.cursor);
  query.set('limit', String(filter.limit ?? 50));
  return sendWorkOrderRequest(
    `/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders?${query.toString()}`,
    { signal: options.signal, parse: (payload) => workOrderListSchema.parse(payload) },
  );
}

export function getWorkOrder(workOrderId: string, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return sendWorkOrderRequest(
    `/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}`,
    { signal: options.signal, parse: (payload) => workOrderSchema.parse(payload) },
  );
}

export function createWorkOrder(input: CreateWorkOrderInput, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return sendWorkOrderRequest(
    `/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders`,
    workOrderMutationRequest(input, options),
  );
}

export function assignWorkOrder(workOrderId: string, input: AssignWorkOrderInput, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return sendWorkOrderRequest(
    `/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}:assign`,
    workOrderMutationRequest(input, options),
  );
}

export function transitionWorkOrder(
  workOrderId: string,
  operation: 'plan' | 'start' | 'block' | 'resume' | 'complete' | 'cancel' | 'reopen',
  input: WorkOrderLifecycleInput,
  options: WorkOrderRequestOptions,
): Promise<WorkOrder> {
  return sendWorkOrderRequest(
    `/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}:${operation}`,
    workOrderMutationRequest(input, options),
  );
}

export function workOrderErrorMessage(error: unknown): string {
  return error instanceof WorkOrderApiError ? error.message : '工单服务暂时不可用。';
}
