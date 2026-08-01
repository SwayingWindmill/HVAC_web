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

export const encodeOperationsAgUiEventStream = (
  view: SiteNightEnergyInvestigationView,
): string => projectOperationsInvestigationToAgUiEvents(view)
  .map((event, index) => [
    `id: ${view.revision}:${index}`,
    `event: ${event.type}`,
    `data: ${JSON.stringify(event)}`,
    '',
    '',
  ].join('\n'))
  .join('');

export const createOperationsAgUiEventStreamResponse = (
  view: SiteNightEnergyInvestigationView,
): Response => new Response(encodeOperationsAgUiEventStream(view), {
  status: 200,
  headers: {
    'Cache-Control': 'no-store, no-transform',
    'Content-Type': 'text/event-stream; charset=utf-8',
    'X-Accel-Buffering': 'no',
  },
});
