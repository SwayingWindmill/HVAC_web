import type {
  AnalysisReferenceRecord,
  EvidenceRecord,
  InvestigationScope,
} from '../../domain/index.js';
import { sha256Hex } from './sha256.js';

export const FINDING_SYNTHESIS_PROMPT_POLICY_VERSION = 'finding-synthesis-policy/v1' as const;
export const FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION = 'finding-synthesis-output/v1' as const;

export type FindingSynthesisClassification = 'INFERENCE' | 'UNABLE_TO_CONCLUDE';
export type FindingSynthesisFallbackReason =
  | 'NOT_CONFIGURED'
  | 'PROVIDER_ERROR'
  | 'TIMEOUT'
  | 'OUTPUT_INVALID';

export interface FindingSynthesisInput {
  readonly schemaVersion: 1;
  readonly investigationId: string;
  readonly scope: InvestigationScope;
  readonly expectedClassification: FindingSynthesisClassification;
  readonly deterministicStatement: string;
  readonly evidence: readonly Pick<EvidenceRecord, 'id' | 'classification' | 'statement'>[];
  readonly analysisReferences: readonly Pick<AnalysisReferenceRecord, 'id' | 'outcome'>[];
  readonly promptPolicyVersion: typeof FINDING_SYNTHESIS_PROMPT_POLICY_VERSION;
  readonly outputSchemaVersion: typeof FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION;
  readonly untrustedContentPolicy: 'DATA_ONLY';
}

export interface FindingSynthesisProviderMetadata {
  readonly provider: string;
  readonly model: string;
  readonly configurationDigest: string;
  readonly latencyMs: number;
  readonly tokenUsage: { readonly inputTokens: number; readonly outputTokens: number } | null;
  readonly traceId: string | null;
}

export interface FindingSynthesisProviderResponse {
  readonly output: unknown;
  readonly metadata: FindingSynthesisProviderMetadata;
}

export interface FindingSynthesizerDescriptor {
  readonly provider: string;
  readonly model: string;
  readonly configurationDigest: string;
}

export interface FindingSynthesizer {
  readonly descriptor: FindingSynthesizerDescriptor;
  synthesize(input: FindingSynthesisInput): Promise<FindingSynthesisProviderResponse>;
}

export interface FindingSynthesisDecision {
  readonly source: 'MODEL' | 'DETERMINISTIC_FALLBACK';
  readonly classification: FindingSynthesisClassification;
  readonly statement: string;
  readonly evidenceIds: readonly string[];
  readonly limitations: readonly string[];
  readonly inputDigest: string;
  readonly outputDigest: string;
  readonly invocation: (FindingSynthesisProviderMetadata & {
    readonly promptPolicyVersion: typeof FINDING_SYNTHESIS_PROMPT_POLICY_VERSION;
    readonly outputSchemaVersion: typeof FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION;
    readonly inputDigest: string;
    readonly outputDigest: string;
  }) | null;
  readonly fallbackReason: FindingSynthesisFallbackReason | null;
}

export interface SynthesizeFindingInput {
  readonly synthesizer?: FindingSynthesizer;
  readonly timeoutMs?: number;
  readonly investigationId: string;
  readonly scope: InvestigationScope;
  readonly expectedClassification: FindingSynthesisClassification;
  readonly deterministicStatement: string;
  readonly evidence: readonly Pick<EvidenceRecord, 'id' | 'classification' | 'statement'>[];
  readonly analysisReferences: readonly Pick<AnalysisReferenceRecord, 'id' | 'outcome'>[];
}

const maximumIdentityCharacters = 256;
const maximumStatementCharacters = 4_000;
const maximumMetadataCharacters = 256;
const maximumLatencyMs = 600_000;
const defaultTimeoutMs = 2_000;
const forbiddenExecutionClaims = [
  /\bcommand intent (?:was )?created\b/iu,
  /\bphysical (?:command )?execution (?:was )?(?:completed|successful)\b/iu,
  /\bsetpoint (?:was )?(?:changed|applied|written)\b/iu,
  /\bcommand (?:was )?(?:executed|approved)\b/iu,
];
const forbiddenUnableConclusionClaims = [
  /\bnight[- ]energy (?:increased|was higher)\b/iu,
  /\bincrease of \d/iu,
  /\b\d+(?:\.\d+)?% (?:increase|higher)\b/iu,
];

class FindingSynthesisTimeoutError extends Error {}

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasExactKeys = (value: Record<string, unknown>, expected: readonly string[]): boolean => {
  const keys = Object.keys(value);
  return keys.length === expected.length && keys.every((key) => expected.includes(key));
};

const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => [key, canonicalize(item)]),
  );
};

const digest = (value: unknown): string => `sha256:${sha256Hex(JSON.stringify(canonicalize(value)))}`;
const boundedString = (value: unknown, maximum: number): value is string => (
  typeof value === 'string' && value.trim().length > 0 && value.length <= maximum
);
const isLowercaseDigest = (value: unknown): value is string => (
  typeof value === 'string'
  && value.length === 71
  && value.startsWith('sha256:')
  && [...value.slice(7)].every((character) => (
    (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')
  ))
);

export const FINDING_SYNTHESIS_DEFAULT_TIMEOUT_MS = defaultTimeoutMs;

const normalizeTokenUsage = (
  value: unknown,
): FindingSynthesisProviderMetadata['tokenUsage'] => {
  if (value === null) return null;
  if (!isRecord(value)
    || !hasExactKeys(value, ['inputTokens', 'outputTokens'])
    || typeof value.inputTokens !== 'number'
    || !Number.isSafeInteger(value.inputTokens)
    || value.inputTokens < 0
    || value.inputTokens > 10_000_000
    || typeof value.outputTokens !== 'number'
    || !Number.isSafeInteger(value.outputTokens)
    || value.outputTokens < 0
    || value.outputTokens > 10_000_000) {
    throw new Error('Finding synthesis token usage is invalid.');
  }
  return Object.freeze({
    inputTokens: value.inputTokens,
    outputTokens: value.outputTokens,
  });
};

const normalizeSynthesizerDescriptor = (value: unknown): FindingSynthesizerDescriptor => {
  if (!isRecord(value) || !hasExactKeys(value, ['provider', 'model', 'configurationDigest'])) {
    throw new Error('Finding synthesizer descriptor is invalid.');
  }
  if (!boundedString(value.provider, maximumMetadataCharacters)
    || !boundedString(value.model, maximumMetadataCharacters)
    || !isLowercaseDigest(value.configurationDigest)) {
    throw new Error('Finding synthesizer descriptor contains unsupported values.');
  }
  return Object.freeze({
    provider: value.provider,
    model: value.model,
    configurationDigest: value.configurationDigest,
  });
};

const normalizeProviderMetadata = (value: unknown): FindingSynthesisProviderMetadata => {
  if (!isRecord(value) || !hasExactKeys(value, [
    'provider', 'model', 'configurationDigest', 'latencyMs', 'tokenUsage', 'traceId',
  ])) {
    throw new Error('Finding synthesis provider metadata is invalid.');
  }
  const provider = value.provider;
  const model = value.model;
  const configurationDigest = value.configurationDigest;
  const latencyMs = value.latencyMs;
  const traceId = value.traceId;
  if (!boundedString(provider, maximumMetadataCharacters)
    || !boundedString(model, maximumMetadataCharacters)
    || !isLowercaseDigest(configurationDigest)
    || typeof latencyMs !== 'number'
    || !Number.isSafeInteger(latencyMs)
    || latencyMs < 0
    || latencyMs > maximumLatencyMs
    || (traceId !== null && !boundedString(traceId, maximumMetadataCharacters))) {
    throw new Error('Finding synthesis provider metadata contains unsupported values.');
  }
  return Object.freeze({
    provider,
    model,
    configurationDigest,
    latencyMs,
    tokenUsage: normalizeTokenUsage(value.tokenUsage),
    traceId: traceId as string | null,
  });
};

const normalizeCandidate = (
  value: unknown,
  input: FindingSynthesisInput,
): Pick<FindingSynthesisDecision, 'classification' | 'statement' | 'evidenceIds' | 'limitations'> => {
  if (!isRecord(value) || !hasExactKeys(value, [
    'classification', 'statement', 'evidenceIds', 'limitations',
  ])) {
    throw new Error('Finding synthesis output must match finding-synthesis-output/v1 exactly.');
  }
  if (value.classification !== input.expectedClassification) {
    throw new Error('Finding synthesis output contains an unsupported classification.');
  }
  if (!boundedString(value.statement, maximumStatementCharacters)) {
    throw new Error('Finding synthesis output contains an unsupported statement.');
  }
  const statement = value.statement;
  if (forbiddenExecutionClaims.some((pattern) => pattern.test(statement))) {
    throw new Error('Finding synthesis output contains an unsupported execution claim.');
  }
  if (input.expectedClassification === 'UNABLE_TO_CONCLUDE'
    && forbiddenUnableConclusionClaims.some((pattern) => pattern.test(statement))) {
    throw new Error('Unable-to-conclude synthesis cannot claim a confirmed increase.');
  }
  if (!Array.isArray(value.evidenceIds)
    || value.evidenceIds.length === 0
    || value.evidenceIds.length > input.evidence.length) {
    throw new Error('Finding synthesis evidence references are invalid.');
  }
  const allowedEvidenceIds = new Set(input.evidence.map(({ id }) => id));
  const evidenceIds: string[] = [];
  for (const evidenceId of value.evidenceIds) {
    if (typeof evidenceId !== 'string'
      || !allowedEvidenceIds.has(evidenceId)
      || evidenceIds.includes(evidenceId)) {
      throw new Error('Finding synthesis referenced unknown or duplicate Evidence.');
    }
    evidenceIds.push(evidenceId);
  }
  if (!Array.isArray(value.limitations) || value.limitations.length > 8) {
    throw new Error('Finding synthesis limitations are invalid.');
  }
  const limitations: string[] = [];
  for (const limitation of value.limitations) {
    if (!boundedString(limitation, 500) || limitations.includes(limitation)) {
      throw new Error('Finding synthesis limitations contain unsupported values.');
    }
    limitations.push(limitation);
  }
  return Object.freeze({
    classification: input.expectedClassification,
    statement: statement.trim(),
    evidenceIds: Object.freeze(evidenceIds),
    limitations: Object.freeze(limitations),
  });
};

const normalizeIdentity = (value: string, label: string): string => {
  if (!boundedString(value, maximumIdentityCharacters)) {
    throw new Error(`${label} must contain 1 to ${maximumIdentityCharacters} characters.`);
  }
  return value;
};

const createProviderInput = (input: SynthesizeFindingInput): FindingSynthesisInput => {
  if (input.expectedClassification !== 'INFERENCE'
    && input.expectedClassification !== 'UNABLE_TO_CONCLUDE') {
    throw new Error('Finding synthesis classification is unsupported.');
  }
  if (!boundedString(input.deterministicStatement, maximumStatementCharacters)) {
    throw new Error('Deterministic Finding statement is invalid.');
  }
  if (input.evidence.length === 0 || input.evidence.length > 32
    || input.analysisReferences.length === 0 || input.analysisReferences.length > 32) {
    throw new Error('Finding synthesis requires committed Evidence and Analysis References.');
  }
  const evidenceIds = new Set<string>();
  const evidence = input.evidence.map((record) => {
    const id = normalizeIdentity(record.id, 'Evidence identity');
    if (evidenceIds.has(id) || !boundedString(record.statement, maximumStatementCharacters)) {
      throw new Error('Finding synthesis Evidence is duplicate or invalid.');
    }
    evidenceIds.add(id);
    return Object.freeze({
      id,
      classification: record.classification,
      statement: record.statement,
    });
  });
  const analysisReferenceIds = new Set<string>();
  const analysisReferences = input.analysisReferences.map((record) => {
    const id = normalizeIdentity(record.id, 'Analysis Reference identity');
    if (analysisReferenceIds.has(id)) throw new Error('Finding synthesis Analysis Reference is duplicate.');
    analysisReferenceIds.add(id);
    return Object.freeze({ id, outcome: record.outcome });
  });
  return Object.freeze({
    schemaVersion: 1,
    investigationId: normalizeIdentity(input.investigationId, 'Investigation identity'),
    scope: Object.freeze({
      tenantId: normalizeIdentity(input.scope.tenantId, 'Tenant identity'),
      siteId: input.scope.siteId === null ? null : normalizeIdentity(input.scope.siteId, 'Site identity'),
      assetId: input.scope.assetId === null
        ? null
        : normalizeIdentity(input.scope.assetId, 'Asset identity'),
      deviceId: input.scope.deviceId === null ? null : normalizeIdentity(input.scope.deviceId, 'Device identity'),
    }),
    expectedClassification: input.expectedClassification,
    deterministicStatement: input.deterministicStatement,
    evidence: Object.freeze(evidence),
    analysisReferences: Object.freeze(analysisReferences),
    promptPolicyVersion: FINDING_SYNTHESIS_PROMPT_POLICY_VERSION,
    outputSchemaVersion: FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION,
    untrustedContentPolicy: 'DATA_ONLY',
  });
};

const withTimeout = async <T>(promise: Promise<T>, timeoutMs: number): Promise<T> => (
  new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new FindingSynthesisTimeoutError()), timeoutMs);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error: unknown) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  })
);

const fallbackDecision = (
  input: FindingSynthesisInput,
  inputDigest: string,
  fallbackReason: FindingSynthesisFallbackReason,
  descriptor: FindingSynthesizerDescriptor | null = null,
  latencyMs: number | null = null,
): FindingSynthesisDecision => {
  const output = Object.freeze({
    classification: input.expectedClassification,
    statement: input.deterministicStatement,
    evidenceIds: Object.freeze(input.evidence.map(({ id }) => id)),
    limitations: Object.freeze([] as string[]),
  });
  const outputDigest = digest(output);
  return Object.freeze({
    source: 'DETERMINISTIC_FALLBACK',
    ...output,
    inputDigest,
    outputDigest,
    invocation: descriptor === null
      ? null
      : Object.freeze({
        ...descriptor,
        latencyMs: latencyMs ?? 0,
        tokenUsage: null,
        traceId: null,
        promptPolicyVersion: FINDING_SYNTHESIS_PROMPT_POLICY_VERSION,
        outputSchemaVersion: FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION,
        inputDigest,
        outputDigest,
      }),
    fallbackReason,
  });
};

export const synthesizeFinding = async (
  input: SynthesizeFindingInput,
): Promise<FindingSynthesisDecision> => {
  const providerInput = createProviderInput(input);
  const inputDigest = digest(providerInput);
  if (input.synthesizer === undefined) {
    return fallbackDecision(providerInput, inputDigest, 'NOT_CONFIGURED');
  }
  const timeoutMs = input.timeoutMs ?? defaultTimeoutMs;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > maximumLatencyMs) {
    throw new Error('Finding synthesis timeout is invalid.');
  }
  const descriptor = normalizeSynthesizerDescriptor(input.synthesizer.descriptor);
  const startedAt = Date.now();
  const elapsedMs = (): number => Math.min(
    maximumLatencyMs,
    Math.max(0, Date.now() - startedAt),
  );
  try {
    const response = await withTimeout(input.synthesizer.synthesize(providerInput), timeoutMs);
    if (!isRecord(response) || !hasExactKeys(response, ['output', 'metadata'])) {
      return fallbackDecision(providerInput, inputDigest, 'OUTPUT_INVALID', descriptor, elapsedMs());
    }
    let candidate: Pick<
      FindingSynthesisDecision,
      'classification' | 'statement' | 'evidenceIds' | 'limitations'
    >;
    let metadata: FindingSynthesisProviderMetadata;
    try {
      candidate = normalizeCandidate(response.output, providerInput);
      metadata = normalizeProviderMetadata(response.metadata);
      if (metadata.provider !== descriptor.provider
        || metadata.model !== descriptor.model
        || metadata.configurationDigest !== descriptor.configurationDigest) {
        throw new Error('Finding synthesis response metadata does not match the configured descriptor.');
      }
    } catch {
      return fallbackDecision(providerInput, inputDigest, 'OUTPUT_INVALID', descriptor, elapsedMs());
    }
    const outputDigest = digest(candidate);
    return Object.freeze({
      source: 'MODEL',
      ...candidate,
      inputDigest,
      outputDigest,
      invocation: Object.freeze({
        ...metadata,
        promptPolicyVersion: FINDING_SYNTHESIS_PROMPT_POLICY_VERSION,
        outputSchemaVersion: FINDING_SYNTHESIS_OUTPUT_SCHEMA_VERSION,
        inputDigest,
        outputDigest,
      }),
      fallbackReason: null,
    });
  } catch (error) {
    return fallbackDecision(
      providerInput,
      inputDigest,
      error instanceof FindingSynthesisTimeoutError ? 'TIMEOUT' : 'PROVIDER_ERROR',
      descriptor,
      elapsedMs(),
    );
  }
};
