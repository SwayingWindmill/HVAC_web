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
