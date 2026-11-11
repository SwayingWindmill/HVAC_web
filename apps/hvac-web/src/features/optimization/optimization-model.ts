// The Optimization owner's latest recommendation for a Site: a chilled-water supply
// temperature weighed against yesterday's plant energy and cost and the zone's comfort band,
// with the frozen constraints, the expected impact and how to verify and roll it back.

export type RecommendationRisk = 'LOW' | 'MEDIUM' | 'HIGH';

/** Why a run was made: an operator asked, or the Site's hourly schedule did. */
export type RecommendationTrigger = 'MANUAL' | 'SCHEDULE';

export const TRIGGER_LABEL: Record<RecommendationTrigger, string> = { MANUAL: '手动生成', SCHEDULE: '定时生成' };

export interface OptimizationRecommendation {
  readonly id: string;
  readonly runId: string;
  readonly trigger: RecommendationTrigger;
  readonly createdAt: string;
  readonly baseline: { readonly supplyTempC: number; readonly zoneTempC: number; readonly dailyEnergyKWh: number; readonly dailyCost: number };
  readonly candidate: { readonly supplyTempC: number; readonly projectedZoneTempC: number; readonly dailyEnergyKWh: number; readonly dailyCost: number };
  readonly saving: { readonly energyKWhPerDay: number; readonly costPerDay: number };
  readonly supplyRange: { readonly minC: number; readonly maxC: number; readonly maxStepC: number };
  readonly comfortRange: { readonly minC: number; readonly maxC: number };
  readonly uncertainty: { readonly energyP90KWh: number; readonly zoneTempP90C: number };
  readonly risk: RecommendationRisk;
  readonly comfortMarginC: number;
  readonly verificationMinutes: number;
  readonly restoreSupplyTempC: number;
  /** How the policy says the plant responds to the supply temperature; set by hand, not fitted from the Site. */
  readonly response: { readonly energyKWhPerSupplyC: number; readonly zoneCPerSupplyC: number };
}

interface PublishedConstraint {
  readonly kind: string;
  readonly minC?: number;
  readonly maxC?: number;
  readonly maxStepC?: number;
}

export interface PublishedRecommendation {
  readonly runId: string;
  readonly trigger: RecommendationTrigger;
  readonly responseModel: { readonly dailyEnergyDeltaKWhPerSupplyTempC: number; readonly zoneTempDeltaCPerSupplyTempC: number };
  readonly recommendation: {
    readonly id?: string;
    readonly createdAt: string;
    readonly baseline: OptimizationRecommendation['baseline'];
    readonly candidate: OptimizationRecommendation['candidate'];
    readonly expectedImpact: { readonly energySavingKWhPerDay: number; readonly costSavingPerDay: number };
    readonly constraints: readonly PublishedConstraint[];
    readonly uncertainty: { readonly energyP90KWh: number; readonly zoneTempP90C: number };
    readonly risk: { readonly level: RecommendationRisk; readonly comfortMarginC: number };
    readonly rollbackPlan?: { readonly restoreSupplyTempC?: number };
    readonly verificationPlan: { readonly windowMinutes: number };
  };
}

function constraint(constraints: readonly PublishedConstraint[], kind: string): PublishedConstraint {
  const found = constraints.find((candidate) => candidate.kind === kind);
  if (!found) throw new Error(`optimization recommendation has no ${kind} constraint`);
  return found;
}

export function parseRecommendation(published: PublishedRecommendation): OptimizationRecommendation {
  const { recommendation } = published;
  const supply = constraint(recommendation.constraints, 'SUPPLY_TEMP');
  const comfort = constraint(recommendation.constraints, 'ZONE_COMFORT');
  return {
    id: recommendation.id ?? published.runId,
    runId: published.runId,
    trigger: published.trigger,
    createdAt: recommendation.createdAt,
    baseline: recommendation.baseline,
    candidate: recommendation.candidate,
    saving: { energyKWhPerDay: recommendation.expectedImpact.energySavingKWhPerDay, costPerDay: recommendation.expectedImpact.costSavingPerDay },
    supplyRange: { minC: supply.minC!, maxC: supply.maxC!, maxStepC: supply.maxStepC! },
    comfortRange: { minC: comfort.minC!, maxC: comfort.minC !== undefined ? comfort.maxC! : 0 },
    uncertainty: recommendation.uncertainty,
    risk: recommendation.risk.level,
    comfortMarginC: recommendation.risk.comfortMarginC,
    verificationMinutes: recommendation.verificationPlan.windowMinutes,
    restoreSupplyTempC: recommendation.rollbackPlan?.restoreSupplyTempC ?? recommendation.baseline.supplyTempC,
    response: {
      energyKWhPerSupplyC: published.responseModel.dailyEnergyDeltaKWhPerSupplyTempC,
      zoneCPerSupplyC: published.responseModel.zoneTempDeltaCPerSupplyTempC,
    },
  };
}

/** Whether the recommendation asks for a different supply temperature than the plant runs now. */
export function changesSupply(recommendation: OptimizationRecommendation): boolean {
  return Math.abs(recommendation.candidate.supplyTempC - recommendation.baseline.supplyTempC) >= 0.05;
}

/**
 * The chiller setpoint that carries out the recommendation: the recommended supply temperature
 * on the setpoint's own step, never past the setting's range or the policy's safe supply band.
 */
export function recommendedSetpoint(
  recommendation: OptimizationRecommendation,
  setting: { readonly minimum: number; readonly maximum: number; readonly step: number },
): number {
  const low = Math.max(setting.minimum, recommendation.supplyRange.minC);
  const high = Math.min(setting.maximum, recommendation.supplyRange.maxC);
  const stepped = setting.minimum + Math.round((recommendation.candidate.supplyTempC - setting.minimum) / setting.step) * setting.step;
  const inside = Math.min(high, Math.max(low, stepped));
  return Number(inside.toFixed(6));
}

/**
 * How the Site's hourly schedule last fired: prepared a run, still preparing, or failed, with the
 * input it found missing.
 */
export interface PreparationOutcome {
  readonly status: 'PREPARED' | 'FAILED' | 'PENDING';
  readonly reason: string | null;
  readonly firedAt: string;
}

const PREPARATION_REASON: Record<string, string> = {
  SITE_NOT_VISIBLE: '优化服务读不到这个站点',
  POLICY_MISSING: '站点没有唯一一份可用的优化策略',
  POLICY_UNSUPPORTED: '站点的优化策略内容不受支持，需要修改策略',
  MAINTENANCE_ACTIVE: '有设备在检修或被手动锁定',
  TOPOLOGY_MISSING: '站点没有已发布的系统拓扑',
  LOAD_FORECAST_MISSING: '还没有最新的负荷预测',
  TARIFF_MISSING: '站点没有生效的电价',
  MODEL_DEPLOYMENT_MISSING: '站点没有启用优化模型',
  PRICED_DAY_MISSING: '最近一天的用电与电费还没有结算',
  SUPPLY_TEMPERATURE_MISSING: '读不到可用的实时冷冻水供水温度',
  ZONE_TEMPERATURE_MISSING: '读不到可用的实时室内温度',
  BASELINE_OUTSIDE_LIMITS: '当前供水温度或室内温度已在策略的安全或舒适范围之外',
  INPUTS_INVALID: '输入数据的单位或数值不对',
  INPUTS_UNREADABLE: '读取输入数据时出错，下次定时计算会再试',
};

/** Why the schedule's last firing made no recommendation, in the operator's words; null when it did not fail. */
export function preparationProblem(outcome: PreparationOutcome | null): string | null {
  if (outcome?.status !== 'FAILED') return null;
  return (outcome.reason && PREPARATION_REASON[outcome.reason]) || '计算没有完成，原因没有记录';
}

export type ReadinessKey =
  | 'supply_temp'
  | 'zone_comfort'
  | 'maintenance_locks'
  | 'live_revalidation'
  | 'forecast_and_tariff'
  | 'execution_channel';

export type ReadinessStatus = 'ready' | 'manual' | 'blocked' | 'unknown';

export interface ReadinessItem {
  readonly key: ReadinessKey;
  readonly label: string;
  readonly status: ReadinessStatus;
  readonly detail: string;
}

export interface ReadinessSummary {
  readonly readyCount: number;
  readonly totalCount: 6;
  readonly headline: string;
  readonly items: readonly ReadinessItem[];
}

const FORECAST_AND_TARIFF_REASONS = new Set([
  'SITE_NOT_VISIBLE',
  'POLICY_MISSING',
  'POLICY_UNSUPPORTED',
  'TOPOLOGY_MISSING',
  'LOAD_FORECAST_MISSING',
  'TARIFF_MISSING',
  'MODEL_DEPLOYMENT_MISSING',
  'PRICED_DAY_MISSING',
]);

const LIVE_READING_REASONS = new Set([
  'SUPPLY_TEMPERATURE_MISSING',
  'ZONE_TEMPERATURE_MISSING',
  'INPUTS_INVALID',
  'INPUTS_UNREADABLE',
]);

function fixed1(value: number): string {
  return value.toFixed(1);
}

/**
 * Summarizes the 6 real backend readiness & safety checks without fabricating unmeasured conditions.
 */
export function buildReadinessSummary({
  recommendation,
  preparation,
  hasWritableSetpoint,
}: {
  readonly recommendation: OptimizationRecommendation | null;
  readonly preparation: PreparationOutcome | null;
  readonly hasWritableSetpoint: boolean;
}): ReadinessSummary {
  const activeFailure = preparation?.status === 'FAILED'
    && (recommendation === null || Date.parse(preparation.firedAt) > Date.parse(recommendation.createdAt))
    ? preparation
    : null;
  const activeReason = activeFailure?.reason ?? null;
  const problem = activeFailure ? preparationProblem(activeFailure) : null;
  const preparedPass = preparation?.status === 'PREPARED' || (recommendation !== null && activeFailure === null);

  const supplyItem: ReadinessItem = activeReason === 'SUPPLY_TEMPERATURE_MISSING' || activeReason === 'BASELINE_OUTSIDE_LIMITS'
    ? { key: 'supply_temp', label: '供水温度范围', status: 'blocked', detail: problem! }
    : recommendation
      ? {
          key: 'supply_temp',
          label: '供水温度范围',
          status: 'ready',
          detail: `安全范围 ${fixed1(recommendation.supplyRange.minC)}–${fixed1(recommendation.supplyRange.maxC)} °C，单次调整不超过 ${fixed1(recommendation.supplyRange.maxStepC)} °C`,
        }
      : { key: 'supply_temp', label: '供水温度范围', status: 'unknown', detail: '生成优化建议后按策略安全范围核验' };

  const comfortItem: ReadinessItem = activeReason === 'ZONE_TEMPERATURE_MISSING' || activeReason === 'BASELINE_OUTSIDE_LIMITS'
    ? { key: 'zone_comfort', label: '室内舒适度范围', status: 'blocked', detail: problem! }
    : recommendation
      ? {
          key: 'zone_comfort',
          label: '室内舒适度范围',
          status: 'ready',
          detail: `舒适范围 ${recommendation.comfortRange.minC.toFixed(0)}–${recommendation.comfortRange.maxC.toFixed(0)} °C，预计余量 ${fixed1(recommendation.comfortMarginC)} °C`,
        }
      : { key: 'zone_comfort', label: '室内舒适度范围', status: 'unknown', detail: '生成优化建议后按室内舒适范围核验' };

  const maintenanceItem: ReadinessItem = activeReason === 'MAINTENANCE_ACTIVE'
    ? { key: 'maintenance_locks', label: '检修与手动锁定', status: 'blocked', detail: problem! }
    : preparedPass
      ? { key: 'maintenance_locks', label: '检修与手动锁定', status: 'ready', detail: '无停运检修资产与手动锁定资源' }
      : { key: 'maintenance_locks', label: '检修与手动锁定', status: 'unknown', detail: '待运行准备核验检修与手动锁定状态' };

  const liveItem: ReadinessItem = activeReason !== null && LIVE_READING_REASONS.has(activeReason)
    ? { key: 'live_revalidation', label: '实时读数核验', status: 'blocked', detail: problem! }
    : preparedPass
      ? { key: 'live_revalidation', label: '实时读数核验', status: 'ready', detail: '供水与室内温度在线新鲜；下发控制指令时再次实时复核' }
      : { key: 'live_revalidation', label: '实时读数核验', status: 'unknown', detail: '待核验供水与室内温度实时读数' };

  const forecastItem: ReadinessItem = activeReason !== null && FORECAST_AND_TARIFF_REASONS.has(activeReason)
    ? { key: 'forecast_and_tariff', label: '负荷预测与分时电价', status: 'blocked', detail: problem! }
    : preparedPass
      ? { key: 'forecast_and_tariff', label: '负荷预测与分时电价', status: 'ready', detail: '未来整日负荷预测、生效电价与最近已结算日数据齐全' }
      : { key: 'forecast_and_tariff', label: '负荷预测与分时电价', status: 'unknown', detail: '待核验负荷预测、分时电价与已结算日用电' };

  const channelLabel = hasWritableSetpoint ? '可远程下发设定点' : '仅人工调温工单';
  const channelItem: ReadinessItem = hasWritableSetpoint
    ? { key: 'execution_channel', label: '执行通道状态', status: 'ready', detail: '已登记启用可写冷冻水供水温度设定点，可远程下发控制指令' }
    : { key: 'execution_channel', label: '执行通道状态', status: 'manual', detail: '未登记可写冷冻水供水温度设定点，走人工调温工单' };

  const items: readonly ReadinessItem[] = [
    supplyItem,
    comfortItem,
    maintenanceItem,
    liveItem,
    forecastItem,
    channelItem,
  ];
  const readyCount = items.filter((item) => item.status === 'ready').length;
  const headline = problem
    ? `前置与安全条件：${problem} · ${channelLabel}`
    : recommendation === null && !preparedPass
      ? `前置与安全条件：待核验 · ${channelLabel}`
      : `前置与安全条件：${readyCount}/6 就绪 · ${channelLabel}`;

  return { readyCount, totalCount: 6, headline, items };
}

export const OPTIMIZATION_RECOMMENDATION_SOURCE_PREFIX = 'optimization-recommendation:';

const UNCLOSED_WORK_ORDER_STATUSES = new Set(['DRAFT', 'OPEN', 'IN_PROGRESS', 'BLOCKED']);

export function optimizationRecommendationSourceRef(recommendationId: string): string {
  return `${OPTIMIZATION_RECOMMENDATION_SOURCE_PREFIX}${recommendationId}`;
}

export interface OptimizationWorkOrderDraft {
  readonly initial: {
    readonly title: string;
    readonly priority: 'MEDIUM';
    readonly description: string;
  };
  readonly sourceReferences: {
    readonly domain: 'EXTERNAL';
    readonly resourceId: string;
    readonly relationship: 'ORIGIN';
  }[];
}

export function buildOptimizationWorkOrderDraft(recommendation: OptimizationRecommendation): OptimizationWorkOrderDraft {
  const { baseline, candidate, saving, comfortRange } = recommendation;
  const title = `人工调整冷冻水供水温度至 ${fixed1(candidate.supplyTempC)} °C`;
  const description = [
    `建议将冷冻水供水温度从 ${fixed1(baseline.supplyTempC)} °C 调整至 ${fixed1(candidate.supplyTempC)} °C。`,
    `预计日节电 ${Math.round(saving.energyKWhPerDay)} kWh，预计日节省估算电费 ¥${Math.round(saving.costPerDay)}（按当天峰谷均价折算）。`,
    `预计室内温度 ${fixed1(candidate.projectedZoneTempC)} °C，保持在 ${comfortRange.minC.toFixed(0)}–${comfortRange.maxC.toFixed(0)} °C 舒适范围内（舒适余量 ${fixed1(recommendation.comfortMarginC)} °C）。`,
    `现场调整后观察 ${recommendation.verificationMinutes} 分钟；任一安全或舒适条件不满足时，恢复至 ${fixed1(recommendation.restoreSupplyTempC)} °C。`,
  ].join('\n');

  return {
    initial: { title, priority: 'MEDIUM', description },
    sourceReferences: [
      {
        domain: 'EXTERNAL',
        resourceId: optimizationRecommendationSourceRef(recommendation.id),
        relationship: 'ORIGIN',
      },
    ],
  };
}

export function findActiveRecommendationWorkOrder<
  T extends {
    readonly status: string;
    readonly createdAt: string;
    readonly sourceReferences: readonly { readonly domain: string; readonly resourceId: string }[];
  },
>(workOrders: readonly T[], recommendationId: string): T | null {
  const expectedRef = optimizationRecommendationSourceRef(recommendationId);
  const matching = workOrders
    .filter(
      (order) =>
        UNCLOSED_WORK_ORDER_STATUSES.has(order.status)
        && order.sourceReferences.some(
          (source) => source.domain === 'EXTERNAL' && source.resourceId === expectedRef,
        ),
    )
    .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt));

  return matching[0] ?? null;
}

export type ShadowOutcome =
  | 'FEASIBLE'
  | 'INFEASIBLE'
  | 'NO_USABLE_MODEL'
  | 'HISTORY_UNAVAILABLE'
  | 'INVALID_REQUEST';

export interface PublishedShadowChillerFit {
  readonly deviceId: string;
  readonly status: 'FITTED' | 'INSUFFICIENT_DATA' | 'REJECTED';
  readonly reason?: string;
  readonly samples: number;
  readonly capacityReference?: { readonly kW: number; readonly source: string };
  readonly coefficients?: {
    readonly intercept: number;
    readonly loadRatio: number;
    readonly loadRatioSquared: number;
    readonly chilledWaterSupplyTempC: number;
    readonly condenserReturnTempC?: number;
  };
  readonly cvRmsePower?: number;
  readonly cvRmseCOP?: number;
  readonly meanCOP?: number;
  readonly chilledWaterSupplyRangeC?: readonly [number, number] | readonly number[];
}

export interface PublishedShadowEvaluation {
  readonly evaluationId: string;
  readonly runId: string;
  readonly publishedRecommendationId: string;
  readonly generatedAt: string;
  readonly solver: string;
  readonly mode: string;
  readonly outcome: ShadowOutcome;
  readonly reason?: string;
  readonly cvRmse: number | null;
  readonly condenserReset?: { readonly status?: string; readonly reason?: string };
  readonly energyResponse?: {
    readonly source?: string;
    readonly dailyEnergyDeltaKWhPerSupplyTempC: number;
    readonly referenceChillerEnergyKWh: number;
    readonly referenceSamples: number;
    readonly referenceHours: number;
    readonly replacesPolicyValue: number;
  };
  readonly zoneResponse?: {
    readonly source?: string;
    readonly zoneTempDeltaCPerSupplyTempC: number;
    readonly zoneTempUncertaintyP90C: number;
  };
  readonly fit?: {
    readonly history: {
      readonly from: string;
      readonly to: string;
      readonly bucketMinutes: number;
      readonly chillers: number;
      readonly runningBuckets: number;
      readonly setAside: Readonly<Record<string, number>>;
      readonly loadAllocation?: Readonly<Record<string, number>>;
    };
    readonly chillers: readonly PublishedShadowChillerFit[];
    readonly cvRmsePower: number | null;
  };
  readonly candidates?: readonly {
    readonly supplyTempC: number;
    readonly projectedZoneTempC: number;
    readonly chillerEnergyDeltaKWh: number;
    readonly dailyEnergyKWh: number;
    readonly dailyCost: number;
    readonly extrapolated: boolean;
    readonly feasible: boolean;
    readonly reason?: string;
  }[];
  readonly summary?: {
    readonly supplyTempC: number;
    readonly projectedZoneTempC: number;
    readonly dailyEnergyKWh: number;
    readonly dailyCost: number;
    readonly energySavingKWhPerDay: number;
    readonly costSavingPerDay: number;
    readonly energyP90KWh: number;
    readonly zoneTempP90C: number;
    readonly comfortMarginC: number;
    readonly riskLevel: RecommendationRisk;
    readonly extrapolated: boolean;
    readonly savingWithinEnergyUncertainty: boolean;
  };
}

export interface ShadowCandidateRow {
  readonly supplyTempC: number;
  readonly projectedZoneTempC: number;
  readonly chillerEnergyDeltaKWh: number;
  readonly dailyEnergyKWh: number;
  readonly dailyCost: number;
  readonly extrapolated: boolean;
  readonly feasible: boolean;
  readonly infeasibleLabel: string | null;
  readonly selected: boolean;
}

export interface ShadowChillerDiagnostic {
  readonly deviceId: string;
  readonly displayName: string;
  readonly status: PublishedShadowChillerFit['status'];
  readonly statusLabel: string;
  readonly reasonLabel: string | null;
  readonly samples: number;
  readonly capacityKW: number | null;
  readonly coefficients: PublishedShadowChillerFit['coefficients'] | null;
  readonly cvRmsePower: number | null;
  readonly meanCOP: number | null;
  readonly supplyRangeC: readonly [number, number] | null;
  readonly supplyTempSlopeCOP: number | null;
  readonly condenserReturnSlopeCOP: number | null;
}

export interface OptimizationShadowEvaluation {
  readonly evaluationId: string;
  readonly runId: string;
  readonly publishedRecommendationId: string;
  readonly generatedAt: string;
  readonly outcome: ShadowOutcome;
  readonly outcomeLabel: string;
  readonly outcomeDetail: string | null;
  readonly cvRmse: number | null;
  readonly condenserResetNote: string;
  readonly energyResponse: {
    readonly fittedKWhPerSupplyC: number;
    readonly policyKWhPerSupplyC: number;
    readonly referenceChillerEnergyKWh: number;
    readonly referenceSamples: number;
    readonly referenceHours: number;
  } | null;
  readonly candidates: readonly ShadowCandidateRow[];
  readonly summary: NonNullable<PublishedShadowEvaluation['summary']> | null;
  readonly fit: {
    readonly from: string;
    readonly to: string;
    readonly bucketMinutes: number;
    readonly runningBuckets: number;
    readonly setAsideTotal: number;
    readonly setAsideBreakdown: readonly { readonly key: string; readonly label: string; readonly count: number }[];
    readonly loadAllocationBreakdown: readonly { readonly key: string; readonly label: string; readonly count: number }[];
    readonly chillers: readonly ShadowChillerDiagnostic[];
    readonly cvRmsePower: number | null;
  } | null;
}

const SHADOW_OUTCOME_LABEL: Record<ShadowOutcome, string> = {
  FEASIBLE: '已产出影子对照结果',
  INFEASIBLE: '约束内无可行候选步长',
  NO_USABLE_MODEL: '暂无可用冷机拟合模型',
  HISTORY_UNAVAILABLE: '历史运行数据暂不可读',
  INVALID_REQUEST: '运行输入校验未通过',
};

const SET_ASIDE_LABEL: Record<string, string> = {
  noCoolingLoad: '无有效冷负荷',
  implausibleReading: '水温超出物理范围',
  startingUp: '启机过渡段',
  implausibleCOP: '瞬时 COP 异常',
};

const LOAD_ALLOCATION_LABEL: Record<string, string> = {
  single: '单机运行',
  evaporatorDeltaT: '按冷冻水温差分摊',
  power: '按功率分摊',
};

const CANDIDATE_INFEASIBLE_LABEL: Record<string, string> = {
  SUPPLY_TEMP: '超出供水安全范围',
  ZONE_COMFORT: '超出室内舒适范围',
};

const CHILLER_STATUS_LABEL: Record<PublishedShadowChillerFit['status'], string> = {
  FITTED: '已拟合',
  INSUFFICIENT_DATA: '稳态样本不足',
  REJECTED: '校验未通过',
};

function translateChillerReason(status: PublishedShadowChillerFit['status'], reason?: string): string | null {
  if (status === 'FITTED') return null;
  if (!reason) return status === 'INSUFFICIENT_DATA' ? '近 14 天稳态 15 分钟样本少于 96 个' : '回归模型未通过物理与误差校验';
  if (reason.includes('fewer than')) return '近 14 天稳态 15 分钟样本少于 96 个（约 24 小时运行数据）';
  if (reason.includes('do not vary enough')) return '运行工况变化不足，无法拟合回归系数';
  if (reason.includes('at or below 0.5')) return '观测工况下预测 COP 低于 0.5，不满足物理范围';
  if (reason.includes('warmer chilled-water supply lowers COP')) return '拟合结果违背物理规律（冷冻水供水升温反而降低 COP）';
  if (reason.includes('warmer condenser return raises COP')) return '拟合结果违背物理规律（冷却水回水升温反而升高 COP）';
  if (reason.includes('exceeds')) return '功率拟合误差 CV(RMSE) 超过 30% 上限';
  return reason;
}

function translateShadowOutcomeDetail(outcome: ShadowOutcome, reason?: string): string | null {
  if (outcome === 'FEASIBLE') return null;
  if (outcome === 'NO_USABLE_MODEL') return '近 14 天运行样本不足或回归未通过物理/统计校验，以下保留每台冷机被筛除的具体原因。';
  if (outcome === 'INFEASIBLE') return '所有候选供水温度步长均超出供水安全范围或室内舒适范围。';
  if (outcome === 'HISTORY_UNAVAILABLE') return '未能读取近 14 天冷站历史运行桶数据，本次仅保留已发布的策略求解结果。';
  return reason ?? '影子评估未完成校验。';
}

export function parseShadowEvaluation(
  published: PublishedShadowEvaluation,
  nameByDevice: ReadonlyMap<string, string> = new Map(),
): OptimizationShadowEvaluation {
  const summary = published.summary ?? null;
  const condenserResetNote = published.condenserReset?.reason === 'NO_CONDENSER_SETPOINT'
    ? '站点未登记冷却水设定点，暂未开放冷却水温度重置'
    : '冷却水温度重置暂未开放';

  const candidates: ShadowCandidateRow[] = (published.candidates ?? []).map((candidate) => ({
    supplyTempC: candidate.supplyTempC,
    projectedZoneTempC: candidate.projectedZoneTempC,
    chillerEnergyDeltaKWh: candidate.chillerEnergyDeltaKWh,
    dailyEnergyKWh: candidate.dailyEnergyKWh,
    dailyCost: candidate.dailyCost,
    extrapolated: candidate.extrapolated,
    feasible: candidate.feasible,
    infeasibleLabel: candidate.feasible
      ? null
      : (candidate.reason && CANDIDATE_INFEASIBLE_LABEL[candidate.reason]) || '超出安全或舒适约束',
    selected: summary !== null && candidate.feasible && Math.abs(candidate.supplyTempC - summary.supplyTempC) < 1e-6,
  }));

  const fit = published.fit
    ? (() => {
        const setAsideEntries = Object.entries(published.fit.history.setAside ?? {}).map(([key, count]) => ({
          key,
          label: SET_ASIDE_LABEL[key] ?? key,
          count,
        }));
        const loadAllocationEntries = Object.entries(published.fit.history.loadAllocation ?? {}).map(([key, count]) => ({
          key,
          label: LOAD_ALLOCATION_LABEL[key] ?? key,
          count,
        }));
        const setAsideTotal = setAsideEntries.reduce((sum, entry) => sum + entry.count, 0);
        const chillers: ShadowChillerDiagnostic[] = published.fit.chillers.map((chiller) => ({
          deviceId: chiller.deviceId,
          displayName: nameByDevice.get(chiller.deviceId) ?? chiller.deviceId,
          status: chiller.status,
          statusLabel: CHILLER_STATUS_LABEL[chiller.status],
          reasonLabel: translateChillerReason(chiller.status, chiller.reason),
          samples: chiller.samples,
          capacityKW: chiller.capacityReference?.kW ?? null,
          coefficients: chiller.coefficients ?? null,
          cvRmsePower: chiller.cvRmsePower ?? null,
          meanCOP: chiller.meanCOP ?? null,
          supplyRangeC:
            chiller.chilledWaterSupplyRangeC && chiller.chilledWaterSupplyRangeC.length >= 2
              ? [chiller.chilledWaterSupplyRangeC[0], chiller.chilledWaterSupplyRangeC[1]]
              : null,
          supplyTempSlopeCOP: chiller.coefficients?.chilledWaterSupplyTempC ?? null,
          condenserReturnSlopeCOP: chiller.coefficients?.condenserReturnTempC ?? null,
        }));
        return {
          from: published.fit.history.from,
          to: published.fit.history.to,
          bucketMinutes: published.fit.history.bucketMinutes,
          runningBuckets: published.fit.history.runningBuckets,
          setAsideTotal,
          setAsideBreakdown: setAsideEntries,
          loadAllocationBreakdown: loadAllocationEntries,
          chillers,
          cvRmsePower: published.fit.cvRmsePower,
        };
      })()
    : null;

  return {
    evaluationId: published.evaluationId,
    runId: published.runId,
    publishedRecommendationId: published.publishedRecommendationId,
    generatedAt: published.generatedAt,
    outcome: published.outcome,
    outcomeLabel: SHADOW_OUTCOME_LABEL[published.outcome] ?? published.outcome,
    outcomeDetail: translateShadowOutcomeDetail(published.outcome, published.reason),
    cvRmse: published.cvRmse,
    condenserResetNote,
    energyResponse: published.energyResponse
      ? {
          fittedKWhPerSupplyC: published.energyResponse.dailyEnergyDeltaKWhPerSupplyTempC,
          policyKWhPerSupplyC: published.energyResponse.replacesPolicyValue,
          referenceChillerEnergyKWh: published.energyResponse.referenceChillerEnergyKWh,
          referenceSamples: published.energyResponse.referenceSamples,
          referenceHours: published.energyResponse.referenceHours,
        }
      : null,
    candidates,
    summary,
    fit,
  };
}
