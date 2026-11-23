import type { QueryClient } from '@tanstack/react-query';
import { billingKeys } from '../query';

export interface TelemetryPointOption {
  readonly id: string;
  readonly label: string;
  readonly sublabel: string;
  readonly isCounter: boolean;
}

export function formatMultiplier(value: number): string {
  if (!Number.isFinite(value)) return '—';
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 6 });
}

export async function invalidateAfterHolidayMutation(
  queryClient: QueryClient,
  siteId: string,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: billingKeys.holidaysRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: billingKeys.recalcJobs(siteId) }),
    queryClient.invalidateQueries({ queryKey: ['tariff', siteId] }),
    queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: billingKeys.readingWindowElectricityRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: ['energy-series', siteId] }),
  ]);
}

export async function invalidateAfterRatioMutation(
  queryClient: QueryClient,
  siteId: string,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: billingKeys.meterRatiosRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: billingKeys.recalcJobs(siteId) }),
    queryClient.invalidateQueries({ queryKey: billingKeys.monthElectricityRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: billingKeys.readingWindowElectricityRoot(siteId) }),
    queryClient.invalidateQueries({ queryKey: ['energy-series', siteId] }),
  ]);
}
