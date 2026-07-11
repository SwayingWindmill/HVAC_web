import type { ComponentProps, HTMLAttributes, ReactElement } from 'react';
import {
  CopilotChatView,
  useCopilotChatConfiguration,
} from '@copilotkit/react-core/v2';
import {
  ArrowRightOutlined,
  ExpandOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { AI_ASSISTANT_NAME } from './config';
import { useAiApplicationContext } from './context';

type WelcomeScreenProps = ComponentProps<typeof CopilotChatView.WelcomeScreen>;

export function HvacCopilotToggleIcon() {
  const context = useAiApplicationContext();
  const countLabel = context.attentionCount > 9 ? '9+' : String(context.attentionCount);

  return (
    <span className="hvac-copilot-toggle-content">
      <span className="hvac-copilot-toggle-mark" aria-hidden="true">✦</span>
      <span className="hvac-copilot-toggle-label">AI 运维助手</span>
      {context.attentionCount > 0 && (
        <span className="hvac-copilot-toggle-count" aria-label={`${context.attentionCount} 项待关注`}>
          {countLabel}
        </span>
      )}
    </span>
  );
}

export function HvacCopilotHeaderContent({ closeButton }: { closeButton: ReactElement }) {
  const navigate = useNavigate();
  const context = useAiApplicationContext();
  const configuration = useCopilotChatConfiguration();

  const openWorkspace = () => {
    configuration?.setModalOpen(false);
    navigate('/ai');
  };

  return (
    <div className="hvac-copilot-header">
      <div className="hvac-copilot-header-layout">
        <div className="hvac-copilot-header-identity">
          <span className="hvac-copilot-header-icon" aria-hidden="true">✦</span>
          <div className="hvac-copilot-header-copy">
            <div className="hvac-copilot-header-title-row">
              <strong>{AI_ASSISTANT_NAME}</strong>
              <span className="hvac-copilot-readonly-badge">只读分析</span>
            </div>
            <span className="hvac-copilot-header-scope" title={context.scopeLabel}>
              {context.scopeLabel}
            </span>
          </div>
        </div>
        <div className="hvac-copilot-header-actions">
          <button
            type="button"
            className="hvac-copilot-icon-button"
            aria-label="新建会话"
            title="新建会话"
            onClick={() => configuration?.startNewThread()}
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
    </div>
  );
}

export function HvacCopilotWelcomeScreen(props: WelcomeScreenProps) {
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
    <div {...rest} className={['hvac-copilot-welcome', className].filter(Boolean).join(' ')}>
      <section className="hvac-copilot-welcome-hero">
        <div className="hvac-copilot-welcome-mark" aria-hidden="true">
          <ThunderboltOutlined />
        </div>
        <div className="hvac-copilot-welcome-eyebrow">HVAC 智慧能源 Agent</div>
        <h2>{context.welcomeTitle}</h2>
        <p>{context.pageDescription}</p>
      </section>

      <section className="hvac-copilot-context-card" aria-label="当前 AI 上下文">
        <div className="hvac-copilot-context-icon" aria-hidden="true">
          <SafetyCertificateOutlined />
        </div>
        <div className="hvac-copilot-context-copy">
          <span>当前分析范围</span>
          <strong>{context.scopeLabel}</strong>
        </div>
        <span className="hvac-copilot-context-role">{context.roleLabel}</span>
      </section>

      <section className="hvac-copilot-metric-strip" aria-label="当前运维摘要">
        <div>
          <span>高风险诊断</span>
          <strong>{context.metrics.highRiskDiagnoses}</strong>
        </div>
        <div>
          <span>SLA 风险工单</span>
          <strong>{context.metrics.slaRiskWorkOrders}</strong>
        </div>
        <div>
          <span>待决策优化</span>
          <strong>{context.metrics.pendingOptimizations}</strong>
        </div>
      </section>

      <section className="hvac-copilot-suggestion-section">
        <div className="hvac-copilot-section-heading">
          <span>建议开始</span>
          <ArrowRightOutlined aria-hidden="true" />
        </div>
        {suggestionView}
      </section>

      <div className="hvac-copilot-welcome-input">{input}</div>
    </div>
  );
}

export function HvacCopilotDisclaimer(props: HTMLAttributes<HTMLDivElement>) {
  const { className, ...rest } = props;
  return (
    <div {...rest} className={['hvac-copilot-disclaimer', className].filter(Boolean).join(' ')}>
      AI 结论基于当前页面与已接入数据；设备控制和业务写入必须人工确认。
    </div>
  );
}
