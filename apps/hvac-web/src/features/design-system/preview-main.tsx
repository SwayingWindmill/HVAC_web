import ReactDOM from 'react-dom/client';
import { QueryClient } from '@tanstack/react-query';
import { ThemeGate } from '@/app/ThemeGate';
import { UiPatternGallery } from './UiPatternGallery';
import '@/global.css';

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <ThemeGate queryClient={queryClient}>
    <UiPatternGallery />
  </ThemeGate>,
);
