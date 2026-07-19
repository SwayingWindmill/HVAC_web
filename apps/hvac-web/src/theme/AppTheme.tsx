import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { ConfigProvider, App as AntApp, theme, type ThemeConfig } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BRAND } from './tokens';
import { useUi } from '@/store/ui';

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 30_000, refetchOnWindowFocus: false } },
});

function useResolvedMode(): 'light' | 'dark' {
  const mode = useUi((s) => s.themeMode);
  const [system, setSystem] = useState<'light' | 'dark'>(
    () => (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'),
  );
  useEffect(() => {
    if (mode !== 'system') return;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const on = () => setSystem(mq.matches ? 'dark' : 'light');
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, [mode]);
  return mode === 'system' ? system : mode;
}

// Single brand accent (teal) + locked radii + contrast-safe token set (prototype D language).
function buildTheme(mode: 'light' | 'dark'): ThemeConfig {
  const algorithm = mode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm;
  return {
    algorithm,
    token: {
      colorPrimary: BRAND.teal,
      borderRadius: 10,
      fontSize: 14,
      colorBgLayout: mode === 'dark' ? '#0f1419' : '#f5f7f9',
    },
    components: {
      Layout: { headerBg: mode === 'dark' ? '#161b22' : '#ffffff', siderBg: mode === 'dark' ? '#11161d' : '#ffffff' },
      Menu: { itemBorderRadius: 8, itemHeight: 40 },
      Card: { borderRadiusLG: 12 },
    },
  };
}

export function ThemeGate({ children }: { children: ReactNode }) {
  const mode = useResolvedMode();
  const cfg = useMemo(() => buildTheme(mode), [mode]);

  useEffect(() => {
    document.documentElement.dataset.theme = mode;
  }, [mode]);

  return (
    <QueryClientProvider client={queryClient}>
      <ConfigProvider theme={cfg}>
        <AntApp>{children}</AntApp>
      </ConfigProvider>
    </QueryClientProvider>
  );
}
