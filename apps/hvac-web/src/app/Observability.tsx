import { createContext, useContext, useEffect, useMemo, useRef, type ReactNode } from 'react';

export type ObservabilityEvent = {
  name: 'page_load' | 'route_change' | 'runtime_error' | 'unhandled_rejection' | 'realtime_state';
  at: string;
  fields?: Readonly<Record<string, string | number | boolean | undefined>>;
};

export interface Observability {
  record: (event: Omit<ObservabilityEvent, 'at'>) => void;
  recent: () => readonly ObservabilityEvent[];
}

const ObservabilityContext = createContext<Observability | null>(null);

export function ObservabilityProvider({ children }: { children: ReactNode }) {
  const value = useMemo<Observability>(() => {
    const events: ObservabilityEvent[] = [];
    const record = (event: Omit<ObservabilityEvent, 'at'>) => {
      const next = { ...event, at: new Date().toISOString() };
      events.push(next);
      if (events.length > 100) events.shift();
      globalThis.dispatchEvent(new CustomEvent('hvac:real-observability', { detail: next }));
    };
    return { record, recent: () => [...events] };
  }, []);

  const initialPathRef = useRef(window.location.pathname);

  useEffect(() => {
    value.record({ name: 'page_load', fields: { path: initialPathRef.current } });
    const onError = () => value.record({ name: 'runtime_error', fields: { path: window.location.pathname } });
    const onRejection = () => value.record({ name: 'unhandled_rejection', fields: { path: window.location.pathname } });
    window.addEventListener('error', onError);
    window.addEventListener('unhandledrejection', onRejection);
    return () => {
      window.removeEventListener('error', onError);
      window.removeEventListener('unhandledrejection', onRejection);
    };
  }, [value]);

  return <ObservabilityContext.Provider value={value}>{children}</ObservabilityContext.Provider>;
}

export function useObservability(): Observability {
  const value = useContext(ObservabilityContext);
  if (!value) throw new Error('useObservability must be used inside ObservabilityProvider');
  return value;
}
