import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

export type ThemeMode = 'light' | 'dark' | 'system';

const THEME_STORAGE_KEY = 'hvac-theme-mode';

type ThemeContextValue = {
  mode: ThemeMode;
  resolvedMode: 'light' | 'dark';
  setMode: (mode: ThemeMode) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

function readStoredThemeMode(): ThemeMode {
  try {
    const raw = window.localStorage.getItem(THEME_STORAGE_KEY);
    return raw === 'light' || raw === 'dark' ? raw : 'system';
  } catch {
    return 'system';
  }
}

function writeStoredThemeMode(mode: ThemeMode): void {
  try {
    if (mode === 'system') window.localStorage.removeItem(THEME_STORAGE_KEY);
    else window.localStorage.setItem(THEME_STORAGE_KEY, mode);
  } catch {
    // Theme switching still works in memory when storage is unavailable.
  }
}

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
  const [mode, setModeState] = useState<ThemeMode>(readStoredThemeMode);
  const [systemMode, setSystemMode] = useState<'light' | 'dark'>(systemTheme);
  const mountedRef = useRef(false);

  const setMode = useCallback((next: ThemeMode) => {
    setModeState(next);
    writeStoredThemeMode(next);
  }, []);

  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key !== THEME_STORAGE_KEY) return;
      setModeState(event.newValue === 'light' || event.newValue === 'dark' ? event.newValue : 'system');
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  useEffect(() => {
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => setSystemMode(query.matches ? 'dark' : 'light');
    update();
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);

  const resolvedMode = mode === 'system' ? systemMode : mode;
  const contextValue = useMemo<ThemeContextValue>(() => ({
    mode,
    resolvedMode,
    setMode,
  }), [mode, resolvedMode, setMode]);

  useEffect(() => {
    const root = document.documentElement;
    const isThemeSwitch = mountedRef.current;
    mountedRef.current = true;
    let frameId: number | null = null;
    if (isThemeSwitch) {
      root.classList.add('theme-switching');
      void root.offsetHeight;
    }
    root.dataset.theme = resolvedMode;
    root.classList.toggle('dark', resolvedMode === 'dark');
    document.querySelector('meta[name="color-scheme"]')?.setAttribute(
      'content',
      mode === 'system' ? 'light dark' : mode,
    );
    if (isThemeSwitch) {
      frameId = window.requestAnimationFrame(() => {
        root.classList.remove('theme-switching');
      });
    }
    return () => {
      if (frameId !== null) window.cancelAnimationFrame(frameId);
      root.classList.remove('theme-switching');
    };
  }, [mode, resolvedMode]);

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
