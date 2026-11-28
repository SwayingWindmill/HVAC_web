import { gatewayRequest } from './gateway-client.ts';
import {
  agentSessionEventSchema,
  agentSessionEventTypes,
  agentSessionListSchema,
  agentSessionPaths,
  agentSessionSnapshotSchema,
  agentUsageSchema,
  type AgentSessionEvent,
  type AgentSessionSnapshot,
  type AgentUsage,
} from './generated/operationsAgentSessions.gen.ts';

// The Agent Session routes behind the Gateway. Responses are parsed with the generated
// contract; the event stream is a same-origin EventSource on the Browser Session cookie.

export class AssistantRequestError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, status: number) {
    super(code);
    this.name = 'AssistantRequestError';
    this.code = code;
    this.status = status;
  }
}

/** What the panel says when a request fails; plain words, no codes. */
export const assistantErrorText = (error: unknown): string => {
  if (!(error instanceof AssistantRequestError)) return '连接助手失败，请稍后再试。';
  switch (error.code) {
    case 'OPERATIONS_AGENT_UNAVAILABLE':
      return '这个环境还没有接入 AI 助手。';
    case 'OPERATIONS_RATE_LIMITED':
      return '提问太频繁了，请稍等一会儿再问。';
    case 'RESOURCE_NOT_FOUND':
      return '读不到这个站点的对话。';
    case 'SESSION_REVISION_CONFLICT':
      return '对话已在别处更新，已重新载入，请再发一次。';
    default:
      return '助手暂时没有回应，请稍后再试。';
  }
};

const send = async <T>(
  path: string,
  parse: (payload: unknown) => T,
  post?: { readonly csrfToken: string; readonly body: unknown },
): Promise<T> => {
  const result = await gatewayRequest(
    path,
    post ? { method: 'POST', csrfToken: post.csrfToken, body: post.body, parse } : { parse },
  );
  if (result.kind === 'ok') return result.value;
  if (result.kind === 'routeMissing') throw new AssistantRequestError('ROUTE_NOT_FOUND', result.status);
  if (result.kind === 'unavailable') throw new AssistantRequestError(result.problem?.code ?? 'UNKNOWN', result.status ?? 503);
  throw new AssistantRequestError(result.problem.code ?? 'UNKNOWN', result.problem.status);
};

export const listAssistantSessions = async (siteId: string): Promise<readonly AgentSessionSnapshot[]> => (
  send(agentSessionPaths.collection(siteId), (payload) => agentSessionListSchema.parse(payload))
);

/** The assistant's last 14 Site-local days on a Site; needs the audit.read capability. */
export const getAssistantUsage = async (siteId: string, timezone: string): Promise<AgentUsage> => (
  send(agentSessionPaths.usage(siteId, timezone), (payload) => agentUsageSchema.parse(payload))
);

export const createAssistantSession = async (siteId: string, csrfToken: string, message: string): Promise<AgentSessionSnapshot> => (
  send(agentSessionPaths.collection(siteId), (payload) => agentSessionSnapshotSchema.parse(payload), { csrfToken, body: { message } })
);

export const askInAssistantSession = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
  message: string,
): Promise<AgentSessionSnapshot> => (
  send(
    agentSessionPaths.run(siteId, sessionId),
    (payload) => agentSessionSnapshotSchema.parse(payload),
    { csrfToken, body: { expectedRevision, message } },
  )
);

export const cancelAssistantRun = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
): Promise<AgentSessionSnapshot> => (
  send(
    agentSessionPaths.cancel(siteId, sessionId),
    (payload) => agentSessionSnapshotSchema.parse(payload),
    { csrfToken, body: { expectedRevision } },
  )
);

export const answerAssistantQuestion = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
  requestArtifactId: string,
  value: string,
): Promise<AgentSessionSnapshot> => (
  send(
    agentSessionPaths.submitInput(siteId, sessionId),
    (payload) => agentSessionSnapshotSchema.parse(payload),
    { csrfToken, body: { expectedRevision, requestArtifactId, value } },
  )
);

/**
 * Follows a Session's events until the snapshot after its Run ends. Returns the close function.
 * Events that fail the contract end the stream rather than being shown half-parsed.
 */
export const followAssistantSession = (
  siteId: string,
  sessionId: string,
  onEvent: (event: AgentSessionEvent) => void,
  onEnd: (failed: boolean) => void,
): (() => void) => {
  const source = new EventSource(agentSessionPaths.events(siteId, sessionId));
  let ended = false;
  const end = (failed: boolean) => {
    if (ended) return;
    ended = true;
    source.close();
    onEnd(failed);
  };
  const handle = (message: MessageEvent<string>) => {
    const parsed = agentSessionEventSchema.safeParse(JSON.parse(message.data));
    if (!parsed.success) {
      end(true);
      return;
    }
    onEvent(parsed.data);
    if (parsed.data.type === 'session.snapshot' && parsed.data.payload.snapshot.session.status !== 'ACTIVE') end(false);
  };
  const onMessageEvent: EventListener = (event) => {
    if (event instanceof MessageEvent && typeof event.data === 'string') {
      handle(event);
    }
  };
  for (const type of agentSessionEventTypes) source.addEventListener(type, onMessageEvent);
  // The server closes the stream after the snapshot that ends the Run; a close before it is a failure.
  source.onerror = () => end(source.readyState !== EventSource.CLOSED);
  return () => end(false);
};
