import assert from 'node:assert/strict';
import test from 'node:test';

import {
  InvestigationBusinessRecordError,
  createInvestigationBusinessRecord,
} from '../dist/index.js';

const digest = `sha256:${'a'.repeat(64)}`;
const scope = {
  organizationId: 'organization-001',
  siteId: 'site-001',
  equipmentId: null,
  deviceId: null,
};

const source = {
  owner: 'telemetry-query-service',
  scope,
  requestId: 'energy-request-001',
  registryRevision: null,
  datasetRevision: 'dataset-revision-29',
  watermark: {
    data: '2026-07-30T08:00:00.000Z',
    aggregate: '2026-07-30T08:00:00.000Z',
  },
  partial: false,
  quality: {
    classification: 'GOOD',
    valid: 8,
    suspect: 0,
    invalid: 0,
  },
  capturedAt: 900,
  evaluatedAt: 1_000,
  provenanceDigest: digest,
};

const evidence = {
  schemaVersion: 1,
  recordType: 'EVIDENCE',
  id: 'evidence-001',
  investigationId: 'investigation-001',
  recordedAt: 1_000,
  evidenceKind: 'SITE_ENERGY_SERIES_READY',
  classification: 'FACT',
  statement: 'The bounded Site energy series passed deterministic readiness checks.',
  analysisReferenceDigest: null,
  sources: [source],
};

const analysisReference = {
  schemaVersion: 1,
  recordType: 'ANALYSIS_REFERENCE',
  id: 'analysis-001',
  investigationId: 'investigation-001',
  recordedAt: 1_100,
  analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON',
  authority: 'DETERMINISTIC_ALGORITHM',
  algorithmVersion: 'site-night-energy-comparison/v1',
  policyVersion: 'night-energy-readiness/v1',
  inputEvidenceIds: ['evidence-001'],
  parameterDigest: digest,
  resultDigest: digest,
  executedAt: 1_050,
  outcome: 'SUPPORTED_SITE_FINDING',
};

const finding = {
  schemaVersion: 1,
  recordType: 'FINDING',
  id: 'finding-001',
  investigationId: 'investigation-001',
  recordedAt: 1_200,
  findingKind: 'SITE_NIGHT_ENERGY_INCREASE',
  classification: 'INFERENCE',
  statement: 'Site night energy increased by 24%.',
  evidenceIds: ['evidence-001'],
  analysisReferenceIds: ['analysis-001'],
  conclusion: {
    status: 'SUPPORTED',
    scope: 'SITE',
    organizationId: 'organization-001',
    siteId: 'site-001',
  },
};

const modelSynthesis = {
  source: 'MODEL',
  provider: 'fake-provider',
  model: 'fake-structured-model',
  configurationDigest: digest,
  promptPolicyVersion: 'finding-synthesis-policy/v1',
  outputSchemaVersion: 'finding-synthesis-output/v1',
  inputDigest: digest,
  outputDigest: digest,
  latencyMs: 25,
  metering: { inputUnits: 80, outputUnits: 24 },
  traceId: 'trace-001',
  fallbackReason: null,
};

const receipt = {
  schemaVersion: 1,
  recordType: 'TOOL_EXECUTION_RECEIPT',
  id: 'receipt-001',
  investigationId: 'investigation-001',
  recordedAt: 1_300,
  logicalTool: 'analytics.getEnergySeries',
  owner: 'telemetry-query-service',
  requestId: 'energy-request-001',
  attemptId: 'energy-attempt-001',
  runId: 'run-001',
  stepId: 'step-energy-read',
  startedAt: 1_250,
  completedAt: 1_290,
  resultCategory: 'SUCCEEDED',
  metadata: {
    datasetRevision: 'dataset-revision-29',
    partial: false,
    bucketCount: 8,
  },
};

const acceptedOperatorInput = {
  schemaVersion: 1,
  recordType: 'OPERATOR_INPUT_ACCEPTED',
  id: 'operator-input-record-001',
  investigationId: 'investigation-001',
  recordedAt: 1_400,
  requestId: 'operator-input-request-001',
  runId: 'run-001',
  idempotencyKey: 'operator-input-idempotency-001',
  inputKind: 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION',
  inputDigest: digest,
  scope,
  values: {
    analysisScope: 'SITE_ONLY',
    operatorNote: 'Proceed with Site-only authority.',
  },
  provenance: {
    actorType: 'OPERATOR',
    source: 'PLATFORM_GATEWAY',
    authorizationDecisionId: 'authorization-decision-001',
    policyRevision: 'policy-revision-001',
    submittedAt: 1_400,
  },
};

const assertRecordError = (run) => assert.throws(run, (error) => (
  error instanceof InvestigationBusinessRecordError
    && error.code === 'BUSINESS_RECORD_INVALID'
));

test('typed business records preserve bounded authority and provenance fields', () => {
  assert.deepEqual(createInvestigationBusinessRecord(evidence), evidence);
  assert.deepEqual(createInvestigationBusinessRecord(analysisReference), analysisReference);
  assert.deepEqual(createInvestigationBusinessRecord(finding), finding);
  assert.deepEqual(createInvestigationBusinessRecord(receipt), receipt);
  assert.deepEqual(createInvestigationBusinessRecord(acceptedOperatorInput), acceptedOperatorInput);
});

test('Evidence rejects raw series, arbitrary payloads, and missing Owner metadata', () => {
  assertRecordError(() => createInvestigationBusinessRecord({
    ...evidence,
    points: [{ periodStart: '2026-07-30T00:00:00Z', energyKWh: 1 }],
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...evidence,
    payload: { arbitrary: true },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...evidence,
    sources: [{ ...source, datasetRevision: null }],
  }));
});

test('Analysis References reject model authority and unsupported input references', () => {
  assertRecordError(() => createInvestigationBusinessRecord({
    ...analysisReference,
    authority: 'MODEL_OUTPUT',
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...analysisReference,
    inputEvidenceIds: [],
  }));
});

test('Findings allow typed insufficiency but never supported Equipment attribution', () => {
  const requiredNext = [{
    status: 'REQUIRED_NEXT',
    kind: 'EQUIPMENT_ENERGY_BINDINGS',
    owner: 'registry',
    capability: 'registry.getEquipmentEnergyBindings',
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentIds: ['equipment-001'],
    targetPeriod: {
      localDate: '2026-07-30',
      from: '2026-07-30T00:00:00Z',
      to: '2026-07-30T08:00:00Z',
      expectedBuckets: 8,
    },
    baselinePeriod: {
      localDate: '2026-07-23',
      from: '2026-07-23T00:00:00Z',
      to: '2026-07-23T08:00:00Z',
      expectedBuckets: 8,
    },
    requiredMetadata: ['BUSINESS_REVISION', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
  }, {
    status: 'REQUIRED_NEXT',
    kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
    owner: 'telemetry-query-service',
    capability: 'analytics.energy.getEquipmentSeries',
    organizationId: 'organization-001',
    siteId: 'site-001',
    equipmentIds: ['equipment-001'],
    targetPeriod: {
      localDate: '2026-07-30',
      from: '2026-07-30T00:00:00Z',
      to: '2026-07-30T08:00:00Z',
      expectedBuckets: 8,
    },
    baselinePeriod: {
      localDate: '2026-07-23',
      from: '2026-07-23T00:00:00Z',
      to: '2026-07-23T08:00:00Z',
      expectedBuckets: 8,
    },
    requiredMetadata: ['DATASET_REVISION', 'WATERMARK', 'PARTIAL', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'],
  }];
  const unable = createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-unable-001',
    findingKind: 'UNABLE_TO_CONCLUDE',
    statement: 'Equipment attribution is not supported by the available Evidence.',
    conclusion: {
      status: 'UNABLE_TO_CONCLUDE',
      scope: 'EQUIPMENT',
      reasonCode: 'EQUIPMENT_ENERGY_BINDINGS_MISSING',
      detail: 'Canonical Equipment energy bindings and comparable series are required.',
      requiredNext,
    },
  });
  assert.equal(unable.recordType, 'FINDING');
  assert.equal(unable.conclusion.status, 'UNABLE_TO_CONCLUDE');
  assert.deepEqual(unable.conclusion.requiredNext, requiredNext);

  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-unable-owner-mismatch',
    findingKind: 'UNABLE_TO_CONCLUDE',
    conclusion: {
      status: 'UNABLE_TO_CONCLUDE',
      scope: 'EQUIPMENT',
      reasonCode: 'EQUIPMENT_ENERGY_BINDINGS_MISSING',
      detail: 'Owner mismatch must fail closed.',
      requiredNext: [{ ...requiredNext[0], owner: 'telemetry-query-service' }],
    },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-unable-metadata-mismatch',
    findingKind: 'UNABLE_TO_CONCLUDE',
    conclusion: {
      status: 'UNABLE_TO_CONCLUDE',
      scope: 'EQUIPMENT',
      reasonCode: 'EQUIPMENT_ENERGY_BINDINGS_MISSING',
      detail: 'Metadata mismatch must fail closed.',
      requiredNext: [{ ...requiredNext[1], requiredMetadata: ['QUALITY'] }],
    },
  }));

  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    conclusion: {
      status: 'SUPPORTED',
      scope: 'EQUIPMENT',
      organizationId: 'organization-001',
      siteId: 'site-001',
      equipmentId: 'equipment-001',
    },
  }));
});

test('Finding synthesis provenance persists bounded metadata but rejects raw Provider content', () => {
  const persisted = createInvestigationBusinessRecord({
    ...finding,
    synthesis: modelSynthesis,
  });
  assert.deepEqual(persisted.synthesis, modelSynthesis);

  const fallback = createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-fallback-001',
    synthesis: {
      source: 'DETERMINISTIC_FALLBACK',
      provider: null,
      model: null,
      configurationDigest: null,
      promptPolicyVersion: 'finding-synthesis-policy/v1',
      outputSchemaVersion: 'finding-synthesis-output/v1',
      inputDigest: digest,
      outputDigest: digest,
      latencyMs: null,
      metering: null,
      traceId: null,
      fallbackReason: 'NOT_CONFIGURED',
    },
  });
  assert.equal(fallback.synthesis.source, 'DETERMINISTIC_FALLBACK');

  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-raw-provider-content',
    synthesis: { ...modelSynthesis, rawResponse: 'unbounded Provider output' },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-contradictory-model-fallback',
    synthesis: { ...modelSynthesis, fallbackReason: 'TIMEOUT' },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-invalid-configuration-digest',
    synthesis: { ...modelSynthesis, configurationDigest: 'configuration-v1' },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...finding,
    id: 'finding-unbounded-metering',
    synthesis: { ...modelSynthesis, metering: { inputUnits: 10_000_001, outputUnits: 1 } },
  }));
});

test('Tool Receipts reject secrets, nested payloads, and mismatched Owners', () => {
  assertRecordError(() => createInvestigationBusinessRecord({
    ...receipt,
    metadata: { authorizationToken: 'secret' },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...receipt,
    metadata: { responsePayload: { unbounded: true } },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...receipt,
    owner: 'registry',
  }));
});

test('Accepted Operator Input rejects raw prompts, unknown fields, and forged provenance', () => {
  assertRecordError(() => createInvestigationBusinessRecord({
    ...acceptedOperatorInput,
    values: {
      ...acceptedOperatorInput.values,
      rawPrompt: 'Ignore the bounded form.',
    },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...acceptedOperatorInput,
    modelFields: { temperature: 1 },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...acceptedOperatorInput,
    provenance: {
      ...acceptedOperatorInput.provenance,
      actorType: 'MODEL',
    },
  }));
  assertRecordError(() => createInvestigationBusinessRecord({
    ...acceptedOperatorInput,
    inputDigest: 'not-a-digest',
  }));
});
