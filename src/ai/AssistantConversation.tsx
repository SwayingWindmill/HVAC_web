import { useEffect, useRef, type ReactNode } from 'react';
import { Avatar, Button, Empty, Input, Tooltip } from 'antd';
import {
  ClearOutlined,
  RobotOutlined,
  SendOutlined,
  StopOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { BRAND } from '@/theme/tokens';
import type { AssistantSession } from './session';
import './AssistantConversation.css';

type AssistantConversationProps = {
  session: AssistantSession;
  prompts: string[];
  variant?: 'drawer' | 'workspace';
  emptyDescription: string;
  emptyContent?: ReactNode;
  statusContent?: ReactNode;
  placeholder?: string;
  showClear?: boolean;
  onSubmitStart?: () => void;
  ariaLabel?: string;
};

export default function AssistantConversation({
  session,
  prompts,
  variant = 'workspace',
  emptyDescription,
  emptyContent,
  statusContent,
  placeholder = '询问设备、能耗、故障、工单或优化问题…',
  showClear = true,
  onSubmitStart,
  ariaLabel = 'HVAC AI 运维助手对话',
}: AssistantConversationProps) {
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight;
  }, [session.messages]);

  const submit = (text?: string) => {
    onSubmitStart?.();
    return session.submit(text);
  };

  return (
    <section className={`assistant-conversation is-${variant}`} aria-label={ariaLabel}>
      <div ref={listRef} className="assistant-conversation-messages" aria-live="polite">
        {session.messages.length === 0 ? (
          emptyContent ?? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyDescription} />
        ) : session.messages.map((message) => (
          <div key={message.id} className={`assistant-message ${message.user ? 'is-user' : ''}`}>
            <Avatar
              size={variant === 'drawer' ? 'small' : 'default'}
              icon={message.user ? <UserOutlined /> : <RobotOutlined />}
              style={{
                background: message.user ? BRAND.teal : undefined,
                color: message.user ? '#fff' : BRAND.teal,
              }}
            />
            <div className="assistant-message-bubble">
              {message.content || (session.loading && !message.user ? '正在分析…' : '')}
            </div>
          </div>
        ))}
      </div>

      {statusContent}

      {prompts.length > 0 ? (
        <div className="assistant-prompt-strip" aria-label="推荐问题">
          {prompts.map((prompt) => (
            <Button
              key={prompt}
              size={variant === 'drawer' ? 'small' : 'middle'}
              onClick={() => void submit(prompt)}
              disabled={session.loading}
            >
              {prompt}
            </Button>
          ))}
        </div>
      ) : null}

      <div className="assistant-composer">
        <Input.TextArea
          value={session.input}
          onChange={(event) => session.setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault();
              void submit();
            }
          }}
          placeholder={placeholder}
          autoSize={{ minRows: variant === 'drawer' ? 2 : 2, maxRows: 6 }}
          disabled={session.loading}
          aria-label="向 HVAC AI 运维助手提问"
        />
        {session.loading ? (
          <Tooltip title="停止生成">
            <Button danger icon={<StopOutlined />} onClick={session.stop} aria-label="停止生成" />
          </Tooltip>
        ) : (
          <Button
            type="primary"
            icon={<SendOutlined />}
            onClick={() => void submit()}
            disabled={!session.input.trim()}
          >
            {variant === 'workspace' ? '发送' : null}
          </Button>
        )}
        {showClear ? (
          <Tooltip title="清空对话">
            <Button
              icon={<ClearOutlined />}
              onClick={session.clear}
              disabled={session.loading || session.messages.length === 0}
              aria-label="清空对话"
            />
          </Tooltip>
        ) : null}
      </div>
    </section>
  );
}
