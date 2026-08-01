import { z } from 'zod';

export const alarmUUIDSchema = z.string().uuid();
export const alarmUUIDv7Schema = alarmUUIDSchema.regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
export const alarmSeveritySchema = z.enum(['INFO', 'WARNING', 'MAJOR', 'CRITICAL']);
export const alarmStatusSchema = z.enum(['OPEN', 'ACKNOWLEDGED', 'SUPPRESSED', 'CLOSED']);
export const alarmSourceTypeSchema = z.enum(['DEVICE_RULE', 'SITE_RULE', 'EXTERNAL']);
export const alarmOperationSchema = z.enum([
  'PUBLISH',
  'ACKNOWLEDGE',
  'ASSIGN',
  'UNASSIGN',
  'SUPPRESS',
  'UNSUPPRESS',
  'CLOSE',
  'REOPEN',
]);

export const alarmEvidenceReferenceSchema = z.object({
  kind: z.string().min(1).max(128),
  reference: z.string().min(1).max(512),
  capturedAt: z.string().datetime({ offset: true }),
}).strict();

export const alarmTransitionSchema = z.object({
  fromStatus: alarmStatusSchema.optional(),
  toStatus: alarmStatusSchema,
  operation: alarmOperationSchema.optional(),
  reason: z.string().min(1).max(256),
  actorType: z.string().min(1).max(64),
  actorId: z.string().min(1).max(256).optional(),
  assigneeId: z.string().min(1).max(256).optional(),
  suppressedUntil: z.string().datetime({ offset: true }).optional(),
  policyRevision: z.string().min(1).max(128).optional(),
  correlationId: z.string().min(1).max(256).optional(),
  occurredAt: z.string().datetime({ offset: true }),
  version: z.number().int().positive(),
}).strict();

function addIssue(context: z.RefinementCtx, message: string): void {
  context.addIssue({ code: z.ZodIssueCode.custom, message });
}

function validateOperationShape(
  transition: z.infer<typeof alarmTransitionSchema>,
  context: z.RefinementCtx,
): void {
  if (!transition.fromStatus || !transition.operation) return;
  const sameStatus = transition.toStatus === transition.fromStatus;
  switch (transition.operation) {
    case 'ACKNOWLEDGE':
      if (transition.fromStatus !== 'OPEN' || transition.toStatus !== 'ACKNOWLEDGED' || transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm acknowledgement transition is invalid');
      }
      break;
    case 'ASSIGN':
      if (transition.fromStatus === 'CLOSED' || !sameStatus || !transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm assignment transition is invalid');
      }
      break;
    case 'UNASSIGN':
      if (transition.fromStatus === 'CLOSED' || !sameStatus || transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm unassignment transition is invalid');
      }
      break;
    case 'SUPPRESS': {
      const occurredAt = Date.parse(transition.occurredAt);
      const suppressedUntil = transition.suppressedUntil ? Date.parse(transition.suppressedUntil) : Number.NaN;
      const duration = suppressedUntil - occurredAt;
      if (
        !['OPEN', 'ACKNOWLEDGED'].includes(transition.fromStatus)
        || transition.toStatus !== 'SUPPRESSED'
        || transition.assigneeId
        || !Number.isFinite(suppressedUntil)
        || duration <= 0
        || duration > 30 * 24 * 60 * 60 * 1000
      ) {
        addIssue(context, 'Alarm suppression transition is invalid');
      }
      break;
    }
    case 'UNSUPPRESS':
      if (transition.fromStatus !== 'SUPPRESSED' || !['OPEN', 'ACKNOWLEDGED'].includes(transition.toStatus) || transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm unsuppression transition is invalid');
      }
      break;
    case 'CLOSE':
      if (transition.fromStatus === 'CLOSED' || transition.toStatus !== 'CLOSED' || transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm close transition is invalid');
      }
      break;
    case 'REOPEN':
      if (transition.fromStatus !== 'CLOSED' || transition.toStatus !== 'OPEN' || transition.assigneeId || transition.suppressedUntil) {
        addIssue(context, 'Alarm reopen transition is invalid');
      }
      break;
    case 'PUBLISH':
      addIssue(context, 'Alarm publish operation is only valid for the initial transition');
      break;
  }
}

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
  assigneeId: z.string().min(1).max(256).optional(),
  suppressedUntil: z.string().datetime({ offset: true }).optional(),
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
    addIssue(context, 'Alarm time ordering is invalid');
  }

  let previousStatus: z.infer<typeof alarmStatusSchema> | undefined;
  let previousVersion = 0;
  let projectedAssigneeId: string | undefined;
  let projectedSuppressedUntil: string | undefined;
  let projectedSuppressionReturnStatus: z.infer<typeof alarmStatusSchema> | undefined;
  alarm.transitions.forEach((transition, index) => {
    if (transition.version <= previousVersion) addIssue(context, 'Alarm transition versions are not increasing');
    if (index === 0) {
      if (transition.fromStatus !== undefined) addIssue(context, 'Alarm initial transition has a source status');
      if (transition.operation !== undefined && transition.operation !== 'PUBLISH') addIssue(context, 'Alarm initial operation is invalid');
    } else {
      if (transition.fromStatus !== previousStatus) addIssue(context, 'Alarm transition chain is invalid');
      if (!transition.operation || transition.operation === 'PUBLISH') addIssue(context, 'Alarm lifecycle operation is missing');
      if (!transition.actorId || !transition.policyRevision || !transition.correlationId) addIssue(context, 'Alarm transition audit evidence is incomplete');
      validateOperationShape(transition, context);
    }
    if (transition.operation === 'ASSIGN') projectedAssigneeId = transition.assigneeId;
    if (transition.operation === 'UNASSIGN') projectedAssigneeId = undefined;
    if (transition.operation === 'SUPPRESS') {
      projectedSuppressedUntil = transition.suppressedUntil;
      projectedSuppressionReturnStatus = transition.fromStatus;
    }
    if (transition.operation === 'UNSUPPRESS') {
      if (!projectedSuppressionReturnStatus || transition.toStatus !== projectedSuppressionReturnStatus) {
        addIssue(context, 'Alarm unsuppression does not restore the suppressed lifecycle state');
      }
      projectedSuppressedUntil = undefined;
      projectedSuppressionReturnStatus = undefined;
    }
    if (transition.operation === 'CLOSE' || transition.operation === 'REOPEN') {
      projectedSuppressedUntil = undefined;
      projectedSuppressionReturnStatus = undefined;
    }
    previousStatus = transition.toStatus;
    previousVersion = transition.version;
  });
  const latest = alarm.transitions.at(-1);
  if (!latest || latest.toStatus !== alarm.status || latest.version !== alarm.version) {
    addIssue(context, 'Alarm timeline does not converge');
  }
  if (projectedAssigneeId !== alarm.assigneeId || projectedSuppressedUntil !== alarm.suppressedUntil) {
    addIssue(context, 'Alarm lifecycle facts do not converge');
  }
  if (
    (alarm.status === 'SUPPRESSED') !== Boolean(alarm.suppressedUntil)
    || (alarm.status === 'SUPPRESSED') !== Boolean(projectedSuppressionReturnStatus)
  ) {
    addIssue(context, 'Alarm suppression state is inconsistent');
  }
});

export const alarmListResponseSchema = z.object({
  schemaVersion: z.literal(1),
  items: z.array(alarmSchema).max(100),
  nextCursor: alarmUUIDv7Schema.optional().nullable(),
  hasMore: z.boolean(),
}).strict().superRefine((response, context) => {
  if (response.hasMore !== Boolean(response.nextCursor)) {
    addIssue(context, 'Alarm cursor state is inconsistent');
  }
  if (new Set(response.items.map((item) => item.alarmId)).size !== response.items.length) {
    addIssue(context, 'Alarm list contains duplicate identity');
  }
});

export type AlarmSeverity = z.infer<typeof alarmSeveritySchema>;
export type AlarmStatus = z.infer<typeof alarmStatusSchema>;
export type AlarmSourceType = z.infer<typeof alarmSourceTypeSchema>;
export type AlarmOperation = z.infer<typeof alarmOperationSchema>;
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
