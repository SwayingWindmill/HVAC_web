import {
  OPERATIONS_AGENT_TOOL_RECEIPT_OWNER_BY_LOGICAL_TOOL,
  type LogicalTool,
  type ToolOwner,
} from './generated-tool-contract.js';
import type {
  IdempotencyKey,
  InvestigationScope,
  OperatorInputAnalysisScope,
  OperatorInputRequestKind,
  StepIdentity,
} from './operations-investigation.js';

export type { LogicalTool, ToolOwner } from './generated-tool-contract.js';

export type InvestigationBusinessRecordType =
  | 'EVIDENCE'
  | 'ANALYSIS_REFERENCE'
  | 'FINDING'
  | 'TOOL_EXECUTION_RECEIPT'
  | 'OPERATOR_INPUT_ACCEPTED';

export type EvidenceClassification = 'FACT' | 'ALGORITHM_RESULT';
export type EvidenceOwner = 'registry' | 'telemetry-query-service';
export type EvidenceQualityClassification = 'GOOD' | 'UNCERTAIN' | 'BAD' | 'STALE';

export interface EvidenceQuality {
  readonly classification: EvidenceQualityClassification;
  readonly valid: number;
  readonly suspect: number;
  readonly invalid: number;
}

export interface EvidenceWatermark {
  readonly data: string | null;
  readonly aggregate: string | null;
}

export interface EvidenceSourceReference {
  readonly owner: EvidenceOwner;
  readonly scope: InvestigationScope;
  readonly requestId: string;
  readonly registryRevision: string | null;
  readonly datasetRevision: string | null;
  readonly watermark: EvidenceWatermark;
  readonly partial: boolean;
  readonly quality: EvidenceQuality;
  readonly capturedAt: number;
  readonly evaluatedAt: number;
  readonly provenanceDigest: string;
}

interface InvestigationBusinessRecordBase {
  readonly schemaVersion: 1;
  readonly recordType: InvestigationBusinessRecordType;
  readonly id: string;
  readonly investigationId: string;
  readonly recordedAt: number;
}

export interface EvidenceRecord extends InvestigationBusinessRecordBase {
  readonly recordType: 'EVIDENCE';
  readonly evidenceKind:
    | 'SITE_ENERGY_SERIES_READY'
    | 'SITE_ENERGY_SERIES_READINESS_ASSESSED'
    | 'SITE_ENERGY_PERIOD_COMPARISON';
  readonly classification: EvidenceClassification;
  readonly statement: string;
  readonly analysisReferenceDigest: string | null;
  readonly sources: readonly EvidenceSourceReference[];
}

export interface AnalysisReferenceRecord extends InvestigationBusinessRecordBase {
  readonly recordType: 'ANALYSIS_REFERENCE';
  readonly analysisKind: 'SITE_NIGHT_ENERGY_COMPARISON';
  readonly authority: 'DETERMINISTIC_ALGORITHM';
  readonly algorithmVersion: string;
  readonly policyVersion: string;
  readonly inputEvidenceIds: readonly string[];
  readonly parameterDigest: string;
  readonly resultDigest: string;
  readonly executedAt: number;
  readonly outcome: 'SUPPORTED_SITE_FINDING' | 'UNABLE_TO_CONCLUDE';
}

export interface FindingRequiredNextPeriod {
  readonly localDate: string;
  readonly from: string;
  readonly to: string;
  readonly expectedBuckets: number;
}

export type FindingRequiredNext =
  | {
    readonly status: 'REQUIRED_NEXT';
    readonly kind: 'EQUIPMENT_ENERGY_BINDINGS';
    readonly owner: 'registry';
    readonly capability: 'registry.getEquipmentEnergyBindings';
    readonly tenantId: string;
    readonly siteId: string;
    readonly equipmentIds: readonly string[];
    readonly targetPeriod: FindingRequiredNextPeriod;
    readonly baselinePeriod: FindingRequiredNextPeriod;
    readonly requiredMetadata: readonly [
      'BUSINESS_REVISION',
      'QUALITY',
      'CAPTURED_AT',
      'PAYLOAD_DIGEST',
    ];
  }
  | {
    readonly status: 'REQUIRED_NEXT';
    readonly kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON';
    readonly owner: 'telemetry-query-service';
    readonly capability: 'analytics.energy.getEquipmentSeries';
    readonly tenantId: string;
    readonly siteId: string;
    readonly equipmentIds: readonly string[];
    readonly targetPeriod: FindingRequiredNextPeriod;
    readonly baselinePeriod: FindingRequiredNextPeriod;
    readonly requiredMetadata: readonly [
      'DATASET_REVISION',
      'WATERMARK',
      'PARTIAL',
      'QUALITY',
      'CAPTURED_AT',
      'PAYLOAD_DIGEST',
    ];
  };

export type FindingConclusion =
  | {
    readonly status: 'SUPPORTED';
    readonly scope: 'SITE';
    readonly tenantId: string;
    readonly siteId: string;
  }
  | {
    readonly status: 'UNABLE_TO_CONCLUDE';
    readonly scope: 'SITE' | 'EQUIPMENT';
    readonly reasonCode: string;
    readonly detail: string;
    readonly requiredNext?: readonly FindingRequiredNext[];
  };

export interface FindingSynthesisMetering {
  readonly inputUnits: number;
  readonly outputUnits: number;
}

export interface FindingSynthesisProvenance {
  readonly source: 'MODEL' | 'DETERMINISTIC_FALLBACK';
  readonly provider: string | null;
  readonly model: string | null;
  readonly configurationDigest: string | null;
  readonly promptPolicyVersion: 'finding-synthesis-policy/v1';
  readonly outputSchemaVersion: 'finding-synthesis-output/v1';
  readonly inputDigest: string;
  readonly outputDigest: string;
  readonly latencyMs: number | null;
  readonly metering: FindingSynthesisMetering | null;
  readonly traceId: string | null;
  readonly fallbackReason:
    | 'NOT_CONFIGURED'
    | 'PROVIDER_ERROR'
    | 'TIMEOUT'
    | 'OUTPUT_INVALID'
    | null;
}

export interface FindingRecord extends InvestigationBusinessRecordBase {
  readonly recordType: 'FINDING';
  readonly findingKind:
    | 'SITE_NIGHT_ENERGY_INCREASE'
    | 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD'
    | 'UNABLE_TO_CONCLUDE';
  readonly classification: 'INFERENCE';
  readonly statement: string;
  readonly evidenceIds: readonly string[];
  readonly analysisReferenceIds: readonly string[];
  readonly conclusion: FindingConclusion;
  readonly synthesis?: FindingSynthesisProvenance;
}

export type ToolExecutionResultCategory = 'SUCCEEDED' | 'REJECTED' | 'TIMED_OUT' | 'FAILED';
export type ToolReceiptMetadataValue = string | number | boolean | null;

export interface ToolExecutionReceiptRecord extends InvestigationBusinessRecordBase {
  readonly recordType: 'TOOL_EXECUTION_RECEIPT';
  readonly logicalTool: LogicalTool;
  readonly owner: ToolOwner;
  readonly requestId: string;
  readonly attemptId: string;
  readonly runId: string;
  readonly stepId: StepIdentity;
  readonly startedAt: number;
  readonly completedAt: number;
  readonly resultCategory: ToolExecutionResultCategory;
  readonly metadata: Readonly<Record<string, ToolReceiptMetadataValue>>;
}

export interface OperatorInputAcceptedValues {
  readonly analysisScope: OperatorInputAnalysisScope;
  readonly operatorNote: string | null;
}

export interface OperatorInputAcceptedProvenance {
  readonly actorType: 'OPERATOR';
  readonly source: 'PLATFORM_GATEWAY';
  readonly authorizationDecisionId: string;
  readonly policyRevision: string;
  readonly submittedAt: number;
}

export interface OperatorInputAcceptedRecord extends InvestigationBusinessRecordBase {
  readonly recordType: 'OPERATOR_INPUT_ACCEPTED';
  readonly requestId: string;
  readonly runId: string;
  readonly idempotencyKey: IdempotencyKey;
  readonly inputKind: OperatorInputRequestKind;
  readonly inputDigest: string;
  readonly scope: InvestigationScope;
  readonly values: OperatorInputAcceptedValues;
  readonly provenance: OperatorInputAcceptedProvenance;
}

export type InvestigationBusinessRecord =
  | EvidenceRecord
  | AnalysisReferenceRecord
  | FindingRecord
  | ToolExecutionReceiptRecord
  | OperatorInputAcceptedRecord;

export type InvestigationBusinessRecordErrorCode = 'BUSINESS_RECORD_INVALID';

export class InvestigationBusinessRecordError extends Error {
  readonly code: InvestigationBusinessRecordErrorCode;

  constructor(message: string) {
    super(message);
    this.name = 'InvestigationBusinessRecordError';
    this.code = 'BUSINESS_RECORD_INVALID';
  }
}

const digestPattern = /^sha256:[0-9a-f]{64}$/u;
const metadataKeyPattern = /^[A-Za-z][A-Za-z0-9_.-]{0,63}$/u;
const forbiddenMetadataKeyPattern = /(authorization|cookie|credential|delegation|password|secret|token|api[-_.]?key)/iu;
const maxRecordBytes = 64 * 1024;
const maxStatementLength = 4_000;
const maxEvidenceSources = 8;
const maxReferenceIds = 32;
const maxMetadataEntries = 16;
const maxMetadataStringLength = 512;

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasExactKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
): boolean => {
  const actual = Object.keys(value);
  return actual.length === required.length && actual.every((key) => required.includes(key));
};

function fail(message: string): never {
  throw new InvestigationBusinessRecordError(message);
}

const requireRecord = (value: unknown, label: string): Record<string, unknown> => {
  if (!isRecord(value)) fail(`${label} must be an object.`);
  return value;
};

const requireString = (
  value: unknown,
  label: string,
  maximumLength = 256,
): string => {
  if (typeof value !== 'string' || value.trim().length === 0 || value.length > maximumLength) {
    fail(`${label} must be a non-empty string no longer than ${maximumLength} characters.`);
  }
  return value;
};

const requireNullableString = (
  value: unknown,
  label: string,
  maximumLength = 256,
): string | null => value === null ? null : requireString(value, label, maximumLength);

const requireTimestamp = (value: unknown, label: string): number => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    fail(`${label} must be a non-negative safe-integer timestamp.`);
  }
  return value;
};

const requireCount = (value: unknown, label: string): number => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    fail(`${label} must be a non-negative safe integer.`);
  }
  return value;
};

const requireDigest = (value: unknown, label: string): string => {
  if (typeof value !== 'string' || !digestPattern.test(value)) {
    fail(`${label} must be a lowercase SHA-256 digest.`);
  }
  return value;
};

const requireNullableDigest = (value: unknown, label: string): string | null => (
  value === null ? null : requireDigest(value, label)
);

const normalizeScope = (value: unknown, label: string): InvestigationScope => {
  const scope = requireRecord(value, label);
  if (!hasExactKeys(scope, ['tenantId', 'siteId', 'equipmentId', 'deviceId'])) {
    fail(`${label} has unsupported fields.`);
  }
  return {
    tenantId: requireString(scope.tenantId, `${label}.tenantId`),
    siteId: requireNullableString(scope.siteId, `${label}.siteId`),
    equipmentId: requireNullableString(scope.equipmentId, `${label}.equipmentId`),
    deviceId: requireNullableString(scope.deviceId, `${label}.deviceId`),
  };
};

const normalizeStringIds = (
  value: unknown,
  label: string,
  minimumLength: number,
): readonly string[] => {
  if (!Array.isArray(value) || value.length < minimumLength || value.length > maxReferenceIds) {
    fail(`${label} must contain between ${minimumLength} and ${maxReferenceIds} identities.`);
  }
  const identities = value.map((identity, index) => (
    requireString(identity, `${label}[${index}]`)
  ));
  if (new Set(identities).size !== identities.length) fail(`${label} must be unique.`);
  return identities;
};

const normalizeQuality = (value: unknown, label: string): EvidenceQuality => {
  const quality = requireRecord(value, label);
  if (!hasExactKeys(quality, ['classification', 'valid', 'suspect', 'invalid'])) {
    fail(`${label} has unsupported fields.`);
  }
  if (quality.classification !== 'GOOD'
    && quality.classification !== 'UNCERTAIN'
    && quality.classification !== 'BAD'
    && quality.classification !== 'STALE') {
    fail(`${label}.classification is unsupported.`);
  }
  return {
    classification: quality.classification,
    valid: requireCount(quality.valid, `${label}.valid`),
    suspect: requireCount(quality.suspect, `${label}.suspect`),
    invalid: requireCount(quality.invalid, `${label}.invalid`),
  };
};

const normalizeWatermark = (value: unknown, label: string): EvidenceWatermark => {
  const watermark = requireRecord(value, label);
  if (!hasExactKeys(watermark, ['data', 'aggregate'])) fail(`${label} has unsupported fields.`);
  return {
    data: requireNullableString(watermark.data, `${label}.data`),
    aggregate: requireNullableString(watermark.aggregate, `${label}.aggregate`),
  };
};

const normalizeEvidenceSource = (value: unknown, index: number): EvidenceSourceReference => {
  const label = `sources[${index}]`;
  const source = requireRecord(value, label);
  if (!hasExactKeys(source, [
    'owner',
    'scope',
    'requestId',
    'registryRevision',
    'datasetRevision',
    'watermark',
    'partial',
    'quality',
    'capturedAt',
    'evaluatedAt',
    'provenanceDigest',
  ])) fail(`${label} has unsupported fields.`);
  if (source.owner !== 'registry' && source.owner !== 'telemetry-query-service') {
    fail(`${label}.owner is unsupported.`);
  }
  const registryRevision = requireNullableString(
    source.registryRevision,
    `${label}.registryRevision`,
  );
  const datasetRevision = requireNullableString(
    source.datasetRevision,
    `${label}.datasetRevision`,
  );
  if (source.owner === 'registry' && registryRevision === null) {
    fail(`${label} requires Registry Revision.`);
  }
  if (source.owner === 'telemetry-query-service' && datasetRevision === null) {
    fail(`${label} requires Dataset Revision.`);
  }
  const watermark = normalizeWatermark(source.watermark, `${label}.watermark`);
  if (source.owner === 'telemetry-query-service'
    && watermark.data === null
    && watermark.aggregate === null) {
    fail(`${label} requires a data or aggregate Watermark.`);
  }
  if (typeof source.partial !== 'boolean') fail(`${label}.partial must be boolean.`);
  const capturedAt = requireTimestamp(source.capturedAt, `${label}.capturedAt`);
  const evaluatedAt = requireTimestamp(source.evaluatedAt, `${label}.evaluatedAt`);
  if (evaluatedAt < capturedAt) fail(`${label}.evaluatedAt cannot precede capturedAt.`);
  return {
    owner: source.owner,
    scope: normalizeScope(source.scope, `${label}.scope`),
    requestId: requireString(source.requestId, `${label}.requestId`),
    registryRevision,
    datasetRevision,
    watermark,
    partial: source.partial,
    quality: normalizeQuality(source.quality, `${label}.quality`),
    capturedAt,
    evaluatedAt,
    provenanceDigest: requireDigest(source.provenanceDigest, `${label}.provenanceDigest`),
  };
};

const normalizeSources = (value: unknown): readonly EvidenceSourceReference[] => {
  if (!Array.isArray(value) || value.length === 0 || value.length > maxEvidenceSources) {
    fail(`sources must contain between 1 and ${maxEvidenceSources} references.`);
  }
  const sources = value.map(normalizeEvidenceSource);
  const requestIdentities = sources.map(({ owner, requestId }) => `${owner}:${requestId}`);
  if (new Set(requestIdentities).size !== requestIdentities.length) {
    fail('Evidence source request identities must be unique per Owner.');
  }
  return sources;
};

const normalizeBase = <TRecordType extends InvestigationBusinessRecordType>(
  record: Record<string, unknown>,
  expectedType: TRecordType,
): {
  readonly schemaVersion: 1;
  readonly recordType: TRecordType;
  readonly id: string;
  readonly investigationId: string;
  readonly recordedAt: number;
} => {
  if (record.schemaVersion !== 1 || record.recordType !== expectedType) {
    fail(`Business record must use ${expectedType} schema version 1.`);
  }
  return {
    schemaVersion: 1,
    recordType: expectedType,
    id: requireString(record.id, 'record.id'),
    investigationId: requireString(record.investigationId, 'record.investigationId'),
    recordedAt: requireTimestamp(record.recordedAt, 'record.recordedAt'),
  };
};

const normalizeEvidence = (record: Record<string, unknown>): EvidenceRecord => {
  if (!hasExactKeys(record, [
    'schemaVersion',
    'recordType',
    'id',
    'investigationId',
    'recordedAt',
    'evidenceKind',
    'classification',
    'statement',
    'analysisReferenceDigest',
    'sources',
  ])) fail('Evidence record has unsupported fields.');
  if (record.evidenceKind !== 'SITE_ENERGY_SERIES_READY'
    && record.evidenceKind !== 'SITE_ENERGY_SERIES_READINESS_ASSESSED'
    && record.evidenceKind !== 'SITE_ENERGY_PERIOD_COMPARISON') {
    fail('Evidence kind is unsupported.');
  }
  if (record.classification !== 'FACT' && record.classification !== 'ALGORITHM_RESULT') {
    fail('Evidence classification is unsupported.');
  }
  const analysisReferenceDigest = requireNullableDigest(
    record.analysisReferenceDigest,
    'analysisReferenceDigest',
  );
  if (record.classification === 'ALGORITHM_RESULT' && analysisReferenceDigest === null) {
    fail('Algorithm-result Evidence requires an Analysis Reference digest.');
  }
  return {
    ...normalizeBase(record, 'EVIDENCE'),
    evidenceKind: record.evidenceKind,
    classification: record.classification,
    statement: requireString(record.statement, 'statement', maxStatementLength),
    analysisReferenceDigest,
    sources: normalizeSources(record.sources),
  };
};

const normalizeAnalysisReference = (record: Record<string, unknown>): AnalysisReferenceRecord => {
  if (!hasExactKeys(record, [
    'schemaVersion',
    'recordType',
    'id',
    'investigationId',
    'recordedAt',
    'analysisKind',
    'authority',
    'algorithmVersion',
    'policyVersion',
    'inputEvidenceIds',
    'parameterDigest',
    'resultDigest',
    'executedAt',
    'outcome',
  ])) fail('Analysis Reference record has unsupported fields.');
  if (record.analysisKind !== 'SITE_NIGHT_ENERGY_COMPARISON') {
    fail('Analysis kind is unsupported.');
  }
  if (record.authority !== 'DETERMINISTIC_ALGORITHM') {
    fail('Analysis authority must be deterministic algorithm execution.');
  }
  if (record.outcome !== 'SUPPORTED_SITE_FINDING' && record.outcome !== 'UNABLE_TO_CONCLUDE') {
    fail('Analysis outcome is unsupported.');
  }
  const base = normalizeBase(record, 'ANALYSIS_REFERENCE');
  const executedAt = requireTimestamp(record.executedAt, 'executedAt');
  if (executedAt > base.recordedAt) fail('executedAt cannot be after recordedAt.');
  return {
    ...base,
    analysisKind: record.analysisKind,
    authority: record.authority,
    algorithmVersion: requireString(record.algorithmVersion, 'algorithmVersion', 128),
    policyVersion: requireString(record.policyVersion, 'policyVersion', 128),
    inputEvidenceIds: normalizeStringIds(record.inputEvidenceIds, 'inputEvidenceIds', 1),
    parameterDigest: requireDigest(record.parameterDigest, 'parameterDigest'),
    resultDigest: requireDigest(record.resultDigest, 'resultDigest'),
    executedAt,
    outcome: record.outcome,
  };
};

const normalizeFindingRequiredNextPeriod = (
  value: unknown,
  label: string,
): FindingRequiredNextPeriod => {
  const period = requireRecord(value, label);
  if (!hasExactKeys(period, ['localDate', 'from', 'to', 'expectedBuckets'])) {
    fail(`${label} has unsupported fields.`);
  }
  const expectedBuckets = requireCount(period.expectedBuckets, `${label}.expectedBuckets`);
  if (expectedBuckets === 0 || expectedBuckets > 48) {
    fail(`${label}.expectedBuckets must be between 1 and 48.`);
  }
  return {
    localDate: requireString(period.localDate, `${label}.localDate`, 32),
    from: requireString(period.from, `${label}.from`, 64),
    to: requireString(period.to, `${label}.to`, 64),
    expectedBuckets,
  };
};

const normalizeFindingRequiredNext = (value: unknown, index: number): FindingRequiredNext => {
  const label = `conclusion.requiredNext[${index}]`;
  const required = requireRecord(value, label);
  if (!hasExactKeys(required, [
    'status',
    'kind',
    'owner',
    'capability',
    'tenantId',
    'siteId',
    'equipmentIds',
    'targetPeriod',
    'baselinePeriod',
    'requiredMetadata',
  ]) || required.status !== 'REQUIRED_NEXT') {
    fail(`${label} has unsupported fields.`);
  }
  const bindingMetadata = ['BUSINESS_REVISION', 'QUALITY', 'CAPTURED_AT', 'PAYLOAD_DIGEST'];
  const seriesMetadata = [
    'DATASET_REVISION',
    'WATERMARK',
    'PARTIAL',
    'QUALITY',
    'CAPTURED_AT',
    'PAYLOAD_DIGEST',
  ];
  const expectedMetadata = required.kind === 'EQUIPMENT_ENERGY_BINDINGS'
    ? bindingMetadata
    : required.kind === 'EQUIPMENT_ENERGY_PERIOD_COMPARISON'
      ? seriesMetadata
      : null;
  if (expectedMetadata === null
    || !Array.isArray(required.requiredMetadata)
    || required.requiredMetadata.length !== expectedMetadata.length
    || required.requiredMetadata.some((item, metadataIndex) => item !== expectedMetadata[metadataIndex])) {
    fail(`${label}.requiredMetadata is invalid.`);
  }
  if ((required.kind === 'EQUIPMENT_ENERGY_BINDINGS'
      && (required.owner !== 'registry'
        || required.capability !== 'registry.getEquipmentEnergyBindings'))
    || (required.kind === 'EQUIPMENT_ENERGY_PERIOD_COMPARISON'
      && (required.owner !== 'telemetry-query-service'
        || required.capability !== 'analytics.energy.getEquipmentSeries'))) {
    fail(`${label} Owner and capability do not match its kind.`);
  }
  const common = {
    status: 'REQUIRED_NEXT' as const,
    tenantId: requireString(required.tenantId, `${label}.tenantId`),
    siteId: requireString(required.siteId, `${label}.siteId`),
    equipmentIds: normalizeStringIds(required.equipmentIds, `${label}.equipmentIds`, 0),
    targetPeriod: normalizeFindingRequiredNextPeriod(required.targetPeriod, `${label}.targetPeriod`),
    baselinePeriod: normalizeFindingRequiredNextPeriod(required.baselinePeriod, `${label}.baselinePeriod`),
  };
  if (required.kind === 'EQUIPMENT_ENERGY_BINDINGS') {
    return {
      ...common,
      kind: 'EQUIPMENT_ENERGY_BINDINGS',
      owner: 'registry',
      capability: 'registry.getEquipmentEnergyBindings',
      requiredMetadata: [
        'BUSINESS_REVISION',
        'QUALITY',
        'CAPTURED_AT',
        'PAYLOAD_DIGEST',
      ],
    };
  }
  return {
    ...common,
    kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
    owner: 'telemetry-query-service',
    capability: 'analytics.energy.getEquipmentSeries',
    requiredMetadata: [
      'DATASET_REVISION',
      'WATERMARK',
      'PARTIAL',
      'QUALITY',
      'CAPTURED_AT',
      'PAYLOAD_DIGEST',
    ],
  };
};

const normalizeFindingConclusion = (value: unknown): FindingConclusion => {
  const conclusion = requireRecord(value, 'conclusion');
  if (conclusion.status === 'SUPPORTED') {
    if (!hasExactKeys(conclusion, ['status', 'scope', 'tenantId', 'siteId'])
      || conclusion.scope !== 'SITE') {
      fail('Supported Findings are limited to Site Scope in schema version 1.');
    }
    return {
      status: 'SUPPORTED',
      scope: 'SITE',
      tenantId: requireString(conclusion.tenantId, 'conclusion.tenantId'),
      siteId: requireString(conclusion.siteId, 'conclusion.siteId'),
    };
  }
  const hasLegacyShape = hasExactKeys(conclusion, ['status', 'scope', 'reasonCode', 'detail']);
  const hasRequiredNextShape = hasExactKeys(
    conclusion,
    ['status', 'scope', 'reasonCode', 'detail', 'requiredNext'],
  );
  if (conclusion.status !== 'UNABLE_TO_CONCLUDE'
    || (!hasLegacyShape && !hasRequiredNextShape)
    || (conclusion.scope !== 'SITE' && conclusion.scope !== 'EQUIPMENT')) {
    fail('Finding conclusion is unsupported.');
  }
  let requiredNext: readonly FindingRequiredNext[] | undefined;
  if (hasRequiredNextShape) {
    if (!Array.isArray(conclusion.requiredNext)
      || conclusion.requiredNext.length === 0
      || conclusion.requiredNext.length > 8) {
      fail('conclusion.requiredNext must contain between 1 and 8 requirements.');
    }
    requiredNext = conclusion.requiredNext.map(normalizeFindingRequiredNext);
  }
  return {
    status: 'UNABLE_TO_CONCLUDE',
    scope: conclusion.scope,
    reasonCode: requireString(conclusion.reasonCode, 'conclusion.reasonCode', 128),
    detail: requireString(conclusion.detail, 'conclusion.detail', maxStatementLength),
    ...(requiredNext === undefined ? {} : { requiredNext }),
  };
};

const normalizeFindingSynthesisMetering = (
  value: unknown,
): FindingSynthesisMetering | null => {
  if (value === null) return null;
  const metering = requireRecord(value, 'synthesis.metering');
  if (!hasExactKeys(metering, ['inputUnits', 'outputUnits'])) {
    fail('synthesis.metering has unsupported fields.');
  }
  const inputUnits = requireCount(metering.inputUnits, 'synthesis.metering.inputUnits');
  const outputUnits = requireCount(metering.outputUnits, 'synthesis.metering.outputUnits');
  if (inputUnits > 10_000_000 || outputUnits > 10_000_000) {
    fail('synthesis.metering exceeds the supported bound.');
  }
  return { inputUnits, outputUnits };
};

const normalizeFindingSynthesis = (value: unknown): FindingSynthesisProvenance => {
  const synthesis = requireRecord(value, 'synthesis');
  if (!hasExactKeys(synthesis, [
    'source',
    'provider',
    'model',
    'configurationDigest',
    'promptPolicyVersion',
    'outputSchemaVersion',
    'inputDigest',
    'outputDigest',
    'latencyMs',
    'metering',
    'traceId',
    'fallbackReason',
  ])) fail('Finding synthesis provenance has unsupported fields.');
  if (synthesis.source !== 'MODEL' && synthesis.source !== 'DETERMINISTIC_FALLBACK') {
    fail('Finding synthesis source is unsupported.');
  }
  if (synthesis.promptPolicyVersion !== 'finding-synthesis-policy/v1'
    || synthesis.outputSchemaVersion !== 'finding-synthesis-output/v1') {
    fail('Finding synthesis policy or output schema version is unsupported.');
  }
  const provider = requireNullableString(synthesis.provider, 'synthesis.provider');
  const model = requireNullableString(synthesis.model, 'synthesis.model');
  const configurationDigest = requireNullableDigest(
    synthesis.configurationDigest,
    'synthesis.configurationDigest',
  );
  const latencyMs = synthesis.latencyMs === null
    ? null
    : requireCount(synthesis.latencyMs, 'synthesis.latencyMs');
  if (latencyMs !== null && latencyMs > 600_000) {
    fail('synthesis.latencyMs exceeds the supported bound.');
  }
  const metering = normalizeFindingSynthesisMetering(synthesis.metering);
  const traceId = requireNullableString(synthesis.traceId, 'synthesis.traceId');
  if (synthesis.fallbackReason !== null
    && synthesis.fallbackReason !== 'NOT_CONFIGURED'
    && synthesis.fallbackReason !== 'PROVIDER_ERROR'
    && synthesis.fallbackReason !== 'TIMEOUT'
    && synthesis.fallbackReason !== 'OUTPUT_INVALID') {
    fail('Finding synthesis fallback reason is unsupported.');
  }
  if (synthesis.source === 'MODEL') {
    if (provider === null || model === null || configurationDigest === null
      || latencyMs === null || synthesis.fallbackReason !== null) {
      fail('Model synthesis provenance is incomplete or contradictory.');
    }
  } else if (synthesis.fallbackReason === null) {
    fail('Deterministic fallback provenance requires a reason.');
  } else if (synthesis.fallbackReason === 'NOT_CONFIGURED') {
    if (provider !== null || model !== null || configurationDigest !== null
      || latencyMs !== null || metering !== null || traceId !== null) {
      fail('Unconfigured synthesis fallback cannot contain Provider metadata.');
    }
  } else if (provider === null || model === null || configurationDigest === null || latencyMs === null) {
    fail('Configured synthesis fallback requires bounded Provider metadata.');
  }
  return {
    source: synthesis.source,
    provider,
    model,
    configurationDigest,
    promptPolicyVersion: 'finding-synthesis-policy/v1',
    outputSchemaVersion: 'finding-synthesis-output/v1',
    inputDigest: requireDigest(synthesis.inputDigest, 'synthesis.inputDigest'),
    outputDigest: requireDigest(synthesis.outputDigest, 'synthesis.outputDigest'),
    latencyMs,
    metering,
    traceId,
    fallbackReason: synthesis.fallbackReason,
  };
};

const normalizeFinding = (record: Record<string, unknown>): FindingRecord => {
  const hasSynthesis = Object.hasOwn(record, 'synthesis');
  if (!hasExactKeys(record, [
    'schemaVersion',
    'recordType',
    'id',
    'investigationId',
    'recordedAt',
    'findingKind',
    'classification',
    'statement',
    'evidenceIds',
    'analysisReferenceIds',
    'conclusion',
    ...(hasSynthesis ? ['synthesis'] : []),
  ])) fail('Finding record has unsupported fields.');
  if (record.findingKind !== 'SITE_NIGHT_ENERGY_INCREASE'
    && record.findingKind !== 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD'
    && record.findingKind !== 'UNABLE_TO_CONCLUDE') {
    fail('Finding kind is unsupported.');
  }
  if (record.classification !== 'INFERENCE') fail('Finding classification is unsupported.');
  const evidenceIds = normalizeStringIds(record.evidenceIds, 'evidenceIds', 0);
  const analysisReferenceIds = normalizeStringIds(
    record.analysisReferenceIds,
    'analysisReferenceIds',
    0,
  );
  if (evidenceIds.length === 0 && analysisReferenceIds.length === 0) {
    fail('Finding requires supporting Evidence or Analysis References.');
  }
  const conclusion = normalizeFindingConclusion(record.conclusion);
  if (conclusion.status === 'SUPPORTED'
    && (evidenceIds.length === 0 || analysisReferenceIds.length === 0)) {
    fail('Supported Site Findings require Evidence and Analysis References.');
  }
  if ((record.findingKind === 'UNABLE_TO_CONCLUDE')
    !== (conclusion.status === 'UNABLE_TO_CONCLUDE')) {
    fail('Finding kind and conclusion status must agree.');
  }
  return {
    ...normalizeBase(record, 'FINDING'),
    findingKind: record.findingKind,
    classification: 'INFERENCE',
    statement: requireString(record.statement, 'statement', maxStatementLength),
    evidenceIds,
    analysisReferenceIds,
    conclusion,
    ...(hasSynthesis ? { synthesis: normalizeFindingSynthesis(record.synthesis) } : {}),
  };
};

const expectedOwner = (logicalTool: LogicalTool): ToolOwner => (
  OPERATIONS_AGENT_TOOL_RECEIPT_OWNER_BY_LOGICAL_TOOL[logicalTool]
);

const normalizeMetadata = (value: unknown): Readonly<Record<string, ToolReceiptMetadataValue>> => {
  const metadata = requireRecord(value, 'metadata');
  const entries = Object.entries(metadata);
  if (entries.length > maxMetadataEntries) {
    fail(`metadata may contain at most ${maxMetadataEntries} entries.`);
  }
  const normalized: Record<string, ToolReceiptMetadataValue> = {};
  for (const [key, rawValue] of entries) {
    if (!metadataKeyPattern.test(key) || forbiddenMetadataKeyPattern.test(key)) {
      fail(`metadata key ${key} is invalid or sensitive.`);
    }
    if (rawValue === null || typeof rawValue === 'boolean') {
      normalized[key] = rawValue;
    } else if (typeof rawValue === 'number' && Number.isFinite(rawValue)) {
      normalized[key] = rawValue;
    } else if (typeof rawValue === 'string' && rawValue.length <= maxMetadataStringLength) {
      normalized[key] = rawValue;
    } else {
      fail(`metadata value for ${key} is unsupported or unbounded.`);
    }
  }
  return normalized;
};

const normalizeToolReceipt = (record: Record<string, unknown>): ToolExecutionReceiptRecord => {
  if (!hasExactKeys(record, [
    'schemaVersion',
    'recordType',
    'id',
    'investigationId',
    'recordedAt',
    'logicalTool',
    'owner',
    'requestId',
    'attemptId',
    'runId',
    'stepId',
    'startedAt',
    'completedAt',
    'resultCategory',
    'metadata',
  ])) fail('Tool Execution Receipt has unsupported fields.');
  if (record.logicalTool !== 'registry.getSite'
    && record.logicalTool !== 'registry.listSiteEquipment'
    && record.logicalTool !== 'telemetry.getCurrentSnapshot'
    && record.logicalTool !== 'analytics.getEnergySeries'
    && record.logicalTool !== 'commands.getCapabilities') {
    fail('Logical Tool is unsupported.');
  }
  const owner = record.owner;
  if (owner !== 'registry'
    && owner !== 'telemetry-query-service'
    && owner !== 'command-service') {
    fail('Tool Execution Receipt Owner is unsupported.');
  }
  if (owner !== expectedOwner(record.logicalTool)) {
    fail('Tool Execution Receipt Owner does not match the logical Tool.');
  }
  if (record.resultCategory !== 'SUCCEEDED'
    && record.resultCategory !== 'REJECTED'
    && record.resultCategory !== 'TIMED_OUT'
    && record.resultCategory !== 'FAILED') {
    fail('Tool Execution result category is unsupported.');
  }
  const base = normalizeBase(record, 'TOOL_EXECUTION_RECEIPT');
  const startedAt = requireTimestamp(record.startedAt, 'startedAt');
  const completedAt = requireTimestamp(record.completedAt, 'completedAt');
  if (completedAt < startedAt) fail('completedAt cannot precede startedAt.');
  if (base.recordedAt < completedAt) fail('recordedAt cannot precede completedAt.');
  return {
    ...base,
    logicalTool: record.logicalTool,
    owner,
    requestId: requireString(record.requestId, 'requestId'),
    attemptId: requireString(record.attemptId, 'attemptId'),
    runId: requireString(record.runId, 'runId'),
    stepId: requireString(record.stepId, 'stepId') as StepIdentity,
    startedAt,
    completedAt,
    resultCategory: record.resultCategory,
    metadata: normalizeMetadata(record.metadata),
  };
};

const normalizeOperatorInputAccepted = (
  record: Record<string, unknown>,
): OperatorInputAcceptedRecord => {
  if (!hasExactKeys(record, [
    'schemaVersion',
    'recordType',
    'id',
    'investigationId',
    'recordedAt',
    'requestId',
    'runId',
    'idempotencyKey',
    'inputKind',
    'inputDigest',
    'scope',
    'values',
    'provenance',
  ])) fail('Accepted Operator Input record has unsupported fields.');
  if (record.inputKind !== 'SITE_NIGHT_ENERGY_SCOPE_CONFIRMATION') {
    fail('Accepted Operator Input kind is unsupported.');
  }
  const values = requireRecord(record.values, 'values');
  if (!hasExactKeys(values, ['analysisScope', 'operatorNote'])
    || (values.analysisScope !== 'SITE_ONLY' && values.analysisScope !== 'DEFER')) {
    fail('Accepted Operator Input values do not match the supported schema.');
  }
  const provenance = requireRecord(record.provenance, 'provenance');
  if (!hasExactKeys(provenance, [
    'actorType',
    'source',
    'authorizationDecisionId',
    'policyRevision',
    'submittedAt',
  ])
    || provenance.actorType !== 'OPERATOR'
    || provenance.source !== 'PLATFORM_GATEWAY') {
    fail('Accepted Operator Input provenance is invalid.');
  }
  const base = normalizeBase(record, 'OPERATOR_INPUT_ACCEPTED');
  const submittedAt = requireTimestamp(provenance.submittedAt, 'provenance.submittedAt');
  if (submittedAt !== base.recordedAt) {
    fail('Accepted Operator Input provenance timestamp must match recordedAt.');
  }
  return {
    ...base,
    requestId: requireString(record.requestId, 'requestId'),
    runId: requireString(record.runId, 'runId'),
    idempotencyKey: requireString(record.idempotencyKey, 'idempotencyKey') as IdempotencyKey,
    inputKind: record.inputKind,
    inputDigest: requireDigest(record.inputDigest, 'inputDigest'),
    scope: normalizeScope(record.scope, 'scope'),
    values: {
      analysisScope: values.analysisScope,
      operatorNote: requireNullableString(values.operatorNote, 'operatorNote', 500),
    },
    provenance: {
      actorType: 'OPERATOR',
      source: 'PLATFORM_GATEWAY',
      authorizationDecisionId: requireString(
        provenance.authorizationDecisionId,
        'authorizationDecisionId',
      ),
      policyRevision: requireString(provenance.policyRevision, 'policyRevision'),
      submittedAt,
    },
  };
};

const assertBoundedRecord = (record: InvestigationBusinessRecord): InvestigationBusinessRecord => {
  const serialized = JSON.stringify(record);
  if (Buffer.byteLength(serialized, 'utf8') > maxRecordBytes) {
    fail(`Business record exceeds the ${maxRecordBytes}-byte limit.`);
  }
  return record;
};

export const createInvestigationBusinessRecord = (
  value: unknown,
): InvestigationBusinessRecord => {
  const record = requireRecord(value, 'Business record');
  let normalized: InvestigationBusinessRecord;
  if (record.recordType === 'EVIDENCE') normalized = normalizeEvidence(record);
  else if (record.recordType === 'ANALYSIS_REFERENCE') {
    normalized = normalizeAnalysisReference(record);
  } else if (record.recordType === 'FINDING') normalized = normalizeFinding(record);
  else if (record.recordType === 'TOOL_EXECUTION_RECEIPT') {
    normalized = normalizeToolReceipt(record);
  } else if (record.recordType === 'OPERATOR_INPUT_ACCEPTED') {
    normalized = normalizeOperatorInputAccepted(record);
  } else {
    fail('Business record type is unsupported.');
  }
  return assertBoundedRecord(normalized);
};

const canonicalizeJson = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalizeJson);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey))
      .map(([key, entry]) => [key, canonicalizeJson(entry)]),
  );
};

export const businessRecordsEqual = (
  left: InvestigationBusinessRecord,
  right: InvestigationBusinessRecord,
): boolean => JSON.stringify(canonicalizeJson(left)) === JSON.stringify(canonicalizeJson(right));
