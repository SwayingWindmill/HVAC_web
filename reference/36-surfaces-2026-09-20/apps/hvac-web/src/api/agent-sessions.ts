import { z } from 'zod';

import { createPlatformGatewayClient } from './generated/platformGateway.gen';
import {
  agentSessionEventSchema,
  agentSessionInputRequestSchema,
  agentSessionListSchema,
  agentSessionMessageRequestSchema,
  agentSessionPaths,
  agentSessionRevisionRequestSchema,
  agentSessionRunRequestSchema,
  agentSessionSnapshotSchema,
  type AgentSessionEvent,
  type AgentSessionInputRequest,
  type AgentSessionSnapshot,
} from './generated/operationsAgentSessions.gen';

const problemSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  retryable: z.boolean().optional(),
}).passthrough();

export class AgentSessionApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;

  constructor(status: number, code: string, message: string, retryable = false) {
    super(message);
    this.name = 'AgentSessionApiError';
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

export interface AgentSessionRequestOptions {
  readonly trustedTenantId: string;
  readonly trustedSiteId: string;
  readonly csrfToken?: string;
  readonly signal?: AbortSignal;
  readonly fetchImplementation?: typeof fetch;
  readonly baseUrl?: string;
}

const platformClient = createPlatformGatewayClient();

async function csrfCapability(options: AgentSessionRequestOptions): Promise<string> {
  if (options.csrfToken) return options.csrfToken;
  const principal = await platformClient.getCurrentPrincipal();
  if (principal.data.context.tenantId !== options.trustedTenantId) {
    throw new AgentSessionApiError(503, 'AGENT_SESSION_SCOPE_INVALID', '当前登录 Session 的 Tenant 已改变。');
  }
  return principal.data.session.csrfToken;
}

function fetchFor(options: AgentSessionRequestOptions): typeof fetch {
  return options.fetchImplementation ?? globalThis.fetch.bind(globalThis);
}

async function problemFrom(response: Response): Promise<AgentSessionApiError> {
  const value: unknown = await response.json().catch(() => ({}));
  const parsed = problemSchema.parse(value);
  return new AgentSessionApiError(
    response.status,
    parsed.code ?? 'AGENT_SESSION_UNAVAILABLE',
    parsed.detail ?? parsed.title ?? 'AI 运维调查暂时不可用。',
    parsed.retryable ?? false,
  );
}

function ensureSnapshotScope(snapshot: AgentSessionSnapshot, options: AgentSessionRequestOptions): AgentSessionSnapshot {
  if (snapshot.session.tenantId !== options.trustedTenantId || snapshot.session.siteId !== options.trustedSiteId) {
    throw new AgentSessionApiError(502, 'AGENT_SESSION_SCOPE_INVALID', 'AI 运维调查响应超出当前已验证 Site Scope。');
  }
  return snapshot;
}

async function jsonRequest(
  path: string,
  init: RequestInit,
  options: AgentSessionRequestOptions,
): Promise<AgentSessionSnapshot> {
  const response = await fetchFor(options)(`${options.baseUrl ?? ''}${path}`, {
    ...init,
    credentials: 'same-origin',
    signal: options.signal ?? init.signal,
    headers: {
      Accept: 'application/json, application/problem+json',
      ...Object.fromEntries(new Headers(init.headers)),
    },
  });
  if (!response.ok) throw await problemFrom(response);
  return ensureSnapshotScope(agentSessionSnapshotSchema.parse(await response.json()), options);
}

export async function listAgentSessions(options: AgentSessionRequestOptions): Promise<readonly AgentSessionSnapshot[]> {
  const response = await fetchFor(options)(`${options.baseUrl ?? ''}${agentSessionPaths.collection(options.trustedSiteId)}`, {
    method: 'GET',
    credentials: 'same-origin',
    signal: options.signal,
    headers: { Accept: 'application/json, application/problem+json' },
  });
  if (!response.ok) throw await problemFrom(response);
  const sessions = agentSessionListSchema.parse(await response.json());
  for (const snapshot of sessions) ensureSnapshotScope(snapshot, options);
  return sessions;
}

export async function createAgentSession(message: string, options: AgentSessionRequestOptions): Promise<AgentSessionSnapshot> {
  const body = agentSessionMessageRequestSchema.parse({ message });
  const csrfToken = await csrfCapability(options);
  return jsonRequest(agentSessionPaths.collection(options.trustedSiteId), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify(body),
  }, options);
}

export function getAgentSession(sessionId: string, options: AgentSessionRequestOptions): Promise<AgentSessionSnapshot> {
  return jsonRequest(agentSessionPaths.detail(options.trustedSiteId, sessionId), { method: 'GET' }, options);
}

export async function startAgentSessionRun(
  sessionId: string,
  expectedRevision: number,
  message: string,
  options: AgentSessionRequestOptions,
): Promise<AgentSessionSnapshot> {
  const body = agentSessionRunRequestSchema.parse({ expectedRevision, message });
  const csrfToken = await csrfCapability(options);
  return jsonRequest(agentSessionPaths.run(options.trustedSiteId, sessionId), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify(body),
  }, options);
}

export async function cancelAgentSession(
  sessionId: string,
  expectedRevision: number,
  options: AgentSessionRequestOptions,
): Promise<AgentSessionSnapshot> {
  const body = agentSessionRevisionRequestSchema.parse({ expectedRevision });
  const csrfToken = await csrfCapability(options);
  return jsonRequest(agentSessionPaths.cancel(options.trustedSiteId, sessionId), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify(body),
  }, options);
}

export async function submitAgentSessionInput(
  sessionId: string,
  input: AgentSessionInputRequest,
  options: AgentSessionRequestOptions,
): Promise<AgentSessionSnapshot> {
  const body = agentSessionInputRequestSchema.parse(input);
  const csrfToken = await csrfCapability(options);
  return jsonRequest(agentSessionPaths.submitInput(options.trustedSiteId, sessionId), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
    body: JSON.stringify(body),
  }, options);
}

function parseEventBlock(block: string): AgentSessionEvent {
  let eventName = '';
  let data = '';
  for (const line of block.split('\n')) {
    if (line.startsWith('event: ') && eventName === '') eventName = line.slice(7);
    else if (line.startsWith('data: ') && data === '') data = line.slice(6);
    else if (line !== '') throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查事件流格式无效。', true);
  }
  if (!eventName || !data) throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查事件流不完整。', true);
  const parsed = agentSessionEventSchema.parse(JSON.parse(data) as unknown);
  if (parsed.type !== eventName) throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查事件名称与数据不一致。', true);
  return parsed;
}

export async function streamAgentSessionEvents(
  sessionId: string,
  options: AgentSessionRequestOptions,
  onEvent: (event: AgentSessionEvent) => void,
): Promise<void> {
  const response = await fetchFor(options)(`${options.baseUrl ?? ''}${agentSessionPaths.events(options.trustedSiteId, sessionId)}`, {
    method: 'GET',
    credentials: 'same-origin',
    signal: options.signal,
    headers: { Accept: 'text/event-stream, application/problem+json' },
  });
  if (!response.ok) throw await problemFrom(response);
  if (!response.headers.get('content-type')?.toLowerCase().startsWith('text/event-stream') || response.body === null) {
    throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查没有返回有效事件流。', true);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let receivedSnapshot = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done }).replace(/\r\n?/g, '\n');
      let boundary = buffer.indexOf('\n\n');
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        if (block !== '') {
          const event = parseEventBlock(block);
          if (!receivedSnapshot) {
            if (event.type !== 'session.snapshot') {
              throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查重连必须从权威 Session 快照开始。', true);
            }
            ensureSnapshotScope(event.payload.snapshot, options);
            receivedSnapshot = true;
          }
          if (event.sessionId !== sessionId) {
            throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查事件与当前 Session 不一致。', true);
          }
          onEvent(event);
        }
        boundary = buffer.indexOf('\n\n');
      }
      if (done) break;
    }
  } finally {
    reader.releaseLock();
  }
  if (!receivedSnapshot || buffer.trim() !== '') {
    throw new AgentSessionApiError(502, 'AGENT_SESSION_STREAM_INVALID', 'AI 运维调查事件流提前结束。', true);
  }
}

export type {
  AgentSessionEvent,
  AgentSessionInputRequest,
  AgentSessionSnapshot,
} from './generated/operationsAgentSessions.gen';
