import { Button, Card, Col, Progress, Row, Space, Tag, Typography } from 'antd';
import { CopilotChat, useAgent, useCopilotChatConfiguration } from '@copilotkit/react-core/v2';
import {
  AlertOutlined,
  ApiOutlined,
  CheckCircleOutlined,
  DatabaseOutlined,
  DollarOutlined,
  FileTextOutlined,
  LockOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import PageScaffold from '@/components/PageScaffold';
import {
  OperationsInsightBand,
  OperationsMetrics,
  OperationsPanelHeading,
} from '@/components/OperationsUI';
import CopilotContextBridge from '@/ai/CopilotContextBridge';
import {
  HvacCopilotDisclaimer,
  HvacCopilotWelcomeScreen,
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
  const { role, buildingId, demoMode } = useUi();
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
    { label: '实时遥测', value: `${MOCK_DEVICES.length} 台设备`, detail: `${totalPower} kW · COP ${averageCop}` },
    { label: 'FDD 诊断', value: `${context.metrics.activeDiagnoses} 条`, detail: `${context.metrics.highRiskDiagnoses} 条高风险` },
    { label: '报警工单', value: `${context.metrics.activeWorkOrders} 条活跃`, detail: `${context.metrics.slaRiskWorkOrders} 条 SLA 风险` },
    { label: '优化建议', value: `${context.metrics.pendingOptimizations} 条待决策`, detail: '审批与下发保持人在回路' },
  ];

  return (
    <PageScaffold
      title={AI_ASSISTANT_NAME}
      subtitle="主工作区使用 CopilotKit 官方 CopilotChat，与全局 Popup 共用同一个 Agent、线程、业务上下文和结构化结果组件。"
      eyebrow="智能分析与协作"
      extra={
        <Space size={8} wrap>
          <Tag icon={<LockOutlined />}>只读分析</Tag>
          <Tag color={COPILOTKIT_RUNTIME_CONFIGURED ? 'green' : 'gold'} icon={<ApiOutlined />}>
            {COPILOTKIT_RUNTIME_CONFIGURED ? 'CopilotKit Runtime' : '本地 Agent'}
          </Tag>
          <Tag>{ROLE_LABEL[role]}</Tag>
        </Space>
      }
    >
      <OperationsInsightBand
        title="助手边界"
        icon={<SafetyCertificateOutlined />}
        items={[
          { text: '解释数据、汇总证据、生成建议；不直接控制设备。', tone: 'positive' },
          { text: '策略修改必须进入节能优化审批，并保留回滚条件。', tone: 'warning' },
          { text: '故障处置和责任流转必须进入报警工单闭环。', tone: 'info' },
        ]}
      />

      <Row gutter={[16, 16]} align="stretch" className="ai-workspace-grid">
        <Col xs={24} xl={16}>
          <Card
            variant="borderless"
            className="ai-conversation-shell"
            title={
              <OperationsPanelHeading
                title="AI 运维工作台"
                icon={<RobotOutlined />}
                meta={agent.messages.length ? `${agent.messages.length} 条消息` : '等待提问'}
              />
            }
            extra={<span className="ops-chart-status is-positive">与全局助手共用会话</span>}
          >
            <div className="ai-chat-toolbar">
              <span title={context.scopeLabel}>当前范围：{context.scopeLabel}</span>
              <Button size="small" onClick={() => configuration?.startNewThread()}>
                新建会话
              </Button>
            </div>
            <div className="ai-copilot-chat-shell">
              <CopilotChat
                className="ai-copilot-chat"
                welcomeScreen={HvacCopilotWelcomeScreen}
                input={{
                  disclaimer: HvacCopilotDisclaimer,
                  showDisclaimer: true,
                }}
                labels={{
                  welcomeMessageText: context.welcomeTitle,
                  chatInputPlaceholder: context.inputPlaceholder,
                  chatDisclaimerText: 'AI 结论基于当前页面与已接入数据；设备控制和业务写入必须人工确认。',
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
          </Card>
        </Col>

        <Col xs={24} xl={8}>
          <div className="ai-context-rail">
            <Card
              variant="borderless"
              title={<OperationsPanelHeading title="实时运营上下文" meta={`建筑 ${buildingId}`} />}
            >
              <OperationsMetrics
                className="is-compact"
                ariaLabel="AI 实时运营上下文"
                items={[
                  { label: '总功率', value: totalPower, suffix: 'kW', tone: 'accent' },
                  { label: '综合 COP', value: averageCop, tone: averageCop < 4.5 ? 'warning' : 'positive' },
                  { label: '平均负荷', value: averageLoad, suffix: '%' },
                  { label: '高风险诊断', value: context.metrics.highRiskDiagnoses, tone: context.metrics.highRiskDiagnoses ? 'critical' : 'positive' },
                ]}
              />
              <div className="ai-temperature-summary">
                <div className="ai-temperature-heading">
                  <Text strong>冷冻水温差</Text>
                  <Text type="secondary">供 {supply}℃ / 回 {returnTemperature}℃</Text>
                </div>
                <Progress
                  percent={Math.min(100, Math.max(0, Math.round(temperatureDelta * 18)))}
                  size="small"
                  status={temperatureDelta < 3.5 ? 'exception' : 'active'}
                  showInfo={false}
                />
                <Text type="secondary">当前温差 {temperatureDelta}℃，助手会把该工况作为分析证据之一。</Text>
              </div>
            </Card>

            <Card
              variant="borderless"
              title={<OperationsPanelHeading title="已接入数据" icon={<DatabaseOutlined />} meta="自动上下文" />}
            >
              <div className="ai-source-list">
                {sources.map((source) => (
                  <div className="ai-source-row" key={source.label}>
                    <div>
                      <Text strong>{source.label}</Text>
                      <Text type="secondary">{source.detail}</Text>
                    </div>
                    <Text strong>{source.value}</Text>
                  </div>
                ))}
              </div>
            </Card>

            <Card
              variant="borderless"
              title={<OperationsPanelHeading title="进入业务闭环" meta="AI 不替代确认" />}
            >
              <div className="ai-action-grid">
                <Button icon={<AlertOutlined />} onClick={() => navigate('/fdd')}>查看 FDD 证据</Button>
                <Button icon={<FileTextOutlined />} onClick={() => navigate('/alarms')}>处理报警工单</Button>
                <Button icon={<ThunderboltOutlined />} onClick={() => navigate('/optimize')}>评审优化建议</Button>
                <Button icon={<DollarOutlined />} onClick={() => navigate('/cost')}>核对收益绩效</Button>
              </div>
              <div className="ai-governance-note">
                <CheckCircleOutlined />
                <span>{demoMode ? '当前使用演示数据。' : '当前使用接入数据。'} 所有写操作仍由对应业务页面、权限和二次确认控制。</span>
              </div>
            </Card>
          </div>
        </Col>
      </Row>
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
