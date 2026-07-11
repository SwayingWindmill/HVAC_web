import { useEffect, useMemo, useState } from 'react';
import { Avatar, Button, Drawer, Empty, Grid, Space, Tooltip, Typography } from 'antd';
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseOutlined,
  CompressOutlined,
  DownOutlined,
  ExpandOutlined,
  LoadingOutlined,
  MessageOutlined,
  PlusOutlined,
  ReloadOutlined,
  RightOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useLocation } from 'react-router-dom';
import AssistantConversation from './AssistantConversation';
import CopilotContextBridge from './CopilotContextBridge';
import { AI_ASSISTANT_NAME, COPILOTKIT_ENABLED } from './config';
import { useAiApplicationContext } from './context';
import {
  useCopilotAssistantSession,
  useMockAssistantSession,
  type AssistantDisplayMessage,
  type AssistantSession,
} from './session';
import './GlobalAiAssistant.css';

const { Text } = Typography;
const BUILDING_LABELS: Record<string, string> = { b1: '总部大楼', b2: '研发中心' };

type PanelMode = 'standard' | 'focus';
type PanelView = 'home' | 'chat' | 'history';

type FocusContext = {
  eyebrow: string;
  title: string;
  detail: string;
};

type AgentSkill = {
  key: string;
  title: string;
  description: string;
  prompt: string;
};

type ConversationHistoryEntry = {
  id: string;
  title: string;
  summary: string;
  createdAt: Date;
  messages: AssistantDisplayMessage[];
};

type AssistantDrawerProps = {
  session: AssistantSession;
};

const AGENT_SKILLS: AgentSkill[] = [
  {
    key: 'energy-investigation',
    title: '能耗调查',
    description: '分析能耗变化、同期偏差、增量设备和异常运行时段。',
    prompt: '调查当前页面的能耗变化，分解增量来源并列出关键证据。',
  },
  {
    key: 'device-diagnosis',
    title: '设备诊断',
    description: '汇总设备遥测、健康状态、FDD 诊断和历史问题。',
    prompt: '诊断当前设备或页面中最值得关注的设备，并说明原因和证据。',
  },
  {
    key: 'alarm-attribution',
    title: '告警归因',
    description: '聚合关联告警，区分根因告警、伴随告警和影响范围。',
    prompt: '分析当前告警与诊断的关联关系，判断最可能的根因。',
  },
  {
    key: 'work-order-collaboration',
    title: '工单协同',
    description: '梳理待处理工单、SLA 风险、交接重点和建议顺序。',
    prompt: '按风险和时效总结当前工单，并给出建议处理顺序。',
  },
  {
    key: 'optimization-review',
    title: '节能优化',
    description: '审查节能建议的收益、风险、前置条件和验证方案。',
    prompt: '评估当前优化建议的收益、风险、执行条件和验证方式。',
  },
];

function getFocusContext(pathname: string, search: string, pageTitle: string, buildingId: string): FocusContext {
  const params = new URLSearchParams(search);
  const building = BUILDING_LABELS[buildingId] ?? buildingId;

  if (pathname.startsWith('/assets') && params.get('device')) {
    return { eyebrow: '当前设备', title: params.get('device') ?? '设备详情', detail: `${building} · 资产运行上下文` };
  }
  if (pathname.startsWith('/fdd') && params.get('diagnosis')) {
    return { eyebrow: '当前诊断', title: params.get('diagnosis') ?? 'FDD 诊断', detail: `${building} · 诊断证据上下文` };
  }
  if (pathname.startsWith('/alarms') && params.get('workOrder')) {
    return { eyebrow: '当前工单', title: params.get('workOrder') ?? '报警工单', detail: `${building} · 工单闭环上下文` };
  }
  if (pathname.startsWith('/optimize') && params.get('suggestion')) {
    return { eyebrow: '当前建议', title: params.get('suggestion') ?? '优化建议', detail: `${building} · 收益与风险上下文` };
  }
  if (pathname.startsWith('/energy')) {
    const year = params.get('year');
    const month = params.get('month');
    const day = params.get('day');
    const period = year
      ? `${year}${month ? `-${month.padStart(2, '0')}` : ''}${day ? `-${day.padStart(2, '0')}` : ''}`
      : '当前分析周期';
    return {
      eyebrow: '当前分析',
      title: `${pageTitle} · ${period}`,
      detail: `${building} · ${params.get('type') ?? '全部设备类别'}`,
    };
  }

  return { eyebrow: '当前页面', title: pageTitle, detail: `${building} · 自动读取页面与业务摘要` };
}

function getConversationTitle(messages: AssistantDisplayMessage[]) {
  const firstUserMessage = messages.find((message) => message.user)?.content.trim();
  return firstUserMessage ? firstUserMessage.slice(0, 34) : 'AI 运维分析';
}

function getConversationSummary(messages: AssistantDisplayMessage[]) {
  const lastAssistantMessage = [...messages].reverse().find((message) => !message.user)?.content.trim();
  return lastAssistantMessage ? lastAssistantMessage.slice(0, 70) : '等待 AI 返回分析结果';
}

function AgentPanelHome({
  focus,
  skills,
  onSkill,
  onRotate,
}: {
  focus: FocusContext;
  skills: AgentSkill[];
  onSkill: (skill: AgentSkill) => void;
  onRotate: () => void;
}) {
  return (
    <div className="global-ai-home">
      <section className="global-ai-welcome" aria-labelledby="global-ai-welcome-title">
        <div className="global-ai-welcome-copy">
          <span>您好，欢迎使用</span>
          <strong id="global-ai-welcome-title">HVAC 智慧能源 AI 运维助手</strong>
        </div>
        <div className="global-ai-welcome-mark" aria-hidden="true">
          <RobotOutlined />
        </div>
      </section>

      <section className="global-ai-current-context" aria-label="AI 当前上下文">
        <span>{focus.eyebrow}</span>
        <strong>{focus.title}</strong>
        <small>{focus.detail}</small>
      </section>

      <section className="global-ai-skills" aria-labelledby="global-ai-skills-title">
        <div className="global-ai-skills-heading">
          <div>
            <strong id="global-ai-skills-title">技能（Skills）</strong>
            <span>输入 <kbd>@</kbd> 唤起技能</span>
          </div>
          <Button type="text" icon={<ReloadOutlined />} onClick={onRotate}>换一换</Button>
        </div>
        <div className="global-ai-skill-list">
          {skills.map((skill) => (
            <button
              key={skill.key}
              type="button"
              className="global-ai-skill-card"
              onClick={() => onSkill(skill)}
            >
              <span className="global-ai-skill-copy">
                <strong>{skill.title}</strong>
                <span>{skill.description}</span>
              </span>
              <RightOutlined />
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}

function AgentAnalysisProgress() {
  return (
    <details className="global-ai-processing" open>
      <summary>
        <span className="global-ai-processing-title">
          <LoadingOutlined spin />
          <strong>处理中</strong>
          <span>3 步</span>
        </span>
        <DownOutlined />
      </summary>
      <div className="global-ai-processing-body">
        <div className="global-ai-processing-mode">
          <ThunderboltOutlined />
          <span>深度分析</span>
          <RightOutlined />
        </div>
        <div className="global-ai-processing-step is-complete">
          <CheckCircleOutlined />
          <span><strong>读取页面上下文</strong><small>识别当前对象、范围和角色权限</small></span>
        </div>
        <div className="global-ai-processing-step is-complete">
          <CheckCircleOutlined />
          <span><strong>汇总业务摘要</strong><small>读取已接入的遥测、诊断、工单与优化摘要</small></span>
        </div>
        <div className="global-ai-processing-step is-active">
          <LoadingOutlined spin />
          <span><strong>生成解释与建议</strong><small>组织证据、边界和下一步动作</small></span>
        </div>
      </div>
    </details>
  );
}

function ConversationHistory({
  currentMessages,
  entries,
  onBack,
  onOpenCurrent,
  onOpenEntry,
}: {
  currentMessages: AssistantDisplayMessage[];
  entries: ConversationHistoryEntry[];
  onBack: () => void;
  onOpenCurrent: () => void;
  onOpenEntry: (entry: ConversationHistoryEntry) => void;
}) {
  const hasAnyHistory = currentMessages.length > 0 || entries.length > 0;

  return (
    <div className="global-ai-history" aria-label="AI 对话历史">
      <div className="global-ai-history-heading">
        <Button type="text" icon={<ArrowLeftOutlined />} onClick={onBack} aria-label="返回 AI 对话" />
        <div>
          <strong>对话历史</strong>
          <span>仅保留当前浏览器会话</span>
        </div>
      </div>

      {hasAnyHistory ? (
        <div className="global-ai-history-list">
          {currentMessages.length > 0 ? (
            <button type="button" className="global-ai-history-item is-current" onClick={onOpenCurrent}>
              <MessageOutlined />
              <span>
                <strong>{getConversationTitle(currentMessages)}</strong>
                <small>{getConversationSummary(currentMessages)}</small>
              </span>
              <time>当前</time>
            </button>
          ) : null}
          {entries.map((entry) => (
            <button key={entry.id} type="button" className="global-ai-history-item" onClick={() => onOpenEntry(entry)}>
              <MessageOutlined />
              <span>
                <strong>{entry.title}</strong>
                <small>{entry.summary}</small>
              </span>
              <time>{entry.createdAt.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>
            </button>
          ))}
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无本地会话记录" />
      )}
    </div>
  );
}

function AssistantDrawer({ session }: AssistantDrawerProps) {
  const location = useLocation();
  const screens = Grid.useBreakpoint();
  const context = useAiApplicationContext();
  const [open, setOpen] = useState(false);
  const [panelMode, setPanelMode] = useState<PanelMode>('standard');
  const [view, setView] = useState<PanelView>(session.messages.length ? 'chat' : 'home');
  const [skillOffset, setSkillOffset] = useState(0);
  const [historyEntries, setHistoryEntries] = useState<ConversationHistoryEntry[]>([]);

  const focus = useMemo(
    () => getFocusContext(location.pathname, location.search, context.pageTitle, context.buildingId),
    [context.buildingId, context.pageTitle, location.pathname, location.search],
  );

  const visibleSkills = useMemo(() => (
    AGENT_SKILLS.map((_, index) => AGENT_SKILLS[(index + skillOffset) % AGENT_SKILLS.length])
  ), [skillOffset]);

  const attentionCount = context.metrics.highRiskDiagnoses
    + context.metrics.slaRiskWorkOrders
    + context.metrics.pendingOptimizations;
  const panelWidth = screens.md ? (panelMode === 'focus' ? 860 : 600) : '100%';

  useEffect(() => {
    if (session.messages.length > 0 && view === 'home') setView('chat');
  }, [session.messages.length, view]);

  const archiveCurrentConversation = () => {
    if (session.messages.length === 0) return;
    const snapshot = session.messages.map((message) => ({ ...message }));
    setHistoryEntries((entries) => [{
      id: `conversation-${Date.now()}`,
      title: getConversationTitle(snapshot),
      summary: getConversationSummary(snapshot),
      createdAt: new Date(),
      messages: snapshot,
    }, ...entries].slice(0, 20));
  };

  const startNewConversation = () => {
    if (session.loading) session.stop();
    archiveCurrentConversation();
    session.clear();
    setView('home');
  };

  const restoreConversation = (entry: ConversationHistoryEntry) => {
    if (session.loading) session.stop();
    session.replaceMessages(entry.messages.map((message) => ({ ...message })));
    setView('chat');
  };

  const submitSkill = (skill: AgentSkill) => {
    setView('chat');
    void session.submit(skill.prompt);
  };

  const closePanel = () => {
    setOpen(false);
    setPanelMode('standard');
  };

  return (
    <>
      {!open ? (
        <Tooltip title="打开 AI 运维助手" placement="left">
          <Button
            className="global-ai-launcher"
            type="primary"
            icon={<ThunderboltOutlined />}
            onClick={() => setOpen(true)}
            aria-label="打开 AI 运维助手"
          >
            <span>AI 运维助手</span>
            <span className="global-ai-launcher-count">{attentionCount || '在线'}</span>
          </Button>
        </Tooltip>
      ) : null}

      <Drawer
        open={open}
        onClose={closePanel}
        mask={false}
        closable={false}
        push={false}
        width={panelWidth}
        rootClassName={`global-ai-floating-root is-${panelMode}`}
        title={
          <div className="global-ai-panel-title">
            <Avatar className="global-ai-panel-mark" icon={<RobotOutlined />} />
            <div>
              <span className="global-ai-panel-name">
                <Text strong>{AI_ASSISTANT_NAME}</Text>
                <em>Agent</em>
              </span>
              <Text type="secondary">{focus.title}</Text>
            </div>
          </div>
        }
        extra={
          <Space size={2} className="global-ai-window-actions">
            <Tooltip title="新建会话">
              <Button type="text" icon={<PlusOutlined />} onClick={startNewConversation} aria-label="新建 AI 会话" />
            </Tooltip>
            <Tooltip title="对话历史">
              <Button
                type="text"
                icon={<ClockCircleOutlined />}
                onClick={() => setView('history')}
                aria-label="打开 AI 对话历史"
              />
            </Tooltip>
            {screens.md ? (
              <Tooltip title={panelMode === 'focus' ? '恢复标准宽度' : '展开聚焦视图'}>
                <Button
                  type="text"
                  icon={panelMode === 'focus' ? <CompressOutlined /> : <ExpandOutlined />}
                  onClick={() => setPanelMode((current) => (current === 'focus' ? 'standard' : 'focus'))}
                  aria-label={panelMode === 'focus' ? '恢复 AI 面板标准宽度' : '展开 AI 面板聚焦视图'}
                />
              </Tooltip>
            ) : null}
            <Tooltip title="关闭">
              <Button type="text" icon={<CloseOutlined />} onClick={closePanel} aria-label="关闭 AI 面板" />
            </Tooltip>
          </Space>
        }
        styles={{ body: { padding: 0, display: 'flex', flexDirection: 'column', minHeight: 0 } }}
      >
        {view === 'history' ? (
          <ConversationHistory
            currentMessages={session.messages}
            entries={historyEntries}
            onBack={() => setView(session.messages.length ? 'chat' : 'home')}
            onOpenCurrent={() => setView('chat')}
            onOpenEntry={restoreConversation}
          />
        ) : (
          <div className="global-ai-main-view">
            <AssistantConversation
              session={session}
              prompts={[]}
              variant="drawer"
              emptyDescription={`我已读取「${context.pageTitle}」上下文，可以直接提问。`}
              emptyContent={
                <AgentPanelHome
                  focus={focus}
                  skills={visibleSkills}
                  onSkill={submitSkill}
                  onRotate={() => setSkillOffset((offset) => (offset + 2) % AGENT_SKILLS.length)}
                />
              }
              statusContent={session.loading ? <AgentAnalysisProgress /> : null}
              placeholder="输入 @ 唤起技能，或直接描述运维问题"
              showClear={false}
              onSubmitStart={() => setView('chat')}
            />
            <div className="global-ai-disclaimer">
              <SafetyCertificateOutlined />
              <span>回答由 AI 生成，仅供参考；设备控制、工单流转和策略下发仍由权限与人工确认控制。</span>
            </div>
          </div>
        )}
      </Drawer>
    </>
  );
}

function CopilotKitAssistant() {
  const session = useCopilotAssistantSession();
  return (
    <>
      <CopilotContextBridge />
      <AssistantDrawer session={session} />
    </>
  );
}

function MockGlobalAssistant() {
  const session = useMockAssistantSession();
  return <AssistantDrawer session={session} />;
}

export default function GlobalAiAssistant() {
  const location = useLocation();
  if (location.pathname === '/ai') return null;
  return COPILOTKIT_ENABLED ? <CopilotKitAssistant /> : <MockGlobalAssistant />;
}
