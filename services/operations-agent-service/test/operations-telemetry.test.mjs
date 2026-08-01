import assert from 'node:assert/strict';
import test from 'node:test';

import {
  safeAddOperationsTelemetryCounter,
  safeStartOperationsTelemetrySpan,
} from '../dist/index.js';
import {
  createMemoryOperationsTelemetryExporter,
  createOperationsOtlpHttpExporter,
  createOperationsTelemetryRuntime,
  hashOperationsTelemetryIdentity,
} from '../dist/observability/index.js';
import { createFakeOperationsAgentEnvironment } from './support/fake-operations-agent-environment.mjs';

const parentTraceparent = '00-' + '1'.repeat(32) + '-' + '2'.repeat(16) + '-01';

test('Operations telemetry preserves W3C parent-child correlation and hashes durable identities', async () => {
  const exporter = createMemoryOperationsTelemetryExporter();
  let now = 100;
  const telemetry = createOperationsTelemetryRuntime({ exporter, now: () => now });
  const server = telemetry.startSpan({
    name: 'operations.http.request',
    kind: 'SERVER',
    correlation: {
      traceparent: parentTraceparent,
      requestId: 'request-visible-only-inside-runtime',
      investigationId: 'investigation-001',
      runId: 'run-001',
      stepId: 'step-001',
    },
    attributes: { operation: 'ADVANCE' },
  });
  now = 110;
  const child = telemetry.startSpan({
    name: 'operations.runtime.plan',
    kind: 'INTERNAL',
    correlation: {
      traceparent: server.traceparent,
      investigationId: 'investigation-001',
      runId: 'run-001',
    },
    attributes: { operation: 'PLAN_READS' },
  });
  now = 120;
  child.setStatus('SUCCESS');
  child.end();
  now = 125;
  server.setStatus('SUCCESS');
  server.end();
  await telemetry.flush();

  const spans = exporter.spans();
  assert.equal(spans.length, 2);
  const serverData = spans.find(({ name }) => name === 'operations.http.request');
  const childData = spans.find(({ name }) => name === 'operations.runtime.plan');
  assert.ok(serverData);
  assert.ok(childData);
  assert.equal(serverData.traceId, '1'.repeat(32));
  assert.equal(serverData.parentSpanId, '2'.repeat(16));
  assert.equal(childData.traceId, serverData.traceId);
  assert.equal(childData.parentSpanId, serverData.spanId);
  assert.equal(
    serverData.attributes['operations.investigation.correlation'],
    hashOperationsTelemetryIdentity('investigation', 'investigation-001'),
  );
  const serialized = JSON.stringify(spans);
  assert.doesNotMatch(serialized, /investigation-001|run-001|step-001|request-visible/u);
});

test('durable correlation hashes remain stable across runtime restart and new traces', async () => {
  const firstExporter = createMemoryOperationsTelemetryExporter();
  const secondExporter = createMemoryOperationsTelemetryExporter();
  const first = createOperationsTelemetryRuntime({ exporter: firstExporter });
  const second = createOperationsTelemetryRuntime({ exporter: secondExporter });
  for (const telemetry of [first, second]) {
    const span = telemetry.startSpan({
      name: 'operations.runtime.step',
      kind: 'INTERNAL',
      correlation: {
        investigationId: 'investigation-restart',
        runId: 'run-restart',
        stepId: 'step-restart',
      },
      attributes: { operation: 'EXECUTE_STEP', restarted: true },
    });
    span.setStatus('SUCCESS');
    span.end();
    await telemetry.flush();
  }
  const firstAttributes = firstExporter.spans()[0].attributes;
  const secondAttributes = secondExporter.spans()[0].attributes;
  for (const key of [
    'operations.investigation.correlation',
    'operations.run.correlation',
    'operations.step.correlation',
  ]) {
    assert.equal(firstAttributes[key], secondAttributes[key]);
  }
  assert.notEqual(firstExporter.spans()[0].traceId, secondExporter.spans()[0].traceId);
});

test('raw content is rejected while bounded counts and fixed categories are retained', async () => {
  const exporter = createMemoryOperationsTelemetryExporter();
  const telemetry = createOperationsTelemetryRuntime({ exporter });
  const span = telemetry.startSpan({
    name: 'operations.model.call',
    kind: 'CLIENT',
    attributes: {
      operation: 'SYNTHESIZE_FINDING',
      outcome: 'SUCCESS',
      modelInputTokens: 17,
      modelOutputTokens: 9,
      rawPrompt: 'secret prompt content',
      completion: 'secret completion content',
      operatorNote: 'private operator text',
      ownerPayload: { raw: true },
      authorization: 'Bearer secret-token',
    },
  });
  span.setStatus('SUCCESS');
  span.end();
  await telemetry.flush();

  const data = exporter.spans()[0];
  assert.equal(data.attributes['operations.modelInputTokens'], 17);
  assert.equal(data.attributes['operations.modelOutputTokens'], 9);
  const serialized = JSON.stringify(data);
  assert.doesNotMatch(serialized, /secret prompt|secret completion|private operator|Bearer|ownerPayload/u);
  assert.equal(telemetry.diagnostics().rejectedAttributes, 5);
});

test('metrics accept only fixed low-cardinality labels', () => {
  const telemetry = createOperationsTelemetryRuntime();
  telemetry.addCounter({
    name: 'operations_agent_requests_total',
    labels: { operation: 'GET', outcome: 'SUCCESS' },
  });
  telemetry.addCounter({
    name: 'operations_agent_requests_total',
    labels: { operation: 'investigation-001', outcome: 'SUCCESS' },
  });
  telemetry.addCounter({
    name: 'operations_agent_tool_calls_total',
    labels: { logicalTool: 'registry.getSite', requestId: 'request-001' },
  });
  assert.equal(telemetry.metrics().length, 1);
  assert.deepEqual(telemetry.metrics()[0].labels, { operation: 'GET', outcome: 'SUCCESS' });
  assert.equal(telemetry.diagnostics().rejectedMetrics, 2);
});

test('exporter failures and telemetry implementation failures never escape to business callers', async () => {
  const telemetry = createOperationsTelemetryRuntime({
    exporter: {
      export() {
        throw new Error('collector unavailable');
      },
    },
  });
  const span = telemetry.startSpan({
    name: 'operations.business.commit',
    kind: 'INTERNAL',
    attributes: { operation: 'COMMIT_EFFECT' },
  });
  assert.doesNotThrow(() => {
    span.setStatus('SUCCESS');
    span.end();
  });
  await assert.doesNotReject(telemetry.flush());
  assert.equal(telemetry.diagnostics().failedExports, 1);

  const throwingTelemetry = {
    startSpan() { throw new Error('broken tracer'); },
    addCounter() { throw new Error('broken meter'); },
    observeHistogram() { throw new Error('broken histogram'); },
  };
  const safeSpan = safeStartOperationsTelemetrySpan(throwingTelemetry, {
    name: 'operations.business.commit',
    kind: 'INTERNAL',
  });
  const throwingSpanTelemetry = {
    startSpan() {
      return {
        traceparent: 'invalid-trace-context',
        tracestate: 'invalid' + String.fromCharCode(10) + 'state',
        setAttributes() { throw new Error('broken attributes'); },
        setStatus() { throw new Error('broken status'); },
        end() { throw new Error('broken end'); },
      };
    },
    addCounter() {},
    observeHistogram() {},
  };
  const lifecycleSafeSpan = safeStartOperationsTelemetrySpan(throwingSpanTelemetry, {
    name: 'operations.business.commit',
    kind: 'INTERNAL',
  });
  assert.equal(lifecycleSafeSpan.traceparent, undefined);
  assert.equal(lifecycleSafeSpan.tracestate, undefined);
  assert.doesNotThrow(() => {
    safeSpan.setStatus('SUCCESS');
    safeSpan.end();
    lifecycleSafeSpan.setAttributes({ operation: 'COMMIT_EFFECT' });
    lifecycleSafeSpan.setStatus('SUCCESS');
    lifecycleSafeSpan.end();
    safeAddOperationsTelemetryCounter(throwingTelemetry, {
      name: 'operations_agent_business_commits_total',
      labels: { operation: 'COMMIT_EFFECT', outcome: 'SUCCESS' },
    });
  });
});

test('bounded queue drops excess diagnostic spans without blocking callers', async () => {
  const telemetry = createOperationsTelemetryRuntime({ maximumQueuedSpans: 1 });
  for (let index = 0; index < 3; index += 1) {
    const span = telemetry.startSpan({
      name: 'operations.runtime.step',
      kind: 'INTERNAL',
      attributes: { operation: 'EXECUTE_STEP' },
    });
    span.setStatus('SUCCESS');
    span.end();
  }
  assert.equal(telemetry.diagnostics().droppedSpans, 2);
  await telemetry.flush();
});


test('Coordinator telemetry correlates Runtime, Tool, Owner, budget, commit and terminal work without entering authority records', async () => {
  const exporter = createMemoryOperationsTelemetryExporter();
  const telemetry = createOperationsTelemetryRuntime({ exporter });
  const scope = Object.freeze({
    organizationId: 'organization-observability',
    siteId: 'site-observability',
    equipmentId: null,
    deviceId: null,
  });
  const environment = createFakeOperationsAgentEnvironment({
    scope,
    telemetry,
    telemetryContext: {
      requestId: 'request-observability',
      traceparent: parentTraceparent,
    },
    runtimeSteps: [{
      stepId: 'step-observability',
      plan: {
        batches: [{
          batchId: 'batch-observability',
          requests: [{
            requestId: 'read-observability',
            tool: 'registry.getSite',
            input: { siteId: scope.siteId },
          }],
        }],
      },
      checkpointPosition: 'after-observability',
    }],
  });
  const created = await environment.coordinator.create({ scope });
  const started = await environment.coordinator.start({
    investigationId: created.id,
    runtimeRevision: 'runtime-observability-v1',
    expectedRevision: created.revision,
  });
  const run = started.runs[0];
  const advanced = await environment.coordinator.advance({
    investigationId: started.id,
    runId: run.id,
    leaseId: run.lease.id,
    expectedRevision: started.revision,
  });
  assert.equal(advanced.outcome, 'READ_PLAN_COMPLETED');
  await environment.coordinator.complete({
    investigationId: started.id,
    runId: run.id,
    leaseId: run.lease.id,
    expectedRevision: advanced.investigation.revision,
  });
  await telemetry.flush();

  const spans = exporter.spans();
  const names = new Set(spans.map(({ name }) => name));
  for (const expected of [
    'operations.budget.check',
    'operations.runtime.plan',
    'operations.runtime.step',
    'operations.tool.call',
    'operations.owner.request',
    'operations.business.commit',
    'operations.run.terminal',
  ]) {
    assert.equal(names.has(expected), true, expected);
  }
  assert.equal(spans.every(({ traceId }) => traceId === '1'.repeat(32)), true);
  const serializedSpans = JSON.stringify(spans);
  assert.doesNotMatch(
    serializedSpans,
    /request-observability|investigation-001|run-001|batch-observability|read-observability/u,
  );
  assert.match(serializedSpans, /operations\.investigation\.correlation/u);

  const authoritative = JSON.stringify({
    records: [...environment.businessStore.records.values()].map((record) => record.view()),
    businessRecords: [...environment.businessStore.businessRecords.values()],
    outbox: environment.businessStore.outboxEvents,
    audit: environment.businessStore.auditRecords,
  });
  assert.doesNotMatch(authoritative, /traceparent|tracestate|spanId|telemetry/u);
});


test('OTLP export groups spans by bounded service identity and rejects credential-bearing endpoints', async () => {
  const calls = [];
  const exporter = createOperationsOtlpHttpExporter({
    endpoint: 'https://collector.example/otel',
    fetchImplementation: async (url, init) => {
      calls.push({ url, init });
      return new Response(null, { status: 200 });
    },
  });
  const span = (service, traceId, spanId) => ({
    service,
    name: 'operations.run.terminal',
    kind: 'INTERNAL',
    traceId,
    spanId,
    parentSpanId: null,
    traceState: null,
    startedAt: 10,
    completedAt: 11,
    status: 'SUCCESS',
    attributes: { 'operations.outcome': 'SUCCESS' },
  });
  await exporter.export([
    span('operations-agent-service', '1'.repeat(32), '2'.repeat(16)),
    span('platform-gateway', '3'.repeat(32), '4'.repeat(16)),
  ]);
  assert.equal(calls[0].url, 'https://collector.example/otel/v1/traces');
  const payload = JSON.parse(calls[0].init.body);
  assert.deepEqual(
    payload.resourceSpans.map(({ resource }) => resource.attributes[0].value.stringValue).sort(),
    ['operations-agent-service', 'platform-gateway'],
  );
  assert.throws(
    () => createOperationsOtlpHttpExporter({ endpoint: 'https://user:secret@collector.example/otel' }),
    /without credentials/u,
  );
});
