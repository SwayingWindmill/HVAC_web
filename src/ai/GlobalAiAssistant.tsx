import { useMemo, useState } from 'react';
import { Avatar, Button, Drawer, Grid, Progress, Space, Tag, Tooltip, Typography } from 'antd';
import {
  CheckCircleOutlined,
  CloseOutlined,
  CompressOutlined,
  ExpandOutlined,
  FullscreenOutlined,
  MinusOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { canViewPath } from '@/auth/permissions';
import { useUi } from '@/store/ui';
import AssistantConversation from './AssistantConversation';
import CopilotContextBridge from './CopilotContextBridge';
import { AI_ASSISTANT_NAME, COPILOTKIT_ENABLED } from './config';
import { useAiApplicationContext } from './context';
import {
  useCopilotAssistantSession,
  useMockAssistantSession,
  type AssistantSession,
} from './session';
import './GlobalAiAssistant.css';

const { Text } = Typography;
const BUILDING_LABELS: Record<string, string> = { b1: '总部大楼', b2: '研发中心' };

type PanelMode = 'standard' | 'focus';

type FocusContext = {
  eyebrow: string;
  title: string;
  detail: string;
};

type AttentionItem = {
  key: string;
  title: string;
  detail: string;
  tone: 'critical' | 'warning' | 'info' | 'positive';
  prompt: string;
};

type AssistantDrawerProps = {
  session: AssistantSession;
};

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
    return { eyebrow: '当前分析', title: `${pageTitle} · ${period}`, detail: `${building} · ${params.get('type') ?? '全部设备类别'}` };
  }

  return { eyebrow: '当前页面', title: pageTitle, detail: `${building} · 自动读取页面与业务摘要` };
}

function AgentPanelHome({
  focus,
  attentionItems,
  prompts,
  onPrompt,
}: {
  focus: FocusContext;
  attentionItems: AttentionItem[];
  prompts: string[];
  onPrompt: (prompt: string) => void;
}) {
  return (
    <div className="global-ai-home">
      <section className="global-ai-focus-card" aria-label="AI 当前分析上下文">
        <span className="global-ai-section-label">{focus.eyebrow}</span>
        <strong>{focus.title}</strong>
        <span>{focus.detail}</span>
      </section>

      <section className="global-ai-home-section" aria-labelledby="global-ai-attention-title">
        <div className="global-ai-section-heading">
          <div>
            <span className="global-ai-section-label">Agent briefing</span>
            <strong id="global-ai-attention-title">当前需要关注</strong>
          </div>
          <Tag bordered={false}>只读分析</Tag>
        </div>
        <div className="global-ai-attention-list">
          {attentionItems.map((item, index) => (
            <button
              key={item.key}
              type="button"
              className={`global-ai-attention-item is-${item.tone}`}
              onClick={() => onPrompt(item.prompt)}
            >
              <span className="global-ai-attention-index">{String(index + 1).padStart(2, '0')}</span>
              <span className="global-ai-attention-copy">
                <strong>{item.title}</strong>
                <span>{item.detail}</span>
              </span>
              <span className="global-ai-attention-action">分析</span>
            </button>
          ))}
        </div>
      </section>

      <section className="global-ai-home-section" aria-labelledby="global-ai-actions-title">
        <div className="global-ai-section-heading">
          <div>
            <span className="global-ai-section-label">Context actions</span>
            <strong id="global-ai-actions-title">基于当前页面</strong>
          </div>
        </div>
        <div className="global-ai-action-grid">
          {prompts.slice(0, 4).map((prompt) => (
            <Button key={prompt} onClick={() => onPrompt(prompt)}>
              {prompt}
            </Button>
          ))}
        </div>
      </section>

      <div className="global-ai-home-note">
        <SafetyCertificateOutlined />
        <span>AI 会说明数据来源和边界；设备控制、工单流转和策略下发仍由权限与人工确认控制。</span>
      </div>
    </div>
  );
}

function AgentAnalysisProgress() {
  return (
    <div className="global-ai-analysis-progress" role="status" aria-live="polite">
      <div className="global-ai-analysis-progress-head">
        <div>
          <span>本轮分析</span>
          <strong>正在生成解释与建议</strong>
        </div>
        <Text type="secondary">3 / 3</Text>
      </div>
      <Progress percent={78} showInfo={false} size="small" />
      <div className="global-ai-analysis-steps">
        <span className="is-complete"><CheckCircleOutlined /> 页面上下文</span>
        <span className="is-complete"><CheckCircleOutlined /> 业务摘要</span>
        <span className="is-active"><span className="global-ai-step-dot" /> 生成回答</span>
      </div>
    </div>
  );
}

function AssistantDrawer({ session }: AssistantDrawerProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const screens = Grid.useBreakpoint();
  const context = useAiApplicationContext();
  const role = useUi((state) => state.role);
  const [open, setOpen] = useState(false);
  const [minimized, setMinimized] = useState(false);
  const [panelMode, setPanelMode] = useState<PanelMode>('standard');

  const focus = useMemo(
    () => getFocusContext(location.pathname, location.search, context.pageTitle, context.buildingId),
    [context.buildingId, context.pageTitle, location.pathname, location.search],
  );

  const attentionItems = useMemo<AttentionItem[]>(() => {
    const items: AttentionItem[] = [];
    if (context.metrics.highRiskDiagnoses > 0) {
      items.push({
        key: 'diagnoses',
        title: `${context.metrics.highRiskDiagnoses} 条高风险诊断待核查`,
        detail: '优先检查诊断证据、影响设备和是否需要进入工单闭环。',
        tone: 'critical',
        prompt: '总结当前高风险 FDD 诊断，并说明最应该先处理哪一项。',
      });
    }
    if (context.metrics.slaRiskWorkOrders > 0) {
      items.push({
        key: 'work-orders',
        title: `${context.metrics.slaRiskWorkOrders} 条工单存在 SLA 风险`,
        detail: '检查未接手、处理停滞和即将超时的工作项。',
        tone: 'warning',
        prompt: '按优先级总结当前 SLA 风险工单，并给出值班处理顺序。',
      });
    }
    if (context.metrics.pendingOptimizations > 0) {
      items.push({
        key: 'optimizations',
        title: `${context.metrics.pendingOptimizations} 条优化建议待决策`,
        detail: '评估收益、舒适性风险、前置条件和回滚要求。',
        tone: 'info',
        prompt: '总结待决策优化建议，说明收益、风险和审批关注点。',
      });
    }
    if (items.length === 0) {
      items.push({
        key: 'stable',
        title: '当前没有高风险业务待办',
        detail: '可以继续分析当前页面、设备状态或能耗变化。',
        tone: 'positive',
        prompt: '总结当前页面状态，并指出仍值得持续观察的指标。',
      });
    }
    return items.slice(0, 3);
  }, [context.metrics.highRiskDiagnoses, context.metrics.pendingOptimizations, context.metrics.slaRiskWorkOrders]);

  const attentionCount = context.metrics.highRiskDiagnoses
    + context.metrics.slaRiskWorkOrders
    + context.metrics.pendingOptimizations;
  const hasAssistantResult = session.messages.some((message) => !message.user && message.content.trim().length > 0);
  const panelWidth = screens.md ? (panelMode === 'focus' ? 860 : 600) : '100%';

  const openPanel = () => {
    setMinimized(false);
    setOpen(true);
  };

  const minimizePanel = () => {
    setMinimized(true);
    setOpen(false);
  };

  const closePanel = () => {
    setMinimized(false);
    setOpen(false);
    setPanelMode('standard');
  };

  return (
    <>
      {!open ? (
        <Tooltip title={minimized ? '继续 AI 分析' : '打开 AI 运维助手'} placement="left">
          <Button
            className={`global-ai-launcher ${minimized ? 'is-resume' : ''}`}
            type="primary"
            icon={<ThunderboltOutlined />}
            onClick={openPanel}
            aria-label={minimized ? '继续 AI 分析' : '打开 AI 运维助手'}
          >
            <span>{minimized ? '继续 AI 分析' : 'AI 运维助手'}</span>
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
              <Text strong>{AI_ASSISTANT_NAME}</Text>
              <Text type="secondary">{focus.title}</Text>
            </div>
          </div>
        }
        extra={
          <Space size={2} className="global-ai-window-actions">
            <Tooltip title="最小化">
              <Button type="text" icon={<MinusOutlined />} onClick={minimizePanel} aria-label="最小化 AI 面板" />
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
            {canViewPath(role, '/ai') ? (
              <Tooltip title="打开完整 AI 运维中心">
                <Button
                  type="text"
                  icon={<FullscreenOutlined />}
                  onClick={() => {
                    closePanel();
                    navigate('/ai');
                  }}
                  aria-label="打开完整 AI 运维中心"
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
        <div className="global-ai-scope-bar">
          <div className="global-ai-scope-copy">
            <span>{focus.eyebrow}</span>
            <strong>{focus.title}</strong>
            <small>{focus.detail}</small>
          </div>
          <div className="global-ai-scope-meta">
            <Tag bordered={false}>{session.modeLabel}</Tag>
            <Tag bordered={false} color="cyan">{context.roleLabel}</Tag>
          </div>
        </div>

        {session.loading ? <AgentAnalysisProgress /> : null}
        {hasAssistantResult && !session.loading ? (
          <div className="global-ai-result-status">
            <CheckCircleOutlined />
            <span>本轮分析已完成，结论基于当前页面和已接入业务摘要。</span>
            {canViewPath(role, '/ai') ? <Button type="link" onClick={() => navigate('/ai')}>进入完整工作台</Button> : null}
          </div>
        ) : null}

        <AssistantConversation
          session={session}
          prompts={session.messages.length ? context.suggestedPrompts : []}
          variant="drawer"
          emptyDescription={`我已读取「${context.pageTitle}」上下文，可以直接提问。`}
          emptyContent={
            <AgentPanelHome
              focus={focus}
              attentionItems={attentionItems}
              prompts={context.suggestedPrompts}
              onPrompt={(prompt) => void session.submit(prompt)}
            />
          }
        />
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
