import type { ReactNode } from 'react';
import { Button, Layout } from 'antd';
import { MenuFoldOutlined, MenuUnfoldOutlined } from '@ant-design/icons';

const { Header } = Layout;

export interface AppHeaderFrameProps {
  sidebarCollapsed: boolean;
  onToggleSidebar: () => void;
  compact: boolean;
  className?: string;
  children: ReactNode;
}

export default function AppHeaderFrame({
  sidebarCollapsed,
  onToggleSidebar,
  compact,
  className,
  children,
}: AppHeaderFrameProps) {
  return (
    <Header
      className={className}
      style={{
        display: 'flex', alignItems: 'center', gap: compact ? 8 : 16, padding: '0 16px',
        borderBottom: '1px solid rgba(128,128,128,0.15)', height: 56, minWidth: 0,
      }}
    >
      <Button
        type="text"
        aria-label={sidebarCollapsed ? '展开导航' : '收起导航'}
        icon={sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
        onClick={onToggleSidebar}
      />
      {children}
    </Header>
  );
}
