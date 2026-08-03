import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { App as AntApp, ConfigProvider, theme, type ThemeConfig } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BRAND } from '@/theme/tokens';

export type RealThemeMode = 'light' | 'dark' | 'system';

type RealThemeContextValue = {
  mode: RealThemeMode;
  resolvedMode: 'light' | 'dark';
  setMode: (mode: RealThemeMode) => void;
};

const RealThemeContext = createContext<RealThemeContextValue | null>(null);

function systemTheme(): 'light' | 'dark' {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light';
}

function buildTheme(mode: 'light' | 'dark'): ThemeConfig {
  return {
    algorithm: mode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
    cssVar: { prefix: 'ant' },
    token: {
      colorPrimary: BRAND.teal,
      borderRadius: 10,
      fontSize: 14,
      colorBgLayout: mode === 'dark' ? '#0f1419' : '#f5f7f9',
    },
    components: {
      Layout: {
        headerBg: mode === 'dark' ? '#161b22' : '#ffffff',
        siderBg: mode === 'dark' ? '#11161d' : '#ffffff',
      },
      Menu: { itemBorderRadius: 8, itemHeight: 40 },
      Card: { borderRadiusLG: 12 },
    },
  };
}

export function RealThemeGate({
  children,
  queryClient,
}: {
  children: ReactNode;
  queryClient: QueryClient;
}) {
  const [mode, setMode] = useState<RealThemeMode>('system');
  const [systemMode, setSystemMode] = useState<'light' | 'dark'>(systemTheme);

  useEffect(() => {
    if (mode !== 'system') return;
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => setSystemMode(query.matches ? 'dark' : 'light');
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, [mode]);

  const resolvedMode = mode === 'system' ? systemMode : mode;
  const themeConfig = useMemo(() => buildTheme(resolvedMode), [resolvedMode]);
  const contextValue = useMemo<RealThemeContextValue>(() => ({
    mode,
    resolvedMode,
    setMode,
  }), [mode, resolvedMode]);

  useEffect(() => {
    document.documentElement.dataset.theme = resolvedMode;
  }, [resolvedMode]);

  return (
    <QueryClientProvider client={queryClient}>
      <RealThemeContext.Provider value={contextValue}>
        <ConfigProvider theme={themeConfig}>
          <AntApp>{children}</AntApp>
        </ConfigProvider>
      </RealThemeContext.Provider>
    </QueryClientProvider>
  );
}

export function useRealTheme(): RealThemeContextValue {
  const value = useContext(RealThemeContext);
  if (!value) throw new Error('useRealTheme must be used within RealThemeGate');
  return value;
}
