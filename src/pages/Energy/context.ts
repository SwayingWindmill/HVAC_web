import { useOutletContext } from 'react-router-dom';

export type EnergyGranularity = 'year' | 'month' | 'week' | 'day';
export type EnergyType = 'electricity' | 'water' | 'gas' | 'cooling';
export type EnergyCompareMode = 'previous' | 'year-over-year' | 'baseline';

export interface EnergySystemContext {
  granularity: EnergyGranularity;
  year: number;
  month: number;
  day: number;
  week: number;
  date: string;
  energyType: EnergyType;
  compareMode: EnergyCompareMode;
  unit: string;
  energyLabel: string;
  updateParams: (patch: Record<string, string | number | null>, replace?: boolean) => void;
  navigateGranularity: (granularity: EnergyGranularity, patch?: Record<string, string | number | null>) => void;
}

export const ENERGY_TYPE_META: Record<EnergyType, { label: string; unit: string; enabled: boolean }> = {
  electricity: { label: '电能', unit: 'kWh', enabled: true },
  water: { label: '水耗', unit: 'm³', enabled: false },
  gas: { label: '燃气', unit: 'Nm³', enabled: false },
  cooling: { label: '冷量', unit: 'GJ', enabled: false },
};

export const COMPARE_MODE_LABEL: Record<EnergyCompareMode, string> = {
  previous: '环比同期',
  'year-over-year': '同比同期',
  baseline: '运行基线',
};

export function useEnergySystemContext() {
  return useOutletContext<EnergySystemContext>();
}
