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

const evidenceQualitySchema = z.object({
  classification: z.enum(['GOOD', 'UNCERTAIN', 'BAD', 'STALE']),
  valid: z.number().int().nonnegative(),
  suspect: z.number().int().nonnegative(),
  invalid: z.number().int().nonnegative(),
}).strict();

const evidenceSourceSchema = z.object({
  owner: z.enum(['registry', 'telemetry-query-service']),
  scope: operationsScopeSchema,
  requestId: identitySchema,
  registryRevision: z.string().min(1).max(256).nullable(),
  datasetRevision: z.string().min(1).max(256).nullable(),
  watermark: z.object({
    data: z.string().min(1).max(256).nullable(),
    aggregate: z.string().min(1).max(256).nullable(),
  }).strict(),
  partial: z.boolean(),
  quality: evidenceQualitySchema,
  capturedAt: timestampSchema,
  evaluatedAt: timestampSchema,
  provenanceDigest: z.string().regex(/^sha256:[0-9a-f]{64}$/u),
}).strict();

export const operationsEvidenceSchema = z.object({
  schemaVersion: z.literal(1),
  recordType: z.literal('EVIDENCE'),
  id: identitySchema,
  investigationId: identitySchema,
  recordedAt: timestampSchema,
  evidenceKind: z.enum([
    'SITE_ENERGY_SERIES_READY',
    'SITE_ENERGY_SERIES_READINESS_ASSESSED',
    'SITE_ENERGY_PERIOD_COMPARISON',
  ]),
  classification: z.enum(['FACT', 'ALGORITHM_RESULT']),
  statement: z.string().min(1).max(4_000),
  analysisReferenceDigest: z.string().regex(/^sha256:[0-9a-f]{64}$/u).nullable(),
  sources: z.array(evidenceSourceSchema).min(1).max(8),
}).strict();

export const operationsAnalysisReferenceSchema = z.object({
  schemaVersion: z.literal(1),
  recordType: z.literal('ANALYSIS_REFERENCE'),
  id: identitySchema,
  investigationId: identitySchema,
  recordedAt: timestampSchema,
  analysisKind: z.literal('SITE_NIGHT_ENERGY_COMPARISON'),
  authority: z.literal('DETERMINISTIC_ALGORITHM'),
  algorithmVersion: z.string().min(1).max(128),
  policyVersion: z.string().min(1).max(128),
  inputEvidenceIds: z.array(identitySchema).min(1).max(32),
  parameterDigest: z.string().regex(/^sha256:[0-9a-f]{64}$/u),
  resultDigest: z.string().regex(/^sha256:[0-9a-f]{64}$/u),
  executedAt: timestampSchema,
  outcome: z.enum(['SUPPORTED_SITE_FINDING', 'UNABLE_TO_CONCLUDE']),
}).strict();

const requiredNextPeriodSchema = z.object({
  localDate: z.string().min(1).max(32),
  from: z.string().min(1).max(64),
  to: z.string().min(1).max(64),
  expectedBuckets: z.number().int().min(1).max(48),
}).strict();

const requiredNextCommon = {
  status: z.literal('REQUIRED_NEXT'),
  organizationId: identitySchema,
  siteId: identitySchema,
  equipmentIds: z.array(identitySchema).max(32),
  targetPeriod: requiredNextPeriodSchema,
  baselinePeriod: requiredNextPeriodSchema,
} as const;

export const operationsRequiredNextSchema = z.discriminatedUnion('kind', [
  z.object({
    ...requiredNextCommon,
    kind: z.literal('EQUIPMENT_ENERGY_BINDINGS'),
    owner: z.literal('registry'),
    capability: z.literal('registry.getEquipmentEnergyBindings'),
    requiredMetadata: z.tuple([
      z.literal('BUSINESS_REVISION'),
      z.literal('QUALITY'),
      z.literal('CAPTURED_AT'),
      z.literal('PAYLOAD_DIGEST'),
    ]),
  }).strict(),
  z.object({
    ...requiredNextCommon,
    kind: z.literal('EQUIPMENT_ENERGY_PERIOD_COMPARISON'),
    owner: z.literal('telemetry-query-service'),
    capability: z.literal('analytics.energy.getEquipmentSeries'),
    requiredMetadata: z.tuple([
      z.literal('DATASET_REVISION'),
      z.literal('WATERMARK'),
      z.literal('PARTIAL'),
      z.literal('QUALITY'),
      z.literal('CAPTURED_AT'),
      z.literal('PAYLOAD_DIGEST'),
    ]),
  }).strict(),
]);

const findingConclusionSchema = z.discriminatedUnion('status', [
  z.object({
    status: z.literal('SUPPORTED'),
    scope: z.literal('SITE'),
    organizationId: identitySchema,
    siteId: identitySchema,
  }).strict(),
  z.object({
    status: z.literal('UNABLE_TO_CONCLUDE'),
    scope: z.enum(['SITE', 'EQUIPMENT']),
    reasonCode: z.string().min(1).max(128),
    detail: z.string().min(1).max(4_000),
    requiredNext: z.array(operationsRequiredNextSchema).min(1).max(8).optional(),
  }).strict(),
]);

export const operationsFindingSchema = z.object({
  schemaVersion: z.literal(1),
  recordType: z.literal('FINDING'),
  id: identitySchema,
  investigationId: identitySchema,
  recordedAt: timestampSchema,
  findingKind: z.enum([
    'SITE_NIGHT_ENERGY_INCREASE',
    'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD',
    'UNABLE_TO_CONCLUDE',
  ]),
  classification: z.literal('INFERENCE'),
  statement: z.string().min(1).max(4_000),
  evidenceIds: z.array(identitySchema).max(32),
  analysisReferenceIds: z.array(identitySchema).max(32),
  conclusion: findingConclusionSchema,
}).strict();

const toolReceiptMetadataValueSchema = z.union([
  z.string().max(512),
  z.number(),
  z.boolean(),
  z.null(),
]);

export const operationsToolReceiptSchema = z.object({
  schemaVersion: z.literal(1),
  recordType: z.literal('TOOL_EXECUTION_RECEIPT'),
  id: identitySchema,
  investigationId: identitySchema,
  recordedAt: timestampSchema,
  logicalTool: z.enum([
    'registry.getSite',
    'registry.listSiteEquipment',
    'telemetry.getCurrentSnapshot',
    'analytics.getEnergySeries',
    'commands.getCapabilities',
  ]),
  owner: z.enum(['registry', 'telemetry-query-service', 'command-service']),
  requestId: identitySchema,
  attemptId: identitySchema,
  runId: identitySchema,
  stepId: z.string().min(1).max(128),
  startedAt: timestampSchema,
  completedAt: timestampSchema,
  resultCategory: z.enum(['SUCCEEDED', 'REJECTED', 'TIMED_OUT', 'FAILED']),
  metadata: z.record(z.string(), toolReceiptMetadataValueSchema),
}).strict();

export const operationsInvestigationViewSchema = z.object({
  schemaVersion: z.literal(1),
  id: identitySchema,
  scope: operationsScopeSchema,
  status: z.enum(['DRAFT', 'RUNNING', 'PAUSED', 'COMPLETED', 'FAILED', 'CANCELLED']),
  revision: z.number().int().nonnegative(),
  createdAt: timestampSchema,
  activeRun: activeRunSchema.nullable(),
  outcome: z.enum(['SUPPORTED_SITE_FINDING', 'UNABLE_TO_CONCLUDE']).nullable(),
  evidence: z.array(operationsEvidenceSchema).max(32),
  analysisReferences: z.array(operationsAnalysisReferenceSchema).max(32),
  findings: z.array(operationsFindingSchema).max(32),
  toolReceipts: z.array(operationsToolReceiptSchema).max(64),
}).strict();

export const operationsInvestigationSummarySchema = z.object({
  schemaVersion: z.literal(1),
  id: identitySchema,
  scope: operationsScopeSchema,
  status: operationsInvestigationViewSchema.shape.status,
  revision: z.number().int().nonnegative(),
  createdAt: timestampSchema,
  outcome: operationsInvestigationViewSchema.shape.outcome,
  evidenceCount: z.number().int().nonnegative().max(32),
  analysisReferenceCount: z.number().int().nonnegative().max(32),
  findingCount: z.number().int().nonnegative().max(32),
  toolReceiptCount: z.number().int().nonnegative().max(64),
}).strict();

export const operationsInvestigationListSchema = z.object({
  schemaVersion: z.literal(1),
  investigations: z.array(operationsInvestigationSummarySchema).max(50),
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

export type OperationsEvidence = z.infer<typeof operationsEvidenceSchema>;
export type OperationsAnalysisReference = z.infer<typeof operationsAnalysisReferenceSchema>;
export type OperationsFinding = z.infer<typeof operationsFindingSchema>;
export type OperationsRequiredNext = z.infer<typeof operationsRequiredNextSchema>;
export type OperationsToolReceipt = z.infer<typeof operationsToolReceiptSchema>;
export type OperationsInvestigationView = z.infer<typeof operationsInvestigationViewSchema>;
export type OperationsInvestigationSummary = z.infer<typeof operationsInvestigationSummarySchema>;
export type OperationsInvestigationList = z.infer<typeof operationsInvestigationListSchema>;
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

export type OperationsStreamRecoveryMode = 'FULL_SNAPSHOT' | 'RESUME';
export type OperationsStreamRecoveryReason = 'INITIAL' | 'VALID' | 'UNKNOWN' | 'EXPIRED' | 'CONFLICT';

export interface OperationsAgUiStreamRecovery {
  readonly mode: OperationsStreamRecoveryMode;
  readonly reason: OperationsStreamRecoveryReason;
  readonly snapshotPosition: string;
  readonly latestPosition: string;
  readonly replayFromPosition: string | null;
}

export interface OperationsAgUiStreamBatch {
  readonly events: readonly ParsedOperationsAgUiEvent[];
  readonly recovery: OperationsAgUiStreamRecovery;
}

export function parseOperationsAgUiEventStream(raw: string): ParsedOperationsAgUiEvent[] {
  const normalized = raw.replace(/\r\n/gu, '\n');
  if (!normalized.endsWith('\n\n')) throw new Error('Operations event stream is incomplete.');
  const parsed: ParsedOperationsAgUiEvent[] = [];
  const sequences: number[] = [];
  let streamRevision: string | undefined;
  let previousSequence = -1;
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
    const [revision, sequenceText] = identity.slice(1);
    const sequence = Number(sequenceText);
    if (!Number.isSafeInteger(sequence)
      || sequence <= previousSequence
      || (streamRevision !== undefined && revision !== streamRevision)) {
      throw new Error('Operations event stream identity is invalid.');
    }
    streamRevision ??= revision;
    previousSequence = sequence;
    sequences.push(sequence);
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
  const toolSequences = sequences.slice(2, -1);
  const activities = snapshotEvent.snapshot.toolActivities;
  const expectedLatest = 2 + activities.length * 3;
  if (sequences[0] !== 0 || sequences[1] !== 1 || sequences.at(-1) !== expectedLatest) {
    throw new Error('Operations event stream control positions are invalid.');
  }
  const startActivity = toolSequences.length === 0
    ? activities.length
    : (toolSequences[0] - 2) / 3;
  if (!Number.isInteger(startActivity)
    || startActivity < 0
    || startActivity > activities.length
    || toolEvents.length !== (activities.length - startActivity) * 3) {
    throw new Error('Operations Tool replay suffix does not match the committed snapshot.');
  }
  for (let replayIndex = 0; replayIndex < toolEvents.length / 3; replayIndex += 1) {
    const activityIndex = startActivity + replayIndex;
    const activity = activities[activityIndex];
    const start = toolEvents[replayIndex * 3]?.event;
    const args = toolEvents[replayIndex * 3 + 1]?.event;
    const end = toolEvents[replayIndex * 3 + 2]?.event;
    const expectedStart = 2 + activityIndex * 3;
    if (toolSequences[replayIndex * 3] !== expectedStart
      || toolSequences[replayIndex * 3 + 1] !== expectedStart + 1
      || toolSequences[replayIndex * 3 + 2] !== expectedStart + 2
      || start?.type !== 'TOOL_CALL_START'
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
  }
  return parsed;
}
