import { z } from 'zod';
import { createPlatformGatewayClient } from './generated/platformGateway.gen';
import {
  operationsInvestigationListSchema,
  operationsInvestigationViewSchema,
  operationsOperatorInputSubmissionRequestSchema,
  operationsOperatorInputSubmissionSchema,
  parseOperationsAgUiEventStream,
  type OperationsAgUiStreamBatch,
  type OperationsAgUiStreamRecovery,
  type OperationsInvestigationList,
  type OperationsInvestigationView,
  type OperationsOperatorInputSubmission,
  type OperationsOperatorInputValues,
  type ParsedOperationsAgUiEvent,
} from './operations-contract';

const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

export class OperationsApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(status: number, code: string, message: string, retryable = false) {
    super(message);
    this.name = 'OperationsApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export interface ScopedOperationsRequestOptions {
  readonly trustedTenantId: string;
  readonly trustedSiteId: string;
  readonly csrfToken?: string;
  readonly signal?: AbortSignal;
  readonly recoveryPosition?: string;
  readonly fetchImplementation?: typeof fetch;
  readonly baseUrl?: string;
}

export interface SubmitOperatorInputCommand {
  readonly requestId: string;
  readonly expectedRevision: number;
  readonly idempotencyKey: string;
  readonly values: OperationsOperatorInputValues;
}

const platformClient = createPlatformGatewayClient();

async function csrfCapability(options: ScopedOperationsRequestOptions): Promise<string> {
  if (options.csrfToken) return options.csrfToken;
  const principal = await platformClient.getCurrentPrincipal();
  if (principal.data.context.tenantId !== options.trustedTenantId) {
    throw new OperationsApiError(503, 'OPERATIONS_SCOPE_INVALID', 'Session Organization 已改变。');
  }
  return principal.data.session.csrfToken;
}

const pathFor = (siteId: string, suffix = '') => (
  `/api/v1/sites/${encodeURIComponent(siteId)}/operations/investigations${suffix}`
);

const ensureScope = (
  investigation: OperationsInvestigationView,
  options: ScopedOperationsRequestOptions,
): OperationsInvestigationView => {
  if (investigation.scope.tenantId !== options.trustedTenantId
    || investigation.scope.siteId !== options.trustedSiteId) {
    throw new OperationsApiError(
      503,
      'OPERATIONS_SCOPE_INVALID',
      'Operations Investigation 超出当前已验证 Site Scope。',
    );
  }
  return investigation;
};

async function problemFrom(response: Response): Promise<OperationsApiError> {
  const payload: unknown = await response.json().catch(() => ({}));
  const parsed = problemSchema.parse(payload);
  return new OperationsApiError(
    response.status,
    parsed.code ?? 'OPERATIONS_UNAVAILABLE',
    parsed.detail ?? parsed.title ?? 'Operations Investigation 暂时不可用。',
    parsed.retryable ?? false,
  );
}

async function investigationRequest(
  path: string,
  init: RequestInit,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationView> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}${path}`, {
    ...init,
    credentials: 'same-origin',
    signal: options.signal ?? init.signal,
    headers: {
      Accept: 'application/json, application/problem+json',
      ...Object.fromEntries(new Headers(init.headers)),
    },
  });
  if (!response.ok) throw await problemFrom(response);
  const payload: unknown = await response.json();
  return ensureScope(operationsInvestigationViewSchema.parse(payload), options);
}

export async function listSiteNightEnergyInvestigations(
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationList> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(`${options.baseUrl ?? ''}${pathFor(options.trustedSiteId)}`, {
    method: 'GET',
    credentials: 'same-origin',
    signal: options.signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  if (!response.ok) throw await problemFrom(response);
  const parsed = operationsInvestigationListSchema.parse(await response.json());
  if (parsed.investigations.some((investigation) => (
    investigation.scope.tenantId !== options.trustedTenantId
    || investigation.scope.siteId !== options.trustedSiteId
  ))) {
    throw new OperationsApiError(
      503,
      'OPERATIONS_SCOPE_INVALID',
      'Operations Investigation 列表超出当前已验证 Site Scope。',
    );
  }
  return parsed;
}

export async function startSiteNightEnergyInvestigation(
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationView> {
  const csrfToken = await csrfCapability(options);
  return investigationRequest(pathFor(options.trustedSiteId), {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': csrfToken,
    },
    body: '{}',
  }, options);
}

export function getSiteNightEnergyInvestigation(
  investigationId: string,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationView> {
  return investigationRequest(
    pathFor(options.trustedSiteId, `/${encodeURIComponent(investigationId)}`),
    { method: 'GET' },
    options,
  );
}

export async function advanceSiteNightEnergyInvestigation(
  investigationId: string,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationView> {
  const csrfToken = await csrfCapability(options);
  return investigationRequest(
    pathFor(options.trustedSiteId, `/${encodeURIComponent(investigationId)}:advance`),
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrfToken,
      },
      body: '{}',
    },
    options,
  );
}

export async function cancelSiteNightEnergyInvestigation(
  investigationId: string,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsInvestigationView> {
  const csrfToken = await csrfCapability(options);
  return investigationRequest(
    pathFor(options.trustedSiteId, `/${encodeURIComponent(investigationId)}:cancel`),
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrfToken,
      },
      body: '{}',
    },
    options,
  );
}

export async function submitSiteNightEnergyOperatorInput(
  investigationId: string,
  command: SubmitOperatorInputCommand,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsOperatorInputSubmission> {
  const idempotencyKey = command.idempotencyKey.trim();
  if (!idempotencyKey || idempotencyKey.length > 256) {
    throw new OperationsApiError(
      400,
      'IDEMPOTENCY_KEY_REQUIRED',
      'Operator Input 提交需要有效的 Idempotency Key。',
    );
  }
  const requestBody = operationsOperatorInputSubmissionRequestSchema.parse({
    schemaVersion: 1,
    requestId: command.requestId,
    expectedRevision: command.expectedRevision,
    values: command.values,
  });
  const csrfToken = await csrfCapability(options);
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(
    `${options.baseUrl ?? ''}${pathFor(options.trustedSiteId, `/${encodeURIComponent(investigationId)}:submit-operator-input`)}`,
    {
      method: 'POST',
      credentials: 'same-origin',
      signal: options.signal,
      headers: {
        Accept: 'application/json, application/problem+json',
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrfToken,
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(requestBody),
    },
  );
  if (!response.ok) throw await problemFrom(response);
  const parsed = operationsOperatorInputSubmissionSchema.parse(await response.json());
  if (parsed.investigation.id !== investigationId) {
    throw new OperationsApiError(
      502,
      'OPERATIONS_CONTRACT_INVALID',
      'Operator Input 响应与请求的 Investigation 不一致。',
      true,
    );
  }
  ensureScope(parsed.investigation, options);
  return parsed;
}

const streamPositionPattern = /^(0|[1-9]\d*):(0|[1-9]\d*)$/u;

function streamRecoveryFrom(
  response: Response,
  events: readonly ParsedOperationsAgUiEvent[],
  requestedPosition?: string,
): OperationsAgUiStreamRecovery {
  const mode = response.headers.get('X-Operations-Recovery-Mode');
  const reason = response.headers.get('X-Operations-Recovery-Reason');
  const snapshotPosition = response.headers.get('X-Operations-Snapshot-Position');
  const latestPosition = response.headers.get('X-Operations-Latest-Position');
  const replayFromPosition = response.headers.get('X-Operations-Replay-From');
  const snapshotMatch = snapshotPosition === null ? null : streamPositionPattern.exec(snapshotPosition);
  const latestMatch = latestPosition === null ? null : streamPositionPattern.exec(latestPosition);
  const snapshotEvent = events[1];
  const latestEvent = events.at(-1);
  const normalizedRequestedPosition = requestedPosition?.trim() ?? '';
  const validFullReason = mode === 'FULL_SNAPSHOT'
    && replayFromPosition === null
    && (normalizedRequestedPosition === ''
      ? reason === 'INITIAL'
      : reason === 'UNKNOWN' || reason === 'EXPIRED' || reason === 'CONFLICT');
  const validResume = mode === 'RESUME'
    && normalizedRequestedPosition !== ''
    && reason === 'VALID'
    && replayFromPosition === normalizedRequestedPosition
    && streamPositionPattern.test(replayFromPosition);
  if ((!validFullReason && !validResume)
    || snapshotMatch === null
    || latestMatch === null
    || snapshotMatch[1] !== latestMatch[1]
    || snapshotMatch[2] !== '1'
    || snapshotEvent?.id !== snapshotPosition
    || latestEvent?.id !== latestPosition) {
    throw new OperationsApiError(
      502,
      'OPERATIONS_STREAM_INVALID',
      'Operations Agent 返回了无效恢复元数据。',
      true,
    );
  }
  return Object.freeze({
    mode,
    reason,
    snapshotPosition,
    latestPosition,
    replayFromPosition,
  } as OperationsAgUiStreamRecovery);
}

export async function streamSiteNightEnergyInvestigationEvents(
  investigationId: string,
  options: ScopedOperationsRequestOptions,
): Promise<OperationsAgUiStreamBatch> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
  const response = await fetchImplementation(
    `${options.baseUrl ?? ''}${pathFor(options.trustedSiteId, `/${encodeURIComponent(investigationId)}/events`)}`,
    {
      method: 'GET',
      credentials: 'same-origin',
      signal: options.signal,
      headers: {
        Accept: 'text/event-stream, application/problem+json',
        ...(options.recoveryPosition?.trim()
          ? { 'Last-Event-ID': options.recoveryPosition.trim().slice(0, 128) }
          : {}),
      },
    },
  );
  if (!response.ok) throw await problemFrom(response);
  if (!(response.headers.get('content-type') ?? '').toLowerCase().startsWith('text/event-stream')) {
    throw new OperationsApiError(502, 'OPERATIONS_STREAM_INVALID', 'Operations Agent 返回了无效事件流。', true);
  }
  const events = parseOperationsAgUiEventStream(await response.text());
  const snapshot = events.find((item) => item.event.type === 'STATE_SNAPSHOT');
  if (snapshot?.event.type !== 'STATE_SNAPSHOT'
    || snapshot.event.snapshot.investigation.scope.tenantId !== options.trustedTenantId
    || snapshot.event.snapshot.investigation.scope.siteId !== options.trustedSiteId) {
    throw new OperationsApiError(503, 'OPERATIONS_SCOPE_INVALID', 'Operations 事件流超出当前已验证 Site Scope。');
  }
  return Object.freeze({
    events: Object.freeze(events),
    recovery: streamRecoveryFrom(response, events, options.recoveryPosition),
  });
}

export type {
  OperationsAgUiEvent,
  OperationsAgUiStreamBatch,
  OperationsAgUiStreamRecovery,
  OperationsAnalysisReference,
  OperationsEvidence,
  OperationsFinding,
  OperationsInvestigationList,
  OperationsInvestigationStateSnapshot,
  OperationsInvestigationSummary,
  OperationsInvestigationView,
  OperationsOperatorInputAccepted,
  OperationsOperatorInputRequest,
  OperationsOperatorInputSubmission,
  OperationsOperatorInputSubmissionRequest,
  OperationsOperatorInputValues,
  OperationsRequiredNext,
  OperationsRunResourceBudget,
  OperationsStreamRecoveryMode,
  OperationsStreamRecoveryReason,
  OperationsToolReceipt,
  ParsedOperationsAgUiEvent,
} from './operations-contract';
