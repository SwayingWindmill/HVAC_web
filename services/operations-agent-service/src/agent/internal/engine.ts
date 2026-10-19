import type { AgentArtifact } from './artifacts.js';
import type { AgentEventSink } from './events.js';
import type {
  AgentRunBudget,
  AgentRunContext,
  AgentTool,
} from './policy.js';
import type {
  AgentMessage,
  AgentRun,
  AgentRunTerminalStatus,
  AgentRunUsage,
  AgentSession,
  AgentSessionStatus,
  AgentToolExecution,
} from './session.js';

/** The Site a Run answers for, as the prompt names it and reads its clock. */
export interface AgentRunSite {
  readonly name: string;
  readonly timezone: string;
}

export interface AgentEngineInput {
  readonly session: AgentSession;
  readonly run: AgentRun;
  readonly messages: readonly AgentMessage[];
  readonly tools: readonly AgentTool[];
  readonly context: AgentRunContext;
  /** Read once when the Run starts; the prompt names the Site and its local time (#593). */
  readonly site: () => Promise<AgentRunSite>;
  readonly budget: AgentRunBudget;
  readonly signal: AbortSignal;
  readonly emit: AgentEventSink;
}

export interface AgentEngineResult {
  readonly runStatus: AgentRunTerminalStatus;
  readonly sessionStatus: AgentSessionStatus;
  readonly failureCode: string | null;
  readonly usage: AgentRunUsage;
  readonly finalizedMessages: readonly AgentMessage[];
  readonly toolExecutions: readonly AgentToolExecution[];
  readonly artifacts: readonly AgentArtifact[];
}

export type AgentEngine = (input: AgentEngineInput) => Promise<AgentEngineResult>;
