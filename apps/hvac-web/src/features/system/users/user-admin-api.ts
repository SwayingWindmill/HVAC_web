import { queryOptions } from '@tanstack/react-query';
import {
  PlatformApiError,
  createPlatformGatewayClient,
  type UserAdminCreateMemberRequest,
  type UserAdminProfileRequest,
  type UserAdminRoleAssignment,
} from '@/api/generated/platformGateway.gen';
import { passwordRefusal } from '@/features/account/password';

const client = createPlatformGatewayClient();
const withCsrf = (csrfToken: string): RequestInit => ({ headers: { 'X-CSRF-Token': csrfToken } });

export const userAdminKey = ['iam', 'members'] as const;

export const userAdminQuery = queryOptions({
  queryKey: userAdminKey,
  queryFn: async ({ signal }) => (await client.listUserAdminMembers({ signal })).data,
  retry: false,
});

/** Every write user administration makes, each carrying the session's CSRF token. */
export const userAdminApi = {
  create: (csrfToken: string, body: UserAdminCreateMemberRequest) => client.createUserAdminMember(body, withCsrf(csrfToken)),
  resetPassword: (csrfToken: string, principalId: string, password: string) => client.resetUserAdminMemberPassword(principalId, password, withCsrf(csrfToken)),
  setEnabled: (csrfToken: string, principalId: string, enabled: boolean) => client.setUserAdminMemberEnabled(principalId, enabled, withCsrf(csrfToken)),
  updateProfile: (csrfToken: string, principalId: string, body: UserAdminProfileRequest) => client.updateUserAdminMemberProfile(principalId, body, withCsrf(csrfToken)),
  assign: (csrfToken: string, principalId: string, body: UserAdminRoleAssignment) => client.assignUserAdminMemberRole(principalId, body, withCsrf(csrfToken)),
  revoke: (csrfToken: string, principalId: string, bindingId: string) => client.revokeUserAdminMemberRole(principalId, bindingId, withCsrf(csrfToken)),
};

const MESSAGES: Readonly<Record<string, string>> = {
  USERNAME_TAKEN: '这个用户名已经有人用了',
  IAM_ADMIN_SELF_LOCKOUT: '不能停用自己，也不能去掉自己的管理员角色',
  IAM_ADMIN_REVISION_CONFLICT: '这个用户刚被别人改过，请刷新后再试',
  IAM_ADMIN_FORBIDDEN: '只有管理员可以管理用户',
  USER_ADMIN_REQUEST_INVALID: '填写的内容不完整或格式不对',
  USER_ADMIN_UNAVAILABLE: '用户管理暂时不可用',
};

/** What to tell the administrator when a request is refused. */
export function userAdminErrorMessage(error: unknown): string {
  const refusal = passwordRefusal(error);
  if (refusal) return refusal;
  if (error instanceof PlatformApiError) return MESSAGES[error.problem.code] ?? '没有保存成功，请稍后再试';
  return '没有保存成功，请检查填写的内容';
}
