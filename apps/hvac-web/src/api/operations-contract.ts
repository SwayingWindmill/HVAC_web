import { z } from 'zod';

const identitySchema = z.string().min(1).max(256);
const projectionRunIdentitySchema = z.string().min(1).max(512);
const timestampSchema = z.number().int().nonnegative();

export const operationsScopeSchema = z.object({
  organizationId: identitySchema,
  siteId: identitySchema,
  equipmentId: identitySchema.nullable(),
  deviceId: identitySchema.nullable(),
}).strict();

const activeRunSchema = z.object({
  id: identitySchema,
  status: z.enum(['ACTIVE', 'PAUSED']),
  startedAt: timestampSchema,
}).strict();

const businessRecordSchema = z.object({
  schemaVersion: z.literal(1),
  recordType: z.enum(['EVIDENCE', 'ANALYSIS_REFERENCE', 'FINDING', 'TOOL_EXECUTION_RECEIPT']),
  id: identitySchema,
  investigationId: identitySchema,
  recordedAt: timestampSchema,
}).passthrough();

export const operationsInvestigationViewSchema = z.object({
  schemaVersion: z.literal(1),
  id: identitySchema,
  scope: operationsScopeSchema,
  status: z.enum(['DRAFT', 'RUNNING', 'PAUSED', 'COMPLETED', 'FAILED', 'CANCELLED']),
  revision: z.number().int().nonnegative(),
  createdAt: timestampSchema,
  activeRun: activeRunSchema.nullable(),
  outcome: z.enum(['SUPPORTED_SITE_FINDING', 'UNABLE_TO_CONCLUDE']).nullable(),
  evidence: z.array(businessRecordSchema).max(32),
  analysisReferences: z.array(businessRecordSchema).max(32),
  findings: z.array(businessRecordSchema).max(32),
  toolReceipts: z.array(businessRecordSchema).max(64),
}).strict();

const investigationProjectionSchema = operationsInvestigationViewSchema.omit({ toolReceipts: true });

export const operationsPlanStepSchema = z.object({
  id: z.enum(['READ_SITE_CONTEXT', 'READ_ENERGY_SERIES', 'ANALYZE', 'COMMIT_RESULT']),
  label: z.string().min(1).max(256),
  status: z.enum(['PENDING', 'IN_PROGRESS', 'PAUSED', 'COMPLETED', 'FAILED', 'CANCELLED']),
}).strict();

export const operationsPlanSchema = z.object({
  schemaVersion: z.literal(1),
  id: z.literal('site-night-energy-investigation'),
  label: z.literal('Site night-energy investigation'),
  completedSteps: z.number().int().min(0).max(4),
  totalSteps: z.literal(4),
  progressPercent: z.number().int().min(0).max(100),
  steps: z.array(operationsPlanStepSchema).length(4),
}).strict();

export const operationsToolActivitySchema = z.object({
  recordId: identitySchema,
  logicalTool: z.enum([
    'registry.getSite',
    'registry.listSiteEquipment',
    'telemetry.getCurrentSnapshot',
    'analytics.getEnergySeries',
    'commands.getCapabilities',
  ]),
  owner: z.enum(['registry', 'telemetry-query-service', 'command-service']),
  resultCategory: z.enum(['SUCCEEDED', 'REJECTED', 'TIMED_OUT', 'FAILED']),
  startedAt: timestampSchema,
  completedAt: timestampSchema,
}).strict();

export const operationsInvestigationStateSnapshotSchema = z.object({
  schemaVersion: z.literal('operations-investigation-ui/v1'),
  investigation: investigationProjectionSchema,
  plan: operationsPlanSchema,
  toolActivities: z.array(operationsToolActivitySchema).max(64),
}).strict();

const runStartedSchema = z.object({
  type: z.literal('RUN_STARTED'),
  threadId: identitySchema,
  runId: projectionRunIdentitySchema,
}).strict();
const stateSnapshotSchema = z.object({
  type: z.literal('STATE_SNAPSHOT'),
  snapshot: operationsInvestigationStateSnapshotSchema,
}).strict();
const toolCallStartSchema = z.object({
  type: z.literal('TOOL_CALL_START'),
  toolCallId: identitySchema,
  toolCallName: operationsToolActivitySchema.shape.logicalTool,
}).strict();
const toolCallArgsSchema = z.object({
  type: z.literal('TOOL_CALL_ARGS'),
  toolCallId: identitySchema,
  delta: z.string().min(2).max(4096),
}).strict();
const toolCallEndSchema = z.object({
  type: z.literal('TOOL_CALL_END'),
  toolCallId: identitySchema,
}).strict();
const runFinishedSchema = z.object({
  type: z.literal('RUN_FINISHED'),
  threadId: identitySchema,
  runId: projectionRunIdentitySchema,
  outcome: z.object({ type: z.literal('success') }).strict(),
}).strict();

export const operationsAgUiEventSchema = z.discriminatedUnion('type', [
  runStartedSchema,
  stateSnapshotSchema,
  toolCallStartSchema,
  toolCallArgsSchema,
  toolCallEndSchema,
  runFinishedSchema,
]);

export type OperationsInvestigationView = z.infer<typeof operationsInvestigationViewSchema>;
export type OperationsInvestigationStateSnapshot = z.infer<typeof operationsInvestigationStateSnapshotSchema>;
export type OperationsAgUiEvent = z.infer<typeof operationsAgUiEventSchema>;

const forbiddenStreamKeys = new Set([
  'lease', 'leaseHistory', 'checkpoint', 'opaqueState', 'runtimeRevision',
  'providerMessage', 'points', 'rawPrompt', 'toolPayload', 'delegationGrant',
  'authorizationDecision', 'metadata', 'attemptId',
]);

function rejectForbiddenStreamFields(value: unknown): void {
  if (Array.isArray(value)) {
    value.forEach(rejectForbiddenStreamFields);
    return;
  }
  if (typeof value !== 'object' || value === null) return;
  for (const [key, nested] of Object.entries(value)) {
    if (forbiddenStreamKeys.has(key)) {
      throw new Error(`Operations event contains forbidden field ${key}.`);
    }
    rejectForbiddenStreamFields(nested);
  }
}

export interface ParsedOperationsAgUiEvent {
  id: string;
  event: OperationsAgUiEvent;
}

export function parseOperationsAgUiEventStream(raw: string): ParsedOperationsAgUiEvent[] {
  const normalized = raw.replace(/\r\n/gu, '\n');
  if (!normalized.endsWith('\n\n')) throw new Error('Operations event stream is incomplete.');
  const parsed: ParsedOperationsAgUiEvent[] = [];
  let streamRevision: string | undefined;
  for (const block of normalized.split('\n\n')) {
    if (!block) continue;
    let id = '';
    let eventName = '';
    let data = '';
    for (const line of block.split('\n')) {
      if (line.startsWith('id: ') && !id) id = line.slice(4);
      else if (line.startsWith('event: ') && !eventName) eventName = line.slice(7);
      else if (line.startsWith('data: ') && !data) data = line.slice(6);
      else throw new Error('Operations event stream contains an unsupported field.');
    }
    const identity = /^(\d+):(\d+)$/u.exec(id);
    if (!identity || !eventName || !data) {
      throw new Error('Operations event stream block is incomplete.');
    }
    const [revision, sequence] = identity.slice(1);
    if (Number(sequence) !== parsed.length || (streamRevision !== undefined && revision !== streamRevision)) {
      throw new Error('Operations event stream identity is invalid.');
    }
    streamRevision ??= revision;
    const candidate: unknown = JSON.parse(data);
    rejectForbiddenStreamFields(candidate);
    const event = operationsAgUiEventSchema.parse(candidate);
    if (event.type !== eventName) throw new Error('Operations event name does not match its payload.');
    if (event.type === 'TOOL_CALL_ARGS') {
      const activity: unknown = JSON.parse(event.delta);
      rejectForbiddenStreamFields(activity);
      operationsToolActivitySchema.parse(activity);
    }
    parsed.push({ id, event });
    if (parsed.length > 256) throw new Error('Operations event stream contains too many events.');
  }
  const startEvent = parsed[0]?.event;
  const snapshotEvent = parsed[1]?.event;
  const finishEvent = parsed.at(-1)?.event;
  if (parsed.length < 3
    || startEvent?.type !== 'RUN_STARTED'
    || snapshotEvent?.type !== 'STATE_SNAPSHOT'
    || finishEvent?.type !== 'RUN_FINISHED') {
    throw new Error('Operations event stream lifecycle is incomplete.');
  }
  if (startEvent.threadId !== finishEvent.threadId || startEvent.runId !== finishEvent.runId) {
    throw new Error('Operations event stream run identity changed.');
  }
  if (String(snapshotEvent.snapshot.investigation.revision) !== streamRevision
    || snapshotEvent.snapshot.investigation.id !== startEvent.threadId) {
    throw new Error('Operations event stream does not match its authoritative snapshot.');
  }
  const toolEvents = parsed.slice(2, -1);
  const activities = snapshotEvent.snapshot.toolActivities;
  if (toolEvents.length % 3 !== 0 || activities.length !== toolEvents.length / 3) {
    throw new Error('Operations Tool events do not match the committed snapshot.');
  }
  activities.forEach((activity, index) => {
    const start = toolEvents[index * 3]?.event;
    const args = toolEvents[index * 3 + 1]?.event;
    const end = toolEvents[index * 3 + 2]?.event;
    if (start?.type !== 'TOOL_CALL_START'
      || args?.type !== 'TOOL_CALL_ARGS'
      || end?.type !== 'TOOL_CALL_END'
      || start.toolCallId !== args.toolCallId
      || start.toolCallId !== end.toolCallId
      || start.toolCallId !== activity.recordId
      || start.toolCallName !== activity.logicalTool) {
      throw new Error('Operations Tool event identity changed.');
    }
    const deltaActivity = operationsToolActivitySchema.parse(JSON.parse(args.delta));
    if (JSON.stringify(deltaActivity) !== JSON.stringify(activity)) {
      throw new Error('Operations Tool event differs from the committed snapshot.');
    }
  });
  return parsed;
}
