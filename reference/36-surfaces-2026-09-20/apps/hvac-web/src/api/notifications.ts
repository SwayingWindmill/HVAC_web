import { z } from 'zod';
import { alarmSeveritySchema } from './alarm-contract';

export const notificationSourceActionSchema = z.enum([
  'CREATED',
  'SEVERITY_CHANGED',
  'ACKNOWLEDGED',
  'CLEARED',
]);
export const notificationInboxStatusSchema = z.enum(['UNREAD', 'READ', 'ACKED']);

export const notificationInboxItemSchema = z.object({
  inboxItemId: z.string().uuid(),
  intentId: z.string().uuid(),
  tenantId: z.string().uuid(),
  siteId: z.string().uuid(),
  principalId: z.string().min(1),
  alarmId: z.string().uuid(),
  incidentCorrelationId: z.string().uuid(),
  sourceAction: notificationSourceActionSchema,
  severity: alarmSeveritySchema,
  subject: z.string().min(1),
  body: z.string(),
  status: notificationInboxStatusSchema,
  createdAt: z.string().datetime({ offset: true }),
  readAt: z.string().datetime({ offset: true }).optional(),
}).strict();

const notificationListResponseSchema = z.object({
  data: z.array(notificationInboxItemSchema),
  meta: z.object({
    requestId: z.string().min(1),
    count: z.number().int().nonnegative(),
  }).strict(),
}).strict();

const notificationMutationResponseSchema = z.object({
  data: notificationInboxItemSchema,
  meta: z.object({ requestId: z.string().min(1) }).strict(),
}).strict();

const problemSchema = z.object({
  code: z.string().optional(),
  title: z.string().optional(),
  detail: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

export type NotificationInboxItem = z.infer<typeof notificationInboxItemSchema>;

export class NotificationApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryable = false,
  ) {
    super(message);
    this.name = 'NotificationApiError';
  }
}

async function parseResponse<T>(response: Response, schema: z.ZodType<T>): Promise<T> {
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const parsed = problemSchema.safeParse(payload);
    const problem = parsed.success ? parsed.data : {};
    throw new NotificationApiError(
      response.status,
      problem.code ?? 'NOTIFICATION_UNAVAILABLE',
      problem.detail ?? problem.title ?? '通知服务暂时不可用。',
      problem.retryable ?? false,
    );
  }
  return schema.parse(payload);
}

export async function listNotifications(signal?: AbortSignal): Promise<NotificationInboxItem[]> {
  const response = await fetch('/api/v1/notifications/inbox?limit=100', {
    method: 'GET',
    credentials: 'same-origin',
    headers: { Accept: 'application/json, application/problem+json' },
    signal,
  });
  return (await parseResponse(response, notificationListResponseSchema)).data;
}

export async function markNotificationRead(
  notificationId: string,
  csrfToken: string,
  signal?: AbortSignal,
): Promise<NotificationInboxItem> {
  const response = await fetch(`/api/v1/notifications/inbox/${encodeURIComponent(notificationId)}/read`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json, application/problem+json',
      'X-CSRF-Token': csrfToken,
    },
    signal,
  });
  return (await parseResponse(response, notificationMutationResponseSchema)).data;
}

export function notificationErrorMessage(error: unknown): string {
  return error instanceof NotificationApiError
    ? `${error.code} · ${error.message}`
    : '通知服务暂时不可用。';
}
