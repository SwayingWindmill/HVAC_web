import type {
  RuntimeCheckpoint,
  RuntimeReadPlan,
} from '../../application/index.js';
import type { OperationsInvestigationView } from '../../domain/index.js';

export type LangGraphRuntimeErrorCode =
  | 'PROGRAM_INVALID'
  | 'ACTIVE_RUN_MISMATCH'
  | 'RUNTIME_REVISION_MISMATCH'
  | 'CHECKPOINT_IDENTITY_MISMATCH'
  | 'CHECKPOINT_STATE_INVALID'
  | 'CHECKPOINT_POSITION_MISMATCH';

export class LangGraphRuntimeError extends Error {
  readonly code: LangGraphRuntimeErrorCode;

  constructor(code: LangGraphRuntimeErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'LangGraphRuntimeError';
    this.code = code;
  }
}

export interface LangGraphReadStep {
  readonly id: string;
  readonly plan: RuntimeReadPlan;
}

export interface LangGraphRuntimeProgram {
  readonly id: string;
  readonly runtimeRevision: string;
  readonly steps: readonly LangGraphReadStep[];
}

export interface RuntimeExecutionStateV1 {
  readonly schemaVersion: 1;
  readonly programId: string;
  readonly investigationId: string;
  readonly runId: string;
  readonly runtimeRevision: string;
  readonly nextStepIndex: number;
  readonly completedStepIds: readonly string[];
}

const exactStateKeys = new Set([
  'schemaVersion',
  'programId',
  'investigationId',
  'runId',
  'runtimeRevision',
  'nextStepIndex',
  'completedStepIds',
]);

const maximumProgramSteps = 64;
const maximumIdentityCharacters = 256;
const maximumReadPlanCharacters = 65_536;
const maximumCheckpointStateCharacters = 32_768;

const requireIdentity = (value: string, label: string): string => {
  if (value.trim().length === 0 || value.length > maximumIdentityCharacters) {
    throw new LangGraphRuntimeError(
      'PROGRAM_INVALID',
      `${label} must contain 1 to ${maximumIdentityCharacters} characters.`,
    );
  }
  return value;
};

const clonePlan = (plan: RuntimeReadPlan): RuntimeReadPlan => {
  try {
    const encoded = JSON.stringify(plan);
    if (typeof encoded !== 'string' || encoded.length > maximumReadPlanCharacters) {
      throw new Error(`Runtime READ Plan exceeds ${maximumReadPlanCharacters} characters.`);
    }
    return JSON.parse(encoded) as RuntimeReadPlan;
  } catch (cause) {
    throw new LangGraphRuntimeError(
      'PROGRAM_INVALID',
      'Runtime READ Plan must be bounded JSON data.',
      { cause },
    );
  }
};

export const copyPlan = clonePlan;

export const normalizeProgram = (
  input: LangGraphRuntimeProgram,
): LangGraphRuntimeProgram => {
  const id = requireIdentity(input.id, 'Runtime program identity');
  const runtimeRevision = requireIdentity(input.runtimeRevision, 'Runtime Revision');
  if (!Array.isArray(input.steps)
    || input.steps.length === 0
    || input.steps.length > maximumProgramSteps) {
    throw new LangGraphRuntimeError(
      'PROGRAM_INVALID',
      `LangGraph Runtime program must contain 1 to ${maximumProgramSteps} READ Steps.`,
    );
  }
  const identities = new Set<string>();
  const steps = input.steps.map((step) => {
    const stepId = requireIdentity(step.id, 'Runtime Step Identity');
    if (identities.has(stepId)) {
      throw new LangGraphRuntimeError(
        'PROGRAM_INVALID',
        `Runtime Step Identity ${stepId} is duplicated.`,
      );
    }
    identities.add(stepId);
    if (!Array.isArray(step.plan?.batches) || step.plan.batches.length === 0) {
      throw new LangGraphRuntimeError(
        'PROGRAM_INVALID',
        `Runtime Step ${stepId} must contain at least one READ batch.`,
      );
    }
    return Object.freeze({ id: stepId, plan: clonePlan(step.plan) });
  });
  return Object.freeze({ id, runtimeRevision, steps: Object.freeze(steps) });
};

export const positionFor = (
  state: RuntimeExecutionStateV1,
  program: LangGraphRuntimeProgram,
): string => {
  const step = program.steps[state.nextStepIndex];
  return step === undefined ? 'complete' : `before:${step.id}`;
};

export const initialExecution = (
  investigation: OperationsInvestigationView,
  runId: string,
  program: LangGraphRuntimeProgram,
): RuntimeExecutionStateV1 => {
  return {
    schemaVersion: 1,
    programId: program.id,
    investigationId: investigation.id,
    runId,
    runtimeRevision: program.runtimeRevision,
    nextStepIndex: 0,
    completedStepIds: [],
  };
};

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const decodeState = (
  checkpoint: RuntimeCheckpoint,
  program: LangGraphRuntimeProgram,
): RuntimeExecutionStateV1 => {
  if (checkpoint.opaqueState.length > maximumCheckpointStateCharacters) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_STATE_INVALID',
      `Runtime Checkpoint state exceeds ${maximumCheckpointStateCharacters} characters.`,
    );
  }
  let value: unknown;
  try {
    value = JSON.parse(checkpoint.opaqueState) as unknown;
  } catch (cause) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_STATE_INVALID',
      'Runtime Checkpoint opaque state is not valid JSON.',
      { cause },
    );
  }
  if (!isRecord(value)
    || Object.keys(value).length !== exactStateKeys.size
    || Object.keys(value).some((key) => !exactStateKeys.has(key))
    || value.schemaVersion !== 1
    || typeof value.programId !== 'string'
    || value.programId.length === 0
    || value.programId.length > maximumIdentityCharacters
    || typeof value.investigationId !== 'string'
    || value.investigationId.length === 0
    || value.investigationId.length > maximumIdentityCharacters
    || typeof value.runId !== 'string'
    || value.runId.length === 0
    || value.runId.length > maximumIdentityCharacters
    || typeof value.runtimeRevision !== 'string'
    || value.runtimeRevision.length === 0
    || value.runtimeRevision.length > maximumIdentityCharacters
    || typeof value.nextStepIndex !== 'number'
    || !Number.isSafeInteger(value.nextStepIndex)
    || value.nextStepIndex < 0
    || value.nextStepIndex > program.steps.length
    || !Array.isArray(value.completedStepIds)
    || value.completedStepIds.some((item) => (
      typeof item !== 'string'
      || item.length === 0
      || item.length > maximumIdentityCharacters
    ))) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_STATE_INVALID',
      'Runtime Checkpoint opaque state is outside runtime-state/v1.',
    );
  }
  const completedStepIds = value.completedStepIds as string[];
  const expected = program.steps.slice(0, value.nextStepIndex).map((step) => step.id);
  if (value.programId !== program.id
    || value.runtimeRevision !== program.runtimeRevision
    || completedStepIds.length !== expected.length
    || completedStepIds.some((stepId, index) => stepId !== expected[index])) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_STATE_INVALID',
      'Runtime Checkpoint state does not match the configured Runtime program.',
    );
  }
  return {
    schemaVersion: 1,
    programId: value.programId,
    investigationId: value.investigationId,
    runId: value.runId,
    runtimeRevision: value.runtimeRevision,
    nextStepIndex: value.nextStepIndex,
    completedStepIds: [...completedStepIds],
  };
};

export const assertActiveRun = (
  investigation: OperationsInvestigationView,
  runId: string,
  program: LangGraphRuntimeProgram,
): void => {
  const run = investigation.runs.find((candidate) => candidate.id === runId);
  if (investigation.activeRunId !== runId || run === undefined || run.status !== 'ACTIVE') {
    throw new LangGraphRuntimeError(
      'ACTIVE_RUN_MISMATCH',
      `Agent Run ${runId} is not the active writable Run for Investigation ${investigation.id}.`,
    );
  }
  if (run.runtimeRevision !== program.runtimeRevision) {
    throw new LangGraphRuntimeError(
      'RUNTIME_REVISION_MISMATCH',
      `Agent Run ${runId} is bound to a different Runtime Revision.`,
    );
  }
};

export const restoreExecution = (
  investigation: OperationsInvestigationView,
  runId: string,
  checkpoint: RuntimeCheckpoint | null,
  program: LangGraphRuntimeProgram,
): RuntimeExecutionStateV1 => {
  if (checkpoint === null) return initialExecution(investigation, runId, program);
  if (checkpoint.investigationId !== investigation.id
    || checkpoint.runId !== runId
    || checkpoint.runtimeRevision !== program.runtimeRevision) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_IDENTITY_MISMATCH',
      'Runtime Checkpoint identity does not match the active Investigation, Agent Run, and Runtime Revision.',
    );
  }
  const state = decodeState(checkpoint, program);
  if (state.investigationId !== investigation.id || state.runId !== runId) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_IDENTITY_MISMATCH',
      'Runtime Checkpoint state identity does not match the active Investigation and Agent Run.',
    );
  }
  const expectedPosition = positionFor(state, program);
  if (checkpoint.position !== expectedPosition) {
    throw new LangGraphRuntimeError(
      'CHECKPOINT_POSITION_MISMATCH',
      `Runtime Checkpoint position ${checkpoint.position} does not match ${expectedPosition}.`,
    );
  }
  return {
    ...state,
    completedStepIds: [...state.completedStepIds],
  };
};