// The Agent Session routes behind the Gateway, as the web's api/agent-sessions.ts calls them. Pages
// pass the platform Gateway; the event stream is read in chunks over the same Session.

import { agentEventReader } from './agent-stream';
import type { AgentSessionEvent, AgentSessionSnapshot } from './agent-types';
import { GatewayError, NETWORK_TEXT, type Gateway } from './gateway';

const collection = (siteId: string) => `/api/v1/sites/${encodeURIComponent(siteId)}/operations/agent-sessions`;
const session = (siteId: string, sessionId: string) => `${collection(siteId)}/${encodeURIComponent(sessionId)}`;

/** My conversations on the Site, the newest started first. */
export const listSessions = (gateway: Gateway, siteId: string) => gateway.get<AgentSessionSnapshot[]>(collection(siteId));

export const startSession = (gateway: Gateway, siteId: string, message: string) =>
  gateway.post<AgentSessionSnapshot>(collection(siteId), { message });

export const askInSession = (gateway: Gateway, siteId: string, sessionId: string, expectedRevision: number, message: string) =>
  gateway.post<AgentSessionSnapshot>(`${session(siteId, sessionId)}:run`, { expectedRevision, message });

export const answerQuestion = (gateway: Gateway, siteId: string, sessionId: string, expectedRevision: number, requestArtifactId: string, value: string) =>
  gateway.post<AgentSessionSnapshot>(`${session(siteId, sessionId)}:submit-input`, { expectedRevision, requestArtifactId, value });

export const stopRun = (gateway: Gateway, siteId: string, sessionId: string, expectedRevision: number) =>
  gateway.post<AgentSessionSnapshot>(`${session(siteId, sessionId)}:cancel`, { expectedRevision });

/**
 * Follows a conversation's events until the snapshot after its Run ends, then calls `onEnd(false)`;
 * a stream that ends, fails or sends an unreadable event before that calls `onEnd(true)`. Returns
 * the function that stops it, after which nothing more is called.
 */
export function followSession(
  gateway: Gateway,
  siteId: string,
  sessionId: string,
  onEvent: (event: AgentSessionEvent) => void,
  onEnd: (failed: boolean) => void,
): () => void {
  let ended = false;
  const end = (failed: boolean) => {
    if (ended) return;
    ended = true;
    onEnd(failed);
  };
  const read = agentEventReader((event) => {
    if (ended) return;
    onEvent(event);
    if (event.type === 'session.snapshot' && event.payload.snapshot.session.status !== 'ACTIVE') end(false);
  });
  const stream = gateway.stream(`${session(siteId, sessionId)}/events`, (chunk) => {
    try {
      read(chunk);
    } catch {
      // The Gateway checked every event; one that still does not parse ends the stream as a failure.
      end(true);
      stream.abort();
    }
  });
  // The Gateway closes the stream after the snapshot that ends the Run; an end before it is a failure.
  stream.done.then(() => end(true), () => end(true));
  return () => {
    ended = true;
    stream.abort();
  };
}

/** What the page says when a request fails; plain words, no codes (the web's assistantErrorText). */
export function assistantErrorText(error: unknown): string {
  if (!(error instanceof GatewayError)) return '连接助手失败，请稍后再试。';
  switch (error.code) {
    case 'NETWORK_UNAVAILABLE':
      return NETWORK_TEXT;
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
}
