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
} from './session-client';
import { assistantThreadFrom, type AssistantThread } from './thread-model';

const sessionsKey = (siteId: string) => ['assistant', 'sessions', siteId] as const;

const latest = (sessions: readonly AgentSessionSnapshot[]): AgentSessionSnapshot | null => (
  sessions.reduce<AgentSessionSnapshot | null>((best, candidate) => (
    best === null || candidate.session.updatedAt > best.session.updatedAt ? candidate : best
  ), null)
);

export interface AssistantSession {
  readonly thread: AssistantThread;
  readonly loading: boolean;
  readonly error: string | null;
  readonly ask: (text: string) => Promise<void>;
  readonly answer: (value: string) => Promise<void>;
  readonly cancel: () => Promise<void>;
  readonly startNew: () => void;
}

/**
 * The Site's current conversation: the latest Session, or a new one once the operator starts
 * one. A Session with a running Run is followed live until its terminal snapshot.
 */
export function useAssistantSession(siteId: string, csrfToken: string, enabled: boolean): AssistantSession {
  const queryClient = useQueryClient();
  const sessions = useQuery({
    queryKey: sessionsKey(siteId),
    queryFn: () => listAssistantSessions(siteId),
    enabled,
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

  const startNew = useCallback(() => {
    stopFollowing();
    setLive(null);
    setError(null);
    setSelected({ siteId, sessionId: null });
  }, [siteId, stopFollowing]);

  return {
    thread,
    loading: sessions.isPending && enabled,
    error: error ?? (sessions.error === null ? null : assistantErrorText(sessions.error)),
    ask,
    answer,
    cancel,
    startNew,
  };
}
