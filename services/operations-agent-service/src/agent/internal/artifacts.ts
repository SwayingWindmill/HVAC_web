import { AgentContractError } from './errors.js';

export type AgentOwner =
  | 'REGISTRY'
  | 'TELEMETRY'
  | 'ENERGY'
  | 'ALARM'
  | 'FDD'
  | 'WORK_ORDER'
  | 'COMMAND';

const agentOwners: readonly AgentOwner[] = Object.freeze([
  'REGISTRY',
  'TELEMETRY',
  'ENERGY',
  'ALARM',
  'FDD',
  'WORK_ORDER',
  'COMMAND',
]);

export interface AgentEvidenceRef {
  readonly owner: AgentOwner;
  readonly resourceType: string;
  readonly resourceId: string;
  readonly revision?: string;
  readonly toolExecutionId: string;
}

export const INVESTIGATION_COMPLETE_TOOL_NAME = 'investigation.complete' as const;
export const INVESTIGATION_REQUEST_INPUT_TOOL_NAME = 'investigation.request_input' as const;

export type AgentTerminalToolName =
  | typeof INVESTIGATION_COMPLETE_TOOL_NAME
  | typeof INVESTIGATION_REQUEST_INPUT_TOOL_NAME;

export type InvestigationOutcome = 'SUPPORTED_FINDING' | 'UNABLE_TO_CONCLUDE';

export interface InvestigationComplete {
  readonly outcome: InvestigationOutcome;
  readonly summary: string;
  readonly evidenceRefs: readonly AgentEvidenceRef[];
  readonly limitations: readonly string[];
  readonly recommendedNext: readonly string[];
}

export interface InvestigationInputChoice {
  readonly value: string;
  readonly label: string;
}

export type InvestigationInputResponse =
  | Readonly<{
    kind: 'TEXT';
    maxLength: number;
  }>
  | Readonly<{
    kind: 'SINGLE_SELECT';
    choices: readonly InvestigationInputChoice[];
  }>;

export interface InvestigationRequestInput {
  readonly prompt: string;
  readonly response: InvestigationInputResponse;
}

interface AgentArtifactBase {
  readonly id: string;
  readonly sessionId: string;
  readonly runId: string;
  readonly createdAt: number;
}

export interface AgentEvidenceRefArtifact extends AgentArtifactBase {
  readonly kind: 'EVIDENCE_REF';
  readonly reference: AgentEvidenceRef;
}

export interface AgentFindingArtifact extends AgentArtifactBase {
  readonly kind: 'FINDING';
  readonly finding: InvestigationComplete;
}

export interface AgentProposalArtifact extends AgentArtifactBase {
  readonly kind: 'PROPOSAL';
  readonly proposalType: string;
  readonly summary: string;
}

export interface AgentInputRequestArtifact extends AgentArtifactBase {
  readonly kind: 'INPUT_REQUEST';
  readonly request: InvestigationRequestInput;
}

export interface AgentLimitationArtifact extends AgentArtifactBase {
  readonly kind: 'LIMITATION';
  readonly description: string;
}

export type AgentArtifact =
  | AgentEvidenceRefArtifact
  | AgentFindingArtifact
  | AgentProposalArtifact
  | AgentInputRequestArtifact
  | AgentLimitationArtifact;

export type AgentTerminalArtifact = AgentFindingArtifact | AgentInputRequestArtifact;

const invalidTerminalArtifact = (message: string): never => {
  throw new AgentContractError('TERMINAL_ARTIFACT_INVALID', message);
};

const asRecord = (value: unknown, label: string): Record<string, unknown> => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return invalidTerminalArtifact(`${label} must be an object.`);
  }
  return value as Record<string, unknown>;
};

const assertExactKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[] = [],
): void => {
  const allowed = new Set([...required, ...optional]);
  for (const key of required) {
    if (!(key in value)) invalidTerminalArtifact(`Missing required field ${key}.`);
  }
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) invalidTerminalArtifact(`Unexpected field ${key}.`);
  }
};

const nonEmptyString = (value: unknown, label: string): string => {
  if (typeof value !== 'string' || value.trim().length === 0) {
    return invalidTerminalArtifact(`${label} must be a non-empty string.`);
  }
  return value;
};

const stringList = (value: unknown, label: string): readonly string[] => {
  if (!Array.isArray(value)) return invalidTerminalArtifact(`${label} must be an array.`);
  return Object.freeze(value.map((item, index) => nonEmptyString(item, `${label}[${index}]`)));
};

const parseEvidenceRef = (value: unknown): AgentEvidenceRef => {
  const record = asRecord(value, 'evidenceRefs item');
  assertExactKeys(record, ['owner', 'resourceType', 'resourceId', 'toolExecutionId'], ['revision']);
  const owner = record.owner;
  if (typeof owner !== 'string' || !agentOwners.includes(owner as AgentOwner)) {
    return invalidTerminalArtifact('evidenceRefs owner is not a recognized authoritative owner.');
  }

  const reference = {
    owner: owner as AgentOwner,
    resourceType: nonEmptyString(record.resourceType, 'evidenceRefs resourceType'),
    resourceId: nonEmptyString(record.resourceId, 'evidenceRefs resourceId'),
    toolExecutionId: nonEmptyString(record.toolExecutionId, 'evidenceRefs toolExecutionId'),
  };
  if (!('revision' in record)) return Object.freeze(reference);
  return Object.freeze({
    ...reference,
    revision: nonEmptyString(record.revision, 'evidenceRefs revision'),
  });
};

export const parseInvestigationComplete = (value: unknown): InvestigationComplete => {
  const record = asRecord(value, 'investigation.complete input');
  assertExactKeys(record, ['outcome', 'summary', 'evidenceRefs', 'limitations', 'recommendedNext']);

  if (record.outcome !== 'SUPPORTED_FINDING' && record.outcome !== 'UNABLE_TO_CONCLUDE') {
    return invalidTerminalArtifact('investigation.complete outcome is invalid.');
  }
  if (!Array.isArray(record.evidenceRefs)) {
    return invalidTerminalArtifact('investigation.complete evidenceRefs must be an array.');
  }

  const evidenceRefs = Object.freeze(record.evidenceRefs.map(parseEvidenceRef));
  if (record.outcome === 'SUPPORTED_FINDING' && evidenceRefs.length === 0) {
    return invalidTerminalArtifact('SUPPORTED_FINDING requires at least one evidence reference.');
  }

  return Object.freeze({
    outcome: record.outcome,
    summary: nonEmptyString(record.summary, 'investigation.complete summary'),
    evidenceRefs,
    limitations: stringList(record.limitations, 'investigation.complete limitations'),
    recommendedNext: stringList(record.recommendedNext, 'investigation.complete recommendedNext'),
  });
};

const parseChoice = (value: unknown, index: number): InvestigationInputChoice => {
  const record = asRecord(value, `response choices[${index}]`);
  assertExactKeys(record, ['value', 'label']);
  return Object.freeze({
    value: nonEmptyString(record.value, `response choices[${index}].value`),
    label: nonEmptyString(record.label, `response choices[${index}].label`),
  });
};

const parseInputResponse = (value: unknown): InvestigationInputResponse => {
  const record = asRecord(value, 'investigation.request_input response');
  const kind = record.kind;
  if (kind === 'TEXT') {
    assertExactKeys(record, ['kind', 'maxLength']);
    if (!Number.isSafeInteger(record.maxLength) || (record.maxLength as number) <= 0) {
      return invalidTerminalArtifact('TEXT response maxLength must be a positive safe integer.');
    }
    return Object.freeze({ kind, maxLength: record.maxLength as number });
  }
  if (kind === 'SINGLE_SELECT') {
    assertExactKeys(record, ['kind', 'choices']);
    if (!Array.isArray(record.choices) || record.choices.length === 0) {
      return invalidTerminalArtifact('SINGLE_SELECT response requires at least one choice.');
    }
    return Object.freeze({
      kind,
      choices: Object.freeze(record.choices.map(parseChoice)),
    });
  }
  return invalidTerminalArtifact('investigation.request_input response kind is invalid.');
};

export const parseInvestigationRequestInput = (value: unknown): InvestigationRequestInput => {
  const record = asRecord(value, 'investigation.request_input input');
  assertExactKeys(record, ['prompt', 'response']);
  return Object.freeze({
    prompt: nonEmptyString(record.prompt, 'investigation.request_input prompt'),
    response: parseInputResponse(record.response),
  });
};
