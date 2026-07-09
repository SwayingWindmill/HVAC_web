import { useEffect, useRef } from 'react';
import {
  Card,
  Input,
  Button,
  Tag,
  Typography,
  Space,
  Empty,
  Tooltip,
  Avatar,
  theme,
} from 'antd';
import {
  RobotOutlined,
  UserOutlined,
  SendOutlined,
  StopOutlined,
  ClearOutlined,
  LockOutlined,
  BulbOutlined,
} from '@ant-design/icons';
import PageScaffold from '@/components/PageScaffold';
import { BRAND, STATUS } from '@/theme/tokens';
import { useAiChat, AI_MOCK_MODE } from '@/api/ai';
import { useTelemetryLive } from '@/api';
import { MOCK_DEVICES } from '@/api/mock';

const { Text } = Typography;

export default function Ai() {
  const { token } = theme.useToken();
  const { messages, input, setInput, send, isStreaming, stop, clear, suggested } = useAiChat();

  // 实时上下文摘要（仅用于左侧展示，证明只读查询接入了实时层）
  const live = useTelemetryLive(MOCK_DEVICES, ['power', 'cop', 'load', 'supplyTemp', 'returnTemp']);
  const getv = (d: string, k: string) => live.get(d, k) ?? 0;
  const sumPower = Math.round(MOCK_DEVICES.reduce((s, d) => s + getv(d, 'power'), 0));
  const avgCop =
    Math.round((MOCK_DEVICES.reduce((s, d) => s + getv(d, 'cop'), 0) / MOCK_DEVICES.length) * 100) / 100;

  const scrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  };

  return (
    <PageScaffold
      title="AI 中心"
      subtitle="HVAC 智能问答助手 · 基于实时遥测的自然语言查询"
      extra={
        <Tag color="default" icon={<LockOutlined />} style={{ fontSize: 13, padding: '2px 10px' }}>
          只读模式 · 不控制设备
        </Tag>
      }
    >
      <div style={{ display: 'flex', gap: 16, alignItems: 'stretch', flexWrap: 'wrap' }}>
        {/* 左侧：上下文 + 建议问题 */}
        <div style={{ flex: '1 1 260px', maxWidth: 320, minWidth: 240, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Card size="small" styles={{ body: { padding: 14 } }}>
            <Space direction="vertical" size={6} style={{ width: '100%' }}>
              <Text strong style={{ color: BRAND.tealStrong }}>
                <RobotOutlined /> 实时数据上下文
              </Text>
              <Text type="secondary" style={{ fontSize: 12 }}>
                只读注入当前遥测快照，用于回答你的问题：
              </Text>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 4 }}>
                <Tag color="blue">总功率 {sumPower} kW</Tag>
                <Tag color="cyan">综合 COP {avgCop}</Tag>
                <Tag>{MOCK_DEVICES.length} 台设备在线</Tag>
              </div>
              {AI_MOCK_MODE && (
                <Text type="secondary" style={{ fontSize: 11, marginTop: 2 }}>
                  演示模式：应答为本地模拟，数据来自实时遥测
                </Text>
              )}
            </Space>
          </Card>

          <Card size="small" title="建议问题" styles={{ body: { padding: 12 } }}>
            <Space direction="vertical" size={8} style={{ width: '100%' }}>
              {suggested.map((q) => (
                <Button
                  key={q}
                  type="dashed"
                  block
                  onClick={() => send(q)}
                  disabled={isStreaming}
                  style={{ textAlign: 'left', color: token.colorText }}
                >
                  <BulbOutlined style={{ color: BRAND.teal }} /> {q}
                </Button>
              ))}
            </Space>
          </Card>

          <Card size="small" styles={{ body: { padding: 12, fontSize: 12, color: token.colorTextSecondary } }}>
            <LockOutlined style={{ color: STATUS.ok }} /> 本助手仅查询 HVAC 运行数据，
            <Text strong>不提供任何设备下发或写操作入口</Text>，守住「人在回路」红线。
          </Card>
        </div>

        {/* 右侧：对话面板 */}
        <Card
          size="small"
          style={{ flex: '3 1 480px', minWidth: 320, display: 'flex', flexDirection: 'column' }}
          styles={{ body: { flex: 1, display: 'flex', flexDirection: 'column', padding: 0, minHeight: 0 } }}
        >
          <div
            ref={scrollRef}
            style={{ flex: 1, overflowY: 'auto', padding: 16, display: 'flex', flexDirection: 'column', gap: 14 }}
          >
            {messages.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  <span style={{ color: token.colorTextSecondary }}>
                    你好，我是 HVAC 智能助手。问我园区能耗、COP、节能建议或供水温度吧。
                  </span>
                }
                style={{ margin: 'auto' }}
              />
            ) : (
              messages.map((m) => {
                const isUser = m.role === 'user';
                return (
                  <div
                    key={m.id}
                    style={{ display: 'flex', gap: 10, flexDirection: isUser ? 'row-reverse' : 'row' }}
                  >
                    <Avatar
                      style={{
                        background: isUser ? BRAND.teal : token.colorFillSecondary,
                        color: isUser ? '#fff' : BRAND.tealStrong,
                        flexShrink: 0,
                      }}
                      icon={isUser ? <UserOutlined /> : <RobotOutlined />}
                    />
                    <div
                      style={{
                        maxWidth: '78%',
                        background: isUser ? BRAND.teal : token.colorBgContainer,
                        color: isUser ? '#fff' : token.colorText,
                        border: isUser ? 'none' : `1px solid ${token.colorBorderSecondary}`,
                        borderRadius: 12,
                        padding: '10px 14px',
                        whiteSpace: 'pre-wrap',
                        lineHeight: 1.6,
                        fontSize: 14,
                        boxShadow: '0 1px 2px rgba(0,0,0,0.04)',
                      }}
                    >
                      {m.content ||
                        (isStreaming && m.role === 'assistant' ? (
                          <Text type="secondary">正在输入…</Text>
                        ) : null)}
                    </div>
                  </div>
                );
              })
            )}
          </div>

          {/* 输入区 */}
          <div
            style={{
              borderTop: `1px solid ${token.colorBorderSecondary}`,
              padding: 12,
              display: 'flex',
              gap: 8,
              alignItems: 'flex-end',
            }}
          >
            <Input.TextArea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={onKey}
              placeholder="输入你的问题，Enter 发送 / Shift+Enter 换行"
              autoSize={{ minRows: 1, maxRows: 4 }}
              disabled={isStreaming}
              style={{ flex: 1 }}
            />
            {isStreaming ? (
              <Tooltip title="停止生成">
                <Button icon={<StopOutlined />} onClick={stop} danger />
              </Tooltip>
            ) : (
              <Button type="primary" icon={<SendOutlined />} onClick={() => send()} disabled={!input.trim()}>
                发送
              </Button>
            )}
            <Tooltip title="清空对话">
              <Button icon={<ClearOutlined />} onClick={clear} disabled={isStreaming} />
            </Tooltip>
          </div>
        </Card>
      </div>
    </PageScaffold>
  );
}
