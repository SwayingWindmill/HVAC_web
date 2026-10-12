import type { PeriodCode } from './tariff-model';

/** Period colours, hottest first: the same everywhere a Tariff period is drawn. */
export const PERIOD_COLOR: Record<PeriodCode, string> = {
  SUPER_PEAK: 'var(--destructive)',
  PEAK: 'var(--warning)',
  FLAT: 'var(--chart-1)',
  VALLEY: 'var(--success)',
};
