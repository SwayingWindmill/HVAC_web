import type { ComponentProps, HTMLAttributes, ReactElement } from 'react';
import {
  CopilotChatView,
  useCopilotChatConfiguration,
} from '@copilotkit/react-core/v2';
import {
  ExpandOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { AI_ASSISTANT_NAME } from './config';
import { useAiApplicationContext } from './context';

type WelcomeScreenProps = ComponentProps<typeof CopilotChatView.WelcomeScreen>;
type WelcomeVariant = 'popup' | 'workspace';

function useAttentionItems() {
  const context = useAiApplicationContext();
  return [
    {
      label: '高风险诊断',
      value: context.metrics.highRiskDiagnoses,
      tone: 'critical',
    },
    {
      label: 'SLA 风险工单',
      value: context.metrics.slaRiskWorkOrders,
      tone: 'warning',
    },
    {
      label: '待决策优化',
      value: context.metrics.pendingOptimizations,
      tone: 'neutral',
    },
  ] as const;
}

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
          <span className="hvac-copilot-header-presence" aria-hidden="true" />
          <div className="hvac-copilot-header-copy">
            <div className="hvac-copilot-header-title-row">
              <strong>{AI_ASSISTANT_NAME}</strong>
              <span className="hvac-copilot-readonly-badge">只读</span>
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

function HvacCopilotWelcomeBase({
  variant,
  ...props
}: WelcomeScreenProps & { variant: WelcomeVariant }) {
  const context = useAiApplicationContext();
  const attentionItems = useAttentionItems();
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
          <strong>实时上下文已接入</strong>
          <small>{context.pageTitle}</small>
        </div>
        <h2>{context.welcomeTitle}</h2>
        <p>{context.pageDescription}</p>
      </section>

      <div className="hvac-copilot-scope-line" aria-label="当前 AI 上下文">
        <span>分析范围</span>
        <strong title={context.scopeLabel}>{context.scopeLabel}</strong>
        <small>{context.roleLabel}</small>
      </div>

      <section className="hvac-copilot-attention" aria-label="当前运维关注">
        <header>
          <span>当前关注</span>
          <strong>{context.attentionCount > 0 ? `${context.attentionCount} 项` : '暂无高风险事项'}</strong>
        </header>
        <div className="hvac-copilot-attention-list">
          {attentionItems.map((item) => (
            <div key={item.label} data-tone={item.tone}>
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
          ))}
        </div>
      </section>

      <section className="hvac-copilot-suggestion-section">
        <div className="hvac-copilot-section-heading">建议提问</div>
        {suggestionView}
      </section>

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
