import { useEffect, useMemo, type ComponentType, type ReactNode } from 'react';
import { Grid } from 'antd';
import {
  DashboardOutlined,
  ApartmentOutlined,
  ControlOutlined,
  FundOutlined,
  ThunderboltOutlined,
  BugOutlined,
  AlertOutlined,
  RobotOutlined,
  DollarOutlined,
  DesktopOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router';
import { visibleModules, useUi } from '@/store/ui';
import { ProductSidebar, type ProductMenuItem } from './ProductSidebar';

export { ProductSidebar } from './ProductSidebar';
export type { ProductMenuItem, ProductSidebarProps } from './ProductSidebar';

const { useBreakpoint } = Grid;
type MenuItem = ProductMenuItem;

type NavGroup = {
  key: string;
  label: string;
  paths: string[];
};

const ICONS: Record<string, ComponentType> = {
  DashboardOutlined,
  ApartmentOutlined,
  ControlOutlined,
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
    paths: ['/assets', '/commands', '/fdd', '/alarms', '/optimize'],
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

  return (
    <ProductSidebar
      collapsed={collapsed}
      mobile={mobile}
      primaryItems={primaryItems}
      systemItems={systemItems}
      selectedKey={selectedKey}
      onNavigate={handleNavigate}
      onClose={() => setSidebarCollapsed(true)}
    />
  );
}
