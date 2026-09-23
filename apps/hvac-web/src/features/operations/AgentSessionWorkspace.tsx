import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';

import type { CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import {
  AgentSessionApiError,
  cancelAgentSession,
  createAgentSession,
  listAgentSessions,
  streamAgentSessionEvents,
  submitAgentSessionInput,
  type AgentSessionEvent,
  type AgentSessionSnapshot,
} from '@/api/agent-sessions';
import type { AgentInputRequestArtifact } from '@/api/generated/operationsAgentSessions.gen';
import './agent-session-workspace.css';

interface ProtectedAgentResource {
  readonly id: string;
  readonly kind: 'realtime';
  purge(reason: string): void | Promise<void>;
}

export interface AgentSessionWorkspaceProps {
  readonly site: Readonly<Site>;
  readonly principal: CurrentPrincipalResponse;
  readonly registerProtectedResource: (resource: ProtectedAgentResource) => () => void;
}

type ConnectionState = 'IDLE' | 'CONNECTING' | 'LIVE' | 'CLOSED';

function formatTime(value: number, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('zh-CN', {
      timeZone: timezone,
      dateStyle: 'short',
      timeStyle: 'medium',
    }).format(new Date(value));
  } catch {
    return new Date(value).toLocaleString();
  }
}

function latestPendingInput(snapshot: AgentSessionSnapshot | null): AgentInputRequestArtifact | null {
  if (snapshot?.session.status !== 'WAITING_FOR_INPUT') return null;
  const answered = new Set(
    snapshot.artifacts
      .filter((artifact) => artifact.kind === 'INPUT_RESPONSE')
      .map((artifact) => artifact.requestArtifactId),
  );
  for (let index = snapshot.artifacts.length - 1; index >= 0; index -= 1) {
    const artifact = snapshot.artifacts[index];
    if (artifact?.kind === 'INPUT_REQUEST' && !answered.has(artifact.id)) return artifact;
  }
  return null;
}

function StatusTag({ status }: { readonly status: AgentSessionSnapshot['session']['status'] }) {
  const label = status === 'ACTIVE'
    ? '调查中'
    : status === 'WAITING_FOR_INPUT'
      ? '等待输入'
      : status === 'COMPLETED'
        ? '已完成'
        : status === 'FAILED'
          ? '失败'
          : '已关闭';
  const className = status === 'ACTIVE'
    ? 'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/40 dark:text-blue-300'
    : status === 'WAITING_FOR_INPUT'
      ? 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300'
      : status === 'COMPLETED'
        ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300'
        : status === 'FAILED'
          ? 'border-destructive/30 bg-destructive/10 text-destructive'
          : undefined;
  return <Badge variant="outline" className={className}>{label}</Badge>;
}

export function AgentSessionWorkspace({ site, principal, registerProtectedResource }: AgentSessionWorkspaceProps) {
  const tenantId = principal.context.tenantId;
  const [sessions, setSessions] = useState<readonly AgentSessionSnapshot[]>([]);
  const [selectedId, setSelectedId] = useState('');
  const [snapshot, setSnapshot] = useState<AgentSessionSnapshot | null>(null);
  const [question, setQuestion] = useState('');
  const [inputValue, setInputValue] = useState('');
  const [streamText, setStreamText] = useState('');
  const [connection, setConnection] = useState<ConnectionState>('IDLE');
  const [streamGeneration, setStreamGeneration] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const streamControllerRef = useRef<AbortController | null>(null);

  const requestOptions = useMemo(() => ({
    trustedTenantId: tenantId,
    trustedSiteId: site.id,
  }), [site.id, tenantId]);

  const replaceSnapshot = useCallback((next: AgentSessionSnapshot) => {
    setSnapshot(next);
    setSessions((current) => {
      const without = current.filter(({ session }) => session.id !== next.session.id);
      return [next, ...without].sort((left, right) => right.session.updatedAt - left.session.updatedAt);
    });
  }, []);

  const stopStream = useCallback(() => {
    streamControllerRef.current?.abort();
    streamControllerRef.current = null;
    setConnection('CLOSED');
  }, []);

  useEffect(() => registerProtectedResource({
    id: `operations-agent-session:${site.id}`,
    kind: 'realtime',
    purge() {
      stopStream();
      setSessions([]);
      setSelectedId('');
      setSnapshot(null);
      setStreamText('');
      setInputValue('');
    },
  }), [registerProtectedResource, site.id, stopStream]);

  useEffect(() => {
    const controller = new AbortController();
    setError(null);
    void listAgentSessions({ ...requestOptions, signal: controller.signal })
      .then((items) => {
        setSessions(items);
        setSelectedId((current) => current || items[0]?.session.id || '');
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '无法读取 AI 运维调查。');
      });
    return () => controller.abort();
  }, [requestOptions]);

  useEffect(() => {
    stopStream();
    setStreamText('');
    setInputValue('');
    if (!selectedId) {
      setSnapshot(null);
      setConnection('IDLE');
      return;
    }
    const controller = new AbortController();
    streamControllerRef.current = controller;
    setConnection('CONNECTING');
    setError(null);
    const onEvent = (event: AgentSessionEvent) => {
      if (event.type === 'session.snapshot') {
        replaceSnapshot(event.payload.snapshot);
        setConnection(event.payload.snapshot.session.status === 'ACTIVE' ? 'LIVE' : 'CLOSED');
        return;
      }
      if (event.type === 'assistant.delta') setStreamText((current) => current + event.payload.delta);
      if (event.type === 'run.failed') setConnection('CLOSED');
    };
    void streamAgentSessionEvents(selectedId, { ...requestOptions, signal: controller.signal }, onEvent)
      .then(() => {
        if (!controller.signal.aborted) setConnection('CLOSED');
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        setConnection('CLOSED');
        setError(reason instanceof AgentSessionApiError ? reason.message : 'AI 运维调查实时连接已中断。');
      });
    return () => {
      controller.abort();
      if (streamControllerRef.current === controller) streamControllerRef.current = null;
    };
  }, [replaceSnapshot, requestOptions, selectedId, stopStream, streamGeneration]);

  const create = useCallback(async () => {
    const message = question.trim();
    if (!message || busy) return;
    setBusy(true);
    setError(null);
    try {
      const created = await createAgentSession(message, requestOptions);
      replaceSnapshot(created);
      setSelectedId(created.session.id);
      setQuestion('');
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法创建 AI 运维调查。');
    } finally {
      setBusy(false);
    }
  }, [busy, question, replaceSnapshot, requestOptions]);

  const cancel = useCallback(async () => {
    if (snapshot?.session.status !== 'ACTIVE' || busy) return;
    setBusy(true);
    setError(null);
    try {
      replaceSnapshot(await cancelAgentSession(snapshot.session.id, snapshot.session.revision, requestOptions));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法取消当前调查。');
    } finally {
      setBusy(false);
    }
  }, [busy, replaceSnapshot, requestOptions, snapshot]);

  const pendingInput = latestPendingInput(snapshot);
  const submitInput = useCallback(async () => {
    if (!snapshot || !pendingInput || !inputValue || busy) return;
    setBusy(true);
    setError(null);
    try {
      const continued = await submitAgentSessionInput(snapshot.session.id, {
        expectedRevision: snapshot.session.revision,
        requestArtifactId: pendingInput.id,
        value: inputValue,
      }, requestOptions);
      replaceSnapshot(continued);
      setInputValue('');
      setStreamGeneration((current) => current + 1);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法提交操作员输入。');
    } finally {
      setBusy(false);
    }
  }, [busy, inputValue, pendingInput, replaceSnapshot, requestOptions, snapshot]);

  const finding = snapshot === null
    ? undefined
    : [...snapshot.artifacts].reverse().find((artifact) => artifact.kind === 'FINDING');
  const visibleMessages = snapshot?.messages ?? [];

  return (
    <section className="agent-session-workspace" aria-labelledby="agent-session-title">
      <header className="agent-session-header">
        <div>
          <span className="agent-session-eyebrow">智能运维排查</span>
          <h2 id="agent-session-title">运行异常辅助排查</h2>
          <p className="text-sm text-muted-foreground">{site.displayName} · 当前排查状态与实时进展</p>
        </div>
        <div className="agent-session-connection" role="status" aria-live="polite">
          <span className="agent-session-connection-dot" data-state={connection} />
          {connection === 'LIVE' ? '实时运行中' : connection === 'CONNECTING' ? '正在连接' : '已同步'}
        </div>
      </header>

      {error ? <Alert variant="destructive"><AlertTitle>辅助排查服务不可用</AlertTitle><AlertDescription>{error}</AlertDescription></Alert> : null}

      <div className="agent-session-composer">
        <label htmlFor="agent-session-question">向当前站点发起排查</label>
        <Textarea id="agent-session-question" value={question} maxLength={4000} rows={3} placeholder="例如：2号离心机排气温度偏高、夜间低负荷水泵频率异常等" disabled={busy} onChange={(event) => setQuestion(event.target.value)} />
        <div className="agent-session-composer-actions">
          <span className="text-xs text-muted-foreground">{question.length}/4000</span>
          <Button disabled={busy || !question.trim()} onClick={() => { void create(); }}>{busy ? '正在创建…' : '开始排查'}</Button>
        </div>
      </div>

      <div className="agent-session-body">
        <nav className="agent-session-list" aria-label="排查记录列表">
          <div className="agent-session-section-title"><strong>排查记录</strong><span>{sessions.length}</span></div>
          {sessions.length === 0 ? <span className="text-sm text-muted-foreground">当前站点暂无排查记录。</span> : null}
          {sessions.map((item) => (
            <button type="button" key={item.session.id} className="agent-session-list-item" aria-current={item.session.id === selectedId ? 'page' : undefined} onClick={() => setSelectedId(item.session.id)}>
              <span><StatusTag status={item.session.status} /></span>
              <strong>{item.messages.find((message) => message.role === 'OPERATOR')?.content ?? '运维排查任务'}</strong>
              <small>{formatTime(item.session.updatedAt, site.timezone)}</small>
            </button>
          ))}
        </nav>

        <div className="agent-session-detail">
          {!snapshot ? (
            <div className="agent-session-empty"><strong>选择排查会话</strong><span>查看已提交消息、运行记录和当前诊断步骤。</span></div>
          ) : (
            <>
              <div className="agent-session-detail-heading">
                <div><strong>当前排查</strong><span>最近更新 {formatTime(snapshot.session.updatedAt, site.timezone)}</span></div>
                <div><StatusTag status={snapshot.session.status} />{snapshot.session.status === 'ACTIVE' ? <Button variant="destructive" size="sm" disabled={busy} onClick={() => { void cancel(); }}>取消排查</Button> : null}</div>
              </div>

              <section className="agent-session-transcript" aria-label="排查记录">
                {visibleMessages.map((message) => (
                  <article key={message.id} data-role={message.role}>
                    <small>{message.role === 'OPERATOR' ? '操作员' : '智能排查助手'} · {formatTime(message.createdAt, site.timezone)}</small>
                    <p>{message.content}</p>
                  </article>
                ))}
                {streamText ? <article data-role="ASSISTANT" data-streaming="true" aria-label="正在分析排查结果"><small>智能排查助手 · 正在分析中</small><p>{streamText}</p></article> : null}
              </section>

              {pendingInput ? (
                <section className="agent-session-input" aria-labelledby="agent-session-input-title">
                  <h3 id="agent-session-input-title">需要操作员输入</h3>
                  <p>{pendingInput.request.prompt}</p>
                  {pendingInput.request.response.kind === 'TEXT' ? (
                    <Textarea aria-label="操作员输入" value={inputValue} maxLength={pendingInput.request.response.maxLength} disabled={busy} onChange={(event) => setInputValue(event.target.value)} />
                  ) : (
                    <div className="grid gap-2" role="radiogroup" aria-label="操作员选项">
                      {pendingInput.request.response.choices.map((choice) => (
                        <label key={choice.value} className="flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
                          <input type="radio" name="agent-session-input-choice" value={choice.value} checked={inputValue === choice.value} disabled={busy} onChange={(event) => setInputValue(event.target.value)} />
                          <span>{choice.label}</span>
                        </label>
                      ))}
                    </div>
                  )}
                  <Button disabled={busy || !inputValue} onClick={() => { void submitInput(); }}>{busy ? '正在提交…' : '提交并继续调查'}</Button>
                </section>
              ) : null}

              {finding?.kind === 'FINDING' ? (
                <section className="agent-session-finding" aria-labelledby="agent-session-finding-title">
                  <div className="agent-session-section-title"><h3 id="agent-session-finding-title">调查结论</h3><Badge variant="outline" className={finding.finding.outcome === 'SUPPORTED_FINDING' ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300' : 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300'}>{finding.finding.outcome === 'SUPPORTED_FINDING' ? '有证据支持' : '证据不足'}</Badge></div>
                  <p>{finding.finding.summary}</p>
                  {finding.finding.limitations.length ? <p><strong>限制：</strong>{finding.finding.limitations.join('；')}</p> : null}
                  {finding.finding.recommendedNext.length ? <p><strong>建议下一步：</strong>{finding.finding.recommendedNext.join('；')}</p> : null}
                </section>
              ) : null}

              {snapshot.toolExecutions.length ? (
                <details className="agent-session-tools">
                  <summary>Tool 执行记录 ({snapshot.toolExecutions.length})</summary>
                  <ul>{snapshot.toolExecutions.map((execution) => <li key={execution.id}><strong>{execution.toolName}</strong><span>{execution.status}{execution.failureCode ? ` · ${execution.failureCode}` : ''}</span></li>)}</ul>
                </details>
              ) : null}
            </>
          )}
        </div>
      </div>
    </section>
  );
}
