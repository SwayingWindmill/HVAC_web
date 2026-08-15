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
    businessRecordRepository: persistence.businessRecordRepository,
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
    budgetGuard: persistence.budgetGuard,
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
      tenantId: 'organization-postgres',
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
  const evidenceRecord = {
    schemaVersion: 1,
    recordType: 'EVIDENCE',
    id: 'evidence-001',
    investigationId: started.id,
    recordedAt: currentTime,
    evidenceKind: 'SITE_ENERGY_SERIES_READY',
    classification: 'FACT',
    statement: 'Authoritative Site series passed deterministic readiness checks.',
    analysisReferenceDigest: null,
    sources: [{
      owner: 'telemetry-query-service',
      scope: started.scope,
      requestId: 'energy-request-postgres-001',
      registryRevision: null,
      datasetRevision: 'dataset-revision-postgres-001',
      watermark: { data: '2026-07-30T08:00:00.000Z', aggregate: null },
      partial: false,
      quality: { classification: 'GOOD', valid: 8, suspect: 0, invalid: 0 },
      capturedAt: 10_900,
      evaluatedAt: currentTime,
      provenanceDigest: `sha256:${'e'.repeat(64)}`,
    }],
  };
  const evidenceCommand = {
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-evidence',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
    record: evidenceRecord,
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
    kind: 'PROPOSED_ACTION',
    recordId: 'proposed-action-stale-lease',
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
    kind: 'PROPOSED_ACTION',
    recordId: 'proposed-action-001',
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
  assert.deepEqual(restored.view().proposedActionIds, ['proposed-action-001']);
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

test('PostgreSQL atomically persists typed records across restart, retry, and rollback', async (t) => {
  let currentTime = 20_000;
  let firstPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime,
  });
  let secondPersistence = null;
  const operationsPool = new Pool({ connectionString: operationsConnectionString, max: 1 });
  t.after(async () => {
    await Promise.all([
      firstPersistence?.close(),
      secondPersistence?.close(),
      operationsPool.end(),
    ].filter(Boolean));
  });

  const scope = {
    tenantId: 'organization-records-postgres',
    siteId: 'site-records-postgres',
    equipmentId: null,
    deviceId: null,
  };
  const createCoordinator = (persistence, identities) => createInvestigationCoordinator({
    investigationRepository: persistence.investigationRepository,
    businessRecordRepository: persistence.businessRecordRepository,
    investigationTransaction: persistence.investigationTransaction,
    checkpointRepository: persistence.checkpointRepository,
    applicationOutbox: persistence.applicationOutbox,
    auditRecorder: persistence.auditRecorder,
    authorizationDecisionReader: {
      async authorizeScope() {
        return { decision: 'ALLOW', decisionId: 'typed-records-postgres-allow' };
      },
    },
    agentExecutionRuntime: {
      async planReads() { throw new Error('not used'); },
    },
    budgetGuard: persistence.budgetGuard,
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

  const firstCoordinator = createCoordinator(firstPersistence, {
    investigation: ['investigation-records-postgres'],
    run: ['run-records-postgres'],
    lease: ['lease-records-postgres'],
    checkpoint: [],
  });
  const created = await firstCoordinator.create({ scope });
  const started = await firstCoordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-records-r1',
    expectedRevision: created.revision,
  });
  const runId = started.activeRunId;
  const leaseId = started.runs[0].lease.id;
  const digest = `sha256:${'b'.repeat(64)}`;
  const source = {
    owner: 'telemetry-query-service',
    scope,
    requestId: 'energy-request-records-001',
    registryRevision: null,
    datasetRevision: 'dataset-revision-records-29',
    watermark: {
      data: '2026-07-30T08:00:00.000Z',
      aggregate: '2026-07-30T08:00:00.000Z',
    },
    partial: false,
    quality: { classification: 'GOOD', valid: 8, suspect: 0, invalid: 0 },
    capturedAt: 20_900,
    evaluatedAt: 21_000,
    provenanceDigest: digest,
  };

  currentTime = 21_000;
  const evidenceRecord = {
    schemaVersion: 1,
    recordType: 'EVIDENCE',
    id: 'evidence-records-001',
    investigationId: started.id,
    recordedAt: currentTime,
    evidenceKind: 'SITE_ENERGY_SERIES_READY',
    classification: 'FACT',
    statement: 'Authoritative bounded Site energy series passed readiness checks.',
    analysisReferenceDigest: null,
    sources: [source],
  };
  const evidenceCommand = {
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: started.revision,
    stepId: 'step-records-evidence',
    idempotencyKey: 'effect-records-evidence',
    kind: 'EVIDENCE',
    recordId: evidenceRecord.id,
    record: evidenceRecord,
  };
  const evidence = await firstCoordinator.commitEffect(evidenceCommand);
  assert.equal(evidence.outcome, 'COMMITTED');
  assert.deepEqual(evidence.record, evidenceRecord);

  await firstPersistence.close();
  firstPersistence = null;
  secondPersistence = createPostgresOperationsAgentPersistence({
    operationsConnectionString,
    checkpointsConnectionString,
    checkpointRetentionMs: 60_000,
    now: () => currentTime,
  });
  const secondCoordinator = createCoordinator(secondPersistence, {
    investigation: [],
    run: [],
    lease: [],
    checkpoint: [],
  });

  const duplicate = await secondCoordinator.commitEffect(evidenceCommand);
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.deepEqual(duplicate.record, evidenceRecord);
  const afterDuplicate = await secondCoordinator.get({ investigationId: started.id });
  assert.equal(afterDuplicate.revision, evidence.investigation.revision);

  let counts = await operationsPool.query(
    `SELECT
      (SELECT count(*)::int FROM agent_operations.investigation_business_records WHERE investigation_id = $1) AS record_count,
      (SELECT count(*)::int FROM agent_operations.investigation_effects WHERE investigation_id = $1) AS effect_count,
      (SELECT count(*)::int FROM agent_operations.application_outbox WHERE investigation_id = $1) AS outbox_count,
      (SELECT count(*)::int FROM agent_operations.audit_records WHERE investigation_id = $1) AS audit_count`,
    [started.id],
  );
  assert.deepEqual(counts.rows[0], {
    record_count: 1,
    effect_count: 1,
    outbox_count: 3,
    audit_count: 3,
  });

  currentTime = 21_100;
  const analysisRecord = {
    schemaVersion: 1,
    recordType: 'ANALYSIS_REFERENCE',
    id: 'analysis-records-001',
    investigationId: started.id,
    recordedAt: currentTime,
    analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
    authority: 'DETERMINISTIC_ALGORITHM',
    algorithmVersion: 'site-night-energy-comparison/v1',
    policyVersion: 'night-energy-readiness/v1',
    inputEvidenceIds: [evidenceRecord.id],
    parameterDigest: digest,
    resultDigest: digest,
    executedAt: currentTime,
    outcome: 'SUPPORTED_SITE_FINDING',
  };
  const analysis = await secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-records-analysis',
    idempotencyKey: 'effect-records-analysis',
    kind: 'ANALYSIS_REFERENCE',
    recordId: analysisRecord.id,
    record: analysisRecord,
  });
  assert.equal(analysis.outcome, 'COMMITTED');

  const findingRecord = {
    schemaVersion: 1,
    recordType: 'FINDING',
    id: 'finding-records-001',
    investigationId: started.id,
    recordedAt: 21_200,
    findingKind: 'SITE_NIGHT_ENERGY_INCREASE',
    classification: 'INFERENCE',
    statement: 'Site night energy increased by 24%.',
    evidenceIds: [evidenceRecord.id],
    analysisReferenceIds: [analysisRecord.id],
    conclusion: {
      status: 'SUPPORTED',
      scope: 'SITE',
      tenantId: scope.tenantId,
      siteId: scope.siteId,
    },
  };
  currentTime = 21_200;
  await assertCoordinatorError(() => secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-records-finding-stale-revision',
    idempotencyKey: 'effect-records-finding-stale-revision',
    kind: 'FINDING',
    recordId: 'finding-records-stale-revision',
    record: { ...findingRecord, id: 'finding-records-stale-revision' },
  }), 'REVISION_CONFLICT');
  await assertCoordinatorError(() => secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId: 'lease-records-stale',
    expectedRevision: analysis.investigation.revision,
    stepId: 'step-records-finding-stale-lease',
    idempotencyKey: 'effect-records-finding-stale-lease',
    kind: 'FINDING',
    recordId: 'finding-records-stale-lease',
    record: { ...findingRecord, id: 'finding-records-stale-lease' },
  }), 'LEASE_CONFLICT');
  const finding = await secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: analysis.investigation.revision,
    stepId: 'step-records-finding',
    idempotencyKey: 'effect-records-finding',
    kind: 'FINDING',
    recordId: findingRecord.id,
    record: findingRecord,
  });
  assert.equal(finding.outcome, 'COMMITTED');

  currentTime = 21_300;
  const receiptRecord = {
    schemaVersion: 1,
    recordType: 'TOOL_EXECUTION_RECEIPT',
    id: 'receipt-records-001',
    investigationId: started.id,
    recordedAt: currentTime,
    logicalTool: 'analytics.getEnergySeries',
    owner: 'telemetry-query-service',
    requestId: 'energy-request-records-001',
    attemptId: 'energy-attempt-records-001',
    runId,
    stepId: 'step-records-receipt',
    startedAt: 21_250,
    completedAt: 21_290,
    resultCategory: 'SUCCEEDED',
    metadata: {
      datasetRevision: 'dataset-revision-records-29',
      partial: false,
      bucketCount: 8,
    },
  };
  const receipt = await secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: finding.investigation.revision,
    stepId: receiptRecord.stepId,
    idempotencyKey: 'effect-records-receipt',
    kind: 'TOOL_EXECUTION_RECEIPT',
    recordId: receiptRecord.id,
    record: receiptRecord,
  });
  assert.equal(receipt.outcome, 'COMMITTED');

  counts = await operationsPool.query(
    `SELECT
      (SELECT count(*)::int FROM agent_operations.investigation_business_records WHERE investigation_id = $1) AS record_count,
      (SELECT count(*)::int FROM agent_operations.investigation_effects WHERE investigation_id = $1) AS effect_count,
      (SELECT count(*)::int FROM agent_operations.application_outbox WHERE investigation_id = $1) AS outbox_count,
      (SELECT count(*)::int FROM agent_operations.audit_records WHERE investigation_id = $1) AS audit_count`,
    [started.id],
  );
  assert.deepEqual(counts.rows[0], {
    record_count: 4,
    effect_count: 4,
    outbox_count: 6,
    audit_count: 6,
  });

  currentTime = 21_400;
  await assertCoordinatorError(() => secondCoordinator.commitEffect({
    investigationId: started.id,
    runId,
    leaseId,
    expectedRevision: receipt.investigation.revision,
    stepId: 'step-records-receipt-duplicate',
    idempotencyKey: 'effect-records-receipt-duplicate',
    kind: 'TOOL_EXECUTION_RECEIPT',
    recordId: 'receipt-records-duplicate',
    record: {
      ...receiptRecord,
      id: 'receipt-records-duplicate',
      recordedAt: currentTime,
      stepId: 'step-records-receipt-duplicate',
      startedAt: 21_350,
      completedAt: 21_390,
    },
  }), 'DUPLICATE_RECORD');
  assert.equal(
    (await secondCoordinator.get({ investigationId: started.id })).revision,
    receipt.investigation.revision,
  );

  const rollbackBase = await secondPersistence.investigationRepository.get(started.id);
  assert.notEqual(rollbackBase, null);
  currentTime = 21_500;
  const rollbackEffect = rollbackBase.commitEffect({
    runId,
    leaseId,
    at: currentTime,
    expectedRevision: receipt.investigation.revision,
    stepId: createStepIdentity('step-records-rollback'),
    idempotencyKey: createIdempotencyKey('effect-records-rollback'),
    kind: 'EVIDENCE',
    recordId: 'evidence-records-rollback',
  });
  const rollbackRecord = {
    ...evidenceRecord,
    id: 'evidence-records-rollback',
    recordedAt: currentTime,
    statement: 'This record must roll back with the failed Audit insert.',
    sources: [{
      ...source,
      requestId: 'energy-request-records-rollback',
      capturedAt: 21_450,
      evaluatedAt: currentTime,
    }],
  };
  await assert.rejects(() => secondPersistence.investigationTransaction.save({
    investigation: rollbackEffect.investigation,
    expectedRevision: receipt.investigation.revision,
    expectedAuthority: { runId, leaseId, at: currentTime },
    effect: rollbackEffect.effect,
    record: rollbackRecord,
    event: {
      type: 'INVESTIGATION_EFFECT_COMMITTED',
      investigationId: started.id,
      revision: rollbackEffect.investigation.view().revision,
      occurredAt: currentTime,
    },
    audit: {
      action: '',
      investigationId: started.id,
      runId,
      revision: rollbackEffect.investigation.view().revision,
      occurredAt: currentTime,
    },
  }));

  assert.equal(
    (await secondPersistence.investigationRepository.get(started.id)).view().revision,
    receipt.investigation.revision,
  );
  assert.equal(
    await secondPersistence.businessRecordRepository.get(started.id, rollbackRecord.id),
    null,
  );
  counts = await operationsPool.query(
    `SELECT
      (SELECT count(*)::int FROM agent_operations.investigation_business_records WHERE investigation_id = $1) AS record_count,
      (SELECT count(*)::int FROM agent_operations.investigation_effects WHERE investigation_id = $1) AS effect_count,
      (SELECT count(*)::int FROM agent_operations.application_outbox WHERE investigation_id = $1) AS outbox_count,
      (SELECT count(*)::int FROM agent_operations.audit_records WHERE investigation_id = $1) AS audit_count`,
    [started.id],
  );
  assert.deepEqual(counts.rows[0], {
    record_count: 4,
    effect_count: 4,
    outbox_count: 6,
    audit_count: 6,
  });

  await secondPersistence.checkpointRepository.save({
    id: 'checkpoint-records-disposable',
    investigationId: started.id,
    runId,
    runtimeRevision: 'runtime-records-r1',
    position: 'typed-records-complete',
    opaqueState: 'disposable-runtime-state',
    savedAt: currentTime,
  });
  await secondPersistence.checkpointRepository.delete(started.id, runId);
  assert.equal(await secondPersistence.checkpointRepository.load(started.id, runId), null);
  assert.deepEqual(
    await secondPersistence.businessRecordRepository.get(started.id, evidenceRecord.id),
    evidenceRecord,
  );

  const payloadBounds = await operationsPool.query(
    `SELECT
      bool_and(octet_length(record_payload::text) <= 65536) AS bounded,
      bool_or(record_payload ? 'points') AS contains_points,
      array_agg(record_type ORDER BY recorded_at_ms, record_id) AS record_types
     FROM agent_operations.investigation_business_records
     WHERE investigation_id = $1`,
    [started.id],
  );
  assert.equal(payloadBounds.rows[0].bounded, true);
  assert.equal(payloadBounds.rows[0].contains_points, false);
  assert.deepEqual(payloadBounds.rows[0].record_types, [
    'EVIDENCE',
    'ANALYSIS_REFERENCE',
    'FINDING',
    'TOOL_EXECUTION_RECEIPT',
  ]);
  await assertPermissionDenied(() => operationsPool.query(
    `UPDATE agent_operations.investigation_business_records
     SET record_payload = '{}'::jsonb
     WHERE investigation_id = $1`,
    [started.id],
  ));
});
