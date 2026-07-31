import assert from 'node:assert/strict';
import test from 'node:test';

import { Pool } from 'pg';

import {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
} from '../dist/index.js';
import { InvestigationRepositoryConflictError } from '../dist/application/index.js';
import { createIdempotencyKey, createStepIdentity } from '../dist/domain/index.js';
import { createPostgresOperationsAgentPersistence } from '../dist/persistence/index.js';

const operationsConnectionString = process.env.OPERATIONS_AGENT_OPERATIONS_DATABASE_URL;
const checkpointsConnectionString = process.env.OPERATIONS_AGENT_CHECKPOINTS_DATABASE_URL;

if (!operationsConnectionString || !checkpointsConnectionString) {
  throw new Error('Operations Agent PostgreSQL integration database URLs are required.');
}

const assertCoordinatorError = async (run, expectedCode) => {
  await assert.rejects(run, (error) => (
    error instanceof InvestigationCoordinatorError && error.code === expectedCode
  ));
};

const assertRepositoryError = async (run, expectedCode) => {
  await assert.rejects(run, (error) => (
    error instanceof InvestigationRepositoryConflictError && error.code === expectedCode
  ));
};

const assertPermissionDenied = async (run) => {
  await assert.rejects(run, (error) => (
    typeof error === 'object' && error !== null && error.code === '42501'
  ));
};

test('PostgreSQL keeps Operations facts authoritative and Checkpoints independently disposable', async (t) => {
  let currentTime = 10_000;
  const persistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 1_000,
    now: () => currentTime,
  });
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  const checkpointsPool = new Pool({ connectionString: checkpointsConnectionString, max: 1 });
  t.after(async () => {
    await Promise.all([
      persistence.close(),
      operationsPool.end(),
      checkpointsPool.end(),
    ]);
  });

  const identities = {
    investigation: ['investigation-postgres-001'],
    run: ['run-postgres-001'],
    lease: ['lease-postgres-001'],
    checkpoint: ['checkpoint-coordinator-unused'],
  };
  const coordinator = createInvestigationCoordinator({
    investigationRepository: persistence.investigationRepository,
    investigationTransaction: persistence.investigationTransaction,
    checkpointRepository: persistence.checkpointRepository,
    applicationOutbox: persistence.applicationOutbox,
    auditRecorder: persistence.auditRecorder,
    authorizationDecisionReader: {
      async authorizeScope() {
        return { decision: 'ALLOW', decisionId: 'postgres-test-allow' };
      },
    },
    agentExecutionRuntime: {
      async planReads() {
        throw new Error('Runtime planning is not used by this persistence test.');
      },
    },
    budgetGuard: {
      async check() { return { decision: 'ALLOW' }; },
    },
    ownerReaders: {
      registry: { async read() { throw new Error('not used'); } },
      currentTelemetry: { async read() { throw new Error('not used'); } },
      energyAnalytics: { async read() { throw new Error('not used'); } },
      commandCapabilities: { async read() { throw new Error('not used'); } },
    },
    clock: { now: () => currentTime },
    idGenerator: {
      next(kind) {
        const value = identities[kind]?.shift();
        assert.notEqual(value, undefined, `No identity configured for ${kind}.`);
        return value;
      },
    },
    leaseDurationMs: 10_000,
  });

  const created = await coordinator.create({
    scope: {
      organizationId: 'organization-postgres',
      siteId: 'site-postgres',
      equipmentId: null,
      deviceId: null,
    },
  });
  const started = await coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-r1',
    expectedRevision: created.revision,
  });

  currentTime = 11_000;
  const evidenceCommand = {
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-evidence',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  };
  const evidence = await coordinator.commitEffect(evidenceCommand);
  const duplicate = await coordinator.commitEffect(evidenceCommand);
  assert.equal(evidence.outcome, 'COMMITTED');
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.equal(duplicate.investigation.revision, evidence.investigation.revision);

  const effectCount = await operationsPool.query(
    `SELECT count(*)::int AS count
     FROM agent_operations.investigation_effects
     WHERE investigation_id = $1`,
    [started.id],
  );
  assert.equal(effectCount.rows[0].count, 1);

  await operationsPool.query(
    `UPDATE agent_operations.investigations
     SET active_lease_id = 'lease-competing-writer'
     WHERE investigation_id = $1`,
    [started.id],
  );
  currentTime = 11_500;
  await assertCoordinatorError(() => coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-stale-lease',
    idempotencyKey: 'effect-stale-lease',
    kind: 'FINDING',
    recordId: 'finding-stale-lease',
  }), 'LEASE_CONFLICT');
  await operationsPool.query(
    `UPDATE agent_operations.investigations
     SET active_lease_id = $2
     WHERE investigation_id = $1`,
    [started.id, started.runs[0].lease.id],
  );

  const staleBase = await persistence.investigationRepository.get(started.id);
  assert.notEqual(staleBase, null);
  const staleWrite = staleBase.commitEffect({
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    at: 11_600,
    expectedRevision: evidence.investigation.revision,
    stepId: createStepIdentity('step-stale-revision'),
    idempotencyKey: createIdempotencyKey('effect-stale-revision'),
    kind: 'FINDING',
    recordId: 'finding-stale-revision',
  });

  currentTime = 11_600;
  const committedFinding = await coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-finding',
    idempotencyKey: 'effect-finding-001',
    kind: 'FINDING',
    recordId: 'finding-001',
  });
  assert.equal(committedFinding.outcome, 'COMMITTED');

  await assertRepositoryError(() => persistence.investigationTransaction.save({
    investigation: staleWrite.investigation,
    expectedRevision: evidence.investigation.revision,
    expectedAuthority: {
      runId: started.activeRunId,
      leaseId: started.runs[0].lease.id,
      at: 11_600,
    },
    effect: staleWrite.effect,
    event: {
      type: 'INVESTIGATION_EFFECT_COMMITTED',
      investigationId: started.id,
      revision: staleWrite.investigation.view().revision,
      occurredAt: 11_600,
    },
    audit: {
      action: 'COMMIT_EFFECT',
      investigationId: started.id,
      runId: started.activeRunId,
      revision: staleWrite.investigation.view().revision,
      occurredAt: 11_600,
    },
  }), 'REVISION_CONFLICT');

  const restored = await persistence.investigationRepository.get(started.id);
  assert.notEqual(restored, null);
  assert.deepEqual(restored.view().evidenceIds, ['evidence-001']);
  assert.deepEqual(restored.view().findingIds, ['finding-001']);
  assert.equal(restored.view().committedEffects.length, 2);

  currentTime = 12_000;
  await persistence.checkpointRepository.save({
    id: 'checkpoint-001',
    investigationId: started.id,
    runId: started.activeRunId,
    runtimeRevision: 'runtime-r1',
    position: 'after-read-plan',
    opaqueState: 'opaque-runtime-state',
    savedAt: currentTime,
  });
  assert.equal(
    (await persistence.checkpointRepository.load(started.id, started.activeRunId)).id,
    'checkpoint-001',
  );
  await persistence.checkpointRepository.delete(started.id, started.activeRunId);
  assert.equal(await persistence.checkpointRepository.load(started.id, started.activeRunId), null);
  assert.deepEqual(
    (await persistence.investigationRepository.get(started.id)).view().evidenceIds,
    ['evidence-001'],
  );

  await persistence.checkpointRepository.save({
    id: 'checkpoint-expiring',
    investigationId: started.id,
    runId: started.activeRunId,
    runtimeRevision: 'runtime-r1',
    position: 'temporary',
    opaqueState: 'temporary-runtime-state',
    savedAt: currentTime,
  });
  currentTime = 13_001;
  assert.equal(await persistence.checkpointRepository.load(started.id, started.activeRunId), null);
  assert.equal(await persistence.checkpointRepository.deleteExpired(), 1);
  assert.equal((await persistence.investigationRepository.get(started.id)).view().revision, 3);

  await assertPermissionDenied(() => operationsPool.query(
    'SELECT count(*) FROM agent_checkpoints.runtime_checkpoints',
  ));
  await assertPermissionDenied(() => checkpointsPool.query(
    'SELECT count(*) FROM agent_operations.investigations',
  ));
  await assertPermissionDenied(() => operationsPool.query(
    `UPDATE agent_operations.investigation_effects
     SET record_id = 'rewritten-record'
     WHERE investigation_id = $1`,
    [started.id],
  ));
  await assertPermissionDenied(() => operationsPool.query(
    `UPDATE agent_operations.application_outbox
     SET event_type = 'REWRITTEN'
     WHERE investigation_id = $1`,
    [started.id],
  ));
  await assertPermissionDenied(() => operationsPool.query(
    `UPDATE agent_operations.audit_records
     SET action = 'REWRITTEN'
     WHERE investigation_id = $1`,
    [started.id],
  ));

  const checkpointColumns = await checkpointsPool.query(
    `SELECT column_name
     FROM information_schema.columns
     WHERE table_schema = 'agent_checkpoints'
       AND table_name = 'runtime_checkpoints'
     ORDER BY ordinal_position`,
  );
  const columnNames = checkpointColumns.rows.map(({ column_name }) => column_name);
  for (const forbidden of ['evidence', 'finding', 'proposed_action', 'investigation_snapshot']) {
    assert.equal(columnNames.some((name) => name.includes(forbidden)), false);
  }

  const journalCounts = await operationsPool.query(
    `SELECT
      (SELECT count(*)::int FROM agent_operations.application_outbox WHERE investigation_id = $1) AS outbox_count,
      (SELECT count(*)::int FROM agent_operations.audit_records WHERE investigation_id = $1) AS audit_count`,
    [started.id],
  );
  assert.equal(journalCounts.rows[0].outbox_count, 4);
  assert.equal(journalCounts.rows[0].audit_count, 4);
});
