import { useEffect } from 'react';
import { Badge, Button, Divider, Grid, Layout, List, Popover, Select, Segmented, Space, Switch, Tooltip, Avatar } from 'antd';
import {
  BellOutlined, SunOutlined, MoonOutlined, MenuFoldOutlined, MenuUnfoldOutlined,
  ExperimentOutlined, UserOutlined, DesktopOutlined, ApiOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { ROLE_LABEL, useUi, type Role } from '@/store/ui';
import { API_MODE } from '@/api/config';
import { useAuthorizedRegistrySites } from '@/api/registry';
import { mockAlarms, SEVERITY_LABEL } from '@/mock/data';
import { SEVERITY_COLOR } from '@/theme/tokens';
import { canViewPath } from '@/auth/permissions';

const { Header } = Layout;
const { useBreakpoint } = Grid;

// S2 live sessions are device-scoped. The header must not claim a global Socket.IO connection.
function RealtimeBadge() {
  const realMode = API_MODE === 'real';
  const color = realMode ? '#0FB5AE' : '#f5a623';
  const text = realMode
    ? 'S2 实时连接按可见设备会话建立；此处不代表全局连接状态。'
    : '当前为演示数据模式。';
  return (
    <Tooltip title={text}>
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12, opacity: 0.85 }}>
        <ApiOutlined style={{ color }} />
        <span style={{ width: 8, height: 8, borderRadius: 8, background: color, display: 'inline-block' }} />
        <span style={{ color: 'inherit' }}>{realMode ? 'S2 实时' : '演示'}</span>
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
  const registrySitesQuery = useAuthorizedRegistrySites(API_MODE === 'real');
  const siteOptions = API_MODE === 'real'
    ? (registrySitesQuery.data ?? []).map(({ organization, site }) => ({
        value: site.id,
        label: `${organization.displayName} / ${site.displayName}`,
      }))
    : BUILDINGS;
  const selectedSiteValue = siteOptions.some((option) => option.value === buildingId) ? buildingId : undefined;
  const siteSelectorHint = API_MODE === 'real' && registrySitesQuery.isError
    ? '无法读取授权 Site；真实模式不会显示本地演示站点。'
    : API_MODE === 'real' && !registrySitesQuery.isPending && siteOptions.length === 0
      ? '当前账号没有可见的 Site。'
      : undefined;

  useEffect(() => {
    if (API_MODE !== 'real' || registrySitesQuery.isPending || registrySitesQuery.isError) return;
    const firstSiteId = registrySitesQuery.data?.[0]?.site.id;
    if (firstSiteId && !registrySitesQuery.data?.some(({ site }) => site.id === buildingId)) setBuilding(firstSiteId);
  }, [buildingId, registrySitesQuery.data, registrySitesQuery.isError, registrySitesQuery.isPending, setBuilding]);

  const viewControls = (
    <Space direction={narrow ? 'vertical' : 'horizontal'} size={10} align="start">
      <Tooltip title={siteSelectorHint}>
        <Select
          aria-label={API_MODE === 'real' ? '选择授权 Site' : '选择演示建筑'}
          value={selectedSiteValue}
          options={siteOptions}
          onChange={setBuilding}
          loading={API_MODE === 'real' && registrySitesQuery.isPending}
          disabled={API_MODE === 'real' && (registrySitesQuery.isError || siteOptions.length === 0)}
          placeholder={API_MODE === 'real' ? '选择授权 Site' : '选择演示建筑'}
          notFoundContent={siteSelectorHint ?? '暂无 Site'}
          status={API_MODE === 'real' && registrySitesQuery.isError ? 'error' : undefined}
          style={{ width: 210 }}
          variant="filled"
        />
      </Tooltip>
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
