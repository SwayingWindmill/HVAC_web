// Mock-first: hvac-backend is not running in this dev environment.
// Set VITE_API_MODE=real (or flip the literal below) once the backend is live.
export const API_MODE: 'mock' | 'real' =
  (import.meta.env.VITE_API_MODE as string | undefined) === 'real' ? 'real' : 'mock';
export const USE_MOCK = API_MODE === 'mock';

export const WS_PATH = '/ws/telemetry';
export const REST_BASE = '/api/v1';
