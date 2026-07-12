import { useEffect, useMemo, type ComponentType, type ReactNode } from 'react';
import { Drawer, Grid, Layout, Menu } from 'antd';
import type { MenuProps } from 'antd';
import {
  DashboardOutlined,
  ApartmentOutlined,
  FundOutlined,
  ThunderboltOutlined,
  BugOutlined,
  AlertOutlined,
  RobotOutlined,
  DollarOutlined,
  DesktopOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { visibleModules, useUi } from '@/store/ui';
import './Sidebar.css';

const { Sider } = Layout;
const { useBreakpoint } = Grid;

type MenuItem = Required<MenuProps>['items'][number];

type NavGroup = {
  key: string;
  label: string;
  paths: string[];
};

const ICONS: Record<string, ComponentType> = {
  DashboardOutlined,
  ApartmentOutlined,
  FundOutlined,
  ThunderboltOutlined,
  BugOutlined,
  AlertOutlined,
  RobotOutlined,
  DollarOutlined,
  DesktopOutlined,
  SettingOutlined,
};

const NAV_GROUPS: NavGroup[] = [
  {
    key: 'operations',
    label: '运营管理',
    paths: ['/assets', '/fdd', '/alarms', '/optimize'],
  },
  {
    key: 'analytics',
    label: '分析中心',
    paths: ['/energy', '/cost', '/ai'],
  },
  {
    key: 'presentation',
    label: '展示',
    paths: ['/bigscreen'],
  },
];

function BrandMark() {
  return (
    <svg width="34" height="34" viewBox="0 0 34 34" fill="none" aria-hidden="true">
      <defs>
        <linearGradient id="brand-gradient" x1="5" y1="4" x2="29" y2="30" gradientUnits="userSpaceOnUse">
          <stop stopColor="#12C9C0" />
          <stop offset="1" stopColor="#087E79" />
        </linearGradient>
      </defs>
      <rect x="1" y="1" width="32" height="32" rx="10" fill="url(#brand-gradient)" />
      <path d="M10 10.5V23.5M24 10.5V23.5M10 17H24" stroke="white" strokeWidth="2.2" strokeLinecap="round" />
      <path d="M7.5 12.5C10.3 8.8 14.2 7 18 7" stroke="white" strokeWidth="1.5" strokeLinecap="round" opacity="0.72" />
      <path d="M26.5 21.5C23.7 25.2 19.8 27 16 27" stroke="white" strokeWidth="1.5" strokeLinecap="round" opacity="0.72" />
      <circle cx="25.5" cy="11.5" r="1.5" fill="#CFFFFC" />
      <circle cx="8.5" cy="22.5" r="1.5" fill="#CFFFFC" />
    </svg>
  );
}

function buildItem(path: string, modules: ReturnType<typeof visibleModules>): MenuItem | null {
  const module = modules.find((item) => item.path === path);
  if (!module) return null;
  const Icon = ICONS[module.icon];
  return {
    key: module.path,
    icon: Icon ? <Icon /> : undefined,
    label: module.label,
  };
}

function compactItems(items: Array<MenuItem | null>): MenuItem[] {
  return items.filter(Boolean) as MenuItem[];
}

function groupItem(key: string, label: ReactNode, children: MenuItem[]): MenuItem {
  return { type: 'group', key, label, children } as MenuItem;
}

export default function Sidebar() {
  const navigate = useNavigate();
  const location = useLocation();
  const screens = useBreakpoint();
  const role = useUi((state) => state.role);
  const collapsed = useUi((state) => state.sidebarCollapsed);
  const setSidebarCollapsed = useUi((state) => state.setSidebarCollapsed);
  const mobile = screens.md === false;
  const menuCollapsed = mobile ? false : collapsed;

  useEffect(() => {
    if (mobile) setSidebarCollapsed(true);
  }, [mobile, setSidebarCollapsed]);

  const modules = useMemo(() => visibleModules(role), [role]);

  const primaryItems = useMemo<MenuItem[]>(() => {
    const dashboard = buildItem('/dashboard', modules);
    const groupChildren = NAV_GROUPS.map((group) => ({
      ...group,
      children: compactItems(group.paths.map((path) => buildItem(path, modules))),
    })).filter((group) => group.children.length > 0);

    if (menuCollapsed) {
      return compactItems([
        dashboard,
        ...groupChildren.flatMap((group) => group.children),
      ]);
    }

    return compactItems([
      dashboard,
      ...groupChildren.map((group) => groupItem(group.key, group.label, group.children)),
    ]);
  }, [menuCollapsed, modules]);

  const systemItems = useMemo<MenuItem[]>(() => compactItems([
    buildItem('/system', modules),
  ]), [modules]);

  const selectedKey = `/${location.pathname.split('/')[1] || 'dashboard'}`;
  const handleNavigate = (key: string) => {
    navigate(key === '/energy' ? '/energy/month' : key);
    if (mobile) setSidebarCollapsed(true);
  };

  const content = (
    <>
      <div className={`sidebar-brand ${menuCollapsed ? 'sidebar-brand-collapsed' : ''}`}>
        <BrandMark />
        {!menuCollapsed && (
          <div className="sidebar-brand-copy">
            <div className="sidebar-brand-title">HVAC 智慧能源</div>
            <div className="sidebar-brand-subtitle">SMART ENERGY</div>
          </div>
        )}
      </div>

      <div className="sidebar-navigation">
        <Menu
          mode="inline"
          inlineCollapsed={menuCollapsed}
          selectedKeys={[selectedKey]}
          items={primaryItems}
          onClick={({ key }) => handleNavigate(key)}
        />
      </div>

      {systemItems.length > 0 && (
        <div className="sidebar-system">
          {!menuCollapsed && <div className="sidebar-system-divider" />}
          <Menu
            mode="inline"
            inlineCollapsed={menuCollapsed}
            selectedKeys={[selectedKey]}
            items={systemItems}
            onClick={({ key }) => handleNavigate(key)}
          />
        </div>
      )}
    </>
  );

  if (mobile) {
    return (
      <Drawer
        placement="left"
        open={!collapsed}
        onClose={() => setSidebarCollapsed(true)}
        width={224}
        closable={false}
        rootClassName="app-sidebar-drawer"
        styles={{ body: { padding: 0 } }}
      >
        <div className="app-sidebar app-sidebar-mobile">{content}</div>
      </Drawer>
    );
  }

  return (
    <Sider
      className="app-sidebar"
      collapsible
      collapsed={collapsed}
      trigger={null}
      width={224}
      collapsedWidth={64}
    >
      {content}
    </Sider>
  );
}
