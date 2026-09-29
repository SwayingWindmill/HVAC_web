import type { AgentModelRef } from './policy.js';
import type {
  AgentRunTerminalStatus,
  AgentRunUsage,
  AgentToolExecutionStatus,
} from './session.js';

export type AgentRuntimeBudgetDimension =
  | 'MODEL_CALLS'
  | 'TOOL_CALLS'
  | 'TOOL_CONCURRENCY'
  | 'TOOL_RESULT_BYTES'
  | 'WALL_CLOCK_MS'
  | 'INPUT_TOKENS'
  | 'OUTPUT_TOKENS';

export type AgentRuntimeOwnerErrorClass =
  | 'REQUEST_REJECTED'
  | 'RESOURCE_NOT_FOUND'
  | 'TIMEOUT'
  | 'UNAVAILABLE'
  | 'RESPONSE_INVALID';

interface AgentRuntimeTelemetryEventBase<TType extends string> {
  readonly type: TType;
  readonly sessionId: string;
  readonly runId: string;
  readonly correlationId: string;
  readonly at: number;
}

export type AgentRuntimeTelemetryEvent =
  | (AgentRuntimeTelemetryEventBase<'run.started'> & Readonly<{
    modelRef: AgentModelRef;
  }>)
  | (AgentRuntimeTelemetryEventBase<'model.started'> & Readonly<{
    modelRef: AgentModelRef;
    modelCall: number;
  }>)
  | (AgentRuntimeTelemetryEventBase<'model.completed'> & Readonly<{
    modelRef: AgentModelRef;
    modelCall: number;
    durationMs: number;
    inputTokens: number;
    outputTokens: number;
  }>)
  | (AgentRuntimeTelemetryEventBase<'tool.started'> & Readonly<{
    toolExecutionId: string;
    toolName: string;
    activeToolCalls: number;
  }>)
  | (AgentRuntimeTelemetryEventBase<'tool.completed'> & Readonly<{
    toolExecutionId: string;
    toolName: string;
    status: Exclude<AgentToolExecutionStatus, 'RUNNING'>;
    durationMs: number;
    failureCode: string | null;
    ownerErrorClass: AgentRuntimeOwnerErrorClass | null;
  }>)
  | (AgentRuntimeTelemetryEventBase<'budget.exhausted'> & Readonly<{
    dimension: AgentRuntimeBudgetDimension;
    failureCode: string;
  }>)
  | (AgentRuntimeTelemetryEventBase<'run.completed'> & Readonly<{
    status: AgentRunTerminalStatus;
    durationMs: number;
    usage: AgentRunUsage;
    failureCode: string | null;
  }>);

export interface AgentRuntimeTelemetrySink {
  record(event: AgentRuntimeTelemetryEvent): void;
}

export const NOOP_AGENT_RUNTIME_TELEMETRY: AgentRuntimeTelemetrySink = Object.freeze({
  record() {},
});
