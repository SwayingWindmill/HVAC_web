import type { RecommendationRisk } from './optimization-recommendation-model.ts';

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
  if (reason.includes('do not vary enough')) return '历史读数变化太小，无法拟合回归系数';
  if (reason.includes('at or below 0.5')) return '历史读数下算出的 COP 低于 0.5，不满足物理范围';
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
          displayName: nameByDevice.get(chiller.deviceId) ?? '未命名冷机',
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
