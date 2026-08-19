import { createHash, randomBytes } from 'node:crypto';

import {
  type OperationsAgentTelemetry,
  type OperationsTelemetryAttributes,
  type OperationsTelemetryMetricLabels,
  type OperationsTelemetryMetricName,
  type OperationsTelemetryOutcome,
  type OperationsTelemetrySpan,
  type OperationsTelemetrySpanKind,
  type OperationsTelemetrySpanName,
} from '../../application/index.js';

export interface OperationsTelemetrySpanData {
  readonly service: string;
  readonly name: OperationsTelemetrySpanName;
  readonly kind: OperationsTelemetrySpanKind;
  readonly traceId: string;
  readonly spanId: string;
  readonly parentSpanId: string | null;
  readonly traceState: string | null;
  readonly startedAt: number;
  readonly completedAt: number;
  readonly status: OperationsTelemetryOutcome | null;
  readonly attributes: Readonly<Record<string, string | number | boolean>>;
}

export interface OperationsTelemetryMetricPoint {
  readonly name: OperationsTelemetryMetricName;
  readonly kind: 'COUNTER' | 'HISTOGRAM';
  readonly labels: Readonly<Record<string, string>>;
  readonly value: number;
  readonly count: number;
}

export interface OperationsTelemetryExporter {
  export(spans: readonly OperationsTelemetrySpanData[]): Promise<void> | void;
  shutdown?(): Promise<void> | void;
}

export interface OperationsTelemetryRuntimeDiagnostics {
  readonly queuedSpans: number;
  readonly exportedSpans: number;
  readonly droppedSpans: number;
  readonly failedExports: number;
  readonly rejectedAttributes: number;
  readonly rejectedMetrics: number;
}

export interface OperationsTelemetryRuntime extends OperationsAgentTelemetry {
  readonly service: string;
  spans(): readonly OperationsTelemetrySpanData[];
  metrics(): readonly OperationsTelemetryMetricPoint[];
  diagnostics(): OperationsTelemetryRuntimeDiagnostics;
  flush(): Promise<void>;
  shutdown(): Promise<void>;
}

export interface OperationsTelemetryRuntimeOptions {
  readonly service?: string;
  readonly exporter?: OperationsTelemetryExporter;
  readonly now?: () => number;
  readonly maximumQueuedSpans?: number;
}

export interface OperationsOtlpHttpExporterOptions {
  readonly endpoint: string;
  readonly fetchImplementation?: typeof fetch;
  readonly timeoutMs?: number;
}

const traceparentPattern = /^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/u;
const zeroTraceId = '0'.repeat(32);
const zeroSpanId = '0'.repeat(16);
const maximumIdentityCharacters = 512;
const maximumTraceStateCharacters = 512;
const defaultMaximumQueuedSpans = 256;
const maximumQueuedSpansLimit = 4_096;
const defaultService = 'operations-agent-service';

const spanNames = new Set<OperationsTelemetrySpanName>([
  'operations.http.request',
  'operations.authorization',
  'operations.runtime.plan',
  'operations.runtime.step',
  'operations.model.call',
  'operations.tool.call',
  'operations.owner.request',
  'operations.budget.check',
  'operations.business.commit',
  'operations.run.terminal',
  'operations.stream.recovery',
]);
const spanKinds = new Set<OperationsTelemetrySpanKind>(['SERVER', 'CLIENT', 'INTERNAL']);
const outcomes = new Set<OperationsTelemetryOutcome>([
  'SUCCESS',
  'DENIED',
  'NOT_FOUND',
  'INVALID',
  'TIMEOUT',
  'UNAVAILABLE',
  'CONFLICT',
  'DUPLICATE',
  'EXHAUSTED',
  'PARTIAL',
  'UNABLE_TO_CONCLUDE',
  'CANCELLED',
  'ERROR',
]);
const metricNames = new Set<OperationsTelemetryMetricName>([
  'operations_agent_requests_total',
  'operations_agent_operation_duration_ms',
  'operations_agent_tool_calls_total',
  'operations_agent_tool_duration_ms',
  'operations_agent_retries_total',
  'operations_agent_recovery_total',
  'operations_agent_budget_consumed',
  'operations_agent_budget_exhaustions_total',
  'operations_agent_business_commits_total',
  'operations_agent_terminal_outcomes_total',
  'operations_agent_model_tokens',
]);

const attributeKeys = new Set<keyof OperationsTelemetryAttributes>([
  'operation',
  'outcome',
  'owner',
  'logicalTool',
  'recoveryMode',
  'recoveryReason',
  'budgetDimension',
  'requestId',
  'investigationId',
  'runId',
  'stepId',
  'durationMs',
  'retryCount',
  'budgetConsumed',
  'budgetLimit',
  'ownerRecords',
  'payloadBytes',
  'modelInputTokens',
  'modelOutputTokens',
  'duplicate',
  'restarted',
  'partial',
  'terminal',
]);
const identityAttributeKeys = new Set<keyof OperationsTelemetryAttributes>([
  'requestId',
  'investigationId',
  'runId',
  'stepId',
]);
const numericAttributeKeys = new Set<keyof OperationsTelemetryAttributes>([
  'durationMs',
  'retryCount',
  'budgetConsumed',
  'budgetLimit',
  'ownerRecords',
  'payloadBytes',
  'modelInputTokens',
  'modelOutputTokens',
]);
const booleanAttributeKeys = new Set<keyof OperationsTelemetryAttributes>([
  'duplicate',
  'restarted',
  'partial',
  'terminal',
]);
const metricLabelKeys = new Set<keyof OperationsTelemetryMetricLabels>([
  'operation',
  'outcome',
  'owner',
  'logicalTool',
  'recoveryMode',
  'recoveryReason',
  'budgetDimension',
]);

const operationValues = new Set([
  'START', 'LIST', 'GET', 'STREAM', 'ADVANCE', 'SUBMIT_OPERATOR_INPUT', 'CANCEL',
  'AUTHORIZE', 'PLAN_READS', 'EXECUTE_STEP', 'SYNTHESIZE_FINDING', 'AUTHORIZE_TOOL',
  'READ_OWNER', 'CHECK_BUDGET', 'COMMIT_EFFECT', 'COMPLETE_RUN', 'FAIL_RUN',
]);
const ownerValues = new Set([
  'platform-gateway', 'registry', 'telemetry-query-service', 'command-service',
  'operations-agent-service',
]);
const logicalToolValues = new Set([
  'registry.getSite', 'registry.listSiteAssets', 'telemetry.getCurrentSnapshot',
  'analytics.getEnergySeries', 'commands.getCapabilities',
]);
const recoveryModeValues = new Set(['FULL_SNAPSHOT', 'RESUME']);
const recoveryReasonValues = new Set([
  'INITIAL', 'VALID', 'EXPIRED', 'UNKNOWN', 'FUTURE', 'CONFLICT', 'INVALID',
]);
const budgetDimensionValues = new Set([
  'MODEL_INVOCATIONS', 'TOOL_REQUESTS', 'WALL_CLOCK_MS', 'QUERY_RANGE_MS',
  'QUERY_BUCKETS', 'OWNER_RECORDS', 'PAYLOAD_BYTES',
]);

const validEnumeratedValue = (key: string, value: string): boolean => {
  if (key === 'operation') return operationValues.has(value);
  if (key === 'outcome') return outcomes.has(value as OperationsTelemetryOutcome);
  if (key === 'owner') return ownerValues.has(value);
  if (key === 'logicalTool') return logicalToolValues.has(value);
  if (key === 'recoveryMode') return recoveryModeValues.has(value);
  if (key === 'recoveryReason') return recoveryReasonValues.has(value);
  if (key === 'budgetDimension') return budgetDimensionValues.has(value);
  return false;
};

const boundedIdentity = (value: unknown): value is string => (
  typeof value === 'string'
  && value.trim().length > 0
  && value.length <= maximumIdentityCharacters
  && !/[\r\n]/u.test(value)
);

export const hashOperationsTelemetryIdentity = (
  kind: 'request' | 'investigation' | 'run' | 'step',
  value: string,
): string => {
  if (!boundedIdentity(value)) {
    throw new Error(`Operations telemetry ${kind} identity is invalid.`);
  }
  return `sha256:${createHash('sha256').update(`${kind}:${value}`, 'utf8').digest('hex').slice(0, 32)}`;
};

const parseTraceparent = (value: string | undefined): {
  readonly traceId: string;
  readonly parentSpanId: string | null;
  readonly flags: string;
} => {
  const match = value === undefined ? null : traceparentPattern.exec(value.trim().toLowerCase());
  if (match === null || match[1] === zeroTraceId || match[2] === zeroSpanId) {
    return { traceId: randomBytes(16).toString('hex'), parentSpanId: null, flags: '01' };
  }
  return { traceId: match[1] as string, parentSpanId: match[2] as string, flags: match[3] as string };
};

const sanitizeTraceState = (value: string | undefined): string | null => {
  if (value === undefined) return null;
  const normalized = value.trim();
  if (normalized.length === 0
    || normalized.length > maximumTraceStateCharacters
    || /[\r\n]/u.test(normalized)) return null;
  return normalized;
};

const finiteNonNegative = (value: unknown): value is number => (
  typeof value === 'number' && Number.isFinite(value) && value >= 0
);

const sanitizeAttributes = (
  input: OperationsTelemetryAttributes | undefined,
  rejected: () => void,
): Record<string, string | number | boolean> => {
  if (input === undefined) return {};
  const candidate = input as unknown as Record<string, unknown>;
  const result: Record<string, string | number | boolean> = {};
  for (const [rawKey, value] of Object.entries(candidate)) {
    const key = rawKey as keyof OperationsTelemetryAttributes;
    if (!attributeKeys.has(key)) {
      rejected();
      continue;
    }
    if (identityAttributeKeys.has(key)) {
      if (!boundedIdentity(value)) {
        rejected();
        continue;
      }
      const identityKind = key === 'requestId'
        ? 'request'
        : key === 'investigationId'
          ? 'investigation'
          : key === 'runId' ? 'run' : 'step';
      result[`operations.${identityKind}.correlation`] = hashOperationsTelemetryIdentity(
        identityKind,
        value,
      );
      continue;
    }
    if (numericAttributeKeys.has(key)) {
      if (!finiteNonNegative(value)) {
        rejected();
        continue;
      }
      result[`operations.${key}`] = value;
      continue;
    }
    if (booleanAttributeKeys.has(key)) {
      if (typeof value !== 'boolean') {
        rejected();
        continue;
      }
      result[`operations.${key}`] = value;
      continue;
    }
    if (typeof value !== 'string'
      || value.length > 128
      || value.includes(String.fromCharCode(13))
      || value.includes(String.fromCharCode(10))
      || !validEnumeratedValue(key, value)) {
      rejected();
      continue;
    }
    result[`operations.${key}`] = value;
  }
  return result;
};

const sanitizeMetricLabels = (
  input: OperationsTelemetryMetricLabels | undefined,
): Record<string, string> | null => {
  if (input === undefined) return {};
  const candidate = input as unknown as Record<string, unknown>;
  const result: Record<string, string> = {};
  for (const [rawKey, value] of Object.entries(candidate)) {
    const key = rawKey as keyof OperationsTelemetryMetricLabels;
    if (!metricLabelKeys.has(key)
      || typeof value !== 'string'
      || value.length === 0
      || value.length > 128
      || value.includes(String.fromCharCode(13))
      || value.includes(String.fromCharCode(10))
      || !validEnumeratedValue(key, value)) return null;
    result[key] = value;
  }
  return result;
};

const labelsKey = (labels: Readonly<Record<string, string>>): string => (
  Object.entries(labels)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${value}`)
    .join('|')
);

const cloneSpan = (span: OperationsTelemetrySpanData): OperationsTelemetrySpanData => ({
  ...span,
  attributes: { ...span.attributes },
});

const emptyExporter: OperationsTelemetryExporter = Object.freeze({ export() {} });

export const createMemoryOperationsTelemetryExporter = (): OperationsTelemetryExporter & {
  spans(): readonly OperationsTelemetrySpanData[];
} => {
  const exported: OperationsTelemetrySpanData[] = [];
  return Object.freeze({
    export(spans: readonly OperationsTelemetrySpanData[]) {
      exported.push(...spans.map(cloneSpan));
    },
    spans() {
      return exported.map(cloneSpan);
    },
  });
};

export const createOperationsOtlpHttpExporter = (
  options: OperationsOtlpHttpExporterOptions,
): OperationsTelemetryExporter => {
  const parsed = new URL(options.endpoint);
  if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:')
    || parsed.username !== ''
    || parsed.password !== ''
    || parsed.search !== ''
    || parsed.hash !== '') {
    throw new Error('Operations OTLP endpoint must be an HTTP URL without credentials or query data.');
  }
  const endpoint = parsed.pathname.endsWith('/v1/traces')
    ? parsed.toString()
    : new URL(`${parsed.pathname.replace(/\/+$/u, '')}/v1/traces`, parsed.origin).toString();
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const timeoutMs = options.timeoutMs ?? 750;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 30_000) {
    throw new Error('Operations OTLP timeout must be between 1 and 30000 milliseconds.');
  }
  return Object.freeze({
    async export(spans: readonly OperationsTelemetrySpanData[]) {
      if (spans.length === 0) return;
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      try {
        const spansByService = new Map<string, OperationsTelemetrySpanData[]>();
        for (const span of spans) {
          const serviceSpans = spansByService.get(span.service) ?? [];
          serviceSpans.push(span);
          spansByService.set(span.service, serviceSpans);
        }
        const response = await fetchImplementation(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          signal: controller.signal,
          body: JSON.stringify({
            resourceSpans: [...spansByService.entries()].map(([service, serviceSpans]) => ({
              resource: {
                attributes: [{ key: 'service.name', value: { stringValue: service } }],
              },
              scopeSpans: [{
                scope: { name: '@hvac/operations-agent-service' },
                spans: serviceSpans.map((span: OperationsTelemetrySpanData) => ({
                  traceId: span.traceId,
                  spanId: span.spanId,
                  ...(span.parentSpanId === null ? {} : { parentSpanId: span.parentSpanId }),
                  ...(span.traceState === null ? {} : { traceState: span.traceState }),
                  name: span.name,
                  kind: span.kind === 'SERVER' ? 2 : span.kind === 'CLIENT' ? 3 : 1,
                  startTimeUnixNano: String(Math.trunc(span.startedAt * 1_000_000)),
                  endTimeUnixNano: String(Math.trunc(span.completedAt * 1_000_000)),
                  attributes: Object.entries(span.attributes).map(([key, value]) => ({
                    key,
                    value: typeof value === 'string'
                      ? { stringValue: value }
                      : typeof value === 'boolean'
                        ? { boolValue: value }
                        : Number.isInteger(value)
                          ? { intValue: String(value) }
                          : { doubleValue: value },
                  })),
                  ...(span.status === null ? {} : {
                    status: {
                      code: span.status === 'SUCCESS' ? 1 : 2,
                      message: span.status,
                    },
                  }),
                })),
              }],
            })),
          }),
        });
        if (!response.ok) throw new Error('OPERATIONS_OTLP_EXPORT_REJECTED');
        await response.body?.cancel().catch(() => undefined);
      } finally {
        clearTimeout(timer);
      }
    },
  });
};

export const createOperationsTelemetryRuntime = (
  options: OperationsTelemetryRuntimeOptions = {},
): OperationsTelemetryRuntime => {
  const service = options.service?.trim() || defaultService;
  if (service.length > 128 || /[\r\n]/u.test(service)) {
    throw new Error('Operations telemetry service name is invalid.');
  }
  const maximumQueuedSpans = options.maximumQueuedSpans ?? defaultMaximumQueuedSpans;
  if (!Number.isSafeInteger(maximumQueuedSpans)
    || maximumQueuedSpans <= 0
    || maximumQueuedSpans > maximumQueuedSpansLimit) {
    throw new Error(`maximumQueuedSpans must be between 1 and ${maximumQueuedSpansLimit}.`);
  }
  const now = options.now ?? Date.now;
  const exporter = options.exporter ?? emptyExporter;
  const queued: OperationsTelemetrySpanData[] = [];
  const exported: OperationsTelemetrySpanData[] = [];
  const metricPoints = new Map<string, OperationsTelemetryMetricPoint>();
  let droppedSpans = 0;
  let failedExports = 0;
  let rejectedAttributes = 0;
  let rejectedMetrics = 0;
  let flushScheduled = false;
  let flushing: Promise<void> | null = null;
  let shutdown = false;

  const flush = async (): Promise<void> => {
    if (flushing !== null) return flushing;
    flushing = (async () => {
      while (queued.length > 0) {
        const batch = queued.splice(0, Math.min(64, queued.length));
        try {
          await exporter.export(batch.map(cloneSpan));
          exported.push(...batch.map(cloneSpan));
        } catch {
          failedExports += batch.length;
        }
      }
    })().finally(() => {
      flushing = null;
      flushScheduled = false;
      if (!shutdown && queued.length > 0) scheduleFlush();
    });
    return flushing;
  };

  const scheduleFlush = (): void => {
    if (flushScheduled || shutdown) return;
    flushScheduled = true;
    queueMicrotask(() => { void flush(); });
  };

  const telemetry: OperationsTelemetryRuntime = {
    service,
    startSpan(input) {
      if (!spanNames.has(input.name) || !spanKinds.has(input.kind)) {
        throw new Error('Operations telemetry span name or kind is invalid.');
      }
      const parent = parseTraceparent(input.correlation?.traceparent);
      const spanId = randomBytes(8).toString('hex');
      const traceState = sanitizeTraceState(input.correlation?.tracestate);
      const startedAt = now();
      const attributes = sanitizeAttributes({
        ...input.attributes,
        ...(input.correlation?.requestId === undefined
          ? {} : { requestId: input.correlation.requestId }),
        ...(input.correlation?.investigationId === undefined
          ? {} : { investigationId: input.correlation.investigationId }),
        ...(input.correlation?.runId === undefined ? {} : { runId: input.correlation.runId }),
        ...(input.correlation?.stepId === undefined ? {} : { stepId: input.correlation.stepId }),
      }, () => { rejectedAttributes += 1; });
      let status: OperationsTelemetryOutcome | null = null;
      let ended = false;
      const span: OperationsTelemetrySpan = {
        traceparent: `00-${parent.traceId}-${spanId}-${parent.flags}`,
        ...(traceState === null ? {} : { tracestate: traceState }),
        setAttributes(next) {
          if (ended) return;
          Object.assign(attributes, sanitizeAttributes(next, () => { rejectedAttributes += 1; }));
        },
        setStatus(next) {
          if (!ended && outcomes.has(next)) status = next;
        },
        end() {
          if (ended) return;
          ended = true;
          const completedAt = Math.max(startedAt, now());
          const data: OperationsTelemetrySpanData = Object.freeze({
            service,
            name: input.name,
            kind: input.kind,
            traceId: parent.traceId,
            spanId,
            parentSpanId: parent.parentSpanId,
            traceState,
            startedAt,
            completedAt,
            status,
            attributes: Object.freeze({ ...attributes }),
          });
          if (queued.length >= maximumQueuedSpans) {
            droppedSpans += 1;
            return;
          }
          queued.push(data);
          scheduleFlush();
        },
      };
      return Object.freeze(span);
    },
    addCounter(input) {
      if (!metricNames.has(input.name)
        || !finiteNonNegative(input.value ?? 1)) {
        rejectedMetrics += 1;
        return;
      }
      const labels = sanitizeMetricLabels(input.labels);
      if (labels === null) {
        rejectedMetrics += 1;
        return;
      }
      const key = `COUNTER:${input.name}:${labelsKey(labels)}`;
      const existing = metricPoints.get(key);
      metricPoints.set(key, Object.freeze({
        name: input.name,
        kind: 'COUNTER',
        labels: Object.freeze({ ...labels }),
        value: (existing?.value ?? 0) + (input.value ?? 1),
        count: (existing?.count ?? 0) + 1,
      }));
    },
    observeHistogram(input) {
      if (!metricNames.has(input.name) || !finiteNonNegative(input.value)) {
        rejectedMetrics += 1;
        return;
      }
      const labels = sanitizeMetricLabels(input.labels);
      if (labels === null) {
        rejectedMetrics += 1;
        return;
      }
      const key = `HISTOGRAM:${input.name}:${labelsKey(labels)}`;
      const existing = metricPoints.get(key);
      metricPoints.set(key, Object.freeze({
        name: input.name,
        kind: 'HISTOGRAM',
        labels: Object.freeze({ ...labels }),
        value: (existing?.value ?? 0) + input.value,
        count: (existing?.count ?? 0) + 1,
      }));
    },
    spans() {
      return [...exported, ...queued].map(cloneSpan);
    },
    metrics() {
      return [...metricPoints.values()].map((point) => ({
        ...point,
        labels: { ...point.labels },
      }));
    },
    diagnostics() {
      return {
        queuedSpans: queued.length,
        exportedSpans: exported.length,
        droppedSpans,
        failedExports,
        rejectedAttributes,
        rejectedMetrics,
      };
    },
    flush,
    async shutdown() {
      shutdown = true;
      await flush();
      try {
        await exporter.shutdown?.();
      } catch {
        failedExports += 1;
      }
    },
  };
  return Object.freeze(telemetry);
};
