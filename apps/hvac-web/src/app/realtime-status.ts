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
  /** Which open subscription reports this state; a page can hold several at once. */
  source: string;
  state: RealtimeSubscriptionState;
  siteId: string;
}

export type RealtimeStatusCode = 'CONNECTED' | 'RECONNECTING' | 'DEGRADED' | 'DISCONNECTED' | 'NOT_SUBSCRIBED';

// Most severe first: one stalled subscription is what the header must show.
const SEVERITY: readonly RealtimeSubscriptionState[] = ['unavailable', 'resync-required', 'reconnecting', 'connecting', 'live', 'idle'];

/** The one state the header shows for every subscription open on the page. */
export function combineRealtimeStates(states: Iterable<RealtimeSubscriptionState>): RealtimeSubscriptionState {
  let combined: RealtimeSubscriptionState = 'idle';
  for (const state of states) {
    if (SEVERITY.indexOf(state) < SEVERITY.indexOf(combined)) combined = state;
  }
  return combined;
}

export interface RealtimeStatusPresentation {
  readonly code: RealtimeStatusCode;
  readonly label: string;
}

export function createIdleRealtimeStatus(): RealtimeStatusSnapshot {
  return Object.freeze({ state: 'idle' });
}

export function createRealtimeStatus(update: Omit<RealtimeStatusUpdate, 'source'>): RealtimeStatusSnapshot {
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

export function realtimeStatusPresentation(status: RealtimeStatusSnapshot): RealtimeStatusPresentation {
  switch (status.state) {
    case 'live':
      return {
        code: 'CONNECTED',
        label: '已连接',
      };
    case 'connecting':
      return {
        code: 'RECONNECTING',
        label: '正在连接',
      };
    case 'reconnecting':
      return {
        code: 'RECONNECTING',
        label: '重连中',
      };
    case 'resync-required':
      return {
        code: 'DEGRADED',
        label: '正在重新读取',
      };
    case 'unavailable':
      return {
        code: 'DISCONNECTED',
        label: '已断开',
      };
    case 'idle':
      // Nothing on this page reads live readings, which is not a fault.
      return {
        code: 'NOT_SUBSCRIBED',
        label: '未订阅',
      };
  }
}
