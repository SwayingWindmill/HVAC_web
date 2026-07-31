import {
  Pool,
  type PoolClient,
  type QueryResultRow,
} from 'pg';

import type {
  ApplicationEvent,
  ApplicationOutbox,
  AuditRecord,
  AuditRecorder,
  CheckpointRepository,
  InvestigationBusinessRecordRepository,
  InvestigationRepository,
  InvestigationTransaction,
  RuntimeCheckpoint,
} from '../../application/index.js';
import {
  OperationsInvestigation,
  createInvestigationBusinessRecord,
  type CommittedEffectView,
  type InvestigationBusinessRecord,
  type OperationsInvestigationSnapshot,
} from '../../domain/index.js';
import { InvestigationRepositoryConflictError } from '../../application/index.js';

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
  readonly investigationRepository: InvestigationRepository;
  readonly businessRecordRepository: InvestigationBusinessRecordRepository;
  readonly investigationTransaction: InvestigationTransaction;
  readonly checkpointRepository: PostgresCheckpointRepository;
  readonly applicationOutbox: ApplicationOutbox;
  readonly auditRecorder: AuditRecorder;
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
    || current.scope.organizationId !== next.scope.organizationId
    || current.scope.siteId !== next.scope.siteId
    || current.scope.equipmentId !== next.scope.equipmentId
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
): boolean => investigation.organizationId === candidate.organizationId
  && (investigation.siteId === null || investigation.siteId === candidate.siteId)
  && (investigation.equipmentId === null || investigation.equipmentId === candidate.equipmentId)
  && (investigation.deviceId === null || investigation.deviceId === candidate.deviceId);

const validateBusinessRecord = (
  current: OperationsInvestigationSnapshot,
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
      && (record.conclusion.organizationId !== current.scope.organizationId
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
  await client.query(
    `INSERT INTO agent_operations.audit_records (
      investigation_id,
      action,
      investigation_revision,
      audit_payload,
      occurred_at_ms
    ) VALUES ($1, $2, $3, $4::jsonb, $5)`,
    [
      audit.investigationId,
      audit.action,
      audit.revision,
      JSON.stringify(audit),
      audit.occurredAt,
    ],
  );
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
        validateBusinessRecord(currentSnapshot, input.effect, record);
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

  return Object.freeze({
    investigationRepository,
    businessRecordRepository,
    investigationTransaction,
    checkpointRepository,
    applicationOutbox,
    auditRecorder,
    async close() {
      await Promise.all([operationsPool.end(), checkpointsPool.end()]);
    },
  });
};
