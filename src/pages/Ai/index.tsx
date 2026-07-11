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
  ApiOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  ClockCircleOutlined,
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
  HvacCopilotDisclaimer,
  HvacCopilotWorkspaceWelcomeScreen,
} from '@/ai/HvacCopilotUi';
import { COPILOTKIT_RUNTIME_CONFIGURED } from '@/ai/config';
import { useAiApplicationContext } from '@/ai/context';
import {
  formatThreadTime,
  threadKindLabel,
  threadStatusLabel,
  type AiThreadKind,
  type AiThreadRecord,
  useAiHistory,
  useAiThreadController,
} from '@/ai/history';
import { useTelemetryLive } from '@/api';
import { MOCK_DEVICES } from '@/api/mock';
import { ROLE_LABEL, useUi } from '@/store/ui';
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
                <span>{threadKindLabel[thread.kind]}</span>
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
        <span>本地历史 · 刷新后保留</span>
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
}: {
  totalPower: number;
  averageCop: number;
  averageLoad: number;
}) {
  const navigate = useNavigate();
  const context = useAiApplicationContext();
  const activeThread = useAiThreadController().activeThread;

  const sources = [
    { label: '实时遥测', detail: `${MOCK_DEVICES.length} 台设备 · ${totalPower} kW`, tone: 'live' },
    { label: 'FDD 诊断', detail: `${context.metrics.activeDiagnoses} 条 · ${context.metrics.highRiskDiagnoses} 条高风险`, tone: context.metrics.highRiskDiagnoses ? 'critical' : 'normal' },
    { label: '报警工单', detail: `${context.metrics.activeWorkOrders} 条 · ${context.metrics.slaRiskWorkOrders} 条 SLA 风险`, tone: context.metrics.slaRiskWorkOrders ? 'warning' : 'normal' },
    { label: '优化建议', detail: `${context.metrics.pendingOptimizations} 条待评审`, tone: 'normal' },
  ];

  const handoffs = [
    { label: 'FDD 证据', path: '/fdd', icon: <AlertOutlined /> },
    { label: '报警工单', path: '/alarms', icon: <FileTextOutlined /> },
    { label: '优化评审', path: '/optimize', icon: <ThunderboltOutlined /> },
    { label: '成本绩效', path: '/cost', icon: <DollarOutlined /> },
  ];

  return (
    <div className="ai-evidence-rail-content">
      <section className="ai-evidence-section">
        <header>
          <span>当前线程</span>
          <Tag bordered={false} color={activeThread?.status === 'waiting' ? 'orange' : 'green'}>
            {activeThread ? threadStatusLabel[activeThread.status] : '新会话'}
          </Tag>
        </header>
        <h2>{activeThread?.title || '新会话'}</h2>
        <p>{activeThread?.summary || '从当前页面开始新的运维分析。'}</p>
        <dl className="ai-thread-facts">
          <div><dt>类型</dt><dd>{activeThread ? threadKindLabel[activeThread.kind] : '会话'}</dd></div>
          <div><dt>范围</dt><dd title={activeThread?.scopeLabel}>{activeThread?.scopeLabel || context.scopeLabel}</dd></div>
          <div><dt>消息</dt><dd>{activeThread?.messageCount ?? 0} 条</dd></div>
          <div><dt>更新</dt><dd>{activeThread ? formatThreadTime(activeThread.updatedAt) : '刚刚'}</dd></div>
        </dl>
      </section>

      <section className="ai-evidence-section">
        <header>
          <span>当前运行</span>
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
          <span>证据覆盖</span>
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
        <header><span>进入业务闭环</span></header>
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
        <CheckCircleFilled />
        <span>AI 负责解释、归因和建议；审批、派工与设备控制继续由原业务权限约束。</span>
      </div>
    </div>
  );
}

function AiWorkspace() {
  const [searchParams] = useSearchParams();
  const context = useAiApplicationContext();
  const { role, demoMode } = useUi();
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
    />
  );

  return (
    <div className="ai-hub" aria-label="AI 运维中心">
      <header className="ai-hub-header">
        <div className="ai-hub-heading">
          <div className="ai-hub-eyebrow">AI 运维</div>
          <div>
            <h1>AI 运维中心</h1>
            <p>会话、调查和报告在同一工作台持续沉淀，结论可追溯到设备、诊断和工单。</p>
          </div>
        </div>
        <div className="ai-hub-header-actions">
          <Tag icon={<ClockCircleOutlined />}>本地历史</Tag>
          <Tag color={COPILOTKIT_RUNTIME_CONFIGURED ? 'green' : 'default'} icon={<ApiOutlined />}>
            {COPILOTKIT_RUNTIME_CONFIGURED ? 'Runtime 在线' : '本地 Agent'}
          </Tag>
          <Tag>{ROLE_LABEL[role]}</Tag>
          <Button className="ai-mobile-history-button" icon={<MenuOutlined />} onClick={() => setHistoryDrawerOpen(true)}>
            会话
          </Button>
          <Button className="ai-mobile-evidence-button" icon={<DatabaseOutlined />} onClick={() => setEvidenceDrawerOpen(true)}>
            证据
          </Button>
        </div>
      </header>

      <div className="ai-hub-body">
        <aside className="ai-thread-sidebar" aria-label="AI 会话历史">
          <ThreadNavigator onRename={beginRename} />
        </aside>

        <section className="ai-conversation-pane" aria-label="AI 运维对话工作台">
          <header className="ai-conversation-header">
            <div>
              <span>{activeThread ? threadKindLabel[activeThread.kind] : '会话'} · {demoMode ? '演示数据' : '接入数据'}</span>
              <h2>{activeThread?.title || '新会话'}</h2>
            </div>
            <div className="ai-conversation-actions">
              <span>{agent.messages.length ? `${agent.messages.length} 条消息` : '尚未开始'}</span>
              <Button icon={<PlusOutlined />} onClick={startNewThread}>新建会话</Button>
            </div>
          </header>

          <div className="ai-conversation-context">
            <span className="ai-presence-dot" aria-hidden="true" />
            <strong title={activeThread?.scopeLabel || context.scopeLabel}>{activeThread?.scopeLabel || context.scopeLabel}</strong>
            <small>只读分析 · 与全局 Popup 共用线程</small>
          </div>

          <div className="ai-copilot-chat-shell">
            <CopilotChat
              className="ai-copilot-chat"
              welcomeScreen={HvacCopilotWorkspaceWelcomeScreen}
              input={{
                disclaimer: HvacCopilotDisclaimer,
                showDisclaimer: true,
              }}
              labels={{
                welcomeMessageText: context.welcomeTitle,
                chatInputPlaceholder: context.inputPlaceholder,
                chatDisclaimerText: '仅用于分析与建议；设备控制及业务写入需人工确认。',
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
