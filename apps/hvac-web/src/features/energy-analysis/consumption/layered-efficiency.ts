import type { Breakdown, EquipmentCategory } from '../../breakdown/breakdown-model.ts';
import type { EnergySummary } from './model.ts';

export const MIN_LAYERED_COOLING_KWH = 1;
export const MIN_CHILLER_COP = 1;
export const MAX_CHILLER_COP = 15;
export const MIN_CHILLER_KWH = 1;
export const MIN_PUMP_KWH = 0.5;
export const MAX_PUMP_WTF = 100;
export const MAX_TOWER_SHARE_TOLERANCE = 1.05;
export const DEFAULT_METER_BALANCE_THRESHOLD = 0.05;

export interface LayeredEfficiency {
  readonly plantCop: number | null;
  readonly chillerCop: number | null;
  readonly chilledPumpWtf: number | null;
  readonly coolingPumpWtf: number | null;
  readonly towerPowerShare: number | null;
}

export type MeterBalanceStatus =
  | 'NO_MAIN_METER'
  | 'BALANCED'
  | 'UNMETERED_GAP'
  | 'SUBMETER_EXCEEDS_MAIN';

export interface MeterBalance {
  readonly status: MeterBalanceStatus;
  readonly mainMeterKWh: number | null;
  readonly subMeteredKWh: number;
  readonly diffKWh: number | null;
  readonly diffRatio: number | null;
  readonly threshold: number;
}

export const METER_BALANCE_LABEL: Readonly<Record<MeterBalanceStatus, string>> = {
  NO_MAIN_METER: '暂无冷站主计量',
  BALANCED: '主配电平衡',
  UNMETERED_GAP: '存在未计量差额',
  SUBMETER_EXCEEDS_MAIN: '设备合计高于主计量',
};

function meteredCategoryEnergy(
  categories: readonly Breakdown['categories'][number][] | undefined,
  category: EquipmentCategory,
): number | null {
  const entry = categories?.find((item) => item.category === category);
  if (!entry || entry.metered <= 0 || !Number.isFinite(entry.energyKWh)) return null;
  return entry.energyKWh;
}

function computePumpWtf(coolingKWh: number | null, pumpKWh: number | null): number | null {
  if (coolingKWh === null || pumpKWh === null || pumpKWh < MIN_PUMP_KWH) return null;
  const ratio = coolingKWh / pumpKWh;
  return Number.isFinite(ratio) && ratio > 0 && ratio <= MAX_PUMP_WTF ? ratio : null;
}

/**
 * Computes plant and subsystem layered efficiency figures from period energy totals and
 * equipment category sub-meter sums, filtering out standby and implausible outliers.
 */
export function computeLayeredEfficiency({
  summary,
  breakdown,
}: {
  readonly summary: Pick<EnergySummary, 'cop' | 'electricityKWh' | 'coolingKWh'> | null | undefined;
  readonly breakdown: Pick<Breakdown, 'totalKWh' | 'categories'> | null | undefined;
}): LayeredEfficiency {
  const plantCop = summary?.cop !== null && summary?.cop !== undefined && Number.isFinite(summary.cop)
    ? summary.cop
    : null;
  const coolingKWh = summary?.coolingKWh !== null
    && summary?.coolingKWh !== undefined
    && Number.isFinite(summary.coolingKWh)
    && summary.coolingKWh >= MIN_LAYERED_COOLING_KWH
    ? summary.coolingKWh
    : null;

  const chillerKWh = meteredCategoryEnergy(breakdown?.categories, 'CHILLER');
  const chilledPumpKWh = meteredCategoryEnergy(breakdown?.categories, 'CHILLED_WATER_PUMP');
  const coolingPumpKWh = meteredCategoryEnergy(breakdown?.categories, 'COOLING_WATER_PUMP');
  const towerKWh = meteredCategoryEnergy(breakdown?.categories, 'COOLING_TOWER');

  let chillerCop: number | null = null;
  if (coolingKWh !== null && chillerKWh !== null && chillerKWh >= MIN_CHILLER_KWH) {
    const ratio = coolingKWh / chillerKWh;
    if (Number.isFinite(ratio) && ratio >= MIN_CHILLER_COP && ratio <= MAX_CHILLER_COP) {
      chillerCop = ratio;
    }
  }

  const chilledPumpWtf = computePumpWtf(coolingKWh, chilledPumpKWh);
  const coolingPumpWtf = computePumpWtf(coolingKWh, coolingPumpKWh);

  const plantElectricity = breakdown?.totalKWh !== null && breakdown?.totalKWh !== undefined
    ? breakdown.totalKWh
    : (summary?.electricityKWh ?? null);

  let towerPowerShare: number | null = null;
  if (
    plantElectricity !== null
    && Number.isFinite(plantElectricity)
    && plantElectricity > 0
    && towerKWh !== null
    && towerKWh >= 0
    && towerKWh <= plantElectricity * MAX_TOWER_SHARE_TOLERANCE
  ) {
    towerPowerShare = Math.min(1, towerKWh / plantElectricity);
  }

  return {
    plantCop,
    chillerCop,
    chilledPumpWtf,
    coolingPumpWtf,
    towerPowerShare,
  };
}

/**
 * Checks main plant meter vs. sum of device sub-meters against the balance threshold (default 5%).
 */
export function computeMeterBalance({
  breakdown,
  threshold = DEFAULT_METER_BALANCE_THRESHOLD,
}: {
  readonly breakdown: Pick<Breakdown, 'totalKWh' | 'subMeteredKWh'> | null | undefined;
  readonly threshold?: number;
}): MeterBalance {
  const subMeteredKWh = breakdown && Number.isFinite(breakdown.subMeteredKWh)
    ? breakdown.subMeteredKWh
    : 0;
  const safeThreshold = Number.isFinite(threshold) && threshold >= 0
    ? threshold
    : DEFAULT_METER_BALANCE_THRESHOLD;

  if (
    !breakdown
    || breakdown.totalKWh === null
    || !Number.isFinite(breakdown.totalKWh)
    || breakdown.totalKWh <= 0
    || !Number.isFinite(breakdown.subMeteredKWh)
  ) {
    return {
      status: 'NO_MAIN_METER',
      mainMeterKWh: breakdown?.totalKWh ?? null,
      subMeteredKWh,
      diffKWh: null,
      diffRatio: null,
      threshold: safeThreshold,
    };
  }

  const diffKWh = breakdown.totalKWh - subMeteredKWh;
  const diffRatio = diffKWh / breakdown.totalKWh;
  const status: MeterBalanceStatus = Math.abs(diffRatio) <= safeThreshold
    ? 'BALANCED'
    : diffRatio > safeThreshold
      ? 'UNMETERED_GAP'
      : 'SUBMETER_EXCEEDS_MAIN';

  return {
    status,
    mainMeterKWh: breakdown.totalKWh,
    subMeteredKWh,
    diffKWh,
    diffRatio,
    threshold: safeThreshold,
  };
}
