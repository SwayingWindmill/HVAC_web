import { Badge, Button, Divider, Grid, List, Popover, Select, Segmented, Space, Switch, Tooltip, Avatar } from 'antd';
import {
  BellOutlined, SunOutlined, MoonOutlined,
  ExperimentOutlined, UserOutlined, DesktopOutlined, ApiOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router';
import { ROLE_LABEL, useUi, type Role } from '@/store/ui';
import { mockAlarms, SEVERITY_LABEL } from '@/mock/data';
import { SEVERITY_COLOR } from '@/theme/tokens';
import { canViewPath } from '@/auth/permissions';

const { useBreakpoint } = Grid;

function RealtimeBadge() {
  const color = '#f5a623';
  return (
    <Tooltip title="当前为演示数据模式。">
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12, opacity: 0.85 }}>
        <ApiOutlined style={{ color }} />
        <span style={{ width: 8, height: 8, borderRadius: 8, background: color, display: 'inline-block' }} />
        <span style={{ color: 'inherit' }}>演示</span>
      </span>
    </Tooltip>
  );
}

const BUILDINGS = [
  { value: 'b1', label: '总部大楼' },
  { value: 'b2', label: '研发中心' },
];

export function DemoHeaderContent() {
  const screens = useBreakpoint();
  const { role, demoMode, buildingId, setRole, toggleDemoMode, setBuilding } = useUi();
  const narrow = !screens.xl;

  const viewControls = (
    <Space direction={narrow ? 'vertical' : 'horizontal'} size={10} align="start">
      <Select
        aria-label="选择演示建筑"
        value={buildingId}
        options={BUILDINGS}
        onChange={setBuilding}
        style={{ width: 210 }}
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
        {!narrow && <span style={{ fontSize: 12, opacity: 0.7 }}>演示数据</span>}
      </Space>
    </Space>
  );

  if (!narrow) return viewControls;
  return (
    <Popover
      trigger="click"
      placement="bottomLeft"
      content={<div style={{ width: 260 }}>{viewControls}<Divider style={{ margin: '10px 0' }} /><span style={{ fontSize: 12, opacity: 0.65 }}>视图配置仅影响前端演示上下文。</span></div>}
    >
      <Button size="small" icon={<ExperimentOutlined />}>视图配置</Button>
    </Popover>
  );
}

export function DemoHeaderActions() {
  const navigate = useNavigate();
  const screens = useBreakpoint();
  const { role, themeMode, setThemeMode } = useUi();
  const compact = !screens.xl;
  const canOpenAlarms = canViewPath(role, '/alarms');

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
    <Space size={compact ? 4 : 8}>
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
      {!compact && <Avatar style={{ background: '#0FB5AE' }} icon={<UserOutlined />} />}
    </Space>
  );
}
