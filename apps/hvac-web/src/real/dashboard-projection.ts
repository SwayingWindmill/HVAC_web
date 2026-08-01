import type { Device } from '@/api/generated/platformGateway.gen';
import type { DeviceObservationSnapshot } from '@/api/generated/s2Telemetry.gen';
import type { EnergySeriesResponse } from '@/api/energy-analytics';

export type DashboardDeviceState = 'ONLINE' | 'OFFLINE' | 'STALE' | 'UNKNOWN' | 'UNAVAILABLE' | 'NOT_APPLICABLE';

export interface DashboardPresenceSuccess {
  status: 'ok';
  deviceId: string;
  snapshot: Pick<DeviceObservationSnapshot, 'deviceId' | 'evaluatedAt' | 'evaluationAvailability' | 'displayState' | 'presence'>;
}

export interface DashboardPresenceFailure {
  status: 'error';
  deviceId: string;
}

export type DashboardPresenceItem = DashboardPresenceSuccess | DashboardPresenceFailure;

export interface DashboardDeviceCounts {
  total: number;
  online: number;
  offline: number;
  stale: number;
  unknown: number;
  unavailable: number;
  notApplicable: number;
  attention: number;
}

export interface DashboardAttentionDevice {
  deviceId: string;
  displayName: string;
  deviceType: string;
  state: Exclude<DashboardDeviceState, 'ONLINE' | 'NOT_APPLICABLE'>;
  lastSeenAt: string | null;
  evaluatedAt: string | null;
}

export interface DashboardDeviceProjection {
  counts: DashboardDeviceCounts;
  attentionDevices: DashboardAttentionDevice[];
}

const ATTENTION_PRIORITY: Record<DashboardAttentionDevice['state'], number> = {
  OFFLINE: 0,
  STALE: 1,
  UNKNOWN: 2,
  UNAVAILABLE: 3,
};

function dashboardState(item: DashboardPresenceItem | undefined): DashboardDeviceState {
  if (!item || item.status === 'error') return 'UNAVAILABLE';
  if (item.snapshot.presence.applicability === 'NOT_APPLICABLE' || item.snapshot.displayState === null) {
    return 'NOT_APPLICABLE';
  }
  return item.snapshot.displayState;
}

export function projectDashboardDevices(
  devices: ReadonlyArray<Pick<Device, 'id' | 'displayName' | 'deviceType'>>,
  presenceItems: ReadonlyArray<DashboardPresenceItem>,
): DashboardDeviceProjection {
  const byDeviceId = new Map(presenceItems.map((item) => [item.deviceId, item]));
  const counts: DashboardDeviceCounts = {
    total: devices.length,
    online: 0,
    offline: 0,
    stale: 0,
    unknown: 0,
    unavailable: 0,
    notApplicable: 0,
    attention: 0,
  };
  const attentionDevices: DashboardAttentionDevice[] = [];

  for (const device of devices) {
    const item = byDeviceId.get(device.id);
    const state = dashboardState(item);
    switch (state) {
      case 'ONLINE':
        counts.online += 1;
        break;
      case 'OFFLINE':
        counts.offline += 1;
        counts.attention += 1;
        break;
      case 'STALE':
        counts.stale += 1;
        counts.attention += 1;
        break;
      case 'UNKNOWN':
        counts.unknown += 1;
        counts.attention += 1;
        break;
      case 'UNAVAILABLE':
        counts.unavailable += 1;
        counts.attention += 1;
        break;
      case 'NOT_APPLICABLE':
        counts.notApplicable += 1;
        break;
    }

    if (state !== 'ONLINE' && state !== 'NOT_APPLICABLE') {
      const snapshot = item?.status === 'ok' ? item.snapshot : null;
      attentionDevices.push({
        deviceId: device.id,
        displayName: device.displayName,
        deviceType: device.deviceType,
        state,
        lastSeenAt: snapshot?.presence.lastSeenAt ?? null,
        evaluatedAt: snapshot?.evaluatedAt ?? null,
      });
    }
  }

  attentionDevices.sort((left, right) => (
    ATTENTION_PRIORITY[left.state] - ATTENTION_PRIORITY[right.state]
    || left.displayName.localeCompare(right.displayName)
  ));
  return { counts, attentionDevices };
}

export type DashboardEnergyState = 'READY' | 'EMPTY' | 'PARTIAL' | 'STALE' | 'SUSPECT';

export interface DashboardEnergyProjection {
  state: DashboardEnergyState;
  totalKWh: number | null;
  pointCount: number;
}

const GRANULARITY_MILLISECONDS = {
  hour: 60 * 60 * 1000,
  day: 24 * 60 * 60 * 1000,
  month: 28 * 24 * 60 * 60 * 1000,
} as const;

export function projectDashboardEnergy(
  response: Pick<EnergySeriesResponse, 'points' | 'metadata'>,
  requestedTo: string,
): DashboardEnergyProjection {
  const pointCount = response.points.length;
  const totalKWh = pointCount === 0
    ? null
    : response.points.reduce((total, point) => total + point.energyKWh, 0);
  if (pointCount === 0) return { state: 'EMPTY', totalKWh, pointCount };
  if (response.metadata.partial) return { state: 'PARTIAL', totalKWh, pointCount };

  const watermark = response.metadata.aggregateWatermark ?? response.metadata.dataWatermark;
  const tolerance = GRANULARITY_MILLISECONDS[response.metadata.actualGranularity] * 2;
  if (!watermark || Date.parse(requestedTo) - Date.parse(watermark) > tolerance) {
    return { state: 'STALE', totalKWh, pointCount };
  }
  if (response.metadata.qualitySummary.suspect > 0 || response.metadata.qualitySummary.invalid > 0) {
    return { state: 'SUSPECT', totalKWh, pointCount };
  }
  return { state: 'READY', totalKWh, pointCount };
}
