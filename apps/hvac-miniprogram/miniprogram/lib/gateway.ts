// The one way the mini program talks to the platform Gateway (#664): every request carries the
// Session in the Authorization header, a Session the platform no longer accepts signs the mini
// program out, and failures come back as the platform's problem code.

import { utf8Reader } from './utf8';

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

/** A GET whose body arrives in chunks as it is written (`wx.request` with enableChunked); `done` settles when it ends. */
export type ChunkedTransport = (request: TransportRequest & {
  readonly onHeaders: (statusCode: number) => void;
  readonly onChunk: (chunk: ArrayBuffer) => void;
}) => { readonly done: Promise<void>; readonly abort: () => void };

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
  readonly chunked: ChunkedTransport;
  readonly session: SessionStore;
  readonly onSignedOut: () => void;
}) {
  const { origin, transport, chunked, session, onSignedOut } = options;

  /** The Session goes in the Authorization header of every signed-in request. */
  const signedInHeader = (header: Record<string, string>): Record<string, string> => {
    const token = session.get();
    return token === null ? header : { ...header, Authorization: `Bearer ${token}` };
  };

  const exchange = async (method: 'GET' | 'POST', path: string, data: unknown, signedIn: boolean, extra: Record<string, string> = {}): Promise<TransportResponse> => {
    const base: Record<string, string> = data === undefined ? { Accept: 'application/json', ...extra } : { Accept: 'application/json', 'Content-Type': 'application/json', ...extra };
    const header = signedIn ? signedInHeader(base) : base;
    try {
      return await transport({ url: origin + path, method, header, data });
    } catch {
      throw new GatewayError('NETWORK_UNAVAILABLE', 0);
    }
  };

  /** A refusal as the caller sees it; one that ends the Session also signs the mini program out. */
  const refusal = (statusCode: number, data: unknown): GatewayError => {
    if (statusCode === 401) {
      session.clear();
      onSignedOut();
    }
    return new GatewayError(problemCode(data), statusCode);
  };

  const call = async <T>(method: 'GET' | 'POST', path: string, data?: unknown, headers?: Record<string, string>): Promise<T> => {
    const response = await exchange(method, path, data, true, headers);
    if (response.statusCode < 200 || response.statusCode >= 300) throw refusal(response.statusCode, response.data);
    return response.data as T;
  };

  /**
   * A GET read as it is written, its chunks handed to `onChunk` while the status is a success.
   * A refusal's body is read whole for its problem code instead. `abort` ends it quietly.
   */
  const stream = (path: string, onChunk: (chunk: ArrayBuffer) => void) => {
    const header = signedInHeader({ Accept: 'text/event-stream, application/problem+json' });
    let statusCode = 0;
    let refused = '';
    const decode = utf8Reader();
    let aborted = false;
    const exchanged = chunked({
      url: origin + path,
      method: 'GET',
      header,
      onHeaders: (status) => { statusCode = status; },
      onChunk: (chunk) => {
        if (statusCode >= 200 && statusCode < 300) onChunk(chunk);
        else refused += decode(chunk);
      },
    });
    const done = exchanged.done.then(
      () => {
        if (aborted || (statusCode >= 200 && statusCode < 300)) return;
        let problem: unknown = null;
        try { problem = JSON.parse(refused); } catch { /* not a problem document */ }
        throw refusal(statusCode, problem);
      },
      (error: unknown) => {
        if (aborted) return;
        throw error instanceof GatewayError ? error : new GatewayError('NETWORK_UNAVAILABLE', 0);
      },
    );
    return { done, abort: () => { aborted = true; exchanged.abort(); } };
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
    /** A change; without `data` it sends no body. `headers` adds request headers such as an idempotency key. */
    post: <T>(path: string, data?: unknown, headers?: Record<string, string>) => call<T>('POST', path, data, headers),
    stream,
    signIn: (username: string, password: string) => signInStep('/api/v1/auth/app/sessions', { username, password }),
    completeTotp: (pendingLogin: string, code: string) => signInStep('/api/v1/auth/app/sessions/totp', { pendingLogin, code }),
  };
}

export type Gateway = ReturnType<typeof createGateway>;
