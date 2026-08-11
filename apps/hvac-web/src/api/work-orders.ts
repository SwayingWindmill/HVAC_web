import { z } from 'zod';

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
  organizationId: z.string(),
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

const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

export type WorkOrder = z.infer<typeof workOrderSchema>;
export type WorkOrderPriority = z.infer<typeof workOrderPrioritySchema>;
export type WorkOrderStatus = z.infer<typeof workOrderStatusSchema>;
export type WorkOrderOperation = z.infer<typeof workOrderOperationSchema>;
export type WorkOrderSourceReference = z.infer<typeof sourceReferenceSchema>;
export type WorkOrderEvidenceReference = z.infer<typeof evidenceReferenceSchema>;
export type WorkOrderList = z.infer<typeof workOrderListSchema>;

export class WorkOrderApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string, readonly retryable = false) {
    super(message);
    this.name = 'WorkOrderApiError';
  }
}

export type WorkOrderListFilter = {
  status?: WorkOrderStatus;
  priority?: WorkOrderPriority;
  assigneeId?: string;
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

async function request<T>(path: string, schema: z.ZodType<T>, init: RequestInit): Promise<T> {
  const response = await fetch(path, { ...init, credentials: 'same-origin' });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const problem = problemSchema.parse(payload);
    throw new WorkOrderApiError(response.status, problem.code ?? 'WORK_ORDER_UNAVAILABLE', problem.detail ?? problem.title ?? 'Work Order 服务暂时不可用。', problem.retryable ?? false);
  }
  return schema.parse(payload);
}

function mutationHeaders(options: WorkOrderRequestOptions): HeadersInit {
  return {
    Accept: 'application/json, application/problem+json',
    'Content-Type': 'application/json',
    'X-CSRF-Token': options.csrfToken ?? '',
    'Idempotency-Key': options.idempotencyKey ?? `work-order-${crypto.randomUUID()}`,
  };
}

export function listWorkOrders(filter: WorkOrderListFilter, options: WorkOrderRequestOptions): Promise<WorkOrderList> {
  const query = new URLSearchParams();
  if (filter.status) query.set('status', filter.status);
  if (filter.priority) query.set('priority', filter.priority);
  if (filter.assigneeId) query.set('assigneeId', filter.assigneeId);
  if (filter.cursor) query.set('cursor', filter.cursor);
  query.set('limit', String(filter.limit ?? 50));
  return request(`/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders?${query.toString()}`, workOrderListSchema, {
    method: 'GET', signal: options.signal, headers: { Accept: 'application/json, application/problem+json' },
  });
}

export function getWorkOrder(workOrderId: string, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return request(`/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}`, workOrderSchema, {
    method: 'GET', signal: options.signal, headers: { Accept: 'application/json, application/problem+json' },
  });
}

export function createWorkOrder(input: CreateWorkOrderInput, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return request(`/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders`, workOrderSchema, {
    method: 'POST', signal: options.signal, headers: mutationHeaders(options), body: JSON.stringify(input),
  });
}

export function assignWorkOrder(workOrderId: string, input: AssignWorkOrderInput, options: WorkOrderRequestOptions): Promise<WorkOrder> {
  return request(`/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}:assign`, workOrderSchema, {
    method: 'POST', signal: options.signal, headers: mutationHeaders(options), body: JSON.stringify(input),
  });
}

export function transitionWorkOrder(
  workOrderId: string,
  operation: 'plan' | 'start' | 'block' | 'resume' | 'complete' | 'cancel' | 'reopen',
  input: WorkOrderLifecycleInput,
  options: WorkOrderRequestOptions,
): Promise<WorkOrder> {
  return request(`/api/v1/sites/${encodeURIComponent(options.siteId)}/work-orders/${encodeURIComponent(workOrderId)}:${operation}`, workOrderSchema, {
    method: 'POST', signal: options.signal, headers: mutationHeaders(options), body: JSON.stringify(input),
  });
}

export function workOrderErrorMessage(error: unknown): string {
  return error instanceof WorkOrderApiError ? `${error.code} · ${error.message}` : 'Work Order 服务暂时不可用。';
}
