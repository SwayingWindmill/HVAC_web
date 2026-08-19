import { isUUIDv7 } from './site-routing';

export type RealtimeSubscriptionState =
  | 'idle'
  | 'connecting'
  | 'live'
  | 'reconnecting'
  | 'resync-required'
  | 'unavailable';

export interface RealtimeStatusSnapshot {
  readonly state: RealtimeSubscriptionState;
  readonly siteId?: string;
}

export interface RealtimeStatusUpdate {
  state: RealtimeSubscriptionState;
  siteId: string;
}

export type RealtimeStatusCode = 'CONNECTED' | 'RECONNECTING' | 'DEGRADED' | 'DISCONNECTED';

export interface RealtimeStatusPresentation {
  readonly code: RealtimeStatusCode;
  readonly label: string;
  readonly detail: string;
}

const REALTIME_STATUS_LABELS: Readonly<Record<RealtimeSubscriptionState, string>> = Object.freeze({
  idle: 'Idle — not subscribed',
  connecting: 'Connecting',
  live: 'Live',
  reconnecting: 'Reconnecting',
  'resync-required': 'Resync required',
  unavailable: 'Unavailable',
});

export function createIdleRealtimeStatus(): RealtimeStatusSnapshot {
  return Object.freeze({ state: 'idle' });
}

export function createRealtimeStatus(update: RealtimeStatusUpdate): RealtimeStatusSnapshot {
  if (!isUUIDv7(update.siteId)) {
    throw new Error('Realtime subscription status requires a validated Site identity.');
  }
  return Object.freeze({
    state: update.state,
    siteId: update.siteId,
  });
}

export function assertRealtimeStatusForSite(
  status: RealtimeStatusSnapshot,
  activeSiteId: string,
): RealtimeStatusSnapshot {
  if (!status.siteId || status.siteId !== activeSiteId) {
    throw new Error('Realtime subscription status does not belong to the active Site.');
  }
  return status;
}

export function realtimeStatusLabel(status: RealtimeStatusSnapshot): string {
  return REALTIME_STATUS_LABELS[status.state];
}

export function realtimeStatusPresentation(status: RealtimeStatusSnapshot): RealtimeStatusPresentation {
  switch (status.state) {
    case 'live':
      return {
        code: 'CONNECTED',
        label: '已连接',
        detail: status.siteId ? `当前 Site ${status.siteId} 的实时订阅已建立。` : '实时订阅已建立。',
      };
    case 'connecting':
      return {
        code: 'RECONNECTING',
        label: '正在连接',
        detail: '正在建立同一 Site 范围内的权威实时订阅。',
      };
    case 'reconnecting':
      return {
        code: 'RECONNECTING',
        label: '重连中',
        detail: '实时传输正在重连；未确认的增量不会被当作当前状态。',
      };
    case 'resync-required':
      return {
        code: 'DEGRADED',
        label: '需要重新同步',
        detail: '实时序列需要重新读取权威 Snapshot，当前值不会由缺口增量拼接。',
      };
    case 'unavailable':
      return {
        code: 'DISCONNECTED',
        label: '已断开',
        detail: '实时链路当前不可用；页面不会把传输故障当作设备离线。',
      };
    case 'idle':
      return {
        code: 'DISCONNECTED',
        label: '未订阅',
        detail: '当前没有活动 Site 实时订阅。',
      };
  }
}
