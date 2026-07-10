import { create } from 'zustand';

// Roles map 3-way to backend (per #5 RBAC): demo=READONLY, ops=MAINTENANCE, rd=ADMIN.
export type Role = 'demo' | 'ops' | 'rd';
export type ThemeMode = 'light' | 'dark' | 'system';

export interface ModuleDef {
  key: string;
  path: string;
  label: string;
  icon: string; // icon name resolved in Sidebar
  roles: Role[]; // who sees it in the sidebar
}

// Top-level IA (10 modules) from #2 resolution. Role visibility verbatim from the ticket.
export const MODULES: ModuleDef[] = [
  { key: 'dashboard', path: '/dashboard', label: '总览驾驶舱', icon: 'DashboardOutlined', roles: ['demo', 'ops', 'rd'] },
  { key: 'assets', path: '/assets', label: '设备与建筑', icon: 'ApartmentOutlined', roles: ['ops', 'rd'] },
  { key: 'energy', path: '/energy', label: '能耗分析', icon: 'FundOutlined', roles: ['rd'] },
  { key: 'optimize', path: '/optimize', label: '节能优化', icon: 'ThunderboltOutlined', roles: ['rd'] },
  { key: 'fdd', path: '/fdd', label: '故障检测', icon: 'BugOutlined', roles: ['ops', 'rd'] },
  { key: 'alarms', path: '/alarms', label: '报警工单', icon: 'AlertOutlined', roles: ['ops', 'rd'] },
  { key: 'ai', path: '/ai', label: 'AI 运维助手', icon: 'RobotOutlined', roles: ['rd'] },
  { key: 'cost', path: '/cost', label: '成本与绩效', icon: 'DollarOutlined', roles: ['rd'] },
  { key: 'bigscreen', path: '/bigscreen', label: '演示大屏', icon: 'DesktopOutlined', roles: ['demo', 'ops', 'rd'] },
  { key: 'system', path: '/system', label: '系统管理', icon: 'SettingOutlined', roles: ['rd'] },
];

export const ROLE_LABEL: Record<Role, string> = {
  demo: '演示/汇报',
  ops: '安装/运维',
  rd: '内部研发',
};

interface UiState {
  role: Role;
  themeMode: ThemeMode;
  demoMode: boolean; // demo data overlay (separate from role; any role can flip it)
  sidebarCollapsed: boolean;
  buildingId: string; // global context (X-Site-Id analogue)
  setRole: (r: Role) => void;
  setThemeMode: (m: ThemeMode) => void;
  toggleDemoMode: () => void;
  toggleSidebar: () => void;
  setSidebarCollapsed: (collapsed: boolean) => void;
  setBuilding: (id: string) => void;
}

export const useUi = create<UiState>((set) => ({
  role: 'rd',
  themeMode: 'system',
  demoMode: true,
  sidebarCollapsed: false,
  buildingId: 'b1',
  setRole: (role) => set({ role }),
  setThemeMode: (themeMode) => set({ themeMode }),
  toggleDemoMode: () => set((s) => ({ demoMode: !s.demoMode })),
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  setSidebarCollapsed: (sidebarCollapsed) => set({ sidebarCollapsed }),
  setBuilding: (buildingId) => set({ buildingId }),
}));

export const visibleModules = (role: Role): ModuleDef[] =>
  MODULES.filter((m) => m.roles.includes(role));
