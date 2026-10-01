import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createInMemoryRunResourceBudgetGuard,
  runResourceOwnerResultBatchCost,
  runResourceOwnerResultBatchOperationId,
  runResourceOwnerResultCost,
  runResourceOwnerResultOperationId,
  runResourceReadBatchCost,
  runResourceReadBatchOperationId,
  toRunResourceBudgetOutcome,
} from '../dist/index.js';

const basePolicy = (limits = {}) => ({
  schemaVersion: 1,
  revision: 'test-run-resource-policy/v1',
  limits: {
    modelInvocations: 2,
    toolRequests: 2,
    wallClockMs: 1_000,
    queryRangeMs: 24 * 60 * 60 * 1_000,
    queryBuckets: 24,
    ownerRecords: 5,
    payloadBytes: 1_024,
    ...limits,
  },
});

const baseInput = (operationId, policy, cost, at = 100) => ({
  investigationId: 'investigation-budget-001',
  runId: 'run-budget-001',
  startedAt: 0,
  at,
  operationId,
  policy,
  cost,
});

test('exact retry reuses the original Tool budget while a new logical batch exhausts it', async () => {
  const guard = createInMemoryRunResourceBudgetGuard();
  const policy = basePolicy({ toolRequests: 1 });
  const firstBatch = [{
    requestId: 'registry-site',
    tool: 'registry.getSite',
    input: { siteId: 'site-001' },
  }];
  const firstOperationId = runResourceReadBatchOperationId(firstBatch);
  const firstCost = runResourceReadBatchCost(firstBatch);

  const first = await guard.check(baseInput(firstOperationId, policy, firstCost));
  assert.equal(first.decision, 'ALLOW');
  assert.equal(first.duplicate, false);
  assert.equal(first.snapshot.usage.toolRequests, 1);

  const retry = await guard.check(baseInput(firstOperationId, policy, firstCost, 200));
  assert.equal(retry.decision, 'ALLOW');
  assert.equal(retry.duplicate, true);
  assert.equal(retry.snapshot.usage.toolRequests, 1);

  const secondBatch = [{
    requestId: 'registry-asset',
    tool: 'registry.listSiteAssets',
    input: { siteId: 'site-001' },
  }];
  const denied = await guard.check(baseInput(
    runResourceReadBatchOperationId(secondBatch),
    policy,
    runResourceReadBatchCost(secondBatch),
    300,
  ));
  assert.equal(denied.decision, 'DENY');
  assert.deepEqual(toRunResourceBudgetOutcome(denied.snapshot), {
    schemaVersion: 1,
    policyRevision: policy.revision,
    outcome: 'UNABLE_TO_CONCLUDE',
    exhaustedDimension: 'TOOL_REQUESTS',
    consumed: 2,
    limit: 1,
  });
});

test('a Run cannot widen limits under the same policy revision', async () => {
  const guard = createInMemoryRunResourceBudgetGuard();
  const policy = basePolicy({ toolRequests: 1 });
  const requests = [{
    requestId: 'registry-site',
    tool: 'registry.getSite',
    input: { siteId: 'site-001' },
  }];
  const operationId = runResourceReadBatchOperationId(requests);
  const cost = runResourceReadBatchCost(requests);
  assert.equal((await guard.check(baseInput(operationId, policy, cost))).decision, 'ALLOW');

  await assert.rejects(
    guard.check(baseInput('widened-operation', basePolicy({ toolRequests: 10 }), cost, 200)),
    /policy cannot change/u,
  );
});

test('wall-clock exhaustion blocks even an otherwise exact retry', async () => {
  const guard = createInMemoryRunResourceBudgetGuard();
  const policy = basePolicy({ wallClockMs: 100 });
  const requests = [{
    requestId: 'registry-site',
    tool: 'registry.getSite',
    input: { siteId: 'site-001' },
  }];
  const operationId = runResourceReadBatchOperationId(requests);
  const cost = runResourceReadBatchCost(requests);
  assert.equal((await guard.check(baseInput(operationId, policy, cost, 50))).decision, 'ALLOW');

  const denied = await guard.check(baseInput(operationId, policy, cost, 101));
  assert.equal(denied.decision, 'DENY');
  assert.equal(denied.snapshot.exhaustion.dimension, 'WALL_CLOCK_MS');
  assert.equal(denied.snapshot.usage.toolRequests, 1);
});

test('one historical query wider than policy is rejected before it can consume Tool budget', async () => {
  const guard = createInMemoryRunResourceBudgetGuard();
  const policy = basePolicy({ queryRangeMs: 60 * 60 * 1_000 });
  const requests = [{
    requestId: 'energy-wide',
    tool: 'analytics.getEnergySeries',
    input: {
      tenantId: 'organization-001',
      siteId: 'site-001',
      energyType: 'electricity',
      granularity: 'hour',
      timezone: 'UTC',
      from: '2026-07-01T00:00:00Z',
      to: '2026-07-01T02:00:00Z',
      qualityPolicy: 'VALID_ONLY',
    },
  }];
  const denied = await guard.check(baseInput(
    runResourceReadBatchOperationId(requests),
    policy,
    runResourceReadBatchCost(requests),
  ));

  assert.equal(denied.decision, 'DENY');
  assert.equal(denied.snapshot.exhaustion.dimension, 'QUERY_RANGE_MS');
  assert.equal(denied.snapshot.usage.toolRequests, 0);
  assert.equal(denied.snapshot.usage.queryBuckets, 0);
});

test('parallel Owner results use one stable batch identity and aggregate every completed payload', () => {
  const first = {
    requestId: 'owner-result-a',
    owner: 'registry',
    scope: {
      tenantId: 'organization-001',
      siteId: 'site-001',
      assetId: null,
      deviceId: null,
    },
    revision: 'registry-r1',
    quality: 'GOOD',
    provenance: 'platform-core-service:r1',
    payload: { rows: ['a', 'b'] },
  };
  const second = {
    ...first,
    requestId: 'owner-result-b',
    payload: { rows: ['c'] },
  };
  const requests = [{
    requestId: first.requestId,
    tool: 'registry.getSite',
    input: { siteId: 'site-001' },
  }, {
    requestId: second.requestId,
    tool: 'registry.listSiteAssets',
    input: { siteId: 'site-001' },
  }];
  assert.equal(
    runResourceOwnerResultBatchOperationId(requests),
    runResourceOwnerResultBatchOperationId(requests.map((request) => ({
      ...request,
      input: { ...request.input },
    }))),
  );
  assert.notEqual(
    runResourceOwnerResultBatchOperationId(requests),
    runResourceOwnerResultBatchOperationId([
      { ...requests[0], input: { siteId: 'site-002' } },
      requests[1],
    ]),
  );
  const firstCost = runResourceOwnerResultCost(first);
  const secondCost = runResourceOwnerResultCost(second);
  assert.deepEqual(runResourceOwnerResultBatchCost([first, second]), {
    modelInvocations: 0,
    toolRequests: 0,
    queryRangeMs: 0,
    queryBuckets: 0,
    ownerRecords: firstCost.ownerRecords + secondCost.ownerRecords,
    payloadBytes: firstCost.payloadBytes + secondCost.payloadBytes,
  });
});

test('payload exhaustion after accepted Evidence is a bounded partial outcome', async () => {
  const guard = createInMemoryRunResourceBudgetGuard();
  const policy = basePolicy({ payloadBytes: 30 });
  const firstResult = {
    requestId: 'owner-small',
    owner: 'registry',
    scope: {
      tenantId: 'organization-001',
      siteId: 'site-001',
      assetId: null,
      deviceId: null,
    },
    revision: 'registry-r1',
    quality: 'GOOD',
    provenance: 'platform-core-service:r1',
    payload: { rows: ['a'] },
  };
  const first = await guard.check(baseInput(
    runResourceOwnerResultOperationId(firstResult.requestId),
    policy,
    runResourceOwnerResultCost(firstResult),
  ));
  assert.equal(first.decision, 'ALLOW');
  assert.equal(first.snapshot.usage.ownerRecords, 1);

  const secondResult = {
    ...firstResult,
    requestId: 'owner-large',
    payload: { rows: ['this payload exceeds the remaining byte budget'] },
  };
  const denied = await guard.check(baseInput(
    runResourceOwnerResultOperationId(secondResult.requestId),
    policy,
    runResourceOwnerResultCost(secondResult),
    200,
  ));
  assert.equal(denied.decision, 'DENY');
  assert.deepEqual(toRunResourceBudgetOutcome(denied.snapshot), {
    schemaVersion: 1,
    policyRevision: policy.revision,
    outcome: 'PARTIAL',
    exhaustedDimension: 'PAYLOAD_BYTES',
    consumed: denied.snapshot.exhaustion.consumed,
    limit: 30,
  });
  assert.equal(denied.snapshot.usage.ownerRecords, 1);
});
