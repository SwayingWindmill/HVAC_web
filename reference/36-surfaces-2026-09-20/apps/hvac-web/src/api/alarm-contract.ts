import { z } from 'zod';

export const alarmUUIDSchema = z.string().uuid();
export const alarmUUIDv7Schema = alarmUUIDSchema.regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
export const alarmSeveritySchema = z.enum(['INFO', 'WARNING', 'MINOR', 'MAJOR', 'CRITICAL']);
export const alarmConditionSchema = z.enum(['ACTIVE', 'CLEARED']);
export const alarmSourceTypeSchema = z.enum(['DEVICE_RULE', 'SITE_RULE', 'EXTERNAL']);
export const alarmOperationSchema = z.enum(['PUBLISH', 'ACKNOWLEDGE', 'ASSIGN', 'UNASSIGN', 'SUPPRESS', 'UNSUPPRESS', 'CLEAR']);
export const alarmLinkKindSchema = z.enum(['DEVICE', 'EVENT', 'POINT', 'WORK_ORDER']);

export const alarmEvidenceReferenceSchema = z.object({
  kind: z.string().min(1).max(128),
  reference: z.string().min(1).max(512),
  capturedAt: z.string().datetime({ offset: true }),
}).strict();

export const alarmAcknowledgementSchema = z.object({
  acknowledgedAt: z.string().datetime({ offset: true }),
  acknowledgedBy: z.string().min(1).max(256),
  comment: z.string().max(1000).optional(),
}).strict();

export const alarmSuppressionSchema = z.object({
  startsAt: z.string().datetime({ offset: true }),
  expiresAt: z.string().datetime({ offset: true }),
  reason: z.string().min(1).max(256),
  actorId: z.string().min(1).max(256),
  policyRevision: z.string().min(1).max(128),
}).strict().superRefine((suppression, context) => {
  const duration = Date.parse(suppression.expiresAt) - Date.parse(suppression.startsAt);
  if (duration <= 0 || duration > 30 * 24 * 60 * 60 * 1000) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm suppression interval is invalid' });
  }
});

export const alarmLinkSchema = z.object({
  kind: alarmLinkKindSchema,
  targetId: alarmUUIDv7Schema,
}).strict();

export const alarmTimelineEntrySchema = z.object({
  operation: alarmOperationSchema,
  condition: alarmConditionSchema,
  reason: z.string().max(1000),
  actorType: z.string().min(1).max(64),
  actorId: z.string().min(1).max(256).optional(),
  assigneeId: z.string().min(1).max(256).optional(),
  suppression: alarmSuppressionSchema.optional(),
  currentSeverity: alarmSeveritySchema,
  policyRevision: z.string().min(1).max(128).optional(),
  correlationId: z.string().min(1).max(256),
  occurredAt: z.string().datetime({ offset: true }),
  version: z.number().int().positive(),
}).strict();

export const alarmCursorSchema = z.string().min(1).max(4096);

const severityRank: Record<z.infer<typeof alarmSeveritySchema>, number> = {
  INFO: 1,
  WARNING: 2,
  MINOR: 3,
  MAJOR: 4,
  CRITICAL: 5,
};

export const alarmSchema = z.object({
  schemaVersion: z.literal(2),
  alarmId: alarmUUIDv7Schema,
  tenantId: alarmUUIDv7Schema,
  siteId: alarmUUIDv7Schema,
  deviceId: alarmUUIDv7Schema.optional(),
  eventId: alarmUUIDv7Schema.optional(),
  pointId: alarmUUIDv7Schema.optional(),
  alarmType: z.string().min(1).max(128),
  fingerprint: z.string().regex(/^[a-f0-9]{64}$/),
  incidentCorrelationId: alarmUUIDv7Schema,
  sourceType: alarmSourceTypeSchema,
  sourceReference: z.string().min(1).max(512),
  ruleRevision: z.string().min(1).max(128),
  title: z.string().min(1).max(256),
  summary: z.string().min(1).max(2048),
  condition: alarmConditionSchema,
  currentSeverity: alarmSeveritySchema,
  peakSeverity: alarmSeveritySchema,
  acknowledgement: alarmAcknowledgementSchema.optional(),
  assigneeId: z.string().min(1).max(256).optional(),
  suppression: alarmSuppressionSchema.optional(),
  occurrenceCount: z.number().int().positive(),
  firstOccurredAt: z.string().datetime({ offset: true }),
  lastOccurredAt: z.string().datetime({ offset: true }),
  clearedAt: z.string().datetime({ offset: true }).optional(),
  evidence: z.array(alarmEvidenceReferenceSchema),
  links: z.array(alarmLinkSchema),
  timeline: z.array(alarmTimelineEntrySchema).min(1),
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
  if ((alarm.condition === 'CLEARED') !== Boolean(alarm.clearedAt)) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm clear fact is inconsistent' });
  }
  if (alarm.condition === 'CLEARED' && alarm.suppression) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Cleared Alarm cannot remain suppressed' });
  }
  if (severityRank[alarm.peakSeverity] < severityRank[alarm.currentSeverity]) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm peak severity is below current severity' });
  }
  let previousVersion = 0;
  let previousCondition: AlarmCondition = 'ACTIVE';
  alarm.timeline.forEach((entry, index) => {
    if (entry.version !== previousVersion + 1) {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm timeline versions are not contiguous' });
    }
    if (!entry.actorId || !entry.policyRevision) {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm timeline audit evidence is incomplete' });
    }
    if (index === 0) {
      if (entry.operation !== 'PUBLISH' || entry.condition !== 'ACTIVE') {
        context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm initial timeline entry is invalid' });
      }
    } else if (entry.operation === 'CLEAR') {
      if (previousCondition !== 'ACTIVE' || entry.condition !== 'CLEARED') {
        context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm clear transition is invalid' });
      }
    } else if (entry.operation === 'ACKNOWLEDGE') {
      if (entry.condition !== previousCondition) {
        context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm acknowledgement changed physical condition' });
      }
    } else if (previousCondition !== 'ACTIVE' || entry.condition !== 'ACTIVE') {
      context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm operation changed physical condition' });
    }
    previousVersion = entry.version;
    previousCondition = entry.condition;
  });
  const latest = alarm.timeline.at(-1);
  if (!latest || latest.version !== alarm.version || latest.condition !== alarm.condition || latest.currentSeverity !== alarm.currentSeverity) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: 'Alarm timeline does not converge' });
  }
});

export const alarmListResponseSchema = z.object({
  schemaVersion: z.literal(2),
  items: z.array(alarmSchema).max(200),
  nextCursor: alarmCursorSchema.optional().nullable(),
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
export type AlarmCondition = z.infer<typeof alarmConditionSchema>;
export type AlarmSourceType = z.infer<typeof alarmSourceTypeSchema>;
export type AlarmOperation = z.infer<typeof alarmOperationSchema>;
export type AlarmEvidenceReference = z.infer<typeof alarmEvidenceReferenceSchema>;
export type AlarmAcknowledgement = z.infer<typeof alarmAcknowledgementSchema>;
export type AlarmSuppression = z.infer<typeof alarmSuppressionSchema>;
export type AlarmTimelineEntry = z.infer<typeof alarmTimelineEntrySchema>;
export type AlarmLink = z.infer<typeof alarmLinkSchema>;
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

export function validateAlarmScope(alarm: Alarm, scope: { readonly trustedTenantId: string; readonly trustedSiteId: string }): Alarm {
  const tenantId = alarmUUIDv7Schema.parse(scope.trustedTenantId);
  const siteId = alarmUUIDv7Schema.parse(scope.trustedSiteId);
  if (alarm.tenantId !== tenantId || alarm.siteId !== siteId) {
    throw new AlarmApiError(404, 'RESOURCE_NOT_FOUND', '未找到该 Alarm。');
  }
  return alarm;
}

export function validateAlarmListScope(response: AlarmListResponse, scope: { readonly trustedTenantId: string; readonly trustedSiteId: string }): AlarmListResponse {
  response.items.forEach((alarm) => validateAlarmScope(alarm, scope));
  return response;
}
