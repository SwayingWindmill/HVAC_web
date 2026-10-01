export type EnergyCommodity =
  | 'electricity'
  | 'gas'
  | 'water'
  | 'cooling'
  | 'heating'
  | 'solar'
  | 'storage';

export type EnergyStatusTone =
  | 'normal'
  | 'warning'
  | 'critical'
  | 'offline'
  | 'saving'
  | 'waste';

export interface CommodityConfig {
  readonly id: EnergyCommodity;
  readonly label: string;
  readonly defaultUnit: string;
  readonly largeUnit: string;
  readonly color: string; // Tailwind color token or hex
  readonly bgLight: string;
  readonly textClass: string;
}

export const COMMODITY_CONFIG: Readonly<Record<EnergyCommodity, CommodityConfig>> = {
  electricity: {
    id: 'electricity',
    label: '电力',
    defaultUnit: 'kWh',
    largeUnit: 'MWh',
    color: '#f59e0b', // amber-500
    bgLight: 'bg-amber-50 dark:bg-amber-950/30',
    textClass: 'text-amber-600 dark:text-amber-400',
  },
  gas: {
    id: 'gas',
    label: '燃气',
    defaultUnit: 'm³',
    largeUnit: 'km³',
    color: '#f97316', // orange-500
    bgLight: 'bg-orange-50 dark:bg-orange-950/30',
    textClass: 'text-orange-600 dark:text-orange-400',
  },
  water: {
    id: 'water',
    label: '用水',
    defaultUnit: 't',
    largeUnit: 'kt',
    color: '#06b6d4', // cyan-500
    bgLight: 'bg-cyan-50 dark:bg-cyan-950/30',
    textClass: 'text-cyan-600 dark:text-cyan-400',
  },
  cooling: {
    id: 'cooling',
    label: '供冷',
    defaultUnit: 'kWh',
    largeUnit: 'MWh',
    color: '#0ea5e9', // sky-500
    bgLight: 'bg-sky-50 dark:bg-sky-950/30',
    textClass: 'text-sky-600 dark:text-sky-400',
  },
  heating: {
    id: 'heating',
    label: '供热',
    defaultUnit: 'GJ',
    largeUnit: 'GJ',
    color: '#f43f5e', // rose-500
    bgLight: 'bg-rose-50 dark:bg-rose-950/30',
    textClass: 'text-rose-600 dark:text-rose-400',
  },
  solar: {
    id: 'solar',
    label: '光伏',
    defaultUnit: 'kWh',
    largeUnit: 'MWh',
    color: '#10b981', // emerald-500
    bgLight: 'bg-emerald-50 dark:bg-emerald-950/30',
    textClass: 'text-emerald-600 dark:text-emerald-400',
  },
  storage: {
    id: 'storage',
    label: '储能',
    defaultUnit: 'kWh',
    largeUnit: 'MWh',
    color: '#8b5cf6', // purple-500
    bgLight: 'bg-purple-50 dark:bg-purple-950/30',
    textClass: 'text-purple-600 dark:text-purple-400',
  },
};
