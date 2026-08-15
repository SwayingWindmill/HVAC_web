import assert from 'node:assert/strict';
import test from 'node:test';

import { createSiteNightEnergyInvestigationCoordinator } from '../dist/index.js';
import { createFakeFindingSynthesizer } from '../dist/model/index.js';
import { createFakeOperationsAgentEnvironment } from './support/fake-operations-agent-environment.mjs';

const tenantId = '0198f5c0-7c00-7000-8000-000000000001';
const siteId = '0198f5c0-7c00-7000-8000-000000000002';
const equipmentIds = [
  '0198f5c0-7c00-7000-8000-000000000010',
  '0198f5c0-7c00-7000-8000-000000000011',
];
const scope = Object.freeze({
  tenantId,
  siteId,
  equipmentId: null,
  deviceId: null,
});
const investigationId = 'investigation-001';

const energySeries = (request, energyPerHour, partial = false) => {
  const from = Date.parse(request.input.from);
  const to = Date.parse(request.input.to);
  const hours = Math.round((to - from) / 3_600_000);
  return {
    schemaVersion: 1,
    points: Array.from({ length: hours }, (_value, index) => ({
      periodStart: new Date(from + index * 3_600_000).toISOString(),
      periodEnd: new Date(from + (index + 1) * 3_600_000).toISOString(),
      energyKWh: energyPerHour,
    })),
    metadata: {
      requestedGranularity: 'hour',
      actualGranularity: 'hour',
      dataWatermark: new Date(to + 300_000).toISOString(),
      aggregateWatermark: new Date(to + 300_000).toISOString(),
      datasetRevision: 'energy-dataset-r17',
      partial,
      qualitySummary: { valid: hours, suspect: 0, invalid: 0 },
    },
  };
};

const createOwnerResultFactory = ({ partial = false } = {}) => async (request) => {
  if (request.tool === 'registry.getSite') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-site:17',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site/v1',
      payload: {
        kind: 'SITE',
        site: { id: siteId, tenantId: tenantId, timezone: 'Asia/Tokyo' },
      },
    };
  }
  if (request.tool === 'registry.listSiteEquipment') {
    return {
      requestId: request.requestId,
      owner: 'registry',
      scope,
      revision: 'registry-site-equipment:r29',
      quality: 'GOOD',
      provenance: 'platform-core-service:registry-site-equipment/v1',
      payload: {
        kind: 'SITE_EQUIPMENT',
        siteId,
        equipment: equipmentIds.map((id) => ({ id })),
      },
    };
  }
  const target = request.requestId.endsWith('energy-target');
  return {
    requestId: request.requestId,
    owner: 'telemetry-query-service',
    scope,
    revision: 'energy-dataset-r17',
    quality: 'GOOD',
    provenance: 'telemetry-query-service:energy-series/v1',
    payload: energySeries(request, target ? 155 : 125, partial),
  };
};

const createEnvironment = (options = {}) => createFakeOperationsAgentEnvironment({
  scope,
  initialTime: Date.parse('2026-07-31T00:00:00.000Z'),
  leaseDurationMs: 86_400_000,
  ownerDelayMs: 5,
  ownerResultFactory: createOwnerResultFactory(options),
  runtimeSteps: [{
    stepId: 'collect-registry-context',
    plan: {
      batches: [{
        batchId: 'registry-context',
        requests: [{
          requestId: `${investigationId}:registry-site`,
          tool: 'registry.getSite',
          input: { siteId },
        }, {
          requestId: `${investigationId}:registry-equipment`,
          tool: 'registry.listSiteEquipment',
          input: { siteId },
        }],
      }],
    },
    checkpointPosition: 'complete',
  }],
});

test('Site night-energy use case commits a supported Investigation and exact replay is inert', async () => {
  const environment = createEnvironment();
  const toolAuthorizationCalls = [];
  const coordinator = createSiteNightEnergyInvestigationCoordinator({
    ...environment.ports,
    toolAuthorizationReader: {
      async authorize({ request, context }) {
        assert.equal(context.authorization.delegationGrant, 'fake-delegation-grant');
        toolAuthorizationCalls.push(request.requestId);
        return {
          delegationGrant: `dynamic:${request.requestId}`,
          policyRevision: 'dynamic-tool-policy',
        };
      },
    },
  });

  const started = await coordinator.start({ tenantId, siteId });
  assert.equal(started.id, investigationId);
  assert.equal(started.status, 'RUNNING');
  assert.equal(started.activeRun?.status, 'ACTIVE');
  assert.equal(JSON.stringify(started).includes('lease'), false);
  assert.equal(JSON.stringify(started).includes('runtimeRevision'), false);

  const completed = await coordinator.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.outcome, 'SUPPORTED_SITE_FINDING');
  assert.equal(completed.activeRun, null);
  assert.equal(completed.toolReceipts.length, 4);
  assert.equal(completed.evidence.length, 2);
  assert.equal(completed.analysisReferences.length, 1);
  assert.equal(completed.findings.length, 1);
  assert.equal(completed.findings[0].conclusion.status, 'SUPPORTED');
  assert.equal(completed.findings[0].conclusion.scope, 'SITE');
  assert.equal(completed.findings[0].statement.includes('24%'), true);
  assert.equal(completed.analysisReferences[0].authority, 'DETERMINISTIC_ALGORITHM');
  assert.equal(JSON.stringify(completed).includes('"points"'), false);
  assert.equal(JSON.stringify(completed).includes('synthesis'), false);
  const deterministicFinding = await environment.ports.businessRecordRepository.get(
    started.id,
    `${started.id}:finding:night-energy`,
  );
  assert.equal(deterministicFinding.synthesis.source, 'DETERMINISTIC_FALLBACK');
  assert.equal(deterministicFinding.synthesis.fallbackReason, 'NOT_CONFIGURED');
  assert.equal(deterministicFinding.synthesis.provider, null);
  assert.equal(environment.owners.maxConcurrentReads, 2);
  assert.deepEqual([...toolAuthorizationCalls].sort(), [
    `${investigationId}:registry-site`,
    `${investigationId}:registry-equipment`,
    `${investigationId}:energy-target`,
    `${investigationId}:energy-baseline`,
  ].sort());

  const countsBeforeReplay = {
    revision: completed.revision,
    records: environment.businessStore.businessRecords.size,
    saves: environment.businessStore.saveCalls.length,
    outbox: environment.businessStore.outboxEvents.length,
    audit: environment.businessStore.auditRecords.length,
  };
  const replay = await coordinator.advance({ investigationId: started.id });
  assert.deepEqual(replay, completed);
  assert.deepEqual({
    revision: replay.revision,
    records: environment.businessStore.businessRecords.size,
    saves: environment.businessStore.saveCalls.length,
    outbox: environment.businessStore.outboxEvents.length,
    audit: environment.businessStore.auditRecords.length,
  }, countsBeforeReplay);

  await environment.checkpointStore.repository.delete(started.id, started.activeRun.id);
  assert.deepEqual(await coordinator.get({ investigationId: started.id }), completed);
});

test('model synthesis can refine Finding text without controlling outcome or replay', async () => {
  const environment = createEnvironment();
  const findingSynthesizer = createFakeFindingSynthesizer({
    output(input) {
      return {
        classification: input.expectedClassification,
        statement: 'Model-assisted summary: Site night energy was 24% above baseline, without Equipment attribution.',
        evidenceIds: input.evidence.map(({ id }) => id),
        limitations: ['Equipment attribution remains unavailable.'],
      };
    },
  });
  const coordinator = createSiteNightEnergyInvestigationCoordinator({
    ...environment.ports,
    findingSynthesizer,
  });

  const started = await coordinator.start({ tenantId, siteId });
  const completed = await coordinator.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.outcome, 'SUPPORTED_SITE_FINDING');
  assert.equal(completed.findings[0].findingKind, 'SITE_NIGHT_ENERGY_INCREASE');
  assert.equal(completed.findings[0].conclusion.status, 'SUPPORTED');
  assert.equal(completed.findings[0].conclusion.scope, 'SITE');
  assert.equal(completed.findings[0].statement.startsWith('Model-assisted summary:'), true);
  assert.equal(completed.analysisReferences[0].authority, 'DETERMINISTIC_ALGORITHM');
  assert.equal(findingSynthesizer.calls.length, 1);
  assert.equal(JSON.stringify(completed).includes('fake-provider'), false);
  assert.equal(JSON.stringify(completed).includes('limitations'), false);
  assert.equal(JSON.stringify(completed).includes('synthesis'), false);

  const persistedFinding = await environment.ports.businessRecordRepository.get(
    started.id,
    `${started.id}:finding:night-energy`,
  );
  assert.equal(persistedFinding.recordType, 'FINDING');
  assert.equal(persistedFinding.synthesis.source, 'MODEL');
  assert.equal(persistedFinding.synthesis.provider, 'fake-provider');
  assert.equal(persistedFinding.synthesis.configurationDigest, `sha256:${'0'.repeat(64)}`);
  assert.equal(persistedFinding.synthesis.fallbackReason, null);

  const replay = await coordinator.advance({ investigationId: started.id });
  assert.deepEqual(replay, completed);
  assert.equal(findingSynthesizer.calls.length, 1);
});

test('a saved Runtime Checkpoint without receipts recovers by replaying only the fixed Registry reads', async () => {
  const environment = createEnvironment();
  const coordinator = createSiteNightEnergyInvestigationCoordinator(environment.ports);
  const started = await coordinator.start({ tenantId, siteId });
  const aggregate = await environment.businessStore.repository.get(started.id);
  const internal = aggregate.view();
  const run = internal.runs.find(({ id }) => id === internal.activeRunId);

  const runtimeAdvance = await environment.coordinator.advance({
    investigationId: started.id,
    runId: run.id,
    leaseId: run.lease.id,
    expectedRevision: internal.revision,
  });
  assert.equal(runtimeAdvance.results.length, 2);
  assert.notEqual(await environment.checkpointStore.repository.load(started.id, run.id), null);
  assert.equal(environment.businessStore.businessRecords.size, 0);

  const completed = await coordinator.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.outcome, 'SUPPORTED_SITE_FINDING');
  assert.equal(completed.toolReceipts.length, 4);
  assert.equal(environment.runtime.calls.length, 1);
});

test('readiness blockers persist a stable unable-to-conclude Finding without a comparison claim', async () => {
  const environment = createEnvironment({ partial: true });
  const coordinator = createSiteNightEnergyInvestigationCoordinator(environment.ports);
  const started = await coordinator.start({ tenantId, siteId });

  const completed = await coordinator.advance({ investigationId: started.id });
  assert.equal(completed.status, 'COMPLETED');
  assert.equal(completed.outcome, 'UNABLE_TO_CONCLUDE');
  assert.equal(completed.toolReceipts.length, 4);
  assert.equal(completed.evidence.length, 1);
  assert.equal(completed.evidence[0].evidenceKind, 'SITE_ENERGY_SERIES_READINESS_ASSESSED');
  assert.equal(completed.analysisReferences.length, 1);
  assert.equal(completed.analysisReferences[0].outcome, 'UNABLE_TO_CONCLUDE');
  assert.equal(completed.findings.length, 1);
  assert.equal(completed.findings[0].findingKind, 'UNABLE_TO_CONCLUDE');
  assert.equal(completed.findings[0].conclusion.status, 'UNABLE_TO_CONCLUDE');
  assert.equal(completed.findings[0].conclusion.reasonCode, 'PARTIAL_DATASET');
  assert.equal(completed.findings[0].statement.includes('increase'), false);
});
