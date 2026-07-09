import { useUi } from '@/store/ui';
import { USE_MOCK } from './config';

/** X-Site-Id analogue — the current global building context from the UI store. */
export const getSiteId = (): string => useUi.getState().buildingId;

/**
 * JWT for the WS handshake / REST Authorization header.
 * Mock mode has no auth; real mode reads the persisted token.
 */
export const getToken = (): string | null => {
  if (USE_MOCK) return null;
  return localStorage.getItem('hvac_token');
};
