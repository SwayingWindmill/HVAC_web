// The Optimization owner's latest recommendation for a Site: a chilled-water supply
// temperature weighed against yesterday's plant energy and cost and the zone's comfort band,
// with the frozen constraints, the expected impact and how to verify and roll it back.

export type RecommendationRisk = 'LOW' | 'MEDIUM' | 'HIGH';

export interface OptimizationRecommendation {
  readonly runId: string;
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
}

interface PublishedConstraint {
  readonly kind: string;
  readonly minC?: number;
  readonly maxC?: number;
  readonly maxStepC?: number;
}

export interface PublishedRecommendation {
  readonly runId: string;
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
