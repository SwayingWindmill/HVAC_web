import type { AgentEvidenceRef } from './artifacts.js';
import { AgentContractError } from './errors.js';
import type { AgentModelRef } from './policy.js';

export type AgentSessionStatus =
  | 'ACTIVE'
  | 'WAITING_FOR_INPUT'
  | 'COMPLETED'
  | 'FAILED'
  | 'CANCELLED';

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

export type AgentRunStatus = 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED';
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

export type AgentMessageRole = 'OPERATOR' | 'ASSISTANT';

export interface AgentMessage {
  readonly id: string;
  readonly sessionId: string;
  readonly runId: string | null;
  readonly role: AgentMessageRole;
  readonly content: string;
  readonly createdAt: number;
}

export type AgentToolExecutionStatus = 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED';

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

export type AgentSessionTransition =
  | Readonly<{ status: 'ACTIVE'; activeRunId: string; at: number }>
  | Readonly<{
    status: Exclude<AgentSessionStatus, 'ACTIVE'>;
    at: number;
  }>;

const allowedTransitions: Readonly<Record<AgentSessionStatus, readonly AgentSessionStatus[]>> = {
  ACTIVE: ['WAITING_FOR_INPUT', 'COMPLETED', 'FAILED', 'CANCELLED'],
  WAITING_FOR_INPUT: ['ACTIVE', 'CANCELLED'],
  COMPLETED: [],
  FAILED: [],
  CANCELLED: [],
};

export const transitionAgentSession = (
  session: AgentSession,
  transition: AgentSessionTransition,
): AgentSession => {
  if (!allowedTransitions[session.status].includes(transition.status)) {
    throw new AgentContractError(
      'SESSION_TRANSITION_INVALID',
      `Agent Session cannot transition from ${session.status} to ${transition.status}.`,
    );
  }

  if (transition.status === 'ACTIVE') {
    if (transition.activeRunId.length === 0) {
      throw new AgentContractError(
        'SESSION_TRANSITION_INVALID',
        'An ACTIVE Agent Session requires an active Run identity.',
      );
    }
    return Object.freeze({
      ...session,
      status: 'ACTIVE',
      activeRunId: transition.activeRunId,
      revision: session.revision + 1,
      updatedAt: transition.at,
    });
  }

  return Object.freeze({
    ...session,
    status: transition.status,
    activeRunId: null,
    revision: session.revision + 1,
    updatedAt: transition.at,
  });
};
