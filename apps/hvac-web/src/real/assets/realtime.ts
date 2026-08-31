import type { DeviceObservationSnapshot } from '../../api/generated/s2Telemetry.gen.ts';
import { selectRealAssetsRepresentativePoints, type RealAssetsDeviceRow } from './model.ts';
import {
  projectRealAssetsDeviceOperationalState,
  type RealAssetsSnapshotResult,
} from './operational-projection.ts';

export interface RealAssetsRealtimeTarget {
  readonly clientSubscriptionId: string;
  readonly deviceId: string;
  readonly keys: readonly string[];
}

interface RealAssetsRealtimeStateBase extends RealAssetsRealtimeTarget {
  readonly updatedAt: string;
}

export type RealAssetsRealtimeState =
  | (RealAssetsRealtimeStateBase & { readonly status: 'initializing'; readonly snapshot: null })
  | (RealAssetsRealtimeStateBase & {
    readonly status: 'snapshot';
    readonly snapshot: Readonly<DeviceObservationSnapshot>;
    readonly reason: 'authoritative-snapshot' | 'recovering' | 'reconnecting';
  })
  | (RealAssetsRealtimeStateBase & {
    readonly status: 'live';
    readonly snapshot: Readonly<DeviceObservationSnapshot>;
    readonly recovered: boolean;
  })
  | (RealAssetsRealtimeStateBase & {
    readonly status: 'unavailable';
    readonly snapshot: Readonly<DeviceObservationSnapshot> | null;
    readonly reason: 'snapshot-unavailable' | 'transport-unavailable' | 'recovery-required' | 'protocol-violation';
    readonly retryable: boolean;
  })
  | (RealAssetsRealtimeStateBase & { readonly status: 'revoked'; readonly snapshot: null });

export interface RealAssetsRealtimeScope {
  readonly protectedGeneration: number;
  readonly clientSubscriptionId: string;
  readonly deviceId: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly keys: readonly string[];
}

export interface RealAssetsRealtimeProjection {
  readonly row: RealAssetsDeviceRow;
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

export type RealAssetsRealtimeSubscriptionEligibility =
  | { readonly state: 'eligible'; readonly pointCount: number }
  | { readonly state: 'no-points'; readonly pointCount: 0 }
  | { readonly state: 'too-many-points'; readonly pointCount: number; readonly limit: typeof REAL_ASSETS_REALTIME_KEY_LIMIT };

export function listRealAssetsRealtimeKeys(row: Pick<RealAssetsDeviceRow, 'telemetryPoints'>): readonly string[] {
  return row.telemetryPoints
    .filter((point) => point.status === 'ACTIVE' && point.pointType !== 'COMMAND')
    .map((point) => point.pointCode)
    .sort((left, right) => left.localeCompare(right));
}

export function realAssetsRealtimeSubscriptionEligibility(
  row: Pick<RealAssetsDeviceRow, 'telemetryPoints'>,
): RealAssetsRealtimeSubscriptionEligibility {
  const pointCount = listRealAssetsRealtimeKeys(row).length;
  if (pointCount === 0) return { state: 'no-points', pointCount: 0 };
  if (pointCount > REAL_ASSETS_REALTIME_KEY_LIMIT) {
    return { state: 'too-many-points', pointCount, limit: REAL_ASSETS_REALTIME_KEY_LIMIT };
  }
  return { state: 'eligible', pointCount };
}

export function realAssetsRealtimeSubscriptionId(protectedGeneration: number, deviceId: string): string {
  if (!Number.isSafeInteger(protectedGeneration) || protectedGeneration < 1) {
    throw new Error('Realtime subscription requires a positive protected generation');
  }
  return `real-assets-detail:${protectedGeneration}:${deviceId}`;
}

export function createRealAssetsRealtimeScope(
  row: RealAssetsDeviceRow,
  protectedGeneration: number,
): RealAssetsRealtimeScope {
  const keys = listRealAssetsRealtimeKeys(row);
  if (keys.length === 0) throw new Error('Realtime subscription requires at least one active non-command Registry Point');
  if (keys.length > REAL_ASSETS_REALTIME_KEY_LIMIT) throw new Error(`Realtime subscription exceeds the ${REAL_ASSETS_REALTIME_KEY_LIMIT}-key public limit`);
  return Object.freeze({
    protectedGeneration,
    clientSubscriptionId: realAssetsRealtimeSubscriptionId(protectedGeneration, row.device.id),
    deviceId: row.device.id,
    tenantId: row.device.tenantId,
    siteId: row.device.siteId,
    keys: Object.freeze([...keys]),
  });
}

export function createRealAssetsRealtimeTarget(scope: RealAssetsRealtimeScope): RealAssetsRealtimeTarget {
  return {
    clientSubscriptionId: scope.clientSubscriptionId,
    deviceId: scope.deviceId,
    keys: [...scope.keys],
  };
}

function validateSnapshot(snapshot: DeviceObservationSnapshot, scope: RealAssetsRealtimeScope): void {
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

export function validateRealAssetsRealtimeState(
  state: RealAssetsRealtimeState,
  scope: RealAssetsRealtimeScope,
): RealAssetsRealtimeState {
  if (state.clientSubscriptionId !== scope.clientSubscriptionId
    || state.deviceId !== scope.deviceId
    || !exactArray(state.keys, scope.keys)) {
    throw new Error('Realtime state escaped the exact subscription scope');
  }
  if (state.snapshot) validateSnapshot(state.snapshot, scope);
  return state;
}

function withSnapshotResult(
  row: RealAssetsDeviceRow,
  snapshotResult: RealAssetsSnapshotResult | undefined,
): RealAssetsDeviceRow {
  const operational = projectRealAssetsDeviceOperationalState({
    device: row.device,
    telemetryPoints: row.telemetryPoints,
    snapshotResult,
  });
  return {
    ...row,
    snapshotResult,
    operational,
    representativePoints: selectRealAssetsRepresentativePoints(row.telemetryPoints, row.profile, operational.points),
  };
}

export function projectRealAssetsRealtimeRow(
  row: RealAssetsDeviceRow,
  state: RealAssetsRealtimeState | null,
): RealAssetsRealtimeProjection {
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

export function describeRealAssetsRealtimeState(state: RealAssetsRealtimeState): {
  readonly label: string;
  readonly detail: string;
  readonly degraded: boolean;
  readonly retryable: boolean;
} {
  if (state.status === 'initializing') {
    return { label: '正在建立精确实时订阅', detail: '等待同一 Device 与关键点位范围的权威 Snapshot。', degraded: false, retryable: false };
  }
  if (state.status === 'live') {
    return {
      label: state.recovered ? '实时已恢复' : '实时已连接',
      detail: '连续 Business Revision 已通过校验；transport position 不作为业务真相展示。',
      degraded: false, retryable: false,
    };
  }
  if (state.status === 'snapshot') {
    return {
      label: state.reason === 'reconnecting' ? '实时 transport 正在重连' : '正在以权威 Snapshot 对账',
      detail: '保留最后一次仍获授权的 Snapshot；未连续确认的 publication 不会写入当前状态。',
      degraded: true, retryable: true,
    };
  }
  if (state.status === 'unavailable') {
    return {
      label: state.reason === 'transport-unavailable' ? '实时 transport 暂不可用' : '实时连续性需要重新同步',
      detail: state.snapshot
        ? '保留最后一次仍获授权的 Snapshot，并重新读取权威基线；不会把 checkpoint 或历史点当成当前值。'
        : '当前没有可保留的权威 Snapshot；等待重新授权并读取新基线。',
      degraded: true, retryable: state.retryable,
    };
  }
  return {
    label: '实时订阅已撤权',
    detail: '受保护 Snapshot 与恢复记录已清除；晚到 event 不会继续写入详情。',
    degraded: true, retryable: false,
  };
}
