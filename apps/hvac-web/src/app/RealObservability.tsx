import { createContext, useContext, useEffect, useMemo, useRef, type ReactNode } from 'react';
import { useLocation } from 'react-router';

export type RealObservabilityEvent = {
  name: 'page_load' | 'route_change' | 'runtime_error' | 'unhandled_rejection' | 'realtime_state';
  at: string;
  fields?: Readonly<Record<string, string | number | boolean | undefined>>;
};

export interface RealObservability {
  record: (event: Omit<RealObservabilityEvent, 'at'>) => void;
  recent: () => readonly RealObservabilityEvent[];
}

const RealObservabilityContext = createContext<RealObservability | null>(null);

export function RealObservabilityProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const value = useMemo<RealObservability>(() => {
    const events: RealObservabilityEvent[] = [];
    const record = (event: Omit<RealObservabilityEvent, 'at'>) => {
      const next = { ...event, at: new Date().toISOString() };
      events.push(next);
      if (events.length > 100) events.shift();
      globalThis.dispatchEvent(new CustomEvent('hvac:real-observability', { detail: next }));
    };
    return { record, recent: () => [...events] };
  }, []);

  const initialPathRef = useRef(location.pathname);
  const currentPathRef = useRef(location.pathname);
  currentPathRef.current = location.pathname;

  useEffect(() => {
    value.record({ name: 'page_load', fields: { path: initialPathRef.current } });
    const onError = () => value.record({ name: 'runtime_error', fields: { path: currentPathRef.current } });
    const onRejection = () => value.record({ name: 'unhandled_rejection', fields: { path: currentPathRef.current } });
    window.addEventListener('error', onError);
    window.addEventListener('unhandledrejection', onRejection);
    return () => {
      window.removeEventListener('error', onError);
      window.removeEventListener('unhandledrejection', onRejection);
    };
  }, [value]);

  useEffect(() => {
    value.record({ name: 'route_change', fields: { path: location.pathname } });
  }, [location.pathname, location.search, value]);

  return <RealObservabilityContext.Provider value={value}>{children}</RealObservabilityContext.Provider>;
}

export function useRealObservability(): RealObservability {
  const value = useContext(RealObservabilityContext);
  if (!value) throw new Error('useRealObservability must be used inside RealObservabilityProvider');
  return value;
}
