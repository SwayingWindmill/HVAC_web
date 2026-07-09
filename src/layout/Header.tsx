import { useEffect, useState } from 'react';
import { Badge, Layout, Select, Segmented, Switch, Popover, List, Button, Tooltip, Avatar, Space } from 'antd';
import {
  BellOutlined, SunOutlined, MoonOutlined, MenuFoldOutlined, MenuUnfoldOutlined,
  ExperimentOutlined, UserOutlined, DesktopOutlined, ApiOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { ROLE_LABEL, useUi, type Role } from '@/store/ui';
import { mockAlarms, SEVERITY_LABEL } from '@/mock/data';
import { SEVERITY_COLOR } from '@/theme/tokens';
import { telemetry, type RealtimeStatus } from '@/api';

const { Header } = Layout;

// 全局实时连接状态徽标：消费 TelemetryClient 的连接状态（#11 / #8 实时层可见性）。
const STATUS_MAP: Record<RealtimeStatus, { color: string; text: string }> = {
  open: { color: '#22c55e', text: '实时已连接' },
  connecting: { color: '#f5a623', text: '实时连接中' },
  degraded: { color: '#f5a623', text: '实时连接降级' },
  closed: { color: '#ef4444', text: '实时未连接' },
};

function RealtimeBadge() {
  const [status, setStatus] = useState<RealtimeStatus>(telemetry.getStatus());
  useEffect(() => telemetry.onStatus(setStatus), []);
  const s = STATUS_MAP[status];
  return (
    <Tooltip title={s.text}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12, opacity: 0.85 }}>
        <ApiOutlined style={{ color: s.color }} />
        <span style={{ width: 8, height: 8, borderRadius: 8, background: s.color, boxShadow: `0 0 6px ${s.color}`, display: 'inline-block' }} />
        <span style={{ color: 'inherit' }}>实时</span>
      </span>
    </Tooltip>
  );
}

const BUILDINGS = [
  { value: 'b1', label: '总部大楼' },
  { value: 'b2', label: '研发中心' },
];

export default function TopHeader() {
  const navigate = useNavigate();
  const { role, themeMode, demoMode, buildingId, sidebarCollapsed,
    setRole, setThemeMode, toggleDemoMode, toggleSidebar, setBuilding } = useUi();

  const bell = (
    <Popover
      trigger="click"
      placement="bottomRight"
      content={
        <div style={{ width: 300 }}>
          <List
            size="small"
            dataSource={mockAlarms.slice(0, 4)}
            renderItem={(a) => (
              <List.Item style={{ cursor: 'pointer' }} onClick={() => navigate('/alarms')}>
                <List.Item.Meta
                  avatar={<Badge color={SEVERITY_COLOR[a.severity]} />}
                  title={<span style={{ fontSize: 13 }}>{a.text}</span>}
                  description={<span style={{ fontSize: 12 }}>
                    {a.device} · {SEVERITY_LABEL[a.severity]} · {a.ts}
                  </span>}
                />
              </List.Item>
            )}
          />
          <Button type="link" size="small" block onClick={() => navigate('/alarms')}>查看全部工单 ›</Button>
        </div>
      }
    >
      <Badge count={mockAlarms.filter((a) => a.severity === 'critical' || a.severity === 'major').length} size="small">
        <BellOutlined style={{ fontSize: 18 }} />
      </Badge>
    </Popover>
  );

  return (
    <Header
      style={{
        display: 'flex', alignItems: 'center', gap: 16, padding: '0 16px',
        borderBottom: '1px solid rgba(128,128,128,0.15)', height: 56,
      }}
    >
      <Button type="text" icon={sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={toggleSidebar} />
      <Select
        value={buildingId}
        options={BUILDINGS}
        onChange={setBuilding}
        style={{ width: 150 }}
        variant="filled"
      />
      <Segmented<Role>
        value={role}
        onChange={setRole}
        options={(['demo', 'ops', 'rd'] as Role[]).map((r) => ({ label: ROLE_LABEL[r], value: r }))}
      />
      <Space size={6}>
        <ExperimentOutlined style={{ color: demoMode ? '#0FB5AE' : undefined }} />
        <Switch size="small" checked={demoMode} onChange={toggleDemoMode} />
        <span style={{ fontSize: 12, opacity: 0.7 }}>演示数据</span>
      </Space>

      <div style={{ flex: 1 }} />

      <RealtimeBadge />
      <Tooltip title="进入演示大屏（全屏）">
        <Button type="text" icon={<DesktopOutlined />} onClick={() => navigate('/bigscreen')} />
      </Tooltip>
      {bell}
      <Tooltip title={themeMode === 'dark' ? '切浅色' : '切深色'}>
        <Button
          type="text"
          icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
          onClick={() => setThemeMode(themeMode === 'dark' ? 'light' : 'dark')}
        />
      </Tooltip>
      <Avatar style={{ background: '#0FB5AE' }} icon={<UserOutlined />} />
    </Header>
  );
}
