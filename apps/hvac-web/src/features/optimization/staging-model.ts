import type { StagingDiagnostic, StagingEvaluation, StagingRecommendationDraft } from '../../api/generated/platformGateway.gen';

// Stopping a chiller the remaining ones can cover, or a pump or tower still running for a stopped
// chiller. The optimization service suggests; a person confirms each one as a high-risk command.
// The service's own sentences carry internal names, so the page builds its copy from the numbers
// and from the reason codes below.

export type StagingKind = StagingRecommendationDraft['kind'];

export const KIND_LABEL: Readonly<Record<StagingKind, string>> = {
  CHILLER_STAGE_DOWN: '减台',
  IDLE_AUXILIARY_STOP: '辅机空转',
};

export interface StagingCard {
  readonly id: string;
  readonly kind: StagingKind;
  readonly assetId: string;
  readonly assetName: string;
  readonly pointId: string | null;
  readonly savingKW: number;
  readonly shiftKWh: number;
  readonly shiftHours: number;
  /** Cooling load against what the chillers left running can deliver. */
  readonly capacity: { readonly loadKW: number; readonly remainingKW: number; readonly headroomPercent: number } | null;
  readonly cop: { readonly before: number; readonly after: number } | null;
  readonly idleMinutes: number | null;
  /** The remaining chillers would run beyond the load the last 14 days showed. */
  readonly extrapolated: boolean;
  readonly requiresTotp: boolean;
  readonly requiresSupervisorReview: boolean;
}

export interface PausedItem {
  readonly key: string;
  readonly assetId: string | null;
  readonly name: string;
  readonly why: string;
  /** The blocker is a lock or the Site's control mode, which the control page changes. */
  readonly controlFix: boolean;
}

export interface StagingView {
  readonly cards: readonly StagingCard[];
  readonly paused: readonly PausedItem[];
  /** Why no chiller can be stopped now, when that is simply the plant's state. */
  readonly stageDownIdle: string | null;
  readonly totals: { readonly savingKW: number; readonly shiftKWh: number; readonly shiftHours: number; readonly powerBeforeKW: number | null } | null;
}

const SCOPE_NAME: Readonly<Record<string, string>> = { CHILLER_STAGE_DOWN: '减台', IDLE_AUXILIARY_STOP: '辅机停机' };

/** Normal plant states the service reports while it looks, not blockers. */
const QUIET = new Set([
  'CHILLER_STAGE_DOWN_ELIGIBLE', 'IDLE_AUXILIARY_ELIGIBLE', 'PAIRED_CHILLER_RUNNING', 'PAIRED_CHILLER_STARTING',
  'AUXILIARY_ALREADY_STOPPED', 'AUXILIARY_STARTING', 'IDLE_DEBOUNCE_NOT_MET', 'UNSUPPORTED_AUXILIARY_TYPE',
]);

const EXACT: Readonly<Record<string, string>> = {
  SITE_CONTROL_EXIT_ACTIVE: '站点不在远程自动控制，停机建议已暂停',
  ASSET_CONTROL_LOCKED: '已锁定，解锁后才会给停机建议',
  INSUFFICIENT_CHILLERS: '登记的冷机不到 2 台，不需要减台',
  COOLING_LOAD_MISSING: '读不到冷站制冷量，为安全起见不给减台建议',
  COOLING_LOAD_NON_POSITIVE: '现在没有供冷负荷',
  PLANT_CHILLER_STARTING: '有冷机正在开机，稳定后再算',
  ZERO_CHILLERS_RUNNING: '现在没有冷机在运行',
  SINGLE_CHILLER_RUNNING: '只有 1 台冷机在运行，不能再减',
  NO_CANDIDATE_IMPROVED: '停哪一台都不会更省电',
  COP_NOT_IMPROVED: '停哪一台都不会更省电',
  HEADROOM_INSUFFICIENT: '停一台后剩下的制冷能力不够',
  CHILLER_CAPACITY_UNKNOWN: '缺冷机额定冷量，算不出停机后还剩多少能力',
  CHILLER_COP_MODEL_UNAVAILABLE: '冷机能效模型还不可用，算不出停机后的用电',
  MODEL_PREDICTION_IMPLAUSIBLE: '冷机能效模型算出的结果不可信，这次不给建议',
  CHW_SUPPLY_TEMP_MISSING: '缺冷冻水出水温度读数',
  CONDENSER_TEMPERATURE_MISSING: '缺冷却水出水温度读数',
  SAVING_BELOW_THRESHOLD: '省电太少，不值得为此启停',
  NO_SERVED_CHILLERS_MAPPED: '没登记它服务哪台冷机，判断不了是否空转',
  PAIRED_CHILLER_STATE_MISSING: '读不到对应主机的运行状态',
  PAIRED_CHILLER_DRAWING_POWER: '对应主机电表还有功率，确认不了已停机',
  IDLE_DURATION_UNCONFIRMED: '空转时长的读数不全，确认不了',
};

const PREFIX: readonly (readonly [string, string])[] = [
  ['COOLING_LOAD_', '冷站制冷量读数不可信，暂停减台评估'],
  ['CHILLER_METER_', '冷机电表读数不可用，不给减台建议'],
  ['CHILLER_STATE_', '读不到冷机运行状态，算不准开机台数'],
  ['IDLE_HISTORY_', '空转时长的读数不全，确认不了'],
  ['AUXILIARY_STATE_', '读不到这台设备的运行状态'],
  ['STAGING_', '这次没算出来，下次评估会重试'],
];

/** The plain sentence for a diagnostic's reason code. */
export function stagingReason(reason: string): string {
  return EXACT[reason] ?? PREFIX.find(([prefix]) => reason.startsWith(prefix))?.[1] ?? '这次没有给出建议';
}

const isControlFix = (reason: string) => reason === 'SITE_CONTROL_EXIT_ACTIVE' || reason === 'ASSET_CONTROL_LOCKED';

function card(draft: StagingRecommendationDraft, assetName: (assetId: string) => string | undefined): StagingCard {
  const capacity = draft.currentCoolingLoadKW !== undefined && draft.remainingCapacityKW !== undefined && draft.remainingCapacityKW > 0
    ? {
      loadKW: draft.currentCoolingLoadKW,
      remainingKW: draft.remainingCapacityKW,
      headroomPercent: Math.round((draft.headroomMarginFraction ?? 1 - draft.currentCoolingLoadKW / draft.remainingCapacityKW) * 100),
    }
    : null;
  return {
    id: draft.id,
    kind: draft.kind,
    assetId: draft.targetAssetId,
    assetName: assetName(draft.targetAssetId) ?? draft.targetAssetName ?? '未登记设备',
    pointId: draft.suggestedCommandPointId ?? null,
    savingKW: draft.predictedPowerSavingKW,
    shiftKWh: draft.estimatedShiftSavingKWh,
    shiftHours: draft.shiftHours,
    capacity,
    cop: draft.copBefore !== undefined && draft.projectedCopAfter !== undefined ? { before: draft.copBefore, after: draft.projectedCopAfter } : null,
    idleMinutes: draft.continuousIdleMinutes ?? null,
    extrapolated: draft.extrapolated === true,
    requiresTotp: draft.requiresTotp,
    requiresSupervisorReview: draft.requiresSupervisorReview,
  };
}

function pausedItem(diagnostic: StagingDiagnostic, index: number, assetName: (assetId: string) => string | undefined): PausedItem {
  const assetId = diagnostic.assetId ?? null;
  return {
    key: `${diagnostic.scope}:${assetId ?? index}:${diagnostic.reason}`,
    assetId,
    name: (assetId ? assetName(assetId) : undefined) ?? SCOPE_NAME[diagnostic.scope] ?? '停机建议',
    why: stagingReason(diagnostic.reason),
    controlFix: isControlFix(diagnostic.reason),
  };
}

/** What the page shows from the latest evaluation: the cards to confirm, what is paused and why, and the totals. */
export function stagingView(evaluation: StagingEvaluation, assetName: (assetId: string) => string | undefined): StagingView {
  const cards = [...evaluation.recommendations]
    .sort((left, right) => (left.kind === right.kind ? right.predictedPowerSavingKW - left.predictedPowerSavingKW : left.kind === 'CHILLER_STAGE_DOWN' ? -1 : 1))
    .map((draft) => card(draft, assetName));
  const paused = evaluation.diagnostics
    .filter((diagnostic) => diagnostic.status === 'SUPPRESSED' && !QUIET.has(diagnostic.reason))
    .map((diagnostic, index) => pausedItem(diagnostic, index, assetName));
  const stageDownIdle = cards.some((item) => item.kind === 'CHILLER_STAGE_DOWN')
    ? null
    : evaluation.diagnostics.find((diagnostic) => diagnostic.scope === 'CHILLER_STAGE_DOWN' && diagnostic.status === 'NO_OP');
  const powerBefore = evaluation.recommendations.find((draft) => draft.currentTotalPowerKW !== undefined)?.currentTotalPowerKW ?? null;
  const totals = cards.length > 0
    ? {
      savingKW: cards.reduce((sum, item) => sum + item.savingKW, 0),
      shiftKWh: cards.reduce((sum, item) => sum + item.shiftKWh, 0),
      shiftHours: cards[0].shiftHours,
      powerBeforeKW: powerBefore,
    }
    : null;
  return { cards, paused, stageDownIdle: stageDownIdle ? stagingReason(stageDownIdle.reason) : null, totals };
}
