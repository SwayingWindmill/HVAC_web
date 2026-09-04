import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Input, Radio, Tag, Typography } from 'antd';

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
  const color = status === 'ACTIVE'
    ? 'processing'
    : status === 'WAITING_FOR_INPUT'
      ? 'warning'
      : status === 'COMPLETED'
        ? 'success'
        : status === 'FAILED'
          ? 'error'
          : 'default';
  return <Tag color={color}>{status.replace(/_/gu, ' ')}</Tag>;
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
          <Typography.Text className="agent-session-eyebrow">SITE-SCOPED AGENT</Typography.Text>
          <Typography.Title level={2} id="agent-session-title">AI 运维调查</Typography.Title>
          <Typography.Text type="secondary">{site.displayName} · 权威 Session 快照 + 当前 Run 实时事件</Typography.Text>
        </div>
        <div className="agent-session-connection" role="status" aria-live="polite">
          <span className="agent-session-connection-dot" data-state={connection} />
          {connection === 'LIVE' ? '实时运行中' : connection === 'CONNECTING' ? '正在连接' : '已同步'}
        </div>
      </header>

      {error ? <Alert type="error" showIcon message="AI 运维调查不可用" description={error} /> : null}

      <div className="agent-session-composer">
        <label htmlFor="agent-session-question">向当前 Site 发起调查</label>
        <Input.TextArea id="agent-session-question" value={question} maxLength={4000} autoSize={{ minRows: 2, maxRows: 5 }} placeholder="例如：昨夜能耗为什么高于基线？" disabled={busy} onChange={(event) => setQuestion(event.target.value)} />
        <div className="agent-session-composer-actions">
          <Typography.Text type="secondary">{question.length}/4000</Typography.Text>
          <Button type="primary" loading={busy} disabled={!question.trim()} onClick={() => { void create(); }}>开始调查</Button>
        </div>
      </div>

      <div className="agent-session-body">
        <nav className="agent-session-list" aria-label="AI 运维调查列表">
          <div className="agent-session-section-title"><strong>Sessions</strong><span>{sessions.length}</span></div>
          {sessions.length === 0 ? <Typography.Text type="secondary">当前 Site 尚无调查。</Typography.Text> : null}
          {sessions.map((item) => (
            <button type="button" key={item.session.id} className="agent-session-list-item" aria-current={item.session.id === selectedId ? 'page' : undefined} onClick={() => setSelectedId(item.session.id)}>
              <span><StatusTag status={item.session.status} /></span>
              <strong>{item.messages.find((message) => message.role === 'OPERATOR')?.content ?? 'AI 运维调查'}</strong>
              <small>{formatTime(item.session.updatedAt, site.timezone)}</small>
            </button>
          ))}
        </nav>

        <div className="agent-session-detail">
          {!snapshot ? (
            <div className="agent-session-empty"><strong>选择一个 Session</strong><span>查看已提交消息、证据和当前 Run。</span></div>
          ) : (
            <>
              <div className="agent-session-detail-heading">
                <div><strong>{snapshot.session.id}</strong><span>revision {snapshot.session.revision}</span></div>
                <div><StatusTag status={snapshot.session.status} />{snapshot.session.status === 'ACTIVE' ? <Button danger disabled={busy} onClick={() => { void cancel(); }}>取消 Run</Button> : null}</div>
              </div>

              <section className="agent-session-transcript" aria-label="调查消息">
                {visibleMessages.map((message) => (
                  <article key={message.id} data-role={message.role}>
                    <small>{message.role === 'OPERATOR' ? '操作员' : 'AI 运维调查'} · {formatTime(message.createdAt, site.timezone)}</small>
                    <p>{message.content}</p>
                  </article>
                ))}
                {streamText ? <article data-role="ASSISTANT" data-streaming="true" aria-label="AI 正在生成回复"><small>AI 运维调查 · streaming</small><p>{streamText}</p></article> : null}
              </section>

              {pendingInput ? (
                <section className="agent-session-input" aria-labelledby="agent-session-input-title">
                  <Typography.Title level={3} id="agent-session-input-title">需要操作员输入</Typography.Title>
                  <p>{pendingInput.request.prompt}</p>
                  {pendingInput.request.response.kind === 'TEXT' ? (
                    <Input.TextArea aria-label="操作员输入" value={inputValue} maxLength={pendingInput.request.response.maxLength} disabled={busy} onChange={(event) => setInputValue(event.target.value)} />
                  ) : (
                    <Radio.Group aria-label="操作员选项" value={inputValue} disabled={busy} options={pendingInput.request.response.choices.map((choice) => ({ label: choice.label, value: choice.value }))} onChange={(event) => setInputValue(String(event.target.value))} />
                  )}
                  <Button type="primary" loading={busy} disabled={!inputValue} onClick={() => { void submitInput(); }}>提交并继续调查</Button>
                </section>
              ) : null}

              {finding?.kind === 'FINDING' ? (
                <section className="agent-session-finding" aria-labelledby="agent-session-finding-title">
                  <div className="agent-session-section-title"><Typography.Title level={3} id="agent-session-finding-title">调查结论</Typography.Title><Tag color={finding.finding.outcome === 'SUPPORTED_FINDING' ? 'success' : 'warning'}>{finding.finding.outcome}</Tag></div>
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
