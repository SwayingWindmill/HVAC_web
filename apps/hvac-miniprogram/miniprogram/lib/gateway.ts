// The one way the mini program talks to the platform Gateway (#664): every request carries the
// Session in the Authorization header, a Session the platform no longer accepts signs the mini
// program out, and failures come back as the platform's problem code.

export interface TransportRequest {
  readonly url: string;
  readonly method: 'GET' | 'POST';
  readonly header: Record<string, string>;
  readonly data?: unknown;
}

export interface TransportResponse {
  readonly statusCode: number;
  readonly data: unknown;
}

/** One HTTP exchange; `wx.request` in the mini program, a fake in tests. */
export type Transport = (request: TransportRequest) => Promise<TransportResponse>;

export interface SessionStore {
  get(): string | null;
  set(token: string): void;
  clear(): void;
}

export class GatewayError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, status: number) {
    super(code);
    this.code = code;
    this.status = status;
  }
}

export type SignInOutcome =
  | { readonly kind: 'signed-in' }
  | { readonly kind: 'totp'; readonly pendingLogin: string }
  | { readonly kind: 'failed'; readonly message: string };

const SIGN_IN_MESSAGES: Readonly<Record<string, string>> = {
  INVALID_CREDENTIALS: '账号或密码不对。连续输错 5 次，账号会锁 15 分钟。',
  TOTP_INVALID: '验证码不对，请看验证器里最新的 6 位数字。',
  LOGIN_EXPIRED: '登录已过期，请重新输入账号和密码。',
  NETWORK_UNAVAILABLE: '连不上平台，请检查网络后再试。',
};
const SIGN_IN_UNAVAILABLE = '暂时登录不了，请稍后再试。';

const problemCode = (data: unknown): string => (
  typeof data === 'object' && data !== null && typeof (data as { code?: unknown }).code === 'string' ? (data as { code: string }).code : 'UNKNOWN'
);

export function createGateway(options: {
  readonly origin: string;
  readonly transport: Transport;
  readonly session: SessionStore;
  readonly onSignedOut: () => void;
}) {
  const { origin, transport, session, onSignedOut } = options;

  const exchange = async (method: 'GET' | 'POST', path: string, data: unknown, signedIn: boolean): Promise<TransportResponse> => {
    const header: Record<string, string> = { Accept: 'application/json' };
    if (data !== undefined) header['Content-Type'] = 'application/json';
    const token = session.get();
    if (signedIn && token !== null) header.Authorization = `Bearer ${token}`;
    try {
      return await transport({ url: origin + path, method, header, data });
    } catch {
      throw new GatewayError('NETWORK_UNAVAILABLE', 0);
    }
  };

  const call = async <T>(method: 'GET' | 'POST', path: string, data?: unknown): Promise<T> => {
    const response = await exchange(method, path, data, true);
    if (response.statusCode === 401) {
      session.clear();
      onSignedOut();
    }
    if (response.statusCode < 200 || response.statusCode >= 300) throw new GatewayError(problemCode(response.data), response.statusCode);
    return response.data as T;
  };

  const signInStep = async (path: string, body: unknown): Promise<SignInOutcome> => {
    let response: TransportResponse;
    try {
      response = await exchange('POST', path, body, false);
    } catch (error) {
      return { kind: 'failed', message: SIGN_IN_MESSAGES[(error as GatewayError).code] ?? SIGN_IN_UNAVAILABLE };
    }
    const data = response.data as { sessionToken?: string; pendingLogin?: string };
    if (response.statusCode === 201 && typeof data.sessionToken === 'string') {
      session.set(data.sessionToken);
      return { kind: 'signed-in' };
    }
    if (response.statusCode === 202 && typeof data.pendingLogin === 'string') return { kind: 'totp', pendingLogin: data.pendingLogin };
    return { kind: 'failed', message: SIGN_IN_MESSAGES[problemCode(response.data)] ?? SIGN_IN_UNAVAILABLE };
  };

  return {
    get: <T>(path: string) => call<T>('GET', path),
    post: <T>(path: string, data: unknown) => call<T>('POST', path, data),
    signIn: (username: string, password: string) => signInStep('/api/v1/auth/app/sessions', { username, password }),
    completeTotp: (pendingLogin: string, code: string) => signInStep('/api/v1/auth/app/sessions/totp', { pendingLogin, code }),
  };
}

export type Gateway = ReturnType<typeof createGateway>;
