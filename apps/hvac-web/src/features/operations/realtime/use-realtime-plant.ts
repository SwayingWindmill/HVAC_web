import { useLayoutEffect } from 'react';
import { useSiteRealtimeContext, type PlantLiveMode } from './site-realtime-context';

export type { PlantLiveMode };

/** Acquires the shared Site realtime + snapshot session for as long as the calling view stays mounted. */
export function useRealtimePlant() {
  const { acquire, value } = useSiteRealtimeContext();
  useLayoutEffect(() => acquire(), [acquire]);
  return value;
}
