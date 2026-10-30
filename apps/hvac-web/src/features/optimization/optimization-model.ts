// The Optimization owner's latest recommendation for a Site: a chilled-water supply
// temperature weighed against yesterday's plant energy and cost and the zone's comfort band,
// with the frozen constraints, the expected impact and how to verify and roll it back.

export type RecommendationRisk = 'LOW' | 'MEDIUM' | 'HIGH';

/** Why a run was made: an operator asked, or the Site's hourly schedule did. */
export type RecommendationTrigger = 'MANUAL' | 'SCHEDULE';

export const TRIGGER_LABEL: Record<RecommendationTrigger, string> = { MANUAL: '手动生成', SCHEDULE: '定时生成' };

export interface OptimizationRecommendation {
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
    readonly createdAt: string;
    readonly baseline: OptimizationRecommendation['baseline'];
    readonly candidate: OptimizationRecommendation['candidate'];
    readonly expectedImpact: { readonly energySavingKWhPerDay: number; readonly costSavingPerDay: number };
    readonly constraints: readonly PublishedConstraint[];
    readonly uncertainty: { readonly energyP90KWh: number; readonly zoneTempP90C: number };
    readonly risk: { readonly level: RecommendationRisk; readonly comfortMarginC: number };
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
    runId: published.runId,
    trigger: published.trigger,
    createdAt: recommendation.createdAt,
    baseline: recommendation.baseline,
    candidate: recommendation.candidate,
    saving: { energyKWhPerDay: recommendation.expectedImpact.energySavingKWhPerDay, costPerDay: recommendation.expectedImpact.costSavingPerDay },
    supplyRange: { minC: supply.minC!, maxC: supply.maxC!, maxStepC: supply.maxStepC! },
    comfortRange: { minC: comfort.minC!, maxC: comfort.maxC! },
    uncertainty: recommendation.uncertainty,
    risk: recommendation.risk.level,
    comfortMarginC: recommendation.risk.comfortMarginC,
    verificationMinutes: recommendation.verificationPlan.windowMinutes,
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
