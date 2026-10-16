import {
  agentSessionEventSchema,
  agentSessionEventTypes,
  agentSessionListSchema,
  agentSessionPaths,
  agentSessionSnapshotSchema,
  type AgentSessionEvent,
  type AgentSessionSnapshot,
} from '@/api/generated/operationsAgentSessions.gen';

// The Agent Session routes behind the Gateway. Responses are parsed with the generated
// contract; the event stream is a same-origin EventSource on the Browser Session cookie.

export class AssistantRequestError extends Error {
  constructor(readonly code: string, readonly status: number) {
    super(code);
    this.name = 'AssistantRequestError';
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

const send = async (path: string, init: RequestInit): Promise<unknown> => {
  const response = await fetch(path, { credentials: 'same-origin', ...init });
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const code = typeof payload === 'object' && payload !== null && 'code' in payload && typeof payload.code === 'string'
      ? payload.code
      : 'UNKNOWN';
    throw new AssistantRequestError(code, response.status);
  }
  return payload;
};

const read = (path: string) => send(path, { headers: { Accept: 'application/json, application/problem+json' } });

const post = (path: string, csrfToken: string, body: unknown) => send(path, {
  method: 'POST',
  headers: { Accept: 'application/json, application/problem+json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
  body: JSON.stringify(body),
});

export const listAssistantSessions = async (siteId: string): Promise<readonly AgentSessionSnapshot[]> => (
  agentSessionListSchema.parse(await read(agentSessionPaths.collection(siteId)))
);

export const createAssistantSession = async (siteId: string, csrfToken: string, message: string): Promise<AgentSessionSnapshot> => (
  agentSessionSnapshotSchema.parse(await post(agentSessionPaths.collection(siteId), csrfToken, { message }))
);

export const askInAssistantSession = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
  message: string,
): Promise<AgentSessionSnapshot> => (
  agentSessionSnapshotSchema.parse(await post(agentSessionPaths.run(siteId, sessionId), csrfToken, { expectedRevision, message }))
);

export const cancelAssistantRun = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
): Promise<AgentSessionSnapshot> => (
  agentSessionSnapshotSchema.parse(await post(agentSessionPaths.cancel(siteId, sessionId), csrfToken, { expectedRevision }))
);

export const answerAssistantQuestion = async (
  siteId: string,
  csrfToken: string,
  sessionId: string,
  expectedRevision: number,
  requestArtifactId: string,
  value: string,
): Promise<AgentSessionSnapshot> => (
  agentSessionSnapshotSchema.parse(await post(
    agentSessionPaths.submitInput(siteId, sessionId),
    csrfToken,
    { expectedRevision, requestArtifactId, value },
  ))
);

/**
 * Follows a Session's events until its terminal snapshot. Returns the close function.
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
  for (const type of agentSessionEventTypes) source.addEventListener(type, handle as EventListener);
  // The server closes the stream after the terminal snapshot; a close before it is a failure.
  source.onerror = () => end(source.readyState !== EventSource.CLOSED);
  return () => end(false);
};
