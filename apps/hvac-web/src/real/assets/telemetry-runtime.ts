import {
  createS2TelemetryClient,
  type S2TelemetryClient,
} from '../../api/generated/s2Telemetry.gen.ts';

export interface RealAssetsTelemetryRuntime {
  readonly client: S2TelemetryClient;
  currentRoutePolicyRevision(): string | null;
  subscribeRoutePolicyChange(listener: (previousRevision: string, nextRevision: string) => void): () => void;
}

export function createRealAssetsTelemetryRuntime(
  baseURL = '',
  fetchImplementation: typeof fetch = globalThis.fetch.bind(globalThis),
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
  return {
    client: createS2TelemetryClient(baseURL, routeAwareFetch),
    currentRoutePolicyRevision: () => routePolicyRevision,
    subscribeRoutePolicyChange: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
