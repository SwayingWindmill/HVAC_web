import { z } from 'zod';

export const alarmUUIDSchema = z.string().uuid();
export const alarmUUIDv7Schema = alarmUUIDSchema.regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
export const alarmSeveritySchema = z.enum(['INFO', 'WARNING', 'MAJOR', 'CRITICAL']);
export const alarmStatusSchema = z.enum(['OPEN', 'ACKNOWLEDGED', 'SUPPRESSED', 'CLOSED']);
export const alarmSourceTypeSchema = z.enum(['DEVICE_RULE', 'SITE_RULE', 'EXTERNAL']);

export const alarmEvidenceReferenceSchema = z.object({
  kind: z.string().min(1).max(128),
  reference: z.string().min(1).max(512),
  capturedAt: z.string().datetime({ offset: true }),
}).strict();

export const alarmTransitionSchema = z.object({
  fromStatus: alarmStatusSchema.optional(),
  toStatus: alarmStatusSchema,
  reason: z.string().min(1).max(256),
  actorType: z.string().min(1).max(64),
  actorId: z.string().min(1).max(256).optional(),
  occurredAt: z.string().datetime({ offset: true }),
  version: z.number().int().positive(),
}).strict();

export const alarmSchema = z.object({
  schemaVersion: z.literal(1),
  alarmId: alarmUUIDv7Schema,
  organizationId: alarmUUIDv7Schema,
  siteId: alarmUUIDv7Schema,
  deviceId: alarmUUIDv7Schema.optional(),
  sourceType: alarmSourceTypeSchema,
  sourceReference: z.string().min(1).max(512),
  title: z.string().min(1).max(256),
  summary: z.string().min(1).max(2048),
  severity: alarmSeveritySchema,
  status: alarmStatusSchema,
  occurrenceCount: z.number().int().positive(),
  firstOccurredAt: z.string().datetime({ offset: true }),
  lastOccurredAt: z.string().datetime({ offset: true }),
  evidence: z.array(alarmEvidenceReferenceSchema).max(256),
  transitions: z.array(alarmTransitionSchema).min(1).max(256),
  version: z.number().int().positive(),
  createdAt: z.string().datetime({ offset: true }),
  updatedAt: z.string().datetime({ offset: true }),
}).strict().superRefine((alarm, context) => {
  const first = Date.parse(alarm.firstOccurredAt);
  const last = Date.parse(alarm.lastOccurredAt);
  const created = Date.parse(alarm.createdAt);
  const updated = Date.parse(alarm.updatedAt);
  if (last < first || updated < created || updated < last) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm time ordering is invalid' });
  }
  let previousStatus: z.infer<typeof alarmStatusSchema> | undefined;
  let previousVersion = 0;
  alarm.transitions.forEach((transition, index) => {
    if (transition.version <= previousVersion) {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm transition versions are not increasing' });
    }
    if (index === 0 && transition.fromStatus !== undefined) {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm initial transition has a source status' });
    }
    if (index > 0 && transition.fromStatus !== previousStatus) {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm transition chain is invalid' });
    }
    previousStatus = transition.toStatus;
    previousVersion = transition.version;
  });
  const latest = alarm.transitions.at(-1);
  if (!latest || latest.toStatus !== alarm.status || latest.version !== alarm.version) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm timeline does not converge' });
  }
});

export const alarmListResponseSchema = z.object({
  schemaVersion: z.literal(1),
  items: z.array(alarmSchema).max(100),
  nextCursor: alarmUUIDv7Schema.optional().nullable(),
  hasMore: z.boolean(),
}).strict().superRefine((response, context) => {
  if (response.hasMore !== Boolean(response.nextCursor)) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm cursor state is inconsistent' });
  }
  if (new Set(response.items.map((item) => item.alarmId)).size !== response.items.length) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm list contains duplicate identity' });
  }
});

export type AlarmSeverity = z.infer<typeof alarmSeveritySchema>;
export type AlarmStatus = z.infer<typeof alarmStatusSchema>;
export type AlarmSourceType = z.infer<typeof alarmSourceTypeSchema>;
export type AlarmEvidenceReference = z.infer<typeof alarmEvidenceReferenceSchema>;
export type AlarmTransition = z.infer<typeof alarmTransitionSchema>;
export type Alarm = z.infer<typeof alarmSchema>;
export type AlarmListResponse = z.infer<typeof alarmListResponseSchema>;

export class AlarmApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(status: number, code: string, message: string, retryable = false) {
    super(message);
    this.name = 'AlarmApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export function validateAlarmScope(
  alarm: Alarm,
  scope: { readonly trustedOrganizationId: string; readonly trustedSiteId: string },
): Alarm {
  const organizationId = alarmUUIDv7Schema.parse(scope.trustedOrganizationId);
  const siteId = alarmUUIDv7Schema.parse(scope.trustedSiteId);
  if (alarm.organizationId !== organizationId || alarm.siteId !== siteId) {
    throw new AlarmApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Alarm。');
  }
  return alarm;
}

export function validateAlarmListScope(
  response: AlarmListResponse,
  scope: { readonly trustedOrganizationId: string; readonly trustedSiteId: string },
): AlarmListResponse {
  response.items.forEach((alarm) => validateAlarmScope(alarm, scope));
  return response;
}
