import { useState } from 'react';
import { Avatar, Button, Drawer, Grid, Space, Tag, Tooltip, Typography } from 'antd';
import {
  FullscreenOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { BRAND } from '@/theme/tokens';
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

type AssistantDrawerProps = {
  session: AssistantSession;
};

function AssistantDrawer({ session }: AssistantDrawerProps) {
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
  const context = useAiApplicationContext();
  const role = useUi((state) => state.role);
  const [open, setOpen] = useState(false);

  return (
    <>
      <Tooltip title="打开 AI 运维助手" placement="left">
        <Button
          className="global-ai-launcher"
          type="primary"
          shape="circle"
          icon={<RobotOutlined />}
          onClick={() => setOpen(true)}
          aria-label="打开 AI 运维助手"
        />
      </Tooltip>

      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        width={screens.md ? 460 : '100%'}
        className="global-ai-drawer"
        title={
          <Space>
            <Avatar style={{ background: BRAND.teal }} icon={<RobotOutlined />} />
            <span>
              <Text strong>{AI_ASSISTANT_NAME}</Text>
              <br />
              <Text type="secondary" style={{ fontSize: 12 }}>{context.pageTitle} · {context.roleLabel}</Text>
            </span>
          </Space>
        }
        extra={canViewPath(role, '/ai') ? (
          <Tooltip title="打开完整 AI 工作台">
            <Button
              type="text"
              icon={<FullscreenOutlined />}
              onClick={() => {
                setOpen(false);
                navigate('/ai');
              }}
              aria-label="打开完整 AI 工作台"
            />
          </Tooltip>
        ) : null}
        styles={{ body: { padding: 0, display: 'flex', flexDirection: 'column', minHeight: 0 } }}
      >
        <div className="global-ai-context">
          <Space size={[6, 6]} wrap>
            <Tag color="cyan">建筑 {BUILDING_LABELS[context.buildingId] ?? context.buildingId}</Tag>
            <Tag>{session.modeLabel}</Tag>
            <Tag>活跃工单 {context.metrics.activeWorkOrders}</Tag>
            <Tag color={context.metrics.highRiskDiagnoses ? 'red' : 'green'}>
              高风险诊断 {context.metrics.highRiskDiagnoses}
            </Tag>
            <Tag color="gold">待决策优化 {context.metrics.pendingOptimizations}</Tag>
          </Space>
          <div className="global-ai-safety">
            <SafetyCertificateOutlined /> 只读分析；设备控制、工单流转和策略下发必须人工确认。
          </div>
        </div>

        <AssistantConversation
          session={session}
          prompts={context.suggestedPrompts}
          variant="drawer"
          emptyDescription={`我已读取「${context.pageTitle}」上下文，可以直接提问。`}
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
