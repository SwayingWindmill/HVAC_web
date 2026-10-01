export interface AgentRunContext {
  readonly tenantId: string;
  readonly siteId: string;
  readonly principalId: string;
  readonly capabilities: readonly string[];
  readonly sessionId: string;
  readonly runId: string;
  readonly correlationId: string;
}

export interface AgentModelRef {
  readonly provider: string;
  readonly model: string;
}

export interface AgentRunBudget {
  readonly maxModelCalls: number;
  readonly maxToolCalls: number;
  readonly maxWallClockMs: number;
  readonly maxParallelToolCalls: number;
  readonly maxQueryRangeMs: number;
  readonly maxToolResultRecords: number;
  readonly maxToolResultBytes: number;
  readonly maxInputTokens: number;
  readonly maxOutputTokens: number;
}

export type AgentToolExecutionMode = 'parallel' | 'sequential';
export type AgentToolReplayPolicy = 'safe' | 'idempotent' | 'never';
export type AgentToolInputSchema = Readonly<Record<string, unknown>>;

export interface AgentToolDefinition {
  readonly name: string;
  readonly description: string;
  readonly inputSchema: AgentToolInputSchema;
  readonly executionMode: AgentToolExecutionMode;
  readonly replayPolicy: AgentToolReplayPolicy;
  readonly requiredCapabilities: readonly string[];
}

export interface AgentToolExecutionRequest<TArguments = unknown> {
  readonly context: AgentRunContext;
  readonly arguments: TArguments;
  readonly signal: AbortSignal;
}

export interface AgentTool<TArguments = unknown, TResult = unknown> {
  readonly definition: AgentToolDefinition;
  execute(request: AgentToolExecutionRequest<TArguments>): Promise<TResult>;
}
