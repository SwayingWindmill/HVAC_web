import {
  createS2TelemetryClient,
  type S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';
import type {
  RealAssetsRealtimeState,
  RealAssetsRealtimeTarget,
} from './realtime.ts';

export interface RealAssetsTelemetryLiveSession {
  getState(clientSubscriptionId: string): RealAssetsRealtimeState | undefined;
  getStates(): ReadonlyArray<RealAssetsRealtimeState>;
  subscribe(listener: (states: ReadonlyArray<RealAssetsRealtimeState>) => void): () => void;
  refresh(): Promise<void>;
  checkpoint(): Promise<void>;
  close(): void;
}

export interface RealAssetsTelemetryLiveClient {
  open(
    targets: ReadonlyArray<RealAssetsRealtimeTarget>,
    options?: { signal?: AbortSignal },
  ): Promise<RealAssetsTelemetryLiveSession>;
  purge(): void;
}

export interface RealAssetsTelemetryRuntime {
  readonly client: S2TelemetryClient;
  readonly live: RealAssetsTelemetryLiveClient;
  currentRoutePolicyRevision(): string | null;
  subscribeRoutePolicyChange(listener: (previousRevision: string, nextRevision: string) => void): () => void;
}

export interface RealAssetsTelemetryRuntimeDependencies {
  readonly client?: S2TelemetryClient;
  readonly live?: RealAssetsTelemetryLiveClient;
}

function createLazyTelemetryLiveClient(telemetry: S2TelemetryClient): RealAssetsTelemetryLiveClient {
  let loaded: Promise<RealAssetsTelemetryLiveClient> | null = null;
  const load = () => {
    loaded ??= import('../../platform/telemetry-live/index.ts').then((module) => (
      module.createTelemetryLiveClient({ telemetry }) as RealAssetsTelemetryLiveClient
    ));
    return loaded;
  };
  return {
    open: async (targets, options) => (await load()).open(targets, options),
    purge: () => {
      if (loaded) void loaded.then((client) => client.purge(), () => undefined);
    },
  };
}

export function createRealAssetsTelemetryRuntime(
  baseURL = '',
  fetchImplementation: typeof fetch = globalThis.fetch.bind(globalThis),
  dependencies: RealAssetsTelemetryRuntimeDependencies = {},
): RealAssetsTelemetryRuntime {
  let routePolicyRevision: string | null = null;
  const listeners = new Set<(previousRevision: string, nextRevision: string) => void>();
  const routeAwareFetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const response = await fetchImplementation(input, init);
    const nextRevision = response.headers.get('x-route-policy-revision');
    if (nextRevision) {
      const previousRevision = routePolicyRevision;
      routePolicyRevision = nextRevision;
      if (previousRevision && previousRevision !== nextRevision) {
        globalThis.queueMicrotask(() => {
          for (const listener of listeners) listener(previousRevision, nextRevision);
        });
      }
    }
    return response;
  }) as typeof fetch;
  const client = dependencies.client ?? createS2TelemetryClient(baseURL, routeAwareFetch);
  return {
    client,
    live: dependencies.live ?? createLazyTelemetryLiveClient(client),
    currentRoutePolicyRevision: () => routePolicyRevision,
    subscribeRoutePolicyChange: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
