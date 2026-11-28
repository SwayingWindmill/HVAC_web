import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import process from 'node:process';

interface OpenApiSchemaNode {
  readonly enum?: readonly string[];
  readonly const?: unknown;
  readonly minItems?: number;
  readonly maxItems?: number;
  readonly minLength?: number;
  readonly maxLength?: number;
  readonly items?: OpenApiSchemaNode;
  readonly properties?: Readonly<Record<string, OpenApiSchemaNode>>;
}

interface OpenApiSpec {
  readonly openapi?: string;
  readonly paths?: Readonly<Record<string, Readonly<Record<string, { readonly operationId?: string } | undefined>> | undefined>>;
  readonly components?: {
    readonly schemas?: Readonly<Record<string, OpenApiSchemaNode | undefined>>;
  };
}

interface TargetSpec {
  readonly label: string;
  readonly path: string;
  readonly content: string;
}

const root = resolve(import.meta.dirname, '..');
const contractPath = resolve(root, 'contracts/http/operations-agent-sessions-public.openapi.json');
const checkOnly = process.argv.includes('--check');
const source = await readFile(contractPath, 'utf8');
const contract = JSON.parse(source) as OpenApiSpec;
const digest = createHash('sha256').update(source.replace(/\r\n?/g, '\n')).digest('hex');

const requiredOperations = new Map<string, readonly [string, string]>([
  ['listAgentSessions', ['get', '/api/v1/sites/{siteId}/operations/agent-sessions']],
  ['createAgentSession', ['post', '/api/v1/sites/{siteId}/operations/agent-sessions']],
  ['getAgentSession', ['get', '/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}']],
  ['streamAgentSessionEvents', ['get', '/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}/events']],
  ['startAgentSessionRun', ['post', '/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:run']],
  ['cancelAgentSession', ['post', '/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:cancel']],
  ['submitAgentSessionInput', ['post', '/api/v1/sites/{siteId}/operations/agent-sessions/{sessionId}:submit-input']],
  ['getAgentUsage', ['get', '/api/v1/sites/{siteId}/operations/agent-usage']],
]);

const found = new Map<string, readonly [string, string]>();
for (const [path, item] of Object.entries(contract.paths ?? {})) {
  for (const method of ['get', 'post'] as const) {
    const operationId = item?.[method]?.operationId;
    if (operationId) found.set(operationId, [method, path]);
  }
}

if (contract.openapi !== '3.1.0') throw new Error('Agent Session OpenAPI must be 3.1.0.');
for (const [operationId, expected] of requiredOperations) {
  if (JSON.stringify(found.get(operationId)) !== JSON.stringify(expected)) {
    throw new Error(`Agent Session OpenAPI operation ${operationId} is missing or moved.`);
  }
}

const requireSchema = (name: string): OpenApiSchemaNode => {
  const schema = contract.components?.schemas?.[name];
  if (!schema) throw new Error(`Missing component schema ${name} in OpenAPI spec.`);
  return schema;
};

const requireProp = (schema: OpenApiSchemaNode, name: string, label: string): OpenApiSchemaNode => {
  const prop = schema.properties?.[name];
  if (!prop) throw new Error(`Missing property ${name} in ${label}.`);
  return prop;
};

const requireEnum = (schema: OpenApiSchemaNode, label: string): readonly string[] => {
  if (!Array.isArray(schema.enum) || schema.enum.length === 0 || schema.enum.some((v) => typeof v !== 'string')) {
    throw new Error(`Missing string enum in ${label}.`);
  }
  return schema.enum;
};

const requireInt = (value: unknown, label: string): number => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Invalid integer bound for ${label}.`);
  }
  return value;
};

const agentSessionSchema = requireSchema('AgentSession');
const agentRunSchema = requireSchema('AgentRun');
const agentMessageSchema = requireSchema('AgentMessage');
const agentEvidenceRefSchema = requireSchema('AgentEvidenceRef');
const agentToolExecutionSchema = requireSchema('AgentToolExecution');
const investigationCompleteSchema = requireSchema('InvestigationComplete');
const findingChartSchema = requireSchema('FindingChart');
const snapshotSchema = requireSchema('AgentSessionSnapshot');
const sessionListSchema = requireSchema('AgentSessionList');
const eventSchema = requireSchema('AgentEvent');

const eventVersion = requireProp(eventSchema, 'version', 'AgentEvent').const;
if (eventVersion !== 'hvac.agent.event/v1') {
  throw new Error('Agent Session OpenAPI event version must be hvac.agent.event/v1.');
}
const eventTypes = requireEnum(requireProp(eventSchema, 'type', 'AgentEvent'), 'AgentEvent.type');
const expectedEventTypes = [
  'session.snapshot', 'run.started', 'assistant.delta', 'tool.started', 'tool.completed',
  'artifact.created', 'input.required', 'run.completed', 'run.failed',
];
if (JSON.stringify(eventTypes) !== JSON.stringify(expectedEventTypes)) {
  throw new Error('Agent Session OpenAPI event vocabulary does not match hvac.agent.event/v1.');
}

const sessionStatuses = requireEnum(requireProp(agentSessionSchema, 'status', 'AgentSession'), 'AgentSession.status');
const runStatuses = requireEnum(requireProp(agentRunSchema, 'status', 'AgentRun'), 'AgentRun.status');
const messageRoles = requireEnum(requireProp(agentMessageSchema, 'role', 'AgentMessage'), 'AgentMessage.role');
const toolExecutionStatuses = requireEnum(requireProp(agentToolExecutionSchema, 'status', 'AgentToolExecution'), 'AgentToolExecution.status');
const evidenceOwners = requireEnum(requireProp(agentEvidenceRefSchema, 'owner', 'AgentEvidenceRef'), 'AgentEvidenceRef.owner');
const investigationOutcomes = requireEnum(requireProp(investigationCompleteSchema, 'outcome', 'InvestigationComplete'), 'InvestigationComplete.outcome');
const findingChartKinds = requireEnum(requireProp(findingChartSchema, 'kind', 'FindingChart'), 'FindingChart.kind');

const maxChartCategories = requireInt(requireProp(findingChartSchema, 'categories', 'FindingChart').maxItems, 'FindingChart.categories.maxItems');
const maxChartSeries = requireInt(requireProp(findingChartSchema, 'series', 'FindingChart').maxItems, 'FindingChart.series.maxItems');
const maxKeyFigures = requireInt(requireProp(investigationCompleteSchema, 'keyFigures', 'InvestigationComplete').maxItems, 'InvestigationComplete.keyFigures.maxItems');
const maxEvidenceRefs = requireInt(requireProp(investigationCompleteSchema, 'evidenceRefs', 'InvestigationComplete').maxItems, 'InvestigationComplete.evidenceRefs.maxItems');
const maxFindingListItems = requireInt(requireProp(investigationCompleteSchema, 'limitations', 'InvestigationComplete').maxItems, 'InvestigationComplete.limitations.maxItems');
const maxFindingListItemLength = requireInt(requireProp(investigationCompleteSchema, 'limitations', 'InvestigationComplete').items?.maxLength, 'InvestigationComplete.limitations.items.maxLength');
const maxEvidenceRefFieldLength = requireInt(requireProp(agentEvidenceRefSchema, 'resourceId', 'AgentEvidenceRef').maxLength, 'AgentEvidenceRef.resourceId.maxLength');
const maxSessionList = requireInt(sessionListSchema.maxItems, 'AgentSessionList.maxItems');
const maxSnapshotRuns = requireInt(requireProp(snapshotSchema, 'runs', 'AgentSessionSnapshot').maxItems, 'AgentSessionSnapshot.runs.maxItems');
const maxSnapshotMessages = requireInt(requireProp(snapshotSchema, 'messages', 'AgentSessionSnapshot').maxItems, 'AgentSessionSnapshot.messages.maxItems');
const maxSnapshotToolExecutions = requireInt(requireProp(snapshotSchema, 'toolExecutions', 'AgentSessionSnapshot').maxItems, 'AgentSessionSnapshot.toolExecutions.maxItems');
const maxSnapshotArtifacts = requireInt(requireProp(snapshotSchema, 'artifacts', 'AgentSessionSnapshot').maxItems, 'AgentSessionSnapshot.artifacts.maxItems');

const collectionPath = found.get('listAgentSessions')![1];
const detailPath = found.get('getAgentSession')![1];
const eventsPath = found.get('streamAgentSessionEvents')![1];
const runPath = found.get('startAgentSessionRun')![1];
const cancelPath = found.get('cancelAgentSession')![1];
const submitInputPath = found.get('submitAgentSessionInput')![1];
const usagePath = found.get('getAgentUsage')![1];

const zodEnumLiteral = (values: readonly string[]): string => `[${values.map((v) => `'${v}'`).join(', ')}]`;
const goQuoteList = (values: readonly string[]): string => values.map((v) => `"${v}"`).join(', ');

const webOutput = `// Code generated by scripts/generate-operations-agent-session-contracts.ts; DO NOT EDIT.
// Contract SHA-256: ${digest}

import { z } from 'zod';

export const HVAC_AGENT_EVENT_VERSION = '${eventVersion}' as const;
export const agentSessionEventTypes = ${JSON.stringify(eventTypes)} as const;

const idSchema = z.string().trim().min(1).max(${maxEvidenceRefFieldLength});
const timestampSchema = z.number().int().nonnegative();
const nullableIdSchema = idSchema.nullable();

export const agentSessionPaths = {
  collection: (siteId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions\`,
  detail: (siteId: string, sessionId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions/\${encodeURIComponent(sessionId)}\`,
  events: (siteId: string, sessionId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions/\${encodeURIComponent(sessionId)}/events\`,
  run: (siteId: string, sessionId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions/\${encodeURIComponent(sessionId)}:run\`,
  cancel: (siteId: string, sessionId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions/\${encodeURIComponent(sessionId)}:cancel\`,
  submitInput: (siteId: string, sessionId: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-sessions/\${encodeURIComponent(sessionId)}:submit-input\`,
  usage: (siteId: string, timezone: string) => \`/api/v1/sites/\${encodeURIComponent(siteId)}/operations/agent-usage?timezone=\${encodeURIComponent(timezone)}\`,
} as const;

export const agentModelRefSchema = z.object({ provider: idSchema, model: idSchema }).strict();
export const agentRunUsageSchema = z.object({
  inputTokens: z.number().int().nonnegative(), outputTokens: z.number().int().nonnegative(),
  modelCalls: z.number().int().nonnegative(), toolCalls: z.number().int().nonnegative(),
}).strict();
export const agentSessionSchema = z.object({
  id: idSchema, tenantId: idSchema, siteId: idSchema, agentDefinitionId: idSchema, createdBy: idSchema,
  revision: z.number().int().nonnegative(), createdAt: timestampSchema, updatedAt: timestampSchema,
  status: z.enum(${zodEnumLiteral(sessionStatuses)}),
  activeRunId: nullableIdSchema,
}).strict().superRefine((session, context) => {
  if (session.status === 'ACTIVE' && session.activeRunId === null) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ['activeRunId'], message: 'ACTIVE Session requires activeRunId.' });
  }
  if (session.status !== 'ACTIVE' && session.activeRunId !== null) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ['activeRunId'], message: 'Inactive Session cannot expose activeRunId.' });
  }
});
export const agentRunSchema = z.object({
  id: idSchema, sessionId: idSchema, modelRef: agentModelRefSchema,
  status: z.enum(${zodEnumLiteral(runStatuses)}),
  startedAt: timestampSchema, finishedAt: timestampSchema.nullable(), usage: agentRunUsageSchema, failureCode: idSchema.nullable(),
}).strict();
export const agentMessageSchema = z.object({
  id: idSchema, sessionId: idSchema, runId: nullableIdSchema, role: z.enum(${zodEnumLiteral(messageRoles)}),
  content: z.string().min(1).max(4000), createdAt: timestampSchema,
}).strict();
export const agentEvidenceRefSchema = z.object({
  owner: z.enum(${zodEnumLiteral(evidenceOwners)}),
  resourceType: idSchema, resourceId: idSchema, revision: idSchema.optional(), toolExecutionId: idSchema,
}).strict();
export const agentToolExecutionSchema = z.object({
  id: idSchema, sessionId: idSchema, runId: idSchema, toolName: z.string().min(1).max(512),
  argumentsDigest: z.string().min(1).max(512), status: z.enum(${zodEnumLiteral(toolExecutionStatuses)}),
  startedAt: timestampSchema, finishedAt: timestampSchema.nullable(), resultSummary: z.string().max(4000).nullable(),
  provenance: z.array(agentEvidenceRefSchema).max(${maxEvidenceRefs}), failureCode: idSchema.nullable(),
}).strict();
const inputChoiceSchema = z.object({ value: idSchema, label: z.string().min(1).max(256) }).strict();
export const agentInputResponseSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('TEXT'), maxLength: z.number().int().min(1).max(4000) }).strict(),
  z.object({ kind: z.literal('SINGLE_SELECT'), choices: z.array(inputChoiceSchema).min(1).max(32) }).strict(),
]);
export const findingChartSchema = z.object({
  kind: z.enum(${zodEnumLiteral(findingChartKinds)}), title: z.string().min(1).max(64), unit: z.string().max(16),
  categories: z.array(z.string().min(1).max(32)).min(1).max(${maxChartCategories}),
  series: z.array(z.object({ name: z.string().min(1).max(32), values: z.array(z.number().nullable()).max(${maxChartCategories}) }).strict()).min(1).max(${maxChartSeries}),
}).strict().refine((chart) => chart.series.every((series) => series.values.length === chart.categories.length), 'Each chart series has one value per category.');
export const investigationCompleteSchema = z.object({
  outcome: z.enum(${zodEnumLiteral(investigationOutcomes)}),
  keyFigures: z.array(z.object({ label: z.string().min(1).max(32), value: z.string().min(1).max(24), unit: z.string().min(1).max(16).optional() }).strict()).max(${maxKeyFigures}).optional(),
  evidenceRefs: z.array(agentEvidenceRefSchema).max(${maxEvidenceRefs}), limitations: z.array(z.string().min(1).max(${maxFindingListItemLength})).max(${maxFindingListItems}),
  recommendedNext: z.array(z.string().min(1).max(${maxFindingListItemLength})).max(${maxFindingListItems}), chart: findingChartSchema.optional(),
}).strict();
const artifactBase = { id: idSchema, sessionId: idSchema, runId: idSchema, createdAt: timestampSchema } as const;
export const agentArtifactSchema = z.discriminatedUnion('kind', [
  z.object({ ...artifactBase, kind: z.literal('EVIDENCE_REF'), reference: agentEvidenceRefSchema }).strict(),
  z.object({ ...artifactBase, kind: z.literal('FINDING'), finding: investigationCompleteSchema }).strict(),
  z.object({ ...artifactBase, kind: z.literal('PROPOSAL'), proposalType: idSchema, summary: z.string().min(1).max(4000) }).strict(),
  z.object({ ...artifactBase, kind: z.literal('INPUT_REQUEST'), request: z.object({ prompt: z.string().min(1).max(4000), response: agentInputResponseSchema }).strict() }).strict(),
  z.object({ ...artifactBase, kind: z.literal('INPUT_RESPONSE'), requestArtifactId: idSchema, value: z.string().min(1).max(4000), submittedBy: idSchema }).strict(),
  z.object({ ...artifactBase, kind: z.literal('LIMITATION'), description: z.string().min(1).max(4000) }).strict(),
]);
export const agentSessionSnapshotSchema = z.object({
  session: agentSessionSchema, runs: z.array(agentRunSchema).max(${maxSnapshotRuns}), messages: z.array(agentMessageSchema).max(${maxSnapshotMessages}),
  toolExecutions: z.array(agentToolExecutionSchema).max(${maxSnapshotToolExecutions}), artifacts: z.array(agentArtifactSchema).max(${maxSnapshotArtifacts}),
}).strict();
export const agentSessionListSchema = z.array(agentSessionSnapshotSchema).max(${maxSessionList});
export const agentSessionMessageRequestSchema = z.object({ message: z.string().trim().min(1).max(4000) }).strict();
export const agentSessionRunRequestSchema = z.object({ expectedRevision: z.number().int().nonnegative(), message: z.string().trim().min(1).max(4000) }).strict();
export const agentSessionRevisionRequestSchema = z.object({ expectedRevision: z.number().int().nonnegative() }).strict();
export const agentSessionInputRequestSchema = z.object({ expectedRevision: z.number().int().nonnegative(), requestArtifactId: idSchema, value: z.string().trim().min(1).max(4000) }).strict();

const countSchema = z.number().int().nonnegative();
export const agentToolFailureReasonSchema = z.object({ code: z.string().min(1).max(64), count: z.number().int().positive() }).strict();
export const agentToolUsageSchema = z.object({ toolName: z.string().min(1).max(512), calls: countSchema, failed: countSchema, failureReasons: z.array(agentToolFailureReasonSchema).max(32) }).strict();
export const agentUsageDaySchema = z.object({
  date: z.string().regex(/^\\d{4}-\\d{2}-\\d{2}$/), completed: countSchema, failed: countSchema, cancelled: countSchema,
  averageDurationMs: countSchema.nullable(), inputTokens: countSchema, outputTokens: countSchema, tools: z.array(agentToolUsageSchema).max(64),
}).strict();
export const agentUsageSchema = z.object({ schemaVersion: z.literal(1), timezone: z.string().min(1).max(64), days: z.array(agentUsageDaySchema).length(14) }).strict();

const eventBase = {
  version: z.literal(HVAC_AGENT_EVENT_VERSION), sessionId: idSchema, runId: nullableIdSchema, sequence: z.number().int().nonnegative(), at: timestampSchema,
} as const;
export const agentSessionEventSchema = z.discriminatedUnion('type', [
  z.object({ ...eventBase, type: z.literal('session.snapshot'), payload: z.object({ snapshot: agentSessionSnapshotSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('run.started'), payload: z.object({ run: agentRunSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('assistant.delta'), payload: z.object({ messageId: idSchema, delta: z.string().min(1).max(16384) }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('tool.started'), payload: z.object({ toolExecutionId: idSchema, toolName: idSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('tool.completed'), payload: z.object({ toolExecution: agentToolExecutionSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('artifact.created'), payload: z.object({ artifact: agentArtifactSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('input.required'), payload: z.object({ artifact: agentArtifactSchema.refine((artifact) => artifact.kind === 'INPUT_REQUEST', 'input.required must carry INPUT_REQUEST') }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('run.completed'), payload: z.object({ run: agentRunSchema }).strict() }).strict(),
  z.object({ ...eventBase, type: z.literal('run.failed'), payload: z.object({ run: agentRunSchema }).strict() }).strict(),
]);

export type AgentSessionSnapshot = z.infer<typeof agentSessionSnapshotSchema>;
export type AgentSessionEvent = z.infer<typeof agentSessionEventSchema>;
export type AgentArtifact = z.infer<typeof agentArtifactSchema>;
export type AgentInputRequestArtifact = Extract<AgentArtifact, { kind: 'INPUT_REQUEST' }>;
export type AgentFindingArtifact = Extract<AgentArtifact, { kind: 'FINDING' }>;
export type FindingChart = z.infer<typeof findingChartSchema>;
export type AgentSessionMessageRequest = z.infer<typeof agentSessionMessageRequestSchema>;
export type AgentSessionRunRequest = z.infer<typeof agentSessionRunRequestSchema>;
export type AgentSessionRevisionRequest = z.infer<typeof agentSessionRevisionRequestSchema>;
export type AgentSessionInputRequest = z.infer<typeof agentSessionInputRequestSchema>;
export type AgentUsage = z.infer<typeof agentUsageSchema>;
export type AgentUsageDay = z.infer<typeof agentUsageDaySchema>;
`;

const agentServiceOutput = `// Code generated by scripts/generate-operations-agent-session-contracts.ts; DO NOT EDIT.
// Contract SHA-256: ${digest}

export const HVAC_AGENT_EVENT_VERSION = '${eventVersion}' as const;
export const AGENT_EVENT_TYPES = Object.freeze(${zodEnumLiteral(eventTypes)} as const);
export const AGENT_SESSION_STATUSES = Object.freeze(${zodEnumLiteral(sessionStatuses)} as const);
export const AGENT_RUN_STATUSES = Object.freeze(${zodEnumLiteral(runStatuses)} as const);
export const AGENT_MESSAGE_ROLES = Object.freeze(${zodEnumLiteral(messageRoles)} as const);
export const AGENT_TOOL_EXECUTION_STATUSES = Object.freeze(${zodEnumLiteral(toolExecutionStatuses)} as const);
export const AGENT_OWNERS = Object.freeze(${zodEnumLiteral(evidenceOwners)} as const);
export const INVESTIGATION_OUTCOMES = Object.freeze(${zodEnumLiteral(investigationOutcomes)} as const);
export const FINDING_CHART_KINDS = Object.freeze(${zodEnumLiteral(findingChartKinds)} as const);

export const MAX_CHART_CATEGORIES = ${maxChartCategories} as const;
export const MAX_CHART_SERIES = ${maxChartSeries} as const;
export const MAX_KEY_FIGURES = ${maxKeyFigures} as const;
export const MAX_FINDING_EVIDENCE_REFS = ${maxEvidenceRefs} as const;
export const MAX_FINDING_LIST_ITEMS = ${maxFindingListItems} as const;
export const MAX_FINDING_LIST_ITEM_LENGTH = ${maxFindingListItemLength} as const;
export const MAX_EVIDENCE_REF_FIELD_LENGTH = ${maxEvidenceRefFieldLength} as const;
`;

const gatewayOutput = `// Code generated by scripts/generate-operations-agent-session-contracts.ts; DO NOT EDIT.
// Contract SHA-256: ${digest}

package gateway

const (
\tPublicAgentSessionsPathTemplate           = "${collectionPath}"
\tPublicAgentSessionPathTemplate            = "${detailPath}"
\tPublicAgentSessionEventsPathTemplate      = "${eventsPath}"
\tPublicAgentSessionStartRunPathTemplate    = "${runPath}"
\tPublicAgentSessionCancelPathTemplate      = "${cancelPath}"
\tPublicAgentSessionSubmitInputPathTemplate = "${submitInputPath}"
\tPublicAgentUsagePathTemplate              = "${usagePath}"

\tAgentEventVersion             = "${eventVersion}"
\tmaxAgentSessionList           = ${maxSessionList}
\tmaxAgentSessionRuns           = ${maxSnapshotRuns}
\tmaxAgentSessionMessages       = ${maxSnapshotMessages}
\tmaxAgentSessionToolExecutions = ${maxSnapshotToolExecutions}
\tmaxAgentSessionArtifacts      = ${maxSnapshotArtifacts}
\tmaxFindingKeyFigures          = ${maxKeyFigures}
\tmaxFindingEvidenceRefs        = ${maxEvidenceRefs}
\tmaxFindingListItems           = ${maxFindingListItems}
\tmaxFindingListItemLength      = ${maxFindingListItemLength}
\tmaxFindingChartCategories     = ${maxChartCategories}
\tmaxFindingChartSeries         = ${maxChartSeries}
)

func isAllowedAgentEvidenceOwner(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(evidenceOwners)})
}

func isAllowedAgentRunStatus(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(runStatuses)})
}

func isAllowedAgentMessageRole(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(messageRoles)})
}

func isAllowedAgentToolExecutionStatus(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(toolExecutionStatuses)})
}

func isAllowedAgentSessionStatus(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(sessionStatuses)})
}

func isAllowedAgentInvestigationOutcome(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(investigationOutcomes)})
}

func isAllowedFindingChartKind(value any) bool {
\treturn operationsAllowedString(value, ${goQuoteList(findingChartKinds)})
}
`;

const targets: readonly TargetSpec[] = [
  { label: 'Web', path: 'apps/hvac-web/src/api/generated/operationsAgentSessions.gen.ts', content: webOutput },
  { label: 'Agent Service', path: 'services/operations-agent-service/src/agent/internal/session-contract.gen.ts', content: agentServiceOutput },
  { label: 'Go Gateway', path: 'cmd/energy-api/internal/gateway/operations_agent_contract_gen.go', content: gatewayOutput },
];

if (checkOnly) {
  const stale: string[] = [];
  for (const target of targets) {
    const current = await readFile(resolve(root, target.path), 'utf8').catch(() => '');
    if (current.replace(/\r\n?/g, '\n') !== target.content) {
      stale.push(target.path);
    }
  }
  if (stale.length > 0) {
    console.error(`Generated Operations Agent Session contract(s) are stale (${stale.join(', ')}). Run npm run operations-agent:contracts:generate.`);
    process.exit(1);
  }
  console.log(`Operations Agent Session generated contracts (${targets.length} targets) are current.`);
} else {
  for (const target of targets) {
    await writeFile(resolve(root, target.path), target.content, 'utf8');
  }
  console.log(`Generated Operations Agent Session contracts (${targets.map((t) => t.label).join(', ')}).`);
}
