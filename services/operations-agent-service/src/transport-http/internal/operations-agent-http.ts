import {
  InvestigationCoordinatorError,
  safeAddOperationsTelemetryCounter,
  safeObserveOperationsTelemetryHistogram,
  safeStartOperationsTelemetrySpan,
  type AuthorizationDecision,
  type OperationsAgentTelemetry,
  type OperationsTelemetryCorrelation,
  type OperationsTelemetryOperation,
  type OperationsTelemetryOutcome,
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
  readonly tracestate?: string;
}

export interface OperationsAgentHttpAuthorizer {
  authorize(input: OperationsAgentHttpAuthorizationInput): Promise<AuthorizationDecision>;
}

export interface OperationsAgentHttpCoordinatorContext {
  readonly organizationId: string;
  readonly siteId: string;
  readonly authorization: AuthorizationDecision;
  readonly telemetryContext: OperationsTelemetryCorrelation;
  readonly now: number;
}

export interface OperationsAgentHttpOptions {
  readonly authorizer: OperationsAgentHttpAuthorizer;
  readonly createCoordinator: (
    context: OperationsAgentHttpCoordinatorContext,
  ) => SiteNightEnergyInvestigationCoordinator;
  readonly createAgUiEventStreamResponse?: (
    view: SiteNightEnergyInvestigationView,
    requestedPosition?: string | null,
  ) => Response;
  readonly now?: () => number;
  readonly maximumRequestBytes?: number;
  readonly telemetry?: OperationsAgentTelemetry;
}

export interface OperationsAgentHttpHandler {
  handle(request: Request): Promise<Response>;
}

interface MatchedRoute {
  readonly kind:
    | 'START'
    | 'LIST'
    | 'GET'
    | 'STREAM'
    | 'ADVANCE'
    | 'SUBMIT_OPERATOR_INPUT'
    | 'CANCEL';
  readonly siteId: string;
  readonly investigationId: string | null;
}

const defaultMaximumRequestBytes = 8_192;
const maximumIdentityLength = 256;
const collectionPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations$/u;
const itemPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+)$/u;
const streamPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+)\/events$/u;
const advancePattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+):advance$/u;
const submitOperatorInputPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+):submit-operator-input$/u;
const cancelPattern = /^\/internal\/v1\/sites\/([^/]+)\/operations\/investigations\/([^/:]+):cancel$/u;
const traceparentPattern = /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/iu;

const validTraceparent = (value: string): boolean => {
  const normalized = value.toLowerCase();
  return traceparentPattern.test(normalized)
    && normalized.slice(3, 35) !== '0'.repeat(32)
    && normalized.slice(36, 52) !== '0'.repeat(16);
};

const validTracestate = (value: string | undefined): boolean => (
  value === undefined
  || (value.trim().length > 0
    && value.length <= 512
    && !value.includes(String.fromCharCode(13))
    && !value.includes(String.fromCharCode(10)))
);

const operationForRoute = (kind: MatchedRoute['kind']): OperationsTelemetryOperation => kind;

const telemetryOutcomeForStatus = (status: number): OperationsTelemetryOutcome => {
  if (status >= 200 && status < 300) return 'SUCCESS';
  if (status === 400 || status === 413 || status === 415 || status === 422) return 'INVALID';
  if (status === 401 || status === 403) return 'DENIED';
  if (status === 404) return 'NOT_FOUND';
  if (status === 408 || status === 504) return 'TIMEOUT';
  if (status === 409) return 'CONFLICT';
  if (status === 429) return 'EXHAUSTED';
  if (status === 502 || status === 503) return 'UNAVAILABLE';
  return 'ERROR';
};

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
  const collection = collectionPattern.exec(path);
  if (collection !== null) {
    const siteId = decodeIdentity(collection[1] ?? '');
    if (siteId === null) {
      throw problem(404, 'RESOURCE_NOT_FOUND', 'Resource not found', 'The requested Site was not found.');
    }
    if (request.method === 'GET') return { kind: 'LIST', siteId, investigationId: null };
    if (request.method === 'POST') return { kind: 'START', siteId, investigationId: null };
    throw problem(405, 'METHOD_NOT_ALLOWED', 'Method not allowed', 'This route requires GET or POST.');
  }
  const candidates: readonly [RegExp, MatchedRoute['kind'], string][] = [
    [streamPattern, 'STREAM', 'GET'],
    [advancePattern, 'ADVANCE', 'POST'],
    [submitOperatorInputPattern, 'SUBMIT_OPERATOR_INPUT', 'POST'],
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
    const investigationId = decodeIdentity(match[2] ?? '');
    if (siteId === null || investigationId === null) {
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
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'A JSON object body is required.');
  }
  return value;
};

const requireEmptyMutationBody = (value: unknown): void => {
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
};

interface SubmitOperatorInputBody {
  readonly requestId: string;
  readonly expectedRevision: number;
  readonly values: {
    readonly analysisScope: 'SITE_ONLY' | 'DEFER';
    readonly operatorNote: string | null;
  };
}

const parseSubmitOperatorInputBody = (value: unknown): SubmitOperatorInputBody => {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'A JSON object body is required.');
  }
  const body = value as Record<string, unknown>;
  const bodyKeys = Object.keys(body);
  if (bodyKeys.length !== 4
    || !bodyKeys.includes('schemaVersion')
    || !bodyKeys.includes('requestId')
    || !bodyKeys.includes('expectedRevision')
    || !bodyKeys.includes('values')
    || body.schemaVersion !== 1
    || typeof body.requestId !== 'string'
    || body.requestId.trim().length === 0
    || body.requestId.length > maximumIdentityLength
    || !Number.isSafeInteger(body.expectedRevision)
    || (body.expectedRevision as number) < 0
    || typeof body.values !== 'object'
    || body.values === null
    || Array.isArray(body.values)) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'Operator Input submission is invalid.');
  }
  const values = body.values as Record<string, unknown>;
  const valueKeys = Object.keys(values);
  if (valueKeys.length !== 2
    || !valueKeys.includes('analysisScope')
    || !valueKeys.includes('operatorNote')
    || (values.analysisScope !== 'SITE_ONLY' && values.analysisScope !== 'DEFER')
    || (values.operatorNote !== null
      && (typeof values.operatorNote !== 'string'
        || values.operatorNote.trim().length === 0
        || values.operatorNote.length > 500))) {
    throw problem(400, 'REQUEST_INVALID', 'Request invalid', 'Operator Input values are invalid.');
  }
  return {
    requestId: body.requestId,
    expectedRevision: body.expectedRevision as number,
    values: {
      analysisScope: values.analysisScope,
      operatorNote: values.operatorNote as string | null,
    },
  };
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
    if (error.code === 'UNTRUSTED_CONTENT_REJECTED') {
      return problem(
        422,
        'UNTRUSTED_CONTENT_REJECTED',
        'Untrusted content rejected',
        'Runtime output attempted to alter the bounded Operations Agent control policy.',
      );
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

  const handleRequest = async (
    request: Request,
    telemetryContext: OperationsTelemetryCorrelation,
  ): Promise<Response> => {
    try {
        const url = new URL(request.url);
        if (url.search !== '') {
          return problem(400, 'QUERY_UNSUPPORTED', 'Query unsupported', 'Operations Investigation routes do not accept query parameters.');
        }
        const route = matchRoute(request);
        if (route === null) {
          return problem(404, 'ROUTE_NOT_FOUND', 'Route not found', 'The requested internal route does not exist.');
        }
        let mutationBody: unknown = undefined;
        let operatorInputBody: SubmitOperatorInputBody | null = null;
        if (route.kind === 'GET' || route.kind === 'LIST' || route.kind === 'STREAM') {
          if (request.body !== null) {
            return problem(400, 'REQUEST_INVALID', 'Request invalid', 'GET requests must not contain a body.');
          }
        } else {
          mutationBody = await readBoundedBody(request, maximumRequestBytes);
          if (route.kind === 'SUBMIT_OPERATOR_INPUT') {
            operatorInputBody = parseSubmitOperatorInputBody(mutationBody);
          } else {
            requireEmptyMutationBody(mutationBody);
          }
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
        const requestId = telemetryContext.requestId ?? telemetryContext.traceparent ?? 'operations-request';
        if (organizationId === null
          || gatewayDelegationGrant === null
          || policyRevision === null) {
          return problem(
            401,
            'OPERATIONS_AGENT_AUTHORIZATION_INVALID',
            'Authorization invalid',
            'The internal Operations Agent authorization context is incomplete.',
          );
        }

        const authorizationStartedAt = now();
        const authorizationSpan = safeStartOperationsTelemetrySpan(options.telemetry, {
          name: 'operations.authorization',
          kind: 'CLIENT',
          correlation: {
            ...telemetryContext,
            requestId,
            ...(route.investigationId === null
              ? {} : { investigationId: route.investigationId }),
          },
          attributes: { operation: 'AUTHORIZE', owner: 'platform-gateway' },
        });
        let authorization: AuthorizationDecision;
        try {
          authorization = await options.authorizer.authorize({
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
            ...((authorizationSpan.traceparent ?? telemetryContext.traceparent) === undefined
              ? {}
              : { traceparent: authorizationSpan.traceparent ?? telemetryContext.traceparent }),
            ...((authorizationSpan.tracestate ?? telemetryContext.tracestate) === undefined
              ? {}
              : { tracestate: authorizationSpan.tracestate ?? telemetryContext.tracestate }),
          });
          authorizationSpan.setStatus(authorization.decision === 'ALLOW' ? 'SUCCESS' : 'DENIED');
        } catch (error) {
          authorizationSpan.setStatus('ERROR');
          throw error;
        } finally {
          const authorizationDurationMs = Math.max(0, now() - authorizationStartedAt);
          authorizationSpan.setAttributes({ durationMs: authorizationDurationMs });
          authorizationSpan.end();
          safeObserveOperationsTelemetryHistogram(options.telemetry, {
            name: 'operations_agent_operation_duration_ms',
            value: authorizationDurationMs,
            labels: { operation: 'AUTHORIZE' },
          });
        }
        if (authorization.decision !== 'ALLOW') {
          return problem(
            404,
            'RESOURCE_NOT_FOUND',
            'Resource not found',
            'The requested Operations Investigation was not found.',
          );
        }
        const coordinatorAuthorization: AuthorizationDecision = {
          ...authorization,
          ...(telemetryContext.traceparent === undefined
            ? {} : { traceparent: telemetryContext.traceparent }),
          ...(telemetryContext.tracestate === undefined
            ? {} : { tracestate: telemetryContext.tracestate }),
        };
        const coordinator = options.createCoordinator({
          organizationId,
          siteId: route.siteId,
          authorization: coordinatorAuthorization,
          telemetryContext: {
            ...telemetryContext,
            requestId,
            ...(route.investigationId === null
              ? {} : { investigationId: route.investigationId }),
          },
          now: now(),
        });
        if (route.kind === 'LIST') {
          return jsonResponse(200, await coordinator.list({
            organizationId,
            siteId: route.siteId,
          }));
        }
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
          const requestedPosition = request.headers.get('Last-Event-ID');
          return options.createAgUiEventStreamResponse(
            await coordinator.get({ investigationId }),
            requestedPosition !== null && requestedPosition.length <= 128
              ? requestedPosition
              : requestedPosition === null ? null : 'invalid',
          );
        }
        if (route.kind === 'GET') {
          return jsonResponse(200, await coordinator.get({ investigationId }));
        }
        if (route.kind === 'ADVANCE') {
          return jsonResponse(200, await coordinator.advance({ investigationId }));
        }
        if (route.kind === 'SUBMIT_OPERATOR_INPUT') {
          const idempotencyKey = requiredHeader(request, 'Idempotency-Key');
          if (idempotencyKey === null || idempotencyKey.length > maximumIdentityLength) {
            return problem(
              400,
              'IDEMPOTENCY_KEY_REQUIRED',
              'Idempotency Key required',
              'Operator Input submission requires a bounded Idempotency-Key header.',
            );
          }
          if (operatorInputBody === null) {
            return problem(400, 'REQUEST_INVALID', 'Request invalid', 'Operator Input submission is invalid.');
          }
          return jsonResponse(200, await coordinator.acceptOperatorInput({
            investigationId,
            requestId: operatorInputBody.requestId,
            expectedRevision: operatorInputBody.expectedRevision,
            idempotencyKey,
            values: operatorInputBody.values,
          }));
        }
        return jsonResponse(200, await coordinator.cancel({ investigationId }));
    } catch (error) {
      return mapError(error);
    }
  };

  return Object.freeze({
    async handle(request: Request): Promise<Response> {
      const startedAt = now();
      const rawRequestId = request.headers.get('X-Request-ID') ?? undefined;
      const requestIdCandidate = rawRequestId !== undefined
        && rawRequestId.trim().length > 0
        && rawRequestId.length <= maximumIdentityLength
        ? rawRequestId
        : undefined;
      const rawTraceparent = request.headers.get('traceparent') ?? undefined;
      const incomingTraceparent = rawTraceparent !== undefined
        && validTraceparent(rawTraceparent)
        ? rawTraceparent
        : undefined;
      const rawTracestate = request.headers.get('tracestate') ?? undefined;
      const incomingTracestate = validTracestate(rawTracestate) ? rawTracestate : undefined;
      let operation: OperationsTelemetryOperation = 'GET';
      try {
        const route = matchRoute(request);
        if (route !== null) operation = operationForRoute(route.kind);
      } catch {
        // The authoritative route parser inside handleRequest owns the HTTP result.
      }
      const span = safeStartOperationsTelemetrySpan(options.telemetry, {
        name: 'operations.http.request',
        kind: 'SERVER',
        correlation: {
          ...(requestIdCandidate === undefined ? {} : { requestId: requestIdCandidate }),
          ...(incomingTraceparent === undefined ? {} : { traceparent: incomingTraceparent }),
          ...(incomingTracestate === undefined ? {} : { tracestate: incomingTracestate }),
        },
        attributes: { operation },
      });
      const telemetryContext: OperationsTelemetryCorrelation = {
        requestId: requestIdCandidate ?? span.traceparent ?? 'operations-request',
        ...(span.traceparent === undefined
          ? (incomingTraceparent === undefined ? {} : { traceparent: incomingTraceparent })
          : { traceparent: span.traceparent }),
        ...(span.tracestate === undefined
          ? (incomingTracestate === undefined ? {} : { tracestate: incomingTracestate })
          : { tracestate: span.tracestate }),
      };
      const response = await handleRequest(request, telemetryContext);
      const outcome = telemetryOutcomeForStatus(response.status);
      if (operation === 'STREAM' && response.status >= 200 && response.status < 300) {
        const recoveryMode = response.headers.get('X-Operations-Recovery-Mode');
        const recoveryReason = response.headers.get('X-Operations-Recovery-Reason');
        const validMode = recoveryMode === 'FULL_SNAPSHOT' || recoveryMode === 'RESUME';
        const validReason = recoveryReason === 'INITIAL'
          || recoveryReason === 'VALID'
          || recoveryReason === 'EXPIRED'
          || recoveryReason === 'UNKNOWN'
          || recoveryReason === 'FUTURE'
          || recoveryReason === 'CONFLICT'
          || recoveryReason === 'INVALID';
        if (validMode && validReason) {
          const recoverySpan = safeStartOperationsTelemetrySpan(options.telemetry, {
            name: 'operations.stream.recovery',
            kind: 'INTERNAL',
            correlation: telemetryContext,
            attributes: {
              operation: 'STREAM',
              outcome: 'SUCCESS',
              recoveryMode,
              recoveryReason,
              restarted: recoveryMode === 'RESUME',
            },
          });
          recoverySpan.setStatus('SUCCESS');
          recoverySpan.end();
          safeAddOperationsTelemetryCounter(options.telemetry, {
            name: 'operations_agent_recovery_total',
            labels: { operation: 'STREAM', recoveryMode, recoveryReason, outcome: 'SUCCESS' },
          });
        }
      }
      const durationMs = Math.max(0, now() - startedAt);
      span.setAttributes({ operation, outcome, durationMs });
      span.setStatus(outcome);
      span.end();
      safeAddOperationsTelemetryCounter(options.telemetry, {
        name: 'operations_agent_requests_total',
        labels: { operation, outcome },
      });
      safeObserveOperationsTelemetryHistogram(options.telemetry, {
        name: 'operations_agent_operation_duration_ms',
        value: durationMs,
        labels: { operation, outcome },
      });
      try {
        if (telemetryContext.traceparent !== undefined) {
          response.headers.set('traceparent', telemetryContext.traceparent);
        }
        if (telemetryContext.tracestate !== undefined) {
          response.headers.set('tracestate', telemetryContext.tracestate);
        }
      } catch {
        // Correlation response headers are diagnostic and cannot replace the HTTP result.
      }
      return response;
    },
  });
};
