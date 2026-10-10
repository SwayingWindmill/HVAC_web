// Role keys are tenant configuration; operators see a name for the roles the product knows and never the raw key.

const ROLE_LABELS: Readonly<Record<string, string>> = {
  'platform-admin': '平台管理员',
  'local-admin': '管理员',
  admin: '管理员',
  administrator: '管理员',
  operator: '运维员',
  viewer: '查看员',
};

const UNNAMED_ROLE = '授权用户';

/** The names of a principal's roles, deduplicated; unknown roles share one generic name. */
export function roleLabels(roles: readonly string[]): string[] {
  const labels = roles.map((role) => ROLE_LABELS[role.trim().toLowerCase()] ?? UNNAMED_ROLE);
  return labels.length === 0 ? [UNNAMED_ROLE] : [...new Set(labels)];
}

/** The first role the product can name, for compact identity display. */
export function primaryRoleLabel(roles: readonly string[]): string {
  return roleLabels(roles).find((label) => label !== UNNAMED_ROLE) ?? UNNAMED_ROLE;
}
