import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ProtectedScopeRequestToken, ProtectedScopeResource } from '../protected-scope.ts';
import type { RealAssetsDeviceRow } from './model.ts';
import {
  createRealAssetsRealtimeScope,
  createRealAssetsRealtimeTarget,
  type RealAssetsRealtimeScope,
  type RealAssetsRealtimeState,
  validateRealAssetsRealtimeState,
} from './realtime.ts';
import { createBoundedRealtimePublisher } from './realtime-publisher.ts';
import type {
  RealAssetsTelemetryLiveSession,
  RealAssetsTelemetryRuntime,
} from './telemetry-runtime.ts';

export type RealAssetsRealtimePhase =
  | 'closed'
  | 'not-authorized'
  | 'not-configured'
  | 'opening'
  | 'active'
  | 'error'
  | 'purged';

export interface RealAssetsRealtimeResult {
  readonly phase: RealAssetsRealtimePhase;
  readonly state: RealAssetsRealtimeState | null;
  readonly error: Error | null;
  readonly refresh: () => void;
}

interface UseRealAssetsDeviceRealtimeInput {
  readonly row: RealAssetsDeviceRow | null;
  readonly allowed: boolean;
  readonly protectedGeneration: number;
  readonly authorizationEpoch: string;
  readonly runtime: RealAssetsTelemetryRuntime;
  readonly protectedRequestToken: () => ProtectedScopeRequestToken;
  readonly registerProtectedResource: (resource: ProtectedScopeResource) => () => void;
  readonly onRevoked?: () => void;
}

interface RealtimeSnapshot {
  phase: RealAssetsRealtimePhase;
  state: RealAssetsRealtimeState | null;
  error: Error | null;
}

const CLOSED: RealtimeSnapshot = { phase: 'closed', state: null, error: null };

function errorValue(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}

function createScope(row: RealAssetsDeviceRow | null, protectedGeneration: number): RealAssetsRealtimeScope | null {
  if (!row || row.profile.state !== 'configured') return null;
  return createRealAssetsRealtimeScope(row, protectedGeneration);
}

export function useRealAssetsDeviceRealtime({
  row,
  allowed,
  protectedGeneration,
  authorizationEpoch,
  runtime,
  protectedRequestToken,
  registerProtectedResource,
  onRevoked,
}: UseRealAssetsDeviceRealtimeInput): RealAssetsRealtimeResult {
  const [retryEpoch, setRetryEpoch] = useState(0);
  const [snapshot, setSnapshot] = useState<RealtimeSnapshot>(CLOSED);
  const sessionRef = useRef<RealAssetsTelemetryLiveSession | null>(null);
  const scope = useMemo(
    () => createScope(row, protectedGeneration),
    [protectedGeneration, row?.device.id, row?.device.tenantId, row?.device.siteId, row?.device.deviceType],
  );
  const keySignature = scope?.keys.join('|') ?? '';

  const refresh = useCallback(() => {
    const session = sessionRef.current;
    if (!session) {
      setRetryEpoch((value) => value + 1);
      return;
    }
    void session.refresh().catch((error: unknown) => {
      setSnapshot((current) => ({ ...current, phase: 'error', error: errorValue(error) }));
    });
  }, []);

  useEffect(() => {
    if (!row) {
      sessionRef.current = null;
      setSnapshot(CLOSED);
      return undefined;
    }
    if (!allowed) {
      sessionRef.current = null;
      setSnapshot({ phase: 'not-authorized', state: null, error: null });
      return undefined;
    }
    if (!scope || keySignature.length === 0) {
      sessionRef.current = null;
      setSnapshot({ phase: 'not-configured', state: null, error: null });
      return undefined;
    }

    let scopeGuard: ProtectedScopeRequestToken;
    try {
      scopeGuard = protectedRequestToken();
      if (scopeGuard.siteId !== scope.siteId || scopeGuard.generation !== protectedGeneration) {
        throw new DOMException('Protected Site scope is not current.', 'AbortError');
      }
    } catch (error) {
      setSnapshot({ phase: 'error', state: null, error: errorValue(error) });
      return undefined;
    }

    let active = true;
    let purged = false;
    let session: RealAssetsTelemetryLiveSession | null = null;
    let unsubscribe: (() => void) | null = null;
    const controller = new AbortController();
    const abortFromScope = () => {
      if (!controller.signal.aborted) controller.abort(scopeGuard.signal.reason);
    };
    if (scopeGuard.signal.aborted) abortFromScope();
    else scopeGuard.signal.addEventListener('abort', abortFromScope, { once: true });

    const close = (purge: boolean) => {
      if (!active && !purge) return;
      active = false;
      if (!controller.signal.aborted) controller.abort(new DOMException('Realtime detail closed.', 'AbortError'));
      unsubscribe?.();
      unsubscribe = null;
      session?.close();
      session = null;
      if (sessionRef.current) sessionRef.current = null;
      if (purge) runtime.live.purge();
    };

    const publisher = createBoundedRealtimePublisher<RealAssetsRealtimeState>(
      (callback) => window.requestAnimationFrame(callback),
      (handle) => window.cancelAnimationFrame(handle),
      (nextState) => {
        if (!active) return;
        try {
          validateRealAssetsRealtimeState(nextState, scope);
        } catch (error) {
          close(true);
          scopeGuard.commit(() => setSnapshot({ phase: 'error', state: null, error: errorValue(error) }));
          return;
        }
        const committed = scopeGuard.commit(() => {
          setSnapshot({ phase: 'active', state: nextState, error: null });
        });
        if (!committed) {
          close(false);
          return;
        }
        if (nextState.status === 'revoked') {
          runtime.live.purge();
          onRevoked?.();
        }
      },
    );

    const resource: ProtectedScopeResource = {
      id: `real-assets-realtime:${protectedGeneration}:${scope.deviceId}`,
      kind: 'realtime',
      purge: () => {
        if (purged) return;
        purged = true;
        publisher.cancel();
        close(true);
        setSnapshot({ phase: 'purged', state: null, error: null });
      },
    };
    const unregister = registerProtectedResource(resource);
    setSnapshot({ phase: 'opening', state: null, error: null });

    runtime.live.open([createRealAssetsRealtimeTarget(scope)], { signal: controller.signal }).then((opened) => {
      if (!active || controller.signal.aborted) {
        opened.close();
        return;
      }
      if (!scopeGuard.commit(() => undefined)) {
        opened.close();
        close(false);
        return;
      }
      session = opened;
      sessionRef.current = opened;
      const publish = () => {
        const state = opened.getState(scope.clientSubscriptionId);
        if (!state) {
          publisher.cancel();
          close(true);
          scopeGuard.commit(() => setSnapshot({
            phase: 'error', state: null, error: new Error('Realtime session omitted the exact subscription state'),
          }));
          return;
        }
        publisher.push(state);
      };
      publish();
      unsubscribe = opened.subscribe(publish);
    }).catch((error: unknown) => {
      if (!active || controller.signal.aborted) return;
      scopeGuard.commit(() => setSnapshot({ phase: 'error', state: null, error: errorValue(error) }));
    });

    return () => {
      unregister();
      publisher.cancel();
      scopeGuard.signal.removeEventListener('abort', abortFromScope);
      close(false);
    };
  }, [
    allowed,
    authorizationEpoch,
    keySignature,
    onRevoked,
    protectedGeneration,
    protectedRequestToken,
    registerProtectedResource,
    retryEpoch,
    row?.device.id,
    runtime,
    scope,
  ]);

  return { ...snapshot, refresh };
}
