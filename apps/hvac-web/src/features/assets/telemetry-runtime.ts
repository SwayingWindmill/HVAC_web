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
    options?: { signal?: AbortSignal; csrfToken?: string },
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
  readonly getCsrfToken?: () => string | undefined;
}

function createLazyTelemetryLiveClient(
  telemetry: S2TelemetryClient,
  getCsrfToken?: () => string | undefined,
): AssetsTelemetryLiveClient {
  let loaded: Promise<AssetsTelemetryLiveClient> | null = null;
  const load = () => {
    loaded ??= import('../../platform/telemetry-live/index.ts').then((module) => (
      module.createTelemetryLiveClient({ telemetry, getCsrfToken })
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
  const routeAwareFetch: typeof fetch = Object.assign(
    async (input: RequestInfo | URL, init?: RequestInit) => {
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
    },
    fetchImplementation,
  );
  const client = dependencies.client ?? createS2TelemetryClient(baseURL, routeAwareFetch);
  return {
    client,
    live: dependencies.live ?? createLazyTelemetryLiveClient(client, dependencies.getCsrfToken),
    currentRoutePolicyRevision: () => routePolicyRevision,
    subscribeRoutePolicyChange: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
