import {
  AgentSessionLifecycleError,
  AgentSessionServiceError,
  type AgentSessionAccessContext,
  type AgentSessionService,
  type AgentUsageReader,
  type AuthorizationDecision,
} from '../../application/index.js';
export interface AgentSessionHttpAuthorizationInput {
  readonly method: string;
  readonly path: string;
  readonly tenantId: string;
  readonly siteId: string;
  readonly sessionId: string | null;
  readonly gatewayDelegationGrant: string;
  readonly policyRevision: string;
}

export interface AgentSessionHttpAuthorizer {
  authorize(input: AgentSessionHttpAuthorizationInput): Promise<AuthorizationDecision>;
}

export interface AgentSessionHttpOptions {
  readonly authorizer: AgentSessionHttpAuthorizer;
  readonly service: AgentSessionService;
  readonly usage: AgentUsageReader;
  readonly createEventStreamResponse: (
    service: AgentSessionService,
    context: AgentSessionAccessContext,
    sessionId: string,
  ) => Promise<Response>;
  readonly maximumRequestBytes?: number;
}

export interface AgentSessionHttpHandler {
  handle(request: Request): Promise<Response>;
}

type AgentSessionRouteKind = 'CREATE' | 'LIST' | 'GET' | 'START' | 'CANCEL' | 'SUBMIT_INPUT' | 'STREAM' | 'USAGE';

interface AgentSessionRoute {
  readonly kind: AgentSessionRouteKind;
  readonly siteId: string;
  readonly sessionId: string | null;
}

const collectionPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions$/u;
const usagePattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-usage$/u;
// The usage view covers the last two weeks of Site-local days, today included (#597).
const USAGE_DAYS = 14;
const itemPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions\/([^/:]+)$/u;
const streamPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions\/([^/:]+)\/events$/u;
const startPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions\/([^/:]+):run$/u;
const cancelPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions\/([^/:]+):cancel$/u;
const inputPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/agent-sessions\/([^/:]+):submit-input$/u;
const maximumIdentityLength = 256;
const defaultMaximumRequestBytes = 8_192;

const jsonHeaders = Object.freeze({
  'Cache-Control': 'no-store',
  'Content-Type': 'application/json; charset=utf-8',
});
const problemHeaders = Object.freeze({
  'Cache-Control': 'no-store',
  'Content-Type': 'application/problem+json; charset=utf-8',
});

const jsonResponse = (status: number, value: unknown): Response => new Response(
  JSON.stringify(value),
  { status, headers: jsonHeaders },
);

const problem = (status: number, code: string, title: string, detail: string): Response => new Response(
  JSON.stringify({
    type: `urn:hvac:operations-agent:${code.toLowerCase()}`,
    title,
    status,
    code,
    detail,
  }),
  { status, headers: problemHeaders },
);

const decodeIdentity = (value: string): string | null => {
  let decoded: string;
  try {
    decoded = decodeURIComponent(value);
  } catch {
    return null;
  }
  return decoded.trim().length > 0 && decoded.length <= maximumIdentityLength ? decoded : null;
};

const matchRoute = (request: Request): AgentSessionRoute | null => {
  const path = new URL(request.url).pathname;
  const collection = collectionPattern.exec(path);
  if (collection !== null) {
    const siteId = decodeIdentity(collection[1] ?? '');
    if (siteId === null) return null;
    if (request.method === 'GET') return { kind: 'LIST', siteId, sessionId: null };
    if (request.method === 'POST') return { kind: 'CREATE', siteId, sessionId: null };
    throw problem(405, 'METHOD_NOT_ALLOWED', 'Method not allowed', 'This route requires GET or POST.');
  }
  const usage = usagePattern.exec(path);
  if (usage !== null) {
    if (request.method !== 'GET') throw problem(405, 'METHOD_NOT_ALLOWED', 'Method not allowed', 'This route requires GET.');
    const siteId = decodeIdentity(usage[1] ?? '');
    return siteId === null ? null : { kind: 'USAGE', siteId, sessionId: null };
  }
  const candidates: readonly [RegExp, AgentSessionRouteKind, string][] = [
    [streamPattern, 'STREAM', 'GET'],
    [startPattern, 'START', 'POST'],
    [cancelPattern, 'CANCEL', 'POST'],
    [inputPattern, 'SUBMIT_INPUT', 'POST'],
    [itemPattern, 'GET', 'GET'],
  ];
  for (const [pattern, kind, method] of candidates) {
    const match = pattern.exec(path);
    if (match === null) continue;
    if (request.method !== method) {
      throw problem(405, 'METHOD_NOT_ALLOWED', 'Method not allowed', `This route requires ${method}.`);
    }
    const siteId = decodeIdentity(match[1] ?? '');
    const sessionId = decodeIdentity(match[2] ?? '');
    return siteId === null || sessionId === null ? null : { kind, siteId, sessionId };
  }
  return null;
};

/** The Site-local dates the usage view covers, from the Site's time zone; null when it is not one. */
const usageRange = (url: URL, now: Date): { readonly timezone: string; readonly fromDate: string; readonly toDate: string } | null => {
  const timezone = url.searchParams.get('timezone') ?? '';
  if ([...url.searchParams.keys()].some((key) => key !== 'timezone') || timezone.length === 0 || timezone.length > 64) return null;
  let today: string;
  try {
    today = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(now);
  } catch {
    return null;
  }
  const [year, month, day] = today.split('-').map(Number);
  const fromDate = new Date(Date.UTC(year!, month! - 1, day! - (USAGE_DAYS - 1))).toISOString().slice(0, 10);
  return { timezone, fromDate, toDate: today };
};

const requiredHeader = (request: Request, name: string): string | null => {
  const value = request.headers.get(name);
  return value !== null && value.trim().length > 0 && value.length <= 8_192 ? value : null;
};

const readBody = async (request: Request, maximumBytes: number): Promise<Record<string, unknown>> => {
  const contentType = request.headers.get('content-type')?.toLowerCase() ?? '';
  if (!contentType.startsWith('application/json')) {
    throw problem(415, 'CONTENT_TYPE_UNSUPPORTED', 'Content type unsupported', 'Mutation requests require application/json.');
  }
  const raw = new Uint8Array(await request.arrayBuffer());
  if (raw.byteLength > maximumBytes) {
    throw problem(413, 'REQUEST_TOO_LARGE', 'Request too large', 'The request body is too large.');
  }
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder().decode(raw)) as unknown;
  } catch {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'The request body is not valid JSON.');
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'A JSON object body is required.');
  }
  return value as Record<string, unknown>;
};

const exactKeys = (value: Record<string, unknown>, keys: readonly string[]): boolean => (
  Object.keys(value).length === keys.length && keys.every((key) => key in value)
);

const parseMessageBody = (value: Record<string, unknown>): { readonly expectedRevision?: number; readonly message: string } => {
  const hasRevision = 'expectedRevision' in value;
  const expectedKeys = hasRevision ? ['expectedRevision', 'message'] : ['message'];
  if (!exactKeys(value, expectedKeys)
    || typeof value.message !== 'string'
    || value.message.trim().length === 0
    || value.message.length > 4_000
    || (hasRevision && (!Number.isSafeInteger(value.expectedRevision) || (value.expectedRevision as number) < 0))) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'Agent Session message request is invalid.');
  }
  return hasRevision
    ? { expectedRevision: value.expectedRevision as number, message: value.message }
    : { message: value.message };
};

const parseRevisionBody = (value: Record<string, unknown>): number => {
  if (!exactKeys(value, ['expectedRevision'])
    || !Number.isSafeInteger(value.expectedRevision)
    || (value.expectedRevision as number) < 0) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'Agent Session revision request is invalid.');
  }
  return value.expectedRevision as number;
};

const parseInputBody = (value: Record<string, unknown>) => {
  if (!exactKeys(value, ['expectedRevision', 'requestArtifactId', 'value'])
    || !Number.isSafeInteger(value.expectedRevision)
    || (value.expectedRevision as number) < 0
    || typeof value.requestArtifactId !== 'string'
    || value.requestArtifactId.trim().length === 0
    || value.requestArtifactId.length > maximumIdentityLength
    || typeof value.value !== 'string'
    || value.value.trim().length === 0
    || value.value.length > 4_000) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'Operator Input submission is invalid.');
  }
  return {
    expectedRevision: value.expectedRevision as number,
    requestArtifactId: value.requestArtifactId,
    value: value.value,
  };
};

const mapError = (error: unknown): Response => {
  if (error instanceof Response) return error;
  if (error instanceof AgentSessionServiceError) {
    if (error.code === 'SESSION_NOT_FOUND' || error.code === 'SESSION_SCOPE_MISMATCH') {
      return problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Agent Session was not found.');
    }
    return problem(400, error.code, 'Agent Session request invalid', error.message);
  }
  if (error instanceof AgentSessionLifecycleError) {
    if (error.code === 'SESSION_REVISION_CONFLICT'
      || error.code === 'RUN_ALREADY_ACTIVE'
      || error.code === 'RUN_STALE'
      || error.code === 'IDEMPOTENCY_CONFLICT') {
      return problem(409, error.code, 'Agent Session conflict', error.message);
    }
    return problem(400, error.code, 'Agent Session request invalid', error.message);
  }
  return problem(500, 'OPERATIONS_AGENT_INTERNAL', 'Operations Agent internal error', 'The Operations Agent could not complete the request.');
};

const contextFromAuthorization = (
  request: Request,
  route: AgentSessionRoute,
  tenantId: string,
  authorization: AuthorizationDecision,
): AgentSessionAccessContext | null => {
  const principalId = authorization.operator?.id ?? null;
  if (principalId === null) return null;
  return Object.freeze({
    tenantId,
    siteId: route.siteId,
    principalId,
    capabilities: Object.freeze([...(authorization.capabilities ?? [])]),
    correlationId: requiredHeader(request, 'X-Request-ID') ?? authorization.decisionId,
    authorization,
  });
};

export const createAgentSessionHttpHandler = (options: AgentSessionHttpOptions): AgentSessionHttpHandler => {
  const maximumRequestBytes = options.maximumRequestBytes ?? defaultMaximumRequestBytes;
  if (!Number.isSafeInteger(maximumRequestBytes) || maximumRequestBytes <= 0) {
    throw new Error('maximumRequestBytes must be a positive safe integer.');
  }

  return Object.freeze({
    async handle(request: Request): Promise<Response> {
      try {
        const url = new URL(request.url);
        const route = matchRoute(request);
        if (route === null) {
          return problem(404, 'ROUTE_NOT_FOUND', 'Route not found', 'The requested internal Agent Session route does not exist.');
        }
        const range = route.kind === 'USAGE' ? usageRange(url, new Date()) : null;
        if (route.kind === 'USAGE' && range === null) {
          return problem(400, 'REQUEST_INVALID', 'Request invalid', 'Agent usage takes only the Site time zone, as timezone.');
        }
        if (route.kind !== 'USAGE' && url.search !== '') {
          return problem(400, 'QUERY_UNSUPPORTED', 'Query unsupported', 'Agent Session routes do not accept query parameters.');
        }
        if ((route.kind === 'GET' || route.kind === 'LIST' || route.kind === 'STREAM' || route.kind === 'USAGE') && request.body !== null) {
          return problem(400, 'REQUEST_INVALID', 'Request invalid', 'GET requests must not contain a body.');
        }

        const tenantId = requiredHeader(request, 'X-Tenant-ID');
        const gatewayDelegationGrant = requiredHeader(request, 'X-Delegation-Grant');
        const policyRevision = requiredHeader(request, 'X-Route-Policy-Revision');
        if (tenantId === null || gatewayDelegationGrant === null || policyRevision === null) {
          return problem(401, 'OPERATIONS_AGENT_AUTHORIZATION_INVALID', 'Authorization invalid', 'The internal Agent Session authorization context is incomplete.');
        }
        const authorization = await options.authorizer.authorize({
          method: request.method,
          path: url.pathname,
          tenantId,
          siteId: route.siteId,
          sessionId: route.sessionId,
          gatewayDelegationGrant,
          policyRevision,
        });
        if (authorization.decision !== 'ALLOW') {
          return problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Agent Session was not found.');
        }
        const context = contextFromAuthorization(request, route, tenantId, authorization);
        if (context === null) {
          return problem(401, 'OPERATIONS_AGENT_AUTHORIZATION_INVALID', 'Authorization invalid', 'The authenticated Operator identity is unavailable.');
        }

        if (route.kind === 'LIST') return jsonResponse(200, await options.service.list(context));
        if (range !== null) {
          return jsonResponse(200, await options.usage.daily(context.tenantId, context.siteId, range.timezone, range.fromDate, range.toDate));
        }
        if (route.kind === 'CREATE') {
          const body = parseMessageBody(await readBody(request, maximumRequestBytes));
          if (body.expectedRevision !== undefined) {
            return problem(400, 'REQUEST_INVALID', 'Request invalid', 'New Agent Sessions do not accept expectedRevision.');
          }
          return jsonResponse(201, await options.service.create(context, { message: body.message }));
        }

        const sessionId = route.sessionId;
        if (sessionId === null) {
          return problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Agent Session was not found.');
        }
        if (route.kind === 'GET') return jsonResponse(200, await options.service.get(context, sessionId));
        if (route.kind === 'STREAM') {
          await options.service.get(context, sessionId);
          return options.createEventStreamResponse(options.service, context, sessionId);
        }
        const body = await readBody(request, maximumRequestBytes);
        if (route.kind === 'START') {
          const parsed = parseMessageBody(body);
          if (parsed.expectedRevision === undefined) {
            return problem(400, 'REQUEST_INVALID', 'Request invalid', 'Run start requires expectedRevision.');
          }
          return jsonResponse(202, await options.service.start(context, {
            sessionId,
            expectedRevision: parsed.expectedRevision,
            message: parsed.message,
          }));
        }
        if (route.kind === 'CANCEL') {
          return jsonResponse(200, await options.service.cancel(context, {
            sessionId,
            expectedRevision: parseRevisionBody(body),
          }));
        }
        const parsed = parseInputBody(body);
        return jsonResponse(202, await options.service.submitInput(context, {
          sessionId,
          ...parsed,
        }));
      } catch (error) {
        return mapError(error);
      }
    },
  });
};
