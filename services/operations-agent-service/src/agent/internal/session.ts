import type { AgentEvidenceRef } from './artifacts.js';
import type { AgentModelRef } from './policy.js';
import type {
  AGENT_MESSAGE_ROLES,
  AGENT_RUN_STATUSES,
  AGENT_SESSION_STATUSES,
  AGENT_TOOL_EXECUTION_STATUSES,
} from './session-contract.gen.js';

export type AgentSessionStatus = (typeof AGENT_SESSION_STATUSES)[number];

interface AgentSessionBase {
  readonly id: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly agentDefinitionId: string;
  readonly createdBy: string;
  readonly revision: number;
  readonly createdAt: number;
  readonly updatedAt: number;
}

export interface ActiveAgentSession extends AgentSessionBase {
  readonly status: 'ACTIVE';
  readonly activeRunId: string;
}

export interface InactiveAgentSession extends AgentSessionBase {
  readonly status: Exclude<AgentSessionStatus, 'ACTIVE'>;
  readonly activeRunId: null;
}

export type AgentSession = ActiveAgentSession | InactiveAgentSession;

export type AgentRunStatus = (typeof AGENT_RUN_STATUSES)[number];
export type AgentRunTerminalStatus = Exclude<AgentRunStatus, 'RUNNING'>;

export interface AgentRunUsage {
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly modelCalls: number;
  readonly toolCalls: number;
}

export interface AgentRun {
  readonly id: string;
  readonly sessionId: string;
  readonly modelRef: AgentModelRef;
  readonly status: AgentRunStatus;
  readonly startedAt: number;
  readonly finishedAt: number | null;
  readonly usage: AgentRunUsage;
  readonly failureCode: string | null;
}

export type AgentMessageRole = (typeof AGENT_MESSAGE_ROLES)[number];

export interface AgentMessage {
  readonly id: string;
  readonly sessionId: string;
  readonly runId: string | null;
  readonly role: AgentMessageRole;
  readonly content: string;
  readonly createdAt: number;
}

export type AgentToolExecutionStatus = (typeof AGENT_TOOL_EXECUTION_STATUSES)[number];

export interface AgentToolExecution {
  readonly id: string;
  readonly sessionId: string;
  readonly runId: string;
  readonly toolName: string;
  readonly argumentsDigest: string;
  readonly status: AgentToolExecutionStatus;
  readonly startedAt: number;
  readonly finishedAt: number | null;
  readonly resultSummary: string | null;
  readonly provenance: readonly AgentEvidenceRef[];
  readonly failureCode: string | null;
}
