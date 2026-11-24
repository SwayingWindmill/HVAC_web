export type LoginContextResponse =
  | { status: 'ready'; step: 'password' | 'totp'; csrfToken: string; error?: string }
  | { status: 'expired' }
  | { status: 'none' }
  | { status: 'unavailable' };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

export function parseLoginContext(payload: unknown): LoginContextResponse {
  if (!isRecord(payload) || typeof payload.status !== 'string') {
    return { status: 'unavailable' };
  }
  if (payload.status === 'expired') {
    return { status: 'expired' };
  }
  if (payload.status === 'none') {
    return { status: 'none' };
  }
  if (payload.status === 'ready') {
    const csrfToken = typeof payload.csrfToken === 'string' ? payload.csrfToken : '';
    const step = payload.step === 'totp' ? 'totp' : payload.step === 'password' ? 'password' : null;
    if (csrfToken === '' || step === null) {
      return { status: 'unavailable' };
    }
    const error = typeof payload.error === 'string' && payload.error !== '' ? payload.error : undefined;
    return { status: 'ready', step, csrfToken, error };
  }
  return { status: 'unavailable' };
}

let cachedLoginContextPromise: Promise<LoginContextResponse> | null = null;

export function fetchLoginContext(): Promise<LoginContextResponse> {
  if (cachedLoginContextPromise === null) {
    cachedLoginContextPromise = (async (): Promise<LoginContextResponse> => {
      try {
        const response = await fetch('/identity/login-context', {
          method: 'GET',
          credentials: 'same-origin',
          headers: { Accept: 'application/json' },
          cache: 'no-store',
        });
        const contentType = response.headers.get('content-type') ?? '';
        if (!response.ok && !contentType.includes('application/json')) {
          return { status: 'unavailable' };
        }
        const payload: unknown = await response.json();
        return parseLoginContext(payload);
      } catch {
        return { status: 'unavailable' };
      }
    })();
  }
  return cachedLoginContextPromise;
}
