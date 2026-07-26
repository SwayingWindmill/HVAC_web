import type { Role } from '@/store/ui';

export type PermissionAction =
  | 'view'
  | 'create'
  | 'update'
  | 'manage'
  | 'transition'
  | 'approve'
  | 'reject'
  | 'dispatch'
  | 'simulate';

export type PermissionSubject =
  | 'dashboard'
  | 'assets'
  | 'commands'
  | 'energy'
  | 'optimize'
  | 'fdd'
  | 'alarms'
  | 'ai'
  | 'cost'
  | 'bigscreen'
  | 'system'
  | 'asset'
  | 'command'
  | 'workOrder'
  | 'diagnosis'
  | 'optimization'
  | 'costReport'
  | 'systemConfig';

type PermissionRule = {
  actions: PermissionAction[];
  subjects: PermissionSubject[];
};

const ROLE_RULES: Record<Role, PermissionRule[]> = {
  demo: [
    { actions: ['view'], subjects: ['dashboard', 'bigscreen'] },
  ],
  ops: [
    { actions: ['view'], subjects: ['dashboard', 'assets', 'commands', 'fdd', 'alarms', 'bigscreen', 'asset', 'command', 'diagnosis', 'workOrder'] },
    { actions: ['create'], subjects: ['workOrder', 'command'] },
    { actions: ['transition'], subjects: ['workOrder'] },
    { actions: ['approve'], subjects: ['command'] },
  ],
  rd: [
    {
      actions: ['view'],
      subjects: [
        'dashboard', 'assets', 'commands', 'energy', 'optimize', 'fdd', 'alarms', 'ai', 'cost', 'bigscreen', 'system',
        'asset', 'command', 'diagnosis', 'workOrder', 'optimization', 'costReport', 'systemConfig',
      ],
    },
    { actions: ['create', 'update', 'manage'], subjects: ['asset', 'systemConfig'] },
    { actions: ['create', 'transition'], subjects: ['workOrder'] },
    { actions: ['create', 'approve'], subjects: ['command'] },
    { actions: ['approve', 'reject', 'dispatch', 'simulate', 'update'], subjects: ['optimization'] },
  ],
};

export function can(role: Role, action: PermissionAction, subject: PermissionSubject): boolean {
  return ROLE_RULES[role].some((rule) =>
    rule.actions.includes(action) && rule.subjects.includes(subject),
  );
}

export function cannot(role: Role, action: PermissionAction, subject: PermissionSubject): boolean {
  return !can(role, action, subject);
}

export function routeSubjectFromModuleKey(key: string): PermissionSubject | null {
  const known = [
    'dashboard', 'assets', 'commands', 'energy', 'optimize', 'fdd', 'alarms', 'ai', 'cost', 'bigscreen', 'system',
  ] as const;
  return (known as readonly string[]).includes(key) ? (key as PermissionSubject) : null;
}

export function routeSubjectFromPath(path: string): PermissionSubject | null {
  const normalized = `/${path.replace(/^\//, '').split('/')[0] || 'dashboard'}`;
  const mapping: Record<string, PermissionSubject> = {
    '/dashboard': 'dashboard',
    '/assets': 'assets',
    '/commands': 'commands',
    '/energy': 'energy',
    '/optimize': 'optimize',
    '/fdd': 'fdd',
    '/alarms': 'alarms',
    '/ai': 'ai',
    '/cost': 'cost',
    '/bigscreen': 'bigscreen',
    '/system': 'system',
  };
  return mapping[normalized] ?? null;
}

export function canViewPath(role: Role, path: string): boolean {
  const subject = routeSubjectFromPath(path);
  return subject ? can(role, 'view', subject) : true;
}

export function readonlyHint(role: Role, subject: PermissionSubject): string {
  if (role === 'demo') return '当前为演示角色，只允许查看演示驾驶舱和大屏。';
  if (subject === 'optimization') return '当前角色不可审批或下发优化策略。';
  if (subject === 'command') return '当前角色不可提交或审批设备 Command。';
  if (subject === 'workOrder') return '当前角色不可流转工单。';
  if (subject === 'asset') return '当前角色不可维护资产台账。';
  return '当前角色无该操作权限。';
}
