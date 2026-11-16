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
