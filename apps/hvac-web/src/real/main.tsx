import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import RealApp, { RealConfigurationBlocked } from './RealApp';
import { validateRealRuntimeConfig } from './runtime-config';
import '@/global.css';

const runtimeConfig = validateRealRuntimeConfig();
const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    {runtimeConfig.ok ? (
      <RealApp config={runtimeConfig.config} />
    ) : (
      <RealConfigurationBlocked failures={runtimeConfig.failures} />
    )}
  </QueryClientProvider>,
);
