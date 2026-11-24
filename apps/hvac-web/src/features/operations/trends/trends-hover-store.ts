export interface TrendsHoverStore {
  getSnapshot(): number | null;
  setHoverAt(atMs: number | null): void;
  subscribe(listener: () => void): () => void;
}

/**
 * Shared hover timestamp store for Trends panels so pointer motion only updates
 * small header readouts and hairline overlays without re-rendering Recharts SVG trees.
 */
export function createTrendsHoverStore(): TrendsHoverStore {
  let current: number | null = null;
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => current,
    setHoverAt: (atMs: number | null) => {
      const normalized = atMs !== null && Number.isFinite(atMs) ? atMs : null;
      if (normalized === current) return;
      current = normalized;
      for (const listener of listeners) listener();
    },
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}
