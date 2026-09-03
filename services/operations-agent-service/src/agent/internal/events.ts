import type {
  AgentArtifact,
  AgentInputRequestArtifact,
} from './artifacts.js';
import type {
  AgentRun,
  AgentSession,
  AgentToolExecution,
} from './session.js';

export const HVAC_AGENT_EVENT_VERSION = 'hvac.agent.event/v1' as const;

interface AgentEventBase<TType extends string, TPayload> {
  readonly version: typeof HVAC_AGENT_EVENT_VERSION;
  readonly type: TType;
  readonly sessionId: string;
  readonly runId: string | null;
  readonly sequence: number;
  readonly at: number;
  readonly payload: TPayload;
}

export type AgentEvent =
  | AgentEventBase<'session.snapshot', Readonly<{ session: AgentSession }>>
  | AgentEventBase<'run.started', Readonly<{ run: AgentRun }>>
  | AgentEventBase<'assistant.delta', Readonly<{ messageId: string; delta: string }>>
  | AgentEventBase<'tool.started', Readonly<{ toolExecutionId: string; toolName: string }>>
  | AgentEventBase<'tool.completed', Readonly<{ toolExecution: AgentToolExecution }>>
  | AgentEventBase<'artifact.created', Readonly<{ artifact: AgentArtifact }>>
  | AgentEventBase<'input.required', Readonly<{ artifact: AgentInputRequestArtifact }>>
  | AgentEventBase<'run.completed', Readonly<{ run: AgentRun }>>
  | AgentEventBase<'run.failed', Readonly<{ run: AgentRun }>>;

export type AgentEventType = AgentEvent['type'];
export type AgentEventSink = (event: AgentEvent) => void;
