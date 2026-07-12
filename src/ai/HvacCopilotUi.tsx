import { useMemo, useState, type ComponentProps, type ReactElement } from 'react';
import { CopilotChatView } from '@copilotkit/react-core/v2';
import {
  CloseOutlined,
  ExpandOutlined,
  HistoryOutlined,
  PlusOutlined,
  RobotOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useAiApplicationContext } from './context';
import {
  formatThreadTime,
  threadKindLabel,
  useAiHistory,
  useAiThreadController,
} from './history';

type WelcomeScreenProps = ComponentProps<typeof CopilotChatView.WelcomeScreen>;
type WelcomeVariant = 'popup' | 'workspace';

function HvacCopilotAttentionBadge() {
  const context = useAiApplicationContext();
  const countLabel = context.attentionCount > 9 ? '9+' : String(context.attentionCount);

  if (context.attentionCount <= 0) return null;

  return (
    <span className="hvac-copilot-toggle-count" aria-label={`${context.attentionCount} 项待关注`}>
      {countLabel}
    </span>
  );
}

export function HvacCopilotToggleIcon() {
  return (
    <span className="hvac-copilot-toggle-content">
      <RobotOutlined className="hvac-copilot-toggle-glyph" aria-hidden="true" />
      <HvacCopilotAttentionBadge />
    </span>
  );
}

export function HvacCopilotToggleCloseIcon() {
  return (
    <span className="hvac-copilot-toggle-content">
      <CloseOutlined className="hvac-copilot-toggle-glyph" aria-hidden="true" />
      <HvacCopilotAttentionBadge />
    </span>
  );
}

function HvacCopilotHistoryPanel() {
  const navigate = useNavigate();
  const [query, setQuery] = useState('');
  const popupHistoryOpen = useAiHistory((state) => state.popupHistoryOpen);
  const setPopupHistoryOpen = useAiHistory((state) => state.setPopupHistoryOpen);
  const { threads, activeThreadId, openThread, startNewThread } = useAiThreadController();

  const visibleThreads = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return threads
      .filter((thread) => !thread.archived && thread.messageCount > 0)
      .filter((thread) => !normalized || `${thread.title} ${thread.summary} ${thread.scopeLabel}`.toLowerCase().includes(normalized))
      .slice(0, 7);
  }, [query, threads]);

  if (!popupHistoryOpen) return null;

  return (
    <section className="hvac-copilot-history-panel" aria-label="AI 对话历史">
      <header className="hvac-copilot-history-heading">
        <div>
          <span>会话记录</span>
          <strong>{threads.filter((thread) => !thread.archived && thread.messageCount > 0).length} 条</strong>
        </div>
        <button
          type="button"
          onClick={() => {
            startNewThread();
            setPopupHistoryOpen(false);
          }}
        >
          <PlusOutlined />
          新建
        </button>
      </header>

      <label className="hvac-copilot-history-search">
        <SearchOutlined aria-hidden="true" />
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜索会话、设备或结论"
          aria-label="搜索 AI 会话"
        />
      </label>

      <div className="hvac-copilot-history-list">
        {visibleThreads.length ? visibleThreads.map((thread) => (
          <button
            type="button"
            key={thread.id}
            className={thread.id === activeThreadId ? 'is-active' : undefined}
            onClick={() => openThread(thread.id)}
          >
            <span className="hvac-copilot-history-type" data-kind={thread.kind}>
              {threadKindLabel[thread.kind]}
            </span>
            <span className="hvac-copilot-history-copy">
              <strong>{thread.title}</strong>
              <small>{thread.summary}</small>
            </span>
            <time>{formatThreadTime(thread.updatedAt)}</time>
          </button>
        )) : (
          <div className="hvac-copilot-history-empty">没有匹配的会话记录</div>
        )}
      </div>

      <footer className="hvac-copilot-history-footer">
        <span>历史保存在当前浏览器</span>
        <button
          type="button"
          onClick={() => {
            setPopupHistoryOpen(false);
            navigate('/ai');
          }}
        >
          打开运维中心
          <ExpandOutlined />
        </button>
      </footer>
    </section>
  );
}

export function HvacCopilotHeaderContent({ closeButton }: { closeButton: ReactElement }) {
  const popupHistoryOpen = useAiHistory((state) => state.popupHistoryOpen);
  const setPopupHistoryOpen = useAiHistory((state) => state.setPopupHistoryOpen);
  const { activeThread, startNewThread } = useAiThreadController();
  const title = activeThread?.messageCount ? activeThread.title : '新对话';

  return (
    <div className="hvac-copilot-header">
      <div className="hvac-copilot-header-layout">
        <div className="hvac-copilot-header-identity">
          <strong title={title}>{title}</strong>
        </div>
        <div className="hvac-copilot-header-actions">
          <button
            type="button"
            className={popupHistoryOpen ? 'hvac-copilot-icon-button is-active' : 'hvac-copilot-icon-button'}
            aria-label="对话历史"
            title="对话历史"
            onClick={() => setPopupHistoryOpen(!popupHistoryOpen)}
          >
            <HistoryOutlined />
          </button>
          <button
            type="button"
            className="hvac-copilot-icon-button"
            aria-label="新建会话"
            title="新建会话"
            onClick={startNewThread}
          >
            <PlusOutlined />
          </button>
          {closeButton}
        </div>
      </div>
      <HvacCopilotHistoryPanel />
    </div>
  );
}

function RecentThreadList({ limit = 3 }: { limit?: number }) {
  const { threads, activeThreadId, openThread } = useAiThreadController();
  const recentThreads = threads
    .filter((thread) => !thread.archived && thread.messageCount > 0 && thread.id !== activeThreadId)
    .slice(0, limit);

  if (!recentThreads.length) return null;

  return (
    <section className="hvac-copilot-recent" aria-label="最近会话">
      <div className="hvac-copilot-section-heading">最近会话</div>
      <div className="hvac-copilot-recent-list">
        {recentThreads.map((thread) => (
          <button type="button" key={thread.id} onClick={() => openThread(thread.id)}>
            <span>
              <strong>{thread.title}</strong>
              <small>{threadKindLabel[thread.kind]} · {thread.scopeLabel}</small>
            </span>
            <time>{formatThreadTime(thread.updatedAt)}</time>
          </button>
        ))}
      </div>
    </section>
  );
}

function HvacCopilotWelcomeBase({
  variant,
  ...props
}: WelcomeScreenProps & { variant: WelcomeVariant }) {
  const context = useAiApplicationContext();
  const {
    input,
    suggestionView,
    welcomeMessage: _welcomeMessage,
    className,
    children: _children,
    ...rest
  } = props;

  if (variant === 'popup') {
    return (
      <div
        {...rest}
        data-variant={variant}
        className={['hvac-copilot-welcome', className].filter(Boolean).join(' ')}
      >
        <div className="hvac-copilot-popup-context" aria-label="当前 AI 上下文">
          <span>已接入</span>
          <strong>{context.pageTitle}</strong>
          <small title={context.scopeLabel}>{context.scopeLabel}</small>
        </div>

        <div className="hvac-copilot-popup-spacer" aria-hidden="true" />

        <RecentThreadList />

        <div className="hvac-copilot-welcome-input">{input}</div>
      </div>
    );
  }

  return (
    <div
      {...rest}
      data-variant={variant}
      className={['hvac-copilot-welcome', className].filter(Boolean).join(' ')}
    >
      <div className="hvac-copilot-workspace-intro">
        <section className="hvac-copilot-brief">
          <div className="hvac-copilot-presence-line">
            <span aria-hidden="true" />
            <strong>{context.pageTitle}</strong>
            <small>已接入当前页面</small>
          </div>
          <h2>从当前运行数据开始调查</h2>
          <p>描述设备、能耗或运维异常，AI 将关联实时监测、FDD 诊断和工单证据。</p>
        </section>

        <div className="hvac-copilot-scope-line" aria-label="当前 AI 上下文">
          <span>分析范围</span>
          <strong title={context.scopeLabel}>{context.scopeLabel}</strong>
          <small>{context.roleLabel}</small>
        </div>

        <section className="hvac-copilot-suggestion-section">
          <div className="hvac-copilot-section-heading">可以这样开始</div>
          {suggestionView}
        </section>
      </div>

      <div className="hvac-copilot-welcome-input">
        <div className="hvac-copilot-composer-label">开始调查</div>
        {input}
      </div>
    </div>
  );
}

export function HvacCopilotWelcomeScreen(props: WelcomeScreenProps) {
  return <HvacCopilotWelcomeBase {...props} variant="popup" />;
}

export function HvacCopilotWorkspaceWelcomeScreen(props: WelcomeScreenProps) {
  return <HvacCopilotWelcomeBase {...props} variant="workspace" />;
}

