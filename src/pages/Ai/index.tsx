import { Button, Progress, Space, Tag, Typography } from 'antd';
import { CopilotChat, useAgent, useCopilotChatConfiguration } from '@copilotkit/react-core/v2';
import {
  AlertOutlined,
  ApiOutlined,
  ArrowRightOutlined,
  CheckCircleFilled,
  DatabaseOutlined,
  DollarOutlined,
  FileTextOutlined,
  LockOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import PageScaffold from '@/components/PageScaffold';
import CopilotContextBridge from '@/ai/CopilotContextBridge';
import {
  HvacCopilotDisclaimer,
  HvacCopilotWorkspaceWelcomeScreen,
} from '@/ai/HvacCopilotUi';
import { AI_ASSISTANT_NAME, COPILOTKIT_RUNTIME_CONFIGURED } from '@/ai/config';
import { useAiApplicationContext } from '@/ai/context';
import { useTelemetryLive } from '@/api';
import { MOCK_DEVICES } from '@/api/mock';
import { ROLE_LABEL, useUi } from '@/store/ui';
import './Ai.css';

const { Text } = Typography;

function AiWorkspace() {
  const navigate = useNavigate();
  const context = useAiApplicationContext();
  const { role, demoMode } = useUi();
  const { agent } = useAgent({ throttleMs: 40 });
  const configuration = useCopilotChatConfiguration();
  const live = useTelemetryLive(MOCK_DEVICES, ['power', 'cop', 'load', 'supplyTemp', 'returnTemp']);
  const value = (deviceId: string, key: string) => live.get(deviceId, key) ?? 0;
  const totalPower = Math.round(MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'power'), 0));
  const averageCop = Math.round((MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'cop'), 0) / MOCK_DEVICES.length) * 100) / 100;
  const averageLoad = Math.round(MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'load'), 0) / MOCK_DEVICES.length);
  const supply = Math.round((MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'supplyTemp'), 0) / MOCK_DEVICES.length) * 10) / 10;
  const returnTemperature = Math.round((MOCK_DEVICES.reduce((sum, deviceId) => sum + value(deviceId, 'returnTemp'), 0) / MOCK_DEVICES.length) * 10) / 10;
  const temperatureDelta = Math.round((returnTemperature - supply) * 10) / 10;

  const sources = [
    { label: '实时遥测', value: `${MOCK_DEVICES.length} 台设备`, detail: `${totalPower} kW · COP ${averageCop}`, tone: 'live' },
    { label: 'FDD 诊断', value: `${context.metrics.activeDiagnoses} 条`, detail: `${context.metrics.highRiskDiagnoses} 条高风险`, tone: context.metrics.highRiskDiagnoses ? 'critical' : 'normal' },
    { label: '报警工单', value: `${context.metrics.activeWorkOrders} 条`, detail: `${context.metrics.slaRiskWorkOrders} 条 SLA 风险`, tone: context.metrics.slaRiskWorkOrders ? 'warning' : 'normal' },
    { label: '优化建议', value: `${context.metrics.pendingOptimizations} 条`, detail: '待决策，不自动下发', tone: 'normal' },
  ];

  const handoffs = [
    { label: '查看 FDD 证据', description: '诊断规则、根因与证据链', path: '/fdd', icon: <AlertOutlined /> },
    { label: '处理报警工单', description: '责任流转、SLA 与闭环记录', path: '/alarms', icon: <FileTextOutlined /> },
    { label: '评审优化建议', description: '收益、风险、审批与回滚', path: '/optimize', icon: <ThunderboltOutlined /> },
    { label: '核对收益绩效', description: '费用、节能量与 ROI', path: '/cost', icon: <DollarOutlined /> },
  ];

  return (
    <PageScaffold
      title="AI 运维中枢"
      subtitle="跨设备、能耗、诊断和工单持续调查；结论保留证据来源，动作回到业务模块完成。"
      eyebrow="统一智能运维层"
      extra={
        <Space size={8} wrap>
          <Tag icon={<LockOutlined />}>只读分析</Tag>
          <Tag color={COPILOTKIT_RUNTIME_CONFIGURED ? 'green' : 'default'} icon={<ApiOutlined />}>
            {COPILOTKIT_RUNTIME_CONFIGURED ? 'Runtime 在线' : '本地 Agent'}
          </Tag>
          <Tag>{ROLE_LABEL[role]}</Tag>
        </Space>
      }
    >
      <div className="ai-command-meta" aria-label="AI 工作台状态">
        <div className="ai-command-scope">
          <span className="ai-presence-dot" aria-hidden="true" />
          <div>
            <span>当前分析范围</span>
            <strong title={context.scopeLabel}>{context.scopeLabel}</strong>
          </div>
        </div>
        <div className="ai-command-facts">
          <span>{demoMode ? '演示数据' : '接入数据'}</span>
          <span>{MOCK_DEVICES.length} 台设备遥测</span>
          <span>{context.attentionCount} 项待关注</span>
        </div>
      </div>

      <div className="ai-workbench">
        <section className="ai-chat-pane" aria-label="AI 运维对话工作台">
          <header className="ai-chat-pane-header">
            <div>
              <span>当前会话</span>
              <h2>{AI_ASSISTANT_NAME}</h2>
            </div>
            <div className="ai-chat-pane-actions">
              <span>{agent.messages.length ? `${agent.messages.length} 条消息` : '尚未开始'}</span>
              <Button
                size="small"
                icon={<PlusOutlined />}
                onClick={() => configuration?.startNewThread()}
              >
                新建会话
              </Button>
            </div>
          </header>

          <div className="ai-chat-scope-row">
            <span>上下文</span>
            <strong title={context.scopeLabel}>{context.scopeLabel}</strong>
            <small>与全局 Popup 共用线程</small>
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

        <aside className="ai-context-rail" aria-label="AI 运维上下文">
          <section className="ai-rail-section ai-operating-pulse">
            <header className="ai-rail-heading">
              <div>
                <span>运行态</span>
                <h2>{context.buildingLabel}</h2>
              </div>
              <span className="ai-live-label"><i aria-hidden="true" />实时</span>
            </header>

            <div className="ai-primary-reading">
              <span>综合 COP</span>
              <strong>{averageCop.toFixed(2)}</strong>
              <small>{averageCop < 4.5 ? '低于健康阈值 4.50' : '处于健康区间'}</small>
            </div>

            <dl className="ai-reading-list">
              <div><dt>总功率</dt><dd>{totalPower}<small>kW</small></dd></div>
              <div><dt>平均负荷</dt><dd>{averageLoad}<small>%</small></dd></div>
              <div><dt>高风险诊断</dt><dd data-tone={context.metrics.highRiskDiagnoses ? 'critical' : 'normal'}>{context.metrics.highRiskDiagnoses}</dd></div>
            </dl>

            <div className="ai-temperature-summary">
              <div className="ai-temperature-heading">
                <Text strong>冷冻水温差</Text>
                <Text type="secondary">{temperatureDelta}℃</Text>
              </div>
              <Progress
                percent={Math.min(100, Math.max(0, Math.round(temperatureDelta * 18)))}
                size="small"
                status={temperatureDelta < 3.5 ? 'exception' : 'active'}
                showInfo={false}
              />
              <Text type="secondary">供 {supply}℃ / 回 {returnTemperature}℃</Text>
            </div>
          </section>

          <section className="ai-rail-section">
            <header className="ai-rail-heading">
              <div>
                <span>证据覆盖</span>
                <h2>已接入数据</h2>
              </div>
              <DatabaseOutlined aria-hidden="true" />
            </header>
            <div className="ai-source-list">
              {sources.map((source) => (
                <div className="ai-source-row" key={source.label} data-tone={source.tone}>
                  <i aria-hidden="true" />
                  <div>
                    <strong>{source.label}</strong>
                    <span>{source.detail}</span>
                  </div>
                  <b>{source.value}</b>
                </div>
              ))}
            </div>
          </section>

          <section className="ai-rail-section">
            <header className="ai-rail-heading">
              <div>
                <span>业务交接</span>
                <h2>进入处置闭环</h2>
              </div>
            </header>
            <div className="ai-handoff-list">
              {handoffs.map((item) => (
                <Button
                  key={item.path}
                  type="text"
                  className="ai-handoff-row"
                  onClick={() => navigate(item.path)}
                >
                  <span className="ai-handoff-icon" aria-hidden="true">{item.icon}</span>
                  <span className="ai-handoff-copy">
                    <strong>{item.label}</strong>
                    <small>{item.description}</small>
                  </span>
                  <ArrowRightOutlined aria-hidden="true" />
                </Button>
              ))}
            </div>
          </section>

          <div className="ai-governance-note">
            <CheckCircleFilled aria-hidden="true" />
            <span>AI 负责解释、归因和建议；审批、派工与设备控制继续由原业务权限和二次确认约束。</span>
          </div>
        </aside>
      </div>
    </PageScaffold>
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
