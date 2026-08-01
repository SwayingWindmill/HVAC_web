import type { LogicalTool } from '../../domain/index.js';

export type OperationsTelemetrySpanName =
  | 'operations.http.request'
  | 'operations.authorization'
  | 'operations.runtime.plan'
  | 'operations.runtime.step'
  | 'operations.model.call'
  | 'operations.tool.call'
  | 'operations.owner.request'
  | 'operations.budget.check'
  | 'operations.business.commit'
  | 'operations.run.terminal'
  | 'operations.stream.recovery';

export type OperationsTelemetrySpanKind = 'SERVER' | 'CLIENT' | 'INTERNAL';

export type OperationsTelemetryOperation =
  | 'START'
  | 'LIST'
  | 'GET'
  | 'STREAM'
  | 'ADVANCE'
  | 'SUBMIT_OPERATOR_INPUT'
  | 'CANCEL'
  | 'AUTHORIZE'
  | 'PLAN_READS'
  | 'EXECUTE_STEP'
  | 'SYNTHESIZE_FINDING'
  | 'AUTHORIZE_TOOL'
  | 'READ_OWNER'
  | 'CHECK_BUDGET'
  | 'COMMIT_EFFECT'
  | 'COMPLETE_RUN'
  | 'FAIL_RUN';

export type OperationsTelemetryOutcome =
  | 'SUCCESS'
  | 'DENIED'
  | 'NOT_FOUND'
  | 'INVALID'
  | 'TIMEOUT'
  | 'UNAVAILABLE'
  | 'CONFLICT'
  | 'DUPLICATE'
  | 'EXHAUSTED'
  | 'PARTIAL'
  | 'UNABLE_TO_CONCLUDE'
  | 'CANCELLED'
  | 'ERROR';

export type OperationsTelemetryOwner =
  | 'platform-gateway'
  | 'registry'
  | 'telemetry-query-service'
  | 'command-service'
  | 'operations-agent-service';

export type OperationsTelemetryRecoveryMode = 'FULL_SNAPSHOT' | 'RESUME';

export type OperationsTelemetryRecoveryReason =
  | 'INITIAL'
  | 'VALID'
  | 'EXPIRED'
  | 'UNKNOWN'
  | 'FUTURE'
  | 'CONFLICT'
  | 'INVALID';

export type OperationsTelemetryBudgetDimension =
  | 'MODEL_INVOCATIONS'
  | 'TOOL_REQUESTS'
  | 'WALL_CLOCK_MS'
  | 'QUERY_RANGE_MS'
  | 'QUERY_BUCKETS'
  | 'OWNER_RECORDS'
  | 'PAYLOAD_BYTES';

export interface OperationsTelemetryCorrelation {
  readonly requestId?: string;
  readonly traceparent?: string;
  readonly tracestate?: string;
  readonly investigationId?: string;
  readonly runId?: string;
  readonly stepId?: string;
}

export interface OperationsTelemetryAttributes {
  readonly operation?: OperationsTelemetryOperation;
  readonly outcome?: OperationsTelemetryOutcome;
  readonly owner?: OperationsTelemetryOwner;
  readonly logicalTool?: LogicalTool;
  readonly recoveryMode?: OperationsTelemetryRecoveryMode;
  readonly recoveryReason?: OperationsTelemetryRecoveryReason;
  readonly budgetDimension?: OperationsTelemetryBudgetDimension;
  readonly requestId?: string;
  readonly investigationId?: string;
  readonly runId?: string;
  readonly stepId?: string;
  readonly durationMs?: number;
  readonly retryCount?: number;
  readonly budgetConsumed?: number;
  readonly budgetLimit?: number;
  readonly ownerRecords?: number;
  readonly payloadBytes?: number;
  readonly modelInputTokens?: number;
  readonly modelOutputTokens?: number;
  readonly duplicate?: boolean;
  readonly restarted?: boolean;
  readonly partial?: boolean;
  readonly terminal?: boolean;
}

export interface OperationsTelemetryMetricLabels {
  readonly operation?: OperationsTelemetryOperation;
  readonly outcome?: OperationsTelemetryOutcome;
  readonly owner?: OperationsTelemetryOwner;
  readonly logicalTool?: LogicalTool;
  readonly recoveryMode?: OperationsTelemetryRecoveryMode;
  readonly recoveryReason?: OperationsTelemetryRecoveryReason;
  readonly budgetDimension?: OperationsTelemetryBudgetDimension;
}

export type OperationsTelemetryMetricName =
  | 'operations_agent_requests_total'
  | 'operations_agent_operation_duration_ms'
  | 'operations_agent_tool_calls_total'
  | 'operations_agent_tool_duration_ms'
  | 'operations_agent_retries_total'
  | 'operations_agent_recovery_total'
  | 'operations_agent_budget_consumed'
  | 'operations_agent_budget_exhaustions_total'
  | 'operations_agent_business_commits_total'
  | 'operations_agent_terminal_outcomes_total'
  | 'operations_agent_model_tokens';

export interface OperationsTelemetrySpan {
  readonly traceparent?: string;
  readonly tracestate?: string;
  setAttributes(attributes: OperationsTelemetryAttributes): void;
  setStatus(outcome: OperationsTelemetryOutcome): void;
  end(): void;
}

export interface OperationsAgentTelemetry {
  startSpan(input: {
    readonly name: OperationsTelemetrySpanName;
    readonly kind: OperationsTelemetrySpanKind;
    readonly correlation?: OperationsTelemetryCorrelation;
    readonly attributes?: OperationsTelemetryAttributes;
  }): OperationsTelemetrySpan;
  addCounter(input: {
    readonly name: OperationsTelemetryMetricName;
    readonly value?: number;
    readonly labels?: OperationsTelemetryMetricLabels;
  }): void;
  observeHistogram(input: {
    readonly name: OperationsTelemetryMetricName;
    readonly value: number;
    readonly labels?: OperationsTelemetryMetricLabels;
  }): void;
}

const noopSpan: OperationsTelemetrySpan = Object.freeze({
  setAttributes() {},
  setStatus() {},
  end() {},
});

export const NOOP_OPERATIONS_AGENT_TELEMETRY: OperationsAgentTelemetry = Object.freeze({
  startSpan() {
    return noopSpan;
  },
  addCounter() {},
  observeHistogram() {},
});

const validTelemetryTraceparent = (value: unknown): value is string => {
  if (typeof value !== 'string'
    || !/^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/u.test(value)) return false;
  return value.slice(3, 35) !== '0'.repeat(32)
    && value.slice(36, 52) !== '0'.repeat(16);
};

const validTelemetryTracestate = (value: unknown): value is string => (
  typeof value === 'string'
  && value.length > 0
  && value.length <= 512
  && !value.includes(String.fromCharCode(13))
  && !value.includes(String.fromCharCode(10))
);

const safeOperationsTelemetrySpan = (span: OperationsTelemetrySpan): OperationsTelemetrySpan => {
  let traceparent: string | undefined;
  let tracestate: string | undefined;
  try {
    if (validTelemetryTraceparent(span.traceparent)) traceparent = span.traceparent;
    if (validTelemetryTracestate(span.tracestate)) tracestate = span.tracestate;
  } catch {
    // Invalid diagnostic context is dropped rather than entering business execution.
  }
  return Object.freeze({
    ...(traceparent === undefined ? {} : { traceparent }),
    ...(tracestate === undefined ? {} : { tracestate }),
    setAttributes(attributes: OperationsTelemetryAttributes) {
      try {
        span.setAttributes(attributes);
      } catch {
        // Telemetry is diagnostic only and cannot alter Application behavior.
      }
    },
    setStatus(outcome: OperationsTelemetryOutcome) {
      try {
        span.setStatus(outcome);
      } catch {
        // Telemetry is diagnostic only and cannot alter Application behavior.
      }
    },
    end() {
      try {
        span.end();
      } catch {
        // Telemetry is diagnostic only and cannot alter Application behavior.
      }
    },
  });
};

export const safeStartOperationsTelemetrySpan = (
  telemetry: OperationsAgentTelemetry | undefined,
  input: Parameters<OperationsAgentTelemetry['startSpan']>[0],
): OperationsTelemetrySpan => {
  try {
    return safeOperationsTelemetrySpan(
      (telemetry ?? NOOP_OPERATIONS_AGENT_TELEMETRY).startSpan(input),
    );
  } catch {
    return noopSpan;
  }
};

export const safeAddOperationsTelemetryCounter = (
  telemetry: OperationsAgentTelemetry | undefined,
  input: Parameters<OperationsAgentTelemetry['addCounter']>[0],
): void => {
  try {
    (telemetry ?? NOOP_OPERATIONS_AGENT_TELEMETRY).addCounter(input);
  } catch {
    // Telemetry is diagnostic only and cannot alter Application behavior.
  }
};

export const safeObserveOperationsTelemetryHistogram = (
  telemetry: OperationsAgentTelemetry | undefined,
  input: Parameters<OperationsAgentTelemetry['observeHistogram']>[0],
): void => {
  try {
    (telemetry ?? NOOP_OPERATIONS_AGENT_TELEMETRY).observeHistogram(input);
  } catch {
    // Telemetry is diagnostic only and cannot alter Application behavior.
  }
};
