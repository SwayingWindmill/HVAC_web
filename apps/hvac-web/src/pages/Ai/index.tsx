import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Button,
  Drawer,
  Dropdown,
  Input,
  Modal,
  Pagination,
  Segmented,
  Tag,
  Tooltip,
  type MenuProps,
} from 'antd';
import { CopilotChat, useAgent } from '@copilotkit/react-core/v2';
import {
  AlertOutlined,
  ArrowRightOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  DollarOutlined,
  EditOutlined,
  FileTextOutlined,
  HistoryOutlined,
  InboxOutlined,
  MenuOutlined,
  MoreOutlined,
  PlusOutlined,
  PushpinFilled,
  PushpinOutlined,
  SearchOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import CopilotContextBridge from '@/ai/CopilotContextBridge';
import {
  HvacCopilotWorkspaceWelcomeScreen,
} from '@/ai/HvacCopilotUi';
import { useAiApplicationContext } from '@/ai/context';
import {
  formatThreadTime,
  threadStatusLabel,
  type AiThreadKind,
  type AiThreadRecord,
  useAiHistory,
  useAiThreadController,
} from '@/ai/history';
import { useTelemetryLive } from '@/api';
import { MOCK_DEVICES } from '@/api/mock';
import { useUi } from '@/store/ui';
import './Ai.css';

type ThreadFilter = 'all' | AiThreadKind | 'archived';

const THREAD_PAGE_SIZE = 7;

const FILTER_OPTIONS: { label: string; value: ThreadFilter }[] = [
  { label: '全部', value: 'all' },
  { label: '调查', value: 'investigation' },
  { label: '报告', value: 'report' },
  { label: '归档', value: 'archived' },
];

function ThreadNavigator({
  onOpen,
  onRename,
}: {
  onOpen?: () => void;
  onRename: (thread: AiThreadRecord) => void;
}) {
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<ThreadFilter>('all');
  const [page, setPage] = useState(1);
  const { threads, activeThreadId, startNewThread, openThread } = useAiThreadController();
  const togglePinned = useAiHistory((state) => state.togglePinned);
  const archiveThread = useAiHistory((state) => state.archiveThread);
  const deleteThread = useAiHistory((state) => state.deleteThread);

  const filteredThreads = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return threads.filter((thread) => {
      if (filter === 'archived') {
        if (!thread.archived) return false;
      } else {
        if (thread.archived) return false;
        if (filter !== 'all' && thread.kind !== filter) return false;
      }
      if (!normalized) return true;
      return `${thread.title} ${thread.summary} ${thread.scopeLabel}`.toLowerCase().includes(normalized);
    });
  }, [filter, query, threads]);

  useEffect(() => setPage(1), [filter, query]);

  const pageCount = Math.max(1, Math.ceil(filteredThreads.length / THREAD_PAGE_SIZE));
  const safePage = Math.min(page, pageCount);
  const visibleThreads = filteredThreads.slice((safePage - 1) * THREAD_PAGE_SIZE, safePage * THREAD_PAGE_SIZE);

  const open = (threadId: string) => {
    openThread(threadId);
    onOpen?.();
  };

  const removeThread = (thread: AiThreadRecord) => {
    Modal.confirm({
      title: '删除会话记录？',
      content: `“${thread.title}”及其本地消息将被删除。`,
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => {
        if (thread.id === activeThreadId) startNewThread();
        deleteThread(thread.id);
      },
    });
  };

  const archive = (thread: AiThreadRecord) => {
    if (thread.id === activeThreadId && !thread.archived) startNewThread();
    archiveThread(thread.id, !thread.archived);
  };

  const menuFor = (thread: AiThreadRecord): MenuProps => ({
    items: [
      {
        key: 'pin',
        icon: thread.pinned ? <PushpinFilled /> : <PushpinOutlined />,
        label: thread.pinned ? '取消固定' : '固定会话',
      },
      {
        key: 'rename',
        icon: <EditOutlined />,
        label: '重命名',
      },
      {
        key: 'archive',
        icon: <InboxOutlined />,
        label: thread.archived ? '移出归档' : '归档',
      },
      { type: 'divider' },
      {
        key: 'delete',
        icon: <DeleteOutlined />,
        label: '删除',
        danger: true,
      },
    ],
    onClick: ({ key, domEvent }) => {
      domEvent.stopPropagation();
      if (key === 'pin') togglePinned(thread.id);
      if (key === 'rename') onRename(thread);
      if (key === 'archive') archive(thread);
      if (key === 'delete') removeThread(thread);
    },
  });

  return (
    <div className="ai-thread-navigator">
      <header className="ai-thread-navigator-header">
        <div>
          <span>会话与任务</span>
          <strong>{threads.filter((thread) => !thread.archived && thread.messageCount > 0).length}</strong>
        </div>
        <Tooltip title="新建会话">
          <Button
            type="primary"
            size="small"
            icon={<PlusOutlined />}
            onClick={() => {
              startNewThread();
              onOpen?.();
            }}
          />
        </Tooltip>
      </header>

      <Input
        className="ai-thread-search"
        allowClear
        prefix={<SearchOutlined />}
        placeholder="搜索会话、设备或结论"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />

      <Segmented<ThreadFilter>
        className="ai-thread-filter"
        size="small"
        block
        value={filter}
        options={FILTER_OPTIONS}
        onChange={setFilter}
      />

      <div className="ai-thread-list" aria-label="AI 会话记录">
        {visibleThreads.length ? visibleThreads.map((thread) => (
          <div
            key={thread.id}
            className={thread.id === activeThreadId ? 'ai-thread-row is-active' : 'ai-thread-row'}
            role="button"
            tabIndex={0}
            onClick={() => open(thread.id)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                open(thread.id);
              }
            }}
          >
            <span className="ai-thread-status" data-status={thread.status} aria-hidden="true" />
            <div className="ai-thread-copy">
              <div className="ai-thread-title-row">
                <strong>{thread.title}</strong>
                {thread.pinned ? <PushpinFilled aria-label="已固定" /> : null}
              </div>
              <p>{thread.summary}</p>
              <div className="ai-thread-meta">
                <span>{threadStatusLabel[thread.status]}</span>
                <time>{formatThreadTime(thread.updatedAt)}</time>
              </div>
            </div>
            <Dropdown menu={menuFor(thread)} trigger={['click']} placement="bottomRight">
              <Button
                type="text"
                size="small"
                className="ai-thread-more"
                icon={<MoreOutlined />}
                aria-label={`管理会话 ${thread.title}`}
                onClick={(event) => event.stopPropagation()}
              />
            </Dropdown>
          </div>
        )) : (
          <div className="ai-thread-empty">
            <HistoryOutlined />
            <strong>没有匹配记录</strong>
            <span>调整搜索或筛选条件</span>
          </div>
        )}
      </div>

      <footer className="ai-thread-footer">
        {filteredThreads.length > THREAD_PAGE_SIZE ? (
          <Pagination
            simple
            size="small"
            current={safePage}
            pageSize={THREAD_PAGE_SIZE}
            total={filteredThreads.length}
            onChange={setPage}
          />
        ) : null}
      </footer>
    </div>
  );
}

function EvidenceRail({
  totalPower,
  averageCop,
  averageLoad,
  hasStarted,
}: {
  totalPower: number;
  averageCop: number;
  averageLoad: number;
  hasStarted: boolean;
}) {
  const navigate = useNavigate();
  const context = useAiApplicationContext();
  const activeThread = useAiThreadController().activeThread;

  const sources = [
    { label: '实时遥测', detail: `${MOCK_DEVICES.length} 台设备在线`, tone: 'live' },
    { label: 'FDD 诊断', detail: `${context.metrics.activeDiagnoses} 条 · ${context.metrics.highRiskDiagnoses} 条高风险`, tone: context.metrics.highRiskDiagnoses ? 'critical' : 'normal' },
    { label: '报警工单', detail: `${context.metrics.activeWorkOrders} 条 · ${context.metrics.slaRiskWorkOrders} 条 SLA 风险`, tone: context.metrics.slaRiskWorkOrders ? 'warning' : 'normal' },
  ];

  const handoffs = [
    { label: '查看 FDD 证据', path: '/fdd', icon: <AlertOutlined /> },
    { label: '进入报警工单', path: '/alarms', icon: <FileTextOutlined /> },
    { label: '评审优化建议', path: '/optimize', icon: <ThunderboltOutlined /> },
    { label: '查看成本绩效', path: '/cost', icon: <DollarOutlined /> },
  ];

  if (!hasStarted) {
    return (
      <div className="ai-evidence-rail-content is-empty">
        <section className="ai-evidence-section ai-context-summary">
          <header><span>当前范围</span></header>
          <h2 title={context.scopeLabel}>{context.scopeLabel}</h2>
        </section>

        <section className="ai-evidence-section ai-available-data-section">
          <header><span>可用数据</span></header>
          <div className="ai-evidence-list is-compact">
            {sources.map((source) => (
              <div key={source.label}>
                <span><strong>{source.label}</strong><small>{source.detail}</small></span>
              </div>
            ))}
          </div>
        </section>

        <div className="ai-governance-bar">
          <span>只读分析</span>
        </div>
      </div>
    );
  }

  return (
    <div className="ai-evidence-rail-content">
      <section className="ai-evidence-section ai-thread-summary">
        <header>
          <span>当前调查</span>
          <Tag bordered={false} color={activeThread?.status === 'waiting' ? 'orange' : 'green'}>
            {activeThread ? threadStatusLabel[activeThread.status] : '新会话'}
          </Tag>
        </header>
        <h2>{activeThread?.title || '新会话'}</h2>
        <p>{activeThread?.summary || '从当前页面开始新的运维分析。'}</p>
        <div className="ai-thread-scope" title={activeThread?.scopeLabel || context.scopeLabel}>
          <span>分析范围</span>
          <strong>{activeThread?.scopeLabel || context.scopeLabel}</strong>
        </div>
      </section>

      <section className="ai-evidence-section">
        <header>
          <span>运行快照</span>
          <i className="ai-live-dot" aria-label="实时" />
        </header>
        <div className="ai-runtime-readings">
          <div><span>综合 COP</span><strong>{averageCop.toFixed(2)}</strong></div>
          <div><span>总功率</span><strong>{totalPower}<small>kW</small></strong></div>
          <div><span>平均负荷</span><strong>{averageLoad}<small>%</small></strong></div>
        </div>
      </section>

      <section className="ai-evidence-section">
        <header>
          <span>已接入证据</span>
          <DatabaseOutlined />
        </header>
        <div className="ai-evidence-list">
          {sources.map((source) => (
            <div key={source.label} data-tone={source.tone}>
              <i aria-hidden="true" />
              <span><strong>{source.label}</strong><small>{source.detail}</small></span>
            </div>
          ))}
        </div>
      </section>

      <section className="ai-evidence-section ai-handoff-section">
        <header><span>下一步</span></header>
        <div className="ai-handoff-grid">
          {handoffs.map((item) => (
            <button type="button" key={item.path} onClick={() => navigate(item.path)}>
              {item.icon}
              <span>{item.label}</span>
              <ArrowRightOutlined />
            </button>
          ))}
        </div>
      </section>

      <div className="ai-governance-bar">
        <span>当前为只读分析。审批、派工和设备控制仍在原业务流程中完成。</span>
      </div>
    </div>
  );
}

function AiWorkspace() {
  const [searchParams] = useSearchParams();
  const context = useAiApplicationContext();
  const { demoMode } = useUi();
  const { agent } = useAgent({ throttleMs: 40 });
  const { activeThread, activeThreadId, startNewThread, openThread } = useAiThreadController();
  const renameThread = useAiHistory((state) => state.renameThread);
  const threads = useAiHistory((state) => state.threads);
  const [renamingThread, setRenamingThread] = useState<AiThreadRecord | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [historyDrawerOpen, setHistoryDrawerOpen] = useState(false);
  const [evidenceDrawerOpen, setEvidenceDrawerOpen] = useState(false);
  const openedQueryThreadRef = useRef<string | null>(null);
  const live = useTelemetryLive(MOCK_DEVICES, ['power', 'cop', 'load']);
  const value = (deviceId: string, key: string) => live.get(deviceId, key) ?? 0;
  const totalPower = Math.round(MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'power'), 0));
  const averageCop = Math.round((MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'cop'), 0) / MOCK_DEVICES.length) * 100) / 100;
  const averageLoad = Math.round(MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'load'), 0) / MOCK_DEVICES.length);
  const hasStarted = agent.messages.length > 0 || (activeThread?.messageCount ?? 0) > 0;

  useEffect(() => {
    const threadId = searchParams.get('thread');
    if (!threadId || openedQueryThreadRef.current === threadId) return;
    if (!threads.some((thread) => thread.id === threadId)) return;
    openedQueryThreadRef.current = threadId;
    if (threadId !== activeThreadId) openThread(threadId);
  }, [activeThreadId, openThread, searchParams, threads]);

  const beginRename = (thread: AiThreadRecord) => {
    setRenamingThread(thread);
    setRenameValue(thread.title);
  };

  const confirmRename = () => {
    if (!renamingThread || !renameValue.trim()) return;
    renameThread(renamingThread.id, renameValue);
    setRenamingThread(null);
  };

  const evidenceRail = (
    <EvidenceRail
      totalPower={totalPower}
      averageCop={averageCop}
      averageLoad={averageLoad}
      hasStarted={hasStarted}
    />
  );

  return (
    <div className="ai-ops-workspace">
      <div className="ai-hub" aria-label="AI 运维中心">
        <div className="ai-hub-body">
          <aside className="ai-thread-sidebar" aria-label="AI 会话历史">
            <ThreadNavigator onRename={beginRename} />
          </aside>

          <section className="ai-conversation-pane" aria-label="AI 运维对话工作台">
            <header className="ai-conversation-header">
              <div>
                {hasStarted ? (
                  <span>{demoMode ? '演示数据' : '接入数据'} · {agent.messages.length || activeThread?.messageCount || 0} 条消息</span>
                ) : null}
                <h2>{hasStarted ? (activeThread?.title || '运维调查') : 'AI 运维助手'}</h2>
                {hasStarted ? (
                  <p title={activeThread?.scopeLabel || context.scopeLabel}>{activeThread?.scopeLabel || context.scopeLabel}</p>
                ) : null}
              </div>
              <div className="ai-conversation-actions">
                <Button className="ai-mobile-history-button" icon={<MenuOutlined />} onClick={() => setHistoryDrawerOpen(true)}>
                  会话
                </Button>
                <Button className="ai-mobile-evidence-button" icon={<DatabaseOutlined />} onClick={() => setEvidenceDrawerOpen(true)}>
                  证据
                </Button>
                {hasStarted ? <Button type="primary" icon={<PlusOutlined />} onClick={startNewThread}>新建调查</Button> : null}
              </div>
            </header>

            <div className="ai-copilot-chat-shell">
              <CopilotChat
                className="ai-copilot-chat"
                welcomeScreen={HvacCopilotWorkspaceWelcomeScreen}
                labels={{
                  welcomeMessageText: context.welcomeTitle,
                  chatInputPlaceholder: context.inputPlaceholder,
                  chatInputToolbarStartTranscribeButtonLabel: '开始语音输入',
                  chatInputToolbarCancelTranscribeButtonLabel: '取消语音输入',
                  chatInputToolbarFinishTranscribeButtonLabel: '完成语音输入',
                  chatInputToolbarAddButtonLabel: '添加内容',
                  chatInputToolbarToolsButtonLabel: '可用工具',
                  assistantMessageToolbarCopyCodeLabel: '复制代码',
                  assistantMessageToolbarCopyCodeCopiedLabel: '已复制',
                  assistantMessageToolbarCopyMessageLabel: '复制回答',
                  assistantMessageToolbarThumbsUpLabel: '有帮助',
                  assistantMessageToolbarThumbsDownLabel: '需要改进',
                  assistantMessageToolbarReadAloudLabel: '朗读回答',
                  assistantMessageToolbarRegenerateLabel: '重新生成',
                  userMessageToolbarCopyMessageLabel: '复制问题',
                  userMessageToolbarEditMessageLabel: '编辑问题',
                }}
              />
            </div>
          </section>

          <aside className="ai-evidence-sidebar" aria-label="当前线程证据与业务动作">
            {evidenceRail}
          </aside>
        </div>
      </div>

      <Drawer
        className="ai-mobile-drawer"
        title="会话与任务"
        placement="left"
        width="min(88vw, 340px)"
        open={historyDrawerOpen}
        onClose={() => setHistoryDrawerOpen(false)}
        destroyOnClose={false}
      >
        <ThreadNavigator onOpen={() => setHistoryDrawerOpen(false)} onRename={beginRename} />
      </Drawer>

      <Drawer
        className="ai-mobile-drawer"
        title="证据与业务动作"
        placement="right"
        width="min(88vw, 340px)"
        open={evidenceDrawerOpen}
        onClose={() => setEvidenceDrawerOpen(false)}
        destroyOnClose={false}
      >
        {evidenceRail}
      </Drawer>

      <Modal
        title="重命名会话"
        open={Boolean(renamingThread)}
        okText="保存"
        cancelText="取消"
        okButtonProps={{ disabled: !renameValue.trim() }}
        onOk={confirmRename}
        onCancel={() => setRenamingThread(null)}
      >
        <Input
          autoFocus
          maxLength={40}
          value={renameValue}
          onChange={(event) => setRenameValue(event.target.value)}
          onPressEnter={confirmRename}
          placeholder="输入会话名称"
        />
      </Modal>
    </div>
  );
}

function CopilotAiWorkspace() {
  return (
    <>
      <CopilotContextBridge />
      <AiWorkspace />
    </>
  );
}

export default function Ai() {
  return <CopilotAiWorkspace />;
}
