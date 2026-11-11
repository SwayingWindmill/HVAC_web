import { queryOptions } from '@tanstack/react-query';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type EnergySeriesResponse,
  type UtilityBillMeterScope,
  type UtilityBillStatus,
} from '@/api/generated/platformGateway.gen';
import {
  describeBillingApiError,
  resolveBillingMonthWindow,
  resolveReadingDateWindow,
} from './billing-model';

export const billingGatewayClient = createPlatformGatewayClient();

export const billingKeys = {
  budgetsRoot: (siteId: string) => ['electricity-budgets', siteId] as const,
  budgetForMonth: (siteId: string, billingMonth: string) =>
    ['electricity-budgets', siteId, billingMonth] as const,
  billsRoot: (siteId: string) => ['utility-bills', siteId] as const,
  billsList: (
    siteId: string,
    params: {
      readonly billingMonth?: string;
      readonly meterScope?: UtilityBillMeterScope;
      readonly status?: UtilityBillStatus;
    },
  ) =>
    [
      'utility-bills',
      siteId,
      params.billingMonth ?? 'ALL',
      params.meterScope ?? 'ALL',
      params.status ?? 'ALL',
    ] as const,
  monthElectricity: (siteId: string, billingMonth: string, queryToIso: string) =>
    ['billing-month-electricity', siteId, billingMonth, queryToIso] as const,
  readingWindowElectricity: (
    siteId: string,
    readingStartDate: string,
    readingEndDate: string,
  ) =>
    ['billing-reading-window-electricity', siteId, readingStartDate, readingEndDate] as const,
};

export function siteMonthlyBudgetQuery(siteId: string, billingMonth: string) {
  return queryOptions({
    queryKey: billingKeys.budgetForMonth(siteId, billingMonth),
    queryFn: ({ signal }) =>
      billingGatewayClient.listSiteMonthlyElectricityBudgets(
        siteId,
        { billingMonth },
        { signal },
      ),
    select: (response) => response.data.items[0] ?? null,
    staleTime: 30_000,
  });
}

export function siteUtilityBillsQuery(
  siteId: string,
  params: {
    readonly billingMonth?: string;
    readonly meterScope?: UtilityBillMeterScope;
    readonly status?: UtilityBillStatus;
  } = {},
) {
  return queryOptions({
    queryKey: billingKeys.billsList(siteId, params),
    queryFn: ({ signal }) =>
      billingGatewayClient.listSiteUtilityBills(siteId, params, { signal }),
    select: (response) => response.data.items,
    staleTime: 30_000,
  });
}

export function siteBillingMonthElectricityQuery(input: {
  readonly tenantId: string;
  readonly siteId: string;
  readonly timezone: string;
  readonly billingMonth: string;
  readonly now: Date;
  readonly csrfToken: string;
  readonly enabled: boolean;
}) {
  const window = resolveBillingMonthWindow(
    input.billingMonth,
    input.now,
    input.timezone,
  );
  // Bucket query timestamp to the minute so live clock ticks do not spam energy-series every render.
  const queryToMs =
    window.state === 'CURRENT'
      ? Math.max(
          window.from.getTime(),
          Math.floor(window.queryTo.getTime() / 60_000) * 60_000,
        )
      : window.queryTo.getTime();
  const queryToIso = new Date(queryToMs).toISOString();
  const hasSpan = queryToMs > window.from.getTime();

  return queryOptions({
    queryKey: billingKeys.monthElectricity(
      input.siteId,
      input.billingMonth,
      hasSpan ? queryToIso : 'FUTURE',
    ),
    queryFn: async ({ signal }): Promise<EnergySeriesResponse | null> => {
      if (!hasSpan) return null;
      const response = await billingGatewayClient.queryEnergySeries(
        {
          tenantId: input.tenantId,
          siteId: input.siteId,
          energyType: 'electricity',
          granularity: 'day',
          timezone: input.timezone,
          from: window.from.toISOString(),
          to: queryToIso,
          qualityPolicy: 'VALID_ONLY',
        },
        input.csrfToken,
        { signal },
      );
      return response.data;
    },
    enabled: input.enabled,
    staleTime: 60_000,
    retry: false,
  });
}

export function siteReadingWindowElectricityQuery(input: {
  readonly tenantId: string;
  readonly siteId: string;
  readonly timezone: string;
  readonly readingStartDate: string;
  readonly readingEndDate: string;
  readonly csrfToken: string;
  readonly enabled: boolean;
}) {
  const window = resolveReadingDateWindow(
    input.readingStartDate,
    input.readingEndDate,
    input.timezone,
  );

  return queryOptions({
    queryKey: billingKeys.readingWindowElectricity(
      input.siteId,
      input.readingStartDate,
      input.readingEndDate,
    ),
    queryFn: async ({ signal }): Promise<EnergySeriesResponse | null> => {
      if (!window) return null;
      const response = await billingGatewayClient.queryEnergySeries(
        {
          tenantId: input.tenantId,
          siteId: input.siteId,
          energyType: 'electricity',
          granularity: 'day',
          timezone: input.timezone,
          from: window.from.toISOString(),
          to: window.to.toISOString(),
          qualityPolicy: 'VALID_ONLY',
        },
        input.csrfToken,
        { signal },
      );
      return response.data;
    },
    enabled: input.enabled && window !== null,
    staleTime: 60_000,
    retry: false,
  });
}

export function isBillingRevisionConflict(error: unknown): boolean {
  return (
    error instanceof PlatformApiError &&
    error.problem.status === 409 &&
    error.problem.code === 'REGISTRY_REVISION_CONFLICT'
  );
}

export { describeBillingApiError };
