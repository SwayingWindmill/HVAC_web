import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import RealAssets from '@/pages/Assets/RealAssets';
import { createTelemetryCurrentRuntime } from '@/api/telemetry-current';
import { ControlledTelemetryLiveClient, type BrowserLiveMode } from './support';
import '@/global.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 0,
      retry: false,
      refetchOnWindowFocus: false,
    },
  },
});
const controlledLive = new ControlledTelemetryLiveClient();
const runtime = createTelemetryCurrentRuntime({ live: controlledLive });

const cachedDeviceIds = (): string[] => {
  const ids = new Set<string>();
  for (const query of queryClient.getQueryCache().findAll({ queryKey: ['s2-current'] })) {
    const data = query.state.data as { items?: Array<{ deviceId?: string }> } | undefined;
    for (const item of data?.items ?? []) if (item.deviceId) ids.add(item.deviceId);
  }
  return [...ids].sort();
};

const control = {
  setMode(mode: BrowserLiveMode) {
    controlledLive.setMode(mode);
  },
  routeCohortChanged() {
    runtime.routePolicy.observe('fixture-route-revision-10');
    return cachedDeviceIds();
  },
  async refreshRegistry() {
    await queryClient.invalidateQueries({ queryKey: ['registry'] });
  },
  telemetryCacheCount() {
    return queryClient.getQueryCache().findAll({ queryKey: ['s2-current'] })
      .filter((query) => query.state.data !== undefined).length;
  },
  cachedDeviceIds,
  audit() {
    return structuredClone(controlledLive.audit);
  },
};

declare global {
  interface Window {
    __S2_HVAC_WEB_CONTROL__: typeof control;
  }
}

window.__S2_HVAC_WEB_CONTROL__ = control;

ReactDOM.createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <BrowserRouter>
      <RealAssets telemetryRuntime={runtime} />
    </BrowserRouter>
  </QueryClientProvider>,
);
