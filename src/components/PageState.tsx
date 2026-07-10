import type { ReactNode } from 'react';
import { Alert, Button, Empty, Result, Space, Spin, Typography } from 'antd';
import { ExclamationCircleOutlined, LockOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { ROLE_LABEL, type Role } from '@/store/ui';
import type { PermissionSubject } from '@/auth/permissions';

interface AccessDeniedProps {
  role: Role;
  subject?: PermissionSubject;
  title?: string;
  subTitle?: string;
}

export function AccessDenied({ role, subject, title = '403', subTitle }: AccessDeniedProps) {
  const navigate = useNavigate();
  const message = subTitle ?? `当前角色「${ROLE_LABEL[role]}」无权访问${subject ? ` ${subject}` : '该页面'}。`;
  return (
    <Result
      status="403"
      title={title}
      subTitle={message}
      extra={<Button type="primary" onClick={() => navigate('/dashboard')}>返回总览驾驶舱</Button>}
    />
  );
}

interface ReadonlyNoticeProps {
  role: Role;
  message?: ReactNode;
  description?: ReactNode;
}

export function ReadonlyNotice({ role, message = '当前角色只读', description }: ReadonlyNoticeProps) {
  return (
    <Alert
      type="info"
      showIcon
      icon={<LockOutlined />}
      message={message}
      description={description ?? `当前角色「${ROLE_LABEL[role]}」可以查看数据，但不能执行该操作。`}
    />
  );
}

interface EmptyStateProps {
  description: ReactNode;
  action?: ReactNode;
}

export function EmptyState({ description, action }: EmptyStateProps) {
  return (
    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description}>
      {action}
    </Empty>
  );
}

interface LoadingStateProps {
  tip?: ReactNode;
  minHeight?: number;
}

export function LoadingState({ tip = '加载中', minHeight = 280 }: LoadingStateProps) {
  return (
    <div style={{ minHeight, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Spin tip={tip} />
    </div>
  );
}

interface ErrorStateProps {
  title?: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
}

export function ErrorState({ title = '加载失败', description = '请稍后重试，或返回总览驾驶舱。', action }: ErrorStateProps) {
  return (
    <InlineState
      icon={<ExclamationCircleOutlined style={{ color: '#DC2626', fontSize: 24 }} />}
      title={title}
      description={description}
      extra={action}
    />
  );
}

interface InlineStateProps {
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  extra?: ReactNode;
}

export function InlineState({ icon, title, description, extra }: InlineStateProps) {
  return (
    <Space direction="vertical" size={6} style={{ width: '100%', textAlign: 'center', padding: 24 }}>
      {icon}
      <Typography.Text strong>{title}</Typography.Text>
      {description && <Typography.Text type="secondary">{description}</Typography.Text>}
      {extra}
    </Space>
  );
}
