import { createContext, useCallback, useContext, useSyncExternalStore, type ReactNode } from 'react';
import type { ShellRuntime, ShellSnapshot } from './shell-runtime';

const ShellRuntimeContext = createContext<ShellRuntime | null>(null);

export function ShellRuntimeProvider({ runtime, children }: { runtime: ShellRuntime; children: ReactNode }) {
  return <ShellRuntimeContext.Provider value={runtime}>{children}</ShellRuntimeContext.Provider>;
}

export function useShellRuntime(): ShellRuntime {
  const runtime = useContext(ShellRuntimeContext);
  if (!runtime) throw new Error('useShellRuntime must be used inside ShellRuntimeProvider.');
  return runtime;
}

export function useShellSnapshot(): ShellSnapshot {
  const runtime = useShellRuntime();
  const subscribe = useCallback((listener: () => void) => runtime.subscribe(listener), [runtime]);
  const getSnapshot = useCallback(() => runtime.current(), [runtime]);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
