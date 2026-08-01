import {
  InvestigationCoordinatorError,
  type AuthorizationDecision,
  type SiteNightEnergyInvestigationCoordinator,
  type SiteNightEnergyInvestigationView,
} from '../../application/index.js';

export interface OperationsAgentHttpAuthorizationInput {
  readonly method: string;
  readonly path: string;
  readonly organizationId: string;
  readonly siteId: string;
  readonly investigationId: string | null;
  readonly gatewayDelegationGrant: string;
  readonly registrySiteGrant?: string;
  readonly registryEquipmentGrant?: string;
  readonly energyGrant?: string;
  readonly policyRevision: string;
  readonly traceparent?: string;
}

export interface OperationsAgentHttpAuthorizer {
  authorize(input: OperationsAgentHttpAuthorizationInput): Promise<AuthorizationDecision>;
}

export interface OperationsAgentHttpCoordinatorContext {
  readonly organizationId: string;
  readonly siteId: string;
  readonly authorization: AuthorizationDecision;
  readonly now: number;
}

export interface OperationsAgentHttpOptions {
  readonly authorizer: OperationsAgentHttpAuthorizer;
  readonly createCoordinator: (
    context: OperationsAgentHttpCoordinatorContext,
  ) => SiteNightEnergyInvestigationCoordinator;
  readonly createAgUiEventStreamResponse?: (
    view: SiteNightEnergyInvestigationView,
  ) => Response;
  readonly now?: () => number;
  readonly maximumRequestBytes?: number;
}

export interface OperationsAgentHttpHandler {
  handle(request: Request): Promise<Response>;
}

interface MatchedRoute {
  readonly kind: 'START' | 'GET' | 'STREAM' | 'ADVANCE' | 'CANCEL';
  readonly siteId: string;
  readonly investigationId: string | null;
}

const defaultMaximumRequestBytes = 8_192;
const maximumIdentityLength = 256;
const collectionPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations$/u;
const itemPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+)$/u;
const streamPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+)\/events$/u;
const advancePattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+):advance$/u;
const cancelPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+):cancel$/u;
const traceparentPattern = /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/iu;

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

const problem = (
  status: number,
  code: string,
  title: string,
  detail: string,
): Response => new Response(JSON.stringify({
  type: `urn:hvac:operations-agent:${code.toLowerCase()}`,
  title,
  status,
  code,
  detail,
}), { status, headers: problemHeaders });

const decodeIdentity = (value: string): string | null => {
  let decoded: string;
  try {
    decoded = decodeURIComponent(value);
  } catch {
    return null;
  }
  return decoded.trim().length > 0 && decoded.length <= maximumIdentityLength
    ? decoded
    : null;
};

const matchRoute = (request: Request): MatchedRoute | null => {
  const path = new URL(request.url).pathname;
  const candidates: readonly [RegExp, MatchedRoute['kind'], string][] = [
    [collectionPattern, 'START', 'POST'],
    [streamPattern, 'STREAM', 'GET'],
    [advancePattern, 'ADVANCE', 'POST'],
    [cancelPattern, 'CANCEL', 'POST'],
    [itemPattern, 'GET', 'GET'],
  ];
  for (const [pattern, kind, method] of candidates) {
    const match = pattern.exec(path);
    if (match === null) continue;
    if (request.method !== method) {
      throw problem(405, 'METHOD_NOT_ALLOWED', 'Method not allowed', `This route requires ${method}.`);
    }
    const siteId = decodeIdentity(match[1] ?? '');
    const investigationId = kind === 'START' ? null : decodeIdentity(match[2] ?? '');
    if (siteId === null || (kind !== 'START' && investigationId === null)) {
      throw problem(
        404,
        'RESOURCE_NOT_FOUND',
        'Resource not found',
        'The requested Operations Investigation was not found.',
      );
    }
    return { kind, siteId, investigationId };
  }
  return null;
};

const requiredHeader = (request: Request, name: string): string | null => {
  const value = request.headers.get(name);
  return value !== null && value.trim().length > 0 && value.length <= 8_192 ? value : null;
};

const readBoundedBody = async (request: Request, maximumBytes: number): Promise<unknown> => {
  const contentType = request.headers.get('content-type')?.toLowerCase() ?? '';
  if (!contentType.startsWith('application/json')) {
    throw problem(
      415,
      'CONTENT_TYPE_UNSUPPORTED',
      'Content type unsupported',
      'Mutation requests require application/json.',
    );
  }
  const declared = request.headers.get('content-length');
  if (declared !== null) {
    const length = Number(declared);
    if (!Number.isSafeInteger(length) || length < 0 || length > maximumBytes) {
      throw problem(413, 'REQUEST_TOO_LARGE', 'Request too large', 'The request body is too large.');
    }
  }
  const reader = request.body?.getReader();
  if (reader === undefined) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'A JSON object body is required.');
  }
  const chunks: Uint8Array[] = [];
  let total = 0;
  while (true) {
    const next = await reader.read();
    if (next.done) break;
    if (next.value === undefined) continue;
    total += next.value.byteLength;
    if (total > maximumBytes) {
      await reader.cancel().catch(() => undefined);
      throw problem(413, 'REQUEST_TOO_LARGE', 'Request too large', 'The request body is too large.');
    }
    chunks.push(next.value);
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder().decode(bytes)) as unknown;
  } catch {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'The request body is not valid JSON.');
  }
  if (typeof value !== 'object'
    || value === null
    || Array.isArray(value)
    || Object.keys(value).length !== 0) {
    throw problem(
      400,
      'REQUEST_INVALID',
      'Request invalid',
      'This Operations Investigation mutation accepts only an empty JSON object.',
    );
  }
  return value;
};

const mapError = (error: unknown): Response => {
  if (error instanceof Response) return error;
  if (error instanceof InvestigationCoordinatorError) {
    if (error.code === 'AUTHORIZATION_DENIED' || error.code === 'INVESTIGATION_NOT_FOUND') {
      return problem(
        404,
        'RESOURCE_NOT_FOUND',
        'Resource not found',
        'The requested Operations Investigation was not found.',
      );
    }
    if (error.code === 'OWNER_RESOURCE_NOT_FOUND') {
      return problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Site was not found.');
    }
    if (error.code === 'OWNER_READ_TIMEOUT') {
      return problem(504, 'OPERATIONS_AGENT_TIMEOUT', 'Operations Agent timeout', 'An authoritative Owner timed out.');
    }
    if (error.code === 'OWNER_READ_UNAVAILABLE') {
      return problem(503, 'OPERATIONS_AGENT_UNAVAILABLE', 'Operations Agent unavailable', 'An authoritative Owner is unavailable.');
    }
    if (error.code === 'OWNER_REQUEST_INVALID' || error.code === 'OWNER_RESPONSE_INVALID') {
      return problem(502, 'OWNER_CONTRACT_FAILED', 'Owner contract failed', 'An authoritative Owner response was invalid.');
    }
    if (error.code === 'BUDGET_EXHAUSTED') {
      return problem(429, 'INVESTIGATION_BUDGET_EXHAUSTED', 'Investigation budget exhausted', error.message);
    }
    return problem(409, error.code, 'Investigation conflict', error.message);
  }
  return problem(
    500,
    'OPERATIONS_AGENT_INTERNAL',
    'Operations Agent internal error',
    'The Operations Agent could not complete the request.',
  );
};

export const createOperationsAgentHttpHandler = (
  options: OperationsAgentHttpOptions,
): OperationsAgentHttpHandler => {
  const maximumRequestBytes = options.maximumRequestBytes ?? defaultMaximumRequestBytes;
  if (!Number.isSafeInteger(maximumRequestBytes) || maximumRequestBytes <= 0) {
    throw new Error('maximumRequestBytes must be a positive safe integer.');
  }
  const now = options.now ?? Date.now;

  return Object.freeze({
    async handle(request: Request): Promise<Response> {
      try {
        const url = new URL(request.url);
        if (url.search !== '') {
          return problem(400, 'QUERY_UNSUPPORTED', 'Query unsupported', 'Operations Investigation routes do not accept query parameters.');
        }
        const route = matchRoute(request);
        if (route === null) {
          return problem(404, 'ROUTE_NOT_FOUND', 'Route not found', 'The requested internal route does not exist.');
        }
        if (route.kind === 'GET' || route.kind === 'STREAM') {
          if (request.body !== null) {
            return problem(400, 'REQUEST_INVALID', 'Request invalid', 'GET requests must not contain a body.');
          }
        } else {
          await readBoundedBody(request, maximumRequestBytes);
        }

        const organizationId = requiredHeader(request, 'X-Acting-Organization-ID');
        const gatewayDelegationGrant = requiredHeader(request, 'X-Delegation-Grant');
        const registrySiteGrant = requiredHeader(request, 'X-Operations-Registry-Site-Grant');
        const registryEquipmentGrant = requiredHeader(
          request,
          'X-Operations-Registry-Equipment-Grant',
        );
        const energyGrant = requiredHeader(request, 'X-Operations-Energy-Grant');
        const policyRevision = requiredHeader(request, 'X-Route-Policy-Revision');
        const traceparent = request.headers.get('traceparent') ?? undefined;
        if (organizationId === null
          || gatewayDelegationGrant === null
          || policyRevision === null
          || (traceparent !== undefined && !traceparentPattern.test(traceparent))) {
          return problem(
            401,
            'OPERATIONS_AGENT_AUTHORIZATION_INVALID',
            'Authorization invalid',
            'The internal Operations Agent authorization context is incomplete.',
          );
        }

        const authorization = await options.authorizer.authorize({
          method: request.method,
          path: url.pathname,
          organizationId,
          siteId: route.siteId,
          investigationId: route.investigationId,
          gatewayDelegationGrant,
          ...(registrySiteGrant === null ? {} : { registrySiteGrant }),
          ...(registryEquipmentGrant === null ? {} : { registryEquipmentGrant }),
          ...(energyGrant === null ? {} : { energyGrant }),
          policyRevision,
          ...(traceparent === undefined ? {} : { traceparent }),
        });
        if (authorization.decision !== 'ALLOW') {
          return problem(
            404,
            'RESOURCE_NOT_FOUND',
            'Resource not found',
            'The requested Operations Investigation was not found.',
          );
        }
        const coordinator = options.createCoordinator({
          organizationId,
          siteId: route.siteId,
          authorization,
          now: now(),
        });
        if (route.kind === 'START') {
          return jsonResponse(201, await coordinator.start({
            organizationId,
            siteId: route.siteId,
          }));
        }
        const investigationId = route.investigationId;
        if (investigationId === null) {
          return problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Investigation was not found.');
        }
        if (route.kind === 'STREAM') {
          if (options.createAgUiEventStreamResponse === undefined) {
            return problem(
              503,
              'OPERATIONS_AGENT_STREAM_UNAVAILABLE',
              'Operations Agent stream unavailable',
              'The Operations Agent event projection is not configured.',
            );
          }
          return options.createAgUiEventStreamResponse(await coordinator.get({ investigationId }));
        }
        if (route.kind === 'GET') {
          return jsonResponse(200, await coordinator.get({ investigationId }));
        }
        if (route.kind === 'ADVANCE') {
          return jsonResponse(200, await coordinator.advance({ investigationId }));
        }
        return jsonResponse(200, await coordinator.cancel({ investigationId }));
      } catch (error) {
        return mapError(error);
      }
    },
  });
};
