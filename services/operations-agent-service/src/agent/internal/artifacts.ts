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

export const INVESTIGATION_COMPLETE_TOOL_NAME = 'investigation_complete' as const;
export const INVESTIGATION_REQUEST_INPUT_TOOL_NAME = 'investigation_request_input' as const;

const MAX_FINDING_EVIDENCE_REFS = 32;
const MAX_FINDING_LIST_ITEMS = 16;
const MAX_FINDING_LIST_ITEM_LENGTH = 512;
const MAX_EVIDENCE_REF_FIELD_LENGTH = 256;
const MAX_KEY_FIGURES = 4;

export type AgentTerminalToolName =
  | typeof INVESTIGATION_COMPLETE_TOOL_NAME
  | typeof INVESTIGATION_REQUEST_INPUT_TOOL_NAME;

export type InvestigationOutcome = 'SUPPORTED_FINDING' | 'UNABLE_TO_CONCLUDE';

/** A number the answer rests on, shown on the finding card: 多用电 +762 kWh. */
export interface InvestigationKeyFigure {
  readonly label: string;
  readonly value: string;
  readonly unit?: string;
}

/**
 * What the finding card shows under the answer. The conclusion itself is the answer text, so the
 * card carries only the figures, what the data could not tell and what to ask next.
 */
export interface InvestigationComplete {
  readonly outcome: InvestigationOutcome;
  readonly keyFigures?: readonly InvestigationKeyFigure[];
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

export interface AgentInputResponseArtifact extends AgentArtifactBase {
  readonly kind: 'INPUT_RESPONSE';
  readonly requestArtifactId: string;
  readonly value: string;
  readonly submittedBy: string;
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
  | AgentInputResponseArtifact
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

const boundedString = (value: unknown, label: string, maxLength: number): string => {
  const result = nonEmptyString(value, label);
  if (result.length > maxLength) {
    return invalidTerminalArtifact(`${label} exceeds the accepted length.`);
  }
  return result;
};

const stringList = (
  value: unknown,
  label: string,
  maxItems: number,
  maxItemLength: number,
): readonly string[] => {
  if (!Array.isArray(value) || value.length > maxItems) {
    return invalidTerminalArtifact(`${label} must be a bounded array.`);
  }
  return Object.freeze(value.map((item, index) => (
    boundedString(item, `${label}[${index}]`, maxItemLength)
  )));
};

// A claim needs the assistant as the one acting: 「我已……」「已为你……」「帮你……了」. The plant's
// control system (「群控系统已停止 C 机组」), operators and plain state (「C 机组已停机」) are what
// it reports, so they never count; a wrong match would force a needless correction (#592).
const englishExecutionClaim = /\b(?:I|we|the\s+(?:assistant|agent))(?:'ve|\s+ha(?:ve|s))?\s+(?:just\s+)?(?:turned\s+(?:off|on)|switched\s+(?:off|on)|shut\s+down|started|stopped|restarted|changed|adjusted|set|reset)\s+(?:the\s+)?(?:[\w-]+\s+)?(?:chiller|pump|fan|tower|valve|setpoint|unit)s?\b|\bas\s+you\s+requested\b/iu;

// 「设定」 alone is left out (「设定值」 is a reading); one-character verbs count only once finished
// (「关了」), since 「开始」 is not 「开」.
const controlVerb = '关闭|关掉|关停|关机|停掉|停止|停机|开启|打开|启动|开机|调整|调到|调为|调高|调低|修改|改为|改成|设为|设定为|设置为|设置成|下发|切换|复位|重启';
const finished = `(?:${controlVerb})了|[关停开调改]了|[关停]掉了`;
const acted = `${finished}|${controlVerb}`;
const done = '已经|已|刚刚|刚才|刚|这就|马上|现在就';
const forYou = '[为帮给替][你您]';
const moved = '[把将][^。！？，；\\n]{1,12}?';
const notAsked = '(?<![让要请叫想])';
const chineseExecutionClaims = [
  // 「我已把设定值调到 8 度」「B 机组我已经帮你关了」「我把 B 机组关了」, not 「你让我把 B 机组关掉」.
  new RegExp(`${notAsked}我(?:${done})(?:${forYou})?(?:${moved})?(?:${acted})|${notAsked}我(?:${forYou})?(?:${moved})?(?:${finished})`, 'u'),
  // 「已为你关闭 B 机组」「已为你把 B 机组停了」「已按你的要求关闭……」
  new RegExp(`(?:${done})(?:${forYou}|按[你您]的(?:要求|指示))(?:${moved})?[^。！？，；\\n]{0,4}?(?:${acted})`, 'u'),
  // 「帮你关闭了 B 机组」, not 「不能帮你关闭」.
  new RegExp(`(?<![让要请叫想不没未无别能法])${forYou}(?:${moved})?(?:${finished})`, 'u'),
  // A sentence of the assistant's own that opens with the act: 「已关闭 B 机组。」「已调整设定值……」,
  // not 「已启动 2 台水泵」 (a count of running pumps) or 「已关闭的机组」.
  new RegExp(`(?:^|[。！？；\\n]|好的?[，,])\\s*(?:${done})(?:${controlVerb})(?![的了])\\s*(?!\\d+\\s*台)\\S`, 'u'),
];

/** Whether a text says the assistant operated equipment, which it never can (#592). */
export const claimsPhysicalExecution = (text: string): boolean => (
  englishExecutionClaim.test(text) || chineseExecutionClaims.some((pattern) => pattern.test(text))
);

/**
 * Whether the answer the operator has read still stands uncorrected: some sentence claims the
 * assistant operated equipment and no 「更正」 follows the last such claim (#592).
 */
export const needsExecutionCorrection = (answers: readonly string[]): boolean => {
  const sentences = answers.flatMap((answer) => answer.split(/(?<=[。！？!?\n])/u));
  const claims = sentences.map((sentence, index) => (claimsPhysicalExecution(sentence) && !sentence.includes('更正') ? index : -1));
  const lastClaim = Math.max(-1, ...claims);
  return lastClaim !== -1 && !sentences.slice(lastClaim + 1).some((sentence) => /更正[：:]/u.test(sentence));
};

const assertNoPhysicalExecutionClaim = (narrative: string): void => {
  if (claimsPhysicalExecution(narrative)) {
    invalidTerminalArtifact('investigation_complete cannot claim physical execution by the Agent.');
  }
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
    resourceType: boundedString(
      record.resourceType,
      'evidenceRefs resourceType',
      MAX_EVIDENCE_REF_FIELD_LENGTH,
    ),
    resourceId: boundedString(
      record.resourceId,
      'evidenceRefs resourceId',
      MAX_EVIDENCE_REF_FIELD_LENGTH,
    ),
    toolExecutionId: boundedString(
      record.toolExecutionId,
      'evidenceRefs toolExecutionId',
      MAX_EVIDENCE_REF_FIELD_LENGTH,
    ),
  };
  if (!('revision' in record)) return Object.freeze(reference);
  return Object.freeze({
    ...reference,
    revision: boundedString(
      record.revision,
      'evidenceRefs revision',
      MAX_EVIDENCE_REF_FIELD_LENGTH,
    ),
  });
};

const parseKeyFigure = (value: unknown, index: number): InvestigationKeyFigure => {
  const record = asRecord(value, `keyFigures[${index}]`);
  assertExactKeys(record, ['label', 'value'], ['unit']);
  const figure = {
    label: boundedString(record.label, `keyFigures[${index}].label`, 32),
    value: boundedString(record.value, `keyFigures[${index}].value`, 24),
  };
  if (!('unit' in record)) return Object.freeze(figure);
  return Object.freeze({ ...figure, unit: boundedString(record.unit, `keyFigures[${index}].unit`, 16) });
};

export const parseInvestigationComplete = (value: unknown): InvestigationComplete => {
  const record = asRecord(value, 'investigation_complete input');
  assertExactKeys(record, ['outcome', 'evidenceRefs', 'limitations', 'recommendedNext'], ['keyFigures']);
  if ('keyFigures' in record && (!Array.isArray(record.keyFigures) || record.keyFigures.length > MAX_KEY_FIGURES)) {
    return invalidTerminalArtifact('investigation_complete keyFigures must be a bounded array.');
  }

  if (record.outcome !== 'SUPPORTED_FINDING' && record.outcome !== 'UNABLE_TO_CONCLUDE') {
    return invalidTerminalArtifact('investigation_complete outcome is invalid.');
  }
  if (!Array.isArray(record.evidenceRefs) || record.evidenceRefs.length > MAX_FINDING_EVIDENCE_REFS) {
    return invalidTerminalArtifact('investigation_complete evidenceRefs must be a bounded array.');
  }

  const evidenceRefs = Object.freeze(record.evidenceRefs.map(parseEvidenceRef));
  if (record.outcome === 'SUPPORTED_FINDING' && evidenceRefs.length === 0) {
    return invalidTerminalArtifact('SUPPORTED_FINDING requires at least one evidence reference.');
  }
  const limitations = stringList(
    record.limitations,
    'investigation_complete limitations',
    MAX_FINDING_LIST_ITEMS,
    MAX_FINDING_LIST_ITEM_LENGTH,
  );
  const recommendedNext = stringList(
    record.recommendedNext,
    'investigation_complete recommendedNext',
    MAX_FINDING_LIST_ITEMS,
    MAX_FINDING_LIST_ITEM_LENGTH,
  );
  for (const narrative of [...limitations, ...recommendedNext]) {
    assertNoPhysicalExecutionClaim(narrative);
  }

  return Object.freeze({
    outcome: record.outcome,
    ...(Array.isArray(record.keyFigures) ? { keyFigures: Object.freeze(record.keyFigures.map(parseKeyFigure)) } : {}),
    evidenceRefs,
    limitations,
    recommendedNext,
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
  const record = asRecord(value, 'investigation_request_input response');
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
  return invalidTerminalArtifact('investigation_request_input response kind is invalid.');
};

export const parseInvestigationRequestInput = (value: unknown): InvestigationRequestInput => {
  const record = asRecord(value, 'investigation_request_input input');
  assertExactKeys(record, ['prompt', 'response']);
  return Object.freeze({
    prompt: nonEmptyString(record.prompt, 'investigation_request_input prompt'),
    response: parseInputResponse(record.response),
  });
};
