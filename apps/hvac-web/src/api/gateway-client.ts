/**
 * One request layer for the Gateway's hand-written feature reads and writes. It sends the session
 * cookie, the CSRF token on a write and asks for problem+json, then sorts every answer into the few
 * outcomes a page acts on, so no feature has to read status codes itself:
 *
 * - `notPublished`: 404 RESOURCE_NOT_FOUND, the owner has nothing for this Site yet;
 * - `routeMissing`: 404 ROUTE_NOT_FOUND, or a 404 with no problem, this deployment does not serve
 *   the route at all (its owner is not configured or not rolled out), which must not read as "nothing yet";
 * - `refused`: the Gateway or the owner understood the request and declined it (4xx, or a 5xx
 *   whose code ends in _REJECTED), carrying its problem and whether to try again later;
 * - `unavailable`: no usable answer: the network failed, a 5xx, or a body that is not the JSON promised.
 *
 * Free of app imports so the plain Node tests run it as it ships.
 */

export interface GatewayProblem {
  readonly status: number;
  readonly code?: string;
  readonly title?: string;
  readonly detail?: string;
  readonly retryable?: boolean;
}

export type GatewayResult<T> =
  | { readonly kind: 'ok'; readonly value: T }
  | { readonly kind: 'notPublished'; readonly problem: GatewayProblem }
  | { readonly kind: 'routeMissing'; readonly status: number }
  | { readonly kind: 'refused'; readonly problem: GatewayProblem }
  | { readonly kind: 'unavailable'; readonly status: number | null };

export type GatewayFailure = Exclude<GatewayResult<unknown>, { kind: 'ok' }>;

type Read = {
  readonly method?: 'GET';
  readonly csrfToken?: undefined;
  readonly body?: undefined;
};
type Write = {
  readonly method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /** Every write carries the session's CSRF token. */
  readonly csrfToken: string;
  readonly body?: unknown;
};

export type GatewayRequest<T> = (Read | Write) & {
  readonly signal?: AbortSignal;
  /** Turns the 2xx JSON body into the feature's value; throwing makes the answer `unavailable`. */
  readonly parse?: (payload: unknown) => T;
  readonly fetch?: typeof fetch;
};

const ACCEPT = 'application/json, application/problem+json';

export async function gatewayRequest<T>(path: string, request: GatewayRequest<T> = {}): Promise<GatewayResult<T>> {
  const headers: Record<string, string> = { Accept: ACCEPT };
  if (request.csrfToken !== undefined) headers['X-CSRF-Token'] = request.csrfToken;
  if (request.body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await (request.fetch ?? fetch)(path, {
      method: request.method ?? 'GET',
      credentials: 'same-origin',
      headers,
      body: request.body === undefined ? undefined : JSON.stringify(request.body),
      signal: request.signal,
    });
  } catch (error) {
    // A cancelled query is not an outage; let the caller's cancellation through.
    if (request.signal?.aborted) throw error;
    return { kind: 'unavailable', status: null };
  }
  const text = await response.text().catch(() => null);
  const payload = parseJSON(text);
  if (response.ok) {
    if (response.status === 204 || text === '') return { kind: 'ok', value: undefined as T };
    if (payload === undefined) return { kind: 'unavailable', status: response.status };
    try {
      return { kind: 'ok', value: request.parse ? request.parse(payload) : (payload as T) };
    } catch {
      return { kind: 'unavailable', status: response.status };
    }
  }
  return classifyFailure(response.status, problemOf(response.status, payload));
}

export function classifyFailure(status: number, problem: GatewayProblem | null): GatewayFailure {
  if (status === 404) {
    if (problem?.code === 'RESOURCE_NOT_FOUND') return { kind: 'notPublished', problem };
    if (!problem?.code || problem.code === 'ROUTE_NOT_FOUND') return { kind: 'routeMissing', status };
    return { kind: 'refused', problem };
  }
  if (status >= 400 && status < 500) return { kind: 'refused', problem: problem ?? { status } };
  if (problem?.code?.endsWith('_REJECTED')) return { kind: 'refused', problem };
  return { kind: 'unavailable', status };
}

function parseJSON(text: string | null): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return undefined;
  }
}

function problemOf(status: number, payload: unknown): GatewayProblem | null {
  if (payload === null || typeof payload !== 'object') return null;
  const body = payload as Record<string, unknown>;
  if (typeof body.code !== 'string') return null;
  return {
    status,
    code: body.code,
    title: typeof body.title === 'string' ? body.title : undefined,
    detail: typeof body.detail === 'string' ? body.detail : undefined,
    retryable: typeof body.retryable === 'boolean' ? body.retryable : undefined,
  };
}

export const ROUTE_MISSING_MESSAGE = '此功能尚未在服务器上启用';

/** A failed Gateway answer, carried to the page with the sentence to show for it. */
export class GatewayRequestError extends Error {
  readonly failure: GatewayFailure;

  constructor(failure: GatewayFailure, message: string) {
    super(message);
    this.name = 'GatewayRequestError';
    this.failure = failure;
  }
}

export interface FailureMessages {
  /** Shown when the service cannot be reached or answers with an outage. */
  readonly unavailable: string;
  /** Shown when the owner declines; defaults to the problem's detail, then `unavailable`. */
  readonly refused?: (problem: GatewayProblem) => string;
  /** Shown when the caller does not expect `notPublished`; defaults to `unavailable`. */
  readonly notPublished?: string;
}

export function failureMessage(failure: GatewayFailure, messages: FailureMessages): string {
  switch (failure.kind) {
    case 'routeMissing':
      return ROUTE_MISSING_MESSAGE;
    case 'refused':
      return messages.refused?.(failure.problem) ?? failure.problem.detail ?? messages.unavailable;
    case 'notPublished':
      return messages.notPublished ?? messages.unavailable;
    case 'unavailable':
      return messages.unavailable;
  }
}

/** The value of an `ok` answer; any other answer throws a GatewayRequestError with its message. */
export function valueOf<T>(result: GatewayResult<T>, messages: FailureMessages): T {
  if (result.kind === 'ok') return result.value;
  throw new GatewayRequestError(result, failureMessage(result, messages));
}

/** As valueOf, with "the owner has nothing yet" as null rather than a failure. */
export function publishedValueOf<T>(result: GatewayResult<T>, messages: FailureMessages): T | null {
  return result.kind === 'notPublished' ? null : valueOf(result, messages);
}

/** A path template with its {name} segments filled in, each encoded. */
export function gatewayPath(template: string, values: Readonly<Record<string, string>>): string {
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = values[name];
    if (value === undefined) throw new Error(`gatewayPath: no value for {${name}} in ${template}`);
    return encodeURIComponent(value);
  });
}
