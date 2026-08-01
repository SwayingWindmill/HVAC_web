import type {
  BudgetDecision,
  BudgetGuard,
  OwnerReadResult,
  ParallelReadRequest,
  RunResourceBudgetCost,
  RunResourceBudgetDimension,
  RunResourceBudgetOutcome,
  RunResourceBudgetPolicy,
  RunResourceBudgetSnapshot,
  RunResourceBudgetUsage,
} from './ports.js';
import { sha256Hex } from './sha256.js';

const hourMs = 60 * 60 * 1_000;
const dayMs = 24 * hourMs;
const maximumIdentityCharacters = 256;
const maximumTraversalDepth = 32;

export const DEFAULT_RUN_RESOURCE_BUDGET_POLICY: RunResourceBudgetPolicy = Object.freeze({
  schemaVersion: 1,
  revision: 'operations-agent-run-resource-policy/v1',
  limits: Object.freeze({
    modelInvocations: 8,
    toolRequests: 32,
    wallClockMs: 15 * 60 * 1_000,
    queryRangeMs: 31 * dayMs,
    queryBuckets: 2_000,
    ownerRecords: 10_000,
    payloadBytes: 4 * 1_024 * 1_024,
  }),
});

export const ZERO_RUN_RESOURCE_BUDGET_COST: RunResourceBudgetCost = Object.freeze({
  modelInvocations: 0,
  toolRequests: 0,
  queryRangeMs: 0,
  queryBuckets: 0,
  ownerRecords: 0,
  payloadBytes: 0,
});

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0 || value.length > maximumIdentityCharacters) {
    throw new Error(`${label} must contain 1 to ${maximumIdentityCharacters} characters.`);
  }
  return value;
};

const requireNonNegativeSafeInteger = (value: number, label: string): number => {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${label} must be a non-negative safe integer.`);
  }
  return value;
};

const requirePositiveSafeInteger = (value: number, label: string): number => {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive safe integer.`);
  }
  return value;
};

export const normalizeRunResourceBudgetPolicy = (
  input: RunResourceBudgetPolicy,
): RunResourceBudgetPolicy => Object.freeze({
  schemaVersion: 1,
  revision: requireIdentity(input.revision, 'Run resource policy revision'),
  limits: Object.freeze({
    modelInvocations: requirePositiveSafeInteger(
      input.limits.modelInvocations,
      'Model invocation limit',
    ),
    toolRequests: requirePositiveSafeInteger(input.limits.toolRequests, 'Tool request limit'),
    wallClockMs: requirePositiveSafeInteger(input.limits.wallClockMs, 'Wall-clock limit'),
    queryRangeMs: requirePositiveSafeInteger(input.limits.queryRangeMs, 'Query range limit'),
    queryBuckets: requirePositiveSafeInteger(input.limits.queryBuckets, 'Query bucket limit'),
    ownerRecords: requirePositiveSafeInteger(input.limits.ownerRecords, 'Owner record limit'),
    payloadBytes: requirePositiveSafeInteger(input.limits.payloadBytes, 'Payload byte limit'),
  }),
});

export const normalizeRunResourceBudgetCost = (
  input: RunResourceBudgetCost,
): RunResourceBudgetCost => Object.freeze({
  modelInvocations: requireNonNegativeSafeInteger(
    input.modelInvocations,
    'Model invocation cost',
  ),
  toolRequests: requireNonNegativeSafeInteger(input.toolRequests, 'Tool request cost'),
  queryRangeMs: requireNonNegativeSafeInteger(input.queryRangeMs, 'Query range cost'),
  queryBuckets: requireNonNegativeSafeInteger(input.queryBuckets, 'Query bucket cost'),
  ownerRecords: requireNonNegativeSafeInteger(input.ownerRecords, 'Owner record cost'),
  payloadBytes: requireNonNegativeSafeInteger(input.payloadBytes, 'Payload byte cost'),
});

const zeroUsage = (): RunResourceBudgetUsage => Object.freeze({
  modelInvocations: 0,
  toolRequests: 0,
  maximumQueryRangeMs: 0,
  queryBuckets: 0,
  ownerRecords: 0,
  payloadBytes: 0,
});

export const createRunResourceBudgetSnapshot = (input: {
  readonly investigationId: string;
  readonly runId: string;
  readonly policy: RunResourceBudgetPolicy;
  readonly startedAt: number;
}): RunResourceBudgetSnapshot => Object.freeze({
  schemaVersion: 1,
  investigationId: requireIdentity(input.investigationId, 'Investigation identity'),
  runId: requireIdentity(input.runId, 'Agent Run identity'),
  policyRevision: normalizeRunResourceBudgetPolicy(input.policy).revision,
  startedAt: requireNonNegativeSafeInteger(input.startedAt, 'Agent Run start time'),
  usage: zeroUsage(),
  exhaustion: null,
});

const addSafe = (left: number, right: number, label: string): number => {
  const result = left + right;
  if (!Number.isSafeInteger(result)) throw new Error(`${label} is outside the safe integer range.`);
  return result;
};

const partialOutcome = (usage: RunResourceBudgetUsage): 'PARTIAL' | 'UNABLE_TO_CONCLUDE' => (
  usage.ownerRecords > 0 || usage.payloadBytes > 0
    ? 'PARTIAL'
    : 'UNABLE_TO_CONCLUDE'
);

interface ExhaustionCandidate {
  readonly dimension: RunResourceBudgetDimension;
  readonly consumed: number;
  readonly limit: number;
}

const exhaustionCandidate = (
  snapshot: RunResourceBudgetSnapshot,
  policy: RunResourceBudgetPolicy,
  cost: RunResourceBudgetCost,
): ExhaustionCandidate | null => {
  const projectedModelInvocations = addSafe(
    snapshot.usage.modelInvocations,
    cost.modelInvocations,
    'Model invocation usage',
  );
  if (projectedModelInvocations > policy.limits.modelInvocations) {
    return {
      dimension: 'MODEL_INVOCATIONS',
      consumed: projectedModelInvocations,
      limit: policy.limits.modelInvocations,
    };
  }
  const projectedToolRequests = addSafe(
    snapshot.usage.toolRequests,
    cost.toolRequests,
    'Tool request usage',
  );
  if (projectedToolRequests > policy.limits.toolRequests) {
    return {
      dimension: 'TOOL_REQUESTS',
      consumed: projectedToolRequests,
      limit: policy.limits.toolRequests,
    };
  }
  if (cost.queryRangeMs > policy.limits.queryRangeMs) {
    return {
      dimension: 'QUERY_RANGE_MS',
      consumed: cost.queryRangeMs,
      limit: policy.limits.queryRangeMs,
    };
  }
  const projectedQueryBuckets = addSafe(
    snapshot.usage.queryBuckets,
    cost.queryBuckets,
    'Query bucket usage',
  );
  if (projectedQueryBuckets > policy.limits.queryBuckets) {
    return {
      dimension: 'QUERY_BUCKETS',
      consumed: projectedQueryBuckets,
      limit: policy.limits.queryBuckets,
    };
  }
  const projectedOwnerRecords = addSafe(
    snapshot.usage.ownerRecords,
    cost.ownerRecords,
    'Owner record usage',
  );
  if (projectedOwnerRecords > policy.limits.ownerRecords) {
    return {
      dimension: 'OWNER_RECORDS',
      consumed: projectedOwnerRecords,
      limit: policy.limits.ownerRecords,
    };
  }
  const projectedPayloadBytes = addSafe(
    snapshot.usage.payloadBytes,
    cost.payloadBytes,
    'Payload byte usage',
  );
  if (projectedPayloadBytes > policy.limits.payloadBytes) {
    return {
      dimension: 'PAYLOAD_BYTES',
      consumed: projectedPayloadBytes,
      limit: policy.limits.payloadBytes,
    };
  }
  return null;
};

export const evaluateRunResourceBudgetCheck = (input: {
  readonly snapshot: RunResourceBudgetSnapshot;
  readonly policy: RunResourceBudgetPolicy;
  readonly at: number;
  readonly operationAlreadyAccepted: boolean;
  readonly cost: RunResourceBudgetCost;
}): BudgetDecision => {
  const policy = normalizeRunResourceBudgetPolicy(input.policy);
  const cost = normalizeRunResourceBudgetCost(input.cost);
  const at = requireNonNegativeSafeInteger(input.at, 'Budget check time');
  const snapshot = input.snapshot;
  if (snapshot.policyRevision !== policy.revision) {
    throw new Error('Agent Run resource policy revision cannot change after the Run starts.');
  }
  if (snapshot.exhaustion !== null) {
    return Object.freeze({ decision: 'DENY', duplicate: input.operationAlreadyAccepted, snapshot });
  }
  const elapsed = at - snapshot.startedAt;
  if (!Number.isSafeInteger(elapsed) || elapsed < 0) {
    throw new Error('Run resource budget time is outside the valid range.');
  }
  if (elapsed > policy.limits.wallClockMs) {
    const next = Object.freeze({
      ...snapshot,
      exhaustion: Object.freeze({
        dimension: 'WALL_CLOCK_MS' as const,
        at,
        consumed: elapsed,
        limit: policy.limits.wallClockMs,
        outcome: partialOutcome(snapshot.usage),
      }),
    });
    return Object.freeze({ decision: 'DENY', duplicate: input.operationAlreadyAccepted, snapshot: next });
  }
  if (input.operationAlreadyAccepted) {
    return Object.freeze({ decision: 'ALLOW', duplicate: true, snapshot });
  }
  const exhausted = exhaustionCandidate(snapshot, policy, cost);
  if (exhausted !== null) {
    const next = Object.freeze({
      ...snapshot,
      exhaustion: Object.freeze({
        ...exhausted,
        at,
        outcome: partialOutcome(snapshot.usage),
      }),
    });
    return Object.freeze({ decision: 'DENY', duplicate: input.operationAlreadyAccepted, snapshot: next });
  }
  const usage = Object.freeze({
    modelInvocations: addSafe(
      snapshot.usage.modelInvocations,
      cost.modelInvocations,
      'Model invocation usage',
    ),
    toolRequests: addSafe(snapshot.usage.toolRequests, cost.toolRequests, 'Tool request usage'),
    maximumQueryRangeMs: Math.max(snapshot.usage.maximumQueryRangeMs, cost.queryRangeMs),
    queryBuckets: addSafe(snapshot.usage.queryBuckets, cost.queryBuckets, 'Query bucket usage'),
    ownerRecords: addSafe(snapshot.usage.ownerRecords, cost.ownerRecords, 'Owner record usage'),
    payloadBytes: addSafe(snapshot.usage.payloadBytes, cost.payloadBytes, 'Payload byte usage'),
  });
  const next = Object.freeze({ ...snapshot, usage });
  return Object.freeze({ decision: 'ALLOW', duplicate: false, snapshot: next });
};

export const toRunResourceBudgetOutcome = (
  snapshot: RunResourceBudgetSnapshot,
): RunResourceBudgetOutcome | null => snapshot.exhaustion === null
  ? null
  : Object.freeze({
    schemaVersion: 1,
    policyRevision: snapshot.policyRevision,
    outcome: snapshot.exhaustion.outcome,
    exhaustedDimension: snapshot.exhaustion.dimension,
    consumed: snapshot.exhaustion.consumed,
    limit: snapshot.exhaustion.limit,
  });

const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (typeof value !== 'object' || value === null) return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => [key, canonicalize(item)]),
  );
};

export const runResourceReadBatchOperationId = (
  requests: readonly ParallelReadRequest[],
): string => `read-batch:sha256:${sha256Hex(JSON.stringify(canonicalize(requests)))}`;

export const runResourceOwnerResultOperationId = (
  requestId: string,
): string => `owner-result:${requireIdentity(requestId, 'Owner request identity')}`;

export const runResourceOwnerResultBatchOperationId = (
  requests: readonly ParallelReadRequest[],
): string => `owner-result-batch:sha256:${sha256Hex(JSON.stringify(canonicalize(requests)))}`;

export const runResourceEffectOperationId = (
  idempotencyKey: string,
): string => `business-effect:${requireIdentity(idempotencyKey, 'Idempotency Key')}`;

const parseDateTime = (value: string, label: string): number => {
  const parsed = Date.parse(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${label} must be an ISO date-time.`);
  return parsed;
};

const energyQueryCost = (
  request: Extract<ParallelReadRequest, { readonly tool: 'analytics.getEnergySeries' }>,
): { readonly rangeMs: number; readonly buckets: number } => {
  const from = parseDateTime(request.input.from, 'Energy query start');
  const to = parseDateTime(request.input.to, 'Energy query end');
  const rangeMs = to - from;
  if (!Number.isSafeInteger(rangeMs) || rangeMs <= 0) {
    throw new Error('Energy query range must be a positive safe integer duration.');
  }
  const divisor = request.input.granularity === 'hour'
    ? hourMs
    : request.input.granularity === 'day'
      ? dayMs
      : 31 * dayMs;
  return { rangeMs, buckets: Math.ceil(rangeMs / divisor) };
};

export const runResourceReadBatchCost = (
  requests: readonly ParallelReadRequest[],
): RunResourceBudgetCost => {
  let queryRangeMs = 0;
  let queryBuckets = 0;
  for (const request of requests) {
    if (request.tool !== 'analytics.getEnergySeries') continue;
    const query = energyQueryCost(request);
    queryRangeMs = Math.max(queryRangeMs, query.rangeMs);
    queryBuckets = addSafe(queryBuckets, query.buckets, 'Query bucket cost');
  }
  return Object.freeze({
    ...ZERO_RUN_RESOURCE_BUDGET_COST,
    toolRequests: requests.length,
    queryRangeMs,
    queryBuckets,
  });
};

const countArrayEntries = (value: unknown, depth = 0): number => {
  if (depth > maximumTraversalDepth) throw new Error('Owner payload nesting exceeds the budget boundary.');
  if (Array.isArray(value)) {
    return value.reduce(
      (total, item) => addSafe(total, countArrayEntries(item, depth + 1), 'Owner record cost'),
      value.length,
    );
  }
  if (typeof value !== 'object' || value === null) return 0;
  return Object.values(value as Record<string, unknown>).reduce<number>(
    (total, item) => addSafe(total, countArrayEntries(item, depth + 1), 'Owner record cost'),
    0,
  );
};

export const runResourceOwnerResultCost = (
  result: OwnerReadResult,
): RunResourceBudgetCost => {
  const encoded = JSON.stringify(result.payload);
  if (typeof encoded !== 'string') throw new Error('Owner payload must be bounded JSON data.');
  const payloadBytes = new TextEncoder().encode(encoded).byteLength;
  const arrayEntries = countArrayEntries(result.payload);
  return Object.freeze({
    ...ZERO_RUN_RESOURCE_BUDGET_COST,
    ownerRecords: Math.max(1, arrayEntries),
    payloadBytes,
  });
};

export const runResourceOwnerResultBatchCost = (
  results: readonly OwnerReadResult[],
): RunResourceBudgetCost => results.reduce<RunResourceBudgetCost>(
  (total, result) => {
    const cost = runResourceOwnerResultCost(result);
    return Object.freeze({
      ...ZERO_RUN_RESOURCE_BUDGET_COST,
      ownerRecords: addSafe(total.ownerRecords, cost.ownerRecords, 'Owner record batch cost'),
      payloadBytes: addSafe(total.payloadBytes, cost.payloadBytes, 'Payload byte batch cost'),
    });
  },
  ZERO_RUN_RESOURCE_BUDGET_COST,
);

export const createInMemoryRunResourceBudgetGuard = (): BudgetGuard => {
  const snapshots = new Map<string, RunResourceBudgetSnapshot>();
  const policies = new Map<string, RunResourceBudgetPolicy>();
  const acceptedOperations = new Map<string, Set<string>>();
  const keyFor = (investigationId: string, runId: string): string => `${investigationId}:${runId}`;
  return Object.freeze({
    async check(input: Parameters<BudgetGuard['check']>[0]) {
      const operationId = requireIdentity(input.operationId, 'Budget operation identity');
      const policy = normalizeRunResourceBudgetPolicy(input.policy);
      const key = keyFor(input.investigationId, input.runId);
      const savedPolicy = policies.get(key);
      if (savedPolicy !== undefined
        && (savedPolicy.revision !== policy.revision
          || savedPolicy.limits.modelInvocations !== policy.limits.modelInvocations
          || savedPolicy.limits.toolRequests !== policy.limits.toolRequests
          || savedPolicy.limits.wallClockMs !== policy.limits.wallClockMs
          || savedPolicy.limits.queryRangeMs !== policy.limits.queryRangeMs
          || savedPolicy.limits.queryBuckets !== policy.limits.queryBuckets
          || savedPolicy.limits.ownerRecords !== policy.limits.ownerRecords
          || savedPolicy.limits.payloadBytes !== policy.limits.payloadBytes)) {
        throw new Error('Agent Run resource budget policy cannot change after the Run starts.');
      }
      if (savedPolicy === undefined) policies.set(key, policy);
      const current = snapshots.get(key) ?? createRunResourceBudgetSnapshot({
        investigationId: input.investigationId,
        runId: input.runId,
        policy,
        startedAt: input.startedAt,
      });
      if (current.startedAt !== input.startedAt) {
        throw new Error('Agent Run resource budget start time cannot change.');
      }
      const operations = acceptedOperations.get(key) ?? new Set<string>();
      const decision = evaluateRunResourceBudgetCheck({
        snapshot: current,
        policy,
        at: input.at,
        operationAlreadyAccepted: operations.has(operationId),
        cost: input.cost,
      });
      snapshots.set(key, decision.snapshot);
      if (decision.decision === 'ALLOW' && !decision.duplicate) {
        operations.add(operationId);
        acceptedOperations.set(key, operations);
      }
      return decision;
    },
    async get(investigationId: string, runId: string) {
      return snapshots.get(keyFor(investigationId, runId)) ?? null;
    },
  });
};
