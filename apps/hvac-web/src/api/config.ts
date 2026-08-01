declare const __HVAC_WEB_BUILD_TARGET__: 'demo' | 'real' | undefined;

const buildTarget = typeof __HVAC_WEB_BUILD_TARGET__ === 'undefined'
  ? undefined
  : __HVAC_WEB_BUILD_TARGET__;

// Real builds always use authoritative APIs. Demo remains mock-first, while
// VITE_API_MODE=real is retained for local integration profiles.
export const API_MODE: 'mock' | 'real' = buildTarget === 'real'
  || import.meta.env.VITE_API_MODE === 'real'
  ? 'real'
  : 'mock';
export const USE_MOCK = API_MODE === 'mock';

export const WS_PATH = '/ws/telemetry';
export const REST_BASE = '/api/v1';
