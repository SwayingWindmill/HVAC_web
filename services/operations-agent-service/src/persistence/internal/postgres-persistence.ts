import {
  Pool,
  type PoolClient,
  type QueryResultRow,
} from 'pg';

import type {
  AgentSessionStateStore,
  ApplicationEvent,
  ApplicationOutbox,
  AuditRecord,
  AuditRecorder,
  BudgetGuard,
  OperationsAuditDeliveryRepository,
  CheckpointRepository,
  InvestigationBusinessRecordRepository,
  InvestigationRepository,
  InvestigationTransaction,
  RunResourceBudgetDimension,
  RunResourceBudgetPolicy,
  RunResourceBudgetSnapshot,
  RuntimeCheckpoint,
} from '../../application/index.js';
import {
  OperationsInvestigation,
  createInvestigationBusinessRecord,
  type CommittedEffectView,
  type InvestigationBusinessRecord,
  type OperationsInvestigationSnapshot,
} from '../../domain/index.js';
import { createPostgresAgentSessionStateStore } from './postgres-agent-session-store.js';

import {
  InvestigationRepositoryConflictError,
  createRunResourceBudgetSnapshot,
  evaluateRunResourceBudgetCheck,
  normalizeRunResourceBudgetPolicy,
  parseOperationsAuditEvent,
} from '../../application/index.js';

export interface PostgresOperationsAgentPersistenceOptions {
  readonly operationsConnectionString: string;
  readonly checkpointsConnectionString: string;
  readonly checkpointRetentionMs: number;
  readonly now?: () => number;
  readonly maxPoolSize?: number;
}

export interface PostgresCheckpointRepository extends CheckpointRepository {
  deleteExpired(at?: number): Promise<number>;
}

export interface PostgresOperationsAgentPersistence {
  readonly agentSessionStateStore: AgentSessionStateStore;
  readonly investigationRepository: InvestigationRepository;
  readonly businessRecordRepository: InvestigationBusinessRecordRepository;
  readonly investigationTransaction: InvestigationTransaction;
  readonly checkpointRepository: PostgresCheckpointRepository;
  readonly budgetGuard: BudgetGuard;
  readonly applicationOutbox: ApplicationOutbox;
  readonly auditRecorder: AuditRecorder;
  readonly auditDeliveryRepository: OperationsAuditDeliveryRepository;
  close(): Promise<void>;
}

interface InvestigationRow extends QueryResultRow {
  readonly revision: string;
  readonly active_run_id: string | null;
  readonly active_lease_id: string | null;
  readonly active_lease_expires_at_ms: string | null;
  readonly snapshot: unknown;
}

interface SnapshotRow extends QueryResultRow {
  readonly snapshot: unknown;
}

interface BusinessRecordRow extends QueryResultRow {
  readonly record_payload: unknown;
}

interface CheckpointRow extends QueryResultRow {
  readonly checkpoint_id: string;
  readonly investigation_id: string;
  readonly run_id: string;
  readonly runtime_revision: string;
  readonly position: string;
  readonly opaque_state: string;
  readonly saved_at_ms: string;
}

interface BooleanRow extends QueryResultRow {
  readonly value: boolean;
}

interface AuditDeliveryRow extends QueryResultRow {
  readonly audit_payload: unknown;
  readonly attempt_count: number;
  readonly next_attempt_at_ms: string;
}

interface RunResourceBudgetRow extends QueryResultRow {
  readonly investigation_id: string;
  readonly run_id: string;
  readonly policy_revision: string;
  readonly started_at_ms: string;
  readonly limit_model_invocations: string;
  readonly limit_tool_requests: string;
  readonly limit_wall_clock_ms: string;
  readonly limit_query_range_ms: string;
  readonly limit_query_buckets: string;
  readonly limit_owner_records: string;
  readonly limit_payload_bytes: string;
  readonly model_invocations: string;
  readonly tool_requests: string;
  readonly maximum_query_range_ms: string;
  readonly query_buckets: string;
  readonly owner_records: string;
  readonly payload_bytes: string;
  readonly exhausted_dimension: RunResourceBudgetDimension | null;
  readonly exhausted_at_ms: string | null;
  readonly exhausted_consumed: string | null;
  readonly exhausted_limit: string | null;
  readonly exhausted_outcome: 'PARTIAL' | 'UNABLE_TO_CONCLUDE' | null;
}

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0) throw new Error(`${label} must not be empty.`);
  return value;
};

const requirePositiveSafeInteger = (value: number, label: string): number => {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive safe integer.`);
  }
  return value;
};

const toSafeInteger = (value: string | number, label: string): number => {
  const parsed = typeof value === 'number' ? value : Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${label} is outside the safe integer range.`);
  return parsed;
};

const budgetPolicyFromRow = (row: RunResourceBudgetRow): RunResourceBudgetPolicy => ({
  schemaVersion: 1,
  revision: row.policy_revision,
  limits: {
    modelInvocations: toSafeInteger(row.limit_model_invocations, 'Model invocation limit'),
    toolRequests: toSafeInteger(row.limit_tool_requests, 'Tool request limit'),
    wallClockMs: toSafeInteger(row.limit_wall_clock_ms, 'Wall-clock limit'),
    queryRangeMs: toSafeInteger(row.limit_query_range_ms, 'Query range limit'),
    queryBuckets: toSafeInteger(row.limit_query_buckets, 'Query bucket limit'),
    ownerRecords: toSafeInteger(row.limit_owner_records, 'Owner record limit'),
    payloadBytes: toSafeInteger(row.limit_payload_bytes, 'Payload byte limit'),
  },
});

const budgetSnapshotFromRow = (row: RunResourceBudgetRow): RunResourceBudgetSnapshot => ({
  schemaVersion: 1,
  investigationId: row.investigation_id,
  runId: row.run_id,
  policyRevision: row.policy_revision,
  startedAt: toSafeInteger(row.started_at_ms, 'Run budget startedAt'),
  usage: {
    modelInvocations: toSafeInteger(row.model_invocations, 'Model invocation usage'),
    toolRequests: toSafeInteger(row.tool_requests, 'Tool request usage'),
    maximumQueryRangeMs: toSafeInteger(row.maximum_query_range_ms, 'Maximum query range'),
    queryBuckets: toSafeInteger(row.query_buckets, 'Query bucket usage'),
    ownerRecords: toSafeInteger(row.owner_records, 'Owner record usage'),
    payloadBytes: toSafeInteger(row.payload_bytes, 'Payload byte usage'),
  },
  exhaustion: row.exhausted_dimension === null
    ? null
    : {
      dimension: row.exhausted_dimension,
      at: toSafeInteger(row.exhausted_at_ms as string, 'Budget exhaustion time'),
      consumed: toSafeInteger(row.exhausted_consumed as string, 'Budget exhaustion usage'),
      limit: toSafeInteger(row.exhausted_limit as string, 'Budget exhaustion limit'),
      outcome: row.exhausted_outcome as 'PARTIAL' | 'UNABLE_TO_CONCLUDE',
    },
});

const sameBudgetPolicy = (
  left: RunResourceBudgetPolicy,
  right: RunResourceBudgetPolicy,
): boolean => left.revision === right.revision
  && left.limits.modelInvocations === right.limits.modelInvocations
  && left.limits.toolRequests === right.limits.toolRequests
  && left.limits.wallClockMs === right.limits.wallClockMs
  && left.limits.queryRangeMs === right.limits.queryRangeMs
  && left.limits.queryBuckets === right.limits.queryBuckets
  && left.limits.ownerRecords === right.limits.ownerRecords
  && left.limits.payloadBytes === right.limits.payloadBytes;

const postgresCode = (error: unknown): string | null => (
  typeof error === 'object'
    && error !== null
    && 'code' in error
    && typeof error.code === 'string'
    ? error.code
    : null
);

const withTransaction = async <T>(pool: Pool, work: (client: PoolClient) => Promise<T>): Promise<T> => {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    const result = await work(client);
    await client.query('COMMIT');
    return result;
  } catch (error) {
    try { await client.query('ROLLBACK'); } catch {}
    throw error;
  } finally {
    client.release();
  }
};

const restoreSnapshot = (value: unknown): OperationsInvestigation => (
  OperationsInvestigation.restore(value as OperationsInvestigationSnapshot)
);

const activeLeaseColumns = (snapshot: OperationsInvestigationSnapshot): {
  readonly activeRunId: string | null;
  readonly activeLeaseId: string | null;
  readonly activeLeaseExpiresAt: number | null;
} => {
  if (snapshot.activeRunId === null) {
    return { activeRunId: null, activeLeaseId: null, activeLeaseExpiresAt: null };
  }
  const run = snapshot.runs.find((candidate) => candidate.id === snapshot.activeRunId);
  if (run === undefined) throw new Error('Active Agent Run is missing from the Investigation snapshot.');
  return {
    activeRunId: run.id,
    activeLeaseId: run.lease?.id ?? null,
    activeLeaseExpiresAt: run.lease?.expiresAt ?? null,
  };
};

const effectsEqual = (left: CommittedEffectView, right: CommittedEffectView): boolean => (
  left.runId === right.runId
  && left.stepId === right.stepId
  && left.idempotencyKey === right.idempotencyKey
  && left.kind === right.kind
  && left.recordId === right.recordId
  && left.committedAt === right.committedAt
);

const validateImmutableFields = (
  current: OperationsInvestigationSnapshot,
  next: OperationsInvestigationSnapshot,
): void => {
  if (current.id !== next.id
    || current.createdAt !== next.createdAt
    || current.scope.tenantId !== next.scope.tenantId
    || current.scope.siteId !== next.scope.siteId
    || current.scope.assetId !== next.scope.assetId
    || current.scope.deviceId !== next.scope.deviceId) {
    throw new InvestigationRepositoryConflictError(
      'IDENTITY_CONFLICT',
      'Investigation identity, Scope, and creation time are immutable.',
    );
  }
};

const validateEffectTransition = (
  current: OperationsInvestigationSnapshot,
  next: OperationsInvestigationSnapshot,
  effect: CommittedEffectView | undefined,
): void => {
  const currentEffects = current.committedEffects;
  const nextEffects = next.committedEffects;
  const prefixMatches = currentEffects.every((candidate, index) => (
    nextEffects[index] !== undefined && effectsEqual(candidate, nextEffects[index])
  ));
  if (!prefixMatches) {
    throw new InvestigationRepositoryConflictError(
      'DUPLICATE_EFFECT',
      'Committed effect history cannot be rewritten.',
    );
  }
  if (effect === undefined) {
    if (nextEffects.length !== currentEffects.length) {
      throw new InvestigationRepositoryConflictError(
        'DUPLICATE_EFFECT',
        'A business effect requires explicit transaction metadata.',
      );
    }
    return;
  }
  const appended = nextEffects[currentEffects.length];
  if (nextEffects.length !== currentEffects.length + 1
    || appended === undefined
    || !effectsEqual(appended, effect)) {
    throw new InvestigationRepositoryConflictError(
      'DUPLICATE_EFFECT',
      'Effect transaction metadata does not match the Investigation snapshot.',
    );
  }
};

const scopeContains = (
  investigation: OperationsInvestigationSnapshot['scope'],
  candidate: OperationsInvestigationSnapshot['scope'],
): boolean => investigation.tenantId === candidate.tenantId
  && (investigation.siteId === null || investigation.siteId === candidate.siteId)
  && (investigation.assetId === null || investigation.assetId === candidate.assetId)
  && (investigation.deviceId === null || investigation.deviceId === candidate.deviceId);

const validateBusinessRecord = (
  current: OperationsInvestigationSnapshot,
  next: OperationsInvestigationSnapshot,
  effect: CommittedEffectView | undefined,
  record: InvestigationBusinessRecord | undefined,
): void => {
  if (record === undefined) {
    if (effect !== undefined && effect.kind !== 'PROPOSED_ACTION') {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        `${effect.kind} effects require a typed business record.`,
      );
    }
    return;
  }
  if (record.recordType === 'OPERATOR_INPUT_ACCEPTED') {
    const appended = next.operatorInputAcceptances[current.operatorInputAcceptances.length];
    const request = current.activeOperatorInputRequest;
    if (effect !== undefined
      || next.operatorInputAcceptances.length !== current.operatorInputAcceptances.length + 1
      || next.acceptedOperatorInputIds.length !== current.acceptedOperatorInputIds.length + 1
      || appended === undefined
      || request === null
      || current.status !== 'WAITING_FOR_OPERATOR_INPUT'
      || next.status !== 'RUNNING'
      || record.investigationId !== current.id
      || record.id !== appended.recordId
      || record.recordedAt !== appended.acceptedAt
      || record.requestId !== request.id
      || record.requestId !== appended.requestId
      || record.runId !== request.runId
      || record.runId !== appended.runId
      || record.idempotencyKey !== appended.idempotencyKey
      || record.inputKind !== request.kind
      || record.inputKind !== appended.kind
      || record.inputDigest !== appended.inputDigest
      || record.scope.tenantId !== current.scope.tenantId
      || record.scope.siteId !== current.scope.siteId
      || record.scope.assetId !== current.scope.assetId
      || record.scope.deviceId !== current.scope.deviceId) {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        'Accepted Operator Input record does not match the authoritative interrupt transition.',
      );
    }
    return;
  }
  if (effect === undefined
    || record.investigationId !== current.id
    || record.id !== effect.recordId
    || record.recordType !== effect.kind
    || record.recordedAt !== effect.committedAt) {
    throw new InvestigationRepositoryConflictError(
      'RECORD_REFERENCE_CONFLICT',
      'Business record identity, type, and timestamp must match its committed effect.',
    );
  }
  if (record.recordType === 'EVIDENCE') {
    if (record.sources.some(({ scope }) => !scopeContains(current.scope, scope))) {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        'Evidence source Scope is outside the Investigation Scope.',
      );
    }
    return;
  }
  if (record.recordType === 'ANALYSIS_REFERENCE') {
    if (record.inputEvidenceIds.some((identity) => !current.evidenceIds.includes(identity))) {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        'Analysis Reference cites Evidence that is not committed to the Investigation.',
      );
    }
    return;
  }
  if (record.recordType === 'FINDING') {
    if (record.evidenceIds.some((identity) => !current.evidenceIds.includes(identity))
      || record.analysisReferenceIds.some((identity) => (
        !current.analysisReferenceIds.includes(identity)
      ))) {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        'Finding support references are not committed to the Investigation.',
      );
    }
    if (record.conclusion.status === 'SUPPORTED'
      && (record.conclusion.tenantId !== current.scope.tenantId
        || record.conclusion.siteId !== current.scope.siteId)) {
      throw new InvestigationRepositoryConflictError(
        'RECORD_REFERENCE_CONFLICT',
        'Supported Site Finding conclusion is outside the Investigation Scope.',
      );
    }
    return;
  }
  if (record.runId !== effect.runId || record.stepId !== effect.stepId) {
    throw new InvestigationRepositoryConflictError(
      'RECORD_REFERENCE_CONFLICT',
      'Tool Execution Receipt Run and Step must match its committed effect.',
    );
  }
};

const insertBusinessRecord = async (
  client: PoolClient,
  record: InvestigationBusinessRecord,
): Promise<void> => {
  const toolOwner = record.recordType === 'TOOL_EXECUTION_RECEIPT' ? record.owner : null;
  const toolRequestId = record.recordType === 'TOOL_EXECUTION_RECEIPT'
    ? record.requestId
    : null;
  const toolAttemptId = record.recordType === 'TOOL_EXECUTION_RECEIPT'
    ? record.attemptId
    : null;
  try {
    await client.query(
      `INSERT INTO agent_operations.investigation_business_records (
        investigation_id,
        record_id,
        record_type,
        schema_version,
        record_payload,
        recorded_at_ms,
        tool_owner,
        tool_request_id,
        tool_attempt_id
      ) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)`,
      [
        record.investigationId,
        record.id,
        record.recordType,
        record.schemaVersion,
        JSON.stringify(record),
        record.recordedAt,
        toolOwner,
        toolRequestId,
        toolAttemptId,
      ],
    );
  } catch (error) {
    if (postgresCode(error) === '23505') {
      throw new InvestigationRepositoryConflictError(
        'DUPLICATE_RECORD',
        'Business record identity or Tool request-attempt identity already exists.',
      );
    }
    throw error;
  }
};

const insertEvent = async (client: PoolClient, event: ApplicationEvent): Promise<void> => {
  await client.query(
    `INSERT INTO agent_operations.application_outbox (
      investigation_id,
      event_type,
      investigation_revision,
      event_payload,
      occurred_at_ms
    ) VALUES ($1, $2, $3, $4::jsonb, $5)`,
    [
      event.investigationId,
      event.type,
      event.revision,
      JSON.stringify(event),
      event.occurredAt,
    ],
  );
};

const insertAudit = async (client: PoolClient, audit: AuditRecord): Promise<void> => {
  const event = parseOperationsAuditEvent(audit);
  const inserted = await client.query<BusinessRecordRow>(
    `INSERT INTO agent_operations.audit_records (
      event_id,
      investigation_id,
      tenant_id,
      site_id,
      run_id,
      action,
      investigation_revision,
      audit_payload,
      occurred_at_ms,
      delivery_status,
      attempt_count,
      next_attempt_at_ms
    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, 'PENDING', 0, $9)
    ON CONFLICT (event_id) DO NOTHING
    RETURNING audit_payload AS record_payload`,
    [
      event.eventId,
      event.investigationId,
      event.tenantId,
      event.siteId,
      event.runId,
      event.operation,
      event.investigationRevision,
      JSON.stringify(event),
      event.occurredAt,
    ],
  );
  if (inserted.rowCount === 1) return;
  const existing = await client.query<BusinessRecordRow>(
    `SELECT audit_payload AS record_payload
     FROM agent_operations.audit_records
     WHERE event_id = $1`,
    [event.eventId],
  );
  const row = existing.rows[0];
  if (row === undefined
    || JSON.stringify(parseOperationsAuditEvent(row.record_payload)) !== JSON.stringify(event)) {
    throw new InvestigationRepositoryConflictError(
      'RECORD_REFERENCE_CONFLICT',
      'Operations Audit event identity was reused with different content.',
    );
  }
};

const insertEffect = async (
  client: PoolClient,
  investigationId: string,
  effect: CommittedEffectView,
): Promise<void> => {
  try {
    await client.query(
      `INSERT INTO agent_operations.investigation_effects (
        investigation_id,
        idempotency_key,
        run_id,
        step_id,
        effect_kind,
        record_id,
        committed_at_ms
      ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
      [
        investigationId,
        effect.idempotencyKey,
        effect.runId,
        effect.stepId,
        effect.kind,
        effect.recordId,
        effect.committedAt,
      ],
    );
  } catch (error) {
    if (postgresCode(error) === '23505') {
      throw new InvestigationRepositoryConflictError(
        'DUPLICATE_EFFECT',
        'Idempotency Key or business effect record already exists.',
      );
    }
    throw error;
  }
};

export const createPostgresOperationsAgentPersistence = (
  options: PostgresOperationsAgentPersistenceOptions,
): PostgresOperationsAgentPersistence => {
  requireIdentity(options.operationsConnectionString, 'Operations database connection string');
  requireIdentity(options.checkpointsConnectionString, 'Checkpoint database connection string');
  const checkpointRetentionMs = requirePositiveSafeInteger(
    options.checkpointRetentionMs,
    'checkpointRetentionMs',
  );
  const max = options.maxPoolSize === undefined
    ? 4
    : requirePositiveSafeInteger(options.maxPoolSize, 'maxPoolSize');
  const now = options.now ?? Date.now;
  const operationsPool = new Pool({
    connectionString: options.operationsConnectionString,
    application_name: 'operations-agent-operations',
    max,
  });
  const checkpointsPool = new Pool({
    connectionString: options.checkpointsConnectionString,
    application_name: 'operations-agent-checkpoints',
    max,
  });
  const agentSessionStateStore = createPostgresAgentSessionStateStore(operationsPool);

  const investigationRepository: InvestigationRepository = {
    async get(investigationId) {
      const result = await operationsPool.query<SnapshotRow>(
        `SELECT snapshot
         FROM agent_operations.investigations
         WHERE investigation_id = $1`,
        [investigationId],
      );
      const row = result.rows[0];
      return row === undefined ? null : restoreSnapshot(row.snapshot);
    },
    async listByScope(input) {
      const result = await operationsPool.query<SnapshotRow>(
        `SELECT snapshot
         FROM agent_operations.investigations
         WHERE snapshot->'scope'->>'tenantId' = $1
           AND snapshot->'scope'->>'siteId' = $2
         ORDER BY created_at_ms DESC, investigation_id DESC
         LIMIT $3`,
        [input.tenantId, input.siteId, input.limit],
      );
      return result.rows.map((row) => restoreSnapshot(row.snapshot));
    },
  };

  const businessRecordRepository: InvestigationBusinessRecordRepository = {
    async get(investigationId, recordId) {
      const result = await operationsPool.query<BusinessRecordRow>(
        `SELECT record_payload
         FROM agent_operations.investigation_business_records
         WHERE investigation_id = $1 AND record_id = $2`,
        [investigationId, recordId],
      );
      const row = result.rows[0];
      return row === undefined
        ? null
        : createInvestigationBusinessRecord(row.record_payload);
    },
  };

  const investigationTransaction: InvestigationTransaction = {
    async create(input) {
      const snapshot = input.investigation.snapshot();
      if (snapshot.revision !== 0) {
        throw new InvestigationRepositoryConflictError(
          'REVISION_CONFLICT',
          'A new Investigation must start at revision zero.',
        );
      }
      const active = activeLeaseColumns(snapshot);
      try {
        await withTransaction(operationsPool, async (client) => {
          await client.query(
            `INSERT INTO agent_operations.investigations (
              investigation_id,
              revision,
              status,
              active_run_id,
              active_lease_id,
              active_lease_expires_at_ms,
              snapshot,
              created_at_ms,
              updated_at_ms
            ) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)`,
            [
              snapshot.id,
              snapshot.revision,
              snapshot.status,
              active.activeRunId,
              active.activeLeaseId,
              active.activeLeaseExpiresAt,
              JSON.stringify(snapshot),
              snapshot.createdAt,
              input.event.occurredAt,
            ],
          );
          await insertEvent(client, input.event);
          await insertAudit(client, input.audit);
        });
      } catch (error) {
        if (postgresCode(error) === '23505') {
          throw new InvestigationRepositoryConflictError(
            'IDENTITY_CONFLICT',
            `Investigation ${snapshot.id} already exists.`,
          );
        }
        throw error;
      }
    },

    async save(input) {
      const nextSnapshot = input.investigation.snapshot();
      if (nextSnapshot.revision !== input.expectedRevision + 1) {
        throw new InvestigationRepositoryConflictError(
          'REVISION_CONFLICT',
          'The next Investigation Revision must advance exactly once.',
        );
      }
      const active = activeLeaseColumns(nextSnapshot);
      await withTransaction(operationsPool, async (client) => {
        const selected = await client.query<InvestigationRow>(
          `SELECT
            revision::text,
            active_run_id,
            active_lease_id,
            active_lease_expires_at_ms::text,
            snapshot
           FROM agent_operations.investigations
           WHERE investigation_id = $1
           FOR UPDATE`,
          [nextSnapshot.id],
        );
        const row = selected.rows[0];
        if (row === undefined) {
          throw new InvestigationRepositoryConflictError(
            'REVISION_CONFLICT',
            `Investigation ${nextSnapshot.id} does not exist.`,
          );
        }
        const persistedRevision = toSafeInteger(row.revision, 'Persisted Investigation Revision');
        if (persistedRevision !== input.expectedRevision) {
          throw new InvestigationRepositoryConflictError(
            'REVISION_CONFLICT',
            `Expected Investigation Revision ${input.expectedRevision}, current revision is ${persistedRevision}.`,
          );
        }
        if (input.expectedAuthority !== undefined) {
          const persistedExpiry = row.active_lease_expires_at_ms === null
            ? null
            : toSafeInteger(row.active_lease_expires_at_ms, 'Persisted Agent Run Lease expiry');
          if (row.active_run_id !== input.expectedAuthority.runId
            || row.active_lease_id !== input.expectedAuthority.leaseId
            || persistedExpiry === null
            || input.expectedAuthority.at >= persistedExpiry) {
            throw new InvestigationRepositoryConflictError(
              'LEASE_CONFLICT',
              'Persisted Agent Run Lease does not authorize this business write.',
            );
          }
        }

        const currentSnapshot = restoreSnapshot(row.snapshot).snapshot();
        const record = input.record === undefined
          ? undefined
          : createInvestigationBusinessRecord(input.record);
        validateImmutableFields(currentSnapshot, nextSnapshot);
        validateEffectTransition(currentSnapshot, nextSnapshot, input.effect);
        validateBusinessRecord(currentSnapshot, nextSnapshot, input.effect, record);
        if (record !== undefined) {
          await insertBusinessRecord(client, record);
        }
        if (input.effect !== undefined) {
          await insertEffect(client, nextSnapshot.id, input.effect);
        }
        await client.query(
          `UPDATE agent_operations.investigations
           SET revision = $2,
               status = $3,
               active_run_id = $4,
               active_lease_id = $5,
               active_lease_expires_at_ms = $6,
               snapshot = $7::jsonb,
               updated_at_ms = $8
           WHERE investigation_id = $1`,
          [
            nextSnapshot.id,
            nextSnapshot.revision,
            nextSnapshot.status,
            active.activeRunId,
            active.activeLeaseId,
            active.activeLeaseExpiresAt,
            JSON.stringify(nextSnapshot),
            input.event.occurredAt,
          ],
        );
        await insertEvent(client, input.event);
        await insertAudit(client, input.audit);
      });
    },
  };

  const checkpointRepository: PostgresCheckpointRepository = {
    async save(checkpoint) {
      const expiresAt = checkpoint.savedAt + checkpointRetentionMs;
      if (!Number.isSafeInteger(expiresAt)) {
        throw new Error('Checkpoint expiry is outside the safe integer range.');
      }
      await checkpointsPool.query(
        `INSERT INTO agent_checkpoints.runtime_checkpoints (
          checkpoint_id,
          investigation_id,
          run_id,
          runtime_revision,
          position,
          opaque_state,
          saved_at_ms,
          expires_at_ms
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
        [
          checkpoint.id,
          checkpoint.investigationId,
          checkpoint.runId,
          checkpoint.runtimeRevision,
          checkpoint.position,
          checkpoint.opaqueState,
          checkpoint.savedAt,
          expiresAt,
        ],
      );
    },

    async load(investigationId, runId) {
      const result = await checkpointsPool.query<CheckpointRow>(
        `SELECT
          checkpoint_id,
          investigation_id,
          run_id,
          runtime_revision,
          position,
          opaque_state,
          saved_at_ms::text
         FROM agent_checkpoints.runtime_checkpoints
         WHERE investigation_id = $1
           AND run_id = $2
           AND expires_at_ms > $3
         ORDER BY saved_at_ms DESC, checkpoint_id DESC
         LIMIT 1`,
        [investigationId, runId, now()],
      );
      const row = result.rows[0];
      if (row === undefined) return null;
      return {
        id: row.checkpoint_id,
        investigationId: row.investigation_id,
        runId: row.run_id,
        runtimeRevision: row.runtime_revision,
        position: row.position,
        opaqueState: row.opaque_state,
        savedAt: toSafeInteger(row.saved_at_ms, 'Checkpoint savedAt'),
      } satisfies RuntimeCheckpoint;
    },

    async delete(investigationId, runId) {
      await checkpointsPool.query(
        `DELETE FROM agent_checkpoints.runtime_checkpoints
         WHERE investigation_id = $1 AND run_id = $2`,
        [investigationId, runId],
      );
    },

    async deleteExpired(at = now()) {
      const result = await checkpointsPool.query(
        `DELETE FROM agent_checkpoints.runtime_checkpoints
         WHERE expires_at_ms <= $1`,
        [at],
      );
      return result.rowCount ?? 0;
    },
  };

  const runResourceBudgetColumns = `
    investigation_id,
    run_id,
    policy_revision,
    started_at_ms,
    limit_model_invocations,
    limit_tool_requests,
    limit_wall_clock_ms,
    limit_query_range_ms,
    limit_query_buckets,
    limit_owner_records,
    limit_payload_bytes,
    model_invocations,
    tool_requests,
    maximum_query_range_ms,
    query_buckets,
    owner_records,
    payload_bytes,
    exhausted_dimension,
    exhausted_at_ms,
    exhausted_consumed,
    exhausted_limit,
    exhausted_outcome`;

  const budgetGuard: BudgetGuard = {
    async check(input) {
      const operationId = requireIdentity(input.operationId, 'Budget operation identity');
      if (operationId.length > 256) {
        throw new Error('Budget operation identity must not exceed 256 characters.');
      }
      const policy = normalizeRunResourceBudgetPolicy(input.policy);
      const initial = createRunResourceBudgetSnapshot({
        investigationId: input.investigationId,
        runId: input.runId,
        policy,
        startedAt: input.startedAt,
      });
      return withTransaction(operationsPool, async (client) => {
        await client.query(
          `INSERT INTO agent_operations.run_resource_budgets (
             investigation_id,
             run_id,
             policy_revision,
             started_at_ms,
             limit_model_invocations,
             limit_tool_requests,
             limit_wall_clock_ms,
             limit_query_range_ms,
             limit_query_buckets,
             limit_owner_records,
             limit_payload_bytes
           ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
           ON CONFLICT (investigation_id, run_id) DO NOTHING`,
          [
            initial.investigationId,
            initial.runId,
            policy.revision,
            initial.startedAt,
            policy.limits.modelInvocations,
            policy.limits.toolRequests,
            policy.limits.wallClockMs,
            policy.limits.queryRangeMs,
            policy.limits.queryBuckets,
            policy.limits.ownerRecords,
            policy.limits.payloadBytes,
          ],
        );
        const selected = await client.query<RunResourceBudgetRow>(
          `SELECT ${runResourceBudgetColumns}
           FROM agent_operations.run_resource_budgets
           WHERE investigation_id = $1 AND run_id = $2
           FOR UPDATE`,
          [initial.investigationId, initial.runId],
        );
        const row = selected.rows[0];
        if (row === undefined) throw new Error('Agent Run resource budget was not persisted.');
        if (toSafeInteger(row.started_at_ms, 'Run budget startedAt') !== initial.startedAt) {
          throw new Error('Agent Run resource budget start time cannot change.');
        }
        if (!sameBudgetPolicy(budgetPolicyFromRow(row), policy)) {
          throw new Error('Agent Run resource budget policy cannot change after the Run starts.');
        }
        const duplicateResult = await client.query<BooleanRow>(
          `SELECT EXISTS (
             SELECT 1
             FROM agent_operations.run_resource_budget_operations
             WHERE investigation_id = $1 AND run_id = $2 AND operation_id = $3
           ) AS value`,
          [initial.investigationId, initial.runId, operationId],
        );
        const operationAlreadyAccepted = duplicateResult.rows[0]?.value ?? false;
        const decision = evaluateRunResourceBudgetCheck({
          snapshot: budgetSnapshotFromRow(row),
          policy,
          at: input.at,
          operationAlreadyAccepted,
          cost: input.cost,
        });
        const snapshot = decision.snapshot;
        await client.query(
          `UPDATE agent_operations.run_resource_budgets
           SET model_invocations = $3,
               tool_requests = $4,
               maximum_query_range_ms = $5,
               query_buckets = $6,
               owner_records = $7,
               payload_bytes = $8,
               exhausted_dimension = $9,
               exhausted_at_ms = $10,
               exhausted_consumed = $11,
               exhausted_limit = $12,
               exhausted_outcome = $13,
               updated_at = clock_timestamp()
           WHERE investigation_id = $1 AND run_id = $2`,
          [
            snapshot.investigationId,
            snapshot.runId,
            snapshot.usage.modelInvocations,
            snapshot.usage.toolRequests,
            snapshot.usage.maximumQueryRangeMs,
            snapshot.usage.queryBuckets,
            snapshot.usage.ownerRecords,
            snapshot.usage.payloadBytes,
            snapshot.exhaustion?.dimension ?? null,
            snapshot.exhaustion?.at ?? null,
            snapshot.exhaustion?.consumed ?? null,
            snapshot.exhaustion?.limit ?? null,
            snapshot.exhaustion?.outcome ?? null,
          ],
        );
        if (decision.decision === 'ALLOW' && !decision.duplicate) {
          const inserted = await client.query(
            `INSERT INTO agent_operations.run_resource_budget_operations (
               investigation_id,
               run_id,
               operation_id,
               accepted_at_ms
             ) VALUES ($1, $2, $3, $4)
             ON CONFLICT (investigation_id, run_id, operation_id) DO NOTHING`,
            [snapshot.investigationId, snapshot.runId, operationId, input.at],
          );
          if (inserted.rowCount !== 1) {
            throw new Error('Budget operation identity was concurrently reused.');
          }
        }
        return decision;
      });
    },

    async get(investigationId, runId) {
      const selected = await operationsPool.query<RunResourceBudgetRow>(
        `SELECT ${runResourceBudgetColumns}
         FROM agent_operations.run_resource_budgets
         WHERE investigation_id = $1 AND run_id = $2`,
        [
          requireIdentity(investigationId, 'Investigation identity'),
          requireIdentity(runId, 'Run identity'),
        ],
      );
      const row = selected.rows[0];
      return row === undefined ? null : budgetSnapshotFromRow(row);
    },
  };

  const applicationOutbox: ApplicationOutbox = {
    async append(event) {
      await withTransaction(operationsPool, (client) => insertEvent(client, event));
    },
  };

  const auditRecorder: AuditRecorder = {
    async record(audit) {
      await withTransaction(operationsPool, (client) => insertAudit(client, audit));
    },
  };

  const auditDeliveryRepository: OperationsAuditDeliveryRepository = {
    async claim(input) {
      const at = requirePositiveSafeInteger(input.at, 'Audit claim time');
      const limit = requirePositiveSafeInteger(input.limit, 'Audit claim limit');
      const leaseDurationMs = requirePositiveSafeInteger(
        input.leaseDurationMs,
        'Audit delivery lease duration',
      );
      if (limit > 100) throw new Error('Audit claim limit must not exceed 100.');
      const leaseUntil = at + leaseDurationMs;
      if (!Number.isSafeInteger(leaseUntil)) {
        throw new Error('Audit delivery lease expiry is outside the safe integer range.');
      }
      return withTransaction(operationsPool, async (client) => {
        const claimed = await client.query<AuditDeliveryRow>(
          `WITH candidates AS (
             SELECT audit_id
             FROM agent_operations.audit_records
             WHERE delivery_status IN ('PENDING', 'FAILED', 'IN_FLIGHT')
               AND next_attempt_at_ms <= $1
               AND (lease_until_ms IS NULL OR lease_until_ms <= $1)
             ORDER BY next_attempt_at_ms, audit_id
             FOR UPDATE SKIP LOCKED
             LIMIT $2
           )
           UPDATE agent_operations.audit_records AS audit
           SET delivery_status = 'IN_FLIGHT',
               attempt_count = audit.attempt_count + 1,
               lease_until_ms = $3,
               last_failure_class = NULL
           FROM candidates
           WHERE audit.audit_id = candidates.audit_id
           RETURNING audit.audit_payload,
                     audit.attempt_count,
                     audit.next_attempt_at_ms::text`,
          [at, limit, leaseUntil],
        );
        return Object.freeze(claimed.rows.map((row) => Object.freeze({
          event: parseOperationsAuditEvent(row.audit_payload),
          attemptCount: row.attempt_count,
          nextAttemptAt: toSafeInteger(row.next_attempt_at_ms, 'Audit next attempt time'),
        })));
      });
    },

    async markDelivered(eventId, deliveredAt) {
      requireIdentity(eventId, 'Operations Audit event identity');
      const at = requirePositiveSafeInteger(deliveredAt, 'Audit delivery time');
      const result = await operationsPool.query(
        `UPDATE agent_operations.audit_records
         SET delivery_status = 'DELIVERED',
             delivered_at_ms = COALESCE(delivered_at_ms, $2),
             lease_until_ms = NULL,
             last_failure_class = NULL
         WHERE event_id = $1`,
        [eventId, at],
      );
      if (result.rowCount !== 1) throw new Error('Operations Audit event was not found.');
    },

    async markFailed(input) {
      requireIdentity(input.eventId, 'Operations Audit event identity');
      const failedAt = requirePositiveSafeInteger(input.failedAt, 'Audit failure time');
      const retryAt = requirePositiveSafeInteger(input.retryAt, 'Audit retry time');
      if (retryAt < failedAt) throw new Error('Audit retry time cannot precede failure time.');
      const result = await operationsPool.query(
        `UPDATE agent_operations.audit_records
         SET delivery_status = 'FAILED',
             next_attempt_at_ms = $2,
             lease_until_ms = NULL,
             last_failure_class = $3
         WHERE event_id = $1
           AND delivery_status <> 'DELIVERED'`,
        [input.eventId, retryAt, input.failureClass],
      );
      if (result.rowCount !== 1) {
        const existing = await operationsPool.query<BooleanRow>(
          `SELECT EXISTS (
             SELECT 1 FROM agent_operations.audit_records
             WHERE event_id = $1 AND delivery_status = 'DELIVERED'
           ) AS value`,
          [input.eventId],
        );
        if (existing.rows[0]?.value !== true) {
          throw new Error('Operations Audit event was not found.');
        }
      }
    },
  };

  return Object.freeze({
    agentSessionStateStore,
    investigationRepository,
    businessRecordRepository,
    investigationTransaction,
    checkpointRepository,
    budgetGuard,
    applicationOutbox,
    auditRecorder,
    auditDeliveryRepository,
    async close() {
      await Promise.all([operationsPool.end(), checkpointsPool.end()]);
    },
  });
};
