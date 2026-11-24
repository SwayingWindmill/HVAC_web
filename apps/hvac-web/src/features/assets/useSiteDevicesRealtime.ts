import { useEffect, useMemo, useState } from 'react';
import type { ProtectedScopeRequestToken, ProtectedScopeResource } from '@/app/protected-scope.ts';
import type { AssetsDeviceRow } from './model.ts';
import {
  assetsRealtimeSubscriptionEligibility,
  createAssetsRealtimeScope,
  createAssetsRealtimeTarget,
  validateAssetsRealtimeState,
  type AssetsRealtimeScope,
  type AssetsRealtimeState,
} from './realtime.ts';
import type { AssetsTelemetryLiveSession, AssetsTelemetryRuntime } from './telemetry-runtime.ts';

// Public subscription limits of the telemetry stream (one session per page).
const MAX_SUBSCRIPTIONS = 100;
const MAX_TOTAL_KEYS = 2048;
const REALTIME_COMMIT_INTERVAL_MS = 400;

export type SiteDevicesRealtimePhase =
  | 'closed'
  | 'not-authorized'
  | 'scope-too-large'
  | 'opening'
  | 'active'
  | 'error'
  | 'purged';

export interface SiteDevicesRealtimeResult {
  readonly phase: SiteDevicesRealtimePhase;
  /** Latest stream state per Device; absent until the session publishes it. */
  readonly states: ReadonlyMap<string, AssetsRealtimeState>;
  readonly error: Error | null;
}

interface UseSiteDevicesRealtimeInput {
  readonly rows: readonly AssetsDeviceRow[];
  readonly allowed: boolean;
  readonly protectedGeneration: number;
  readonly runtime: AssetsTelemetryRuntime;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
}

const NO_STATES: ReadonlyMap<string, AssetsRealtimeState> = new Map();

function errorValue(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}

/**
 * Streams current observations for every subscribable Device of the Site in one
 * session. Rows keep their Snapshot baseline; callers merge stream states over it.
 */
export function useSiteDevicesRealtime({
  rows,
  allowed,
  protectedGeneration,
  runtime,
  protectedRequestToken,
  registerProtectedResource,
}: UseSiteDevicesRealtimeInput): SiteDevicesRealtimeResult {
  const [result, setResult] = useState<SiteDevicesRealtimeResult>({ phase: 'closed', states: NO_STATES, error: null });

  const scopes = useMemo<readonly AssetsRealtimeScope[]>(
    () => protectedGeneration < 1
      ? []
      : rows
        .filter((row) => assetsRealtimeSubscriptionEligibility(row).state === 'eligible')
        .map((row) => createAssetsRealtimeScope(row, protectedGeneration)),
    [protectedGeneration, rows],
  );
  const scopeSignature = scopes.map((scope) => `${scope.deviceId}:${scope.keys.join(',')}`).join('|');
  const tooLarge = scopes.length > MAX_SUBSCRIPTIONS
    || scopes.reduce((total, scope) => total + scope.keys.length, 0) > MAX_TOTAL_KEYS;

  useEffect(() => {
    if (!allowed) {
      setResult({ phase: 'not-authorized', states: NO_STATES, error: null });
      return undefined;
    }
    if (tooLarge) {
      setResult({ phase: 'scope-too-large', states: NO_STATES, error: null });
      return undefined;
    }
    if (scopes.length === 0) {
      setResult({ phase: 'closed', states: NO_STATES, error: null });
      return undefined;
    }

    let scopeGuard: ProtectedScopeRequestToken;
    try {
      scopeGuard = protectedRequestToken();
      if (scopeGuard.generation !== protectedGeneration || scopes.some((scope) => scope.siteId !== scopeGuard.siteId)) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
    } catch (error) {
      setResult({ phase: 'error', states: NO_STATES, error: errorValue(error) });
      return undefined;
    }

    let active = true;
    let session: AssetsTelemetryLiveSession | null = null;
    let unsubscribe: (() => void) | null = null;
    const controller = new AbortController();
    const abortFromScope = () => {
      if (!controller.signal.aborted) controller.abort(scopeGuard.signal.reason);
    };
    if (scopeGuard.signal.aborted) abortFromScope();
    else scopeGuard.signal.addEventListener('abort', abortFromScope, { once: true });

    const close = (purge: boolean) => {
      active = false;
      if (!controller.signal.aborted) controller.abort(new DOMException('Site realtime closed.', 'AbortError'));
      unsubscribe?.();
      unsubscribe = null;
      session?.close();
      session = null;
      if (purge) runtime.live.purge();
    };

    let rafId: number | null = null;
    let timerId: number | null = null;
    let lastCommitAt = -Infinity;
    let hasCommitted = false;
    const validatedByDevice = new Map<string, {
      readonly state: ReturnType<AssetsTelemetryLiveSession['getState']>;
      readonly validated: AssetsRealtimeState;
    }>();

    const cancelScheduled = () => {
      if (rafId !== null) {
        window.cancelAnimationFrame(rafId);
        rafId = null;
      }
      if (timerId !== null) {
        window.clearTimeout(timerId);
        timerId = null;
      }
    };

    const fail = (error: unknown) => {
      cancelScheduled();
      close(true);
      scopeGuard.commit(() => setResult({ phase: 'error', states: NO_STATES, error: errorValue(error) }));
    };

    const unregister = registerProtectedResource({
      id: `site-devices-realtime:${protectedGeneration}`,
      kind: 'realtime',
      purge: () => {
        cancelScheduled();
        close(true);
        setResult({ phase: 'purged', states: NO_STATES, error: null });
      },
    });
    setResult({ phase: 'opening', states: NO_STATES, error: null });

    runtime.live.open(scopes.map(createAssetsRealtimeTarget), { signal: controller.signal }).then((opened) => {
      if (!active || controller.signal.aborted || !scopeGuard.commit(() => undefined)) {
        opened.close();
        return;
      }
      session = opened;

      let lastCommittedSize = 0;
      const flush = () => {
        rafId = null;
        if (timerId !== null) {
          window.clearTimeout(timerId);
          timerId = null;
        }
        if (!active) return;
        const states = new Map<string, AssetsRealtimeState>();
        let anyChanged = false;
        try {
          for (const scope of scopes) {
            const state = opened.getState(scope.clientSubscriptionId);
            if (!state) continue;
            const cached = validatedByDevice.get(scope.deviceId);
            if (cached && cached.state === state) {
              states.set(scope.deviceId, cached.validated);
            } else {
              const validated = validateAssetsRealtimeState(state, scope);
              validatedByDevice.set(scope.deviceId, { state, validated });
              states.set(scope.deviceId, validated);
              anyChanged = true;
            }
          }
        } catch (error) {
          fail(error);
          return;
        }
        if (hasCommitted && !anyChanged && states.size === lastCommittedSize) {
          return;
        }
        if (!scopeGuard.commit(() => setResult({ phase: 'active', states, error: null }))) {
          close(false);
          return;
        }
        hasCommitted = true;
        lastCommittedSize = states.size;
        lastCommitAt = performance.now();
        for (const state of states.values()) {
          if (state.status === 'revoked') {
            runtime.live.purge();
            break;
          }
        }
      };

      const scheduleFlush = (urgent: boolean) => {
        if (!active) return;
        const now = performance.now();
        const elapsed = now - lastCommitAt;
        if (urgent || !hasCommitted || elapsed >= REALTIME_COMMIT_INTERVAL_MS) {
          if (timerId !== null) {
            window.clearTimeout(timerId);
            timerId = null;
          }
          if (rafId === null) rafId = window.requestAnimationFrame(flush);
          return;
        }
        if (rafId !== null || timerId !== null) return;
        timerId = window.setTimeout(() => {
          timerId = null;
          if (!active) return;
          if (rafId === null) rafId = window.requestAnimationFrame(flush);
        }, REALTIME_COMMIT_INTERVAL_MS - elapsed);
      };

      scheduleFlush(true);
      unsubscribe = opened.subscribe((rawStates) => {
        scheduleFlush(rawStates.some((state) => state.status === 'revoked'));
      });
    }).catch((error: unknown) => {
      if (!active || controller.signal.aborted) return;
      scopeGuard.commit(() => setResult({ phase: 'error', states: NO_STATES, error: errorValue(error) }));
    });

    return () => {
      unregister();
      cancelScheduled();
      scopeGuard.signal.removeEventListener('abort', abortFromScope);
      close(false);
    };
    // scopeSignature identifies the subscribed devices and keys.
  }, [allowed, protectedGeneration, protectedRequestToken, registerProtectedResource, runtime, scopeSignature, tooLarge]);

  return result;
}
