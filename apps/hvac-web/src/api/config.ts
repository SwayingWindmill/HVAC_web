// The split Real entry always uses authoritative APIs; Demo remains mock-first.
// VITE_API_MODE=real is retained for compatibility runs and isolated Vite fixtures.
const isRealBuild = typeof __HVAC_WEB_BUILD_TARGET__ !== 'undefined'
  && __HVAC_WEB_BUILD_TARGET__ === 'real';

export const API_MODE: 'mock' | 'real' =
  isRealBuild || (import.meta.env.VITE_API_MODE as string | undefined) === 'real'
    ? 'real'
    : 'mock';
export const USE_MOCK = API_MODE === 'mock';

export const WS_PATH = '/ws/telemetry';
export const REST_BASE = '/api/v1';
