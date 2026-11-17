import {
  OwnerReadError,
  type OwnerReadContext,
  type ToolAuthorizableReadRequest,
} from '../../application/index.js';

export type OwnerGrantLogicalTool = ToolAuthorizableReadRequest['tool'];

export interface OwnerGrantWirePolicy {
  readonly grantHeader:
    | 'X-Delegation-Grant'
    | 'Authorization'
    | 'X-Alarm-Read-Context'
    | 'X-FDD-Context'
    | 'X-Work-Order-Read-Context';
  readonly grantPrefix?: string;
  readonly includePolicyRevision: boolean;
}

export const OWNER_GRANT_WIRE_POLICY: Readonly<Record<OwnerGrantLogicalTool, OwnerGrantWirePolicy>> = Object.freeze({
  'registry.getSite': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: true,
  }),
  'registry.listSiteAssets': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: true,
  }),
  'registry.getSiteAssetModel': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: true,
  }),
  'analytics.getEnergySeries': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: false,
  }),
  'analytics.semanticQuery': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: false,
  }),
  'telemetry.history.aggregateDeviceHistory': Object.freeze({
    grantHeader: 'X-Delegation-Grant',
    includePolicyRevision: false,
  }),
  'telemetry.current.getDeviceObservationSnapshot': Object.freeze({
    grantHeader: 'Authorization',
    grantPrefix: 'Bearer ',
    includePolicyRevision: false,
  }),
  'alarm.listAlarms': Object.freeze({
    grantHeader: 'X-Alarm-Read-Context',
    includePolicyRevision: false,
  }),
  'fdd.listFindings': Object.freeze({
    grantHeader: 'X-FDD-Context',
    includePolicyRevision: false,
  }),
  'workorder.listWorkOrders': Object.freeze({
    grantHeader: 'X-Work-Order-Read-Context',
    includePolicyRevision: false,
  }),
});

export interface OwnerReaderHttpConfig {
  readonly baseUrl: string;
  readonly fetchImplementation?: typeof fetch;
  readonly requestTimeoutMs?: number;
  readonly maximumResponseBytes?: number;
}

export interface NormalizedOwnerReaderHttpConfig {
  readonly baseUrl: string;
  readonly fetchImplementation: typeof fetch;
  readonly requestTimeoutMs: number;
  readonly maximumResponseBytes: number;
}

const defaultRequestTimeoutMs = 5_000;
const defaultMaximumResponseBytes = 1_048_576;

const validTraceparent = (value: string): boolean => {
  const normalized = value.toLowerCase();
  return /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/u.test(normalized)
    && normalized.slice(3, 35) !== '0'.repeat(32)
    && normalized.slice(36, 52) !== '0'.repeat(16);
};

const requirePositiveSafeInteger = (value: number, label: string): number => {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive safe integer.`);
  }
  return value;
};

export const normalizeOwnerReaderHttpConfig = (
  input: OwnerReaderHttpConfig,
): NormalizedOwnerReaderHttpConfig => {
  const parsed = new URL(input.baseUrl);
  if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:')
    || parsed.username !== ''
    || parsed.password !== ''
    || parsed.search !== ''
    || parsed.hash !== '') {
    throw new Error('Owner Reader baseUrl must be an HTTP origin without credentials or query data.');
  }
  const pathname = parsed.pathname.replace(/\/+$/, '');
  return Object.freeze({
    baseUrl: `${parsed.origin}${pathname}`,
    fetchImplementation: input.fetchImplementation ?? globalThis.fetch.bind(globalThis),
    requestTimeoutMs: requirePositiveSafeInteger(
      input.requestTimeoutMs ?? defaultRequestTimeoutMs,
      'requestTimeoutMs',
    ),
    maximumResponseBytes: requirePositiveSafeInteger(
      input.maximumResponseBytes ?? defaultMaximumResponseBytes,
      'maximumResponseBytes',
    ),
  });
};

const requireAuthorizationContext = (
  context: OwnerReadContext,
  logicalTool: OwnerGrantLogicalTool,
  includePolicyRevision: boolean,
): {
  readonly delegationGrant: string;
  readonly policyRevision: string | undefined;
} => {
  const { authorization } = context;
  const delegationGrant = authorization.toolDelegationGrants?.[logicalTool]
    ?? authorization.delegationGrant;
  const policyRevision = authorization.policyRevision;
  const traceparent = authorization.traceparent;
  const tracestate = authorization.tracestate;
  if (authorization.decision !== 'ALLOW'
    || authorization.decisionId.trim().length === 0
    || delegationGrant === undefined
    || delegationGrant.trim().length === 0
    || (includePolicyRevision
      && (policyRevision === undefined || policyRevision.trim().length === 0))
    || (traceparent !== undefined && !validTraceparent(traceparent))
    || (tracestate !== undefined
      && (tracestate.length > 512
        || tracestate.includes(String.fromCharCode(13))
        || tracestate.includes(String.fromCharCode(10))))
    || context.sessionId.trim().length === 0
    || context.runId.trim().length === 0
    || context.correlationId.trim().length === 0) {
    throw new OwnerReadError(
      'OWNER_REQUEST_INVALID',
      'The Owner READ authorization context is incomplete.',
    );
  }
  return { delegationGrant, policyRevision };
};

export const createOwnerHeaders = (
  requestId: string,
  context: OwnerReadContext,
  options: {
    readonly logicalTool: OwnerGrantLogicalTool;
    readonly hasBody: boolean;
    readonly includePolicyRevision?: boolean;
  },
): Record<string, string> => {
  const policy = OWNER_GRANT_WIRE_POLICY[options.logicalTool];
  const includePolicyRevision = policy.includePolicyRevision && (options.includePolicyRevision ?? true);
  const authorization = requireAuthorizationContext(
    context,
    options.logicalTool,
    includePolicyRevision,
  );
  if (requestId.trim().length === 0) {
    throw new OwnerReadError('OWNER_REQUEST_INVALID', 'The Owner READ request identity is invalid.');
  }
  const headers: Record<string, string> = {
    Accept: 'application/json, application/problem+json',
    [policy.grantHeader]: `${policy.grantPrefix ?? ''}${authorization.delegationGrant}`,
    'X-Request-ID': requestId,
    'X-Operations-Agent-Run-ID': context.runId,
    'X-Operations-Correlation-ID': context.correlationId,
  };
  if (options.hasBody) headers['Content-Type'] = 'application/json';
  if (includePolicyRevision) {
    const policyRevision = authorization.policyRevision;
    if (policyRevision === undefined) {
      throw new OwnerReadError(
        'OWNER_REQUEST_INVALID',
        'The Owner READ authorization context is incomplete.',
      );
    }
    headers['X-Route-Policy-Revision'] = policyRevision;
  }
  if (context.authorization.traceparent !== undefined) {
    headers.traceparent = context.authorization.traceparent;
  }
  if (context.authorization.tracestate !== undefined) {
    headers.tracestate = context.authorization.tracestate;
  }
  return headers;
};

export const readAuthorizedOwnerJson = async (
  config: NormalizedOwnerReaderHttpConfig,
  input: {
    readonly requestId: string;
    readonly logicalTool: OwnerGrantLogicalTool;
    readonly context: OwnerReadContext;
    readonly path: string;
    readonly method: 'GET' | 'POST';
    readonly body?: string;
  },
): Promise<unknown> => fetchOwnerJson(config, {
  path: input.path,
  method: input.method,
  headers: createOwnerHeaders(input.requestId, input.context, {
    logicalTool: input.logicalTool,
    hasBody: input.body !== undefined,
  }),
  ...(input.body === undefined ? {} : { body: input.body }),
});

const mapStatusToError = (status: number): OwnerReadError => {
  if (status === 401 || status === 403 || status === 404) {
    return new OwnerReadError(
      'OWNER_RESOURCE_NOT_FOUND',
      'The requested Owner resource was not found.',
    );
  }
  if (status === 408 || status === 504) {
    return new OwnerReadError('OWNER_READ_TIMEOUT', 'The Owner READ request timed out.');
  }
  if (status === 413) {
    return new OwnerReadError(
      'OWNER_RESPONSE_TOO_LARGE',
      'The Owner READ response exceeded the accepted size.',
    );
  }
  if (status >= 400 && status < 500 && status !== 429) {
    return new OwnerReadError(
      'OWNER_REQUEST_INVALID',
      'The Owner rejected the fixed READ request.',
    );
  }
  return new OwnerReadError(
    'OWNER_READ_UNAVAILABLE',
    'The authoritative Owner is temporarily unavailable.',
  );
};

const cancelResponseBody = async (response: Response): Promise<void> => {
  try {
    await response.body?.cancel();
  } catch {
    // Connection cleanup must not replace the stable Application error.
  }
};

const readBoundedText = async (
  response: Response,
  maximumResponseBytes: number,
): Promise<string> => {
  const declaredLength = response.headers.get('content-length');
  if (declaredLength !== null) {
    const parsedLength = Number(declaredLength);
    if (!Number.isSafeInteger(parsedLength) || parsedLength < 0) {
      throw new OwnerReadError(
        'OWNER_RESPONSE_INVALID',
        'The Owner READ response metadata is invalid.',
      );
    }
    if (parsedLength > maximumResponseBytes) {
      await cancelResponseBody(response);
      throw new OwnerReadError(
        'OWNER_RESPONSE_TOO_LARGE',
        'The Owner READ response exceeded the accepted size.',
      );
    }
  }

  if (response.body === null) return '';
  const reader = response.body.getReader();
  const chunks: Buffer[] = [];
  let totalBytes = 0;
  while (true) {
    const next = await reader.read();
    if (next.done) break;
    if (next.value === undefined) continue;
    totalBytes += next.value.byteLength;
    if (totalBytes > maximumResponseBytes) {
      try {
        await reader.cancel();
      } catch {
        // Preserve the stable response-size error even if stream cancellation fails.
      }
      throw new OwnerReadError(
        'OWNER_RESPONSE_TOO_LARGE',
        'The Owner READ response exceeded the accepted size.',
      );
    }
    chunks.push(Buffer.from(next.value));
  }
  return Buffer.concat(chunks, totalBytes).toString('utf8');
};

export const fetchOwnerJson = async (
  config: NormalizedOwnerReaderHttpConfig,
  input: {
    readonly path: string;
    readonly method: 'GET' | 'POST';
    readonly headers: Readonly<Record<string, string>>;
    readonly body?: string;
  },
): Promise<unknown> => {
  const controller = new AbortController();
  let timeoutHandle: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timeoutHandle = setTimeout(() => {
      controller.abort();
      reject(new OwnerReadError('OWNER_READ_TIMEOUT', 'The Owner READ request timed out.'));
    }, config.requestTimeoutMs);
  });

  const operation = async (): Promise<unknown> => {
    const response = await config.fetchImplementation(`${config.baseUrl}${input.path}`, {
      method: input.method,
      headers: input.headers,
      signal: controller.signal,
      ...(input.body === undefined ? {} : { body: input.body }),
    });
    if (!response.ok) {
      await cancelResponseBody(response);
      throw mapStatusToError(response.status);
    }
    const contentType = response.headers.get('content-type')?.toLowerCase() ?? '';
    if (!contentType.includes('application/json')) {
      await cancelResponseBody(response);
      throw new OwnerReadError(
        'OWNER_RESPONSE_INVALID',
        'The Owner READ response was not valid JSON.',
      );
    }
    const text = await readBoundedText(response, config.maximumResponseBytes);
    try {
      return JSON.parse(text) as unknown;
    } catch (cause) {
      throw new OwnerReadError(
        'OWNER_RESPONSE_INVALID',
        'The Owner READ response was not valid JSON.',
        { cause },
      );
    }
  };

  try {
    return await Promise.race([operation(), timeout]);
  } catch (cause) {
    if (cause instanceof OwnerReadError) throw cause;
    if (controller.signal.aborted) {
      throw new OwnerReadError('OWNER_READ_TIMEOUT', 'The Owner READ request timed out.');
    }
    throw new OwnerReadError(
      'OWNER_READ_UNAVAILABLE',
      'The authoritative Owner is temporarily unavailable.',
      { cause },
    );
  } finally {
    if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
  }
};

export const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

export const hasExactKeys = (
  value: Record<string, unknown>,
  keys: readonly string[],
): boolean => {
  const actual = Object.keys(value);
  return actual.length === keys.length && actual.every((key) => keys.includes(key));
};

export const isNonEmptyString = (value: unknown): value is string => (
  typeof value === 'string' && value.trim().length > 0
);

const rfc3339InstantPattern = (
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u
);

export const isInstant = (value: unknown): value is string => (
  typeof value === 'string'
  && rfc3339InstantPattern.test(value)
  && Number.isFinite(Date.parse(value))
);
