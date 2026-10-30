import type { RealtimeSubscriptionState } from '../../app/realtime-status.ts';
import type { AssetsRealtimeState } from './realtime.ts';
import type { SiteDevicesRealtimePhase } from './useSiteDevicesRealtime.ts';

function deviceState(state: AssetsRealtimeState): RealtimeSubscriptionState | null {
  switch (state.status) {
    case 'initializing':
      return 'connecting';
    case 'snapshot':
      if (state.reason === 'reconnecting') return 'reconnecting';
      if (state.reason === 'recovering') return 'resync-required';
      return 'connecting';
    case 'live':
      return 'live';
    case 'unavailable':
      return state.reason === 'recovery-required' ? 'resync-required' : 'unavailable';
    case 'revoked':
      // The session is being purged; the Device no longer has a subscription.
      return null;
  }
}

const SEVERITY: readonly RealtimeSubscriptionState[] = ['unavailable', 'resync-required', 'reconnecting', 'connecting', 'live'];

/**
 * The header state for one page's site-devices session: the most severe Device state, so a
 * single stalled Device is not hidden behind the others. No session means idle (未订阅).
 */
export function siteDevicesRealtimeShellState(
  phase: SiteDevicesRealtimePhase,
  states: ReadonlyMap<string, AssetsRealtimeState>,
): RealtimeSubscriptionState {
  switch (phase) {
    case 'closed':
    case 'not-authorized':
    case 'scope-too-large':
    case 'purged':
      return 'idle';
    case 'opening':
      return 'connecting';
    case 'error':
      return 'unavailable';
    case 'active': {
      let worst: RealtimeSubscriptionState | null = null;
      for (const state of states.values()) {
        const mapped = deviceState(state);
        if (mapped && (worst === null || SEVERITY.indexOf(mapped) < SEVERITY.indexOf(worst))) worst = mapped;
      }
      if (worst !== null) return worst;
      return states.size === 0 ? 'connecting' : 'idle';
    }
  }
}
