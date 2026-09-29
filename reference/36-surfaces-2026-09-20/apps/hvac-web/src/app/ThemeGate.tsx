import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

export type ThemeMode = 'light' | 'dark' | 'system';

type ThemeContextValue = {
  mode: ThemeMode;
  resolvedMode: 'light' | 'dark';
  setMode: (mode: ThemeMode) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

function systemTheme(): 'light' | 'dark' {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light';
}

export function ThemeGate({
  children,
  queryClient,
}: {
  children: ReactNode;
  queryClient: QueryClient;
}) {
  const [mode, setMode] = useState<ThemeMode>('system');
  const [systemMode, setSystemMode] = useState<'light' | 'dark'>(systemTheme);

  useEffect(() => {
    if (mode !== 'system') return;
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => setSystemMode(query.matches ? 'dark' : 'light');
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, [mode]);

  const resolvedMode = mode === 'system' ? systemMode : mode;
  const contextValue = useMemo<ThemeContextValue>(() => ({
    mode,
    resolvedMode,
    setMode,
  }), [mode, resolvedMode]);

  useEffect(() => {
    document.documentElement.dataset.theme = resolvedMode;
    document.documentElement.classList.toggle('dark', resolvedMode === 'dark');
  }, [resolvedMode]);

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeContext.Provider value={contextValue}>
        {children}
      </ThemeContext.Provider>
    </QueryClientProvider>
  );
}

export function useAppTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (!value) throw new Error('useAppTheme must be used within ThemeGate');
  return value;
}
