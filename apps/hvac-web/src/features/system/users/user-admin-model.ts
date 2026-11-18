import type { BuiltInRoleKey, UserAdminMember } from '../../../api/generated/platformGateway.gen.ts';

export type MemberTwoFactorDisplay =
  | { readonly kind: 'enabled'; readonly label: '已开通' }
  | { readonly kind: 'missing_for_high_risk'; readonly label: '未开通'; readonly warning: '提交不了高风险指令' }
  | { readonly kind: 'not_enabled'; readonly label: '未开通' };

/** Built-in Roles whose capability set includes high-risk or critical command submission (026-iam-role-assignments.sql). */
export function roleSubmitsHighRiskCommands(roleKey: BuiltInRoleKey): boolean {
  switch (roleKey) {
    case 'admin':
    case 'site-manager':
      return true;
    case 'operator':
    case 'viewer':
      return false;
    default: {
      const _exhaustive: never = roleKey;
      return _exhaustive;
    }
  }
}

/** Enabled members lose all effective capabilities immediately upon disablement (ADR 0022). */
export function memberSubmitsHighRiskCommands(member: Pick<UserAdminMember, 'enabled' | 'assignments'>): boolean {
  return member.enabled && member.assignments.some((assignment) => roleSubmitsHighRiskCommands(assignment.roleKey));
}

/** Resolves the operator-facing two-step verification status and missing-enrollment risk note for one directory member. */
export function resolveMemberTwoFactorDisplay(
  member: Pick<UserAdminMember, 'enabled' | 'mfaEnabled' | 'assignments'>,
): MemberTwoFactorDisplay {
  if (member.mfaEnabled) {
    return { kind: 'enabled', label: '已开通' };
  }
  if (memberSubmitsHighRiskCommands(member)) {
    return { kind: 'missing_for_high_risk', label: '未开通', warning: '提交不了高风险指令' };
  }
  return { kind: 'not_enabled', label: '未开通' };
}


export type AssignmentValidityPreset = 'permanent' | '4h' | '24h' | '7d' | 'custom';

export interface AssignmentValidityOption {
  readonly value: AssignmentValidityPreset;
  readonly label: string;
}

export const ASSIGNMENT_VALIDITY_OPTIONS: readonly AssignmentValidityOption[] = [
  { value: 'permanent', label: '长期有效' },
  { value: '4h', label: '临时 4 小时' },
  { value: '24h', label: '临时 24 小时' },
  { value: '7d', label: '临时 7 天' },
  { value: 'custom', label: '自定义到期时间' },
];

export function defaultCustomValidToInput(nowMs = Date.now()): string {
  const target = new Date(nowMs + 4 * 3_600_000);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${target.getFullYear()}-${pad(target.getMonth() + 1)}-${pad(target.getDate())}T${pad(target.getHours())}:${pad(target.getMinutes())}`;
}

export function resolveAssignmentValidTo(
  preset: AssignmentValidityPreset,
  customLocalDateTime: string,
  nowMs = Date.now(),
): { readonly validTo?: string; readonly error?: string } {
  if (preset === 'permanent') return {};
  if (preset === '4h') return { validTo: new Date(nowMs + 4 * 3_600_000).toISOString() };
  if (preset === '24h') return { validTo: new Date(nowMs + 24 * 3_600_000).toISOString() };
  if (preset === '7d') return { validTo: new Date(nowMs + 7 * 24 * 3_600_000).toISOString() };

  const trimmed = customLocalDateTime.trim();
  if (!trimmed) return { error: '请选择到期时间。' };
  const parsed = Date.parse(trimmed);
  if (!Number.isFinite(parsed)) return { error: '到期时间格式无效。' };
  if (parsed <= nowMs) return { error: '到期时间必须晚于当前时间。' };
  return { validTo: new Date(parsed).toISOString() };
}

export function formatAssignmentValidity(
  validTo?: string | null,
): { readonly label: string; readonly temporary: boolean } {
  if (!validTo) return { label: '长期', temporary: false };
  const parsed = Date.parse(validTo);
  if (!Number.isFinite(parsed)) return { label: '临时', temporary: true };
  const formatted = new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(parsed));
  return { label: '至 ' + formatted, temporary: true };
}
