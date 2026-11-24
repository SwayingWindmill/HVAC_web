import { queryOptions } from '@tanstack/react-query';
import {
  createSiteMeterRatioVersion,
  deleteSiteAccountingHoliday,
  describeAccountingConfigError,
  fetchSiteCounterAudit,
  listSiteAccountingHolidays,
  listSiteMeterRatioVersions,
  retireSiteMeterRatioVersion,
  upsertSiteAccountingHoliday,
  type CounterAnomalyAudit,
  type MeterRatioVersion,
  type SiteCalendarHoliday,
} from '@/api/accounting-config';
import {
  describeAccountingRecalcError,
  listSiteAccountingRecalcJobs,
  submitSiteAccountingRecalc,
  type AccountingRecalcGranularity,
  type AccountingRecalcJob,
} from '@/api/accounting-recalc';
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
  monthElectricityRoot: (siteId: string) => ['billing-month-electricity', siteId] as const,
  monthElectricity: (siteId: string, billingMonth: string, queryToIso: string) =>
    ['billing-month-electricity', siteId, billingMonth, queryToIso] as const,
  readingWindowElectricityRoot: (siteId: string) =>
    ['billing-reading-window-electricity', siteId] as const,
  readingWindowElectricity: (
    siteId: string,
    readingStartDate: string,
    readingEndDate: string,
  ) =>
    ['billing-reading-window-electricity', siteId, readingStartDate, readingEndDate] as const,
  recalcJobs: (siteId: string) => ['accounting-recalc-jobs', siteId] as const,
  holidaysRoot: (siteId: string) => ['accounting-holidays', siteId] as const,
  holidays: (siteId: string, year?: number) =>
    ['accounting-holidays', siteId, year ?? 'ALL'] as const,
  meterRatiosRoot: (siteId: string) => ['accounting-meter-ratios', siteId] as const,
  meterRatios: (
    siteId: string,
    params: { readonly pointId?: string; readonly includeSuperseded?: boolean } = {},
  ) =>
    [
      'accounting-meter-ratios',
      siteId,
      params.pointId ?? 'ALL',
      params.includeSuperseded ? 'WITH_SUPERSEDED' : 'ACTIVE_ONLY',
    ] as const,
  counterAuditRoot: (siteId: string) => ['accounting-counter-audit', siteId] as const,
  counterAudit: (
    siteId: string,
    params: {
      readonly from: string;
      readonly to: string;
      readonly anomaliesOnly?: boolean;
      readonly limit?: number;
    },
  ) =>
    [
      'accounting-counter-audit',
      siteId,
      params.from,
      params.to,
      params.anomaliesOnly ?? true,
      params.limit ?? 200,
    ] as const,
};

export function siteAccountingHolidaysQuery(siteId: string, year?: number, enabled = true) {
  return queryOptions({
    queryKey: billingKeys.holidays(siteId, year),
    queryFn: ({ signal }) => listSiteAccountingHolidays(siteId, { year, signal }),
    select: (items): readonly SiteCalendarHoliday[] =>
      [...items].sort((left, right) => left.holidayDate.localeCompare(right.holidayDate)),
    enabled: Boolean(siteId) && enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function siteMeterRatioVersionsQuery(
  siteId: string,
  params: { readonly pointId?: string; readonly includeSuperseded?: boolean } = {},
  enabled = true,
) {
  return queryOptions({
    queryKey: billingKeys.meterRatios(siteId, params),
    queryFn: ({ signal }) =>
      listSiteMeterRatioVersions(siteId, {
        pointId: params.pointId,
        includeSuperseded: params.includeSuperseded,
        signal,
      }),
    select: (items): readonly MeterRatioVersion[] =>
      [...items].sort((left, right) => Date.parse(right.effectiveFrom) - Date.parse(left.effectiveFrom)),
    enabled: Boolean(siteId) && enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function siteCounterAuditQuery(
  siteId: string,
  params: {
    readonly from: string;
    readonly to: string;
    readonly anomaliesOnly?: boolean;
    readonly limit?: number;
  },
  enabled = true,
) {
  return queryOptions({
    queryKey: billingKeys.counterAudit(siteId, params),
    queryFn: ({ signal }): Promise<CounterAnomalyAudit> =>
      fetchSiteCounterAudit(siteId, {
        from: params.from,
        to: params.to,
        anomaliesOnly: params.anomaliesOnly,
        limit: params.limit,
        signal,
      }),
    enabled: Boolean(siteId) && enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function siteAccountingRecalcJobsQuery(siteId: string, enabled = true) {
  return queryOptions({
    queryKey: billingKeys.recalcJobs(siteId),
    queryFn: ({ signal }) => listSiteAccountingRecalcJobs(siteId, { signal }),
    select: (items): readonly AccountingRecalcJob[] =>
      [...items].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt)),
    enabled: Boolean(siteId) && enabled,
    staleTime: 10_000,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.some((job) => job.status === 'PENDING' || job.status === 'RUNNING')
        ? 3_000
        : false,
  });
}

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
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === siteId ? previousData : undefined,
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
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === siteId ? previousData : undefined,
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

export {
  createSiteMeterRatioVersion,
  deleteSiteAccountingHoliday,
  describeAccountingConfigError,
  describeAccountingRecalcError,
  describeBillingApiError,
  retireSiteMeterRatioVersion,
  submitSiteAccountingRecalc,
  upsertSiteAccountingHoliday,
  type AccountingRecalcGranularity,
  type AccountingRecalcJob,
  type CounterAnomalyAudit,
  type MeterRatioVersion,
  type SiteCalendarHoliday,
};
