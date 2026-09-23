import ReactDOM from 'react-dom/client';
import { QueryClient } from '@tanstack/react-query';
import { RootErrorBoundary } from '@/app/RootErrorBoundary';
import { ObservabilityProvider } from '@/app/Observability';
import { AppRuntimeHost, ConfigurationBlocked } from './AppRuntimeHost';
import { ThemeGate } from './ThemeGate';
import { validateRuntimeConfig } from './runtime-config';
import '@/global.css';

const runtimeConfig = validateRuntimeConfig();
const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <ThemeGate queryClient={queryClient}>
    <ObservabilityProvider>
      <RootErrorBoundary>
        {runtimeConfig.ok ? (
          <AppRuntimeHost config={runtimeConfig.config} queryClient={queryClient} />
        ) : (
          <ConfigurationBlocked failures={runtimeConfig.failures} />
        )}
      </RootErrorBoundary>
    </ObservabilityProvider>
  </ThemeGate>,
);
