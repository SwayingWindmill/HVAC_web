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

const assertRecordError = (run) => assert.throws(run, (error) => (
  error instanceof InvestigationBusinessRecordError
    && error.code === 'BUSINESS_RECORD_INVALID'
));

test('typed business records preserve bounded authority and provenance fields', () => {
  assert.deepEqual(createInvestigationBusinessRecord(evidence), evidence);
  assert.deepEqual(createInvestigationBusinessRecord(analysisReference), analysisReference);
  assert.deepEqual(createInvestigationBusinessRecord(finding), finding);
  assert.deepEqual(createInvestigationBusinessRecord(receipt), receipt);
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
    },
  });
  assert.equal(unable.recordType, 'FINDING');
  assert.equal(unable.conclusion.status, 'UNABLE_TO_CONCLUDE');

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
