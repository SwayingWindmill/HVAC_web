import type { ReactNode } from 'react';
import {
  AlertOutlined,
  ApartmentOutlined,
  BugOutlined,
  ControlOutlined,
  DashboardOutlined,
  DesktopOutlined,
  DollarOutlined,
  FundOutlined,
  RobotOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { ProLayout, type MenuDataItem } from '@ant-design/pro-components';
import { Outlet, useLocation, useNavigate } from 'react-router';
import GlobalAiAssistant from '@/ai/GlobalAiAssistant';
import { visibleModules, useUi, type Role } from '@/store/ui';
import { DemoHeaderActions, DemoHeaderContent } from './Header';

const NAVIGATION_ICONS: Record<string, ReactNode> = {
  DashboardOutlined: <DashboardOutlined />,
  ApartmentOutlined: <ApartmentOutlined />,
  ControlOutlined: <ControlOutlined />,
  FundOutlined: <FundOutlined />,
  ThunderboltOutlined: <ThunderboltOutlined />,
  BugOutlined: <BugOutlined />,
  AlertOutlined: <AlertOutlined />,
  RobotOutlined: <RobotOutlined />,
  DollarOutlined: <DollarOutlined />,
  DesktopOutlined: <DesktopOutlined />,
  SettingOutlined: <SettingOutlined />,
};

const NAVIGATION_GROUPS = [
  { key: 'operations', name: '运营管理', paths: ['/assets', '/commands', '/fdd', '/alarms', '/optimize'] },
  { key: 'analytics', name: '分析中心', paths: ['/energy', '/cost', '/ai'] },
  { key: 'presentation', name: '展示', paths: ['/bigscreen'] },
] as const;

function buildDemoMenu(role: Role): MenuDataItem[] {
  const modules = visibleModules(role);
  const byPath = new Map(modules.map((module) => [module.path, module]));
  const item = (path: string): MenuDataItem | null => {
    const module = byPath.get(path);
    if (!module) return null;
    return { key: module.path, path: module.path, name: module.label, icon: NAVIGATION_ICONS[module.icon] };
  };
  const dashboard = item('/dashboard');
  const groupedPaths = new Set<string>(NAVIGATION_GROUPS.flatMap((group) => [...group.paths]));
  const groups: MenuDataItem[] = NAVIGATION_GROUPS.map((group) => ({
    key: group.key,
    name: group.name,
    children: group.paths.map(item).filter((entry): entry is MenuDataItem => Boolean(entry)),
  })).filter((group) => (group.children?.length ?? 0) > 0);
  const ungrouped = modules
    .filter((module) => module.path !== '/dashboard' && module.path !== '/system' && !groupedPaths.has(module.path))
    .map((module) => item(module.path))
    .filter((entry): entry is MenuDataItem => Boolean(entry));
  const system = item('/system');
  if (system) groups.push({ key: 'system-group', name: '系统', children: [system] });
  return [...(dashboard ? [dashboard] : []), ...ungrouped, ...groups];
}

export default function AppShell() {
  const location = useLocation();
  const navigate = useNavigate();
  const role = useUi((state) => state.role);
  const collapsed = useUi((state) => state.sidebarCollapsed);
  const setCollapsed = useUi((state) => state.setSidebarCollapsed);
  const isAiWorkspace = location.pathname === '/ai';
  const selectedPath = `/${location.pathname.split('/')[1] || 'dashboard'}`;
  const menuItems = buildDemoMenu(role);

  return (
    <div style={{ minHeight: '100vh' }}>
      <ProLayout
        title="泉来禾智慧能源"
        logo="/quanlaihe-mark.svg"
        layout="side"
        siderWidth={224}
        fixedHeader
        fixSiderbar
        breakpoint="lg"
        collapsed={collapsed}
        onCollapse={setCollapsed}
        location={{ pathname: selectedPath }}
        route={{ path: '/', routes: menuItems }}
        menuItemRender={(item, dom) => {
          if (!item.path) return dom;
          return (
            <a
              href={item.path}
              onClick={(event) => {
                event.preventDefault();
                navigate(item.path === '/energy' ? '/energy/month' : item.path!);
              }}
            >
              {dom}
            </a>
          );
        }}
        headerContentRender={() => <DemoHeaderContent />}
        actionsRender={() => [<DemoHeaderActions key="demo-header-actions" />]}
        contentStyle={{ margin: 0, padding: 0, minHeight: 0 }}
      >
        <main
          className={isAiWorkspace ? 'app-content app-content-ai' : 'app-content'}
          style={{
            minWidth: 0,
            minHeight: isAiWorkspace ? 0 : 'calc(100vh - 56px)',
            height: isAiWorkspace ? 'calc(100vh - 56px)' : 'auto',
            boxSizing: 'border-box',
            padding: isAiWorkspace ? 0 : '20px 20px 88px',
            overflow: isAiWorkspace ? 'hidden' : 'visible',
          }}
        >
          <Outlet />
        </main>
        <GlobalAiAssistant />
      </ProLayout>
    </div>
  );
}
