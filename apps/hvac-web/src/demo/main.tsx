import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThemeGate } from '@/theme/AppTheme';
import App from '@/App';
import AiProvider from '@/ai/AiProvider';
import './mode-marker.css';
import '@/global.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeGate>
      <div className="demo-mode-marker" role="status" aria-label={`Demo Mode 非权威演示数据，构建 ${__HVAC_WEB_BUILD_ID__}`}>
        DEMO MODE · 非权威演示数据 · {__HVAC_WEB_BUILD_ID__}
      </div>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AiProvider>
            <App />
          </AiProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeGate>
  </React.StrictMode>,
);
