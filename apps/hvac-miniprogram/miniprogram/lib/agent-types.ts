// The Agent Session contract as the Gateway passes it on (contracts/http, the web's
// api/generated/operationsAgentSessions.gen.ts). The Gateway checks every snapshot and event against
// the contract before forwarding it, so the mini program reads them as typed here;
// test/assistant-parity.test.ts proves these types match the web's.

type Id = string;
type Timestamp = number;

export interface AgentSession {
  readonly id: Id;
  readonly tenantId: Id;
  readonly siteId: Id;
  readonly agentDefinitionId: Id;
  readonly createdBy: Id;
  readonly revision: number;
  readonly createdAt: Timestamp;
  readonly updatedAt: Timestamp;
  readonly status: 'ACTIVE' | 'WAITING_FOR_INPUT' | 'COMPLETED' | 'FAILED' | 'CANCELLED';
  readonly activeRunId: Id | null;
}

export interface AgentRun {
  readonly id: Id;
  readonly sessionId: Id;
  readonly modelRef: { readonly provider: Id; readonly model: Id };
  readonly status: 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED';
  readonly startedAt: Timestamp;
  readonly finishedAt: Timestamp | null;
  readonly usage: { readonly inputTokens: number; readonly outputTokens: number; readonly modelCalls: number; readonly toolCalls: number };
  readonly failureCode: Id | null;
}

export interface AgentMessage {
  readonly id: Id;
  readonly sessionId: Id;
  readonly runId: Id | null;
  readonly role: 'OPERATOR' | 'ASSISTANT';
  readonly content: string;
  readonly createdAt: Timestamp;
}

export interface AgentEvidenceRef {
  readonly owner: 'REGISTRY' | 'TELEMETRY' | 'ENERGY' | 'ALARM' | 'FDD' | 'WORK_ORDER' | 'COMMAND';
  readonly resourceType: Id;
  readonly resourceId: Id;
  readonly revision?: Id;
  readonly toolExecutionId: Id;
}

export interface AgentToolExecution {
  readonly id: Id;
  readonly sessionId: Id;
  readonly runId: Id;
  readonly toolName: string;
  readonly argumentsDigest: string;
  readonly status: 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED';
  readonly startedAt: Timestamp;
  readonly finishedAt: Timestamp | null;
  readonly resultSummary: string | null;
  readonly provenance: readonly AgentEvidenceRef[];
  readonly failureCode: Id | null;
}

export interface FindingChart {
  readonly kind: 'LINE' | 'BAR' | 'HORIZONTAL_BAR';
  readonly title: string;
  readonly unit: string;
  readonly categories: readonly string[];
  readonly series: readonly { readonly name: string; readonly values: readonly (number | null)[] }[];
}

export interface Finding {
  readonly outcome: 'SUPPORTED_FINDING' | 'UNABLE_TO_CONCLUDE';
  readonly keyFigures?: readonly { readonly label: string; readonly value: string; readonly unit?: string }[];
  readonly evidenceRefs: readonly AgentEvidenceRef[];
  readonly limitations: readonly string[];
  readonly recommendedNext: readonly string[];
  readonly chart?: FindingChart;
}

export type AgentInputResponse =
  | { readonly kind: 'TEXT'; readonly maxLength: number }
  | { readonly kind: 'SINGLE_SELECT'; readonly choices: readonly { readonly value: Id; readonly label: string }[] };

interface ArtifactBase {
  readonly id: Id;
  readonly sessionId: Id;
  readonly runId: Id;
  readonly createdAt: Timestamp;
}

export type AgentInputRequestArtifact = ArtifactBase & {
  readonly kind: 'INPUT_REQUEST';
  readonly request: { readonly prompt: string; readonly response: AgentInputResponse };
};

export type AgentArtifact =
  | (ArtifactBase & { readonly kind: 'EVIDENCE_REF'; readonly reference: AgentEvidenceRef })
  | (ArtifactBase & { readonly kind: 'FINDING'; readonly finding: Finding })
  | (ArtifactBase & { readonly kind: 'PROPOSAL'; readonly proposalType: Id; readonly summary: string })
  | AgentInputRequestArtifact
  | (ArtifactBase & { readonly kind: 'INPUT_RESPONSE'; readonly requestArtifactId: Id; readonly value: string; readonly submittedBy: Id })
  | (ArtifactBase & { readonly kind: 'LIMITATION'; readonly description: string });

export interface AgentSessionSnapshot {
  readonly session: AgentSession;
  readonly runs: readonly AgentRun[];
  readonly messages: readonly AgentMessage[];
  readonly toolExecutions: readonly AgentToolExecution[];
  readonly artifacts: readonly AgentArtifact[];
}

interface EventBase {
  readonly version: 'hvac.agent.event/v1';
  readonly sessionId: Id;
  readonly runId: Id | null;
  readonly sequence: number;
  readonly at: Timestamp;
}

export type AgentSessionEvent = EventBase & (
  | { readonly type: 'session.snapshot'; readonly payload: { readonly snapshot: AgentSessionSnapshot } }
  | { readonly type: 'run.started'; readonly payload: { readonly run: AgentRun } }
  | { readonly type: 'assistant.delta'; readonly payload: { readonly messageId: Id; readonly delta: string } }
  | { readonly type: 'tool.started'; readonly payload: { readonly toolExecutionId: Id; readonly toolName: Id } }
  | { readonly type: 'tool.completed'; readonly payload: { readonly toolExecution: AgentToolExecution } }
  | { readonly type: 'artifact.created'; readonly payload: { readonly artifact: AgentArtifact } }
  | { readonly type: 'input.required'; readonly payload: { readonly artifact: AgentInputRequestArtifact } }
  | { readonly type: 'run.completed'; readonly payload: { readonly run: AgentRun } }
  | { readonly type: 'run.failed'; readonly payload: { readonly run: AgentRun } }
);
