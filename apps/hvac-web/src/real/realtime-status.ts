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
