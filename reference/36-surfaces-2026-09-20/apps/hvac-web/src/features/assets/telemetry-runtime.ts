import {
  createS2TelemetryClient,
  type S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';
import type {
  AssetsRealtimeState,
  AssetsRealtimeTarget,
} from './realtime.ts';

export interface AssetsTelemetryLiveSession {
  getState(clientSubscriptionId: string): AssetsRealtimeState | undefined;
  getStates(): ReadonlyArray<AssetsRealtimeState>;
  subscribe(listener: (states: ReadonlyArray<AssetsRealtimeState>) => void): () => void;
  refresh(): Promise<void>;
  checkpoint(): Promise<void>;
  close(): void;
}

export interface AssetsTelemetryLiveClient {
  open(
    targets: ReadonlyArray<AssetsRealtimeTarget>,
    options?: { signal?: AbortSignal },
  ): Promise<AssetsTelemetryLiveSession>;
  purge(): void;
}

export interface AssetsTelemetryRuntime {
  readonly client: S2TelemetryClient;
  readonly live: AssetsTelemetryLiveClient;
  currentRoutePolicyRevision(): string | null;
  subscribeRoutePolicyChange(listener: (previousRevision: string, nextRevision: string) => void): () => void;
}

export interface AssetsTelemetryRuntimeDependencies {
  readonly client?: S2TelemetryClient;
  readonly live?: AssetsTelemetryLiveClient;
}

function createLazyTelemetryLiveClient(telemetry: S2TelemetryClient): AssetsTelemetryLiveClient {
  let loaded: Promise<AssetsTelemetryLiveClient> | null = null;
  const load = () => {
    loaded ??= import('../../platform/telemetry-live/index.ts').then((module) => (
      module.createTelemetryLiveClient({ telemetry }) as AssetsTelemetryLiveClient
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

export function createAssetsTelemetryRuntime(
  baseURL = '',
  fetchImplementation: typeof fetch = globalThis.fetch.bind(globalThis),
  dependencies: AssetsTelemetryRuntimeDependencies = {},
): AssetsTelemetryRuntime {
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
