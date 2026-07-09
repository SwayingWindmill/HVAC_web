import { useMemo, useState, type ComponentType } from 'react';
import { Layout, Menu, Tree, Typography, Divider } from 'antd';
import {
  DashboardOutlined, ApartmentOutlined, FundOutlined, ThunderboltOutlined,
  BugOutlined, AlertOutlined, RobotOutlined, DollarOutlined, DesktopOutlined, SettingOutlined,
  ApartmentOutlined as TreeIcon,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { MODULES, visibleModules, useUi } from '@/store/ui';
import { mockTree } from '@/mock/data';

const { Sider } = Layout;
const ICONS: Record<string, ComponentType> = {
  DashboardOutlined, ApartmentOutlined, FundOutlined, ThunderboltOutlined,
  BugOutlined, AlertOutlined, RobotOutlined, DollarOutlined, DesktopOutlined, SettingOutlined,
};

export default function Sidebar() {
  const navigate = useNavigate();
  const location = useLocation();
  const role = useUi((s) => s.role);
  const collapsed = useUi((s) => s.sidebarCollapsed);
  const buildingId = useUi((s) => s.buildingId);
  const setBuilding = useUi((s) => s.setBuilding);
  const [treeSel, setTreeSel] = useState<string[]>([buildingId]);

  const items = useMemo(
    () =>
      visibleModules(role).map((m) => {
        const Icon = ICONS[m.icon];
        return { key: m.path, icon: <Icon />, label: m.label };
      }),
    [role],
  );

  const selectedKey = '/' + (location.pathname.split('/')[1] || 'dashboard');
  const openKey = MODULES.find((m) => m.path === selectedKey)?.path;

  return (
    <Sider
      collapsible
      collapsed={collapsed}
      trigger={null}
      width={236}
      collapsedWidth={64}
      style={{ borderInlineEnd: '1px solid rgba(128,128,128,0.15)' }}
    >
      <div style={{ height: 56, display: 'flex', alignItems: 'center', gap: 10, padding: '0 18px', fontWeight: 700 }}>
        <span style={{ color: '#0FB5AE', fontSize: 20 }}>❄</span>
        {!collapsed && <span>HVAC 节能平台</span>}
      </div>

      {!collapsed && (
        <div style={{ padding: '4px 12px 0' }}>
          <Typography.Text type="secondary" style={{ fontSize: 12, paddingLeft: 6 }}>
            全局设备树
          </Typography.Text>
          <Tree
            showIcon
            defaultExpandedKeys={['b1', 'b1-z1']}
            selectedKeys={treeSel}
            treeData={mockTree}
            icon={<TreeIcon style={{ color: '#0FB5AE' }} />}
            onSelect={(keys) => {
              const k = String(keys[0] ?? '');
              setTreeSel([k]);
              setBuilding(k.split('-')[0]);
            }}
            style={{ background: 'transparent', fontSize: 13 }}
          />
          <Divider style={{ margin: '8px 0' }} />
        </div>
      )}

      <Menu
        mode="inline"
        selectedKeys={[openKey ?? '/dashboard']}
        items={items}
        onClick={({ key }) => navigate(key)}
        style={{ background: 'transparent', borderInlineEnd: 'none' }}
      />
    </Sider>
  );
}
