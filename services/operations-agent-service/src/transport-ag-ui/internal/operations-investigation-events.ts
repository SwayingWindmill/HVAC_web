import type { SiteNightEnergyInvestigationView } from '../../application/index.js';
import type { ToolExecutionReceiptRecord } from '../../domain/index.js';

export type OperationsPlanStepStatus =
  | 'PENDING'
  | 'IN_PROGRESS'
  | 'PAUSED'
  | 'COMPLETED'
  | 'FAILED'
  | 'CANCELLED';

export interface OperationsPlanStepView {
  readonly id: 'READ_SITE_CONTEXT' | 'READ_ENERGY_SERIES' | 'ANALYZE' | 'COMMIT_RESULT';
  readonly label: string;
  readonly status: OperationsPlanStepStatus;
}

export interface OperationsPlanView {
  readonly schemaVersion: 1;
  readonly id: 'site-night-energy-investigation';
  readonly label: 'Site night-energy investigation';
  readonly completedSteps: number;
  readonly totalSteps: 4;
  readonly progressPercent: number;
  readonly steps: readonly OperationsPlanStepView[];
}

export interface OperationsToolActivityView {
  readonly recordId: string;
  readonly logicalTool: ToolExecutionReceiptRecord['logicalTool'];
  readonly owner: ToolExecutionReceiptRecord['owner'];
  readonly resultCategory: ToolExecutionReceiptRecord['resultCategory'];
  readonly startedAt: number;
  readonly completedAt: number;
}

export interface OperationsInvestigationStateSnapshot {
  readonly schemaVersion: 'operations-investigation-ui/v1';
  readonly investigation: Omit<SiteNightEnergyInvestigationView, 'toolReceipts'>;
  readonly plan: OperationsPlanView;
  readonly toolActivities: readonly OperationsToolActivityView[];
}

export type OperationsAgUiEvent =
  | {
    readonly type: 'RUN_STARTED';
    readonly threadId: string;
    readonly runId: string;
  }
  | {
    readonly type: 'STATE_SNAPSHOT';
    readonly snapshot: OperationsInvestigationStateSnapshot;
  }
  | {
    readonly type: 'TOOL_CALL_START';
    readonly toolCallId: string;
    readonly toolCallName: ToolExecutionReceiptRecord['logicalTool'];
  }
  | {
    readonly type: 'TOOL_CALL_ARGS';
    readonly toolCallId: string;
    readonly delta: string;
  }
  | {
    readonly type: 'TOOL_CALL_END';
    readonly toolCallId: string;
  }
  | {
    readonly type: 'RUN_FINISHED';
    readonly threadId: string;
    readonly runId: string;
    readonly outcome: { readonly type: 'success' };
  };

export type OperationsStreamRecoveryMode = 'FULL_SNAPSHOT' | 'RESUME';

export type OperationsStreamRecoveryReason =
  | 'INITIAL'
  | 'VALID'
  | 'UNKNOWN'
  | 'EXPIRED'
  | 'CONFLICT';

export interface OperationsAgUiEventFrame {
  readonly id: string;
  readonly event: OperationsAgUiEvent;
}

export interface OperationsAgUiEventBatch {
  readonly frames: readonly OperationsAgUiEventFrame[];
  readonly recovery: {
    readonly mode: OperationsStreamRecoveryMode;
    readonly reason: OperationsStreamRecoveryReason;
    readonly snapshotPosition: string;
    readonly latestPosition: string;
    readonly replayFromPosition: string | null;
  };
}

const cloneJson = <Value>(value: Value): Value => JSON.parse(JSON.stringify(value)) as Value;

const sortedReceipts = (
  receipts: readonly ToolExecutionReceiptRecord[],
): ToolExecutionReceiptRecord[] => [...receipts].sort((left, right) => (
  left.recordedAt - right.recordedAt || left.id.localeCompare(right.id)
));

const completedPlanFlags = (
  view: SiteNightEnergyInvestigationView,
  receipts: readonly ToolExecutionReceiptRecord[],
): readonly boolean[] => {
  const succeeded = receipts.filter((receipt) => receipt.resultCategory === 'SUCCEEDED');
  const siteContextReady = succeeded.some((receipt) => receipt.logicalTool === 'registry.getSite')
    && succeeded.some((receipt) => receipt.logicalTool === 'registry.listSiteEquipment');
  const energyReads = succeeded.filter((receipt) => receipt.logicalTool === 'analytics.getEnergySeries').length;
  const analysisReady = view.analysisReferences.length > 0;
  const resultCommitted = view.findings.length > 0;
  return [siteContextReady, energyReads >= 2, analysisReady, resultCommitted];
};

const statusForIncompleteStep = (
  view: SiteNightEnergyInvestigationView,
  firstIncomplete: boolean,
): OperationsPlanStepStatus => {
  if (!firstIncomplete) return 'PENDING';
  if (view.status === 'FAILED') return 'FAILED';
  if (view.status === 'CANCELLED') return 'CANCELLED';
  if (view.status === 'PAUSED' || view.activeRun?.status === 'PAUSED') return 'PAUSED';
  if (view.status === 'RUNNING') return 'IN_PROGRESS';
  return 'PENDING';
};

const projectPlan = (
  view: SiteNightEnergyInvestigationView,
  receipts: readonly ToolExecutionReceiptRecord[],
): OperationsPlanView => {
  const completed = completedPlanFlags(view, receipts);
  const definitions = [
    ['READ_SITE_CONTEXT', 'Read authoritative Site context'],
    ['READ_ENERGY_SERIES', 'Read authoritative night-energy periods'],
    ['ANALYZE', 'Run deterministic night-energy analysis'],
    ['COMMIT_RESULT', 'Commit Evidence, Analysis and Finding'],
  ] as const;
  const firstIncompleteIndex = completed.findIndex((value) => !value);
  const steps = definitions.map(([id, label], index): OperationsPlanStepView => Object.freeze({
    id,
    label,
    status: completed[index]
      ? 'COMPLETED'
      : statusForIncompleteStep(view, index === firstIncompleteIndex),
  }));
  const completedSteps = completed.filter(Boolean).length;
  return Object.freeze({
    schemaVersion: 1,
    id: 'site-night-energy-investigation',
    label: 'Site night-energy investigation',
    completedSteps,
    totalSteps: 4,
    progressPercent: completedSteps * 25,
    steps: Object.freeze(steps),
  });
};

const projectToolActivity = (
  receipt: ToolExecutionReceiptRecord,
): OperationsToolActivityView => Object.freeze({
  recordId: receipt.id,
  logicalTool: receipt.logicalTool,
  owner: receipt.owner,
  resultCategory: receipt.resultCategory,
  startedAt: receipt.startedAt,
  completedAt: receipt.completedAt,
});

const snapshotRunId = (view: SiteNightEnergyInvestigationView): string => (
  `${view.id}:projection:${view.revision}`
);

const position = (revision: number, sequence: number): string => `${revision}:${sequence}`;

const parseRecoveryPosition = (
  value: string | null | undefined,
): { readonly revision: number; readonly sequence: number; readonly value: string } | null => {
  const normalized = value?.trim() ?? '';
  const match = /^(0|[1-9]\d*):(0|[1-9]\d*)$/u.exec(normalized);
  if (match === null) return null;
  const revision = Number(match[1]);
  const sequence = Number(match[2]);
  if (!Number.isSafeInteger(revision) || !Number.isSafeInteger(sequence)) return null;
  return Object.freeze({ revision, sequence, value: normalized });
};

const isReplayBoundary = (sequence: number, latestSequence: number): boolean => (
  sequence === 0
  || sequence === 1
  || sequence === latestSequence
  || (sequence >= 4 && sequence < latestSequence && (sequence - 4) % 3 === 0)
);

export const projectOperationsInvestigationToAgUiEvents = (
  view: SiteNightEnergyInvestigationView,
): readonly OperationsAgUiEvent[] => {
  const receipts = sortedReceipts(view.toolReceipts);
  const runId = snapshotRunId(view);
  const toolActivities = Object.freeze(receipts.map(projectToolActivity));
  const investigation = cloneJson({
    schemaVersion: view.schemaVersion,
    id: view.id,
    scope: view.scope,
    status: view.status,
    revision: view.revision,
    createdAt: view.createdAt,
    activeRun: view.activeRun,
    outcome: view.outcome,
    evidence: view.evidence,
    analysisReferences: view.analysisReferences,
    findings: view.findings,
  });
  const events: OperationsAgUiEvent[] = [{
    type: 'RUN_STARTED',
    threadId: view.id,
    runId,
  }, {
    type: 'STATE_SNAPSHOT',
    snapshot: Object.freeze({
      schemaVersion: 'operations-investigation-ui/v1',
      investigation: Object.freeze(investigation),
      plan: projectPlan(view, receipts),
      toolActivities,
    }),
  }];
  for (const activity of toolActivities) {
    events.push({
      type: 'TOOL_CALL_START',
      toolCallId: activity.recordId,
      toolCallName: activity.logicalTool,
    }, {
      type: 'TOOL_CALL_ARGS',
      toolCallId: activity.recordId,
      delta: JSON.stringify(activity),
    }, {
      type: 'TOOL_CALL_END',
      toolCallId: activity.recordId,
    });
  }
  events.push({
    type: 'RUN_FINISHED',
    threadId: view.id,
    runId,
    outcome: Object.freeze({ type: 'success' }),
  });
  return Object.freeze(events);
};

const canonicalFrames = (
  view: SiteNightEnergyInvestigationView,
): readonly OperationsAgUiEventFrame[] => Object.freeze(
  projectOperationsInvestigationToAgUiEvents(view).map((event, sequence) => Object.freeze({
    id: position(view.revision, sequence),
    event,
  })),
);

export const projectOperationsInvestigationToAgUiEventBatch = (
  view: SiteNightEnergyInvestigationView,
  requestedPosition?: string | null,
): OperationsAgUiEventBatch => {
  const allFrames = canonicalFrames(view);
  const latestSequence = allFrames.length - 1;
  const parsed = parseRecoveryPosition(requestedPosition);
  let mode: OperationsStreamRecoveryMode = 'FULL_SNAPSHOT';
  let reason: OperationsStreamRecoveryReason = 'INITIAL';
  let replayFromPosition: string | null = null;

  if ((requestedPosition?.trim() ?? '') !== '') {
    if (parsed === null) {
      reason = 'UNKNOWN';
    } else if (parsed.revision < view.revision) {
      reason = 'EXPIRED';
    } else if (parsed.revision > view.revision
      || parsed.sequence > latestSequence
      || !isReplayBoundary(parsed.sequence, latestSequence)) {
      reason = 'CONFLICT';
    } else {
      mode = 'RESUME';
      reason = 'VALID';
      replayFromPosition = parsed.value;
    }
  }

  const frames = mode === 'FULL_SNAPSHOT' || parsed === null
    ? allFrames
    : Object.freeze([
        allFrames[0],
        allFrames[1],
        ...allFrames.slice(2, -1).filter((frame) => {
          const sequence = Number(frame.id.slice(frame.id.indexOf(':') + 1));
          return sequence > parsed.sequence;
        }),
        allFrames.at(-1),
      ].filter((frame): frame is OperationsAgUiEventFrame => frame !== undefined));

  return Object.freeze({
    frames,
    recovery: Object.freeze({
      mode,
      reason,
      snapshotPosition: position(view.revision, 1),
      latestPosition: position(view.revision, latestSequence),
      replayFromPosition,
    }),
  });
};

export const encodeOperationsAgUiEventStream = (
  view: SiteNightEnergyInvestigationView,
  requestedPosition?: string | null,
): string => projectOperationsInvestigationToAgUiEventBatch(view, requestedPosition).frames
  .map((frame) => [
    `id: ${frame.id}`,
    `event: ${frame.event.type}`,
    `data: ${JSON.stringify(frame.event)}`,
    '',
    '',
  ].join('\n'))
  .join('');

export const createOperationsAgUiEventStreamResponse = (
  view: SiteNightEnergyInvestigationView,
  requestedPosition?: string | null,
): Response => {
  const batch = projectOperationsInvestigationToAgUiEventBatch(view, requestedPosition);
  const headers = new Headers({
    'Cache-Control': 'no-store, no-transform',
    'Content-Type': 'text/event-stream; charset=utf-8',
    'X-Accel-Buffering': 'no',
    'X-Operations-Recovery-Mode': batch.recovery.mode,
    'X-Operations-Recovery-Reason': batch.recovery.reason,
    'X-Operations-Snapshot-Position': batch.recovery.snapshotPosition,
    'X-Operations-Latest-Position': batch.recovery.latestPosition,
  });
  if (batch.recovery.replayFromPosition !== null) {
    headers.set('X-Operations-Replay-From', batch.recovery.replayFromPosition);
  }
  return new Response(encodeOperationsAgUiEventStream(view, requestedPosition), {
    status: 200,
    headers,
  });
};
