import { useMemo, useState, type ComponentProps, type HTMLAttributes, type ReactElement } from 'react';
import {
  CopilotChatView,
  useCopilotChatConfiguration,
} from '@copilotkit/react-core/v2';
import {
  ExpandOutlined,
  HistoryOutlined,
  PlusOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { AI_ASSISTANT_NAME } from './config';
import { useAiApplicationContext } from './context';
import {
  formatThreadTime,
  threadKindLabel,
  useAiHistory,
  useAiThreadController,
} from './history';

type WelcomeScreenProps = ComponentProps<typeof CopilotChatView.WelcomeScreen>;
type WelcomeVariant = 'popup' | 'workspace';

export function HvacCopilotToggleIcon() {
  const context = useAiApplicationContext();
  const countLabel = context.attentionCount > 9 ? '9+' : String(context.attentionCount);

  return (
    <span className="hvac-copilot-toggle-content">
      <span className="hvac-copilot-toggle-presence" aria-hidden="true" />
      <span className="hvac-copilot-toggle-label">AI 运维助手</span>
      {context.attentionCount > 0 && (
        <span className="hvac-copilot-toggle-count" aria-label={`${context.attentionCount} 项待关注`}>
          {countLabel}
        </span>
      )}
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
  const navigate = useNavigate();
  const context = useAiApplicationContext();
  const configuration = useCopilotChatConfiguration();
  const popupHistoryOpen = useAiHistory((state) => state.popupHistoryOpen);
  const setPopupHistoryOpen = useAiHistory((state) => state.setPopupHistoryOpen);
  const { activeThreadId, activeThread, startNewThread } = useAiThreadController();

  const openWorkspace = () => {
    setPopupHistoryOpen(false);
    configuration?.setModalOpen(false);
    navigate(`/ai?thread=${activeThreadId}`);
  };

  return (
    <div className="hvac-copilot-header">
      <div className="hvac-copilot-header-layout">
        <div className="hvac-copilot-header-identity">
          <span className="hvac-copilot-header-presence" aria-hidden="true" />
          <div className="hvac-copilot-header-copy">
            <div className="hvac-copilot-header-title-row">
              <strong>{AI_ASSISTANT_NAME}</strong>
              <span className="hvac-copilot-readonly-badge">只读</span>
            </div>
            <span className="hvac-copilot-header-scope" title={activeThread?.title || context.scopeLabel}>
              {activeThread?.messageCount ? activeThread.title : context.scopeLabel}
            </span>
          </div>
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
          <button
            type="button"
            className="hvac-copilot-icon-button"
            aria-label="打开完整 AI 工作台"
            title="打开完整 AI 工作台"
            onClick={openWorkspace}
          >
            <ExpandOutlined />
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

  return (
    <div
      {...rest}
      data-variant={variant}
      className={['hvac-copilot-welcome', className].filter(Boolean).join(' ')}
    >
      <section className="hvac-copilot-brief">
        <div className="hvac-copilot-presence-line">
          <span aria-hidden="true" />
          <strong>已读取当前页面</strong>
          <small>{context.pageTitle}</small>
        </div>
        <h2>{variant === 'workspace' ? '从会话或当前页面开始调查' : context.welcomeTitle}</h2>
        <p>{variant === 'workspace'
          ? '选择左侧历史记录继续处理，或基于当前范围发起新的只读分析。'
          : context.pageDescription}</p>
      </section>

      <div className="hvac-copilot-scope-line" aria-label="当前 AI 上下文">
        <span>当前范围</span>
        <strong title={context.scopeLabel}>{context.scopeLabel}</strong>
        <small>{context.roleLabel}</small>
      </div>

      <section className="hvac-copilot-suggestion-section">
        <div className="hvac-copilot-section-heading">基于当前页面</div>
        {suggestionView}
      </section>

      {variant === 'popup' ? <RecentThreadList /> : null}

      <div className="hvac-copilot-welcome-input">{input}</div>
    </div>
  );
}

export function HvacCopilotWelcomeScreen(props: WelcomeScreenProps) {
  return <HvacCopilotWelcomeBase {...props} variant="popup" />;
}

export function HvacCopilotWorkspaceWelcomeScreen(props: WelcomeScreenProps) {
  return <HvacCopilotWelcomeBase {...props} variant="workspace" />;
}

export function HvacCopilotDisclaimer(props: HTMLAttributes<HTMLDivElement>) {
  const { className, ...rest } = props;
  return (
    <div {...rest} className={['hvac-copilot-disclaimer', className].filter(Boolean).join(' ')}>
      仅用于分析与建议；设备控制及业务写入需人工确认。
    </div>
  );
}
