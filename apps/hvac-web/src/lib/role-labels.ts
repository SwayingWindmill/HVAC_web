// The built-in Roles (ADR 0022); operators see their names and never the raw key.

const ROLE_LABELS: Readonly<Record<string, string>> = {
  admin: '管理员',
  'site-manager': '运维主管',
  operator: '运维人员',
  viewer: '只读',
};

/** What each built-in Role may do, in one line, lowest first. */
export const ROLE_SUMMARIES: readonly { readonly key: string; readonly summary: string }[] = [
  { key: 'viewer', summary: '查看站点、实时与历史数据、能耗、告警和工单' },
  { key: 'operator', summary: '另外可以确认告警、处理工单，下发中风险以下的指令' },
  { key: 'site-manager', summary: '另外可以分派告警和工单、管理节能项目，下发和审批高风险以下的指令' },
  { key: 'admin', summary: '全部权限，包括编辑资产与点位和管理用户；只能授予全部站点' },
];

const UNNAMED_ROLE = '授权用户';

export function roleLabel(role: string): string {
  return ROLE_LABELS[role.trim().toLowerCase()] ?? UNNAMED_ROLE;
}

/** The names of a principal's roles, deduplicated; unknown roles share one generic name. */
export function roleLabels(roles: readonly string[]): string[] {
  const labels = roles.map(roleLabel);
  return labels.length === 0 ? [UNNAMED_ROLE] : [...new Set(labels)];
}

/** The first role the product can name, for compact identity display. */
export function primaryRoleLabel(roles: readonly string[]): string {
  return roleLabels(roles).find((label) => label !== UNNAMED_ROLE) ?? UNNAMED_ROLE;
}
