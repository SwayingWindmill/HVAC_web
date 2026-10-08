import type { DeviceObservationSnapshot } from '../../api/generated/s2Telemetry.gen.ts';
import { selectAssetsRepresentativePoints, type AssetsDeviceRow } from './model.ts';
import {
  projectAssetsDeviceOperationalState,
  type AssetsSnapshotResult,
} from './operational-projection.ts';

export interface AssetsRealtimeTarget {
  readonly clientSubscriptionId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
}

interface AssetsRealtimeStateBase extends AssetsRealtimeTarget {
  readonly updatedAt: string;
}

export type AssetsRealtimeState =
  | (AssetsRealtimeStateBase & { readonly status: 'initializing'; readonly snapshot: null })
  | (AssetsRealtimeStateBase & {
    readonly status: 'snapshot';
    readonly snapshot: Readonly<DeviceObservationSnapshot>;
    readonly reason: 'authoritative-snapshot' | 'recovering' | 'reconnecting';
  })
  | (AssetsRealtimeStateBase & {
    readonly status: 'live';
    readonly snapshot: Readonly<DeviceObservationSnapshot>;
    readonly recovered: boolean;
  })
  | (AssetsRealtimeStateBase & {
    readonly status: 'unavailable';
    readonly snapshot: Readonly<DeviceObservationSnapshot> | null;
    readonly reason: 'snapshot-unavailable' | 'transport-unavailable' | 'recovery-required' | 'protocol-violation';
    readonly retryable: boolean;
  })
  | (AssetsRealtimeStateBase & { readonly status: 'revoked'; readonly snapshot: null });

export interface AssetsRealtimeScope {
  readonly protectedGeneration: number;
  readonly clientSubscriptionId: string;
  readonly deviceId: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly keys: readonly string[];
}

export interface AssetsRealtimeProjection {
  readonly row: AssetsDeviceRow;
  readonly source: 'current-query' | 'realtime' | 'none';
  readonly baselineRevision: number | null;
  readonly realtimeRevision: number | null;
  readonly realtimeOlderThanBaseline: boolean;
  readonly suppressedByRevocation: boolean;
}

function exactArray(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function cloneSnapshot(snapshot: DeviceObservationSnapshot): DeviceObservationSnapshot {
  return JSON.parse(JSON.stringify(snapshot)) as DeviceObservationSnapshot;
}

export const REAL_ASSETS_REALTIME_KEY_LIMIT = 64;

export type AssetsRealtimeSubscriptionEligibility =
  | { readonly state: 'eligible'; readonly pointCount: number }
  | { readonly state: 'no-points'; readonly pointCount: 0 }
  | { readonly state: 'too-many-points'; readonly pointCount: number; readonly limit: typeof REAL_ASSETS_REALTIME_KEY_LIMIT };

export function listAssetsRealtimeKeys(row: Pick<AssetsDeviceRow, 'telemetryPoints'>): readonly string[] {
  return row.telemetryPoints
    .filter((point) => point.status === 'ACTIVE' && point.pointType !== 'COMMAND')
    .map((point) => point.pointCode)
    .sort((left, right) => left.localeCompare(right));
}

export function assetsRealtimeSubscriptionEligibility(
  row: Pick<AssetsDeviceRow, 'telemetryPoints'>,
): AssetsRealtimeSubscriptionEligibility {
  const pointCount = listAssetsRealtimeKeys(row).length;
  if (pointCount === 0) return { state: 'no-points', pointCount: 0 };
  if (pointCount > REAL_ASSETS_REALTIME_KEY_LIMIT) {
    return { state: 'too-many-points', pointCount, limit: REAL_ASSETS_REALTIME_KEY_LIMIT };
  }
  return { state: 'eligible', pointCount };
}

export function assetsRealtimeSubscriptionId(protectedGeneration: number, deviceId: string): string {
  if (!Number.isSafeInteger(protectedGeneration) || protectedGeneration < 1) {
    throw new Error('Realtime subscription requires a positive protected generation');
  }
  return `real-assets-detail:${protectedGeneration}:${deviceId}`;
}

export function createAssetsRealtimeScope(
  row: AssetsDeviceRow,
  protectedGeneration: number,
): AssetsRealtimeScope {
  const keys = listAssetsRealtimeKeys(row);
  if (keys.length === 0) throw new Error('Realtime subscription requires at least one active non-command Registry Point');
  if (keys.length > REAL_ASSETS_REALTIME_KEY_LIMIT) throw new Error(`Realtime subscription exceeds the ${REAL_ASSETS_REALTIME_KEY_LIMIT}-key public limit`);
  return Object.freeze({
    protectedGeneration,
    clientSubscriptionId: assetsRealtimeSubscriptionId(protectedGeneration, row.device.id),
    deviceId: row.device.id,
    tenantId: row.device.tenantId,
    siteId: row.device.siteId,
    keys: Object.freeze([...keys]),
  });
}

export function createAssetsRealtimeTarget(scope: AssetsRealtimeScope): AssetsRealtimeTarget {
  return {
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId: scope.deviceId,
    keys: [...scope.keys],
  };
}

function validateSnapshot(snapshot: DeviceObservationSnapshot, scope: AssetsRealtimeScope): void {
  if (snapshot.schemaVersion !== 1
    || snapshot.deviceId !== scope.deviceId
    || snapshot.tenantId !== scope.tenantId
    || snapshot.siteId !== scope.siteId
    || snapshot.businessRevision < 1
    || snapshot.values.length !== scope.keys.length
    || snapshot.values.some((value, index) => value.key !== scope.keys[index])) {
    throw new Error('Realtime Snapshot escaped the authorized Tenant, Site, Device or exact-key scope');
  }
}

export function validateAssetsRealtimeState(
  state: AssetsRealtimeState,
  scope: AssetsRealtimeScope,
): AssetsRealtimeState {
  if (state.clientSubscriptionId !== scope.clientSubscriptionId
    || state.deviceId !== scope.deviceId
    || !exactArray(state.keys, scope.keys)) {
    throw new Error('Realtime state escaped the exact subscription scope');
  }
  if (state.snapshot) validateSnapshot(state.snapshot, scope);
  return state;
}

function withSnapshotResult(
  row: AssetsDeviceRow,
  snapshotResult: AssetsSnapshotResult | undefined,
): AssetsDeviceRow {
  const operational = projectAssetsDeviceOperationalState({
    device: row.device,
    telemetryPoints: row.telemetryPoints,
    snapshotResult,
  });
  return {
    ...row,
    snapshotResult,
    operational,
    representativePoints: selectAssetsRepresentativePoints(row.telemetryPoints, row.profile, operational.points),
  };
}

export function projectAssetsRealtimeRow(
  row: AssetsDeviceRow,
  state: AssetsRealtimeState | null,
): AssetsRealtimeProjection {
  const baseline = row.snapshotResult?.status === 'ok' ? row.snapshotResult.snapshot : null;
  const baselineRevision = baseline?.businessRevision ?? null;
  if (state?.status === 'revoked') {
    return {
      row: withSnapshotResult(row, undefined), source: 'none', baselineRevision, realtimeRevision: null,
      realtimeOlderThanBaseline: false, suppressedByRevocation: true,
    };
  }
  const realtime = state?.snapshot ?? null;
  const realtimeRevision = realtime?.businessRevision ?? null;
  if (!realtime) {
    return {
      row, source: baseline ? 'current-query' : 'none', baselineRevision, realtimeRevision,
      realtimeOlderThanBaseline: false, suppressedByRevocation: false,
    };
  }
  if (baseline && realtime.businessRevision < baseline.businessRevision) {
    return {
      row, source: 'current-query', baselineRevision, realtimeRevision,
      realtimeOlderThanBaseline: true, suppressedByRevocation: false,
    };
  }
  return {
    row: withSnapshotResult(row, { status: 'ok', snapshot: cloneSnapshot(realtime) }),
    source: 'realtime', baselineRevision, realtimeRevision,
    realtimeOlderThanBaseline: false, suppressedByRevocation: false,
  };
}
