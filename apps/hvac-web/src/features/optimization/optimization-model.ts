import { formatDecimal } from '../../lib/operator-format.ts';
import { formatSigned } from './optimization-recommendation-model.ts';

export {
  OPTIMIZATION_RECOMMENDATION_SOURCE_PREFIX,
  TRIGGER_LABEL,
  auxiliaryLoopAssetType,
  buildOptimizationWorkOrderDraft,
  buildReadinessSummary,
  changesSupply,
  findActiveRecommendationWorkOrder,
  formatAuxiliaryCapabilityLabel,
  formatAuxiliaryLoopLabel,
  formatAuxiliaryRationale,
  formatAuxiliaryTargetValue,
  formatCalibrationDate,
  formatCelsius,
  formatSigned,
  isOptimizationRecommendationSource,
  isUnscheduledReady,
  matchAuxiliaryControlTarget,
  optimizationRecommendationSourceRef,
  parseRecommendation,
  preparationProblem,
  preparationReadinessReasons,
  recommendedAuxiliaryValue,
  recommendedSetpoint,
} from './optimization-recommendation-model.ts';
export type {
  OptimizationAuxiliaryTarget,
  OptimizationRecommendation,
  OptimizationWorkOrderDraft,
  PreparationOutcome,
  PreparationStatus,
  PublishedRecommendation,
  ReadinessItem,
  ReadinessKey,
  ReadinessStatus,
  ReadinessSummary,
  RecommendationRisk,
  RecommendationTrigger,
  ResponseCoefficientStatus,
  TenantStrategyStatus,
} from './optimization-recommendation-model.ts';
export { parseShadowEvaluation } from './optimization-shadow-model.ts';
export type {
  OptimizationShadowEvaluation,
  PublishedShadowChillerFit,
  PublishedShadowEvaluation,
  ShadowCandidateRow,
  ShadowChillerDiagnostic,
  ShadowOutcome,
} from './optimization-shadow-model.ts';

export type GovernanceSolverSource = 'DEFAULT' | 'DEPLOYMENT_BINDING' | 'SITE_LOOP_CANARY';

export interface PublishedGovernanceCopCell {
  readonly wetBulbMinC: number;
  readonly wetBulbMaxC: number;
  readonly coolingLoadMinKW: number;
  readonly coolingLoadMaxKW: number;
  readonly baselineSamples: number;
  readonly aiSamples: number;
  readonly baselineCOP: number;
  readonly aiCOP: number;
  readonly copUpliftPct: number;
  readonly estimatedSavedKWh: number;
}

export interface PublishedOptimizationGovernanceSummary {
  readonly tenantId: string;
  readonly siteId: string;
  readonly windowDays: number;
  readonly generatedAt: string;
  readonly activeSolver: {
    readonly servingRef: string;
    readonly source: GovernanceSolverSource;
    readonly loopKey: string;
    readonly deploymentRevisionId: string | null;
  };
  readonly tickCounts: {
    readonly aiClosedLoop: number;
    readonly aiShadow: number;
    readonly ruleBaseline: number;
    readonly manualOverride: number;
  };
  readonly copComparison: {
    readonly matchedBinCount: number;
    readonly matchedAiSamples: number;
    readonly matchedBaselineSamples: number;
    readonly weightedBaselineCOP: number | null;
    readonly weightedAiCOP: number | null;
    readonly weightedCopUpliftPct: number | null;
    readonly totalEstimatedSavedKWh: number;
    readonly cells: readonly PublishedGovernanceCopCell[];
  };
  readonly flywheel: {
    readonly totalSamples: number;
    readonly guardrailClamped: number;
    readonly guardrailVetoed: number;
    readonly manualOverride: number;
    readonly monitorTripped: number;
    readonly trackingDeviation: number;
  };
}

export interface GovernanceCopCellRow {
  readonly key: string;
  readonly wetBulbMinC: number;
  readonly wetBulbMaxC: number;
  readonly coolingLoadMinKW: number;
  readonly coolingLoadMaxKW: number;
  readonly wetBulbRangeLabel: string;
  readonly coolingLoadRangeLabel: string;
  readonly baselineSamples: number;
  readonly aiSamples: number;
  readonly samplesPairLabel: string;
  readonly baselineCOP: number;
  readonly baselineCOPLabel: string;
  readonly aiCOP: number;
  readonly aiCOPLabel: string;
  readonly copUpliftPct: number;
  readonly copUpliftLabel: string;
  readonly estimatedSavedKWh: number;
  readonly estimatedSavedLabel: string;
}

export interface GovernanceFlywheelItem {
  readonly key:
    | 'guardrailClamped'
    | 'guardrailVetoed'
    | 'manualOverride'
    | 'trackingDeviation'
    | 'monitorTripped';
  readonly label: string;
  readonly count: number;
}

export interface OptimizationGovernanceSummary {
  readonly windowDays: number;
  readonly generatedAt: string;
  readonly activeSolver: {
    readonly solverLabel: string;
    readonly source: GovernanceSolverSource;
    readonly sourceLabel: string;
    readonly loopLabel: string;
    readonly hasDeploymentBinding: boolean;
  };
  readonly tickCounts: {
    readonly aiClosedLoop: number;
    readonly aiShadow: number;
    readonly ruleBaseline: number;
    readonly manualOverride: number;
    readonly totalTicks: number;
  };
  readonly copComparison: {
    readonly matchedBinCount: number;
    readonly matchedAiSamples: number;
    readonly matchedBaselineSamples: number;
    readonly weightedBaselineCOP: number | null;
    readonly weightedBaselineCOPLabel: string;
    readonly weightedAiCOP: number | null;
    readonly weightedAiCOPLabel: string;
    readonly weightedCopUpliftPct: number | null;
    readonly weightedCopUpliftLabel: string;
    readonly totalEstimatedSavedKWh: number;
    readonly totalEstimatedSavedLabel: string;
    readonly cells: readonly GovernanceCopCellRow[];
  };
  readonly flywheel: {
    readonly totalSamples: number;
    readonly items: readonly GovernanceFlywheelItem[];
  };
}

const SOLVER_REF_LABELS: Readonly<Record<string, string>> = {
  'builtin://hvac-recommendation-v1': '内置稳态优化策略',
  'hvac-recommendation-v1': '内置稳态优化策略',
  'builtin://chiller-cop-model': '冷机 COP 拟合策略',
  'chiller-cop-model': '冷机 COP 拟合策略',
};

const LOOP_KEY_LABELS: Readonly<Record<string, string>> = {
  plant: '全冷站主回路',
  chw_pump: '冷冻泵变频回路',
  cw_pump: '冷却泵变频回路',
  ct_fan: '冷却塔风机回路',
  chilled_water_supply_temp: '冷冻水供水温度回路',
  chilled_water_supply_reset: '冷冻水供水温度回路',
  chilled_water_supply: '冷冻水供水温度回路',
  supply_temp: '冷冻水供水温度回路',
  condenser_water_reset: '冷却水温度回路',
  chiller_staging: '冷机台数控制回路',
  default: '默认控制回路',
};

/** Maps internal solver references (builtin:// or http(s)://) to operator-facing Chinese labels. */
export function formatSolverRef(servingRef: string): string {
  const trimmed = servingRef.trim();
  const normalized = trimmed.toLowerCase();
  const exact = SOLVER_REF_LABELS[normalized];
  if (exact) return exact;
  if (normalized.startsWith('http://') || normalized.startsWith('https://')) {
    if (normalized.includes('ai-solver-service')) {
      return '外部 Python 优化服务';
    }
    return '外部协同优化服务';
  }
  if (normalized.startsWith('builtin://')) {
    return '内置优化策略';
  }
  return '默认优化策略';
}

export function formatSolverSource(source: GovernanceSolverSource): string {
  switch (source) {
    case 'DEFAULT':
      return '默认基线';
    case 'DEPLOYMENT_BINDING':
      return '站点部署绑定';
    case 'SITE_LOOP_CANARY':
      return '回路灰度策略';
    default: {
      const _exhaustive: never = source;
      return _exhaustive;
    }
  }
}

/** Maps internal snake_case loop identifiers to operator Chinese without leaking raw keys. */
export function formatLoopKey(loopKey: string): string {
  const trimmed = loopKey.trim();
  if (!trimmed) return '默认控制回路';
  const normalized = trimmed.toLowerCase();
  const mapped = LOOP_KEY_LABELS[normalized];
  if (mapped) return mapped;
  if (/[\u4e00-\u9fff]/.test(trimmed)) return trimmed;
  return '指定控制回路';
}

export function formatCopUpliftPct(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '—';
  return `${formatSigned(value, 1)}%`;
}

export function formatSavedKWh(value: number): string {
  return `${formatDecimal(value, 1)} kWh`;
}

export function formatWetBulbBin(minC: number, maxC: number): string {
  return `${formatDecimal(minC, 1)} 至 ${formatDecimal(maxC, 1)} °C`;
}

export function formatCoolingLoadBin(minKW: number, maxKW: number): string {
  return `${formatDecimal(minKW, 0)} 至 ${formatDecimal(maxKW, 0)} kW`;
}

export function parseOptimizationGovernanceSummary(
  published: PublishedOptimizationGovernanceSummary,
): OptimizationGovernanceSummary {
  const { activeSolver, tickCounts, copComparison, flywheel } = published;
  const totalTicks =
    tickCounts.aiClosedLoop
    + tickCounts.aiShadow
    + tickCounts.ruleBaseline
    + tickCounts.manualOverride;

  const cells: GovernanceCopCellRow[] = copComparison.cells.map((cell) => ({
    key: `${cell.wetBulbMinC}-${cell.wetBulbMaxC}-${cell.coolingLoadMinKW}-${cell.coolingLoadMaxKW}`,
    wetBulbMinC: cell.wetBulbMinC,
    wetBulbMaxC: cell.wetBulbMaxC,
    coolingLoadMinKW: cell.coolingLoadMinKW,
    coolingLoadMaxKW: cell.coolingLoadMaxKW,
    wetBulbRangeLabel: formatWetBulbBin(cell.wetBulbMinC, cell.wetBulbMaxC),
    coolingLoadRangeLabel: formatCoolingLoadBin(cell.coolingLoadMinKW, cell.coolingLoadMaxKW),
    baselineSamples: cell.baselineSamples,
    aiSamples: cell.aiSamples,
    samplesPairLabel: `${cell.aiSamples} / ${cell.baselineSamples}`,
    baselineCOP: cell.baselineCOP,
    baselineCOPLabel: formatDecimal(cell.baselineCOP, 2),
    aiCOP: cell.aiCOP,
    aiCOPLabel: formatDecimal(cell.aiCOP, 2),
    copUpliftPct: cell.copUpliftPct,
    copUpliftLabel: formatCopUpliftPct(cell.copUpliftPct),
    estimatedSavedKWh: cell.estimatedSavedKWh,
    estimatedSavedLabel: formatSavedKWh(cell.estimatedSavedKWh),
  }));

  return {
    windowDays: published.windowDays,
    generatedAt: published.generatedAt,
    activeSolver: {
      solverLabel: formatSolverRef(activeSolver.servingRef),
      source: activeSolver.source,
      sourceLabel: formatSolverSource(activeSolver.source),
      loopLabel: formatLoopKey(activeSolver.loopKey),
      hasDeploymentBinding: Boolean(activeSolver.deploymentRevisionId?.trim()),
    },
    tickCounts: {
      ...tickCounts,
      totalTicks,
    },
    copComparison: {
      matchedBinCount: copComparison.matchedBinCount,
      matchedAiSamples: copComparison.matchedAiSamples,
      matchedBaselineSamples: copComparison.matchedBaselineSamples,
      weightedBaselineCOP: copComparison.weightedBaselineCOP,
      weightedBaselineCOPLabel:
        copComparison.weightedBaselineCOP !== null ? formatDecimal(copComparison.weightedBaselineCOP, 2) : '—',
      weightedAiCOP: copComparison.weightedAiCOP,
      weightedAiCOPLabel:
        copComparison.weightedAiCOP !== null ? formatDecimal(copComparison.weightedAiCOP, 2) : '—',
      weightedCopUpliftPct: copComparison.weightedCopUpliftPct,
      weightedCopUpliftLabel: formatCopUpliftPct(copComparison.weightedCopUpliftPct),
      totalEstimatedSavedKWh: copComparison.totalEstimatedSavedKWh,
      totalEstimatedSavedLabel:
        copComparison.matchedBinCount > 0 ? formatSavedKWh(copComparison.totalEstimatedSavedKWh) : '—',
      cells,
    },
    flywheel: {
      totalSamples: flywheel.totalSamples,
      items: [
        { key: 'guardrailClamped', label: '护栏限幅修正', count: flywheel.guardrailClamped },
        { key: 'guardrailVetoed', label: '物理安全拦截', count: flywheel.guardrailVetoed },
        { key: 'manualOverride', label: '人工接管覆写', count: flywheel.manualOverride },
        { key: 'trackingDeviation', label: '跟踪偏差回退', count: flywheel.trackingDeviation },
        { key: 'monitorTripped', label: '监控越界回退', count: flywheel.monitorTripped },
      ],
    },
  };
}

export type NegativeSampleKind =
  | 'GUARDRAIL_CLAMPED'
  | 'GUARDRAIL_VETOED'
  | 'MANUAL_OVERRIDE'
  | 'MONITOR_TRIPPED'
  | 'TRACKING_DEVIATION';

export type NegativeSampleFilterKind = 'ALL' | NegativeSampleKind;

export const NEGATIVE_SAMPLE_KIND_LABELS: Readonly<Record<NegativeSampleKind, string>> = {
  GUARDRAIL_CLAMPED: '护栏限幅修正',
  GUARDRAIL_VETOED: '物理安全拦截',
  MANUAL_OVERRIDE: '人工接管覆写',
  MONITOR_TRIPPED: '监控越界回退',
  TRACKING_DEVIATION: '跟踪偏差回退',
};

const GUARDRAIL_STAGE_LABELS: Readonly<Record<string, string>> = {
  validity: '输入合法性检查',
  freshness: '测点新鲜度检查',
  fault_and_local_veto: '设备故障与就地检修否决',
  manual_backoff: '人工退避保护',
  interlock_and_dwell: '顺序联锁与启停间隔',
  hard_clamp: '物理上下限钳位',
  slew_rate: '单步调幅限速',
  deadband: '死区静默过滤',
};

const GUARDRAIL_ACTION_LABELS: Readonly<Record<string, string>> = {
  pass: '通过',
  clamped: '已钳位',
  vetoed: '已拦截',
  skipped: '死区跳过',
};

const NEGATIVE_SAMPLE_REASON_LABELS: Readonly<Record<string, string>> = {
  step_rate_limit: '单步变化超限',
  slew_rate_limit: '单步调幅限速',
  min_clamp: '低于最小物理下限',
  max_clamp: '高于最大物理上限',
  hard_clamp: '超出安全上下限',
  manual_backoff: '人工接管退避期内',
  device_fault: '设备故障保护拦截',
  local_manual: '控制柜就地手动锁定',
  tracking_deviation: '设定值跟踪偏差超限',
  zone_temp_high: '室温超出上限回退',
  telemetry_unhealthy: '实时测点健康异常回退',
  stale_telemetry: '采集数据超时失真拦截',
};

export function formatNegativeSampleKind(kind: NegativeSampleKind): string {
  return NEGATIVE_SAMPLE_KIND_LABELS[kind];
}

export function formatGuardrailStage(stage: string): string {
  const normalized = stage.trim().toLowerCase();
  return GUARDRAIL_STAGE_LABELS[normalized] ?? '安全护栏规则';
}

export function formatGuardrailAction(action: string): string {
  const normalized = action.trim().toLowerCase();
  return GUARDRAIL_ACTION_LABELS[normalized] ?? '已处理';
}

export function formatNegativeSampleReason(reasonCode: string): string {
  const trimmed = reasonCode.trim();
  if (!trimmed) return '安全规则触发';
  const normalized = trimmed.toLowerCase();
  const mapped = NEGATIVE_SAMPLE_REASON_LABELS[normalized];
  if (mapped) return mapped;
  if (/[\u4e00-\u9fff]/.test(trimmed)) return trimmed;
  return '安全护栏规则触发';
}

export function serializeNegativeSamplesJsonl(items: readonly unknown[]): string {
  if (items.length === 0) return '';
  return `${items.map((item) => JSON.stringify(item)).join('\n')}\n`;
}
