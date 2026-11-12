import { queryOptions } from '@tanstack/react-query';
import {
  createPlatformGatewayClient,
  type Device,
  type EnergySeriesResponse,
  type SiteAssetModel,
  type TelemetryPoint,
} from '@/api/generated/platformGateway.gen';
import {
  createS2TelemetryClient,
  type DeviceHistoryAggregateBucket,
} from '@/api/generated/s2Telemetry.gen';
import { listWorkOrders } from '@/api/work-orders';
import { loadAssetsCurrentState } from '@/features/assets/data';

const gatewayClient = createPlatformGatewayClient();
const telemetryClient = createS2TelemetryClient();

export interface HourlyHistoryRow {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly value: number;
  readonly minimum: number | null;
  readonly maximum: number | null;
  readonly resetCount: number;
}

export const integrationsKeys = {
  assetModel: (siteId: string) => ['integrations', 'asset-model', siteId] as const,
  snapshots: (siteId: string, deviceCount: number, pointCount: number) =>
    ['integrations', 'snapshots', siteId, deviceCount, pointCount] as const,
  hourlyEnergy: (
    siteId: string,
    energyType: 'electricity' | 'cooling',
    fromIso: string,
    toIso: string,
  ) => ['integrations', 'hourly-energy', siteId, energyType, fromIso, toIso] as const,
  externalWorkOrders: (siteId: string, sourceRef: string) =>
    ['integrations', 'external-work-orders', siteId, sourceRef] as const,
  pointHistory: (siteId: string, deviceId: string, pointCode: string, toMs: number) =>
    ['integrations', 'point-history', siteId, deviceId, pointCode, toMs] as const,
};

export function siteIntegrationsAssetModelQuery(siteId: string | null, enabled: boolean) {
  return queryOptions({
    queryKey: integrationsKeys.assetModel(siteId ?? 'NONE'),
    queryFn: async ({ signal }): Promise<SiteAssetModel> => {
      const response = await gatewayClient.getSiteAssetModel(siteId!, { signal });
      return response.data;
    },
    enabled: Boolean(siteId) && enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function siteIntegrationsSnapshotsQuery(input: {
  readonly tenantId: string | null;
  readonly siteId: string | null;
  readonly devices: readonly Device[];
  readonly telemetryPoints: readonly TelemetryPoint[];
  readonly csrfToken: string;
  readonly enabled: boolean;
}) {
  const activeDevices = input.devices.filter(
    (d) => d.status === 'ACTIVE' && d.deviceType !== 'GATEWAY',
  );
  return queryOptions({
    queryKey: integrationsKeys.snapshots(
      input.siteId ?? 'NONE',
      activeDevices.length,
      input.telemetryPoints.length,
    ),
    queryFn: async ({ signal }) => {
      if (activeDevices.length === 0) return new Map();
      const result = await loadAssetsCurrentState({
        client: telemetryClient,
        devices: activeDevices,
        telemetryPoints: input.telemetryPoints,
        tenantId: input.tenantId!,
        siteId: input.siteId!,
        csrfToken: input.csrfToken,
        currentRoutePolicyRevision: () => null,
        signal,
      });
      return result.byDeviceId;
    },
    enabled:
      Boolean(input.tenantId && input.siteId && input.csrfToken) && input.enabled,
    staleTime: 15_000,
    retry: false,
  });
}

export interface HourlyEnergyPair {
  readonly validOnly: EnergySeriesResponse;
  readonly validAndSuspect: EnergySeriesResponse;
}

export function siteHourlyEnergyPairQuery(input: {
  readonly tenantId: string | null;
  readonly siteId: string | null;
  readonly timezone: string;
  readonly energyType: 'electricity' | 'cooling';
  readonly breakdownDevice?: boolean;
  readonly fromMs: number;
  readonly toMs: number;
  readonly csrfToken: string;
  readonly enabled: boolean;
}) {
  const fromIso = new Date(input.fromMs).toISOString();
  const toIso = new Date(input.toMs).toISOString();
  return queryOptions({
    queryKey: integrationsKeys.hourlyEnergy(
      input.siteId ?? 'NONE',
      input.energyType,
      fromIso,
      toIso,
    ),
    queryFn: async ({ signal }): Promise<HourlyEnergyPair> => {
      const common = {
        tenantId: input.tenantId!,
        siteId: input.siteId!,
        energyType: input.energyType,
        granularity: 'hour' as const,
        timezone: input.timezone,
        from: fromIso,
        to: toIso,
        ...(input.breakdownDevice ? { breakdown: 'device' as const } : {}),
      };
      const [validRes, suspectRes] = await Promise.all([
        gatewayClient.queryEnergySeries(
          { ...common, qualityPolicy: 'VALID_ONLY' },
          input.csrfToken,
          { signal },
        ),
        gatewayClient.queryEnergySeries(
          { ...common, qualityPolicy: 'VALID_AND_SUSPECT' },
          input.csrfToken,
          { signal },
        ),
      ]);
      return {
        validOnly: validRes.data,
        validAndSuspect: suspectRes.data,
      };
    },
    enabled:
      Boolean(input.tenantId && input.siteId && input.csrfToken) && input.enabled,
    staleTime: 60_000,
    retry: false,
  });
}

export function externalSourceWorkOrdersQuery(
  siteId: string | null,
  sourceRef: string,
  enabled: boolean,
) {
  return queryOptions({
    queryKey: integrationsKeys.externalWorkOrders(siteId ?? 'NONE', sourceRef),
    queryFn: async ({ signal }) => {
      const res = await listWorkOrders(
        { sourceDomain: 'EXTERNAL', sourceRef, limit: 20 },
        { siteId: siteId!, signal },
      );
      return res.items;
    },
    enabled: Boolean(siteId && sourceRef) && enabled,
    staleTime: 15_000,
    retry: false,
  });
}

function mapAggregateBucketsToHourlyRows(
  buckets: readonly DeviceHistoryAggregateBucket[],
  pointCode: string,
): readonly HourlyHistoryRow[] {
  const rows: HourlyHistoryRow[] = [];
  for (const bucket of buckets) {
    if (bucket.telemetryKey !== pointCode) continue;
    if (bucket.gauge) {
      rows.push({
        periodStart: bucket.periodStart,
        periodEnd: bucket.periodEnd,
        value: bucket.gauge.average,
        minimum: bucket.gauge.minimum,
        maximum: bucket.gauge.maximum,
        resetCount: 0,
      });
    } else if (bucket.counter) {
      rows.push({
        periodStart: bucket.periodStart,
        periodEnd: bucket.periodEnd,
        value: bucket.counter.deltaSum,
        minimum: null,
        maximum: null,
        resetCount: bucket.counter.resetCount + bucket.counter.rolloverCount,
      });
    }
  }
  return rows;
}

export function devicePointHourlyHistoryQuery(input: {
  readonly siteId: string;
  readonly deviceId: string;
  readonly pointCode: string | null;
  readonly timezone: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly csrfToken: string;
  readonly enabled: boolean;
}) {
  return queryOptions({
    queryKey: integrationsKeys.pointHistory(
      input.siteId,
      input.deviceId,
      input.pointCode ?? 'NONE',
      input.toMs,
    ),
    queryFn: async ({ signal }): Promise<readonly HourlyHistoryRow[]> => {
      const response = await telemetryClient.queryDeviceHistoryAggregate(
        {
          deviceId: input.deviceId,
          keys: [input.pointCode!],
          from: new Date(input.fromMs).toISOString(),
          to: new Date(input.toMs).toISOString(),
          granularity: 'HOUR',
          timezone: input.timezone,
          qualityPolicy: 'USABLE',
        },
        { csrfToken: input.csrfToken, signal },
      );
      return mapAggregateBucketsToHourlyRows(response.buckets, input.pointCode!);
    },
    enabled: Boolean(input.pointCode && input.csrfToken) && input.enabled,
    staleTime: 60_000,
    retry: false,
  });
}
