import ReactDOM from 'react-dom/client';
import { QueryClient } from '@tanstack/react-query';
import { RootErrorBoundary } from '@/app/RootErrorBoundary';
import { ObservabilityProvider } from '@/app/Observability';
import { TooltipProvider } from '@/components/ui/tooltip';
import { AppRuntimeHost, ConfigurationBlocked } from './AppRuntimeHost';
import { ThemeGate } from './ThemeGate';
import { validateRuntimeConfig } from './runtime-config';
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import '@/global.css';

const PRELOAD_RELOAD_KEY = 'hvac:vite-preload-reload-at';
const PRELOAD_RELOAD_COOLDOWN_MS = 30_000;

if (typeof window !== 'undefined') {
  window.addEventListener('vite:preloadError', (event) => {
    try {
      const now = Date.now();
      const last = Number(sessionStorage.getItem(PRELOAD_RELOAD_KEY) ?? '0');
      if (!Number.isFinite(last) || now - last > PRELOAD_RELOAD_COOLDOWN_MS) {
        sessionStorage.setItem(PRELOAD_RELOAD_KEY, String(now));
        event.preventDefault();
        window.location.reload();
      }
    } catch {
      // Ignore storage failures and let RouteErrorSurface present the reload card.
    }
  });
}

const runtimeConfig = validateRuntimeConfig();
const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, staleTime: 15_000 } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <ThemeGate queryClient={queryClient}>
    <TooltipProvider>
      <ObservabilityProvider>
        <RootErrorBoundary>
          {runtimeConfig.ok ? (
            <AppRuntimeHost config={runtimeConfig.config} queryClient={queryClient} />
          ) : (
            <ConfigurationBlocked failures={runtimeConfig.failures} />
          )}
        </RootErrorBoundary>
      </ObservabilityProvider>
    </TooltipProvider>
  </ThemeGate>,
);
