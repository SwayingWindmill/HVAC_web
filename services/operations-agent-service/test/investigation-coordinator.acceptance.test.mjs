import assert from 'node:assert/strict';
import test from 'node:test';

import { InvestigationCoordinatorError } from '../dist/index.js';
import { createFakeOperationsAgentEnvironment } from './support/fake-operations-agent-environment.mjs';

const scope = Object.freeze({
  tenantId: 'organization-acceptance',
  siteId: 'site-acceptance',
  assetId: null,
  deviceId: null,
});

const expectCoordinatorError = async (run, expectedCode) => {
  await assert.rejects(run, (error) => (
    error instanceof InvestigationCoordinatorError && error.code === expectedCode
  ));
};

test('the Coordinator owns a complete framework-independent Investigation acceptance path', async () => {
  const environment = createFakeOperationsAgentEnvironment({
    scope,
    runtimeSteps: [{
      stepId: 'step-collect-site-context',
      attemptBusinessMutation: true,
      plan: {
        batches: [{
          batchId: 'batch-site-context',
          requests: [
            {
              requestId: 'read-asset',
              tool: 'registry.getSite',
              input: { siteId: 'site-acceptance' },
            },
            {
              requestId: 'read-energy',
              tool: 'analytics.getEnergySeries',
              input: {
                tenantId: 'organization-acceptance',
                siteId: 'site-acceptance',
                energyType: 'electricity',
                granularity: 'hour',
                timezone: 'Asia/Tokyo',
                from: '2026-07-01T00:00:00Z',
                to: '2026-07-08T00:00:00Z',
                qualityPolicy: 'VALID_AND_SUSPECT',
              },
            },
          ],
        }],
      },
      checkpointPosition: 'after-site-context',
    }],
  });

  const created = await environment.coordinator.create({ scope });
  const started = await environment.coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'fake-runtime-r1',
    expectedRevision: created.revision,
  });
  const firstRun = started.runs[0];
  assert.equal(started.status, 'RUNNING');
  assert.notEqual(firstRun.lease, null);

  environment.setTime(10_500);
  const advanced = await environment.coordinator.advance({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: started.revision,
  });

  assert.equal(environment.runtime.calls.length, 1);
  const runtimeCall = environment.runtime.calls[0];
  assert.deepEqual({
    stepId: runtimeCall.stepId,
    runId: runtimeCall.runId,
    checkpointPosition: runtimeCall.checkpointPosition,
  }, {
    stepId: 'step-collect-site-context',
    runId: firstRun.id,
    checkpointPosition: null,
  });
  assert.deepEqual(Object.keys(runtimeCall.context).sort(), [
    'allowedReadTools',
    'effectPolicy',
    'investigationId',
    'revision',
    'runId',
    'runStatus',
    'runtimeRevision',
    'schemaVersion',
    'scope',
    'scopePolicy',
    'source',
    'trust',
    'untrustedContentPolicy',
  ]);
  assert.equal(runtimeCall.context.source, 'APPLICATION_POLICY');
  assert.equal(runtimeCall.context.trust, 'TRUSTED_CONTROL');
  assert.equal(runtimeCall.context.untrustedContentPolicy, 'EXCLUDED');
  assert.equal(runtimeCall.context.effectPolicy, 'READ_ONLY');
  assert.equal(Object.isFrozen(runtimeCall.context), true);
  assert.equal(Object.isFrozen(runtimeCall.context.scope), true);
  assert.equal(Object.isFrozen(runtimeCall.context.allowedReadTools), true);
  assert.doesNotMatch(
    JSON.stringify(runtimeCall.context),
    /operatorNote|rawPrompt|payload|finding|evidence|instruction/iu,
  );
  assert.equal(environment.runtime.attemptedBusinessMutation, true);
  assert.equal(environment.owners.maxConcurrentReads, 2);
  assert.equal(advanced.results.length, 2);
  for (const result of advanced.results) {
    assert.equal(result.scope.tenantId, scope.tenantId);
    assert.equal(result.scope.siteId, scope.siteId);
    assert.notEqual(result.owner, '');
    assert.notEqual(result.revision, '');
    assert.equal(result.quality, 'GOOD');
    assert.match(result.provenance, /^fake-owner:\/\//);
  }
  assert.equal(advanced.investigation.status, 'RUNNING');
  assert.deepEqual(advanced.investigation.evidenceIds, []);
  assert.equal(
    (await environment.coordinator.get({ investigationId: started.id })).status,
    'RUNNING',
  );
  await expectCoordinatorError(() => environment.coordinator.advance({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: started.revision,
  }), 'INVALID_INVESTIGATION_STATE');
  assert.equal(environment.runtime.calls.length, 1);

  environment.setTime(11_000);
  const evidenceRecord = {
    schemaVersion: 1,
    recordType: 'EVIDENCE',
    id: 'evidence-site-context',
    investigationId: started.id,
    recordedAt: 11_000,
    evidenceKind: 'SITE_ENERGY_SERIES_READY',
    classification: 'FACT',
    statement: 'Authoritative Site context passed bounded readiness checks.',
    analysisReferenceDigest: null,
    sources: [{
      owner: 'telemetry-query-service',
      scope,
      requestId: 'read-energy',
      registryRevision: null,
      datasetRevision: 'dataset-revision-29',
      watermark: { data: '2026-07-08T00:00:00.000Z', aggregate: null },
      partial: false,
      quality: { classification: 'GOOD', valid: 168, suspect: 0, invalid: 0 },
      capturedAt: 10_500,
      evaluatedAt: 11_000,
      provenanceDigest: `sha256:${'f'.repeat(64)}`,
    }],
  };
  const evidenceCommand = {
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: started.revision,
    stepId: 'step-collect-site-context',
    idempotencyKey: 'effect-evidence-site-context',
    kind: 'EVIDENCE',
    recordId: 'evidence-site-context',
    record: evidenceRecord,
  };
  const evidence = await environment.coordinator.commitEffect(evidenceCommand);
  const replay = await environment.coordinator.commitEffect(evidenceCommand);
  assert.equal(evidence.outcome, 'COMMITTED');
  assert.equal(replay.outcome, 'DUPLICATE');
  assert.equal(replay.investigation.revision, evidence.investigation.revision);

  environment.setTime(11_100);
  const findingRecord = {
    schemaVersion: 1,
    recordType: 'FINDING',
    id: 'finding-site-context',
    investigationId: started.id,
    recordedAt: 11_100,
    findingKind: 'UNABLE_TO_CONCLUDE',
    classification: 'INFERENCE',
    statement: 'The bounded Site context does not support a stronger conclusion.',
    evidenceIds: [evidenceRecord.id],
    analysisReferenceIds: [],
    conclusion: {
      status: 'UNABLE_TO_CONCLUDE',
      scope: 'SITE',
      reasonCode: 'ANALYSIS_REFERENCE_REQUIRED',
      detail: 'A deterministic Analysis Reference is required before a supported Site Finding.',
    },
  };
  const findingCommand = {
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-analyze-site-context',
    idempotencyKey: 'effect-finding-site-context',
    kind: 'FINDING',
    recordId: 'finding-site-context',
    record: findingRecord,
  };
  const finding = await environment.coordinator.commitEffect(findingCommand);
  const findingReplay = await environment.coordinator.commitEffect(findingCommand);
  assert.equal(findingReplay.outcome, 'DUPLICATE');

  environment.setTime(11_200);
  const proposalCommand = {
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: finding.investigation.revision,
    stepId: 'step-propose-site-action',
    idempotencyKey: 'effect-proposal-site-action',
    kind: 'PROPOSED_ACTION',
    recordId: 'proposal-site-action',
  };
  const proposal = await environment.coordinator.commitEffect(proposalCommand);
  const proposalReplay = await environment.coordinator.commitEffect(proposalCommand);
  assert.equal(proposalReplay.outcome, 'DUPLICATE');

  await expectCoordinatorError(() => environment.coordinator.commitEffect({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: proposal.investigation.revision,
    stepId: 'step-rewrite-evidence',
    idempotencyKey: 'effect-evidence-site-context-second-key',
    kind: 'EVIDENCE',
    recordId: 'evidence-site-context',
  }), 'DUPLICATE_EFFECT');

  await expectCoordinatorError(() => environment.coordinator.commitEffect({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: evidence.investigation.revision,
    stepId: 'step-stale-writer',
    idempotencyKey: 'effect-stale-writer',
    kind: 'FINDING',
    recordId: 'finding-stale-writer',
  }), 'REVISION_CONFLICT');

  environment.setTime(11_300);
  const paused = await environment.coordinator.pause({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: proposal.investigation.revision,
  });
  assert.equal(paused.status, 'PAUSED');

  environment.setTime(11_400);
  const resumed = await environment.coordinator.resume({
    investigationId: started.id,
    runId: firstRun.id,
    expectedRevision: paused.revision,
  });
  const resumedRun = resumed.runs.find(({ id }) => id === firstRun.id);
  assert.notEqual(resumedRun.lease, null);
  assert.notEqual(resumedRun.lease.id, firstRun.lease.id);

  await expectCoordinatorError(() => environment.coordinator.commitEffect({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: firstRun.lease.id,
    expectedRevision: resumed.revision,
    stepId: 'step-old-lease',
    idempotencyKey: 'effect-old-lease',
    kind: 'FINDING',
    recordId: 'finding-old-lease',
  }), 'LEASE_CONFLICT');

  environment.setTime(11_500);
  const completed = await environment.coordinator.complete({
    investigationId: started.id,
    runId: firstRun.id,
    leaseId: resumedRun.lease.id,
    expectedRevision: resumed.revision,
  });
  assert.equal(completed.status, 'COMPLETED');

  assert.notEqual(
    await environment.checkpointStore.repository.load(started.id, firstRun.id),
    null,
  );
  await environment.checkpointStore.repository.delete(started.id, firstRun.id);
  assert.equal(
    await environment.checkpointStore.repository.load(started.id, firstRun.id),
    null,
  );
  const afterCheckpointDeletion = await environment.coordinator.get({
    investigationId: started.id,
  });
  assert.deepEqual(afterCheckpointDeletion.evidenceIds, ['evidence-site-context']);
  assert.deepEqual(afterCheckpointDeletion.findingIds, ['finding-site-context']);
  assert.deepEqual(afterCheckpointDeletion.proposedActionIds, ['proposal-site-action']);

  environment.setTime(11_600);
  const reopened = await environment.coordinator.reopen({
    investigationId: started.id,
    runtimeRevision: 'fake-runtime-r2',
    expectedRevision: afterCheckpointDeletion.revision,
  });
  assert.equal(reopened.status, 'RUNNING');
  assert.equal(reopened.runs.length, 2);
  assert.notEqual(reopened.activeRunId, firstRun.id);
  assert.deepEqual(reopened.evidenceIds, ['evidence-site-context']);

  environment.setTime(11_700);
  const cancelled = await environment.coordinator.cancel({
    investigationId: started.id,
    expectedRevision: reopened.revision,
  });
  assert.equal(cancelled.status, 'CANCELLED');
  assert.deepEqual(cancelled.evidenceIds, ['evidence-site-context']);
  assert.deepEqual(cancelled.findingIds, ['finding-site-context']);
  assert.deepEqual(cancelled.proposedActionIds, ['proposal-site-action']);

  assert.deepEqual(
    environment.businessStore.saveCalls
      .filter(({ effect }) => effect !== null)
      .map(({ effect }) => effect.recordId),
    ['evidence-site-context', 'finding-site-context', 'proposal-site-action'],
  );
  assert.equal(
    environment.businessStore.outboxEvents
      .filter(({ type }) => type === 'INVESTIGATION_EFFECT_COMMITTED').length,
    3,
  );
  assert.equal(
    environment.businessStore.auditRecords
      .filter(({ action }) => action === 'COMMIT_EFFECT').length,
    3,
  );
});

test('the Fake Repository exposes deterministic transaction conflicts through the Coordinator', async () => {
  const environment = createFakeOperationsAgentEnvironment({
    scope,
    runtimeSteps: [],
  });
  const created = await environment.coordinator.create({ scope });
  const started = await environment.coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'fake-runtime-r1',
    expectedRevision: created.revision,
  });
  const run = started.runs[0];

  environment.businessStore.forceConflict('REVISION_CONFLICT');
  await expectCoordinatorError(() => environment.coordinator.pause({
    investigationId: started.id,
    runId: run.id,
    leaseId: run.lease.id,
    expectedRevision: started.revision,
  }), 'REVISION_CONFLICT');

  const authoritative = await environment.coordinator.get({ investigationId: started.id });
  assert.equal(authoritative.status, 'RUNNING');
  assert.equal(authoritative.revision, started.revision);
});
