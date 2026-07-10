import { useEffect, useState } from 'react';
import { Badge, Button, Divider, Grid, Layout, List, Popover, Select, Segmented, Space, Switch, Tooltip, Avatar } from 'antd';
import {
  BellOutlined, SunOutlined, MoonOutlined, MenuFoldOutlined, MenuUnfoldOutlined,
  ExperimentOutlined, UserOutlined, DesktopOutlined, ApiOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { ROLE_LABEL, useUi, type Role } from '@/store/ui';
import { mockAlarms, SEVERITY_LABEL } from '@/mock/data';
import { SEVERITY_COLOR } from '@/theme/tokens';
import { telemetry, type RealtimeStatus } from '@/api';
import { canViewPath } from '@/auth/permissions';

const { Header } = Layout;
const { useBreakpoint } = Grid;

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
  const screens = useBreakpoint();
  const { role, themeMode, demoMode, buildingId, sidebarCollapsed,
    setRole, setThemeMode, toggleDemoMode, toggleSidebar, setBuilding } = useUi();
  const compact = !screens.xl;
  const narrow = !screens.xl;
  const canOpenAlarms = canViewPath(role, '/alarms');

  const viewControls = (
    <Space direction={narrow ? 'vertical' : 'horizontal'} size={10} align="start">
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
        {!compact && <span style={{ fontSize: 12, opacity: 0.7 }}>演示数据</span>}
      </Space>
    </Space>
  );

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
              <List.Item
                style={{ cursor: canOpenAlarms ? 'pointer' : 'not-allowed', opacity: canOpenAlarms ? 1 : 0.55 }}
                onClick={() => canOpenAlarms && navigate('/alarms')}
              >
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
          <Tooltip title={canOpenAlarms ? undefined : '当前角色无权查看工单'}>
            <Button type="link" size="small" block disabled={!canOpenAlarms} onClick={() => navigate('/alarms')}>查看全部工单 ›</Button>
          </Tooltip>
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
        display: 'flex', alignItems: 'center', gap: compact ? 8 : 16, padding: '0 16px',
        borderBottom: '1px solid rgba(128,128,128,0.15)', height: 56, minWidth: 0,
      }}
    >
      <Button type="text" icon={sidebarCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={toggleSidebar} />
      {narrow ? (
        <Popover
          trigger="click"
          placement="bottomLeft"
          content={<div style={{ width: 260 }}>{viewControls}<Divider style={{ margin: '10px 0' }} /><span style={{ fontSize: 12, opacity: 0.65 }}>视图配置仅影响前端演示上下文。</span></div>}
        >
          <Button size="small" icon={<ExperimentOutlined />}>视图配置</Button>
        </Popover>
      ) : viewControls}

      <div style={{ flex: 1, minWidth: 8 }} />

      {!compact && <RealtimeBadge />}
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
      {!narrow && <Avatar style={{ background: '#0FB5AE' }} icon={<UserOutlined />} />}
    </Header>
  );
}
