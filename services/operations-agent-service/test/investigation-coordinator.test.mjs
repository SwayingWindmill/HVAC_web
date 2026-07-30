import assert from 'node:assert/strict';
import test from 'node:test';

import {
  InvestigationCoordinatorError,
  createInvestigationCoordinator,
} from '../dist/index.js';
import { InvestigationRepositoryConflictError } from '../dist/application/index.js';

class FakeInvestigationRepository {
  records = new Map();
  saveCalls = [];
  conflictNextSave = false;

  constructor(outboxEvents, auditRecords) {
    this.outboxEvents = outboxEvents;
    this.auditRecords = auditRecords;
  }

  async create({ investigation, event, audit }) {
    const id = investigation.view().id;
    if (this.records.has(id)) {
      throw new InvestigationRepositoryConflictError('Investigation already exists.');
    }
    this.records.set(id, investigation);
    this.outboxEvents.push(event);
    this.auditRecords.push(audit);
  }

  async get(id) {
    return this.records.get(id) ?? null;
  }

  async save({ investigation, expectedRevision, event, audit }) {
    const id = investigation.view().id;
    if (this.conflictNextSave) {
      this.conflictNextSave = false;
      throw new InvestigationRepositoryConflictError('Concurrent Investigation write won the race.');
    }
    const current = this.records.get(id);
    if (current === undefined || current.view().revision !== expectedRevision) {
      throw new InvestigationRepositoryConflictError('Investigation Revision changed.');
    }
    this.saveCalls.push({ id, expectedRevision, nextRevision: investigation.view().revision });
    this.records.set(id, investigation);
    this.outboxEvents.push(event);
    this.auditRecords.push(audit);
  }
}

const createHarness = ({
  authorized = true,
  planningResult = null,
  budgetDecision = { decision: 'ALLOW' },
  readerHandlers = {},
} = {}) => {
  const identifiers = {
    investigation: ['investigation-001'],
    run: ['run-001', 'run-002'],
    lease: ['lease-001', 'lease-002', 'lease-003'],
    checkpoint: ['checkpoint-001'],
  };
  const outboxEvents = [];
  const auditRecords = [];
  const repository = new FakeInvestigationRepository(outboxEvents, auditRecords);
  const checkpoints = [];
  const runtimeInputs = [];
  let currentTime = 1_000;
  let authorizationAllowed = authorized;

  const defaultReader = async () => {
    throw new Error('Owner reader was not configured for this test.');
  };

  const coordinator = createInvestigationCoordinator({
    investigationRepository: repository,
    investigationTransaction: repository,
    authorizationDecisionReader: {
      async authorizeScope() {
        return authorizationAllowed
          ? { decision: 'ALLOW', decisionId: 'decision-001' }
          : { decision: 'DENY', decisionId: 'decision-001', reason: 'Scope is not authorized.' };
      },
    },
    agentExecutionRuntime: {
      async planReads(input) {
        runtimeInputs.push(input);
        if (planningResult === null) throw new Error('Runtime planning was not configured.');
        return planningResult;
      },
    },
    checkpointRepository: {
      async save(checkpoint) { checkpoints.push(checkpoint); },
      async load(investigationId, runId) {
        return checkpoints.findLast((checkpoint) => (
          checkpoint.investigationId === investigationId && checkpoint.runId === runId
        )) ?? null;
      },
      async delete() {},
    },
    applicationOutbox: {
      async append(event) { outboxEvents.push(event); },
    },
    auditRecorder: {
      async record(record) { auditRecords.push(record); },
    },
    budgetGuard: {
      async check() { return budgetDecision; },
    },
    ownerReaders: {
      registry: { read: readerHandlers.registry ?? defaultReader },
      currentTelemetry: { read: readerHandlers.currentTelemetry ?? defaultReader },
      energyAnalytics: { read: readerHandlers.energyAnalytics ?? defaultReader },
      commandCapabilities: { read: readerHandlers.commandCapabilities ?? defaultReader },
    },
    clock: {
      now() { return currentTime; },
    },
    idGenerator: {
      next(kind) {
        const value = identifiers[kind]?.shift();
        assert.notEqual(value, undefined, `No fake identity configured for ${kind}.`);
        return value;
      },
    },
    leaseDurationMs: 1_000,
  });

  return {
    coordinator,
    repository,
    outboxEvents,
    auditRecords,
    checkpoints,
    runtimeInputs,
    setAuthorization(value) { authorizationAllowed = value; },
    setTime(value) { currentTime = value; },
  };
};

const assertCoordinatorError = async (run, expectedCode) => {
  await assert.rejects(run, (error) => (
    error instanceof InvestigationCoordinatorError && error.code === expectedCode
  ));
};

const createAndStart = async (harness) => {
  const created = await harness.coordinator.create({
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: null,
      deviceId: null,
    },
  });
  return harness.coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-r1',
    expectedRevision: created.revision,
  });
};

test('authorized callers create, start, and query an Investigation through the Coordinator', async () => {
  const { coordinator, outboxEvents, auditRecords } = createHarness();

  const created = await coordinator.create({
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: null,
      deviceId: null,
    },
  });
  const started = await coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-r1',
    expectedRevision: created.revision,
  });
  const loaded = await coordinator.get({ investigationId: created.id });

  assert.equal(created.id, 'investigation-001');
  assert.equal(created.status, 'CREATED');
  assert.equal(started.status, 'RUNNING');
  assert.equal(started.activeRunId, 'run-001');
  assert.equal(started.runs[0].lease.id, 'lease-001');
  assert.deepEqual(loaded, started);
  assert.deepEqual(outboxEvents.map(({ type }) => type), [
    'INVESTIGATION_CREATED',
    'AGENT_RUN_STARTED',
  ]);
  assert.deepEqual(auditRecords.map(({ action }) => action), [
    'CREATE_INVESTIGATION',
    'START_AGENT_RUN',
  ]);
});

test('authorization denial is a typed error and creates no Investigation', async () => {
  const { coordinator, repository } = createHarness({ authorized: false });

  await assertCoordinatorError(() => coordinator.create({
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-denied',
      equipmentId: null,
      deviceId: null,
    },
  }), 'AUTHORIZATION_DENIED');

  assert.equal(repository.records.size, 0);
});

test('every query and mutation reauthorizes the authoritative Investigation Scope', async () => {
  const harness = createHarness();
  const created = await harness.coordinator.create({
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: null,
      deviceId: null,
    },
  });
  harness.setAuthorization(false);

  await assertCoordinatorError(
    () => harness.coordinator.get({ investigationId: created.id }),
    'AUTHORIZATION_DENIED',
  );
  await assertCoordinatorError(() => harness.coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-r1',
    expectedRevision: created.revision,
  }), 'AUTHORIZATION_DENIED');
  assert.equal(harness.repository.records.get(created.id).view().status, 'CREATED');
});

test('advance executes independent READ requests in parallel and saves only Runtime Checkpoint metadata', async () => {
  let releaseReads;
  let resolveAllStarted;
  const readGate = new Promise((resolve) => { releaseReads = resolve; });
  const allStarted = new Promise((resolve) => { resolveAllStarted = resolve; });
  const startedRequests = [];
  const scope = {
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentId: null,
    deviceId: null,
  };
  const read = (owner) => async (request) => {
    startedRequests.push(request.requestId);
    if (startedRequests.length === 2) resolveAllStarted();
    await readGate;
    return {
      requestId: request.requestId,
      owner,
      scope,
      revision: 'revision-001',
      quality: 'GOOD',
      provenance: `${owner}:fixture`,
      payload: { ok: true },
    };
  };
  const harness = createHarness({
    planningResult: {
      status: 'PLANNED',
      plan: {
        batches: [{
          batchId: 'batch-001',
          requests: [
            {
              requestId: 'read-registry',
              tool: 'registry.getEquipment',
              input: { equipmentId: 'equipment-001' },
            },
            {
              requestId: 'read-current',
              tool: 'telemetry.getCurrentSnapshot',
              input: { equipmentId: 'equipment-001' },
            },
          ],
        }],
      },
      checkpoint: {
        position: 'reads-planned',
        opaqueState: '[REDACTED_SECRET]',
      },
    },
    readerHandlers: {
      registry: read('registry'),
      currentTelemetry: read('telemetry-query-service'),
    },
  });
  const started = await createAndStart(harness);
  harness.setTime(1_100);

  const advancePromise = harness.coordinator.advance({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
  });
  await allStarted;
  assert.deepEqual(startedRequests.sort(), ['read-current', 'read-registry']);
  releaseReads();
  const advanced = await advancePromise;

  assert.equal(advanced.investigation.revision, started.revision);
  assert.equal(harness.runtimeInputs.length, 1);
  assert.equal(harness.runtimeInputs[0].checkpoint, null);
  assert.deepEqual(advanced.investigation.evidenceIds, []);
  assert.deepEqual(advanced.investigation.findingIds, []);
  assert.deepEqual(advanced.investigation.proposedActionIds, []);
  assert.equal(harness.repository.saveCalls.length, 1);
  assert.equal(advanced.results.length, 2);
  assert.equal(advanced.checkpointId, 'checkpoint-001');
  assert.deepEqual(harness.checkpoints, [{
    id: 'checkpoint-001',
    investigationId: started.id,
    runId: 'run-001',
    runtimeRevision: 'runtime-r1',
    position: 'reads-planned',
    opaqueState: '[REDACTED_SECRET]',
    savedAt: 1_100,
  }]);
  assert.equal('evidenceIds' in harness.checkpoints[0], false);
  assert.equal(harness.outboxEvents.at(-1).type, 'READ_PLAN_COMPLETED');
  assert.equal(harness.auditRecords.at(-1).action, 'PLAN_READS');
});

test('advance resumes from a matching Checkpoint and rejects a mismatched Runtime Revision', async () => {
  const planningResult = {
    status: 'PLANNED',
    plan: {
      batches: [{
        batchId: 'batch-001',
        requests: [{
          requestId: 'read-registry',
          tool: 'registry.getEquipment',
          input: { equipmentId: 'equipment-001' },
        }],
      }],
    },
    checkpoint: { position: 'continued', opaqueState: '[REDACTED_SECRET]' },
  };
  const read = async (request) => ({
    requestId: request.requestId,
    owner: 'registry',
    scope: {
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: null,
      deviceId: null,
    },
    revision: 'revision-001',
    quality: 'GOOD',
    provenance: 'registry:fixture',
    payload: { ok: true },
  });
  const matching = createHarness({
    planningResult,
    readerHandlers: { registry: read },
  });
  const matchingStarted = await createAndStart(matching);
  matching.checkpoints.push({
    id: 'checkpoint-existing',
    investigationId: matchingStarted.id,
    runId: matchingStarted.activeRunId,
    runtimeRevision: 'runtime-r1',
    position: 'previous',
    opaqueState: '[REDACTED_SECRET]',
    savedAt: 1_050,
  });
  matching.setTime(1_100);
  await matching.coordinator.advance({
    investigationId: matchingStarted.id,
    runId: matchingStarted.activeRunId,
    leaseId: matchingStarted.runs[0].lease.id,
    expectedRevision: matchingStarted.revision,
  });
  assert.equal(matching.runtimeInputs[0].checkpoint.id, 'checkpoint-existing');

  const mismatched = createHarness({
    planningResult,
    readerHandlers: { registry: read },
  });
  const mismatchedStarted = await createAndStart(mismatched);
  mismatched.checkpoints.push({
    id: 'checkpoint-wrong-runtime',
    investigationId: mismatchedStarted.id,
    runId: mismatchedStarted.activeRunId,
    runtimeRevision: 'runtime-r0',
    position: 'previous',
    opaqueState: '[REDACTED_SECRET]',
    savedAt: 1_050,
  });
  mismatched.setTime(1_100);
  await assertCoordinatorError(() => mismatched.coordinator.advance({
    investigationId: mismatchedStarted.id,
    runId: mismatchedStarted.activeRunId,
    leaseId: mismatchedStarted.runs[0].lease.id,
    expectedRevision: mismatchedStarted.revision,
  }), 'INVALID_INVESTIGATION_STATE');
  assert.equal(mismatched.runtimeInputs.length, 0);
});

test('advance rejects Owner results whose identity, Owner, Scope, or provenance metadata is invalid', async () => {
  const harness = createHarness({
    planningResult: {
      status: 'PLANNED',
      plan: {
        batches: [{
          batchId: 'batch-001',
          requests: [{
            requestId: 'read-registry',
            tool: 'registry.getEquipment',
            input: { equipmentId: 'equipment-001' },
          }],
        }],
      },
      checkpoint: { position: 'continued', opaqueState: '[REDACTED_SECRET]' },
    },
    readerHandlers: {
      registry: async () => ({
        requestId: 'read-other',
        owner: 'analytics-service',
        scope: {
          organizationId: 'organization-other',
          siteId: 'site-other',
          equipmentId: null,
          deviceId: null,
        },
        revision: '',
        quality: 'GOOD',
        provenance: '',
        payload: { ok: false },
      }),
    },
  });
  const started = await createAndStart(harness);
  harness.setTime(1_100);

  await assertCoordinatorError(() => harness.coordinator.advance({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
  }), 'INVALID_INVESTIGATION_STATE');
  assert.equal(harness.checkpoints.length, 0);
  assert.equal(harness.outboxEvents.filter(({ type }) => type === 'READ_PLAN_COMPLETED').length, 0);
});

test('advance reports budget exhaustion and inability to conclude as distinct typed errors', async () => {
  const plan = {
    status: 'PLANNED',
    plan: {
      batches: [{
        batchId: 'batch-001',
        requests: [{
          requestId: 'read-registry',
          tool: 'registry.getEquipment',
          input: {},
        }],
      }],
    },
    checkpoint: { position: 'planned', opaqueState: '[REDACTED_SECRET]' },
  };
  const exhausted = createHarness({
    planningResult: plan,
    budgetDecision: { decision: 'DENY', reason: 'Read budget is exhausted.' },
  });
  const exhaustedStarted = await createAndStart(exhausted);

  await assertCoordinatorError(() => exhausted.coordinator.advance({
    investigationId: exhaustedStarted.id,
    runId: exhaustedStarted.activeRunId,
    leaseId: exhaustedStarted.runs[0].lease.id,
    expectedRevision: exhaustedStarted.revision,
  }), 'BUDGET_EXHAUSTED');
  assert.equal(exhausted.checkpoints.length, 0);

  const unable = createHarness({
    planningResult: {
      status: 'UNABLE_TO_CONCLUDE',
      reason: 'Required Evidence is unavailable.',
    },
  });
  const unableStarted = await createAndStart(unable);
  await assertCoordinatorError(() => unable.coordinator.advance({
    investigationId: unableStarted.id,
    runId: unableStarted.activeRunId,
    leaseId: unableStarted.runs[0].lease.id,
    expectedRevision: unableStarted.revision,
  }), 'UNABLE_TO_CONCLUDE');
});

test('business effects are serialized and exact retries do not save twice', async () => {
  const harness = createHarness();
  const started = await createAndStart(harness);
  harness.setTime(1_100);

  const committed = await harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-energy-baseline',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });
  const saveCountAfterCommit = harness.repository.saveCalls.length;
  const duplicate = await harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-energy-baseline',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });

  assert.equal(committed.outcome, 'COMMITTED');
  assert.equal(committed.investigation.revision, 2);
  assert.equal(duplicate.outcome, 'DUPLICATE');
  assert.equal(duplicate.investigation.revision, 2);
  assert.equal(harness.repository.saveCalls.length, saveCountAfterCommit);
  assert.equal(harness.outboxEvents.filter(({ type }) => (
    type === 'INVESTIGATION_EFFECT_COMMITTED'
  )).length, 1);

  await assertCoordinatorError(() => harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: committed.investigation.revision,
    stepId: 'step-energy-baseline',
    idempotencyKey: 'effect-evidence-002',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  }), 'DUPLICATE_EFFECT');

  await assertCoordinatorError(() => harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-finding',
    idempotencyKey: 'effect-finding-001',
    kind: 'FINDING',
    recordId: 'finding-001',
  }), 'REVISION_CONFLICT');

  await assertCoordinatorError(() => harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: 'lease-stale',
    expectedRevision: committed.investigation.revision,
    stepId: 'step-finding',
    idempotencyKey: 'effect-finding-001',
    kind: 'FINDING',
    recordId: 'finding-001',
  }), 'LEASE_CONFLICT');

  const outboxCountBeforeConflict = harness.outboxEvents.length;
  const auditCountBeforeConflict = harness.auditRecords.length;
  harness.repository.conflictNextSave = true;
  await assertCoordinatorError(() => harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: committed.investigation.revision,
    stepId: 'step-concurrent-finding',
    idempotencyKey: 'effect-concurrent-finding',
    kind: 'FINDING',
    recordId: 'finding-concurrent',
  }), 'REVISION_CONFLICT');
  const afterConflict = await harness.coordinator.get({ investigationId: started.id });
  assert.deepEqual(afterConflict.findingIds, []);
  assert.equal(harness.outboxEvents.length, outboxCountBeforeConflict);
  assert.equal(harness.auditRecords.length, auditCountBeforeConflict);
});

test('pause, resume, and cancel preserve committed records and reject the old lease', async () => {
  const harness = createHarness();
  const started = await createAndStart(harness);
  harness.setTime(1_100);
  const committed = await harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: started.revision,
    stepId: 'step-energy-baseline',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });

  harness.setTime(1_200);
  const paused = await harness.coordinator.pause({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: started.runs[0].lease.id,
    expectedRevision: committed.investigation.revision,
  });
  harness.setTime(1_300);
  const resumed = await harness.coordinator.resume({
    investigationId: started.id,
    runId: started.activeRunId,
    expectedRevision: paused.revision,
  });

  assert.equal(paused.status, 'PAUSED');
  assert.equal(resumed.status, 'RUNNING');
  assert.equal(resumed.runs[0].lease.id, 'lease-002');
  await assertCoordinatorError(() => harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: 'lease-001',
    expectedRevision: resumed.revision,
    stepId: 'step-after-resume',
    idempotencyKey: 'effect-after-resume',
    kind: 'FINDING',
    recordId: 'finding-after-resume',
  }), 'LEASE_CONFLICT');

  harness.setTime(1_400);
  const cancelled = await harness.coordinator.cancel({
    investigationId: started.id,
    expectedRevision: resumed.revision,
  });
  const retry = await harness.coordinator.commitEffect({
    investigationId: started.id,
    runId: started.activeRunId,
    leaseId: 'lease-001',
    expectedRevision: started.revision,
    stepId: 'step-energy-baseline',
    idempotencyKey: 'effect-evidence-001',
    kind: 'EVIDENCE',
    recordId: 'evidence-001',
  });

  assert.equal(cancelled.status, 'CANCELLED');
  assert.deepEqual(cancelled.evidenceIds, ['evidence-001']);
  assert.equal(retry.outcome, 'DUPLICATE');
  assert.equal(retry.investigation.revision, cancelled.revision);
});

test('complete and fail are Coordinator-owned terminal transitions', async () => {
  const completedHarness = createHarness();
  const completedStarted = await createAndStart(completedHarness);
  completedHarness.setTime(1_100);
  const completed = await completedHarness.coordinator.complete({
    investigationId: completedStarted.id,
    runId: completedStarted.activeRunId,
    leaseId: completedStarted.runs[0].lease.id,
    expectedRevision: completedStarted.revision,
  });
  assert.equal(completed.status, 'COMPLETED');
  completedHarness.setTime(1_200);
  const reopened = await completedHarness.coordinator.reopen({
    investigationId: completed.id,
    runtimeRevision: 'runtime-r2',
    expectedRevision: completed.revision,
  });
  assert.equal(reopened.status, 'RUNNING');
  assert.equal(reopened.activeRunId, 'run-002');
  assert.equal(reopened.runs.length, 2);
  assert.equal(reopened.runs[0].status, 'COMPLETED');
  assert.equal(reopened.runs[1].runtimeRevision, 'runtime-r2');

  const failedHarness = createHarness();
  const failedStarted = await createAndStart(failedHarness);
  failedHarness.setTime(1_100);
  const failed = await failedHarness.coordinator.fail({
    investigationId: failedStarted.id,
    runId: failedStarted.activeRunId,
    leaseId: failedStarted.runs[0].lease.id,
    expectedRevision: failedStarted.revision,
  });
  assert.equal(failed.status, 'FAILED');
});

test('missing Investigations return a typed not-found error', async () => {
  const { coordinator } = createHarness();
  await assertCoordinatorError(
    () => coordinator.get({ investigationId: 'investigation-missing' }),
    'INVESTIGATION_NOT_FOUND',
  );
});
