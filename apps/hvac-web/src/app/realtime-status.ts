import { isUUIDv7 } from './router-paths';

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
        detail: '实时数据已连接。',
      };
    case 'connecting':
      return {
        code: 'RECONNECTING',
        label: '正在连接',
        detail: '正在连接这个站点的实时数据。',
      };
    case 'reconnecting':
      return {
        code: 'RECONNECTING',
        label: '重连中',
        detail: '实时数据正在重连；重连前收到的零散变化不会当作当前值。',
      };
    case 'resync-required':
      return {
        code: 'DEGRADED',
        label: '需要重新同步',
        detail: '实时数据中断过，正在重新读取最新值；不会用中断前后的片段拼出当前值。',
      };
    case 'unavailable':
      return {
        code: 'DISCONNECTED',
        label: '已断开',
        detail: '实时数据暂时连不上；这不代表设备离线。',
      };
    case 'idle':
      return {
        code: 'DISCONNECTED',
        label: '未订阅',
        detail: '还没有连接实时数据。',
      };
  }
}
