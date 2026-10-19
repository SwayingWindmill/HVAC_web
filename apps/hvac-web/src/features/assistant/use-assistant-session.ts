import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { AgentSessionEvent, AgentSessionSnapshot } from '@/api/generated/operationsAgentSessions.gen';
import {
  answerAssistantQuestion,
  askInAssistantSession,
  assistantErrorText,
  cancelAssistantRun,
  createAssistantSession,
  followAssistantSession,
  listAssistantSessions,
} from '@/api/agent-sessions';
import { assistantThreadFrom, type AssistantThread } from './thread-model';

const sessionsKey = (siteId: string) => ['assistant', 'sessions', siteId] as const;

const latest = (sessions: readonly AgentSessionSnapshot[]): AgentSessionSnapshot | null => (
  sessions.reduce<AgentSessionSnapshot | null>((best, candidate) => (
    best === null || candidate.session.updatedAt > best.session.updatedAt ? candidate : best
  ), null)
);

/** One of my conversations on the Site, as the history lists it (#639). */
export interface AssistantConversation {
  readonly id: string;
  readonly question: string;
  readonly startedAt: number;
  readonly status: AgentSessionSnapshot['session']['status'];
}

export interface AssistantSession {
  readonly thread: AssistantThread;
  /** My conversations on the Site, the newest started first, as the Agent lists them. */
  readonly history: readonly AssistantConversation[];
  readonly loading: boolean;
  readonly error: string | null;
  readonly ask: (text: string) => Promise<void>;
  readonly answer: (value: string) => Promise<void>;
  readonly cancel: () => Promise<void>;
  readonly startNew: () => void;
  readonly open: (sessionId: string) => void;
}

/**
 * The Site's current conversation: the latest Session, or a new one once the operator starts
 * one. A Session with a running Run is followed live until the Run ends.
 */
export function useAssistantSession(siteId: string, csrfToken: string): AssistantSession {
  const queryClient = useQueryClient();
  const sessions = useQuery({
    queryKey: sessionsKey(siteId),
    queryFn: () => listAssistantSessions(siteId),
    retry: false,
  });
  // undefined follows the latest Session; null is a new conversation not yet sent.
  const [selected, setSelected] = useState<{ readonly siteId: string; readonly sessionId: string | null } | undefined>();
  const [live, setLive] = useState<{ readonly sessionId: string; readonly events: readonly AgentSessionEvent[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const following = useRef<{ readonly sessionId: string; readonly close: () => void } | null>(null);

  const selectedHere = selected?.siteId === siteId ? selected : undefined;
  const snapshot = useMemo(() => {
    const items = sessions.data ?? [];
    if (selectedHere === undefined) return latest(items);
    if (selectedHere.sessionId === null) return null;
    return items.find((item) => item.session.id === selectedHere.sessionId) ?? null;
  }, [sessions.data, selectedHere]);

  const thread = useMemo(
    () => assistantThreadFrom(snapshot, live !== null && live.sessionId === snapshot?.session.id ? live.events : []),
    [snapshot, live],
  );

  const stopFollowing = useCallback(() => {
    following.current?.close();
    following.current = null;
  }, []);

  const follow = useCallback((sessionId: string) => {
    if (following.current?.sessionId === sessionId) return;
    stopFollowing();
    setLive({ sessionId, events: [] });
    const close = followAssistantSession(
      siteId,
      sessionId,
      (event) => setLive((current) => (current?.sessionId === sessionId ? { sessionId, events: [...current.events, event] } : current)),
      (failed) => {
        if (following.current?.sessionId === sessionId) following.current = null;
        if (failed) setError('与助手的连接中断了，已重新载入对话。');
        void queryClient.invalidateQueries({ queryKey: sessionsKey(siteId) });
      },
    );
    following.current = { sessionId, close };
  }, [queryClient, siteId, stopFollowing]);

  useEffect(() => stopFollowing, [stopFollowing, siteId]);

  // A Session found running (another tab, a reload) is followed as well.
  const runningSessionId = snapshot?.session.status === 'ACTIVE' ? snapshot.session.id : null;
  useEffect(() => {
    if (runningSessionId !== null) follow(runningSessionId);
  }, [follow, runningSessionId]);

  const store = useCallback((next: AgentSessionSnapshot) => {
    queryClient.setQueryData<readonly AgentSessionSnapshot[]>(sessionsKey(siteId), (items = []) => [
      next,
      ...items.filter((item) => item.session.id !== next.session.id),
    ]);
    setSelected({ siteId, sessionId: next.session.id });
  }, [queryClient, siteId]);

  const run = useCallback(async (action: () => Promise<AgentSessionSnapshot>) => {
    setError(null);
    try {
      const next = await action();
      store(next);
      if (next.session.status === 'ACTIVE') follow(next.session.id);
    } catch (failure) {
      setError(assistantErrorText(failure));
      await queryClient.invalidateQueries({ queryKey: sessionsKey(siteId) });
    }
  }, [follow, queryClient, siteId, store]);

  const ask = useCallback((text: string) => run(() => (
    thread.sessionId === null || thread.revision === null
      ? createAssistantSession(siteId, csrfToken, text)
      : askInAssistantSession(siteId, csrfToken, thread.sessionId, thread.revision, text)
  )), [csrfToken, run, siteId, thread.revision, thread.sessionId]);

  const answer = useCallback((value: string) => {
    const pending = thread.pendingInput;
    if (pending === null || thread.sessionId === null || thread.revision === null) return Promise.resolve();
    return run(() => answerAssistantQuestion(siteId, csrfToken, thread.sessionId!, thread.revision!, pending.id, value));
  }, [csrfToken, run, siteId, thread.pendingInput, thread.revision, thread.sessionId]);

  const cancel = useCallback(() => {
    if (thread.sessionId === null || thread.revision === null) return Promise.resolve();
    return run(() => cancelAssistantRun(siteId, csrfToken, thread.sessionId!, thread.revision!));
  }, [csrfToken, run, siteId, thread.revision, thread.sessionId]);

  // null starts a new conversation; an id opens one from the history.
  const select = useCallback((sessionId: string | null) => {
    stopFollowing();
    setLive(null);
    setError(null);
    setSelected({ siteId, sessionId });
  }, [siteId, stopFollowing]);
  const startNew = useCallback(() => select(null), [select]);

  // A conversation always starts with a question, so the first message is the operator's.
  const history = useMemo(() => (sessions.data ?? []).map(({ session, messages }) => ({
    id: session.id,
    question: messages.find((message) => message.role === 'OPERATOR')!.content,
    startedAt: session.createdAt,
    status: session.status,
  })), [sessions.data]);

  return {
    thread,
    loading: sessions.isPending,
    error: error ?? (sessions.error === null ? null : assistantErrorText(sessions.error)),
    ask,
    answer,
    cancel,
    startNew,
    open: select,
    history,
  };
}
